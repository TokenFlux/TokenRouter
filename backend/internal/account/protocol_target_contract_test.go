//go:build unit

package account_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestGetAPIProtocol 验证协议凭证维度的平台校验矩阵：
// responses 仅 deepseek / kimi；缺失/非法值回退 chat_completions（与旧行为一致）。
func TestGetAPIProtocol(t *testing.T) {
	t.Parallel()

	mk := func(platform, protocol string) accountcore.ProtocolTarget {
		creds := map[string]any{"api_key": "sk-test"}
		if protocol != "" {
			creds["api_protocol"] = protocol
		}
		return accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: platform, Type: capability.AccountTypeAPIKey, Credentials: creds}}
	}

	require.Equal(t, accountcore.APIProtocolChatCompletions, mk(capability.PlatformKimi, "").GetAPIProtocol(), "缺失回退默认")
	require.Equal(t, accountcore.APIProtocolAnthropic, mk(capability.PlatformZhipu, accountcore.APIProtocolAnthropic).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolAnthropic, mk(capability.PlatformKimi, accountcore.APIProtocolAnthropic).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolAnthropic, mk(capability.PlatformDeepseek, accountcore.APIProtocolAnthropic).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolResponses, mk(capability.PlatformDeepseek, accountcore.APIProtocolResponses).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolResponses, mk(capability.PlatformKimi, accountcore.APIProtocolResponses).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolAdaptive, mk(capability.PlatformKimi, accountcore.APIProtocolAdaptive).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolAdaptive, mk(capability.PlatformZhipu, accountcore.APIProtocolAdaptive).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolAdaptive, mk(capability.PlatformDeepseek, accountcore.APIProtocolAdaptive).GetAPIProtocol())
	require.Equal(t, accountcore.APIProtocolChatCompletions, mk(capability.PlatformZhipu, accountcore.APIProtocolResponses).GetAPIProtocol(), "zhipu 无 responses 端点")
	require.Equal(t, accountcore.APIProtocolChatCompletions, mk(capability.PlatformKimi, "bogus").GetAPIProtocol(), "非法值回退默认")
	require.Equal(t, accountcore.APIProtocolChatCompletions, (accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}).GetAPIProtocol(), "非 CN 供应商恒为默认")
}

func TestGetCodingPlanProviderUsesPlatformInsteadOfURL(t *testing.T) {
	t.Parallel()
	kimi := accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"account_mode": accountcore.AccountModeCoding,
			"base_url":     "https://relay.example/custom",
		},
	}}
	require.Equal(t, capability.PlatformKimi, kimi.GetCodingPlanProvider())
	zhipu := accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"account_mode": accountcore.AccountModeCoding,
			"base_url":     "https://api.kimi.com/coding/v1",
		},
	}}
	require.Equal(t, capability.PlatformZhipu, zhipu.GetCodingPlanProvider(), "中继路径不得改变平台身份")
}

func TestSupportsNativeCNResponses(t *testing.T) {
	t.Parallel()
	require.True(t, (accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: capability.PlatformDeepseek}}).SupportsNativeCNResponses())
	require.True(t, (accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: capability.PlatformKimi}}).SupportsNativeCNResponses())
	require.True(t, (accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: capability.PlatformKimi, Credentials: map[string]any{"account_mode": accountcore.AccountModeCoding}}}).SupportsNativeCNResponses())
	require.False(t, (accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: capability.PlatformZhipu}}).SupportsNativeCNResponses())
	require.False(t, (accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: capability.PlatformOpenAI}}).SupportsNativeCNResponses())
}

func TestAdaptiveProtocolBaseURLs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		platform      string
		mode          string
		wantChat      string
		wantAnthropic string
		wantResponses string
	}{
		{"kimi payg", capability.PlatformKimi, accountcore.AccountModePayG, accountcore.DefaultKimiPayGBaseURL, accountcore.DefaultKimiPayGAnthropicBaseURL, accountcore.DefaultKimiPayGBaseURL},
		{"kimi coding", capability.PlatformKimi, accountcore.AccountModeCoding, accountcore.DefaultKimiCodingBaseURL, accountcore.DefaultKimiCodingAnthropicBaseURL, accountcore.DefaultKimiCodingBaseURL},
		{"zhipu payg", capability.PlatformZhipu, accountcore.AccountModePayG, accountcore.DefaultZhipuPayGBaseURL, accountcore.DefaultZhipuAnthropicBaseURL, accountcore.DefaultZhipuPayGBaseURL},
		{"zhipu coding", capability.PlatformZhipu, accountcore.AccountModeCoding, accountcore.DefaultZhipuCodingBaseURL, accountcore.DefaultZhipuAnthropicBaseURL, accountcore.DefaultZhipuCodingBaseURL},
		{"deepseek", capability.PlatformDeepseek, accountcore.AccountModePayG, accountcore.DefaultDeepseekBaseURL, accountcore.DefaultDeepseekAnthropicBaseURL, accountcore.DefaultDeepseekBaseURL},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: tc.platform, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{
				"api_protocol": accountcore.APIProtocolAdaptive,
				"account_mode": tc.mode,
			}}}
			require.Equal(t, tc.wantChat, account.GetCNProtocolBaseURL(accountcore.APIProtocolChatCompletions))
			require.Equal(t, tc.wantAnthropic, account.GetCNProtocolBaseURL(accountcore.APIProtocolAnthropic))
			require.Equal(t, tc.wantResponses, account.GetCNProtocolBaseURL(accountcore.APIProtocolResponses))
			require.Equal(t, tc.wantAnthropic, account.GetAnthropicProtocolBaseURL())
		})
	}
}

