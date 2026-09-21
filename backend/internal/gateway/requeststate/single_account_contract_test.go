//go:build unit

package requeststate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsSingleAccountRetry_True(t *testing.T) {
	ctx := WithSingleAccountRetry(context.Background(), true)
	value, _ := SingleAccountRetryFromContext(ctx)
	require.True(t, value)
}

func TestIsSingleAccountRetry_False_NoValue(t *testing.T) {
	value, _ := SingleAccountRetryFromContext(context.Background())
	require.False(t, value)
}

func TestIsSingleAccountRetry_False_ExplicitFalse(t *testing.T) {
	ctx := WithSingleAccountRetry(context.Background(), false)
	value, _ := SingleAccountRetryFromContext(ctx)
	require.False(t, value)
}

func TestIsSingleAccountRetry_False_WrongType(t *testing.T) {
	// 任意旧式上下文值不能成为原生执行参数；合法输入必须是 bool。
	type unrelatedKey struct{}
	ctx := context.WithValue(context.Background(), unrelatedKey{}, "true")
	value, _ := SingleAccountRetryFromContext(ctx)
	require.False(t, value)
}
