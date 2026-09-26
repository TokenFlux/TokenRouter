package selection

import (
	"context"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

func (s *Diagnostics) GetOverview(ctx context.Context, id int64) (*policy.AdvancedSchedulerScoreDiagnosticResponse, error) {
	core, _ := s.diagnosticCore()
	return core.GetOverview(ctx, id)
}

func (s *Diagnostics) GetDetail(ctx context.Context, id int64, request policy.AdvancedSchedulerScoreDiagnosticRequest) (*policy.AdvancedSchedulerScoreDiagnosticResponse, error) {
	core, _ := s.diagnosticCore()
	return core.GetDetail(ctx, id, request)
}

func (s *Diagnostics) effectiveSettings(ctx context.Context, group *routing.Group) (policy.EffectiveSettings, policy.RuntimeSettings) {
	var parameters *schedulercore.Parameters
	if s != nil {
		parameters = s.schedulerParameters
	}
	runtime := parameters.Runtime(ctx)
	return parameters.Effective(ctx, schedulerGroupOverrides(group)), runtime
}

func (s *Diagnostics) prepareEligibilityContext(ctx context.Context, group *routing.Group, accounts []gatewayprovider.ExecutionAccount) context.Context {
	if s == nil {
		return ctx
	}
	if s.gatewayService != nil {
		ctx = s.gatewayService.withGroupContext(ctx, group)
		ctx = s.gatewayService.withWindowCostPrefetch(ctx, accounts)
		ctx = s.gatewayService.withRPMPrefetch(ctx, accounts)
	}
	if s.openAIGateway != nil && group != nil && (group.Platform == capability.PlatformOpenAI || group.Platform == capability.PlatformGrok) {
		ctx = s.openAIGateway.withOpenAIQuotaAutoPauseContext(ctx)
	}
	return ctx
}

func (s *Diagnostics) diagnosticPlatformFilterReason(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	group *routing.Group,
	request policy.AdvancedSchedulerScoreDiagnosticRequest,
	now time.Time,
) string {
	model := strings.TrimSpace(request.RequestedModel)
	if group != nil && (group.Platform == capability.PlatformOpenAI || group.Platform == capability.PlatformGrok) {
		if !gatewayprovider.ExecutionModelPolicy(account).Schedulable(ctx, model) {
			return "model_runtime_blocked"
		}
		if account.View().IsOpenAI() {
			if paused, _ := gatewayprovider.OpenAIQuotaPause(ctx, account); paused {
				return "quota_auto_pause"
			}
		}
		if account.View().IsGrok() {
			if paused, _ := gatewayprovider.GrokQuotaPause(account); paused {
				return "quota_auto_pause"
			}
		}
		if !gatewayprovider.ExecutionModelPolicy(account).SupportsCompatibleRouting(ctx, model) {
			return "model_unsupported"
		}
		if s != nil && s.openAIGateway != nil {
			if s.openAIGateway.isOpenAIAccountRequestRuntimeBlocked(account, model) {
				return "runtime_blocked"
			}
			if s.openAIGateway.isOpenAIProxyStreamQuarantined(ctx, account) {
				return "proxy_stream_quarantined"
			}
			scheduler := &compatiblePicker{service: s.openAIGateway}
			if !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(account), func(id int64) *accountcore.Record {
				return gatewayprovider.ExecutionRecord(scheduler.lookupShadowParentAccount(ctx, id))
			}) {
				return "shadow_parent_unhealthy"
			}
			groupID := group.ID
			if s.openAIGateway.NeedsUpstreamGroupRestriction(ctx, &groupID) &&
				s.openAIGateway.UpstreamRoutingModelRestricted(ctx, groupID, account, model, false) {
				return "group_upstream_restricted"
			}
		}
		return ""
	}

	if s != nil && s.gatewayService != nil {
		if model != "" && !s.gatewayService.isModelSupportedByAccountWithContext(ctx, account, model) {
			return "model_unsupported"
		}
		if !s.gatewayService.isAccountSchedulableForModelSelection(ctx, account, model) {
			return "model_runtime_blocked"
		}
		if !s.gatewayService.isAccountSchedulableForQuota(account) {
			return "quota_exceeded"
		}
		isSticky := account.Record.ID == request.StickyAccountID
		if !s.gatewayService.isAccountSchedulableForWindowCost(ctx, account, isSticky) {
			return "window_cost_exceeded"
		}
		if !s.gatewayService.isAccountSchedulableForRPM(ctx, account, isSticky) {
			return "rpm_exceeded"
		}
		groupID := group.ID
		if s.gatewayService.needsUpstreamGroupRestrictionCheck(ctx, &groupID) &&
			s.gatewayService.isUpstreamModelRestrictedByGroup(ctx, groupID, account, model) {
			return "group_upstream_restricted"
		}
		return ""
	}

	if model != "" && !gatewayprovider.ExecutionProtocolRecord(account).IsModelSupported(model, accountprovider.ModelDefaults(), accountprovider.ModelRules(gatewayprovider.ExecutionProtocolRecord(account))) {
		return "model_unsupported"
	}
	if !gatewayprovider.ExecutionModelPolicy(account).Schedulable(ctx, model) {
		return "model_runtime_blocked"
	}
	return ""
}
