package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestAccountSparkShadowHelpers(t *testing.T) {
	pid := int64(100)
	normal := &accountcore.Record{ID: 100}
	require.False(t, normal.IsShadow())
	require.False(t, normal.IsCredentialShadow())
	require.Equal(t, accountcore.QuotaDimensionGlobal, normal.QuotaDimensionOrDefault())
	shadow := &accountcore.Record{ID: 200, ParentAccountID: &pid, QuotaDimension: accountcore.QuotaDimensionSpark}
	require.True(t, shadow.IsShadow())
	require.True(t, shadow.IsCredentialShadow())
	require.Equal(t, accountcore.QuotaDimensionSpark, shadow.QuotaDimensionOrDefault())
}
