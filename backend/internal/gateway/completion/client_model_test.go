package completion

import (
	usage "github.com/TokenFlux/TokenRouter/internal/usage"
)

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"

	"github.com/stretchr/testify/require"
)

func TestApplyClientModelOnlyChangesRequestedModel(t *testing.T) {
	upstreamModel := "gpt-5.6-luna-upstream"
	mappingChain := "codex-auto-review→gpt-5.6-luna→gpt-5.6-luna-upstream"
	log := &usage.UsageLog{
		Model:             "gpt-5.6-luna",
		RequestedModel:    "gpt-5.6-luna",
		UpstreamModel:     &upstreamModel,
		ModelMappingChain: &mappingChain,
	}
	ctx := context.WithValue(context.Background(), telemetry.ClientModel, "codex-auto-review")

	applyClientModel(ctx, log)

	require.Equal(t, "codex-auto-review", log.RequestedModel)
	require.Equal(t, "gpt-5.6-luna", log.Model)
	require.Equal(t, upstreamModel, *log.UpstreamModel)
	require.Equal(t, mappingChain, *log.ModelMappingChain)
}
