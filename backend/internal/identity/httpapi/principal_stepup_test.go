package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type principalStepUpEnabled struct{}

func (principalStepUpEnabled) IsStepUpEnabled(context.Context) bool { return true }

// TestStepUpUsesVerifiedPrincipal 验证旧展示字段不能把管理员密钥改造成带 sid 的人类会话。
func TestStepUpUsesVerifiedPrincipal(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin", nil)
	authctx.SetPrincipal(c, identity.Principal{UserID: 1, Role: "admin", CredentialKind: "admin_api_key"}, 1, "admin@example.invalid")
	c.Set("auth_method", "jwt")
	c.Set(authctx.ContextKeySessionID, "forged-display-session")
	require.False(t, EnforceStepUp(c, nil, nil, principalStepUpEnabled{}))
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	require.NotEqual(t, "forged-display-session", StepUpSessionKey(c, 1))
}

// TestStepUpUsesCanonicalJWTSession 保留旧无 sid JWT 的摘要回退，已带 sid 的 JWT 使用已验证会话。
func TestStepUpUsesCanonicalJWTSession(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest(http.MethodPost, "/admin", nil)
	authctx.SetPrincipal(c, identity.Principal{UserID: 1, Role: "admin", CredentialKind: "jwt", SessionID: "verified"}, 1, "")
	c.Set(authctx.ContextKeySessionID, "display-only")
	require.Equal(t, "verified", StepUpSessionKey(c, 1))
}
