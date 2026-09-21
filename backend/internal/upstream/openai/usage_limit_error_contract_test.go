package openai

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseOpenAIRateLimitResetTime_OpenCodeGoUsageLimit(t *testing.T) {
	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{
			name: "days",
			body: `{"type":"error","error":{"type":"GoUsageLimitError","message":"Weekly usage limit reached. Resets in 2 days."}}`,
			want: 48 * time.Hour,
		},
		{
			name: "hours",
			body: `{"type":"error","error":{"type":"GoUsageLimitError","message":"Weekly usage limit reached. Resets in 18 hours."}}`,
			want: 18 * time.Hour,
		},
		{
			name: "hours and minutes",
			body: `{"type":"error","error":{"type":"GoUsageLimitError","message":"5-hour usage limit reached. Resets in 4hr 59min."}}`,
			want: 4*time.Hour + 59*time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			resetAt := ParseUsageLimitResetTime([]byte(tt.body), time.Now)
			after := time.Now()

			require.NotNil(t, resetAt)
			actual := time.Unix(*resetAt, 0)
			require.False(t, actual.Before(before.Add(tt.want).Truncate(time.Second)))
			require.False(t, actual.After(after.Add(tt.want)))
		})
	}
}

func TestParseOpenAIRateLimitResetTime_DoesNotParseUnknownErrorMessage(t *testing.T) {
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Resets in 2 days."}}`)

	require.Nil(t, ParseUsageLimitResetTime(body, time.Now))
}
