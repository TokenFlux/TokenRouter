package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// cnAccountProtocolCases 明确列出对外承诺的组合，不从待测校验函数推导期望值。
var cnAccountProtocolCases = []struct {
	platform  string
	mode      string
	protocols []string
}{
	{PlatformDeepseek, AccountModePayG, []string{APIProtocolAdaptive, APIProtocolChatCompletions, APIProtocolAnthropic, APIProtocolResponses}},
	{PlatformKimi, AccountModePayG, []string{APIProtocolAdaptive, APIProtocolChatCompletions, APIProtocolAnthropic, APIProtocolResponses}},
	{PlatformKimi, AccountModeCoding, []string{APIProtocolAdaptive, APIProtocolChatCompletions, APIProtocolAnthropic, APIProtocolResponses}},
	{PlatformZhipu, AccountModePayG, []string{APIProtocolAdaptive, APIProtocolChatCompletions, APIProtocolAnthropic}},
	{PlatformZhipu, AccountModeCoding, []string{APIProtocolAdaptive, APIProtocolChatCompletions, APIProtocolAnthropic}},
}

// cnAccountTestCredentials 模拟前端提交的自定义端点，验证保存时不会改成官方地址。
func cnAccountTestCredentials(platform, mode, protocol string) map[string]any {
	credentials := map[string]any{
		"api_key":      "sk-test",
		"account_mode": mode,
		"api_protocol": protocol,
		"base_url":     "https://relay.example.test/v1",
	}
	if protocol == APIProtocolAdaptive {
		urls := map[string]any{
			APIProtocolChatCompletions: "https://relay.example.test/v1",
			APIProtocolAnthropic:       "https://relay.example.test/anthropic",
		}
		if platform != PlatformZhipu {
			urls[APIProtocolResponses] = "https://relay.example.test/responses"
		}
		credentials["api_base_urls"] = urls
	}
	return credentials
}

// TestCNProviderAccountProtocolPersistence 覆盖真实创建、编辑及批量凭据更新入口。
func TestCNProviderAccountProtocolPersistence(t *testing.T) {
	t.Parallel()
	for _, tc := range cnAccountProtocolCases {
		for _, protocol := range tc.protocols {
			t.Run(tc.platform+"/"+tc.mode+"/"+protocol, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				repo := &accountServiceTestRepo{}
				svc := &adminServiceImpl{accountRepo: repo}
				credentials := cnAccountTestCredentials(tc.platform, tc.mode, protocol)
				wantCredentials := cnAccountTestCredentials(tc.platform, tc.mode, protocol)
				delete(wantCredentials, "api_protocol")
				wantProtocols := []GroupClientProtocol{ProtocolOpenAIChatCompletions}
				switch protocol {
				case APIProtocolAnthropic:
					wantProtocols = []GroupClientProtocol{ProtocolAnthropicMessages}
				case APIProtocolResponses:
					wantProtocols = []GroupClientProtocol{ProtocolOpenAIResponses}
				case APIProtocolAdaptive:
					wantProtocols = []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIResponses, ProtocolOpenAIChatCompletions}
					if tc.platform == PlatformZhipu {
						wantProtocols = []GroupClientProtocol{ProtocolAnthropicMessages, ProtocolOpenAIChatCompletions}
					}
				}
				wantCredentials[upstreamProtocolsKey] = wantProtocols
				if protocol != APIProtocolAdaptive {
					wantCredentials["api_base_urls"] = map[string]any{protocol: "https://relay.example.test/v1"}
				}

				created, err := svc.CreateAccount(ctx, &CreateAccountInput{
					Name: "国产平台账号", Platform: tc.platform, Type: AccountTypeAPIKey,
					Credentials: credentials, SkipDefaultGroupBind: true,
				})
				require.NoError(t, err)
				require.Equal(t, wantProtocols, created.UpstreamProtocols())
				require.Equal(t, wantCredentials, repo.accounts[created.ID].Credentials)

				// 普通字段编辑也会重新校验账号，必须允许已有自适应账号正常保存。
				updated, err := svc.UpdateAccount(ctx, created.ID, &UpdateAccountInput{Name: "已编辑"})
				require.NoError(t, err)
				require.Equal(t, "已编辑", repo.accounts[created.ID].Name)
				require.Equal(t, wantCredentials, updated.Credentials)

				// 从传统 Chat 账号切换到目标协议，覆盖编辑与批量更新的凭据合并。
				repo.accounts[created.ID].Credentials = cnAccountTestCredentials(tc.platform, tc.mode, APIProtocolChatCompletions)
				updated, err = svc.UpdateAccount(ctx, created.ID, &UpdateAccountInput{Credentials: credentials})
				require.NoError(t, err)
				require.Equal(t, wantCredentials, updated.Credentials)
				repo.accounts[created.ID].Credentials = cnAccountTestCredentials(tc.platform, tc.mode, APIProtocolChatCompletions)
				result, err := svc.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{
					AccountIDs: []int64{created.ID}, Credentials: credentials,
				})
				require.NoError(t, err)
				require.Equal(t, 1, result.Success)
				require.Len(t, repo.bulkUpdates, 1)
				require.Equal(t, wantProtocols, repo.bulkUpdates[0].ProtocolUpdates[created.ID][upstreamProtocolsKey])
			})
		}
	}
}

