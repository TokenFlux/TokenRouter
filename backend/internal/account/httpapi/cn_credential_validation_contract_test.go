package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

// cnAccountTestCredentials 模拟前端提交的自定义端点，验证保存时不会改成官方地址。
func cnAccountTestCredentials(platform, mode, protocol string) map[string]any {
	credentials := map[string]any{
		"api_key":      "sk-test",
		"account_mode": mode,
		"api_protocol": protocol,
		"base_url":     "https://relay.example.test/v1",
	}
	if protocol == accountcore.APIProtocolAdaptive {
		urls := map[string]any{
			accountcore.APIProtocolChatCompletions: "https://relay.example.test/v1",
			accountcore.APIProtocolAnthropic:       "https://relay.example.test/anthropic",
		}
		if platform != capability.PlatformZhipu {
			urls[accountcore.APIProtocolResponses] = "https://relay.example.test/responses"
		}
		credentials["api_base_urls"] = urls
	}
	return credentials
}

// TestCNProviderCredentialValidationRejectsInvalid 保持非法组合的结构化错误边界。
func TestCNProviderCredentialValidationRejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, platform, accountType, mode, protocol, reason string
	}{
		{"智谱原生 Responses", capability.PlatformZhipu, capability.AccountTypeAPIKey, accountcore.AccountModePayG, accountcore.APIProtocolResponses, "CN_PROVIDER_PROTOCOL_INVALID"},
		{"智谱 Coding 原生 Responses", capability.PlatformZhipu, capability.AccountTypeAPIKey, accountcore.AccountModeCoding, accountcore.APIProtocolResponses, "CN_PROVIDER_PROTOCOL_INVALID"},
		{"DeepSeek Coding", capability.PlatformDeepseek, capability.AccountTypeAPIKey, accountcore.AccountModeCoding, accountcore.APIProtocolAdaptive, "CN_PROVIDER_MODE_INVALID"},
		{"非 API Key", capability.PlatformDeepseek, capability.AccountTypeOAuth, accountcore.AccountModePayG, accountcore.APIProtocolAdaptive, "CN_PROVIDER_ACCOUNT_TYPE_INVALID"},
		{"未知模式", capability.PlatformKimi, capability.AccountTypeAPIKey, "unknown", accountcore.APIProtocolAdaptive, "CN_PROVIDER_ACCOUNT_MODE_INVALID"},
		{"未知协议", capability.PlatformKimi, capability.AccountTypeAPIKey, accountcore.AccountModePayG, "unknown", "CN_PROVIDER_PROTOCOL_INVALID"},
	}
	for _, tc := range cases {
		for _, create := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/create=%t", tc.name, create), func(t *testing.T) {
				account := &accountcore.Record{
					Platform: tc.platform, Type: tc.accountType,
					Credentials: cnAccountTestCredentials(tc.platform, tc.mode, tc.protocol),
				}
				err := accountcore.NormalizeCNProviderCredentials(account, create)
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
				require.Equal(t, tc.reason, apperror.Reason(err))
			})
		}
	}
}
