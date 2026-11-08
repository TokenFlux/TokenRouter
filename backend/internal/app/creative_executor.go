package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// creative_executor.go constructs creative execution adapters that retain the
// selected provider and scheduler callbacks.
// Creative generation selects providers under the saved task's group policy.
// The executor reads that policy during preparation, carries the scheduler's
// release and feedback callbacks into the selected target, and uses shared HTTP
// execution credentials and transport for generation.
//
// Each selection closes over its execution provider for model resolution,
// attribute lookup, and generation. The task receives the provider identifier
// together with acquired or waiting state and the release callback from that
// same result. Feedback is sent through the scheduler for the selected
// platform, keeping execution and outcome reporting attached to the provider
// whose selection produced those callbacks.

// provideCreativeExecutor binds group policy and provider selection for
// creative preparation. Execution defaults to five minutes unless cfg supplies
// a positive timeout; OpenAI and Grok require targets and choices, while Gemini
// selection is bound when generic is present. Selected targets retain their
// scheduler release and feedback callbacks.
func provideCreativeExecutor(cfg *config.Config, groups creativeprovider.ExecutionGroups, targets *gatewayprovider.CreativeTargets, generic *selection.Generic, choices *selection.Compatible, attributes *routing.ModelAttributeService) *creative.Executor {
	timeout := 5 * time.Minute
	if cfg != nil && cfg.Creative.ExecuteTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Creative.ExecuteTimeoutSeconds) * time.Second
	}
	out := &creative.Executor{Timeout: timeout, Attributes: attributes}
	if groups != nil {
		out.Group = func(ctx context.Context, id int64) (*creative.ExecutionGroup, error) {
			group, err := groups.GetByIDLite(ctx, id)
			if group == nil {
				return nil, err
			}
			value := &creative.ExecutionGroup{RoutingPolicy: group.RoutingPolicy.Clone(), AllowsOperation: func(platform, operation string) bool {
				return group.IsActive() && !group.ClaudeCodeOnly && group.AllowsClientProtocol(creative.OperationProtocol(platform, operation))
			}}
			value.ConfigureContext = func(ctx context.Context, platform, operation string) context.Context {
				return requeststate.WithClientProtocol(requeststate.WithGroup(apikey.WithForcePlatform(ctx, platform), group), creative.OperationProtocol(platform, operation))
			}
			return value, err
		}
	}
	project := func(result *gatewayprovider.SelectionResult, err error) (*creative.Selection, error) {
		if result == nil || result.Provider == nil {
			return nil, err
		}
		value := result.Provider
		selection := &creative.Selection{ProviderID: value.Record.ID, Platform: value.Record.Platform, Acquired: result.Acquired, Waiting: result.WaitPlan != nil, Release: result.ReleaseFunc}
		selection.ResolveAttributes = func(ctx context.Context, model string) (modelcatalog.Resolution, error) {
			return gatewayprovider.ResolveModelAttributes(ctx, value, model, attributes)
		}
		selection.ResolveModel = func(ctx context.Context, model string) string {
			policy := gatewayprovider.ExecutionModelPolicy(value)
			if !policy.Supports(ctx, model) {
				return ""
			}
			return policy.UpstreamModel(ctx, model)
		}
		selection.Execute = func(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, model string) ([]creative.CreativeOutput, error) {
			if run.FrameCount > 0 {
				resolved, err := gatewayprovider.ResolveModelAttributes(ctx, value, model, attributes)
				if err != nil {
					return nil, err
				}
				if err := gatewayprovider.RequireImageGIF(value, creative.OperationProtocol(value.Record.Platform, run.Operation), resolved.Attributes); err != nil {
					return nil, creative.CreativeNonRetryableError("GIF output is unavailable on the selected provider")
				}
			}
			return targets.ForProvider(value).ExecutePlatform(ctx, value.Record.Platform, run, payload, model)
		}
		selection.Report = func(model string, success bool) {
			if value.Record.ID <= 0 {
				return
			}
			switch value.Record.Platform {
			case capability.PlatformOpenAI, capability.PlatformGrok:
				if targets != nil {
					choices.ReportOpenAIProviderScheduleResultForSelection(result, value.Record.ID, model, success, nil)
				}
			case capability.PlatformGemini:
				if generic != nil {
					generic.ReportAdvancedProviderScheduleResult(result, value.Record.ID, success, nil)
				}
			}
		}
		return selection, err
	}
	if targets != nil {
		out.OpenAI = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			id := run.GroupID
			ctx = media.WithImageGIFRequirement(ctx, run.FrameCount > 0)
			value, _, err := choices.SelectProviderWithSchedulerForImages(ctx, &id, "", run.Model, nil, provider.OpenAIImagesCapabilityNative)
			return project(value, err)
		}
		out.Grok = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			id := run.GroupID
			value, _, err := choices.SelectProviderWithSchedulerForCapability(ctx, &id, "", "", run.Model, nil, egress.OpenAIUpstreamTransportHTTPSSE, provider.OpenAIEndpointCapabilityGrokMediaGeneration, false, false, capability.PlatformGrok)
			return project(value, err)
		}
	}
	if generic != nil {
		out.Gemini = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			id := run.GroupID
			value, err := generic.SelectProviderWithLoadAwareness(ctx, &id, "", run.Model, nil)
			return project(value, err)
		}
	}
	return out
}

// provideCreativeTargets connects creative execution to the auxiliary request
// dependencies and activity barrier. auxiliary and activity are required; the
// returned target factory shares credentials, identity, and transport, while
// Gemini uses the supplied token source.
func provideCreativeTargets(cfg *config.Config, auxiliary *gatewayhttp.OpenAIAuxiliary, tokens *provider.GeminiTokenSource, activity *gatewayRequestActivity) *gatewayprovider.CreativeTargets {
	requests := auxiliary.Requests
	return &gatewayprovider.CreativeTargets{
		Requests:       requests,
		ImageResponses: requests.BuildImageResponses,
		Credentials:    requests.Credentials,
		Identity:       requests.Identity,
		Transport:      requests.Transport,
		Routes:         provideGrokRoutes(cfg, nil),
		GeminiTokens:   tokens,
		Enter:          activity.Enter,
	}
}
