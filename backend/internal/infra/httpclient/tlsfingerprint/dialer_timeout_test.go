//go:build unit

package tlsfingerprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TLS 指纹拨号器会绕过 http.Transport 的 DialContext，因此自身必须携带相同的超时边界。
func TestTLSFingerprintNetworkDialerHasBoundedTimeout(t *testing.T) {
	dialer := newTLSFingerprintNetworkDialer()

	require.Equal(t, 10*time.Second, defaultTLSFingerprintDialTimeout)
	require.Equal(t, defaultTLSFingerprintDialTimeout, dialer.Timeout)
	require.Equal(t, defaultTLSFingerprintDialKeepAlive, dialer.KeepAlive)
	require.Equal(t, 10*time.Second, defaultTLSFingerprintHandshakeTimeout)
}
