package service

import "testing"

func TestBuildOpenAIResponsesInputTokensURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "root base",
			base: "https://api.openai.com",
			want: "https://api.openai.com/v1/responses/input_tokens",
		},
		{
			name: "versioned base",
			base: "https://compat.example/v4",
			want: "https://compat.example/v4/responses/input_tokens",
		},
		{
			name: "responses endpoint base",
			base: "https://compat.example/v1/responses",
			want: "https://compat.example/v1/responses/input_tokens",
		},
		{
			name: "input tokens endpoint base",
			base: "https://compat.example/v1/responses/input_tokens",
			want: "https://compat.example/v1/responses/input_tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildOpenAIResponsesInputTokensURL(tt.base); got != tt.want {
				t.Fatalf("buildOpenAIResponsesInputTokensURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}
