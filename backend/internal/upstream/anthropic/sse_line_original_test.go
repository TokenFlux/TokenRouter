package anthropic_test

import (
	"testing"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/stretchr/testify/require"
)

func TestExtractAnthropicSSEDataLine(t *testing.T) {
	t.Run("valid data line with spaces", func(t *testing.T) {
		data, ok := claude.ExtractSSEDataLine("data:   {\"type\":\"message_start\"}")
		require.True(t, ok)
		require.Equal(t, `{"type":"message_start"}`, data)
	})

	t.Run("non data line", func(t *testing.T) {
		data, ok := claude.ExtractSSEDataLine("event: message_start")
		require.False(t, ok)
		require.Empty(t, data)
	})
}
