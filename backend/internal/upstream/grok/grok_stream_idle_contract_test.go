//go:build unit

package grok_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestResolveGrokStreamIdleTimeout(t *testing.T) {
	require.Equal(t, 90*time.Second, grok.ResolveStreamIdleTimeout(90))
	require.Equal(t, grok.DefaultStreamIdleTimeout, grok.ResolveStreamIdleTimeout(0))
	require.Equal(t, grok.DefaultStreamIdleTimeout, grok.ResolveStreamIdleTimeout(-1))
}
