package httpapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeFrontendRedirectPath(t *testing.T) {
	require.Equal(t, "/dashboard", SanitizeFrontendRedirectPath("/dashboard"))
	require.Equal(t, "/dashboard", SanitizeFrontendRedirectPath(" /dashboard "))
	require.Equal(t, "", SanitizeFrontendRedirectPath("dashboard"))
	require.Equal(t, "", SanitizeFrontendRedirectPath("//evil.com"))
	require.Equal(t, "", SanitizeFrontendRedirectPath("https://evil.com"))
	require.Equal(t, "", SanitizeFrontendRedirectPath("/\nfoo"))

	long := "/" + strings.Repeat("a", linuxDoOAuthMaxRedirectLen)
	require.Equal(t, "", SanitizeFrontendRedirectPath(long))
}

func TestSingleLineStripsWhitespace(t *testing.T) {
	require.Equal(t, "hello world", OAuthSingleLine("hello\r\nworld"))
	require.Equal(t, "", OAuthSingleLine("\n\t\r"))
}
