// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// findPlatformDefaultGroup 查找平台默认分组。
// 优先使用显式默认分组；未配置时回退到历史命名约定，兼容旧配置。
func findPlatformDefaultGroup(ctx context.Context, groupRepo routing.GroupRepository, platform string) (*routing.Group, error) {
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

	candidates := make([]routing.DefaultGroupCandidate, len(groups))
	for i := range groups {
		candidates[i] = routing.DefaultGroupCandidate{Name: groups[i].Name, IsDefault: groups[i].IsDefault}
	}
	index := routing.DefaultGroupIndex(platform, candidates)
	if index < 0 {
		return nil, nil
	}
	return &groups[index], nil
}

// FindPlatformDefaultGroup 为过渡装配提供原选择结果，规则仍归分组用例，S06 改绑。
func FindPlatformDefaultGroup(ctx context.Context, repo routing.GroupRepository, platform string) (*routing.Group, error) {
	return findPlatformDefaultGroup(ctx, repo, platform)
}
