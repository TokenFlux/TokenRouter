package admin

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsOpsRealtimeRequestCanceled(t *testing.T) {
	require.False(t, isOpsRealtimeRequestCanceled(nil, nil))
	require.True(t, isOpsRealtimeRequestCanceled(nil, context.Canceled))
	require.True(t, isOpsRealtimeRequestCanceled(nil, errors.New("pq: canceling statement due to user request")))

	// 驱动错误可能丢失 context.Canceled 包装，此时继续检查原始请求上下文。
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &gin.Context{Request: httptest.NewRequest("GET", "/api/v1/admin/ops/concurrency", nil).WithContext(requestCtx)}
	require.True(t, isOpsRealtimeRequestCanceled(c, errors.New("query failed")))
	require.False(t, isOpsRealtimeRequestCanceled(&gin.Context{}, errors.New("query failed")))
}
