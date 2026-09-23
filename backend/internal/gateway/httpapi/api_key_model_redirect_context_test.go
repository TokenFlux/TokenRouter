package httpapi

import (
	modeltrace "github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"

	"context"
	"testing"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyModelRedirectContextAppliesExactlyOneRule(t *testing.T) {
	apiKey := &apikey.APIKey{ModelMapping: map[string]string{
		"codex-auto-review": "gpt-5.6-luna",
		"gpt-5.6-luna":      "must-not-chain",
	}}

	ctx, model := APIKeyModelRedirectContext(context.Background(), apiKey, "codex-auto-review")
	require.Equal(t, "gpt-5.6-luna", model)
	require.Equal(t, "codex-auto-review", ctx.Value(telemetry.ClientModel))
	trace, ok := modeltrace.FromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "codex-auto-review", trace.ClientModel)
	require.Equal(t, "gpt-5.6-luna", trace.TargetModel)
}