// TestCNProviderCredentialValidationRejectsInvalid 保持非法组合的结构化错误边界。
func TestCNProviderCredentialValidationRejectsInvalid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, platform, accountType, mode, protocol, reason string
	}{
		{"智谱原生 Responses", PlatformZhipu, AccountTypeAPIKey, AccountModePayG, APIProtocolResponses, "CN_PROVIDER_PROTOCOL_INVALID"},
		{"智谱 Coding 原生 Responses", PlatformZhipu, AccountTypeAPIKey, AccountModeCoding, APIProtocolResponses, "CN_PROVIDER_PROTOCOL_INVALID"},
		{"DeepSeek Coding", PlatformDeepseek, AccountTypeAPIKey, AccountModeCoding, APIProtocolAdaptive, "CN_PROVIDER_MODE_INVALID"},
		{"非 API Key", PlatformDeepseek, AccountTypeOAuth, AccountModePayG, APIProtocolAdaptive, "CN_PROVIDER_ACCOUNT_TYPE_INVALID"},
		{"未知模式", PlatformKimi, AccountTypeAPIKey, "unknown", APIProtocolAdaptive, "CN_PROVIDER_ACCOUNT_MODE_INVALID"},
		{"未知协议", PlatformKimi, AccountTypeAPIKey, AccountModePayG, "unknown", "CN_PROVIDER_PROTOCOL_INVALID"},
	}
	for _, tc := range cases {
		for _, create := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/create=%t", tc.name, create), func(t *testing.T) {
				account := &Account{
					Platform: tc.platform, Type: tc.accountType,
					Credentials: cnAccountTestCredentials(tc.platform, tc.mode, tc.protocol),
				}
				err := normalizeCNProviderCredentials(account, create)
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
				require.Equal(t, tc.reason, infraerrors.Reason(err))
			})
		}
	}
}

// TestCNProviderBulkProtocolValidationBeforeWrite 混合平台批量修改须在任何写入前拒绝非法组合。
func TestCNProviderBulkProtocolValidationBeforeWrite(t *testing.T) {
	t.Parallel()
	repo := &accountServiceTestRepo{accounts: map[int64]*Account{
		1: {ID: 1, Platform: PlatformKimi, Type: AccountTypeAPIKey, Credentials: cnAccountTestCredentials(PlatformKimi, AccountModePayG, APIProtocolAdaptive)},
		2: {ID: 2, Platform: PlatformZhipu, Type: AccountTypeAPIKey, Credentials: cnAccountTestCredentials(PlatformZhipu, AccountModePayG, APIProtocolAdaptive)},
	}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2}, Credentials: map[string]any{"api_protocol": APIProtocolResponses},
	})
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "CN_PROVIDER_PROTOCOL_INVALID", infraerrors.Reason(err))
	require.Empty(t, repo.bulkUpdates)
	for _, account := range repo.accounts {
		require.Equal(t, APIProtocolAdaptive, account.Credentials["api_protocol"])
	}
}

// TestCNProviderLegacyCredentialDefaults 保留历史读取默认值，普通编辑不补写缺失字段。
func TestCNProviderLegacyCredentialDefaults(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{PlatformDeepseek, PlatformKimi, PlatformZhipu} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			repo := &accountServiceTestRepo{accounts: map[int64]*Account{
				1: {ID: 1, Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "sk-test"}},
			}}
			svc := &adminServiceImpl{accountRepo: repo}
			updated, err := svc.UpdateAccount(ctx, 1, &UpdateAccountInput{Name: "历史账号"})
			require.NoError(t, err)
			require.Equal(t, AccountModePayG, updated.GetAccountMode())
			require.Equal(t, []GroupClientProtocol{ProtocolOpenAIChatCompletions}, updated.UpstreamProtocols())
			require.NotContains(t, updated.Credentials, "account_mode")
			require.NotContains(t, updated.Credentials, "api_protocol")
			created, err := svc.CreateAccount(ctx, &CreateAccountInput{
				Name: "缺省账号", Platform: platform, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "sk-test"}, SkipDefaultGroupBind: true,
			})
			require.NoError(t, err)
			require.Equal(t, AccountModePayG, created.Credentials["account_mode"])
			require.Equal(t, []GroupClientProtocol{ProtocolOpenAIChatCompletions}, created.UpstreamProtocols())
		})
	}
}
