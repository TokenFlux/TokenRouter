package dto_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 展示 DTO 的嵌套修改不能回写账号配置或缓存中的 map。
func TestS06AccountDTOHasIndependentNestedValues(t *testing.T) {
	value := &accountcore.Record{Now: time.Now, LoadLocation: time.LoadLocation, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "model"}}, Extra: map[string]any{"policy": map[string]any{"enabled": true}}}
	view := dto.AccountFromRecordShallow(value)
	mapping, ok := view.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	policy, ok := view.Extra["policy"].(map[string]any)
	require.True(t, ok)
	mapping["alias"] = "changed"
	policy["enabled"] = false
	require.Equal(t, map[string]any{"model_mapping": map[string]any{"alias": "model"}}, value.Credentials)
	require.Equal(t, map[string]any{"policy": map[string]any{"enabled": true}}, value.Extra)
}
