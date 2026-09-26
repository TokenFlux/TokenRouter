// Messages HTTP 适配拥有读写与错误顺序，核心文本循环不依赖 Gin。
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type MessagesHTTPOptions struct {
	MaxBodyBytes                   int64
	MaxSwitches, MaxGeminiSwitches int
}
type MessagesPrompt interface {
	ApplyUserPromptReplacementToBody(context.Context, []byte, string) []byte
}

// MessagesCall 不携带旧实体或完整配置，单次请求的派生值不会写入共享缓存。
type MessagesCall struct {
	Key                                      *apikey.APIKey
	Subject                                  authctx.AuthSubject
	Subscription                             *billing.UserSubscription
	Parsed                                   *requeststate.ParsedRequest
	Body, GeminiBody                         []byte
	Model, GeminiModel, Platform, SessionKey string
	Stream, ClaudeCode, HasBoundSession      bool
	BoundAccountID                           int64
	StreamStarted                            *bool
	Log                                      *zap.Logger
	Route                                    routing.RoutePlan
	Mapping                                  routing.GroupMappingResult
}

// MessagesBackend 只绑定已有用例的单步能力及同步观测，不另建循环或缓存。
type MessagesBackend interface {
	Access(*gin.Context) (*apikey.APIKey, bool)
	CompatibilityMetrics(*zap.Logger)
	ObserveRequest(*gin.Context, string, bool)
	ObserveEndpoint(*gin.Context, bool)
	Reasoning(*gin.Context, *apikey.APIKey, []byte) ([]byte, bool, error)
	PolicyDenied(*gin.Context)
	Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan
	BindPlan(*gin.Context, routing.RoutePlan)
	BindProbe(*gin.Context)
	Probe(*gin.Context) bool
	BindClient(*gin.Context, ClientDetection)
	BindThinking(*gin.Context, bool)
	ClientVersion(*gin.Context) string
	ClientVersionBounds(context.Context) (string, string)
	Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, string, []byte) *moderation.Decision
	BindErrors(*gin.Context)
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	ForcedPlatform(*gin.Context) (string, bool)
	Isolate(context.Context, *apikey.APIKey, int64, string) error
	CachedSession(context.Context, *int64, string) (int64, error)
	Prefetch(*gin.Context, int64, int64)
	PrepareGemini(context.Context, MessagesCall) (*requeststate.ParsedRequest, error)
	MarkStream(*gin.Context, string, string, int)
	FailoverObservation(context.Context, string, map[string]any)
}

// MessagesHandler 的依赖在 app 一次绑定；每次调用只创建本请求数据。
type MessagesHandler struct {
	executor execution.Executor
	requestLifetime

	options     MessagesHTTPOptions
	backend     MessagesBackend
	prompt      MessagesPrompt
	concurrency *ConcurrencyHelper
}

func NewMessagesHandler(options MessagesHTTPOptions, backend MessagesBackend, prompt MessagesPrompt, concurrency *ConcurrencyHelper, executor execution.Executor) *MessagesHandler {
	return &MessagesHandler{options: options, backend: backend, prompt: prompt, concurrency: concurrency, executor: executor}
}

