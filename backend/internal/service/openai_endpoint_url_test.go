package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/stretchr/testify/require"
)

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
			if got := httpclient.BuildOpenAIResponsesInputTokensURL(tt.base); got != tt.want {
				t.Fatalf("buildOpenAIResponsesInputTokensURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestBuildOpenAIEndpointURLPreservesURLComponents(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		endpoint string
		want     string
	}{
		{name: "root", base: "https://upstream.example", endpoint: "/v1/models", want: "https://upstream.example/v1/models"},
		{name: "v1", base: "https://upstream.example/v1", endpoint: "/v1/responses", want: "https://upstream.example/v1/responses"},
		{name: "prefix", base: "https://upstream.example/openai", endpoint: "/v1/chat/completions", want: "https://upstream.example/openai/v1/chat/completions"},
		{name: "version", base: "https://upstream.example/openai/v2", endpoint: "/v1/embeddings", want: "https://upstream.example/openai/v2/embeddings"},
		{name: "query", base: "https://upstream.example/v1?redirect=/", endpoint: "/v1/embeddings", want: "https://upstream.example/v1/embeddings?redirect=/"},
		{name: "fragment is removed", base: "https://upstream.example/v1#stale", endpoint: "/v1/alpha/search", want: "https://upstream.example/v1/alpha/search"},
		{name: "ipv6", base: "http://[2001:db8::1]:8080/v1?tenant=a#stale", endpoint: "/v1/responses/input_tokens", want: "http://[2001:db8::1]:8080/v1/responses/input_tokens?tenant=a"},
		{name: "already complete", base: "https://upstream.example/v1/images/generations?tenant=a", endpoint: "/v1/images/generations", want: "https://upstream.example/v1/images/generations?tenant=a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, httpclient.BuildOpenAIEndpointURL(tt.base, tt.endpoint))
		})
	}
}
