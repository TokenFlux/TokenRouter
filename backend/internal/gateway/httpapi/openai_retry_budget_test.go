package httpapi

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesRejectedFieldRetryStateForRequestAllowsSameTransformAcrossAccounts(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	initialBody := []byte(`{"model":"gpt-5.5","truncation":"auto"}`)
	retryBody := []byte(`{"model":"gpt-5.5"}`)

	accountA := openAIResponsesRejectedFieldRetryStateForRequest(c, initialBody)
	require.True(t, accountA.Allow(retryBody))
	require.False(t, accountA.Allow(retryBody), "one account must not repeat the same transform")

	accountB := openAIResponsesRejectedFieldRetryStateForRequest(c, initialBody)
	require.NotSame(t, accountA, accountB)
	require.Same(t, accountA.Budget(), accountB.Budget())
	require.True(t, accountB.Allow(retryBody), "a failover account must be allowed to apply the same transform")
}
func TestOpenAIResponsesRejectedFieldRetryStateForRequestSharesBoundedBudgetAcrossAccounts(t *testing.T) {

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for attempt := 0; attempt < openai.MaxResponsesRejectedFieldRetries; attempt++ {
		state := openAIResponsesRejectedFieldRetryStateForRequest(c, []byte(fmt.Sprintf(`{"account":%d}`, attempt)))
		require.True(t, state.Allow([]byte(`{"same":"retry"}`)))
	}
	overflow := openAIResponsesRejectedFieldRetryStateForRequest(c, []byte(`{"account":"overflow"}`))
	require.False(t, overflow.Allow([]byte(`{"new":"retry"}`)))
}
