package openai

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestUsageAccumulatorRetainsIndependentImageBuckets interleaves image details,
// cache counts, and sparse text updates. Explicit bucket zeroes and complete
// zero snapshots must clear only the observations authorized by their shape.
func TestUsageAccumulatorRetainsIndependentImageBuckets(t *testing.T) {
	var accumulator UsageAccumulator
	require.Equal(t, ForwardUsage{}, accumulator.ForwardUsage())
	accumulator.Observe([]byte(`{"input_tokens":100,"output_tokens":3,"input_tokens_details":{"cached_tokens":60,"image_tokens":40},"output_tokens_details":{"image_tokens":2}}`))
	accumulator.Observe([]byte(`{"output_tokens":10}`))
	require.Equal(t, ForwardUsage{
		InputTokens: 100, OutputTokens: 10, CacheReadInputTokens: 60,
		ImageInputTokens: 40, ImageOutputTokens: 2,
	}, accumulator.ForwardUsage())
	accumulator.Observe([]byte(`{"input_tokens_details":{"image_tokens":0}}`))
	require.Zero(t, accumulator.ForwardUsage().ImageInputTokens)
	require.Equal(t, 2, accumulator.ForwardUsage().ImageOutputTokens)
	require.Equal(t, 60, accumulator.ForwardUsage().CacheReadInputTokens)
	accumulator.Observe([]byte(`{"input_tokens":0,"output_tokens":0,"total_tokens":0}`))
	require.Equal(t, ForwardUsage{}, accumulator.ForwardUsage())
	require.Equal(t, ResponsesUsage{}, *accumulator.Snapshot())
}

// TestUsageAccumulatorRetainsRawCountsAfterTypedFailure supplies incompatible
// total or detail fields beside usable token observations. Raw accounting must
// survive those failures and subsequent sparse updates, while invalid JSON is ignored.
func TestUsageAccumulatorRetainsRawCountsAfterTypedFailure(t *testing.T) {
	for _, raw := range []string{
		`{"prompt_tokens":100,"completion_tokens":10,"total_tokens":"110","cached_tokens":60,"cache_write_tokens":20}`,
		`{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"audio_tokens":"bad","cached_tokens":60,"cache_write_tokens":20}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var typed ChatUsage
			require.Error(t, json.Unmarshal([]byte(raw), &typed))
			var accumulator UsageAccumulator
			accumulator.Observe([]byte(raw))
			want := ForwardUsage{
				InputTokens: 100, OutputTokens: 10,
				CacheReadInputTokens: 60, CacheCreationInputTokens: 20,
			}
			require.Equal(t, want, accumulator.ForwardUsage())
			for _, invalid := range []string{`{"input_tokens":999`, `null`, `[]`} {
				accumulator.Observe([]byte(invalid))
				require.Equal(t, want, accumulator.ForwardUsage())
			}
			last := accumulator.Observe([]byte(`{"completion_tokens":12}`))
			want.OutputTokens = 12
			require.Equal(t, want, accumulator.ForwardUsage())
			require.Equal(t, 112, last.TotalTokens)
			require.Equal(t, 60, last.InputTokensDetails.CachedTokens)
		})
	}
}

// TestUsageAccumulatorPairsHostedImagesWithUsage supplies hosted and base image
// details in competing envelopes. Nested observations win only for supplied
// categories, and unrelated envelopes cannot manufacture image accounting.
func TestUsageAccumulatorPairsHostedImagesWithUsage(t *testing.T) {
	var accumulator UsageAccumulator
	accumulator.ObserveResponsesEvent([]byte(`{"usage":{"input_tokens":100,"output_tokens":3},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":40},"output_tokens_details":{"image_tokens":2}}}}`), nil)
	require.Equal(t, 40, accumulator.ForwardUsage().ImageInputTokens)
	require.Equal(t, 2, accumulator.ForwardUsage().ImageOutputTokens)
	accumulator.ObserveResponsesEvent([]byte(`{"usage":{"output_tokens":8},"tool_usage":{"image_gen":{"output_tokens_details":{"image_tokens":4}}},"response":{"usage":{"input_tokens_details":{"image_tokens":10}},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":90},"output_tokens_details":{"image_tokens":5}}}}}`), nil)
	require.Equal(t, ForwardUsage{
		InputTokens: 100, OutputTokens: 8, ImageInputTokens: 10, ImageOutputTokens: 5,
	}, accumulator.ForwardUsage())
	var isolated UsageAccumulator
	isolated.ObserveResponsesEvent([]byte(`{"tool_usage":{"image_gen":{"output_tokens_details":{"image_tokens":999}}},"response":{"usage":{"input_tokens":100,"output_tokens":3}}}`), nil)
	require.Zero(t, isolated.ForwardUsage().ImageOutputTokens)
}

// TestUsageDecodeMatchesRawCacheAccounting supplies canonical fields and
// compatible cache aliases. Typed Chat/Responses output must retain exactly
// the cache buckets selected by the raw billing parser.
func TestUsageDecodeMatchesRawCacheAccounting(t *testing.T) {
	tests := []struct {
		name  string // fixture display label
		raw   string // complete upstream usage object
		input int    // expected inclusive input
		read  int    // expected cached input
		write int    // expected cache writes
	}{
		{"canonical", `{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":20}}`, 100, 60, 20},
		{"chat", `{"prompt_tokens":100,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":60,"cache_creation_tokens":20}}`, 100, 60, 20},
		{"aliases", `{"prompt_tokens":100,"completion_tokens":10,"cache_read_tokens":60,"cache_write_input_tokens":20}`, 100, 60, 20},
		{"deepseek", `{"prompt_cache_hit_tokens":60,"prompt_cache_miss_tokens":40,"completion_tokens":10}`, 100, 60, 0},
		{"nested zero", `{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0,"cache_creation_tokens":20},"cache_read_input_tokens":60,"cache_write_tokens":20}`, 100, 0, 0},
		{"detail precedence", `{"input_tokens":100,"output_tokens":10,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":20},"prompt_tokens_details":{"cached_tokens":99,"cache_write_tokens":88}}`, 100, 60, 20},
		{"independent reasoning", `{"prompt_tokens":100,"completion_tokens":6,"total_tokens":110,"completion_tokens_details":{"reasoning_tokens":4},"cached_tokens":60,"cache_write_tokens":20}`, 100, 60, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var responses ResponsesUsage
			var chat ChatUsage
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &responses))
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &chat))
			billing, ok := OpenAIUsageFromGJSON(gjson.Parse(tt.raw))
			require.True(t, ok)
			require.Equal(t, billing, CopyForwardUsage(&responses))
			require.Equal(t, tt.input, responses.InputTokens)
			require.Equal(t, tt.input, chat.PromptTokens)
			require.Equal(t, tt.input+10, chat.TotalTokens)
			require.Equal(t, tt.read, chat.PromptTokensDetails.CachedTokens)
			require.Equal(t, tt.write, chat.PromptTokensDetails.CacheCreationTokens)
			require.Equal(t, tt.write, responses.CacheCreationInputTokens)
		})
	}
}

