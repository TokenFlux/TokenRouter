package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// 迁移必须保持先读取分组、再读取客户端标志，以及未受限目标的短路。
func TestClientGroupResolutionKeepsReadOrder(t *testing.T) {
	first, next := int64(1), int64(2)
	var sequence []string
	reader := func(_ context.Context, id int64) (*Group, error) {
		if id == first {
			sequence = append(sequence, "first")
			return &Group{ID: id, ClaudeCodeOnly: true, FallbackGroupID: &next}, nil
		}
		sequence = append(sequence, "next")
		return &Group{ID: id}, nil
	}
	group, id, err := ResolveClientGroup(context.Background(), &first, reader, func(context.Context) bool {
		sequence = append(sequence, "client")
		return false
	}, ClientGroupPolicy{})
	require.NoError(t, err)
	require.Equal(t, next, *id)
	require.Equal(t, next, group.ID)
	require.Equal(t, []string{"first", "client", "next"}, sequence)
}

// 两个入口对无效回退 ID 的错误优先级不同，不在提取时统一。
func TestClientGroupResolutionKeepsFallbackIDPolicies(t *testing.T) {
	for _, reject := range []bool{false, true} {
		id, invalid := int64(1), int64(0)
		missing := errors.New("fixture group not found")
		var calls []int64
		_, _, err := ResolveClientGroup(context.Background(), &id, func(_ context.Context, id int64) (*Group, error) {
			calls = append(calls, id)
			if id == invalid {
				return nil, missing
			}
			return &Group{ID: id, ClaudeCodeOnly: true, FallbackGroupID: &invalid}, nil
		}, func(context.Context) bool { return false }, ClientGroupPolicy{RejectNonPositiveFallback: reject})
		if reject {
			require.ErrorIs(t, err, ErrClaudeCodeOnly)
			require.Equal(t, []int64{1}, calls)
		} else {
			require.ErrorIs(t, err, missing)
			require.Equal(t, []int64{1, 0}, calls)
		}
	}
}

// 快照入口缺失分组时保留原 ID，不增加数据库回源或客户端判断。
func TestClientGroupResolutionKeepsMissingSnapshot(t *testing.T) {
	id := int64(9)
	group, resolved, err := ResolveClientGroup(context.Background(), &id, func(context.Context, int64) (*Group, error) {
		return nil, nil
	}, func(context.Context) bool {
		t.Fatal("缺失快照不得读取客户端标志")
		return false
	}, ClientGroupPolicy{KeepMissingSnapshot: true})
	require.NoError(t, err)
	require.Nil(t, group)
	require.Equal(t, id, *resolved)
}
