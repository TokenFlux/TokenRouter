// 通用 Responses/Chat 的 HTTP 前置组合，账号循环由 gateway/text 唯一实现。
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type CompatibleTextKind uint8

const (
	CompatibleResponses CompatibleTextKind = iota
	CompatibleChat
)

type CompatibleTextCall struct {
	MessagesCall
	RequestContext                      context.Context
	ForwardBody                         []byte
	GroupPlatform, SelectionSessionHash string
}
type CompatibleTextBackend interface {
	Access(*gin.Context) (*apikey.APIKey, bool)
	ObserveRequest(*gin.Context, string, bool)
	ObserveEndpoint(*gin.Context, bool)
	Reasoning(*gin.Context, *apikey.APIKey, []byte) ([]byte, bool, error)
	PolicyDenied(*gin.Context)
	Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan
	BindPlan(*gin.Context, routing.RoutePlan)
	ImageIntent(*apikey.APIKey, string, []byte, routing.ChannelMappingResult) ([]byte, bool)
	ImageContext(context.Context) context.Context
	ChatImageModel(string, routing.ChannelMappingResult) bool
	Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, string, string, []byte) *moderation.Decision
	BindErrors(*gin.Context)
	AuthLatency(*gin.Context, int64)
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	Isolate(context.Context, *apikey.APIKey, int64, string) error
	MarkStream(*gin.Context, string, string, int)
	FailoverObservation(context.Context, string, map[string]any)
}
type CompatibleTextHandler struct {
	executor execution.Executor
	requestLifetime

	options     MessagesHTTPOptions
	backend     CompatibleTextBackend
	prompt      MessagesPrompt
	concurrency *ConcurrencyHelper
}

