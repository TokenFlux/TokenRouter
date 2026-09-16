package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSendMockInterceptResponse_MaxTokensOneHaiku(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	WriteInterceptResponse(ctx, "claude-haiku-4-5", clientmeta.InterceptTypeMaxTokensOneHaiku)

	require.Equal(t, http.StatusOK, rec.Code)

	var response map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Equal(t, "max_tokens", response["stop_reason"])

	id, ok := response["id"].(string)
	require.True(t, ok)
	require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, id)
	require.Contains(t, response, "stop_details")
	require.Nil(t, response["stop_details"])

	content, ok := response["content"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, content)

	firstBlock, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "#", firstBlock["text"])

	usage, ok := response["usage"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(1), usage["output_tokens"])
	require.NotContains(t, usage, "total_tokens")
}

func TestSendMockInterceptStream_UsesAnthropicSchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)

	WriteInterceptStream(ctx, "claude-sonnet-4-5", clientmeta.InterceptTypeSuggestionMode)

	body := rec.Body.String()
	require.Regexp(t, `"id":"msg_01[0-9A-Za-z]{22}"`, body)
	require.Contains(t, body, `"stop_details":null`)
	require.Contains(t, body, `"cache_creation_input_tokens":0`)
	require.Contains(t, body, `"cache_read_input_tokens":0`)
	require.Contains(t, body, `"usage":{"output_tokens":1}`)
	require.NotContains(t, body, `"usage":{"input_tokens":10,"output_tokens":1}`)
}
