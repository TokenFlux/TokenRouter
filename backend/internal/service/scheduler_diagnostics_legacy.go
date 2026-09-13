// 旧诊断能力仅投影和调用；本次请求的关联表不会跨请求保存，也不包含业务规则。
package service

import (
	"context"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// diagnosticProjectionScope 只为当前诊断保存投影前后的对应关系，保证同 ID 的不同快照不混淆。
type diagnosticProjectionScope struct {
	source        AdvancedSchedulerScoreDiagnosticSource
	next          uint64
	accountValues map[uint64]*Account
	groupValues   map[uint64]*Group
}

func (s *AdvancedSchedulerScoreDiagnosticService) diagnosticCore() (*scheduler.DiagnosticService, *diagnosticProjectionScope) {
	scope := &diagnosticProjectionScope{source: s.source, accountValues: map[uint64]*Account{}, groupValues: map[uint64]*Group{}}
	var source scheduler.DiagnosticSource
	if s.source != nil {
		source = scope
	}
	core := scheduler.NewDiagnosticService(source, s.concurrencyService, scheduler.DiagnosticPorts{
		Now: time.Now,
		Stats: func() *scheduler.RuntimeStats {
			if s.rateLimitService == nil {
				return nil
			}
			return schedulerStats(s.rateLimitService.AdvancedSchedulerRuntimeStats())
		},
		Effective: func(ctx context.Context, g *scheduler.DiagnosticGroup) (policy.EffectiveSettings, policy.RuntimeSettings) {
			effective, runtime := s.effectiveSettings(ctx, scope.originalGroup(g))
			return policy.EffectiveSettings{StickyWeightedEnabled: effective.stickyWeightedEnabled, SubscriptionPriorityEnabled: effective.subscriptionPriorityEnabled, TopK: effective.topK, Weights: policy.ScoreWeights(effective.weights), Feedback: policy.FeedbackConfig{ErrorRateAlpha: effective.feedback.errorRateAlpha, TtftAlpha: effective.feedback.ttftAlpha}, StickyEscape: policy.StickyEscapeConfig{Enabled: effective.stickyEscape.enabled, TtftMs: effective.stickyEscape.ttftMs, ErrorRate: effective.stickyEscape.errorRate}}, schedulerRuntimeSettings(runtime)
		},
		Prepare: func(ctx context.Context, g *scheduler.DiagnosticGroup, accounts []scheduler.DiagnosticAccount) context.Context {
			var values []Account
			if accounts != nil {
				values = make([]Account, len(accounts))
				for i, v := range accounts {
					values[i] = *scope.accountValues[v.ProjectionID]
				}
			}
			return s.prepareEligibilityContext(ctx, scope.originalGroup(g), values)
		},
		Filter: func(ctx context.Context, a *scheduler.DiagnosticAccount, g *scheduler.DiagnosticGroup, request scheduler.AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
			return s.diagnosticPlatformFilterReason(ctx, scope.accountValues[a.ProjectionID], scope.originalGroup(g), request, now)
		},
		Quota: func(id uint64, now time.Time) float64 { return openAIQuotaHeadroomFactor(scope.accountValues[id], now) },
	})
	return core, scope
}
func (s *diagnosticProjectionScope) originalGroup(g *scheduler.DiagnosticGroup) *Group {
	if g == nil {
		return nil
	}
	return s.groupValues[g.ProjectionID]
}
func (s *diagnosticProjectionScope) group(v *Group) *scheduler.DiagnosticGroup {
	if v == nil {
		return nil
	}
	s.next++
	id := s.next
	s.groupValues[id] = v
	return &scheduler.DiagnosticGroup{ProjectionID: id, ID: v.ID, Name: v.Name, Platform: v.Platform, SortOrder: v.SortOrder, Advanced: v.UsesAdvancedScheduler(), RequirePrivacySet: v.RequirePrivacySet, AdvancedSchedulerOverrides: CloneGroupAdvancedSchedulerOverrides(v.AdvancedSchedulerOverrides)}
}
func (s *diagnosticProjectionScope) account(v *Account) *scheduler.DiagnosticAccount {
	if v == nil {
		return nil
	}
	s.next++
	id := s.next
	s.accountValues[id] = v
	a := &scheduler.DiagnosticAccount{ProjectionID: id, ID: v.ID, Name: v.Name, Platform: v.Platform, Type: v.Type, Status: v.Status, Priority: v.Priority, LoadFactor: v.EffectiveLoadFactor(), Schedulable: v.Schedulable, AutoPauseOnExpired: v.AutoPauseOnExpired, PrivacySet: v.IsPrivacySet(), MixedScheduling: v.IsMixedSchedulingEnabled(), SubscriptionPriority: v.IsOpenAIChatGPTSubscription(), ExpiresAt: v.ExpiresAt, OverloadUntil: v.OverloadUntil, RateLimitResetAt: v.RateLimitResetAt, TempUnschedulableUntil: v.TempUnschedulableUntil, SessionWindowEnd: v.SessionWindowEnd, GroupIDs: slices.Clone(v.GroupIDs)}
	if v.AccountGroups != nil {
		a.AccountGroups = make([]scheduler.DiagnosticAccountGroup, len(v.AccountGroups))
		for i, g := range v.AccountGroups {
			a.AccountGroups[i] = scheduler.DiagnosticAccountGroup{Group: s.group(g.Group)}
		}
	}
	if v.Groups != nil {
		a.Groups = make([]*scheduler.DiagnosticGroup, len(v.Groups))
		for i, g := range v.Groups {
			a.Groups[i] = s.group(g)
		}
	}
	return a
}
func (s *diagnosticProjectionScope) accounts(values []*Account) []*scheduler.DiagnosticAccount {
	if values == nil {
		return nil
	}
	out := make([]*scheduler.DiagnosticAccount, len(values))
	for i, v := range values {
		out[i] = s.account(v)
	}
	return out
}
func (s *diagnosticProjectionScope) accountSlice(values []Account) []scheduler.DiagnosticAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.DiagnosticAccount, len(values))
	for i := range values {
		out[i] = *s.account(&values[i])
	}
	return out
}
func (s *diagnosticProjectionScope) GetAccount(ctx context.Context, id int64) (*scheduler.DiagnosticAccount, error) {
	v, err := s.source.GetAccount(ctx, id)
	return s.account(v), err
}
func (s *diagnosticProjectionScope) GetGroup(ctx context.Context, id int64) (*scheduler.DiagnosticGroup, error) {
	v, err := s.source.GetGroup(ctx, id)
	return s.group(v), err
}
func (s *diagnosticProjectionScope) ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, kind, status, search string, groupID int64, privacy string) ([]scheduler.DiagnosticAccount, error) {
	v, err := s.source.ListAccountsForSchedulerScoreFilter(ctx, platform, kind, status, search, groupID, privacy)
	return s.accountSlice(v), err
}
func (s *diagnosticProjectionScope) ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]scheduler.DiagnosticAccount, error) {
	v, err := s.source.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, groupID, platform)
	return s.accountSlice(v), err
}
