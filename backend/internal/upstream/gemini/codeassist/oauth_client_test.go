package codeassist

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCreateOAuthHTTPClient_TimeoutAndReuse constructs two OAuth HTTP clients
// configured with the same HTTP proxy URL. Both calls must return the same
// pooled client instance with a sixty-second timeout.
func TestCreateOAuthHTTPClient_TimeoutAndReuse(t *testing.T) {
	first, err := CreateOAuthHTTPClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Equal(t, 60*time.Second, first.Timeout)

	second, err := CreateOAuthHTTPClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Same(t, first, second)
}
