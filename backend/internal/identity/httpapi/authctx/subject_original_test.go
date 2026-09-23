//go:build unit

package authctx_test

import (
	"testing"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthSubjectHelpers_RoundTrip(t *testing.T) {
	c := &gin.Context{}
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1, Concurrency: 2})
	c.Set(string(authctx.ContextKeyUserRole), "admin")

	sub, ok := authctx.GetAuthSubjectFromContext(c)
	require.True(t, ok)
	require.Equal(t, int64(1), sub.UserID)
	require.Equal(t, 2, sub.Concurrency)

	role, ok := authctx.GetUserRoleFromContext(c)
	require.True(t, ok)
	require.Equal(t, "admin", role)
}
