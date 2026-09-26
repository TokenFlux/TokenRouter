package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// provideCreativeExecutor 直接绑定任务核心和受控执行目标，不建立旧任务运行时或复制状态。
func provideCreativeExecutor(cfg *config.Config, groups creativeprovider.ExecutionGroups, targets *gatewayprovider.CreativeTargets, generic *selection.Generic, choices *selection.Compatible) *creative.Executor {
	timeout := 5 * time.Minute
	if cfg != nil && cfg.Creative.ExecuteTimeoutSeconds > 0 {
		timeout = time.Duration(cfg.Creative.ExecuteTimeoutSeconds) * time.Second
	}
	out := &creative.Executor{Timeout: timeout}
	if groups != nil {
		out.Group = func(ctx context.Context, id int64) (*creative.ExecutionGroup, error) {
			group, err := groups.GetByIDLite(ctx, id)
			if group == nil {
				return nil, err
			}
			value := &creative.ExecutionGroup{Platform: group.Platform, RoutingPolicy: group.RoutingPolicy.Clone()}
			if group.ResponsesImagePolicy != "" || group.ProtocolFallbacks != nil {
				value.ConfigureContext = func(ctx context.Context, platform, operation string) context.Context {
					return requeststate.WithClientProtocol(requeststate.WithGroup(ctx, group), creative.OperationProtocol(platform, operation))
				}
			}
			return value, err
		}
	}
	project := func(result *gatewayprovider.SelectionResult, err error) (*creative.Selection, error) {
		if result == nil || result.Account == nil {
			return nil, err
		}
		value := result.Account
		selection := &creative.Selection{AccountID: value.Record.ID, Platform: value.Record.Platform, Acquired: result.Acquired, Waiting: result.WaitPlan != nil, Release: result.ReleaseFunc}
		selection.ResolveModel = func(ctx context.Context, model string) string {
			policy := gatewayprovider.ExecutionModelPolicy(value)
			if !policy.Supports(ctx, model) {
				return ""
			}
			return policy.UpstreamModel(ctx, model)
		}
		selection.Execute = func(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, model string) ([]creative.CreativeOutput, error) {
			return targets.ForAccount(value).ExecutePlatform(ctx, value.Record.Platform, run, payload, model)
		}
		selection.Report = func(model string, success bool) {
			if value.Record.ID <= 0 {
				return
			}
			switch value.Record.Platform {
			case capability.PlatformOpenAI, capability.PlatformGrok:
				if targets != nil {
					choices.ReportOpenAIAccountScheduleResultForSelection(result, value.Record.ID, model, success, nil)
				}
			case capability.PlatformGemini:
				if generic != nil {
					generic.ReportAdvancedAccountScheduleResult(result, value.Record.ID, success, nil)
				}
			}
		}
		return selection, err
	}
	if targets != nil {
		out.OpenAI = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			id := run.GroupID
			value, _, err := choices.SelectAccountWithSchedulerForImages(ctx, &id, "", run.Model, nil, account.OpenAIImagesCapabilityNative)
			return project(value, err)
		}
		out.Grok = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			id := run.GroupID
			value, _, err := choices.SelectAccountWithSchedulerForCapability(ctx, &id, "", "", run.Model, nil, egress.OpenAIUpstreamTransportHTTPSSE, account.OpenAIEndpointCapabilityGrokMediaGeneration, false, false, capability.PlatformGrok)
			return project(value, err)
		}
	}
	if generic != nil {
		out.Gemini = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			id := run.GroupID
			value, err := generic.SelectAccountWithLoadAwareness(ctx, &id, "", run.Model, nil, "", 0)
			return project(value, err)
		}
	}
	return out
}
