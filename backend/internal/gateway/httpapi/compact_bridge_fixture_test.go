package httpapi

import (
	"net/http"
	"net/http/httptest"

	"testing"

	"github.com/gin-gonic/gin"
)

func newCompactBridgeTestContext(t *testing.T, markClientStream bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	if markClientStream {
		MarkOpenAICompactClientStream(c)
	}
	return c, rec
}
