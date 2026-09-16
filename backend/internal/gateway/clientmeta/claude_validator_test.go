package clientmeta

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemPromptSimilarity(t *testing.T) {
	v := NewClaudeCodeValidator()

	tests := []struct {
		name   string
		prompt string
		want   bool
	}{
		{"精确匹配", "You are Claude Code, Anthropic's official CLI for Claude.", true},
		{"带多余空格", "You  are  Claude  Code,  Anthropic's  official  CLI  for  Claude.", true},
		{"Agent SDK 模板", "You are a Claude agent, built on Anthropic's Claude Agent SDK.", true},
		{"文件搜索专家模板", "You are a file search specialist for Claude Code, Anthropic's official CLI for Claude.", true},
		{"对话摘要模板", "You are a helpful AI assistant tasked with summarizing conversations.", true},
		{"交互式 CLI 模板", "You are an interactive CLI tool that helps users", true},
		{"无关文本", "Write me a poem about cats", false},
		{"空文本", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{
				"model": "claude-sonnet-4",
				"system": []any{
					map[string]any{"type": "text", "text": tt.prompt},
				},
			}
			result := v.IncludesClaudeCodeSystemPrompt(body)
			require.Equal(t, tt.want, result, "提示词: %q", tt.prompt)
		})
	}
}

func TestDiceCoefficient(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want float64
		tol  float64
	}{
		{"相同字符串", "hello", "hello", 1.0, 0.001},
		{"完全不同", "abc", "xyz", 0.0, 0.001},
		{"空字符串", "", "hello", 0.0, 0.001},
		{"单字符", "a", "b", 0.0, 0.001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DiceCoefficient(tt.a, tt.b)
			require.InDelta(t, tt.want, result, tt.tol)
		})
	}
}

// 输入投影不得交换 UA、探测绕过与严格报文检查的顺序。
func TestClaudeCodeValidationInputPreservesGateOrder(t *testing.T) {
	validator := NewClaudeCodeValidator()
	cases := []struct {
		name  string
		input ClaudeCodeValidationInput
		want  bool
	}{
		{"invalid UA with probe", ClaudeCodeValidationInput{Path: "/v1/messages", UserAgent: "curl/1.0.0", MaxTokensOneHaiku: true}, false},
		{"CLI probe", ClaudeCodeValidationInput{Path: "/v1/messages", UserAgent: "claude-cli/2.1.156", MaxTokensOneHaiku: true}, true},
		{"messages requires body", ClaudeCodeValidationInput{Path: "/v1/messages", UserAgent: "claude-cli/2.1.156"}, false},
		{"count tokens bypass", ClaudeCodeValidationInput{Path: "/v1/messages/count_tokens", UserAgent: "claude-cli/2.1.156"}, true},
		{"models bypass", ClaudeCodeValidationInput{Path: "/v1/models", UserAgent: "claude-cli/2.1.156"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, validator.Validate(tc.input, nil)) })
	}
}
