package anthropic_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/stretchr/testify/require"
)

func TestClaudeUsageSpeedParsing(t *testing.T) {
	usage := &protocol.TokenUsage{}
	anthropic.ParseSSEUsage(`{"type":"message_start","message":{"usage":{"input_tokens":10,"speed":"fast"}}}`, usage)
	require.Equal(t, "fast", usage.Speed)

	parsed := anthropic.ParseClaudeUsageFromResponseBody([]byte(`{"usage":{"input_tokens":10,"output_tokens":2,"speed":"standard"}}`))
	require.Equal(t, "standard", parsed.Speed)
}
