package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestQoderGatewayStreamingAwareError_ResponsesStreamingEmitsResponseFailed(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpsRequestContext(c, "deepseek-v4-pro", true)

	WriteQoderStreamError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, QoderResponses)

	body := w.Body.String()
	assert.Contains(t, body, "event: response.failed\n")
	assert.NotContains(t, body, `"type":"error"`)
	resp, errObj := parseResponsesFailedSSE(t, body)
	assert.Equal(t, "failed", resp["status"])
	assert.Equal(t, "deepseek-v4-pro", resp["model"])
	assert.Equal(t, "upstream_error", errObj["code"])
	assert.Equal(t, "Upstream request failed", errObj["message"])
}
