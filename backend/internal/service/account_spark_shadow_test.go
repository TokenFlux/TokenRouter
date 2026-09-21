package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestAccountSparkShadowHelpers(t *testing.T) {
	pid := int64(100)
	normal := &Account{ID: 100}
	require.False(t, normal.IsShadow())
	require.False(t, normal.IsCredentialShadow())
	require.Equal(t, account.QuotaDimensionGlobal, normal.QuotaDimensionOrDefault())
	shadow := &Account{ID: 200, ParentAccountID: &pid, QuotaDimension: account.QuotaDimensionSpark}
	require.True(t, shadow.IsShadow())
	require.True(t, shadow.IsCredentialShadow())
	require.Equal(t, account.QuotaDimensionSpark, shadow.QuotaDimensionOrDefault())
}
