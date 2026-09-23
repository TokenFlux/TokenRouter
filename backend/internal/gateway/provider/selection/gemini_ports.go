package selection

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// geminiSelector 的关联表只存在于本次调用，返回值继续使用原账号读取快照。
func (s *Gemini) geminiSelector() (*scheduler.GeminiSelector, *projectionScope) {
	scope := &projectionScope{accounts: map[uint64]*provider.ExecutionAccount{}, groups: map[uint64]*routing.Group{}}
	ports := scheduler.GeminiSelectionPorts{
		Resolve: func(ctx context.Context, id *int64) (string, bool, bool, *scheduler.FlowGroup, error) {
			platform, mixed, forced, group, err := s.resolvePlatformAndSchedulingMode(ctx, id)
			return platform, mixed, forced, scope.group(group), err
		},
		WithGroup: func(ctx context.Context, group *scheduler.FlowGroup) context.Context {
			return requeststate.WithGroup(ctx, scope.oldGroup(group))
		},
		Effective: s.advancedSchedulerEffectiveSettingsForRequest,
		Sticky: func(ctx context.Context, id *int64, hash, key, model string, excluded map[int64]struct{}, platform string, mixed bool) *scheduler.FlowAccount {
			return scope.account(s.tryStickySessionHit(ctx, id, hash, key, model, excluded, platform, mixed))
		},
		List: func(ctx context.Context, id *int64, platform string, forced bool) ([]scheduler.FlowAccount, error) {
			values, err := s.listSchedulableAccountsOnce(ctx, id, platform, forced)
			return scope.values(values), err
		},
		Eligible: func(ctx context.Context, values []scheduler.FlowAccount, model string, excluded map[int64]struct{}, platform string, mixed bool) []*scheduler.FlowAccount {
			return scope.pointers(s.eligibleGeminiAccounts(ctx, scope.oldValues(values), model, excluded, platform, mixed))
		},
		Advanced: func(ctx context.Context, id *int64, hash, key string, values []*scheduler.FlowAccount, settings policy.EffectiveSettings) *scheduler.FlowAccount {
			var targets []*provider.ExecutionAccount
			if values != nil {
				targets = make([]*provider.ExecutionAccount, len(values))
				for i, v := range values {
					targets[i] = scope.oldAccount(v)
				}
			}
			return scope.account(s.selectAdvancedGeminiAccount(ctx, id, hash, key, targets, settings))
		},
		Unsupported: func(ctx context.Context, values []scheduler.FlowAccount, model, platform string, excluded map[int64]struct{}, mixed bool) error {
			return s.groupModelUnsupportedErrorIfApplicable(ctx, scope.oldValues(values), model, platform, excluded, mixed)
		},
		Hydrate: func(ctx context.Context, value *scheduler.FlowAccount) (*scheduler.FlowAccount, error) {
			out, err := s.hydrateSelectedAccount(ctx, scope.oldAccount(value))
			return scope.account(out), err
		},
	}
	return scheduler.NewGeminiSelector(ports, s.cache), scope
}