// TestPatchResponseUsageRetainsNativeFieldsWhileFillingCache repairs a sparse
// terminal response from earlier observed usage. Vendor numbers and unknown
// fields must retain their original bytes while missing cache totals appear.
func TestPatchResponseUsageRetainsNativeFieldsWhileFillingCache(t *testing.T) {
	var accumulator UsageAccumulator
	accumulator.Observe([]byte(`{"input_tokens":100,"output_tokens":3,"input_tokens_details":{"cached_tokens":60},"cache_write_tokens":20}`))
	final := accumulator.Observe([]byte(`{"output_tokens":10}`))
	body := []byte(`{"id":"resp_cache","usage":{"output_tokens":10,"vendor":9007199254740993},"extra":1.234e+09}`)
	patched, err := PatchResponseUsage(body, final)
	require.NoError(t, err)
	require.Contains(t, string(patched), `"vendor":9007199254740993`)
	require.Contains(t, string(patched), `"extra":1.234e+09`)
	require.Equal(t, int64(100), gjson.GetBytes(patched, "usage.input_tokens").Int())
	require.Equal(t, int64(60), gjson.GetBytes(patched, "usage.input_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(20), gjson.GetBytes(patched, "usage.cache_creation_input_tokens").Int())
	require.Equal(t, int64(110), gjson.GetBytes(patched, "usage.total_tokens").Int())
}

// TestUsageAccumulatorPreservesSparseCacheFields sends repeated and partial
// observations before an explicit zero reset. Earlier snapshots and missing
// cache categories must survive without double-counting the input total.
func TestUsageAccumulatorPreservesSparseCacheFields(t *testing.T) {
	var accumulator UsageAccumulator
	first := accumulator.Observe([]byte(`{"prompt_tokens":100,"completion_tokens":3,"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":20}}`))
	last := accumulator.Observe([]byte(`{"completion_tokens":10}`))
	require.Equal(t, 3, first.OutputTokens)
	require.Equal(t, 100, last.InputTokens)
	require.Equal(t, 110, last.TotalTokens)
	require.Equal(t, 60, last.InputTokensDetails.CachedTokens)
	require.Equal(t, 20, last.CacheCreationInputTokens)
	last = accumulator.Observe([]byte(`{"completion_tokens":10}`))
	require.Equal(t, 110, last.TotalTokens)
	last = accumulator.Observe([]byte(`{"completion_tokens":10,"total_tokens":0}`))
	require.Equal(t, 110, last.TotalTokens)
	last = accumulator.Observe([]byte(`{"input_tokens_details":{"cached_tokens":0}}`))
	require.Zero(t, last.InputTokensDetails.CachedTokens)
	require.Equal(t, 60, first.InputTokensDetails.CachedTokens)
	require.Equal(t, 20, last.CacheCreationInputTokens)
	last = accumulator.Observe([]byte(`{"input_tokens":0,"output_tokens":0,"total_tokens":0}`))
	require.Equal(t, ResponsesUsage{}, *last)
}

// TestUsageAccumulatorSharesTerminalPrecedenceWithClient supplies top-level and
// nested usage in one terminal frame. The emitted event and accounting
// snapshot must agree while retaining cache from an earlier frame.
func TestUsageAccumulatorSharesTerminalPrecedenceWithClient(t *testing.T) {
	var accumulator UsageAccumulator
	accumulator.Observe([]byte(`{"input_tokens":100,"input_tokens_details":{"cached_tokens":60},"output_tokens":1}`))
	raw := []byte(`{"type":"response.completed","usage":{"output_tokens":8},"response":{"id":"resp_cache","usage":{"output_tokens":10}}}`)
	var event ResponsesStreamEvent
	require.NoError(t, json.Unmarshal(raw, &event))
	final := accumulator.ObserveResponsesEvent(raw, &event)
	require.Equal(t, 100, final.InputTokens)
	require.Equal(t, 10, final.OutputTokens)
	require.Equal(t, 60, final.InputTokensDetails.CachedTokens)
	require.Equal(t, final, event.Usage)
	require.Equal(t, final, event.Response.Usage)
	event.Response.Usage.InputTokensDetails.CachedTokens = 1
	require.Equal(t, 60, accumulator.Snapshot().InputTokensDetails.CachedTokens)
}
