// 旧装配仅把已迁模块及尚未迁完的执行端口投影给新 HTTP 入口。
package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type messagesHTTPBackend struct{ h *GatewayHandler }

// NewMessagesHTTPHandler 不创建缓存或工作任务，app 将固定依赖绑定到生产路由。
func (h *GatewayHandler) NewMessagesHTTPHandler() *gatewayhttp.MessagesHandler {
	return gatewayhttp.NewMessagesHandler(
		gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: gatewayMaxBodySize(h.cfg), MaxSwitches: h.maxAccountSwitches, MaxGeminiSwitches: h.maxAccountSwitchesGemini},
		messagesHTTPBackend{h}, h.gatewayService, h.concurrencyHelper, h.NewMessagesExecutor(),
	)
}

// NewMessagesExecutor 供 app 固定绑定；先完成唯一 Recorder 绑定，再构造此执行器。
func (h *GatewayHandler) NewMessagesExecutor() *textflow.MessagesExecutor {
	runtime := &fixedMessagesRuntime{dependencies: newMessageExecutionDependencies(h)}
	return textflow.NewMessagesExecutor(runtime,
		textflow.MessageOptions{MaxSwitches: h.maxAccountSwitches, CompletePartialFailure: true, Observe: telemetry.Failover},
		textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, Observe: telemetry.Failover},
	)
}
func (p messagesHTTPBackend) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := gatewayhttp.EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := middleware.GetAPIKeyFromContext(c)
	return apikey.CopyAPIKey(key), ok
}
func (p messagesHTTPBackend) CompatibilityMetrics(log *zap.Logger) {
	p.h.maybeLogCompatibilityFallbackMetrics(log)
}
func (p messagesHTTPBackend) ObserveRequest(c *gin.Context, model string, stream bool) {
	gatewayhttp.SetOpsRequestContext(c, model, stream)
}
func (p messagesHTTPBackend) ObserveEndpoint(c *gin.Context, stream bool) {
	gatewayhttp.SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}
func (p messagesHTTPBackend) Reasoning(c *gin.Context, key *apikey.APIKey, body []byte) ([]byte, bool, error) {
	return applyAnthropicReasoningEffortPolicyForRequest(c, apikey.CopyAPIKey(key), body)
}
func (p messagesHTTPBackend) PolicyDenied(c *gin.Context) {
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
}
func (p messagesHTTPBackend) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	old := apikey.CopyAPIKey(key)
	return p.h.gatewayService.PlanRoute(ctx, service.APIKeyRouteGroup(old), old.GroupID, model)
}
func (p messagesHTTPBackend) BindPlan(c *gin.Context, plan routing.RoutePlan) {
	c.Request = c.Request.WithContext(requeststate.WithRoutePlan(c.Request.Context(), plan))
}
func (p messagesHTTPBackend) BindProbe(c *gin.Context) {
	c.Request = c.Request.WithContext(requeststate.WithIsMaxTokensOneHaikuRequest(c.Request.Context(), true))
}
func (p messagesHTTPBackend) Probe(c *gin.Context) bool {
	probe, _ := requeststate.IsMaxTokensOneHaikuRequestFromContext(c.Request.Context())
	return probe
}
func (p messagesHTTPBackend) BindClient(c *gin.Context, d gatewayhttp.ClientDetection) {
	ctx := requeststate.SetClaudeCodeClient(c.Request.Context(), d.ClaudeCode)
	if d.ClaudeCode && d.Version != "" {
		ctx = requeststate.SetClaudeCodeVersion(ctx, d.Version)
	}
	c.Request = c.Request.WithContext(ctx)
}
func (p messagesHTTPBackend) BindThinking(c *gin.Context, thinking bool) {
	c.Request = c.Request.WithContext(requeststate.WithThinkingEnabled(c.Request.Context(), thinking))
}
func (p messagesHTTPBackend) ClientVersion(c *gin.Context) string {
	return requeststate.GetClaudeCodeVersion(c.Request.Context())
}
func (p messagesHTTPBackend) ClientVersionBounds(ctx context.Context) (string, string) {
	return p.h.runtimeSettings.GetClaudeCodeVersionBounds(ctx)
}
func (p messagesHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, model string, body []byte) *moderation.Decision {
	return p.h.checkContentModeration(c, log, apikey.CopyAPIKey(key), subject, moderation.ContentModerationProtocolAnthropicMessages, model, body)
}
func (p messagesHTTPBackend) BindErrors(c *gin.Context) {
	if p.h.errorPassthroughService != nil {
		gatewayhttp.BindErrorPassthroughService(c, p.h.errorPassthroughService)
	}
}
func (p messagesHTTPBackend) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	old := apikey.CopyAPIKey(key)
	return p.h.billingCacheService.CheckKey(ctx, old, sub, service.QuotaPlatform(ctx, old), false)
}
func (p messagesHTTPBackend) ForcedPlatform(c *gin.Context) (string, bool) {
	return middleware.GetForcePlatformFromContext(c)
}
func (p messagesHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, userID int64, hash string) error {
	return p.h.ensureGatewaySessionIsolation(ctx, apikey.CopyAPIKey(key), userID, session.SessionIsolationSourceGateway, hash)
}
func (p messagesHTTPBackend) CachedSession(ctx context.Context, groupID *int64, hash string) (int64, error) {
	return p.h.gatewayService.GetCachedSessionAccountID(ctx, groupID, hash)
}
func (p messagesHTTPBackend) Prefetch(c *gin.Context, accountID, groupID int64) {
	c.Request = c.Request.WithContext(requeststate.WithPrefetchedStickySession(c.Request.Context(), accountID, groupID))
}
func (p messagesHTTPBackend) PrepareGemini(ctx context.Context, call gatewayhttp.MessagesCall) (*requeststate.ParsedRequest, error) {
	parsed, _, err := p.h.prepareGatewayAttemptRequest(ctx, call.Parsed, call.Body, apikey.CopyAPIKey(call.Key), call.Model)
	return parsed, err
}

func (p messagesHTTPBackend) MarkStream(c *gin.Context, kind, message string, status int) {
	gatewayhttp.MarkOpsStreamError(c, kind, message, status)
}
func (p messagesHTTPBackend) FailoverObservation(ctx context.Context, event string, values map[string]any) {
	telemetry.Failover(ctx, event, values)
}
