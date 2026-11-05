package app

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// gateway_count_tokens.go constructs token-count HTTP admission and adapts
// selected providers for platform counting.
// Token counting selects an execution provider using the requested model and
// excluded-provider set. The selected target routes auxiliary counting to
// Messages, OpenAI, or Gemini while retaining its session-release capability,
// and HTTP assembly shares funding, prompt replacement, and failure
// presentation.

// countExecution binds route planning and provider selection to token-count
// forwarding.
type countExecution struct {
	planner   *gatewayprovider.RoutePlanner                                    // Key route planner
	choices   *selection.Generic                                               // generic provider selection
	messages  *gatewayhttp.MessagesExecutor                                    // Messages count execution
	auxiliary *gatewayhttp.OpenAIAuxiliary                                     // OpenAI auxiliary execution
	gemini    *gatewayhttp.GeminiExecutor                                      // Gemini count execution
	cooldown  func(context.Context, int64, *forwardcore.UpstreamFailoverError) // provider retry cooldown
}

// countTarget carries one selected provider and its auxiliary counting
// dependencies.
type countTarget struct {
	choices   *selection.Generic                 // generic provider selection
	gateway   *gatewayhttp.MessagesExecutor      // Messages count execution
	auxiliary *gatewayhttp.OpenAIAuxiliary       // OpenAI auxiliary execution
	gemini    *gatewayhttp.GeminiExecutor        // Gemini count execution
	provider  *gatewayprovider.ExecutionProvider // selected execution provider
}

// SelectCountTarget chooses a model-compatible provider and returns its
// counting adapter. Selection errors end preparation; the adapter retains the
// same choices service so failed forwarding can release the selected provider
// session.
func (p countExecution) SelectCountTarget(ctx context.Context, id *int64, hash, model string, excluded map[int64]struct{}) (gatewayhttp.CountTarget, error) {
	value, err := p.choices.SelectProviderForModelWithExclusions(ctx, id, hash, model, excluded)
	if err != nil {
		return nil, err
	}
	return countTarget{gateway: p.messages, auxiliary: p.auxiliary, gemini: p.gemini, provider: value, choices: p.choices}, nil
}

// PlanCountRoute delegates Key and model routing to the shared planner.
func (p countExecution) PlanCountRoute(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	return p.planner.PlanKey(ctx, key, model)
}

// Snapshot returns the selected provider data used by count-attempt reporting.
func (t countTarget) Snapshot() provider.ProviderSnapshot {
	return gatewayprovider.ExecutionSnapshot(t.provider)
}

// RetryLimit returns the selected provider's configured same-provider retry
// count.
func (t countTarget) RetryLimit() int { return t.provider.View().GetPoolModeRetryCount() }

// ForwardCountTokens delegates one auxiliary count exchange to the platform
// executors. Pass the current Gin context and prepared request; platform
// dispatch chooses the supported counting path and returns its forwarding
// error.
func (t countTarget) ForwardCountTokens(ctx context.Context, c *gin.Context, parsed *requeststate.ParsedRequest) error {
	return gatewayhttp.ForwardSelectedCountTokens(ctx, c, t.provider, parsed, t.gateway, t.auxiliary, t.gemini)
}

// ReleaseSession delegates release of this provider's reservation for hash.
// Failed-attempt cleanup supplies an independent context through countAttempt
// so request cancellation does not suppress the session-release operation.
func (t countTarget) ReleaseSession(ctx context.Context, hash string) {
	t.choices.ReleaseProviderSession(ctx, t.provider, hash)
}

// provideCountTokensHTTP binds counting preparation to shared admission and
// execution services. With nil cfg it supplies a zero body-limit input and ten
// provider switches; optional availability, client fallback, error rules, and
// activity tracking are bound when present.
func provideCountTokensHTTP(planner *gatewayprovider.RoutePlanner, messages *gatewayhttp.MessagesExecutor, shared *schedulerSharedState, funding *admission.FundingAdmission, rules *errorpolicy.ErrorPassthroughService, cfg *config.Config, activity *gatewayRequestActivity, prompts *promptpolicy.Service, availability *gatewayModelAvailability, choices *selection.Generic, cooldown *provider.RetryCooldown, auxiliary *gatewayhttp.OpenAIAuxiliary, gemini *gatewayhttp.GeminiExecutor, clients *messageHTTPBindings) *gatewayhttp.CountTokensHandler {
	limit := int64(0)
	switches := 10
	if cfg != nil {
		limit = cfg.Gateway.MaxRequestBodyBytes
		if cfg.Gateway.Failover.MaxProviderSwitches > 0 {
			switches = cfg.Gateway.Failover.MaxProviderSwitches
		}
	}
	var matcher gatewayhttp.ErrorRuleMatcher
	if rules != nil {
		matcher = rules
	}
	ports := gatewayhttp.CountHTTPPorts{
		Executor: countExecution{planner: planner, choices: choices, messages: messages, auxiliary: auxiliary, gemini: gemini, cooldown: messageRetryCooldown(cooldown)}, Funding: funding, ReadAccess: keyhttp.GetAPIKeyFromContext,
		ObserveCompatibility: func(log *zap.Logger) {
			gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot {
				return gatewayCompatibilitySnapshot(shared)
			})
		},
		BusinessError: func(c *gin.Context, err error, started bool, write func(int, string, string, bool)) bool {
			return gatewayhttp.WriteGroupSelectionBusinessError(c, err, started, keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{}, write)
		},
		Failure: func(c *gin.Context, failure *forwardcore.UpstreamFailoverError, platform string, started bool) {
			gatewayhttp.WriteAnthropicFailover(c, failure, platform, started, matcher, forwardcore.IsOpenAISilentRefusalErrorBody, forwardcore.OpenAISilentRefusalClientMessage())
		},
	}
	if clients != nil {
		ports.ClientGroupFallback = clients.bindings.ClientGroupFallback
	}
	if availability != nil {
		ports.Diagnoser = availability.Messages
	}
	result := gatewayhttp.NewCountTokensHandler(limit, switches, ports, prompts)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}

// TempUnscheduleRetryableError passes the selected failure to the provider
// cooldown adapter.
func (p countExecution) TempUnscheduleRetryableError(ctx context.Context, id int64, failure *forwardcore.UpstreamFailoverError) {
	p.cooldown(ctx, id, failure)
}
