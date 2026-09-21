// Gemini 原生生成入口的 HTTP 组合；非消费模型目录路由保持独立。
package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type GeminiNativeOptions struct{ MaxSwitches int }
type GeminiNativeCall struct {
	MessagesCall
	ModelName, Action                                        string
	Concurrency                                              *ConcurrencyHelper
	UseDigestFallback                                        bool
	DigestChain, PrefixHash, SessionUUID, MatchedDigestChain string
	SignatureState                                           requeststate.GeminiSignatureState
}
type GeminiNativeBackend interface {
	Access(*gin.Context) (*apikey.APIKey, bool)
	HasForcedPlatform(*gin.Context) bool
	SafeModelSegment(string) bool
	ObserveRequest(*gin.Context, string, bool)
	ObserveEndpoint(*gin.Context, bool)
	Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, string, []byte) *moderation.Decision
	Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan
	BindPlan(*gin.Context, routing.RoutePlan)
	BindErrors(*gin.Context)
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	Isolate(context.Context, *apikey.APIKey, int64, string) error
	CachedSession(context.Context, *int64, string) (int64, error)
	Prefetch(*gin.Context, int64, int64)
	DigestChain(*protocolgemini.GeminiRequest) string
	PrefixHash(int64, int64, string, string, string, string) string
	FindSession(context.Context, int64, string, string) (string, int64, string, bool)
	DigestSessionKey(string, string) string
	BindSticky(context.Context, *int64, string, int64) error
	FailoverObservation(context.Context, string, map[string]any)
}
type GeminiNativeHandler struct {
	executor execution.Executor
	requestLifetime

	options     GeminiNativeOptions
	backend     GeminiNativeBackend
	prompt      MessagesPrompt
	concurrency *ConcurrencyHelper
	newID       func() string
}

func NewGeminiNativeHandler(options GeminiNativeOptions, backend GeminiNativeBackend, prompt MessagesPrompt, concurrency *ConcurrencyHelper, newID func() string, executor execution.Executor) *GeminiNativeHandler {
	return &GeminiNativeHandler{executor: executor, options: options, backend: backend, prompt: prompt, concurrency: concurrency, newID: newID}
}

// WriteGoogleError 保留原 Google JSON envelope，不执行认证或资金检查。
func WriteGoogleError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": status, "message": message, "status": HTTPStatusToGoogleStatus(status)}})
}
func geminiNativeIsolationError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		WriteGoogleError(c, 403, session.SessionIsolationConflictMessage)
	} else {
		WriteGoogleError(c, 503, "Service temporarily unavailable")
	}
	return true
}

var GeminiCLITmpDirRegex = regexp.MustCompile(`/\.gemini/tmp/([A-Fa-f0-9]{64})`)

type GeminiPathParseError struct{ message string }

func (e *GeminiPathParseError) Error() string { return e.message }

