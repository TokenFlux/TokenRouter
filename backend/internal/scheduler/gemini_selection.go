package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// GeminiSelectionPorts 只提供既有读取、资格和投影；核心负责查询顺序、选择和粘性提交。
type GeminiSelectionPorts struct {
	Resolve     func(context.Context, *int64) (string, bool, bool, *FlowGroup, error)
	WithGroup   func(context.Context, *FlowGroup) context.Context
	Effective   func(context.Context, *int64) policy.EffectiveSettings
	Sticky      func(context.Context, *int64, string, string, string, map[int64]struct{}, string, bool) *FlowAccount
	List        func(context.Context, *int64, string, bool) ([]FlowAccount, error)
	Eligible    func(context.Context, []FlowAccount, string, map[int64]struct{}, string, bool) []*FlowAccount
	Advanced    func(context.Context, *int64, string, string, []*FlowAccount, policy.EffectiveSettings) *FlowAccount
	Unsupported func(context.Context, []FlowAccount, string, string, map[int64]struct{}, bool) error
	Hydrate     func(context.Context, *FlowAccount) (*FlowAccount, error)
}

// GeminiSelector 保留原混合池与强制平台回退，不取得请求槽或创建另一套故障切换循环。
type GeminiSelector struct {
	ports GeminiSelectionPorts
	cache StickyCache
}

func NewGeminiSelector(ports GeminiSelectionPorts, cache StickyCache) *GeminiSelector {
	return &GeminiSelector{ports: ports, cache: cache}
}

func (s *GeminiSelector) SelectOnly(ctx context.Context, input SelectionInput) (*FlowAccount, error) {
	platform, mixed, forced, group, err := s.ports.Resolve(ctx, input.GroupID)
	if err != nil {
		return nil, err
	}
	if group != nil {
		ctx = s.ports.WithGroup(ctx, group)
	}
	cacheKey := "gemini:" + input.SessionHash
	advanced := group != nil && group.UsesAdvancedScheduler()
	settings := s.ports.Effective(ctx, input.GroupID)
	if !advanced || !settings.StickyWeightedEnabled {
		if value := s.ports.Sticky(ctx, input.GroupID, input.SessionHash, cacheKey, input.RequestedModel, input.ExcludedIDs, platform, mixed); value != nil {
			return value, nil
		}
	}
	values, err := s.ports.List(ctx, input.GroupID, platform, forced)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	if len(values) == 0 && input.GroupID != nil && forced {
		values, err = s.ports.List(ctx, nil, platform, forced)
		if err != nil {
			return nil, fmt.Errorf("query accounts failed: %w", err)
		}
	}
	eligible := s.ports.Eligible(ctx, values, input.RequestedModel, input.ExcludedIDs, platform, mixed)
	var selected *FlowAccount
	if advanced {
		selected = s.ports.Advanced(ctx, input.GroupID, input.SessionHash, cacheKey, eligible, settings)
	} else {
		selected = BestGeminiCandidate(eligible)
	}
	if selected == nil {
		if err := s.ports.Unsupported(ctx, values, input.RequestedModel, platform, input.ExcludedIDs, mixed); err != nil {
			return nil, err
		}
		if input.RequestedModel != "" {
			return nil, fmt.Errorf("no available Gemini accounts supporting model: %s", input.RequestedModel)
		}
		return nil, errors.New("no available Gemini accounts")
	}
	if input.SessionHash != "" {
		var groupID int64
		if input.GroupID != nil {
			groupID = *input.GroupID
		}
		_ = s.cache.SetSessionAccountID(ctx, groupID, cacheKey, selected.ID, time.Hour)
	}
	return s.ports.Hydrate(ctx, selected)
}

// BestGeminiCandidate 保留原遍历顺序和优先级/未使用/OAuth/LRU 决胜规则。
func BestGeminiCandidate(values []*FlowAccount) *FlowAccount {
	var selected *FlowAccount
	for _, value := range values {
		if value != nil && (selected == nil || BetterGeminiCandidate(value, selected)) {
			selected = value
		}
	}
	return selected
}
func BetterGeminiCandidate(candidate, current *FlowAccount) bool {
	if candidate.Priority < current.Priority {
		return true
	}
	if candidate.Priority > current.Priority {
		return false
	}
	switch {
	case candidate.LastUsedAt == nil && current.LastUsedAt != nil:
		return true
	case candidate.LastUsedAt != nil && current.LastUsedAt == nil:
		return false
	case candidate.LastUsedAt == nil && current.LastUsedAt == nil:
		return candidate.Type == capability.AccountTypeOAuth && current.Type != capability.AccountTypeOAuth
	default:
		return candidate.LastUsedAt.Before(*current.LastUsedAt)
	}
}
