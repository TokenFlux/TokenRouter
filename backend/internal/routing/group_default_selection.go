// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"context"
	"strings"
)

// DefaultGroupCandidate 只保留默认选择需要的名称与显式标记。
type DefaultGroupCandidate struct {
	Name      string
	IsDefault bool
}

// DefaultGroupIndex 返回原顺序中的候选下标；未命中不会退化为任选一个分组。
func DefaultGroupIndex(platform string, groups []DefaultGroupCandidate) int {
	for i := range groups {
		if groups[i].IsDefault {
			return i
		}
	}

	preferredNames := DefaultGroupNamesByPlatform(platform)
	for _, preferredName := range preferredNames {
		for i := range groups {
			if groups[i].Name == preferredName {
				return i
			}
		}
	}

	if platform == PlatformAntigravity {
		for i := range groups {
			if strings.HasPrefix(groups[i].Name, PlatformAntigravity+"-default") {
				return i
			}
		}
	}

	return -1
}

// DefaultGroupNamesByPlatform 返回各平台默认分组的候选名称，按优先级排序。
func DefaultGroupNamesByPlatform(platform string) []string {
	switch platform {
	case PlatformOpenAI:
		return []string{"openai-default"}
	case PlatformGemini:
		return []string{"gemini-default"}
	case PlatformAntigravity:
		return []string{"antigravity-default", "antigravity-default-1"}
	case PlatformAnthropic:
		return []string{"anthropic-default", "default"}
	default:
		return nil
	}
}

// FindPlatformDefaultGroup 保持一次轻量读取及原存储顺序。
func FindPlatformDefaultGroup(ctx context.Context, repo GroupRepository, platform string) (*Group, error) {
	if repo == nil {
		return nil, nil
	}
	groups, err := repo.ListActiveByPlatformLite(ctx, platform)
	if err != nil {
		return nil, err
	}
	candidates := make([]DefaultGroupCandidate, len(groups))
	for i := range groups {
		candidates[i] = DefaultGroupCandidate{Name: groups[i].Name, IsDefault: groups[i].IsDefault}
	}
	index := DefaultGroupIndex(platform, candidates)
	if index < 0 {
		return nil, nil
	}
	return &groups[index], nil
}
