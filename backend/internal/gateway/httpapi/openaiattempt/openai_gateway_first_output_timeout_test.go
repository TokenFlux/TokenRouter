package openaiattempt

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	httpapi "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIForwardMayFailoverOnlyAfterNonSemanticWrite(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	before := httpapi.OpenAICompactKeepaliveAdjustedWrittenSize(c)

	_, err := fmt.Fprint(c.Writer, ":\n\n")
	require.NoError(t, err)
	c.Writer.Flush()

	require.True(t, OpenAIForwardMayFailover(c, before, &forwardcore.UpstreamFailoverError{
		SafeToFailoverAfterWrite: true,
	}))
	require.False(t, OpenAIForwardMayFailover(c, before, &forwardcore.UpstreamFailoverError{}))
}

func TestOpenAIFirstOutputFailoverStopsAfterOneAccountSwitch(t *testing.T) {
	failoverErr := &forwardcore.UpstreamFailoverError{SafeToFailoverAfterWrite: true}
	count := 0

	require.False(t, failover.FirstOutputExhausted(failoverErr.SafeToFailoverAfterWrite, &count))
	require.Equal(t, 1, count)
	require.True(t, failover.FirstOutputExhausted(failoverErr.SafeToFailoverAfterWrite, &count))
	require.Equal(t, 1, count)
}

func TestOpenAIRequestAllowsFailoverReplayStopsCanceledClient(t *testing.T) {
	require.False(t, OpenAIRequestAllowsFailoverReplay(nil))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	requestCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil).WithContext(requestCtx)

	require.True(t, OpenAIRequestAllowsFailoverReplay(c))
	cancel()
	require.False(t, OpenAIRequestAllowsFailoverReplay(c))
}
