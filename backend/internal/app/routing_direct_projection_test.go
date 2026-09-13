package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
)

// 分组直读只获得模型投影；平台目录仍在每次实际读取时取得，返回值不污染目录。
func TestRoutingGroupProjectionKeepsLazyModelDefaults(t *testing.T) {
	calls := 0
	catalog := map[string]string{"model-b": "model-b"}
	reader := routingGroupAccounts{Defaults: account.ModelMappingDefaults{Antigravity: func() map[string]string { calls++; return catalog }}}
	source := &account.Record{ID: 8, Platform: account.PlatformAntigravity, Type: account.AccountTypeOAuth}
	first := reader.project(source)
	require.Equal(t, []string{"model-b"}, first.Models)
	require.Equal(t, 1, calls)
	first.Models[0] = "caller changed"
	second := reader.project(source)
	require.Equal(t, []string{"model-b"}, second.Models)
	require.Equal(t, 2, calls)
	catalog = map[string]string{"model-a": "model-a"}
	third := reader.project(source)
	require.Equal(t, []string{"model-a"}, third.Models)
	require.Equal(t, 3, calls)
	require.Equal(t, source.ID, third.ID)
	require.Equal(t, source.Type, third.Type)
}
