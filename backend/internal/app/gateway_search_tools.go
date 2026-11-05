package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/transport"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/search"
)

// gateway_search_tools.go constructs search-tool emulation and standalone Grok
// search execution and HTTP handling.
// Standalone Grok search selects a provider and carries its acquisition,
// waiting, and release data into HTTP orchestration. Search-tool emulation
// separately shares configuration and group policy, while both paths reuse
// application transport and runtime endpoint-mode readers.

// standaloneSearchExecution adapts Grok provider selection and one search
// exchange.
type standaloneSearchExecution struct {
	selector *selection.Compatible               // Grok provider selection
	executor *gatewayprovider.GrokSearchExecutor // standalone search exchange
}

// standaloneSearchTarget holds the selected record used for search and
// completion.
type standaloneSearchTarget struct {
	source   *gatewayprovider.GrokSearchExecutor // selected search execution
	provider *provider.Record                    // selected provider record
}

// ProvideGatewaySearchTools combines shared search configuration with group
// tool policy.
func ProvideGatewaySearchTools(settings *search.ConfigService, modelConfigs *routing.PricingConfigService) *searchtools.Emulator {
	runtime := gatewayprovider.NewSearchTools(settings, modelConfigs)
	return runtime
}

// Select requests a Grok text-capable provider and returns its search target.
// Selection errors return false; absence also returns false with nil error,
// while a target carries scheduler acquisition and release information for HTTP
// ownership.
func (p standaloneSearchExecution) Select(ctx context.Context, group int64, model string, excluded map[int64]struct{}) (gatewayhttp.StandaloneSearchTarget, searchtools.Selection, bool, error) {
	selected, _, err := p.selector.SelectProviderWithSchedulerForCapability(ctx, &group, "", "", model, excluded, egress.OpenAIUpstreamTransportHTTPSSE, provider.OpenAIEndpointCapabilityTextGeneration, false, false, capability.PlatformGrok)
	if err != nil {
		return nil, searchtools.Selection{}, false, err
	}
	if selected == nil || selected.Provider == nil {
		return nil, searchtools.Selection{}, false, nil
	}
	target := standaloneSearchTarget{source: p.executor, provider: gatewayprovider.ExecutionRecord(selected.Provider)}
	return target, searchtools.Selection{ProviderID: selected.Provider.Record.ID, Acquired: selected.Acquired, Release: selected.ReleaseFunc, WaitPlan: selected.WaitPlan}, true, nil
}

// CompletionRecord returns the selected provider record for synchronous
// completion capture. The pointer belongs to this target, so callers capture
// the needed data before releasing its request resources.
func (t standaloneSearchTarget) CompletionRecord() *provider.Record {
	return t.provider
}

// Execute delegates one standalone search exchange using the selected provider.
func (t standaloneSearchTarget) Execute(ctx context.Context, body []byte) ([]byte, error) {
	return t.source.Execute(ctx, t.provider, body)
}

// ProvideGatewaySearchHTTP binds search admission, slots, completion, and
// selected-provider execution. Configured ping seconds control the concurrency
// helper; optional Key, moderation, and activity services are attached when
// supplied, while the search-tools dependency only orders assembly.
func ProvideGatewaySearchHTTP(executor *gatewayprovider.GrokSearchExecutor, selector *selection.Compatible, funding *admission.FundingAdmission, concurrency *scheduler.ConcurrencyService, keys *apikey.APIKeyService, moderator *moderation.ContentModerationService, workers *completion.UsageRecordWorkerPool, cfg *config.Config, _ *searchtools.Emulator, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.SearchHandler {
	interval := time.Duration(0)
	if cfg != nil {
		// Search slot waiting emits pings at the configured interval in seconds.
		interval = time.Duration(cfg.Gateway.Streaming.WaitPingIntervalSeconds) * time.Second
	}
	ports := gatewayhttp.SearchPorts{Selector: standaloneSearchExecution{selector, executor}, Funding: funding, Concurrency: gatewayhttp.NewConcurrencyHelper(concurrency, gatewayhttp.SSEPingFormatClaude, interval), Recorder: recorders.Forward, Workers: workers}
	if keys != nil {
		ports.Keys = keys
	}
	if moderator != nil {
		ports.Moderation = moderator
	}
	result := gatewayhttp.NewSearchHandler(ports)
	if activity != nil {
		result.BindRequestActivity(activity.Enter)
	}
	return result
}

// provideGrokSearchExecutor binds shared transport and optional dynamic
// endpoint lookup. With readers present, DefaultBaseURL reads the current mode
// through a background context each time the executor requests its default URL.
func provideGrokSearchExecutor(client *transport.Client, readers *gatewayprovider.RuntimeReaders) *gatewayprovider.GrokSearchExecutor {
	out := &gatewayprovider.GrokSearchExecutor{Transport: client}
	if readers != nil {
		out.DefaultBaseURL = func() string {
			return gatewayprovider.GrokBaseURLForMode(readers.Gateway.GetGrokDefaultBaseURLMode(context.Background()))
		}
	}
	return out
}
