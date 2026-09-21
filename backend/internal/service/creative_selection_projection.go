// 兼容调度端口仅投影选择结果，候选与反馈状态继续使用唯一调度实例。
package service

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/creative"
)

func (e *CreativeExecutor) selectionProjection(account *Account, result *AccountSelectionResult) *creative.Selection {
	if account == nil {
		return nil
	}
	out := &creative.Selection{AccountID: account.ID, Platform: account.Platform}
	if result != nil {
		out.Acquired = result.Acquired
		out.Waiting = result.WaitPlan != nil
		out.Release = result.ReleaseFunc
	}
	out.ResolveModel = func(ctx context.Context, model string) string {
		return resolveAccountUpstreamModel(ctx, account, model)
	}
	out.Execute = func(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, model string) ([]creative.CreativeOutput, error) {
		target := e.nativeTarget(account)
		switch account.Platform {
		case capability.PlatformOpenAI:
			return target.ExecuteOpenAI(ctx, run, payload, model)
		case capability.PlatformGrok:
			return target.ExecuteGrok(ctx, run, payload, model)
		case capability.PlatformGemini:
			return target.ExecuteGemini(ctx, run, payload, model)
		default:
			return nil, creative.CreativeNonRetryableError("creative executor unsupported account platform %s", account.Platform)
		}
	}
	out.Report = func(model string, success bool) {
		e.reportScheduleResult(&CreativeExecution{Account: account, Selection: result, UpstreamModel: model}, account.ID, success)
	}
	return out
}
func (e *CreativeExecutor) nativeExecutor(selected **AccountSelectionResult) *creative.Executor {
	out := &creative.Executor{Timeout: e.executeTimeout()}
	if e.bindings.Groups != nil {
		out.Group = func(ctx context.Context, id int64) (*creative.ExecutionGroup, error) {
			g, err := e.bindings.Groups.GetByIDLite(ctx, id)
			if g == nil {
				return nil, err
			}
			value := &creative.ExecutionGroup{Platform: g.Platform}
			if g.ResponsesImagePolicy != "" || g.ProtocolFallbacks != nil {
				value.ConfigureContext = func(ctx context.Context, platform, operation string) context.Context {
					return requeststate.WithClientProtocol(requeststate.WithGroup(ctx, g), creative.OperationProtocol(platform, operation))
				}
			}
			return value, err
		}
	}
	project := func(result *AccountSelectionResult, err error) (*creative.Selection, error) {
		if selected != nil {
			*selected = result
		}
		if result == nil {
			return nil, err
		}
		return e.selectionProjection(result.Account, result), err
	}
	if e.bindings.OpenAI != nil {
		out.OpenAI = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			v, err := e.bindings.OpenAI(ctx, run)
			return project(v, err)
		}
	}
	if e.bindings.Grok != nil {
		out.Grok = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			v, err := e.bindings.Grok(ctx, run)
			return project(v, err)
		}
	}
	if e.bindings.Gemini != nil {
		out.Gemini = func(ctx context.Context, run creative.CreativeRun) (*creative.Selection, error) {
			v, err := e.bindings.Gemini(ctx, run)
			return project(v, err)
		}
	}

	return out
}

// CreativeExecutionBindings 只暴露调度、受控目标和反馈，不向任务核心传递旧聚合服务。
type CreativeExecutionBindings struct {
	Groups               creativeprovider.ExecutionGroups
	OpenAI, Grok, Gemini func(context.Context, creative.CreativeRun) (*AccountSelectionResult, error)
	Target               func(*Account) *creativeprovider.Target
	Report               func(*CreativeExecution, int64, bool)
}

// legacyCreativeExecutionBindings 只在保留构造入口把已有实例的方法投影为窄能力。
func legacyCreativeExecutionBindings(cfg *config.Config, groups creativeprovider.ExecutionGroups, gateway *OpenAIGatewayService, generic *GatewayService, tokens *accountcore.GeminiTokenSource, e *CreativeExecutor) CreativeExecutionBindings {
	out := CreativeExecutionBindings{Groups: groups, Target: legacyCreativeTargetFactory(gateway, cfg, tokens, e)}
	if gateway != nil {
		out.OpenAI = func(ctx context.Context, run creative.CreativeRun) (*AccountSelectionResult, error) {
			id := run.GroupID
			v, _, err := gateway.SelectAccountWithSchedulerForImages(ctx, &id, "", run.Model, nil, OpenAIImagesCapabilityNative)
			return v, err
		}
		out.Grok = func(ctx context.Context, run creative.CreativeRun) (*AccountSelectionResult, error) {
			id := run.GroupID
			v, _, err := gateway.SelectAccountWithSchedulerForCapability(ctx, &id, "", "", run.Model, nil, egress.OpenAIUpstreamTransportHTTPSSE, accountcore.OpenAIEndpointCapabilityGrokMediaGeneration, false, false, capability.PlatformGrok)
			return v, err
		}
	}
	if generic != nil {
		out.Gemini = func(ctx context.Context, run creative.CreativeRun) (*AccountSelectionResult, error) {
			id := run.GroupID
			return generic.SelectAccountWithLoadAwareness(ctx, &id, "", run.Model, nil, "", 0)
		}
	}
	out.Report = func(execution *CreativeExecution, id int64, success bool) {
		if execution == nil || execution.Selection == nil || id <= 0 {
			return
		}
		switch execution.Account.Platform {
		case capability.PlatformOpenAI, capability.PlatformGrok:
			if gateway != nil {
				gateway.ReportOpenAIAccountScheduleResultForSelection(execution.Selection, id, execution.UpstreamModel, success, nil)
			}
		case capability.PlatformGemini:
			if generic != nil {
				generic.ReportAdvancedAccountScheduleResult(execution.Selection, id, success, nil)
			}
		}
	}
	return out
}
