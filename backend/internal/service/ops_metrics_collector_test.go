package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWriteOpenAIFastPolicyBlockedResponseMarksBusinessLimited(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	writeOpenAIFastPolicyBlockedResponse(c, &tierpolicy.BlockedError{Message: "custom fast policy block"})

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.True(t, gatewayhttp.HasOpsClientBusinessLimited(c))
	reason, ok := c.Get(gatewayhttp.OpsClientBusinessLimitedReasonKey)
	require.True(t, ok)
	require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied, reason)
}
