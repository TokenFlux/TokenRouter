//go:build unit

package account_test

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsInvalidGrantError(t *testing.T) {
	require.True(t, accountcore.IsInvalidGrantError(errors.New("invalid_grant: token revoked")))
	require.True(t, accountcore.IsInvalidGrantError(errors.New("INVALID_GRANT")))
	require.False(t, accountcore.IsInvalidGrantError(errors.New("invalid_client")))
	require.False(t, accountcore.IsInvalidGrantError(nil))
}
