package account

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account/transfer"
	"github.com/stretchr/testify/require"
)

// JSON 省略和显式 null/false/空集合继续区别，同时默认模板不能被某个导入条目污染。
func TestArchiveDefaultsPreservePresenceAndIsolation(t *testing.T) {
	concurrency, priority := 4, 3
	defaults := &transfer.OpenAIOAuthImportDefaults{Account: transfer.OpenAIOAuthImportAccountDefaults{Concurrency: &concurrency, Priority: &priority}, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "model"}}, Extra: map[string]any{"enabled": true, "list": []any{"default"}, "nullable": "default"}}
	var item transfer.DataAccount
	require.NoError(t, json.Unmarshal([]byte(`{"name":"fixture","platform":"openai","type":"oauth","credentials":{"id_token":"token"},"concurrency":null,"extra":{"enabled":false,"list":[],"nullable":null}}`), &item))
	ApplyArchiveDefaults(&item, defaults)
	require.Nil(t, item.Concurrency)
	require.True(t, item.ConcurrencySet)
	require.Equal(t, 3, *item.Priority)
	require.Equal(t, false, item.Extra["enabled"])
	require.Equal(t, []any{}, item.Extra["list"])
	require.Contains(t, item.Extra, "nullable")
	require.Nil(t, item.Extra["nullable"])
	mapping, ok := item.Credentials["model_mapping"].(map[string]any)
	require.True(t, ok)
	mapping["alias"] = "changed"
	*item.Priority = 91
	require.Equal(t, map[string]any{"alias": "model"}, defaults.Credentials["model_mapping"])
	require.Equal(t, 3, priority)
	data, err := json.Marshal(item)
	require.NoError(t, err)
	require.NotContains(t, string(data), "ConcurrencySet")
	require.NotContains(t, string(data), "PrioritySet")
}

// ID Token 只是导入提示，保留明确的平台/type 边界与“已有非空值优先”。
func TestArchiveIdentityHintsDoNotReplaceExplicitValues(t *testing.T) {
	item := transfer.DataAccount{Platform: " OpenAI ", Type: " OAUTH ", Credentials: map[string]any{"id_token": " fixture-token ", "email": "admin@example.test", "plan_type": nil}}
	require.Equal(t, " fixture-token ", ArchiveIDToken(&item))
	FillArchiveIdentity(&item, &ArchiveIdentityHints{Email: "token@example.test", PlanType: "pro", ChatGPTAccountID: "workspace"})
	require.Equal(t, "admin@example.test", item.Credentials["email"])
	require.Equal(t, "pro", item.Credentials["plan_type"])
	require.Equal(t, "workspace", item.Credentials["chatgpt_account_id"])
	item.Type = AccountTypeAPIKey
	require.Empty(t, ArchiveIDToken(&item))
	require.Empty(t, ArchiveIDToken(nil))
}
