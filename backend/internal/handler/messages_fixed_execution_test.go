package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 固定生产适配只复用构造期依赖，不把旧聚合 handler 放进请求会话。
func TestFixedMessagesOpenUsesBoundDependenciesAndExplicitState(t *testing.T) {

	dependencies := &messageExecutionDependencies{}
	runtime := &fixedMessagesRuntime{dependencies: dependencies}
	var previous *messageAttemptBridge
	for _, model := range []string{"first", "second"} {
		writer := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(writer)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		started := false
		sink := &gatewayhttp.MessagesOutput{ResponseSink: gatewayhttp.ResponseSink{Writer: c.Writer}, HTTP: c, Log: zap.NewNop(), StreamStarted: &started}
		input := execution.Request{Model: model, Body: []byte(model), UserID: 5, Funding: execution.FundingState{Key: &apikey.APIKey{ID: 9}}}
		ports, err := runtime.Open(context.Background(), input, sink)
		require.NoError(t, err)
		state, ok := ports.(*messageAttemptBridge)
		require.True(t, ok)
		require.Nil(t, state.h)
		require.Same(t, dependencies, state.fixed)
		require.Equal(t, model, state.reqModel)
		require.Equal(t, int64(9), state.apiKey.ID)
		if previous != nil {
			require.NotSame(t, previous, state)
			require.Equal(t, "first", previous.reqModel)
		}
		previous = state
	}
}

// 失败与已观测结果可同时返回，返回值不共享后续完成处理会修改的集合。
func TestMessageObservedAttemptRetainsPartialUsageAndCopiesMetadata(t *testing.T) {
	first := 12
	result := &forwardcore.MessagesResult{Model: "model", Usage: upstream.TokenUsage{OutputTokens: 2}, UpstreamHeaders: http.Header{"X-Request-Id": []string{"upstream"}}, FirstTokenMs: &first, ImageOutputSizes: []string{"1K"}}
	observed := messageObservedAttempt(result, context.Canceled)
	first = 99
	http.Header(result.UpstreamHeaders).Set("X-Request-Id", "changed")
	result.ImageOutputSizes[0] = "4K"
	require.True(t, observed.Cancelled)
	require.True(t, observed.HasUsage)
	require.True(t, observed.Served)
	require.Equal(t, 2, observed.Usage.OutputTokens)
	require.Equal(t, 12, *observed.FirstTokenMs)
	require.Equal(t, "upstream", observed.UpstreamHeaders.Get("X-Request-Id"))
	require.Equal(t, []string{"1K"}, observed.ImageOutputSizes)
}