// Messages handles Claude API compatible messages endpoint
// POST /v1/messages
func (h *MessagesHandler) Messages(c *gin.Context) {
	done, accepted := h.beginRequest(c, "anthropic")
	if !accepted {
		return
	}
	defer done()

	// 从context获取apiKey和user（ApiKeyAuth中间件已设置）
	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.gateway.messages",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	defer h.backend.CompatibilityMetrics(reqLog)

	// 读取请求体
	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, "", false)

	// 用户提示词替换必须早于解析、内容审计和会话 hash，确保后续链路看到同一份请求体。
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "anthropic_messages")

	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, err := requeststate.ParseGatewayRequest(bodyRef, capability.PlatformAnthropic)
	if err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	body = parsedReq.Body.Bytes()
	reqModel := parsedReq.Model
	if policyBody, changed, policyErr := h.backend.Reasoning(c, apiKey, body); policyErr != nil {
		h.backend.PolicyDenied(c)
		h.errorResponse(c, http.StatusForbidden, "permission_error", policyErr.Error())
		return
	} else if changed {
		if err := parsedReq.ReplaceBody(policyBody); err != nil {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to apply reasoning effort policy")
			return
		}
		body = parsedReq.Body.Bytes()
	}
	reqStream := parsedReq.Stream
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	// 解析分组模型映射
	// 当前分组和分组映射结果进入独立计划，不改变原解析位置。
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)

	// 设置 max_tokens=1 + haiku 探测请求标识到 context 中
	// 必须在 SetClaudeCodeClientContext 之前设置，因为 ClaudeCodeValidator 需要读取此标识进行绕过判断
	if clientmeta.IsHaikuProbe(reqModel, parsedReq.MaxTokens) {
		h.backend.BindProbe(c)
	}

	// 检查是否为 Claude Code 客户端，设置到 context 中（复用已解析请求，避免二次反序列化）。
	detection := DetectClaudeCodeRequest(c, body, parsedReq, h.backend.Probe(c))
	h.backend.BindClient(c, detection)
	isClaudeCodeClient := detection.ClaudeCode

	// 版本检查：仅对 Claude Code 客户端，拒绝低于最低版本的请求
	if !h.checkClientVersion(c, detection) {
		return
	}

	// 在请求上下文中记录 thinking 状态，供 Antigravity 最终模型 key 推导/模型维度限流使用
	h.backend.BindThinking(c, parsedReq.ThinkingEnabled)

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)

	// 验证 model 必填
	if reqModel == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, reqModel, body); decision != nil && decision.Blocked {
		h.errorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	// Track if we've started streaming (for error handling)
	streamStarted := false

	// 绑定错误透传服务，允许 service 层在非 failover 错误场景复用规则。
	h.backend.BindErrors(c)

	// 获取订阅信息（可能为nil）- 提前获取用于后续检查
	subscription, _ := SubscriptionFromContext(c)

	// 1. 首先获取用户并发槽位
	userReleaseFunc, err := h.concurrency.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		reqLog.Warn("gateway.user_slot_acquire_failed", zap.Error(err))
		h.concurrencyError(c, err, "user", streamStarted)
		return
	}
	// 在请求结束或 Context 取消时确保释放槽位，避免客户端断开造成泄漏
	userReleaseFunc = scheduler.WrapRelease(c.Request.Context(), scheduler.ReleaseOnCancel, userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. 【新增】Wait后二次检查余额/订阅
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("gateway.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.streamingError(c, status, code, message, streamStarted)
		return
	}

	// 设置请求所属分组 ID（用于分组功能判断，如 WebSearch 模拟）
	parsedReq.GroupID = apiKey.GroupID

	// 计算粘性会话hash
	parsedReq.SessionContext = &requeststate.SessionContext{
		ClientIP:  clientip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	sessionHash := session.GenerateSessionHash(parsedReq, slog.Info)

	// [DEBUG-STICKY] 打印会话 hash 生成结果
	reqLog.Info("sticky.session_hash_generated",
		zap.String("session_hash", sessionHash),
		zap.String("metadata_user_id_raw", parsedReq.MetadataUserID),
	)

	// 获取平台：优先使用强制平台（/antigravity 路由，中间件已设置 request.Context），否则使用分组平台
	platform := ""
	if forcePlatform, ok := h.backend.ForcedPlatform(c); ok {
		platform = forcePlatform
	} else if apiKey.Group != nil {
		platform = apiKey.Group.Platform
	}
	sessionKey := sessionHash
	if platform == capability.PlatformGemini && sessionHash != "" {
		sessionKey = "gemini:" + sessionHash
	}
	if isolationSessionID := MetadataSessionID(parsedReq.MetadataUserID); isolationSessionID != "" {
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, isolationSessionID); h.isolationError(c, err, streamStarted) {
			return
		}
	}

	// 查询粘性会话绑定的账号 ID
	var sessionBoundAccountID int64
	if sessionKey != "" {
		sessionBoundAccountID, _ = h.backend.CachedSession(c.Request.Context(), apiKey.GroupID, sessionKey)
		// [DEBUG-STICKY] 打印粘性会话查询结果
		reqLog.Info("sticky.cache_lookup",
			zap.String("session_key", sessionKey),
			zap.Int64("bound_account_id", sessionBoundAccountID),
		)
		if sessionBoundAccountID > 0 {
			prefetchedGroupID := int64(0)
			if apiKey.GroupID != nil {
				prefetchedGroupID = *apiKey.GroupID
			}
			h.backend.Prefetch(c, sessionBoundAccountID, prefetchedGroupID)
		}
	} else {
		reqLog.Info("sticky.no_session_key", zap.String("session_hash", sessionHash))
	}
	// 判断是否真的绑定了粘性会话：有 sessionKey 且已经绑定到某个账号
	hasBoundSession := sessionKey != "" && sessionBoundAccountID > 0

	call := MessagesCall{Key: apiKey, Subject: subject, Subscription: subscription, Parsed: parsedReq, Body: body, Model: reqModel, Stream: reqStream, ClaudeCode: isClaudeCodeClient, Platform: platform, SessionKey: sessionKey, BoundAccountID: sessionBoundAccountID, HasBoundSession: hasBoundSession, StreamStarted: &streamStarted, Log: reqLog, Route: groupMappingRoutePlan, Mapping: groupMapping}
	kind := execution.TextMessages
	if platform == capability.PlatformGemini {
		attempt, err := h.backend.PrepareGemini(c.Request.Context(), call)
		if err != nil {
			reqLog.Warn("gateway.prepare_gemini_group_mapping_failed", zap.Error(err))
			h.streamingError(c, http.StatusBadRequest, "invalid_request_error", "Failed to apply group model mapping", streamStarted)
			return
		}
		call.GeminiBody = attempt.Body.Bytes()
		call.GeminiModel = attempt.Model
		kind = execution.TextGeminiMessages
	}
	request := execution.Request{
		Hints:   requeststate.ExecutionHintsFromContext(c.Request.Context()),
		Routing: requeststate.RoutingStateFromContext(c.Request.Context()),
		Route:   call.Route, UserID: subject.UserID, Concurrency: subject.Concurrency, Stream: reqStream, Body: body, Model: reqModel,
		Funding: execution.FundingState{Key: apiKey, Subscription: subscription}, SessionHash: sessionKey,
		Metadata: execution.RequestMetadata{ClaudeCode: isClaudeCodeClient},
		Text:     execution.TextState{Kind: kind, Parsed: parsedReq, Platform: platform, BoundAccountID: sessionBoundAccountID, HasBoundSession: hasBoundSession, GeminiBody: call.GeminiBody, GeminiModel: call.GeminiModel},
	}
	output := &MessagesOutput{ResponseSink: ResponseSink{Writer: c.Writer}, HTTP: c, Log: reqLog, StreamStarted: &streamStarted}
	_, _ = h.executor.Execute(c.Request.Context(), request, output)
}

