package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplaceModelInResponseBody(t *testing.T) {

	tests := []struct {
		name     string
		body     string
		from     string
		to       string
		expected string
	}{
		{
			name:     "替换顶层 model",
			body:     `{"id":"chatcmpl-123","model":"gpt-4o","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","model":"alias","choices":[]}`,
		},
		{
			name:     "model 不匹配不替换",
			body:     `{"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
		},
		{
			name:     "无 model 字段不替换",
			body:     `{"id":"chatcmpl-123","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","choices":[]}`,
		},
		{
			name:     "非法 JSON 返回原值",
			body:     `not json`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `not json`,
		},
		{
			name:     "空 body 返回原值",
			body:     ``,
			from:     "gpt-4o",
			to:       "alias",
			expected: ``,
		},
		{
			name:     "保持嵌套结构不变",
			body:     `{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":20},"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"model":"alias","usage":{"prompt_tokens":10,"completion_tokens":20},"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplaceModelInResponseBody([]byte(tt.body), tt.from, tt.to)
			require.Equal(t, tt.expected, string(got))
		})
	}
}
