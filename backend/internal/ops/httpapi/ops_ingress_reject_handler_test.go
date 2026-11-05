package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"
)

// newIngressRejectHandlerForTest constructs an Ops handler with default
// monitoring enabled and no ingress repository or aggregator. Valid health
// reads therefore expose capacity alone, while malformed filters fail during
// HTTP validation.
func newIngressRejectHandlerForTest() *OpsHandler {
	return NewOpsHandler(ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{}), OpsWSOptions{})
}

// TestListIngressRejectsValidatesFilters supplies an unknown reason, an invalid
// address, and nonpositive caller identities to the aggregate endpoint. Each
// case must return bad request before repository availability matters.
func TestListIngressRejectsValidatesFilters(t *testing.T) {
	for _, path := range []string{
		"/api/v1/admin/ops/ingress-rejections?reason=not-valid",
		"/api/v1/admin/ops/ingress-rejections?client_ip=not-an-ip",
		"/api/v1/admin/ops/ingress-rejections?user_id=0",
		"/api/v1/admin/ops/ingress-rejections?api_key_id=-1",
	} {
		t.Run(path, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest(http.MethodGet, path, nil)
			newIngressRejectHandlerForTest().ListIngressRejects(context)
			require.Equal(t, http.StatusBadRequest, context.Writer.Status())
		})
	}
}

// TestGetIngressRejectHealthAvailable reads health from an Ops service with no
// aggregator bound. It checks HTTP success and the configured capacity so an
// idle fixture still exposes the aggregation limit.
func TestGetIngressRejectHealthAvailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/ingress-rejections/health", nil)
	newIngressRejectHandlerForTest().GetIngressRejectHealth(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"capacity":8192`)
}
