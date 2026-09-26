package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MessagesBindings 固定路由、资金、隔离和审核端口；HTTP 状态操作由本模块拥有。
type MessagesBindings struct {
	PlanRoute      func(context.Context, *apikey.APIKey, string) routing.RoutePlan
	ClientVersions func(context.Context) (string, string)
	Funding        interface {
		CheckKey(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error
	}
	Moderation           ModerationPort
	Errors               *errorpolicy.ErrorPassthroughService
	IsolateSession       func(context.Context, *apikey.APIKey, int64, string, string) error
	CachedSession        func(context.Context, *int64, string) (int64, error)
	ObserveCompatibility func(*zap.Logger)
}

type messagesHTTPBackend struct{ bindings MessagesBindings }

// NewBoundMessagesHandler 直接组合原生 HTTP 行为与固定执行器，不依赖旧 Handler 工厂。
func NewBoundMessagesHandler(options MessagesHTTPOptions, bindings MessagesBindings, prompt MessagesPrompt, concurrency *ConcurrencyHelper, executor execution.Executor) *MessagesHandler {
	return NewMessagesHandler(options, messagesHTTPBackend{bindings}, prompt, concurrency, executor)
}

func (p messagesHTTPBackend) Access(c *gin.Context) (*apikey.APIKey, bool) {
	if key, ok := EffectiveAPIKey(c); ok {
		return key, true
	}
	key, ok := keyhttp.GetAPIKeyFromContext(c)
	return apikey.CopyAPIKey(key), ok
}

func (p messagesHTTPBackend) CompatibilityMetrics(log *zap.Logger) {
	p.bindings.ObserveCompatibility(log)
}

func (p messagesHTTPBackend) ObserveRequest(c *gin.Context, model string, stream bool) {
	SetOpsRequestContext(c, model, stream)
}

func (p messagesHTTPBackend) ObserveEndpoint(c *gin.Context, stream bool) {
	SetOpsEndpointContext(c, "", int16(usage.RequestTypeFromLegacy(stream, false)))
}

func (p messagesHTTPBackend) Reasoning(c *gin.Context, key *apikey.APIKey, body []byte) ([]byte, bool, error) {
	return ApplyAnthropicReasoningEffortPolicyForRequest(c, apikey.CopyAPIKey(key), body)
}

func (p messagesHTTPBackend) PolicyDenied(c *gin.Context) {
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
}

func (p messagesHTTPBackend) Plan(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	old := apikey.CopyAPIKey(key)
	return p.bindings.PlanRoute(ctx, old, model)
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

func (p messagesHTTPBackend) BindClient(c *gin.Context, d ClientDetection) {
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
	return p.bindings.ClientVersions(ctx)
}

func (p messagesHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, model string, body []byte) *moderation.Decision {
	return RunContentModeration(GatewayModerationEndpoints{}, c, log, p.bindings.Moderation, apikey.CopyAPIKey(key), subject, moderation.ContentModerationProtocolAnthropicMessages, model, body)
}

func (p messagesHTTPBackend) BindErrors(c *gin.Context) {
	if p.bindings.Errors != nil {
		BindErrorPassthroughService(c, p.bindings.Errors)
	}
}

func (p messagesHTTPBackend) Eligibility(ctx context.Context, key *apikey.APIKey, sub *billing.UserSubscription) error {
	old := apikey.CopyAPIKey(key)
	return p.bindings.Funding.CheckKey(ctx, old, sub, admission.QuotaPlatform(ctx, old), false)
}

func (p messagesHTTPBackend) ForcedPlatform(c *gin.Context) (string, bool) {
	return keyhttp.GetForcePlatformFromContext(c)
}

func (p messagesHTTPBackend) Isolate(ctx context.Context, key *apikey.APIKey, userID int64, hash string) error {
	if p.bindings.IsolateSession == nil {
		return nil
	}
	return p.bindings.IsolateSession(ctx, apikey.CopyAPIKey(key), userID, session.SessionIsolationSourceGateway, hash)
}

func (p messagesHTTPBackend) CachedSession(ctx context.Context, groupID *int64, hash string) (int64, error) {
	return p.bindings.CachedSession(ctx, groupID, hash)
}

func (p messagesHTTPBackend) Prefetch(c *gin.Context, accountID, groupID int64) {
	c.Request = c.Request.WithContext(requeststate.WithPrefetchedStickySession(c.Request.Context(), accountID, groupID))
}

func (p messagesHTTPBackend) PrepareGemini(ctx context.Context, call MessagesCall) (*requeststate.ParsedRequest, error) {
	parsed, _, err := PrepareGroupAttempt(ctx, call.Parsed, call.Body, apikey.CopyAPIKey(call.Key), call.Model, p.bindings.PlanRoute)
	return parsed, err
}

func (p messagesHTTPBackend) MarkStream(c *gin.Context, kind, message string, status int) {
	MarkOpsStreamError(c, kind, message, status)
}

func (p messagesHTTPBackend) FailoverObservation(ctx context.Context, event string, values map[string]any) {
	telemetry.Failover(ctx, event, values)
}
