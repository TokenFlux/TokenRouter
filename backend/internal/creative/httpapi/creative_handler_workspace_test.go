//go:build unit

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	service "github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestCreativeRunScopeFromRequest 校验工作区 header 的缺失、非法值和大小写规范化。
func TestCreativeRunScopeFromRequest(t *testing.T) {

	t.Run("缺失 header", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		_, err := creativeRunScopeFromRequest(c, 7)
		require.ErrorIs(t, err, service.ErrCreativeWorkspaceRequired)
	})

	t.Run("非法 UUID", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = &http.Request{Header: make(http.Header)}
		c.Request.Header.Set(service.CreativeWorkspaceHeader, "invalid")
		_, err := creativeRunScopeFromRequest(c, 7)
		require.ErrorIs(t, err, service.ErrCreativeWorkspaceInvalid)
	})

	t.Run("规范化 UUID", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = &http.Request{Header: make(http.Header)}
		c.Request.Header.Set(service.CreativeWorkspaceHeader, "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA")
		scope, err := creativeRunScopeFromRequest(c, 7)
		require.NoError(t, err)
		require.Equal(t, service.CreativeRunScope{UserID: 7, WorkspaceID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}, scope)
	})
}
