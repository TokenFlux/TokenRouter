package selection

import (
	"context"
	"slices"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// diagnosticProjectionScope 只为当前诊断保存投影前后的对应关系，保证同 ID 的不同快照不混淆。
type diagnosticScope struct {
	source        DiagnosticSource
	next          uint64
	accountValues map[uint64]*gatewayprovider.ExecutionAccount
	groupValues   map[uint64]*routing.Group
}

func (s *Diagnostics) diagnosticCore() (*schedulercore.DiagnosticService, *diagnosticScope) {
	scope := &diagnosticScope{source: s.source, accountValues: map[uint64]*gatewayprovider.ExecutionAccount{}, groupValues: map[uint64]*routing.Group{}}
	var source schedulercore.DiagnosticSource
	if s.source != nil {
		source = scope
	}
	core := schedulercore.NewDiagnosticService(source, s.concurrencyService, schedulercore.DiagnosticPorts{
		Now: time.Now,
		Stats: func() *schedulercore.RuntimeStats {
			return s.feedback
		},
		Effective: func(ctx context.Context, g *schedulercore.DiagnosticGroup) (policy.EffectiveSettings, policy.RuntimeSettings) {
			effective, runtime := s.effectiveSettings(ctx, scope.originalGroup(g))
			return effective, runtime
		},
		Prepare: func(ctx context.Context, g *schedulercore.DiagnosticGroup, accounts []schedulercore.DiagnosticAccount) context.Context {
			var values []gatewayprovider.ExecutionAccount
			if accounts != nil {
				values = make([]gatewayprovider.ExecutionAccount, len(accounts))
				for i, v := range accounts {
					values[i] = *scope.accountValues[v.ProjectionID]
				}
			}
			return s.prepareEligibilityContext(ctx, scope.originalGroup(g), values)
		},
		Filter: func(ctx context.Context, a *schedulercore.DiagnosticAccount, g *schedulercore.DiagnosticGroup, request schedulercore.AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
			return s.diagnosticPlatformFilterReason(ctx, scope.accountValues[a.ProjectionID], scope.originalGroup(g), request, now)
		},
		Quota: func(id uint64, now time.Time) float64 { return openAIQuotaHeadroomFactor(scope.accountValues[id], now) },
	})
	return core, scope
}
func (s *diagnosticScope) originalGroup(g *schedulercore.DiagnosticGroup) *routing.Group {
	if g == nil {
		return nil
	}
	return s.groupValues[g.ProjectionID]
}
func (s *diagnosticScope) group(v *routing.Group) *schedulercore.DiagnosticGroup {
	if v == nil {
		return nil
	}
	s.next++
	id := s.next
	s.groupValues[id] = v
	return &schedulercore.DiagnosticGroup{ProjectionID: id, ID: v.ID, Name: v.Name, Platform: v.Platform, SortOrder: v.SortOrder, Advanced: v.UsesAdvancedScheduler(), RequirePrivacySet: v.RequirePrivacySet, AdvancedSchedulerOverrides: accessview.CloneGroupAdvancedSchedulerOverrides(v.AdvancedSchedulerOverrides)}
}
func (s *diagnosticScope) account(v *gatewayprovider.ExecutionAccount) *schedulercore.DiagnosticAccount {
	if v == nil {
		return nil
	}
	s.next++
	id := s.next
	s.accountValues[id] = v
	a := &schedulercore.DiagnosticAccount{ProjectionID: id, ID: v.Record.ID, Name: v.Record.Name, Platform: v.Record.Platform, Type: v.Record.Type, Status: v.Record.Status, Priority: v.Record.Priority, LoadFactor: v.View().EffectiveLoadFactor(), Schedulable: v.Record.Schedulable, AutoPauseOnExpired: v.Record.AutoPauseOnExpired, PrivacySet: v.View().IsPrivacySet(), MixedScheduling: v.View().IsMixedSchedulingEnabled(), SubscriptionPriority: v.View().IsOpenAIChatGPTSubscription(), ExpiresAt: v.Record.ExpiresAt, OverloadUntil: v.Record.OverloadUntil, RateLimitResetAt: v.Record.RateLimitResetAt, TempUnschedulableUntil: v.Record.TempUnschedulableUntil, SessionWindowEnd: v.Record.SessionWindowEnd, GroupIDs: slices.Clone(v.Record.GroupIDs)}
	if v.Record.AccountGroups != nil {
		a.AccountGroups = make([]schedulercore.DiagnosticAccountGroup, len(v.Record.AccountGroups))
		for i, g := range v.Record.AccountGroups {
			a.AccountGroups[i] = schedulercore.DiagnosticAccountGroup{Group: s.group((*routing.Group)(g.Group))}
		}
	}
	if v.Record.Groups != nil {
		a.Groups = make([]*schedulercore.DiagnosticGroup, len(v.Record.Groups))
		for i, g := range v.Record.Groups {
			a.Groups[i] = s.group((*routing.Group)(g))
		}
	}
	return a
}
func (s *diagnosticScope) accounts(values []*gatewayprovider.ExecutionAccount) []*schedulercore.DiagnosticAccount {
	if values == nil {
		return nil
	}
	out := make([]*schedulercore.DiagnosticAccount, len(values))
	for i, v := range values {
		out[i] = s.account(v)
	}
	return out
}
func (s *diagnosticScope) accountSlice(values []gatewayprovider.ExecutionAccount) []schedulercore.DiagnosticAccount {
	if values == nil {
		return nil
	}
	out := make([]schedulercore.DiagnosticAccount, len(values))
	for i := range values {
		out[i] = *s.account(&values[i])
	}
	return out
}
func (s *diagnosticScope) GetAccount(ctx context.Context, id int64) (*schedulercore.DiagnosticAccount, error) {
	v, err := s.source.GetAccount(ctx, id)
	return s.account(v), err
}
func (s *diagnosticScope) GetGroup(ctx context.Context, id int64) (*schedulercore.DiagnosticGroup, error) {
	v, err := s.source.GetGroup(ctx, id)
	return s.group(v), err
}
func (s *diagnosticScope) ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, kind, status, search string, groupID int64, privacy string) ([]schedulercore.DiagnosticAccount, error) {
	v, err := s.source.ListAccountsForSchedulerScoreFilter(ctx, platform, kind, status, search, groupID, privacy)
	return s.accountSlice(v), err
}
func (s *diagnosticScope) ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]schedulercore.DiagnosticAccount, error) {
	v, err := s.source.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, groupID, platform)
	return s.accountSlice(v), err
}