// @project-doc docs/interfaces/gemini_upstream.md#gemini_protocol_dispatch
func (h *GeminiNativeHandler) GeminiV1BetaModels(c *gin.Context) {
	done, accepted := h.beginRequest(c, "google")
	if !accepted {
		return
	}
	defer done()

	apiKey, ok := h.backend.Access(c)
	if !ok || apiKey == nil {
		WriteGoogleError(c, http.StatusUnauthorized, "Invalid API key")
		return
	}
	authSubject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		WriteGoogleError(c, http.StatusInternalServerError, "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.gemini_v1beta.models",
		zap.Int64("user_id", authSubject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// 检查平台：优先使用强制平台（/antigravity 路由，中间件已设置 request.Context），否则要求 gemini 分组
	if !h.backend.HasForcedPlatform(c) {
		if apiKey.Group == nil || apiKey.Group.Platform != capability.PlatformGemini {
			WriteGoogleError(c, http.StatusBadRequest, "API key group platform is not gemini")
			return
		}
	}

	modelName, action, err := ParseGeminiModelAction(strings.TrimPrefix(c.Param("modelAction"), "/"))
	if err != nil {
		WriteGoogleError(c, http.StatusNotFound, err.Error())
		return
	}
	// URL 里的模型名最终会被拼进上游 /v1beta/models/{model}:{action}，
	// 先在入口通过端口校验片段合规性。
	if !h.backend.SafeModelSegment(modelName) {
		WriteGoogleError(c, http.StatusBadRequest, "Invalid model in URL")
		return
	}

	stream := action == "streamGenerateContent"
	reqLog = reqLog.With(zap.String("model", modelName), zap.String("action", action), zap.Bool("stream", stream))

	body, err := ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := compatibleMaxBytesError(err); ok {
			WriteGoogleError(c, http.StatusRequestEntityTooLarge, BodyTooLargeMessage(maxErr.Limit))
			return
		}
		WriteGoogleError(c, http.StatusBadRequest, "Failed to read request body")
		return
	}
	if len(body) == 0 {
		WriteGoogleError(c, http.StatusBadRequest, "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, modelName, stream)
	h.backend.ObserveEndpoint(c, stream)
	// 用户提示词替换必须早于内容审计、会话 hash 和转发，避免审计与上游请求不一致。
	body = h.prompt.ApplyUserPromptReplacement(c.Request.Context(), body, "gemini")

	if decision := h.backend.Moderate(c, reqLog, apiKey, authSubject, modelName, body); decision != nil && decision.Blocked {
		WriteGoogleError(c, ModerationHTTPStatus(decision), decision.Message)
		return
	}

	// 解析渠道级模型映射
	// 当前分组和渠道结果进入独立计划，不改变原解析位置。
	channelMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, modelName)
	channelMapping := channelMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, channelMappingRoutePlan)
	reqModel := modelName // 保存映射前的原始模型名
	if channelMapping.Mapped {
		modelName = channelMapping.MappedModel
	}

	// 读取可为空的订阅投影
	subscription, _ := SubscriptionFromContext(c)

	// Gemini 原生请求不发送 Claude 心跳帧。
	geminiConcurrency := NewConcurrencyHelper(h.concurrency.Service(), SSEPingFormatNone, 0)

	// 1）获取用户并发槽
	streamStarted := false
	h.backend.BindErrors(c)
	userReleaseFunc, err := geminiConcurrency.AcquireUserSlotWithWait(c, authSubject.UserID, authSubject.Concurrency, stream, &streamStarted)
	if err != nil {
		reqLog.Warn("gemini.user_slot_acquire_failed", zap.Error(err))
		WriteGoogleError(c, http.StatusTooManyRequests, err.Error())
		return
	}
	// 确保请求取消时也会释放槽位，避免长连接被动中断造成泄漏
	userReleaseFunc = scheduler.WrapRelease(c.Request.Context(), scheduler.ReleaseOnCancel, userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2）等待后复查资金资格
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("gemini.billing_eligibility_check_failed", zap.Error(err))
		status, _, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		WriteGoogleError(c, status, message)
		return
	}

	// 3）根据请求内容准备粘性会话选择
	// 优先使用 Gemini CLI 的会话标识（privileged-user-id + tmp 目录哈希）
	sessionHash := ExtractGeminiCLISessionHash(c, body)
	explicitGeminiCLISession := sessionHash != ""
	if sessionHash == "" {
		// Fallback: 使用通用的会话哈希生成逻辑（适用于其他客户端）
		parsedReq, _ := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformGemini)
		if parsedReq != nil {
			parsedReq.SessionContext = &requeststate.SessionContext{
				ClientIP:  clientip.GetClientIP(c),
				UserAgent: c.GetHeader("User-Agent"),
				APIKeyID:  apiKey.ID,
			}
		}
		sessionHash = session.GenerateSessionHash(parsedReq, slog.Info)
	}
	sessionKey := sessionHash
	if sessionHash != "" {
		sessionKey = "gemini:" + sessionHash
	}
	if explicitGeminiCLISession {
		if err := h.backend.Isolate(c.Request.Context(), apiKey, authSubject.UserID, sessionKey); geminiNativeIsolationError(c, err) {
			return
		}
	}

	// 查询粘性会话绑定的账号 ID（用于检测账号切换）
	var sessionBoundAccountID int64
	if sessionKey != "" {
		sessionBoundAccountID, _ = h.backend.CachedSession(c.Request.Context(), apiKey.GroupID, sessionKey)
		if sessionBoundAccountID > 0 {
			prefetchedGroupID := int64(0)
			if apiKey.GroupID != nil {
				prefetchedGroupID = *apiKey.GroupID
			}
			h.backend.Prefetch(c, sessionBoundAccountID, prefetchedGroupID)
		}
	}

	// === Gemini 内容摘要会话 Fallback 逻辑 ===
	// 当原有会话标识无效时（sessionBoundAccountID == 0），尝试基于内容摘要链匹配
	var geminiDigestChain string
	var geminiPrefixHash string
	var geminiSessionUUID string
	var matchedDigestChain string
	useDigestFallback := sessionBoundAccountID == 0

	if useDigestFallback {
		// 解析 Gemini 请求体
		var geminiReq protocolgemini.GeminiRequest
		if err := json.Unmarshal(body, &geminiReq); err == nil && len(geminiReq.Contents) > 0 {
			// 生成摘要链
			geminiDigestChain = h.backend.DigestChain(&geminiReq)
			if geminiDigestChain != "" {
				// 生成前缀 hash
				userAgent := c.GetHeader("User-Agent")
				clientIP := clientip.GetClientIP(c)
				platform := ""
				if apiKey.Group != nil {
					platform = apiKey.Group.Platform
				}
				geminiPrefixHash = h.backend.PrefixHash(
					authSubject.UserID,
					apiKey.ID,
					clientIP,
					userAgent,
					platform,
					modelName,
				)

				// 查找会话
				foundUUID, foundAccountID, foundMatchedChain, found := h.backend.FindSession(
					c.Request.Context(),
					geminiGroupID(apiKey.GroupID),
					geminiPrefixHash,
					geminiDigestChain,
				)
				if found {
					matchedDigestChain = foundMatchedChain
					sessionBoundAccountID = foundAccountID
					geminiSessionUUID = foundUUID
					reqLog.Info("gemini.digest_fallback_matched",
						zap.String("session_uuid_prefix", GeminiShortPrefix(foundUUID, 8)),
						zap.Int64("account_id", foundAccountID),
						zap.String("digest_chain", TruncateGeminiDigestChain(geminiDigestChain)),
					)

					// 关键：如果原 sessionKey 为空，使用 prefixHash + uuid 作为 sessionKey
					// 这样 SelectAccountWithLoadAwareness 的粘性会话逻辑会优先使用匹配到的账号
					if sessionKey == "" {
						sessionKey = h.backend.DigestSessionKey(geminiPrefixHash, foundUUID)
					}
					_ = h.backend.BindSticky(c.Request.Context(), apiKey.GroupID, sessionKey, foundAccountID)
				} else {
					// 生成新的会话 UUID
					geminiSessionUUID = h.newID()
					// 为新会话也生成 sessionKey（用于后续请求的粘性会话）
					if sessionKey == "" {
						sessionKey = h.backend.DigestSessionKey(geminiPrefixHash, geminiSessionUUID)
					}
				}
			}
		}
	}

	// 判断是否真的绑定了粘性会话：有 sessionKey 且已经绑定到某个账号
	hasBoundSession := sessionKey != "" && sessionBoundAccountID > 0

	call := GeminiNativeCall{
		MessagesCall: MessagesCall{
			Key:             apiKey,
			Subject:         authSubject,
			Subscription:    subscription,
			Body:            body,
			Model:           reqModel,
			Stream:          stream,
			HasBoundSession: hasBoundSession,
			SessionKey:      sessionKey,
			BoundAccountID:  sessionBoundAccountID,
			StreamStarted:   &streamStarted,
			Log:             reqLog,
			Route:           channelMappingRoutePlan,
			Mapping:         channelMapping,
		},
		ModelName:          modelName,
		Action:             action,
		Concurrency:        geminiConcurrency,
		UseDigestFallback:  useDigestFallback,
		DigestChain:        geminiDigestChain,
		PrefixHash:         geminiPrefixHash,
		SessionUUID:        geminiSessionUUID,
		MatchedDigestChain: matchedDigestChain,
		SignatureState: requeststate.GeminiSignatureState{
			BoundAccountID: sessionBoundAccountID,
		},
	}
	request := execution.Request{
		Hints:       requeststate.ExecutionHintsFromContext(c.Request.Context()),
		Routing:     requeststate.RoutingStateFromContext(c.Request.Context()),
		Route:       call.Route,
		UserID:      call.Subject.UserID,
		Concurrency: call.Subject.Concurrency,
		Stream:      call.Stream,
		Body:        call.Body,
		Model:       call.Model,
		Funding: execution.FundingState{
			Key:          call.Key,
			Subscription: call.Subscription,
		},
		SessionHash: call.SessionKey,

		Text: execution.TextState{Kind: execution.TextNativeGemini, Platform: call.Platform, BoundAccountID: call.BoundAccountID, HasBoundSession: call.HasBoundSession, GeminiModel: call.ModelName, Action: call.Action, UseDigestFallback: call.UseDigestFallback, DigestChain: call.DigestChain, PrefixHash: call.PrefixHash, SessionUUID: call.SessionUUID, MatchedDigestChain: call.MatchedDigestChain, SignatureState: call.SignatureState, Mapping: call.Mapping}}
	output := &MessagesOutput{ResponseSink: ResponseSink{Writer: c.Writer}, HTTP: c, Log: call.Log, StreamStarted: call.StreamStarted, Concurrency: call.Concurrency}
	_, _ = h.executor.Execute(c.Request.Context(), request, output)
}

