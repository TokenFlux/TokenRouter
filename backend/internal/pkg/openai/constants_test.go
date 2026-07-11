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

func TestDefaultModelsIncludeBareGPT56Alias(t *testing.T) {
	require.Contains(t, DefaultModelIDs(), "gpt-5.6")
}
