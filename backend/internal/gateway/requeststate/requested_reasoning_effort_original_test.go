package requeststate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalRequestedReasoningEffort(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		candidates []string
		want       string
	}{
		{name: "nested explicit", body: `{"model":"gpt-5.4","reasoning":{"effort":"MAX"}}`, want: "max"},
		{name: "flat explicit", body: `{"model":"gpt-5.4","reasoning_effort":"x-high"}`, want: "xhigh"},
		{name: "anthropic output config", body: `{"model":"claude-sonnet","output_config":{"effort":"high"}}`, want: "high"},
		{name: "none explicit", body: `{"model":"gpt-5.4","reasoning_effort":"none"}`, want: "none"},
		{name: "candidate suffix", body: `{"model":"gpt-5.4"}`, candidates: []string{"gpt-5.4-max"}, want: "max"},
		{name: "body suffix fallback", body: `{"model":"gpt-5.4-high"}`, want: "high"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanonicalRequestedReasoningEffort([]byte(tt.body), tt.candidates...)
			require.NotNil(t, got)
			require.Equal(t, tt.want, *got)
		})
	}
}

func TestCanonicalRequestedReasoningEffortFromReqBodyPreservesNone(t *testing.T) {
	got := CanonicalRequestedReasoningEffortFromReqBody(map[string]any{
		"reasoning": map[string]any{"effort": " none "},
	})
	require.NotNil(t, got)
	require.Equal(t, "none", *got)
}

func TestRequestedReasoningEffortContext(t *testing.T) {
	ctx := WithRequestedReasoningEffort(context.Background(), " max ")
	got := RequestedReasoningEffortFromContext(ctx)
	require.NotNil(t, got)
	require.Equal(t, "max", *got)
	require.Nil(t, RequestedReasoningEffortFromContext(context.Background()))
}
