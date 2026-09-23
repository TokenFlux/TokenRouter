package dto_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAccountFromServiceShallow_RedactsSensitiveCredentials(t *testing.T) {
	src := &account.Record{Now: time.Now, LoadLocation: time.LoadLocation,
		ID:       42,
		Name:     "demo",
		Platform: "anthropic",
		Type:     "oauth",
		Credentials: map[string]any{
			"access_token":  "at-secret",
			"refresh_token": "rt-secret",
			"id_token":      "id-secret",
			"api_key":       "sk-secret",
			"base_url":      "https://api.example.com",
			"model_mapping": map[string]any{"foo": "bar"},
		},
	}

	got := dto.AccountFromRecordShallow(src)
	require.NotNil(t, got)

	// 敏感键不在 Credentials 里
	require.NotContains(t, got.Credentials, "access_token")
	require.NotContains(t, got.Credentials, "refresh_token")
	require.NotContains(t, got.Credentials, "id_token")
	require.NotContains(t, got.Credentials, "api_key")
	// 非敏感键保留
	require.Equal(t, "https://api.example.com", got.Credentials["base_url"])
	require.Equal(t, map[string]any{"foo": "bar"}, got.Credentials["model_mapping"])

	// 状态 map 标记敏感键存在
	require.True(t, got.CredentialsStatus["has_access_token"])
	require.True(t, got.CredentialsStatus["has_refresh_token"])
	require.True(t, got.CredentialsStatus["has_id_token"])
	require.True(t, got.CredentialsStatus["has_api_key"])

	// JSON 序列化校验：响应体里不会出现敏感子串
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "rt-secret")
	require.NotContains(t, string(raw), "at-secret")
	require.NotContains(t, string(raw), "sk-secret")
	require.NotContains(t, string(raw), "id-secret")
	// 状态标识应序列化进 JSON
	require.Contains(t, string(raw), "credentials_status")
	require.Contains(t, string(raw), "has_refresh_token")

	// 原始 account.Record 不应被改动
	require.Equal(t, "rt-secret", src.Credentials["refresh_token"])
}

func TestAccountFromServiceShallow_RedactsOllamaCloudManagedExtra(t *testing.T) {
	snapshot := map[string]any{
		"status":          account.OllamaCloudUsageStatusOK,
		"last_attempt_at": "2026-07-22T12:00:00Z",
		"next_refresh_at": "2026-07-22T13:00:00Z",
		"data":            map[string]any{"plan": "Pro"},
	}
	src := &account.Record{Now: time.Now, LoadLocation: time.LoadLocation,
		ID: 9, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://ollama.com", "api_key": "secret-key",
			account.NewAPIUserAccessTokenCredentialKey: "wallet-token-secret",
		},
		Extra: map[string]any{
			account.OllamaCloudUsageSessionExtraKey:     "ciphertext-secret",
			account.OllamaCloudUsageAutoRefreshExtraKey: true,
			account.OllamaCloudUsageSnapshotExtraKey:    snapshot,
			"ordinary":                                  "kept",
		},
	}

	got := dto.AccountFromRecordShallow(src)
	require.NotContains(t, got.Extra, account.OllamaCloudUsageSessionExtraKey)
	require.NotContains(t, got.Extra, account.OllamaCloudUsageAutoRefreshExtraKey)
	require.NotContains(t, got.Extra, account.OllamaCloudUsageSnapshotExtraKey)
	require.Equal(t, "kept", got.Extra["ordinary"])
	require.NotNil(t, got.OllamaCloudUsage)
	require.True(t, got.OllamaCloudUsage.Configured)
	require.True(t, got.OllamaCloudUsage.AutoRefreshEnabled)
	require.Equal(t, "Pro", got.OllamaCloudUsage.Snapshot.Data.Plan)

	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "ciphertext-secret")
	require.NotContains(t, string(raw), "secret-key")
	require.NotContains(t, string(raw), "wallet-token-secret")
	require.Contains(t, src.Extra, account.OllamaCloudUsageSessionExtraKey)
}

func TestAccountFromServiceShallow_RedactsLegacyUpstreamUsageSecrets(t *testing.T) {
	src := &account.Record{Now: time.Now, LoadLocation: time.LoadLocation,
		ID: 10, Type: capability.AccountTypeAPIKey,
		Extra: map[string]any{
			account.UpstreamUsageQueryExtraKey: map[string]any{
				"enabled": true, "adapter": "legacy-secret", "base_url": "https://user:legacy-secret@gateway.example?token=legacy-secret",
				"api_key": "legacy-secret", "headers": map[string]any{"Authorization": "Bearer legacy-secret"},
			},
		},
	}
	got := dto.AccountFromRecordShallow(src)
	require.Equal(t, map[string]any{
		"enabled": true,
	}, got.Extra[account.UpstreamUsageQueryExtraKey])
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "legacy-secret")
	// 映射层不得修改数据库对象中的历史值。
	legacyConfig, ok := src.Extra[account.UpstreamUsageQueryExtraKey].(map[string]any)
	require.True(t, ok)
	require.Contains(t, legacyConfig, "api_key")
}

func TestAccountFromServiceShallow_PreservesZivvAdapterSelection(t *testing.T) {
	src := &account.Record{Now: time.Now, LoadLocation: time.LoadLocation,
		ID: 11, Type: capability.AccountTypeAPIKey,
		Extra: map[string]any{account.UpstreamUsageQueryExtraKey: map[string]any{
			"enabled": true, "adapter": account.UpstreamUsageAdapterZivv,
		}},
	}
	got := dto.AccountFromRecordShallow(src)
	require.Equal(t, map[string]any{
		"enabled": true, "adapter": account.UpstreamUsageAdapterZivv,
	}, got.Extra[account.UpstreamUsageQueryExtraKey])
}

func TestAccountFromServiceShallow_NilCredentialsOmitsStatus(t *testing.T) {
	src := &account.Record{Now: time.Now, LoadLocation: time.LoadLocation, ID: 1, Name: "n", Platform: "anthropic", Type: "oauth"}
	got := dto.AccountFromRecordShallow(src)
	require.NotNil(t, got)
	require.Nil(t, got.Credentials)
	require.Nil(t, got.CredentialsStatus)
}

func TestAccountFromServiceShallow_OpenAIOAuthTLSFingerprint(t *testing.T) {
	src := &account.Record{Now: time.Now, LoadLocation: time.LoadLocation,
		ID:       3,
		Name:     "openai-oauth",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": -1,
		},
	}

	got := dto.AccountFromRecordShallow(src)
	require.NotNil(t, got)
	require.NotNil(t, got.EnableTLSFingerprint)
	require.True(t, *got.EnableTLSFingerprint)
	require.NotNil(t, got.TLSFingerprintProfileID)
	require.Equal(t, int64(-1), *got.TLSFingerprintProfileID)
}
