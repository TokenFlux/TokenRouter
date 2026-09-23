//go:build unit

package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// ============================================================================
// Part 2: 分组隔离端到端调度测试
// ============================================================================

// groupAwareMockAccountRepo 嵌入 mockAccountRepoForPlatform，覆写分组隔离相关方法。
// allAccounts 存储所有账号，分组查询方法按 AccountGroups 字段进行真实过滤。
type groupAwareMockAccountRepo struct {
	*mockAccountRepoForPlatform
	allAccounts []gatewayprovider.

		// ListSchedulableUngroupedByPlatform 仅返回未分组账号（AccountGroups 为空）
		ExecutionAccount
}

func (m *groupAwareMockAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range m.allAccounts {
		if acc.Record.Platform == platform && acc.View().IsSchedulable() && len(acc.Record.AccountGroups) == 0 {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableUngroupedByPlatforms 仅返回未分组账号（多平台版本）
func (m *groupAwareMockAccountRepo) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	platformSet := make(map[string]bool, len(platforms))
	for _, p := range platforms {
		platformSet[p] = true
	}
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range m.allAccounts {
		if platformSet[acc.Record.Platform] && acc.View().IsSchedulable() && len(acc.Record.AccountGroups) == 0 {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableByGroupIDAndPlatform 返回属于指定分组的账号
func (m *groupAwareMockAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range m.allAccounts {
		if acc.Record.Platform == platform && acc.View().IsSchedulable() && accountBelongsToGroup(acc, groupID) {
			result = append(result, acc)
		}
	}
	return result, nil
}

// ListSchedulableByGroupIDAndPlatforms 返回属于指定分组的账号（多平台版本）
func (m *groupAwareMockAccountRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	platformSet := make(map[string]bool, len(platforms))
	for _, p := range platforms {
		platformSet[p] = true
	}
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range m.allAccounts {
		if platformSet[acc.Record.Platform] && acc.View().IsSchedulable() && accountBelongsToGroup(acc, groupID) {
			result = append(result, acc)
		}
	}
	return result, nil
}

// accountBelongsToGroup 检查账号是否属于指定分组
func accountBelongsToGroup(acc gatewayprovider.ExecutionAccount, groupID int64) bool {
	for _, ag := range acc.Record.AccountGroups {
		if ag.GroupID == groupID {
			return true
		}
	}
	return false
}

// Verify interface implementation
var _ gatewayprovider.ExecutionAccountStore = (*groupAwareMockAccountRepo)(nil)