func TestAdaptiveProtocolBaseURLOverrides(t *testing.T) {
	t.Parallel()

	account := accountcore.ProtocolTarget{Record: &accountcore.Record{Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{
		"api_protocol": accountcore.APIProtocolAdaptive,
		"base_url":     "https://legacy-chat.example.com",
		"api_base_urls": map[string]any{
			accountcore.APIProtocolChatCompletions: "https://chat.example.com",
			accountcore.APIProtocolAnthropic:       "https://anthropic.example.com",
			accountcore.APIProtocolResponses:       "https://responses.example.com",
		},
	}}}

	require.Equal(t, "https://chat.example.com", account.GetOpenAIBaseURL())
	require.Equal(t, "https://chat.example.com", account.GetCNProtocolBaseURL(accountcore.APIProtocolChatCompletions))
	require.Equal(t, "https://anthropic.example.com", account.GetAnthropicProtocolBaseURL())
	require.Equal(t, "https://responses.example.com", account.GetCNProtocolBaseURL(accountcore.APIProtocolResponses))
}

// TestAnthropicProtocolBaseURL 验证 Anthropic 协议默认端点与协议感知的
// OpenAI 格式 base 回退。
func TestAnthropicProtocolBaseURL(t *testing.T) {
	t.Parallel()

	// 默认端点（按供应商 × 模式）
	require.Equal(t, "https://api.moonshot.cn/anthropic", (accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolAnthropic},
	}}).GetAnthropicProtocolBaseURL())
	require.Equal(t, "https://api.kimi.com/coding", (accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolAnthropic, "account_mode": accountcore.AccountModeCoding},
	}}).GetAnthropicProtocolBaseURL())
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic", (accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolAnthropic},
	}}).GetAnthropicProtocolBaseURL())
	require.Equal(t, "https://api.deepseek.com/anthropic", (accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolAnthropic},
	}}).GetAnthropicProtocolBaseURL())

	// 凭证 base_url 覆盖默认值
	require.Equal(t, "https://custom.example.com/anthropic", (accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": accountcore.APIProtocolAnthropic, "base_url": "https://custom.example.com/anthropic"},
	}}).GetAnthropicProtocolBaseURL())

	// 非 Anthropic 协议返回空串
	require.Empty(t, (accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://open.bigmodel.cn/api/paas/v4"},
	}}).GetAnthropicProtocolBaseURL())
}

// TestGetOpenAIFormatBaseURL_ProtocolAware 验证 Anthropic 协议账号的 OpenAI
// 格式路径：官方端点映射到默认 CC base，自定义中继保留 host 与路径前缀。
func TestGetOpenAIFormatBaseURL_ProtocolAware(t *testing.T) {
	t.Parallel()

	zhipuAnthropic := accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": accountcore.APIProtocolAnthropic,
			"base_url":     "https://open.bigmodel.cn/api/anthropic",
		},
	}}
	require.Equal(t, "https://open.bigmodel.cn/api/paas/v4", zhipuAnthropic.GetOpenAIFormatBaseURL())

	kimiCodingAnthropic := accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": accountcore.APIProtocolAnthropic,
			"account_mode": accountcore.AccountModeCoding,
			"base_url":     "https://api.kimi.com/coding",
		},
	}}
	require.Equal(t, "https://api.kimi.com/coding/v1", kimiCodingAnthropic.GetOpenAIFormatBaseURL())

	deepseekRelay := accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": accountcore.APIProtocolAnthropic,
			"base_url":     "https://relay.example.com/proxy/deepseek/anthropic/",
		},
	}}
	require.Equal(t, "https://relay.example.com/proxy/deepseek", deepseekRelay.GetOpenAIFormatBaseURL())

	kimiRelay := accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_protocol": accountcore.APIProtocolAnthropic,
			"base_url":     "https://relay.example.com/moonshot/anthropic",
		},
	}}
	require.Equal(t, "https://relay.example.com/moonshot", kimiRelay.GetOpenAIFormatBaseURL())

	// chat_completions 协议下行为不变（凭证 base_url 原样返回）
	ccAccount := accountcore.ProtocolTarget{Record: &accountcore.Record{
		Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ds-relay.example.com"},
	}}
	require.Equal(t, "https://ds-relay.example.com", ccAccount.GetOpenAIFormatBaseURL())
}
