//go:build integration

package postgres

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// TestUsagePerformanceSQLMatchesRecordedMetrics compares PostgreSQL aggregation
// expressions with usage API derivation. Missing, zero, inverted, incomplete and
// differently selected observations must yield identical samples and rounded TPS.
func TestUsagePerformanceSQLMatchesRecordedMetrics(t *testing.T) {
	complete := timing.NewPerformance(timing.FirstOutput, 100)
	complete.Stream = true
	complete.Complete = true
	complete.UsageComplete = true
	complete.Values["upstream_first_event_ms"] = 100
	complete.Values["upstream_first_output_ms"] = 300
	complete.Values["upstream_last_output_ms"] = 900
	complete.Values["upstream_output_event_count"] = 2
	wantSpeed := 21.3
	wantGeminiSpeed := 7.8
	wantLongWaitSpeed := 1.1
	for _, tc := range []struct {
		name   string                    // independent sample label
		change func(*timing.Performance) // sample-specific observation change
		tokens int                       // final output token count
		want   *float64                  // nil means unavailable estimate
	}{
		{"content", func(*timing.Performance) {}, 17, &wantSpeed},
		{"zero_event", func(p *timing.Performance) { p.Mode = timing.FirstEvent }, 17, &wantSpeed},
		{"missing_start", func(p *timing.Performance) { delete(p.Values, "upstream_started_ms") }, 17, nil},
		{"inverted_ttft", func(p *timing.Performance) { p.Values["upstream_first_output_ms"] = 90 }, 17, &wantSpeed},
		{"missing_first_output", func(p *timing.Performance) { delete(p.Values, "upstream_first_output_ms") }, 17, &wantSpeed},
		{"missing_event_count", func(p *timing.Performance) { delete(p.Values, "upstream_output_event_count") }, 17, &wantSpeed},
		{"single_chunk", func(p *timing.Performance) {
			p.Values["upstream_output_event_count"] = 1
			p.Values["upstream_first_output_ms"] = 900
		}, 17, &wantSpeed},
		{"missing_last_output", func(p *timing.Performance) { delete(p.Values, "upstream_last_output_ms") }, 17, nil},
		{"zero_interval", func(p *timing.Performance) { p.Values["upstream_last_output_ms"] = 100 }, 17, nil},
		{"inverted_interval", func(p *timing.Performance) { p.Values["upstream_last_output_ms"] = 99 }, 17, nil},
		{"unfinished", func(p *timing.Performance) { p.Complete = false }, 17, nil},
		{"partial_usage", func(p *timing.Performance) { p.UsageComplete = false }, 17, nil},
		{"nonstreaming", func(p *timing.Performance) { p.Stream = false }, 17, nil},
		{"zero_tokens", func(*timing.Performance) {}, 0, nil},
		{"negative_tokens", func(*timing.Performance) {}, -1, nil},
		{"gemini_buffered", func(p *timing.Performance) {
			p.Values["upstream_started_ms"] = 11103
			p.Values["upstream_first_response_byte_ms"] = 26291
			p.Values["upstream_first_output_ms"] = 26297
			p.Values["upstream_last_output_ms"] = 26297
			p.Values["upstream_output_event_count"] = 1
		}, 119, &wantGeminiSpeed},
		{"gemini_long_wait", func(p *timing.Performance) {
			p.Values["upstream_started_ms"] = 5198
			p.Values["upstream_first_output_ms"] = 58731
			p.Values["upstream_last_output_ms"] = 58731
			p.Values["upstream_output_event_count"] = 1
		}, 61, &wantLongWaitSpeed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := complete.Clone()
			tc.change(p)
			encoded, err := json.Marshal(p)
			require.NoError(t, err)
			var latency sql.NullInt64
			var speed sql.NullFloat64
			query := "SELECT " + usageTTFTSQL("u") + "," + usageTPSSQL("u") + " FROM (SELECT $1::jsonb AS timings,$2::int AS output_tokens) u"
			require.NoError(t, integrationDB.QueryRowContext(t.Context(), query, string(encoded), tc.tokens).Scan(&latency, &speed))
			require.Equal(t, p.TTFTMs() != nil, latency.Valid)
			if latency.Valid {
				require.Equal(t, int64(*p.TTFTMs()), latency.Int64)
			}
			require.Equal(t, tc.want, p.TPS(tc.tokens))
			require.Equal(t, tc.want != nil, speed.Valid)
			if speed.Valid {
				// Permit floating-point representation noise without masking differences in the rounded tenth.
				require.InDelta(t, *tc.want, speed.Float64, 0.00001)
			}
		})
	}
}
