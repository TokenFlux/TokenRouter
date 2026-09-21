//go:build unit

package upstream

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCNProviderResponseIndicatesInsufficientBalance 覆盖中英文余额不足文案与否定用例。
func TestCNProviderResponseIndicatesInsufficientBalance(t *testing.T) {
	t.Parallel()
	positive := []string{
		`{"error":{"message":"余额不足"}}`,
		`{"error":{"message":"Insufficient balance"}}`,
		`{"code":"insufficient_credit"}`,
		`"balance is not enough"`,
		`"no enough balance"`,
	}
	for _, body := range positive {
		require.True(t, CNResponseIndicatesInsufficientBalance([]byte(body)), body)
	}
	negative := []string{
		`{"error":{"message":"rate limit exceeded"}}`,
		`{"error":{"message":"quota exhausted"}}`,
		``,
	}
	for _, body := range negative {
		require.False(t, CNResponseIndicatesInsufficientBalance([]byte(body)), body)
	}
}
