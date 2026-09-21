package bedrock

import "testing"

func TestDefaultBedrockModelMapping_ContainsNewClaudeModels(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-fable-5-1": "anthropic.claude-fable-5-1",
		"claude-fable-5":   "anthropic.claude-fable-5",
		"claude-opus-5":    "us.anthropic.claude-opus-5",
		"claude-opus-4-8":  "us.anthropic.claude-opus-4-8",
		"claude-opus-4-7":  "us.anthropic.claude-opus-4-7",
		"claude-sonnet-5":  "us.anthropic.claude-sonnet-5",
		// 旧型号合法的版本及日期后缀必须保留。
		"claude-opus-4-6":          "us.anthropic.claude-opus-4-6-v1",
		"claude-opus-4-5-20251101": "us.anthropic.claude-opus-4-5-20251101-v1:0",
	}
	for from, want := range cases {
		got, ok := DefaultBedrockModelMapping[from]
		if !ok {
			t.Fatalf("expected Bedrock mapping for %q to exist", from)
		}
		if got != want {
			t.Fatalf("unexpected Bedrock mapping for %q: got %q want %q", from, got, want)
		}
	}
}
