package routing

import (
	"context"
	"errors"
	"fmt"
)

// ErrClaudeCodeOnly 保留原客户端限制的错误身份及消息。
var ErrClaudeCodeOnly = errors.New("this group only allows Claude Code clients")

// ClientGroupPolicy 明确保留两个已有入口的快照缺失与回退 ID 差异。
type ClientGroupPolicy struct {
	KeepMissingSnapshot       bool
	RejectNonPositiveFallback bool
}

// ResolveClientGroup 按原顺序读取并跟随客户端限制回退，不新增账号或资金查询。
// 通用入口的读取端口必须返回有效分组或错误；快照入口可明确保留未解析分组。
// @project-doc docs/domains/gateway_policy_controls.md#gateway_policy_layers
func ResolveClientGroup(ctx context.Context, groupID *int64, read func(context.Context, int64) (*Group, error), isClient func(context.Context) bool, policy ClientGroupPolicy) (*Group, *int64, error) {
	if groupID == nil {
		return nil, nil, nil
	}
	currentID := *groupID
	visited := map[int64]struct{}{}
	for {
		if _, seen := visited[currentID]; seen {
			return nil, nil, fmt.Errorf("fallback group cycle detected")
		}
		visited[currentID] = struct{}{}
		group, err := read(ctx, currentID)
		if err != nil {
			return nil, nil, err
		}
		if group == nil && policy.KeepMissingSnapshot {
			return nil, &currentID, nil
		}
		if !group.ClaudeCodeOnly || isClient(ctx) {
			return group, &currentID, nil
		}
		if group.FallbackGroupID == nil || (policy.RejectNonPositiveFallback && *group.FallbackGroupID <= 0) {
			return nil, nil, ErrClaudeCodeOnly
		}
		currentID = *group.FallbackGroupID
	}
}