func (h *MessagesHandler) errorResponse(c *gin.Context, status int, kind, message string) {
	WriteAnthropicError(c, status, kind, "", message)
}

func (h *MessagesHandler) streamingError(c *gin.Context, status int, kind, message string, started bool) {
	WriteAnthropicStreamError(c, status, kind, "", message, started, h.backend.MarkStream)
}

func (h *MessagesHandler) concurrencyError(c *gin.Context, err error, slot string, started bool) {
	status, kind, code, message := ConcurrencyErrorResponse(err, slot)
	WriteAnthropicStreamError(c, status, kind, code, message, started, h.backend.MarkStream)
}

func (h *MessagesHandler) isolationError(c *gin.Context, err error, started bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		h.streamingError(c, http.StatusForbidden, "permission_error", session.SessionIsolationConflictMessage, started)
	} else {
		h.streamingError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", started)
	}
	return true
}

func (h *MessagesHandler) checkClientVersion(c *gin.Context, detected ClientDetection) bool {
	if !detected.ClaudeCode || strings.HasSuffix(c.Request.URL.Path, "/count_tokens") {
		return true
	}
	min, max := h.backend.ClientVersionBounds(c.Request.Context())
	if min == "" && max == "" {
		return true
	}
	message := clientmeta.ClaudeVersionRejection(h.backend.ClientVersion(c), min, max)
	if message == "" {
		return true
	}
	h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", message)
	return false
}

// SubscriptionFromContext 仅投影原 HTTP 权益字段，不执行查询。
func SubscriptionFromContext(c *gin.Context) (*billing.UserSubscription, bool) {
	value, exists := c.Get("subscription")
	if !exists {
		return nil, false
	}
	sub, ok := value.(*billing.UserSubscription)
	return sub, ok
}

func MetadataSessionID(raw string) string {
	parsed := anthropic.ParseMetadataUserID(raw)
	if parsed == nil {
		return ""
	}
	return strings.TrimSpace(parsed.SessionID)
}

func ModerationHTTPStatus(decision *moderation.Decision) int {
	if decision == nil || decision.StatusCode < 400 || decision.StatusCode > 599 {
		return http.StatusForbidden
	}
	return decision.StatusCode
}
