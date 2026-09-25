package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsErrorLoggerMiddleware_DedicatedCyberSessionBlockRecordsExactlyOnce(t *testing.T) {
	queue := newOpsCaptureQueue(3)

	ops := opscore.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Ops: ops, Queue: queue})
	apiKey := &apikey.APIKey{ID: 41, Key: "sk-dedicated-test"}
	router := gin.New()
	router.Use(gatewayhttp.OpsErrorLoggerMiddleware(ops, queue, gatewayhttp.OpsObservationAccess{}))
	router.POST("/v1/responses", func(c *gin.Context) {
		h.enqueueCyberSessionBlockedOpsEntry(c, apiKey, "gpt-test", "session-block-hash")
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"type": "permission_error", "code": "session_blocked_by_cyber_policy", "message": "blocked",
		}})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Equal(t, int64(1), queue.health.Length)
	job := <-queue.jobs
	require.Equal(t, "cyber_policy_session_blocked", job.entry.ErrorType)
	require.Equal(t, http.StatusForbidden, job.entry.StatusCode)
}
