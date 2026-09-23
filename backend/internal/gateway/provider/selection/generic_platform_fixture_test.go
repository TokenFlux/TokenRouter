//go:build unit

package selection

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// selectAccountForModelWithPlatform 选择单平台账户（完全隔离）
func (s *Generic) selectAccountForModelWithPlatform(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, platform string) (*gatewayprovider.ExecutionAccount, error) {
	core, scope := s.genericSelector()
	selected, err := core.SelectPlatform(ctx, groupID, sessionHash, requestedModel, excludedIDs, platform)
	return scope.oldAccount(selected), err
}

// selectAccountWithMixedScheduling 选择账户（支持混合调度）
// 查询原生平台账户 + 启用 mixed_scheduling 的 antigravity 账户
func (s *Generic) selectAccountWithMixedScheduling(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, nativePlatform string) (*gatewayprovider.ExecutionAccount, error) {
	core, scope := s.genericSelector()
	selected, err := core.SelectMixed(ctx, groupID, sessionHash, requestedModel, excludedIDs, nativePlatform)
	return scope.oldAccount(selected), err
}
