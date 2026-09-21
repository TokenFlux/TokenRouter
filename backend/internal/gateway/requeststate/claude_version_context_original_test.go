package requeststate_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/stretchr/testify/require"
)

func TestSetGetClaudeCodeVersion(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, "", requeststate.GetClaudeCodeVersion(ctx), "empty context should return empty string")

	ctx = requeststate.SetClaudeCodeVersion(ctx, "2.1.63")
	require.Equal(t, "2.1.63", requeststate.GetClaudeCodeVersion(ctx))
}
