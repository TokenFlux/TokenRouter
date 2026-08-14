package repository

import (
	"context"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestApplyClientModelOnlyChangesRequestedModel(t *testing.T) {
	upstreamModel := "gpt-5.6-luna-upstream"
	mappingChain := "codex-auto-review→gpt-5.6-luna→gpt-5.6-luna-upstream"
	log := &service.UsageLog{
		Model:             "gpt-5.6-luna",
		RequestedModel:    "gpt-5.6-luna",
		UpstreamModel:     &upstreamModel,
		ModelMappingChain: &mappingChain,
	}
	ctx := context.WithValue(context.Background(), ctxkey.ClientModel, "codex-auto-review")

	applyClientModel(ctx, log)

	require.Equal(t, "codex-auto-review", log.RequestedModel)
	require.Equal(t, "gpt-5.6-luna", log.Model)
	require.Equal(t, upstreamModel, *log.UpstreamModel)
	require.Equal(t, mappingChain, *log.ModelMappingChain)
}
