package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQoderGatewayNonStreamingReadDoesNotCommitResponseBeforeError(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewBufferString(
			"data: {\"headers\":{\"Content-Type\":[\"application/json\"]},\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
		)),
	}

	events, err := qoder.ReadQoderSSEEventsContext(context.Background(), resp, nil)

	require.Error(t, err)
	require.Empty(t, events)
	require.Empty(t, rec.Body.String())
	require.Empty(t, rec.Header().Get("Cache-Control"))
	require.False(t, c.Writer.Written())
}
