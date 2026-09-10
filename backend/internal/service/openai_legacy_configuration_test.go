//go:build unit

package service

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

// 所有旧探测值只用于清理，不得成为保存后的配置或清空其它配置的指令。
func TestLegacyOpenAIConfigurationInputBoundary(t *testing.T) {
	for _, value := range []any{nil, false, true, "unsupported", []any{1}, map[string]any{"invalid": true}} {
		extra := map[string]any{}
		for _, key := range deprecatedOpenAIAccountExtraKeys {
			extra[key] = value
		}
		normalized, replace := NormalizeDeprecatedAccountExtraUpdate(extra)
		require.False(t, replace)
		require.Nil(t, normalized)
		extra["keep"] = map[string]any{"enabled": false}
		extra["openai_compact_mode"] = " AUTO "
		extra[openAINativeCompactionV2ModeExtraKey] = "force_off"
		normalized, replace = NormalizeDeprecatedAccountExtraUpdate(extra)
		require.True(t, replace)
		require.Equal(t, map[string]any{"keep": map[string]any{"enabled": false}, "openai_compact_mode": "force_on", openAINativeCompactionV2ModeExtraKey: "force_off"}, normalized)
		require.Equal(t, " AUTO ", extra["openai_compact_mode"], "边界处理不得修改调用方对象")
	}
	normalized, replace := NormalizeDeprecatedAccountExtraUpdate(map[string]any{})
	require.True(t, replace, "显式空对象仍表示清空")
	require.Empty(t, normalized)
}

type importDefaultsMemoryRepo struct {
	SettingRepository
	value string
}

func (r *importDefaultsMemoryRepo) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}
func (r *importDefaultsMemoryRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

// 读取、保存旧模板时都清理旧字段，缺省配置保持缺省，显式关闭不变。
func TestOpenAIImportDefaultsNormalizeLegacyConfiguration(t *testing.T) {
	ctx := context.Background()
	repo := &importDefaultsMemoryRepo{value: `{"extra":{"openai_compact_mode":"auto","openai_native_compaction_v2_mode":"force_off","openai_responses_probe_status":{},"keep":7}}`}
	svc := &SettingService{settingRepo: repo}
	got, err := svc.GetOpenAIOAuthImportDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"openai_compact_mode": "force_on", openAINativeCompactionV2ModeExtraKey: "force_off", "keep": float64(7)}, got.Extra)
	input := &OpenAIOAuthImportDefaults{Extra: map[string]any{"openai_compact_mode": "auto", "openai_compact_supported": false, "keep": 7}}
	original := maps.Clone(input.Extra)
	require.NoError(t, svc.SetOpenAIOAuthImportDefaults(ctx, input))
	require.Equal(t, original, input.Extra)
	var stored OpenAIOAuthImportDefaults
	require.NoError(t, json.Unmarshal([]byte(repo.value), &stored))
	require.Equal(t, map[string]any{"openai_compact_mode": "force_on", "keep": float64(7)}, stored.Extra)
	require.NoError(t, svc.SetOpenAIOAuthImportDefaults(ctx, &OpenAIOAuthImportDefaults{}))
	got, err = svc.GetOpenAIOAuthImportDefaults(ctx)
	require.NoError(t, err)
	require.Empty(t, got.Extra)
}

// 只有废弃键的单账号更新不得覆盖管理员保存的路由和两个压缩开关。
func TestUpdateAccountDeprecatedProbeOnlyPreservesConfiguration(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	account := &Account{Name: "manual", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test"},
		Extra:       map[string]any{"openai_text_route_mode": "force_responses", "openai_compact_mode": "force_off", openAINativeCompactionV2ModeExtraKey: "force_on", "keep": true}}
	require.NoError(t, repo.Create(ctx, account))
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{Extra: map[string]any{"openai_responses_supported": false}})
	require.NoError(t, err)
	require.Contains(t, updated.UpstreamProtocols(), ProtocolOpenAIResponses)
	require.NotContains(t, updated.UpstreamProtocols(), ProtocolOpenAIChatCompletions)
	require.Equal(t, "force_off", updated.Extra["openai_compact_mode"])
	require.Equal(t, "force_on", updated.Extra[openAINativeCompactionV2ModeExtraKey])
	require.Equal(t, true, updated.Extra["keep"])
	require.NotContains(t, updated.Extra, "openai_responses_supported")
}
