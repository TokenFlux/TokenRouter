package app

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// gateway_openai_tokens.go constructs OpenAI token-count handlers and adapts
// auxiliary provider selections for counting.
// OpenAI token-count preparation uses shared route planning and auxiliary
// execution. Responses input-token selection asks the scheduler to select
// without normal slot admission and carries any acquired release callback,
// while missing application dependencies are recorded for early HTTP rejection.

// openAITokenExecution joins auxiliary token methods with route planning and
// selection.
type openAITokenExecution struct {
	// OpenAIAuxiliary supplies the selected-platform token exchanges. Handler
	// dependency checks reject a missing auxiliary executor before these promoted methods are used.
	*gatewayhttp.OpenAIAuxiliary
	planner *gatewayprovider.RoutePlanner // requested model routing
	choices *selection.Compatible         // compatible provider selection
}

// openAITokenTarget binds a selected execution provider to auxiliary token
// forwarding.
type openAITokenTarget struct {
	source *gatewayhttp.OpenAIAuxiliary       // selected token execution
	value  *gatewayprovider.ExecutionProvider // selected provider target
}

// PlanTokenRoute delegates the authenticated Key and model to route planning.
func (p openAITokenExecution) PlanTokenRoute(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
	return p.planner.PlanKey(ctx, key, model)
}

// TokenSessionHash derives the OpenAI session identifier from HTTP input.
func (p openAITokenExecution) TokenSessionHash(c *gin.Context, body []byte) string {
	return gatewayhttp.GenerateOpenAISessionHash(c, body)
}

// SelectCount delegates platform-aware auxiliary selection and wraps its
// returned provider. An absent provider returns nil with the selection error;
// an available provider is retained together with any returned error.
func (p openAITokenExecution) SelectCount(ctx context.Context, group *int64, hash, model, platform string) (gatewayhttp.OpenAICountTarget, error) {
	value, err := p.choices.SelectProviderForTokenCount(ctx, group, hash, model, provider.OpenAIEndpointCapabilityTextGeneration, platform)
	if value == nil {
		return nil, err
	}
	return openAITokenTarget{source: p.OpenAIAuxiliary, value: value}, err
}

// SelectInputTokens selects a text-capable provider with select-only scheduler
// context. Absent or failed selection returns an empty result; when selection
// reports an acquired resource, its release callback is returned for the
// caller's cleanup.
func (p openAITokenExecution) SelectInputTokens(ctx context.Context, group *int64, hash, model, routingModel string, excluded map[int64]struct{}, platform string) (gatewayhttp.InputTokensSelection, error) {
	// Auxiliary input counting requests select-only behavior. Retain any reported
	// acquisition callback so the caller can release resources returned by selection.
	selected, _, err := p.choices.SelectProviderWithSchedulerForCapabilityAndRoutingModel(scheduler.WithSelectOnly(ctx), group, "", hash, model, routingModel, excluded, egress.OpenAIUpstreamTransportAny, provider.OpenAIEndpointCapabilityTextGeneration, false, false, platform)
	if err != nil || selected == nil || selected.Provider == nil {
		return gatewayhttp.InputTokensSelection{}, err
	}
	result := gatewayhttp.InputTokensSelection{Target: openAITokenTarget{source: p.OpenAIAuxiliary, value: selected.Provider}}
	if selected.Acquired {
		result.Release = selected.ReleaseFunc
	}
	return result, nil
}

// Snapshot returns candidate provider data for this auxiliary token target.
func (t openAITokenTarget) Snapshot() provider.ProviderSnapshot {
	return gatewayprovider.ExecutionSnapshot(t.value)
}

// RetryLimit returns the selected provider's same-provider retry count for
// counting.
func (t openAITokenTarget) RetryLimit() int { return t.value.View().GetPoolModeRetryCount() }

// ForwardCount delegates Anthropic-shaped token counting to the selected
// auxiliary executor.
func (t openAITokenTarget) ForwardCount(ctx context.Context, c *gin.Context, body []byte, model string) error {
	return t.source.ForwardCountTokensAsAnthropic(ctx, c, t.value, body, model)
}

// ForwardInputTokens delegates Responses input-token forwarding using the
// selected provider.
func (t openAITokenTarget) ForwardInputTokens(ctx context.Context, c *gin.Context, body []byte) error {
	return t.source.ForwardResponsesInputTokens(ctx, c, t.value, body)
}

// provideOpenAITokensHTTP constructs auxiliary counting with three provider
// switches by default. It records missing source, funding, Key, and concurrency
// dependencies for HTTP rejection, binds optional diagnostics and error rules,
// and attaches request activity when supplied.
func provideOpenAITokensHTTP(source *gatewayhttp.OpenAIAuxiliary, funding *admission.FundingAdmission, keys *apikey.APIKeyService, concurrency *scheduler.ConcurrencyService, rules *errorpolicy.ErrorPassthroughService, cfg *config.Config, activity *gatewayRequestActivity, prompts *promptpolicy.Service, availability *gatewayModelAvailability, choices *selection.Compatible, planner *gatewayprovider.RoutePlanner) *gatewayhttp.OpenAITokensHandler {
	options := gatewayhttp.OpenAITokenOptions{MaxSwitches: 3}
	if cfg != nil {
		options.MaxBodyBytes = cfg.Gateway.MaxRequestBodyBytes
		if cfg.Gateway.Failover.MaxProviderSwitches > 0 {
			options.MaxSwitches = cfg.Gateway.Failover.MaxProviderSwitches
		}
	}
	ports := gatewayhttp.OpenAITokenPorts{Execution: openAITokenExecution{OpenAIAuxiliary: source, choices: choices, planner: planner}, Funding: funding}
	if availability != nil {
		ports.Diagnoser = availability.Compatible
		ports.ResolvedDiagnoser = availability.Resolved
	}
	if rules != nil {
		ports.Rules = rules
	}
	if source == nil {
		ports.MissingDependencies = append(ports.MissingDependencies, "gatewayService")
	}
	if funding == nil {
		ports.MissingDependencies = append(ports.MissingDependencies, "billingCacheService")
	}
	if keys == nil {
		ports.MissingDependencies = append(ports.MissingDependencies, "apiKeyService")
	}
	if concurrency == nil {
		ports.MissingDependencies = append(ports.MissingDependencies, "concurrencyHelper")
	}
	result := gatewayhttp.NewOpenAITokensHandler(options, ports, prompts)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}
