package clientmeta

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDetectInterceptType_SuggestionModeUnaffected supplies a final user
// message beginning with the suggestion marker. It remains locally classified
// as suggestion mode when the request is not marked as a Claude client.
func TestDetectInterceptType_SuggestionModeUnaffected(t *testing.T) {
	body := []byte(`{
		"messages":[{
			"role":"user",
			"content":[{"type":"text","text":"[SUGGESTION MODE:foo]"}]
		}],
		"system":[]
	}`)

	got := DetectInterceptType(body, "claude-sonnet-4-5", 256, false)
	require.Equal(t, InterceptTypeSuggestionMode, got)
}

// TestDetectInterceptType_MaxTokensOneHaikuRequiresClaudeCodeClient supplies
// the same single-token Haiku payload with both client classifications. Local
// probe interception requires the validated Claude-client flag, while the model
// and token predicate recognizes the probe itself.
func TestDetectInterceptType_MaxTokensOneHaikuRequiresClaudeCodeClient(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)

	notClaudeCode := DetectInterceptType(body, "claude-haiku-4-5", 1, false)
	require.Equal(t, InterceptTypeNone, notClaudeCode)

	isClaudeCode := DetectInterceptType(body, "claude-haiku-4-5", 1, true)
	require.Equal(t, InterceptTypeMaxTokensOneHaiku, isClaudeCode)

	require.True(t, IsHaikuProbe("claude-haiku-4-5", 1), "流式 haiku 探测也应被识别")
}

// TestDetectInterceptType_IgnoresTitleKeywordInToolsAndRecognizesRealExtractor
// supplies tool schemas containing title properties beside a real title prompt.
// Ordinary tool requests allocate nothing and return InterceptTypeNone, while
// genuine title-extractor prompts still match, including Unicode-escaped text.
func TestDetectInterceptType_IgnoresTitleKeywordInToolsAndRecognizesRealExtractor(t *testing.T) {
	ordinaryWithTitleSchema := []byte(`{
		"model":"claude-opus-4-6",
		"tools":[{"name":"create_issue","input_schema":{"type":"object","title":"IssueInput","properties":{"title":{"type":"string"}}}}],
		"messages":[{"role":"user","content":[{"type":"text","text":"Please implement \u003cdiv\u003e \u0026\u0026 update the PR title."}]}]
	}`)
	allocs := testing.AllocsPerRun(20, func() {
		if got := DetectInterceptType(ordinaryWithTitleSchema, "claude-opus-4-6", 4096, true); got != InterceptTypeNone {
			t.Fatalf("unexpected intercept %v", got)
		}
	})
	require.Zero(t, allocs)

	realTitlePrompt := []byte(`{
		"system":[{"type":"text","text":"Analyze if this message indicates a new conversation topic. If it does, extract a 2-3 word title."}],
		"messages":[{"role":"user","content":[{"type":"text","text":"hello world"}]}]
	}`)
	require.Equal(t, InterceptTypeWarmup, DetectInterceptType(realTitlePrompt, "claude-haiku-4-5", 128, true))

	escapedSpaceTitlePrompt := []byte(`{
		"messages":[{"role":"user","content":[{"type":"text","text":"Please write a 5-10 word\u0020title for the following conversation: debugging latency"}]}]
	}`)
	require.Equal(t, InterceptTypeWarmup, DetectInterceptType(escapedSpaceTitlePrompt, "claude-haiku-4-5", 128, true))

	escapedSuggestion := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"[SUGGESTION MODE\u003Atest]"}]}]}`)
	require.Equal(t, InterceptTypeSuggestionMode, DetectInterceptType(escapedSuggestion, "claude-sonnet-4-5", 128, false))
}
