package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFailoverClientGone(t *testing.T) {

	t.Run("活跃请求返回false", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

		require.False(t, FailoverClientGone(c))
		require.Equal(t, http.StatusOK, c.Writer.Status(), "不应改动状态码")
	})

	t.Run("客户端已断开_返回true并标记499", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)

		require.True(t, FailoverClientGone(c))
		require.Equal(t, StatusClientClosedRequest, c.Writer.Status())
	})

	t.Run("响应已提交_不改状态码", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		c.String(http.StatusOK, "partial")

		require.True(t, FailoverClientGone(c))
		require.Equal(t, http.StatusOK, c.Writer.Status(), "已提交的状态码不应被覆盖")
	})

	t.Run("nil安全", func(t *testing.T) {
		require.False(t, FailoverClientGone(nil))
	})
}
