//go:build unit

package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthHandlerRevokeAllSessionsInvalidatesAccessTokens(t *testing.T) {

	repo := &userHandlerRepoStub{
		user: &identity.User{
			ID:           29,
			Email:        "session@example.com",
			Username:     "session-user",
			Role:         identity.RoleUser,
			Status:       billing.StatusActive,
			TokenVersion: 7,
		},
	}
	refreshTokenCache := &userHandlerRefreshTokenCacheStub{}
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
	}
	authService := newUserBindingAuth(repo, refreshTokenCache, cfg, nil, nil)
	handler := identityhttp.NewSessionHandler(authService, nil, nil, nil, nil, nil, identityhttp.SessionHTTPOptions{})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/revoke-all-sessions", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 29})

	handler.RevokeAllSessions(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{29}, refreshTokenCache.revokedUserIDs)
	// users 表没有 token_version 列（见 resolvedTokenVersion：JWT 里的值由
	// email+password_hash 指纹推导），所以自增 TokenVersion 只停留在内存里。
	// 此前紧跟其后的整行 Update 不写任何有效数据，却会用旧快照覆盖并发写入的列，
	// 已移除。会话撤销由上面的 refresh session 清理承担。
	require.Equal(t, int64(7), repo.user.TokenVersion)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "All sessions have been revoked. Please log in again.", resp.Data.Message)
}
