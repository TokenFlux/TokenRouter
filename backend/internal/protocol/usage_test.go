package protocol

import (
	"testing"
)

// usage_test.go verifies cache-TTL reclassification and independent reasoning
// overlap rules on protocol usage counters. Cache-write usage can arrive as an
// aggregate, a detail-only projection, or mismatched aggregate and detail
// counts, while reasoning observations can overlap reported output; the
// fixtures protect bucket movement and unrelated usage fields.

// TestApplyCacheTTLOverrideContract reclassifies absent, partial, duplicate,
// detail-only, and already classified cache-write details with known and
// unknown TTL targets. Positive aggregates must govern total reclassified
// tokens, the changed flag must distinguish category changes from initial
// aggregate filling, and unrelated usage fields must retain their values.
func TestApplyCacheTTLOverrideContract(t *testing.T) {
	tests := []struct {
		// target supplies the exact TTL selection passed to ApplyCacheTTLOverride.
		// Only 1h selects the hour category; other spellings in these fixtures
		// select the five-minute category.
		// name: fixture subtest display label
		// target: requested cache-write TTL category
		name, target string
		// aggregate supplies CacheCreationInputTokens while five and hour
		// initialize the two TTL detail counters. A positive aggregate governs the
		// reclassified total; zero aggregates fall back to the detail sum.
		// aggregate: initial aggregate cache-write tokens
		// five: initial five-minute cache-write tokens
		// hour: initial one-hour cache-write tokens
		aggregate, five, hour int
		// wantFive and wantHour are compared after reclassification and default to
		// zero when omitted by a fixture. Their expected details are separate from
		// changed, which reports category movement.
		// wantFive: expected five-minute cache-write tokens
		// wantHour: expected one-hour cache-write tokens
		wantFive, wantHour int
		changed            bool // expected category movement
	}{
		{name: "empty", target: "1h"},
		{name: "aggregate fills 5m without reporting override", target: "5m", aggregate: 9, wantFive: 9},
		{name: "aggregate reclassified to 1h", target: "1h", aggregate: 9, wantHour: 9, changed: true},
		{name: "positive aggregate governs partial details to 1h", target: "1h", aggregate: 311, five: 211, hour: 0, wantHour: 311, changed: true},
		{name: "positive aggregate governs partial details to 5m", target: "5m", aggregate: 311, five: 211, hour: 0, wantFive: 311, changed: true},
		{name: "positive aggregate caps duplicate details to 1h", target: "1h", aggregate: 10, five: 10, hour: 10, wantHour: 10, changed: true},
		{name: "positive aggregate caps duplicate details to 5m", target: "5m", aggregate: 10, five: 10, hour: 10, wantFive: 10, changed: true},
		{name: "detail-only projection falls back to detail sum", target: "1h", five: 3, hour: 4, wantHour: 7, changed: true},
		{name: "mixed to 5m", target: "5m", five: 3, hour: 4, wantFive: 7, changed: true},
		{name: "already 1h", target: "1h", hour: 7, wantHour: 7},
		{name: "already 5m", target: "5m", five: 7, wantFive: 7},
		{name: "unknown target uses 5m", target: "other", hour: 7, wantFive: 7, changed: true},
		{name: "target remains case sensitive", target: "1H", hour: 7, wantFive: 7, changed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value := TokenUsage{InputTokens: 13, OutputTokens: 17, CacheReadInputTokens: 19, CacheCreationInputTokens: tt.aggregate, CacheCreation5mTokens: tt.five, CacheCreation1hTokens: tt.hour, ImageOutputTokens: 23, Speed: "fast"}
			original := value
			if got := ApplyCacheTTLOverride(&value, tt.target); got != tt.changed {
				t.Fatalf("changed=%v, want %v", got, tt.changed)
			}
			if value.CacheCreation5mTokens != tt.wantFive || value.CacheCreation1hTokens != tt.wantHour {
				t.Fatalf("buckets=(%d,%d), want (%d,%d)", value.CacheCreation5mTokens, value.CacheCreation1hTokens, tt.wantFive, tt.wantHour)
			}
			value.CacheCreation5mTokens = original.CacheCreation5mTokens
			value.CacheCreation1hTokens = original.CacheCreation1hTokens
			if value != original {
				t.Fatal("reclassification changed unrelated usage fields")
			}
		})
	}
}

// TestIncludeIndependentReasoningTokens supplies separate reasoning counts,
// inclusive output, incomplete gaps, and inconsistent totals. Added reasoning
// must fit the observed gap and its own count, while unusable totals leave
// the supplied output unchanged.
func TestIncludeIndependentReasoningTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                         string // reasoning usage scenario
		input, output, total, reason int64  // supplied token observations
		want                         int64  // expected inclusive output
	}{
		{name: "xAI chat example", input: 32, output: 9, total: 135, reason: 94, want: 103},
		{name: "xAI responses example", input: 32, output: 9, total: 151, reason: 110, want: 119},
		{name: "OpenAI inclusive output", input: 32, output: 103, total: 135, reason: 94, want: 103},
		{name: "gap smaller than reasoning", input: 10, output: 20, total: 33, reason: 5, want: 23},
		{name: "absent total", input: 10, output: 20, total: 0, reason: 5, want: 20},
		{name: "total already complete", input: 10, output: 20, total: 30, reason: 5, want: 20},
		{name: "inconsistent undershoot", input: 10, output: 20, total: 25, reason: 5, want: 20},
		{name: "zero reasoning", input: 10, output: 20, total: 35, reason: 0, want: 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IncludeIndependentReasoningTokens(tt.input, tt.output, tt.total, tt.reason)
			if got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}
