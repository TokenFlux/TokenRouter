package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultModelsContainsCodexAutoReview(t *testing.T) {
	for _, model := range DefaultModels {
		if model.ID == "codex-auto-review" {
			return
		}
	}
	t.Fatal("默认 OpenAI 模型列表应包含 codex-auto-review")
}

// GPT-5.6 系列只提供三个明确的内置产品 ID。
func TestDefaultModelsExcludeBareGPT56(t *testing.T) {
	require.NotContains(t, DefaultModelIDs(), "gpt-5.6")
	for _, model := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		require.Contains(t, DefaultModelIDs(), model)
	}
}

// TestDefaultModelsContainsGPT6Astra 确保默认目录及其派生 ID 列表公开 Astra。
func TestDefaultModelsContainsGPT6Astra(t *testing.T) {
	for _, model := range DefaultModels {
		if model.ID != "gpt-6-astra" {
			continue
		}
		require.Equal(t, "GPT-6 Astra", model.DisplayName)
		require.Contains(t, DefaultModelIDs(), model.ID)
		return
	}
	t.Fatal("默认 OpenAI 模型列表应包含 gpt-6-astra")
}

func TestDefaultModelsPreferConcreteGPT56SolForAccountTests(t *testing.T) {
	require.NotEmpty(t, DefaultModels)
	require.Equal(t, "gpt-5.6-sol", DefaultModels[0].ID)
}
