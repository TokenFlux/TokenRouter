package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

// TestSystemOneCompositeErrorUsesDecisionEndpoint 检查独立端点识别及客户端可读取的复合前缀错误。
func TestSystemOneCompositeErrorUsesDecisionEndpoint(t *testing.T) {
	for _, path := range []string{EndpointSystemOne, EndpointSystemOne + "/"} {
		require.True(t, IsSystemOneEndpoint(path))
		require.False(t, IsOpenAICompositeEndpoint(path))
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, path, nil)
		AbortCompositeKeyError(c, apikey.ErrCompositeKeyPrefixRequired)
		require.True(t, c.IsAborted())
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), `"code":"COMPOSITE_KEY_MODEL_PREFIX_REQUIRED"`)
		require.Contains(t, rec.Body.String(), `"param":"model"`)
	}
	require.False(t, IsSystemOneEndpoint("/v1/chat/completions"))
	require.False(t, IsSystemOneEndpoint("/other/systemone"))
	require.True(t, IsOpenAICompositeEndpoint("/v1/chat/completions"))
}
