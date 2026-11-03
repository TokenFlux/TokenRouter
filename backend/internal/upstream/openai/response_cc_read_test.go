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

// TestScanCCStreamPreservesCacheAliasesAcrossSparseTail sends cache aliases
// followed by output-only usage. Both the serialized client usage and internal
// accounting must retain the same inclusive input and separate cache buckets.
func TestScanCCStreamPreservesCacheAliasesAcrossSparseTail(t *testing.T) {
	body := "data: " + `{"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":3,"cache_read_input_tokens":60,"cache_write_tokens":20}}` + "\n\n" +
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
	encoded, err := json.Marshal(last)
	require.NoError(t, err)
	require.Equal(t, int64(100), gjson.GetBytes(encoded, "usage.prompt_tokens").Int())
	require.Equal(t, int64(110), gjson.GetBytes(encoded, "usage.total_tokens").Int())
	require.Equal(t, int64(60), gjson.GetBytes(encoded, "usage.prompt_tokens_details.cached_tokens").Int())
	require.Equal(t, int64(20), gjson.GetBytes(encoded, "usage.prompt_tokens_details.cache_creation_tokens").Int())
}
