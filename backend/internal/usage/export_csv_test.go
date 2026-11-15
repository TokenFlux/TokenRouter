package usage

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// TestUsageExportCSVWriterUserColumnsAndFormulaNeutralization streams user-scope
// usage rows with spring DST timestamps and spreadsheet formula prefixes through
// usageExportCSVWriter. Output starts with a UTF-8 BOM, renders all 19 localized
// headers in the requested IANA zone, and neutralizes formula cells while keeping
// the "-" reasoning placeholder intact.
func TestUsageExportCSVWriterUserColumnsAndFormulaNeutralization(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)

	var buf bytes.Buffer
	writer, err := newUsageExportCSVWriter(&buf, UsageExportScopeUser, UsageExportLocaleEN, "Credits", loc)
	require.NoError(t, err)

	endpoint := "/v1/responses"
	ipAddr := "203.0.113.9"
	durationMs := 1500
	perf := &timing.Performance{
		Mode:          timing.FirstOutput,
		Stream:        true,
		Complete:      true,
		UsageComplete: true,
		Values: map[string]int64{
			"upstream_started_ms":      100,
			"upstream_first_output_ms": 350,
			"upstream_last_output_ms":  1100,
		},
	}

	err = writer.WriteBatch([]UsageLog{
		{
			CreatedAt:           time.Date(2026, 7, 15, 14, 30, 45, 0, time.UTC),
			APIKey:              &KeyView{Name: "=cmd|' /C calc'!A0"},
			Model:               "gpt-5.4",
			InboundEndpoint:     &endpoint,
			IPAddress:           &ipAddr,
			RequestType:         RequestTypeStream,
			ImageCount:          2,
			InputTokens:         120,
			OutputTokens:        40,
			CacheReadTokens:     10,
			CacheCreationTokens: 5,
			RateMultiplier:      1.25,
			ActualCost:          0.01234567,
			TotalCost:           0.00987654,
			Timings:             perf,
			DurationMs:          &durationMs,
		},
	})
	require.NoError(t, err)

	raw := buf.String()
	require.True(t, strings.HasPrefix(raw, utf8BOM))

	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, utf8BOM))).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Len(t, records[0], 19)
	require.Equal(t, "User billed (Credits)", records[0][13])

	row := records[1]
	require.Equal(t, "2026-07-15T10:30:45-04:00", row[0])
	require.Equal(t, "'=cmd|' /C calc'!A0", row[1])
	require.Equal(t, "gpt-5.4", row[2])
	require.Equal(t, "-", row[3])
	require.Equal(t, "/v1/responses", row[4])
	require.Equal(t, "203.0.113.9", row[5])
	require.Equal(t, "Stream", row[6])
	require.Equal(t, "Image", row[7])
	require.Equal(t, "120", row[8])
	require.Equal(t, "40", row[9])
	require.Equal(t, "10", row[10])
	require.Equal(t, "5", row[11])
	require.Equal(t, "1.25", row[12])
	require.Equal(t, "0.01234567", row[13])
	require.Equal(t, "0.00987654", row[14])
	require.Equal(t, "250", row[15])
	require.Equal(t, "First", row[16])
	require.Equal(t, "1500", row[17])
	require.Equal(t, "40.0", row[18])
}

// TestUsageExportCSVWriterAdminColumnsAndDetailedTiming verifies that admin CSV
// output emits 38 Chinese headers, formats 4-significant-digit multipliers,
// serializes Detailed Timing in DTO field order, and suppresses Provider billed
// when SystemUsage.CostState is unknown.
func TestUsageExportCSVWriterAdminColumnsAndDetailedTiming(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)

	var buf bytes.Buffer
	writer, err := newUsageExportCSVWriter(&buf, UsageExportScopeAdmin, UsageExportLocaleZH, "USD", shanghai)
	require.NoError(t, err)

	upstreamModel := "claude-opus-4-6"
	respModel := "claude-opus-4-6-20260301"
	mismatch := true
	reqEffort := "x-high"
	fwdEffort := "high"
	inbound := "/v1/messages"
	upstream := "/v1/messages"
	providerRate := 0.85
	providerBase := 0.2
	upstreamReqID := "up_123"
	ua := "@malicious-agent"
	ip := "198.51.100.2"
	durationMs := 800
	perf := &timing.Performance{
		Mode:          timing.FirstEvent,
		Stream:        true,
		Complete:      true,
		UsageComplete: true,
		Values: map[string]int64{
			"upstream_started_ms":        50,
			"upstream_first_event_ms":    150,
			"upstream_last_output_ms":    550,
			"attempt_number":             1,
			"upstream_connection_reused": 1,
		},
	}

	err = writer.WriteBatch([]UsageLog{
		{
			CreatedAt:                time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC),
			User:                     &UserView{Email: "+admin@example.com"},
			APIKey:                   &KeyView{Name: "Prod Key"},
			Provider:                 &ProviderView{Name: "Anthropic Primary"},
			Group:                    &GroupView{Name: "Enterprise"},
			Model:                    "claude-opus",
			RequestedModel:           "claude-opus-alias",
			UpstreamModel:            &upstreamModel,
			UpstreamResponseModel:    &respModel,
			UpstreamModelMismatch:    &mismatch,
			RequestedReasoningEffort: &reqEffort,
			ReasoningEffort:          &fwdEffort,
			InboundEndpoint:          &inbound,
			UpstreamEndpoint:         &upstream,
			RequestType:              RequestTypeWSV2,
			InputTokens:              1000,
			OutputTokens:             50,
			InputCost:                0.15,
			OutputCost:               0.05,
			TotalCost:                0.2,
			ActualCost:               0.3,
			RateMultiplier:           1.5,
			ProviderRateMultiplier:   &providerRate,
			ProviderStatsCost:        &providerBase,
			Timings:                  perf,
			DurationMs:               &durationMs,
			RequestID:                "req_123",
			UpstreamRequestID:        &upstreamReqID,
			RequestSource:            SourceUser,
			UserAgent:                &ua,
			IPAddress:                &ip,
		},
		{
			CreatedAt:     time.Date(2026, 4, 16, 0, 0, 1, 0, time.UTC),
			Model:         "gpt-5.4",
			RequestSource: SourceProviderTest,
			TotalCost:     0.1,
			SystemUsage:   &SystemUsage{CostState: "unknown"},
		},
	})
	require.NoError(t, err)

	raw := buf.String()
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(raw, utf8BOM))).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 3)
	require.Len(t, records[0], 38)

	first := records[1]
	require.Equal(t, "2026-04-16T08:00:00+08:00", first[0])
	require.Equal(t, "'+admin@example.com", first[1])
	require.Equal(t, "claude-opus-alias", first[4])
	require.Equal(t, "是", first[7])
	require.Equal(t, "XHigh", first[8])
	require.Equal(t, "High", first[9])
	require.Equal(t, "1.500", first[22])
	require.Equal(t, "0.8500", first[23])
	require.Equal(t, "0.170000", first[26])
	require.Equal(t, "100", first[27])
	require.Equal(t, "首包", first[28])
	require.Equal(t, "100.0", first[30])
	require.Contains(t, first[31], "upstream_stream=true; upstream_complete=true; usage_complete=true")
	require.Contains(t, first[31], "upstream_started_ms=50ms")
	require.Contains(t, first[31], "attempt_number=1")
	require.Contains(t, first[31], "upstream_connection_reused=true")
	require.Equal(t, "'@malicious-agent", first[36])

	second := records[2]
	require.Equal(t, "", second[26])
	require.Equal(t, "手动提供商测试", second[34])
	require.Equal(t, "unknown", second[35])
}
