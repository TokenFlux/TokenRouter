package handler

import (
	"context"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyModelRedirectContextAppliesExactlyOneRule(t *testing.T) {
	apiKey := &service.APIKey{ModelMapping: map[string]string{
		"codex-auto-review": "gpt-5.6-luna",
		"gpt-5.6-luna":      "must-not-chain",
	}}

	ctx, model := apiKeyModelRedirectContext(context.Background(), apiKey, "codex-auto-review")
	require.Equal(t, "gpt-5.6-luna", model)
	require.Equal(t, "codex-auto-review", ctx.Value(ctxkey.ClientModel))
	trace, ok := service.APIKeyModelRedirectTraceFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, "codex-auto-review", trace.ClientModel)
	require.Equal(t, "gpt-5.6-luna", trace.TargetModel)
}
