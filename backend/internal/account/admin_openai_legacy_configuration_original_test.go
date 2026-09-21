//go:build unit

package account_test

import (
	"context"
	"encoding/json"
	"maps"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/transfer"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

type importDefaultsMemoryRepo struct {
	settings.Repository
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
	svc := accountcore.NewRuntimeSettings(repo, settings.ErrSettingNotFound)
	got, err := svc.GetOpenAIOAuthImportDefaults(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"openai_compact_mode": "force_on", accountcore.OpenAINativeCompactionV2ModeExtraKey: "force_off", "keep": float64(7)}, got.Extra)
	input := &transfer.OpenAIOAuthImportDefaults{Extra: map[string]any{"openai_compact_mode": "auto", "openai_compact_supported": false, "keep": 7}}
	original := maps.Clone(input.Extra)
	require.NoError(t, svc.SetOpenAIOAuthImportDefaults(ctx, input))
	require.Equal(t, original, input.Extra)
	var stored transfer.OpenAIOAuthImportDefaults
	require.NoError(t, json.Unmarshal([]byte(repo.value), &stored))
	require.Equal(t, map[string]any{"openai_compact_mode": "force_on", "keep": float64(7)}, stored.Extra)
	require.NoError(t, svc.SetOpenAIOAuthImportDefaults(ctx, &transfer.OpenAIOAuthImportDefaults{}))
	got, err = svc.GetOpenAIOAuthImportDefaults(ctx)
	require.NoError(t, err)
	require.Empty(t, got.Extra)
}

// 只有废弃键的单账号更新不得覆盖管理员保存的路由和两个压缩开关。
func TestUpdateAccountDeprecatedProbeOnlyPreservesConfiguration(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	account := &accountcore.Record{Name: "manual", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "test"},
		Extra:       map[string]any{"openai_text_route_mode": "force_responses", "openai_compact_mode": "force_off", accountcore.OpenAINativeCompactionV2ModeExtraKey: "force_on", "keep": true}}
	require.NoError(t, repo.Create(ctx, account))
	svc := newOriginalAccountEditor(repo)
	updated, err := svc.UpdateAccount(ctx, account.ID, &accountcore.UpdateAccountInput{Extra: map[string]any{"openai_responses_supported": false}})
	require.NoError(t, err)
	require.Contains(t, updated.UpstreamProtocols(), protocol.ProtocolOpenAIResponses)
	require.NotContains(t, updated.UpstreamProtocols(), protocol.ProtocolOpenAIChatCompletions)
	require.Equal(t, "force_off", updated.Extra["openai_compact_mode"])
	require.Equal(t, "force_on", updated.Extra[accountcore.OpenAINativeCompactionV2ModeExtraKey])
	require.Equal(t, true, updated.Extra["keep"])
	require.NotContains(t, updated.Extra, "openai_responses_supported")
}