func NewCompatibleTextHandler(options MessagesHTTPOptions, backend CompatibleTextBackend, prompt MessagesPrompt, concurrency *ConcurrencyHelper, executor execution.Executor) *CompatibleTextHandler {
	return &CompatibleTextHandler{executor: executor, options: options, backend: backend, prompt: prompt, concurrency: concurrency}
}
func compatibleMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var limit *http.MaxBytesError
	ok := errors.As(err, &limit)
	return limit, ok
}
func WriteCompatibleResponsesError(c *gin.Context, status int, kind, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": kind, "message": message}})
}
func WriteCompatibleChatError(c *gin.Context, status int, kind, message string) {
	c.JSON(status, gin.H{"error": gin.H{"type": kind, "message": message}})
}
func (h *CompatibleTextHandler) responsesErrorResponse(c *gin.Context, status int, kind, message string) {
	WriteCompatibleResponsesError(c, status, kind, message)
}
func (h *CompatibleTextHandler) chatCompletionsErrorResponse(c *gin.Context, status int, kind, message string) {
	WriteCompatibleChatError(c, status, kind, message)
}
func (h *CompatibleTextHandler) concurrencyError(c *gin.Context, err error, slot string, started bool) {
	status, kind, code, message := ConcurrencyErrorResponse(err, slot)
	WriteAnthropicStreamError(c, status, kind, code, message, started, h.backend.MarkStream)
}
func (h *CompatibleTextHandler) Responses(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	streamStarted := false

	requestStart := time.Now()

	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.responsesErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.responsesErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.gateway.responses",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// 读取请求体
	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := compatibleMaxBytesError(err); ok {
			h.responsesErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, "", false)

	// 验证 JSON
	if !gjson.ValidBytes(body) {
		LogRequestBodyParseFailure(reqLog, body, nil)
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	// 用户提示词替换必须早于模型解析、内容审计和会话 hash，确保后续链路看到同一份请求体。
	body = h.prompt.ApplyUserPromptReplacement(c.Request.Context(), body, "openai_responses")

	// 按原字段规则读取模型与流标志
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	// 在协议转换前裁决客户端显式档位，并保留改写前的审计值。
	if policyBody, _, policyErr := h.backend.Reasoning(c, apiKey, body); policyErr != nil {
		h.backend.PolicyDenied(c)
		h.responsesErrorResponse(c, http.StatusForbidden, "permission_error", policyErr.Error())
		return
	} else {
		body = policyBody
	}
	reqStream, ok := ParseOpenAICompatibleStream(body)
	if !ok {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", InvalidStreamFieldTypeMessage)
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)
	// 生图能力和模型级限流以渠道模型 C 及其请求体为准。
	// 当前分组和渠道结果进入独立计划，不改变原解析位置。
	channelMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	channelMapping := channelMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, channelMappingRoutePlan)
	forwardBody, imageIntent := h.backend.ImageIntent(apiKey, reqModel, body, channelMapping)
	requestCtx := c.Request.Context()
	if imageIntent {
		requestCtx = h.backend.ImageContext(requestCtx)
	}

	// Responses 不是 Claude Code 入口，专用分组在进入等待和账号选择前拒绝。
	if apiKey.Group != nil && apiKey.Group.ClaudeCodeOnly {
		h.responsesErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group is restricted to Claude Code clients (/v1/messages only)")
		return
	}

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, moderation.ContentModerationProtocolOpenAIResponses, reqModel, body); decision != nil && decision.Blocked {
		h.responsesErrorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	// 绑定错误展示规则
	h.backend.BindErrors(c)

	subscription, _ := SubscriptionFromContext(c)

	h.backend.AuthLatency(c, time.Since(requestStart).Milliseconds())

	// 1. 获取用户并发槽
	userReleaseFunc, err := h.concurrency.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		reqLog.Warn("gateway.responses.user_slot_acquire_failed", zap.Error(err))
		h.concurrencyError(c, err, "user", streamStarted)
		return
	}
	// 只追加新取得的请求租约，保留生图意图等之前固化的上下文。
	if lease := scheduler.RequestLease(c.Request.Context()); lease != nil {
		requestCtx = scheduler.WithRequestLease(requestCtx, lease)
	}
	userReleaseFunc = scheduler.WrapRelease(c.Request.Context(), scheduler.ReleaseOnCancel, userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. 等待后复查资金资格
	if err := h.backend.Eligibility(requestCtx, apiKey, subscription); err != nil {
		reqLog.Info("gateway.responses.billing_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.responsesErrorResponse(c, status, code, message)
		return
	}

	// 解析请求并生成会话哈希
	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, _ := requeststate.ParseGatewayRequest(bodyRef, "responses")
	if parsedReq == nil {
		parsedReq = &requeststate.ParsedRequest{Model: reqModel, Stream: reqStream, Body: bodyRef}
	}
	parsedReq.SessionContext = &requeststate.SessionContext{
		ClientIP:  clientip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	sessionHash := session.GenerateSessionHash(parsedReq, slog.Info)
	if isolationSessionID := MetadataSessionID(parsedReq.MetadataUserID); isolationSessionID != "" {
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, isolationSessionID); err != nil {
			if errors.Is(err, session.ErrSessionIsolationConflict) {
				h.responsesErrorResponse(c, http.StatusForbidden, "permission_error", session.SessionIsolationConflictMessage)
			} else {
				h.responsesErrorResponse(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable")
			}
			return
		}
	}

	call := CompatibleTextCall{
		MessagesCall: MessagesCall{
			Key:           apiKey,
			Subject:       subject,
			Subscription:  subscription,
			Parsed:        parsedReq,
			Body:          body,
			Model:         reqModel,
			Stream:        reqStream,
			SessionKey:    sessionHash,
			StreamStarted: &streamStarted,
			Log:           reqLog,
			Route:         channelMappingRoutePlan,
			Mapping:       channelMapping,
		},
		RequestContext: requestCtx,
		ForwardBody:    forwardBody,
	}
	h.executeCompatible(c, call, execution.TextGenericResponses)
}

func (h *CompatibleTextHandler) ChatCompletions(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	streamStarted := false

	requestStart := time.Now()

	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.chatCompletionsErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.chatCompletionsErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.gateway.chat_completions",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// 读取请求体
	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := compatibleMaxBytesError(err); ok {
			h.chatCompletionsErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.chatCompletionsErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.chatCompletionsErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, "", false)

	// 验证 JSON
	if !gjson.ValidBytes(body) {
		LogRequestBodyParseFailure(reqLog, body, nil)
		h.chatCompletionsErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	// 用户提示词替换必须早于模型解析、内容审计和会话 hash，确保后续链路看到同一份请求体。
	body = h.prompt.ApplyUserPromptReplacement(c.Request.Context(), body, "chat_completions")

	// 读取模型与流标志
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.chatCompletionsErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	// 与 Messages/Responses 使用相同策略，避免兼容入口绕过分组上限。
	if policyBody, _, policyErr := h.backend.Reasoning(c, apiKey, body); policyErr != nil {
		h.backend.PolicyDenied(c)
		h.chatCompletionsErrorResponse(c, http.StatusForbidden, "permission_error", policyErr.Error())
		return
	} else {
		body = policyBody
	}
	reqStream, ok := ParseOpenAICompatibleStream(body)
	if !ok {
		h.chatCompletionsErrorResponse(c, http.StatusBadRequest, "invalid_request_error", InvalidStreamFieldTypeMessage)
		return
	}
	// Chat Completions 的端点能力以渠道模型 C 为准，客户端模型 R 仍用于日志和错误语义。
	// 当前分组和渠道结果进入独立计划，不改变原解析位置。
	channelMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	channelMapping := channelMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, channelMappingRoutePlan)
	if h.backend.ChatImageModel(reqModel, channelMapping) {
		h.chatCompletionsErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "This model is not supported on the Chat Completions endpoint")
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)

	// 保留 Claude Code 专用分组限制
	if apiKey.Group != nil && apiKey.Group.ClaudeCodeOnly {
		h.chatCompletionsErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group is restricted to Claude Code clients (/v1/messages only)")
		return
	}

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, moderation.ContentModerationProtocolOpenAIChat, reqModel, body); decision != nil && decision.Blocked {
		h.chatCompletionsErrorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	// 绑定错误展示规则
	h.backend.BindErrors(c)

	subscription, _ := SubscriptionFromContext(c)

	h.backend.AuthLatency(c, time.Since(requestStart).Milliseconds())

	// 1. 获取用户并发槽
	userReleaseFunc, err := h.concurrency.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		reqLog.Warn("gateway.cc.user_slot_acquire_failed", zap.Error(err))
		h.concurrencyError(c, err, "user", streamStarted)
		return
	}
	userReleaseFunc = scheduler.WrapRelease(c.Request.Context(), scheduler.ReleaseOnCancel, userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. 等待后复查资金资格
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("gateway.cc.billing_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.chatCompletionsErrorResponse(c, status, code, message)
		return
	}

	// 解析请求并生成会话哈希
	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, _ := requeststate.ParseGatewayRequest(bodyRef, "chat_completions")
	if parsedReq == nil {
		parsedReq = &requeststate.ParsedRequest{Model: reqModel, Stream: reqStream, Body: bodyRef}
	}
	parsedReq.SessionContext = &requeststate.SessionContext{
		ClientIP:  clientip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	sessionHash := session.GenerateSessionHash(parsedReq, slog.Info)
	groupPlatform := ""
	if apiKey.Group != nil {
		groupPlatform = apiKey.Group.Platform
	}
	selectionSessionHash := sessionHash
	if groupPlatform == capability.PlatformGemini && selectionSessionHash != "" {
		selectionSessionHash = "gemini:" + selectionSessionHash
	}
	if isolationSessionID := MetadataSessionID(parsedReq.MetadataUserID); isolationSessionID != "" {
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, isolationSessionID); err != nil {
			if errors.Is(err, session.ErrSessionIsolationConflict) {
				h.chatCompletionsErrorResponse(c, http.StatusForbidden, "permission_error", session.SessionIsolationConflictMessage)
			} else {
				h.chatCompletionsErrorResponse(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable")
			}
			return
		}
	}

	call := CompatibleTextCall{
		MessagesCall: MessagesCall{
			Key:           apiKey,
			Subject:       subject,
			Subscription:  subscription,
			Parsed:        parsedReq,
			Body:          body,
			Model:         reqModel,
			Stream:        reqStream,
			SessionKey:    sessionHash,
			StreamStarted: &streamStarted,
			Log:           reqLog,
			Route:         channelMappingRoutePlan,
			Mapping:       channelMapping,
		},
		RequestContext:       c.Request.Context(),
		GroupPlatform:        groupPlatform,
		SelectionSessionHash: selectionSessionHash,
	}
	h.executeCompatible(c, call, execution.TextGenericChat)
}

func (h *CompatibleTextHandler) executeCompatible(c *gin.Context, call CompatibleTextCall, kind execution.TextKind) {
	request := execution.Request{
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
		AttemptBody: call.ForwardBody,

		Text: execution.TextState{Kind: kind, Parsed: call.Parsed, Platform: call.GroupPlatform, SelectionContext: call.RequestContext, SelectionSessionHash: call.SelectionSessionHash, Mapping: call.Mapping, AlternateBudget: kind == execution.TextGenericChat && call.GroupPlatform == capability.PlatformGemini}}
	output := &MessagesOutput{ResponseSink: ResponseSink{Writer: c.Writer}, HTTP: c, Log: call.Log, StreamStarted: call.StreamStarted}
	_, _ = h.executor.Execute(c.Request.Context(), request, output)
}
