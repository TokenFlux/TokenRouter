package authctx

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"testing"
)

// TestPrincipalOwnsLegacyProjection 防止旧 Gin 展示字段成为第二份可变认证来源。
func TestPrincipalOwnsLegacyProjection(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	principal := identity.Principal{UserID: 7, Role: "user", SessionID: "session-1", CredentialKind: "jwt"}
	SetPrincipal(c, principal, 3, "s05@example.invalid")
	c.Set(ContextKeyUser, AuthSubject{UserID: 99, Concurrency: 999})
	c.Set(ContextKeyUserRole, "admin")
	got, ok := GetPrincipal(c)
	require.True(t, ok)
	require.Equal(t, principal, got)
	subject, ok := GetAuthSubjectFromContext(c)
	require.True(t, ok)
	require.Equal(t, AuthSubject{UserID: 7, Concurrency: 3}, subject)
	role, ok := GetUserRoleFromContext(c)
	require.True(t, ok)
	require.Equal(t, "user", role)
}

// TestAPIKeyPrincipalKeepsActorSeparateFromPayer 保留团队付款上下文，不赋予行为 Key 管理员身份。
func TestAPIKeyPrincipalKeepsActorSeparateFromPayer(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set(ContextKeyUser, AuthSubject{UserID: 11, Concurrency: 4})
	c.Set(ContextKeyUserRole, "admin")
	SetAuthenticatedPrincipal(c, identity.Principal{UserID: 22, CredentialKind: "api_key"})
	p, ok := GetPrincipal(c)
	require.True(t, ok)
	require.Equal(t, int64(22), p.UserID)
	require.Empty(t, p.Role)
	require.Empty(t, p.SessionID)
	subject, ok := GetAuthSubjectFromContext(c)
	require.True(t, ok)
	require.Equal(t, int64(11), subject.UserID)
	role, ok := GetUserRoleFromContext(c)
	require.True(t, ok)
	require.Equal(t, "admin", role, "旧网关展示投影仍按原付款用户提供")
}

// TestLegacyOnlyContextRemainsReadable 兼容尚未切换新写入入口的消费者及 HTTP 测试替身。
func TestLegacyOnlyContextRemainsReadable(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)
	c.Set(ContextKeyUser, AuthSubject{UserID: 9})
	c.Set(ContextKeyUserRole, "user")
	_, ok := GetPrincipal(c)
	require.False(t, ok)
	subject, ok := GetAuthSubjectFromContext(c)
	require.True(t, ok)
	require.Equal(t, int64(9), subject.UserID)
	role, ok := GetUserRoleFromContext(c)
	require.True(t, ok)
	require.Equal(t, "user", role)
}
