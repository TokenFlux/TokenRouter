// 计数 HTTP 入口保留鉴权、报文与资金预检顺序，不取得并发槽或提交费用。
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// CountTokensBackend 组合固定依赖，每次仅建立无槽尝试状态。
type CountTokensBackend interface {
	Access(*gin.Context) (*apikey.APIKey, bool)
	CompatibilityMetrics(*zap.Logger)
	ObserveRequest(*gin.Context, string, bool)
	ObserveEndpoint(*gin.Context, bool)
	BindClient(*gin.Context, ClientDetection)
	BindThinking(*gin.Context, bool)
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	Execution(*gin.Context, *apikey.APIKey, *requeststate.ParsedRequest, string, *zap.Logger) textflow.CountPorts
	FailoverObservation(context.Context, string, map[string]any)
}
type CountTokensHandler struct {
	requestLifetime

	maxBodyBytes int64
	maxSwitches  int
	backend      CountTokensBackend
	prompt       MessagesPrompt
}

func NewCountTokensHandler(limit int64, switches int, backend CountTokensBackend, prompt MessagesPrompt) *CountTokensHandler {
	return &CountTokensHandler{maxBodyBytes: limit, maxSwitches: switches, backend: backend, prompt: prompt}
}
func (h *CountTokensHandler) errorResponse(c *gin.Context, status int, kind, message string) {
	WriteAnthropicError(c, status, kind, "", message)
}
func countMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var exceeded *http.MaxBytesError
	ok := errors.As(err, &exceeded)
	return exceeded, ok
}

// CountTokens handles token counting endpoint
// POST /v1/messages/count_tokens
// 特点：校验订阅/余额，但不计算并发、不记录使用量
func (h *CountTokensHandler) CountTokens(c *gin.Context) {
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

	_, ok = authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.gateway.count_tokens",
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	defer h.backend.CompatibilityMetrics(reqLog)

	// 读取请求体
	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.maxBodyBytes)
	if err != nil {
		if maxErr, ok := countMaxBytesError(err); ok {
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

	// count_tokens 也要先执行用户提示词替换，保证解析、会话 hash 和上游请求体一致。
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "anthropic_messages")

	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, err := requeststate.ParseGatewayRequest(bodyRef, capability.PlatformAnthropic)
	if err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	body = parsedReq.Body.Bytes()
	// count_tokens 走 messages 严格校验时，复用已解析请求，避免二次反序列化。
	h.backend.BindClient(c, DetectClaudeCodeRequest(c, body, parsedReq, false))
	reqLog = reqLog.With(zap.String("model", parsedReq.Model), zap.Bool("stream", parsedReq.Stream))
	// 在请求上下文中记录 thinking 状态，供 Antigravity 最终模型 key 推导/模型维度限流使用
	h.backend.BindThinking(c, parsedReq.ThinkingEnabled)

	// 验证 model 必填
	if parsedReq.Model == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	h.backend.ObserveRequest(c, parsedReq.Model, parsedReq.Stream)
	h.backend.ObserveEndpoint(c, parsedReq.Stream)

	// 获取订阅信息（可能为nil）
	subscription, _ := SubscriptionFromContext(c)

	// 校验 billing eligibility（订阅/余额）
	// 【注意】不计算并发，但需要校验订阅/余额
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	// 计算粘性会话 hash
	parsedReq.SessionContext = &requeststate.SessionContext{
		ClientIP:  clientip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	sessionHash := session.GenerateSessionHash(parsedReq, slog.Info)

	textflow.RunCountTokens(h.backend.Execution(c, apiKey, parsedReq, sessionHash, reqLog), h.maxSwitches, h.backend.FailoverObservation)
}
