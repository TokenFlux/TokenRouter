package anthropic_test

import (
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/stretchr/testify/require"
)

func TestSanitizeOpenCodeText_RewritesCanonicalSentence(t *testing.T) {
	in := "You are OpenCode, the best coding agent on the planet."
	got := anthropic.SanitizeSystemText(in)
	require.Equal(t, strings.TrimSpace(anthropic.ClaudeCodeSystemPrompt), got)
}
