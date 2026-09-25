//go:build unit

package media

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsGrokImageGenerationModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		model string
		want  bool
	}{
		{"grok-imagine", true},
		{"grok-imagine-image-quality", true},
		{"grok-imagine-edit", true},
		{"grok-imagine-image-hd", true},
		{" Grok-Imagine ", true},
		{"grok-imagine-video", false},
		{"grok-4.5", false},
		{"grok-composer", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			require.Equal(t, tt.want, IsGrokImageGenerationModel(tt.model))
		})
	}
}
