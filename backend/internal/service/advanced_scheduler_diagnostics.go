package service

import (
	"context"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"

	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// AdvancedSchedulerScoreDiagnosticRequest 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticAccount 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticGroup 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticGroupSummary 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticResponse 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticContext 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticDetail 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticCandidatePool 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticRanges 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticCandidate 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticScore 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticMetric 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticSetting 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticPolicySignal 保留原诊断值入口。

// AdvancedSchedulerScoreDiagnosticSource 是诊断服务所需的最小账号读取能力。
// 通过窄接口保持服务可独立测试，也避免诊断路径触及凭据读取或写入接口。
type AdvancedSchedulerScoreDiagnosticSource interface {
	GetAccount(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error)
	GetGroup(ctx context.Context, id int64) (*routing.Group, error)
	ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]gatewayprovider.ExecutionAccount, error)
	ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]gatewayprovider.ExecutionAccount, error)
}

// AdvancedSchedulerScoreDiagnosticService 编排只读评分诊断。
// 它复用高级评分核心，但绝不获取并发槽、绑定会话或回写运行时状态。
type AdvancedSchedulerScoreDiagnosticService struct {
	feedback            *schedulercore.RuntimeStats
	schedulerParameters *schedulercore.Parameters
	source              AdvancedSchedulerScoreDiagnosticSource
	concurrencyService  *schedulercore.ConcurrencyService

	gatewayService *GatewayService
	openAIGateway  *OpenAIGatewayService
}

// SetSchedulingServices 注入生产调度服务，供诊断复用只读硬过滤逻辑。
func (s *AdvancedSchedulerScoreDiagnosticService) SetSchedulingServices(gateway *GatewayService, openAIGateway *OpenAIGatewayService) {
	if s == nil {
		return
	}
	s.gatewayService = gateway
	s.openAIGateway = openAIGateway
}

// NewAdvancedSchedulerScoreDiagnosticService 创建高级评分诊断服务。
func NewAdvancedSchedulerScoreDiagnosticService(
	source AdvancedSchedulerScoreDiagnosticSource,
	concurrencyService *schedulercore.ConcurrencyService,

) *AdvancedSchedulerScoreDiagnosticService {
	return &AdvancedSchedulerScoreDiagnosticService{
		source:             source,
		concurrencyService: concurrencyService,
	}
}

func (s *AdvancedSchedulerScoreDiagnosticService) GetOverview(ctx context.Context, id int64) (*policy.AdvancedSchedulerScoreDiagnosticResponse, error) {
	core, _ := s.diagnosticCore()
	return core.GetOverview(ctx, id)
}
func (s *AdvancedSchedulerScoreDiagnosticService) GetDetail(ctx context.Context, id int64, request policy.AdvancedSchedulerScoreDiagnosticRequest) (*policy.AdvancedSchedulerScoreDiagnosticResponse, error) {
	core, _ := s.diagnosticCore()
	return core.GetDetail(ctx, id, request)
}
func (s *AdvancedSchedulerScoreDiagnosticService) loadMap(ctx context.Context, accounts []*gatewayprovider.ExecutionAccount) map[int64]*schedulercore.AccountLoadInfo {
	core, scope := s.diagnosticCore()
	return core.LoadMap(ctx, scope.accounts(accounts))
}
func (s *AdvancedSchedulerScoreDiagnosticService) diagnosticHardFilterReason(ctx context.Context, a *gatewayprovider.ExecutionAccount, g *routing.Group, request policy.AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
	core, scope := s.diagnosticCore()
	return core.HardFilterReason(ctx, scope.account(a), scope.group(g), request, now)
}
func (s *AdvancedSchedulerScoreDiagnosticService) effectiveSettings(ctx context.Context, group *routing.Group) (policy.EffectiveSettings, policy.RuntimeSettings) {
	var parameters *schedulercore.Parameters
	if s != nil {
		parameters = s.schedulerParameters
	}
	runtime := parameters.Runtime(ctx)
	return parameters.Effective(ctx, schedulerGroupOverrides(group)), runtime
}
func (s *AdvancedSchedulerScoreDiagnosticService) prepareEligibilityContext(ctx context.Context, group *routing.Group, accounts []gatewayprovider.ExecutionAccount) context.Context {
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
func (s *AdvancedSchedulerScoreDiagnosticService) diagnosticPlatformFilterReason(
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
			if paused, _ := shouldAutoPauseOpenAIAccountByQuota(ctx, account); paused {
				return "quota_auto_pause"
			}
		}
		if account.View().IsGrok() {
			if paused, _ := shouldAutoPauseGrokAccountByQuota(account); paused {
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
			scheduler := &defaultOpenAIAccountScheduler{service: s.openAIGateway}
			if !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(account), func(id int64) *accountcore.Record {
				return gatewayprovider.ExecutionRecord(scheduler.lookupShadowParentAccount(ctx, id))
			}) {
				return "shadow_parent_unhealthy"
			}
			groupID := group.ID
			if s.openAIGateway.needsUpstreamChannelRestrictionCheck(ctx, &groupID) &&
				s.openAIGateway.isUpstreamRoutingModelRestrictedByChannel(ctx, groupID, account, model, false) {
				return "channel_upstream_restricted"
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
		if s.gatewayService.needsUpstreamChannelRestrictionCheck(ctx, &groupID) &&
			s.gatewayService.isUpstreamModelRestrictedByChannel(ctx, groupID, account, model) {
			return "channel_upstream_restricted"
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
