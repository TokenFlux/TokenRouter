package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClaudeUsageResponse_FableWindowDecoding(t *testing.T) {
	t.Run("seven_day_overage_included", func(t *testing.T) {
		raw := `{
  "five_hour": {"utilization": 12.0, "resets_at": "2026-07-03T10:00:00Z"},
  "seven_day": {"utilization": 34.0, "resets_at": "2026-07-08T00:00:00Z"},
  "seven_day_overage_included": {"utilization": 56.0, "resets_at": "2026-07-08T03:00:00Z"}
}`
		var resp ClaudeUsageResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &resp))
		require.Equal(t, 56.0, resp.SevenDayOverageIncluded.Utilization)
		require.Equal(t, "2026-07-08T03:00:00Z", resp.SevenDayOverageIncluded.ResetsAt)
	})

	t.Run("absent", func(t *testing.T) {
		raw := `{"five_hour": {"utilization": 12.0, "resets_at": "2026-07-03T10:00:00Z"}}`
		var resp ClaudeUsageResponse
		require.NoError(t, json.Unmarshal([]byte(raw), &resp))
		require.Zero(t, resp.SevenDayOverageIncluded.Utilization)
		require.Empty(t, resp.SevenDayOverageIncluded.ResetsAt)
	})
}
