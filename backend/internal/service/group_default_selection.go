package service

import (
	"context"
	"strings"
)

// findPlatformDefaultGroup 查找平台默认分组。
// 优先使用显式默认分组；未配置时回退到历史命名约定，兼容旧配置。
func findPlatformDefaultGroup(ctx context.Context, groupRepo GroupRepository, platform string) (*Group, error) {
	if groupRepo == nil {
		return nil, nil
	}

	groups, err := groupRepo.ListActiveByPlatformLite(ctx, platform)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, nil
	}

	for i := range groups {
		if groups[i].IsDefault {
			group := groups[i]
			return &group, nil
		}
	}

	preferredNames := defaultGroupNamesByPlatform(platform)
	for _, preferredName := range preferredNames {
		for i := range groups {
			if groups[i].Name == preferredName {
				group := groups[i]
				return &group, nil
			}
		}
	}

	if platform == PlatformAntigravity {
		for i := range groups {
			if strings.HasPrefix(groups[i].Name, PlatformAntigravity+"-default") {
				group := groups[i]
				return &group, nil
			}
		}
	}

	return nil, nil
}

// defaultGroupNamesByPlatform 返回各平台默认分组的候选名称，按优先级排序。
func defaultGroupNamesByPlatform(platform string) []string {
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
