package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/stretchr/testify/require"
)

// 显式空模型和未提供模型保留原区别，供应商解析收到原始模型而冷却使用规范值。
func TestHealthObservationKeepsRawAndEffectiveModel(t *testing.T) {
	ctx := requeststate.WithHealthModel(context.Background(), []string{"stored-model"})
	ctx = requeststate.WithOpenAIImagesEndpoint(requeststate.WithThinkingEnabled(ctx, false))
	for _, models := range [][]string{nil, {""}, {" mapped-model "}} {
		input := HealthObservationFromContext(ctx, http.StatusNotFound, nil, []byte("failure"), models)
		require.Equal(t, len(models) > 0, input.ModelProvided)
		require.Equal(t, requeststate.HealthModel(ctx, models), input.EffectiveModel)
		if len(models) > 0 {
			require.Equal(t, models[0], input.Model)
		}
		require.True(t, input.ImagesEndpoint)
		require.NotNil(t, input.Thinking)
		require.False(t, *input.Thinking)
	}
}