func ParseGeminiModelAction(rest string) (model string, action string, err error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", &GeminiPathParseError{"missing path"}
	}

	// 标准路径：{model}:{action}
	if i := strings.Index(rest, ":"); i > 0 && i < len(rest)-1 {
		return rest[:i], rest[i+1:], nil
	}

	// 兼容路径：{model}/{action}
	if i := strings.Index(rest, "/"); i > 0 && i < len(rest)-1 {
		return rest[:i], rest[i+1:], nil
	}

	return "", "", &GeminiPathParseError{"invalid model action path"}
}

func ExtractGeminiCLISessionHash(c *gin.Context, body []byte) string {
	// 1. 从请求体中提取 tmp 目录哈希
	match := GeminiCLITmpDirRegex.FindSubmatch(body)
	if len(match) < 2 {
		return "" // 没有找到 tmp 目录，不使用粘性会话
	}
	tmpDirHash := string(match[1])

	// 2. 提取 privileged-user-id
	privilegedUserID := strings.TrimSpace(c.GetHeader("x-gemini-api-privileged-user-id"))

	// 3. 组合生成最终的 session hash
	if privilegedUserID != "" {
		// 组合两个标识符：privileged-user-id + tmp 目录哈希
		combined := privilegedUserID + ":" + tmpDirHash
		hash := sha256.Sum256([]byte(combined))
		return hex.EncodeToString(hash[:])
	}

	// 如果没有 privileged-user-id，直接使用 tmp 目录哈希
	return tmpDirHash
}

func TruncateGeminiDigestChain(chain string) string {
	if len(chain) <= 50 {
		return chain
	}
	return chain[:50] + "..."
}

func GeminiShortPrefix(value string, n int) string {
	if n <= 0 || len(value) <= n {
		return value
	}
	return value[:n]
}

func geminiGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}
