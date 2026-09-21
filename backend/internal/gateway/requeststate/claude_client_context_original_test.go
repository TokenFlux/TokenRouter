//go:build unit

package requeststate_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/stretchr/testify/require"
)

func TestIsClaudeCodeClient_Context(t *testing.T) {
	ctx := context.Background()

	require.False(t, requeststate.IsClaudeCodeClient(ctx))

	ctx = requeststate.SetClaudeCodeClient(ctx, true)
	require.True(t, requeststate.IsClaudeCodeClient(ctx))

	ctx = requeststate.SetClaudeCodeClient(ctx, false)
	require.False(t, requeststate.IsClaudeCodeClient(ctx))
}
