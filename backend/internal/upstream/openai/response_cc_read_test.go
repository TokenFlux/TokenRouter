package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// TestScanCCStreamRetainsUsageWhenTypedDecodingFails supplies usable token counts
// with a string aggregate that the client decoder rejects. Accounting must retain
// those counts and merge a later sparse update without emitting the rejected chunk.
func TestScanCCStreamRetainsUsageWhenTypedDecodingFails(t *testing.T) {
	for _, tail := range []string{"", "data: " + `{"choices":[],"usage":{"completion_tokens":12}}` + "\n\n"} {
		t.Run(tail, func(t *testing.T) {
			body := "data: " + `{"choices":[{"index":0,"delta":{"content":"OK"}}]}` + "\n\n" +
				"data: " + `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":"110","cache_read_tokens":60,"cache_write_tokens":20}}` + "\n\n" +
				tail + "data: [DONE]\n\n"
			response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
			var chunks []wire.ChatCompletionsChunk
			observed := ScanCCStream(response, CCResponseOptions{
				ObserveChunk: func([]byte) {},
				ServiceTier:  func() string { return "" },
			}, "usage test", "request", time.Now(), func(chunk *wire.ChatCompletionsChunk) {
				chunks = append(chunks, *chunk)
			})
			require.NoError(t, observed.Err)
			require.True(t, observed.SawDone)
			require.Equal(t, 100, observed.Usage.InputTokens)
			require.Equal(t, 60, observed.Usage.CacheReadInputTokens)
			require.Equal(t, 20, observed.Usage.CacheCreationInputTokens)
			if tail == "" {
				require.Equal(t, 10, observed.Usage.OutputTokens)
				require.Len(t, chunks, 1)
				return
			}
			require.Equal(t, 12, observed.Usage.OutputTokens)
			require.Len(t, chunks, 2)
			require.Equal(t, 100, chunks[1].Usage.PromptTokens)
			require.Equal(t, 112, chunks[1].Usage.TotalTokens)
			require.Equal(t, 60, chunks[1].Usage.PromptTokensDetails.CachedTokens)
		})
	}
}

// TestScanCCStreamPreservesCacheAliasesAcrossSparseTail sends cache aliases
// followed by output-only usage. Both the serialized client usage and internal
// accounting must retain the same inclusive input and separate cache buckets.
func TestScanCCStreamPreservesCacheAliasesAcrossSparseTail(t *testing.T) {
	body := "data: " + `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":3,"cache_read_input_tokens":60,"cache_write_tokens":20,"prompt_tokens_details":{"image_tokens":40},"completion_tokens_details":{"image_tokens":2}}}` + "\n\n" +
		"data: " + `{"choices":[],"usage":{"completion_tokens":10}}` + "\n\ndata: [DONE]\n\n"
	response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	var last wire.ChatCompletionsChunk
	got := ScanCCStream(response, CCResponseOptions{
		ObserveChunk: func([]byte) {}, ServiceTier: func() string { return "" },
	}, "cache test", "request", time.Now(), func(chunk *wire.ChatCompletionsChunk) { last = *chunk })
	require.NoError(t, got.Err)
	require.True(t, got.SawDone)
	require.Equal(t, 100, got.Usage.InputTokens)
	require.Equal(t, 60, got.Usage.CacheReadInputTokens)
	require.Equal(t, 20, got.Usage.CacheCreationInputTokens)
	require.Equal(t, 10, got.Usage.OutputTokens)
	require.Equal(t, 40, got.Usage.ImageInputTokens)
	require.Equal(t, 2, got.Usage.ImageOutputTokens)
	encoded, err := json.Marshal(last)
	require.NoError(t, err)
	require.Equal(t, int64(100), gjson.GetBytes(encoded, "usage.prompt_tokens").Int())
	require.Equal(t, int64(110), gjson.GetBytes(encoded, "usage.total_tokens").Int())
	require.Equal(t, int64(60), gjson.GetBytes(encoded, "usage.prompt_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(20), gjson.GetBytes(encoded, "usage.prompt_tokens_details.cache_creation_tokens").Int())
}
