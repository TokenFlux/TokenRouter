//go:build unit

package account

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestGetOpenAIProtocolAPIKey_CNProviders 验证 OpenAI 协议族密钥读取覆盖国产供应商，
// 同时保持 IsOpenAIApiKey 的 openai-only 语义（调度倍率/WS 门控不受影响）。
func TestGetOpenAIProtocolAPIKey_CNProviders(t *testing.T) {
	t.Parallel()

	kimi := &Record{
		Platform:    capability.PlatformKimi,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-kimi"},
	}
	require.Equal(t, "sk-kimi", kimi.GetOpenAIProtocolAPIKey())
	require.False(t, kimi.IsOpenAIApiKey(), "IsOpenAIApiKey stays openai-only for scheduling gates")

	// 非 APIKey 类型的 CN 账号不返回密钥
	notAPIKey := &Record{
		Platform:    capability.PlatformDeepseek,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"api_key": "sk-leak"},
	}
	require.Equal(t, "", notAPIKey.GetOpenAIProtocolAPIKey())

	// openai 原生账号行为不变
	openai := &Record{
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-openai"},
	}
	require.Equal(t, "sk-openai", openai.GetOpenAIProtocolAPIKey())
}

func TestCNProviderAccountModeAndCredentialValidation(t *testing.T) {
	t.Parallel()
	historical := &Record{
		Platform:    capability.PlatformKimi,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test"},
	}
	require.Equal(t, AccountModePayG, historical.GetAccountMode())
	require.Equal(t, APIProtocolChatCompletions, (ProtocolTarget{Record: historical}).GetAPIProtocol())
	require.NoError(t, NormalizeCNProviderCredentials(historical, false))
	require.NotContains(t, historical.Credentials, "account_mode", "编辑历史账号不应强制回写默认字段")

	created := &Record{
		Platform:    capability.PlatformZhipu,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test"},
	}
	require.NoError(t, NormalizeCNProviderCredentials(created, true))
	require.Equal(t, AccountModePayG, created.Credentials["account_mode"])
	require.Equal(t, APIProtocolChatCompletions, created.Credentials["api_protocol"])

	invalidResponses := &Record{
		Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": APIProtocolResponses},
	}
	require.Error(t, NormalizeCNProviderCredentials(invalidResponses, false))
	invalidType := &Record{Platform: capability.PlatformDeepseek, Type: capability.AccountTypeOAuth}
	require.Error(t, NormalizeCNProviderCredentials(invalidType, false))
}
