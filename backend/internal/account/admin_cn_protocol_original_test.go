package account_test

import (
	"context"
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

// cnAccountProtocolCases 明确列出对外承诺的组合，不从待测校验函数推导期望值。
var cnAccountProtocolCases = []struct {
	platform  string
	mode      string
	protocols []string
}{
	{capability.PlatformDeepseek, accountcore.AccountModePayG, []string{accountcore.APIProtocolAdaptive, accountcore.APIProtocolChatCompletions, accountcore.APIProtocolAnthropic, accountcore.APIProtocolResponses}},
	{capability.PlatformKimi, accountcore.AccountModePayG, []string{accountcore.APIProtocolAdaptive, accountcore.APIProtocolChatCompletions, accountcore.APIProtocolAnthropic, accountcore.APIProtocolResponses}},
	{capability.PlatformKimi, accountcore.AccountModeCoding, []string{accountcore.APIProtocolAdaptive, accountcore.APIProtocolChatCompletions, accountcore.APIProtocolAnthropic, accountcore.APIProtocolResponses}},
	{capability.PlatformZhipu, accountcore.AccountModePayG, []string{accountcore.APIProtocolAdaptive, accountcore.APIProtocolChatCompletions, accountcore.APIProtocolAnthropic}},
	{capability.PlatformZhipu, accountcore.AccountModeCoding, []string{accountcore.APIProtocolAdaptive, accountcore.APIProtocolChatCompletions, accountcore.APIProtocolAnthropic}},
}

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

// TestCNProviderAccountProtocolPersistence 覆盖真实创建、编辑及批量凭据更新入口。
func TestCNProviderAccountProtocolPersistence(t *testing.T) {
	t.Parallel()
	for _, tc := range cnAccountProtocolCases {
		for _, protocol := range tc.protocols {
			t.Run(tc.platform+"/"+tc.mode+"/"+protocol, func(t *testing.T) {
				t.Parallel()
				ctx := context.Background()
				repo := &accountServiceTestRepo{}
				svc := newOriginalAccountEditor(repo)
				credentials := cnAccountTestCredentials(tc.platform, tc.mode, protocol)
				wantCredentials := cnAccountTestCredentials(tc.platform, tc.mode, protocol)
				delete(wantCredentials, "api_protocol")
				wantProtocols := []protocolcore.ProtocolID{protocolcore.ProtocolOpenAIChatCompletions}
				switch protocol {
				case accountcore.APIProtocolAnthropic:
					wantProtocols = []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages}
				case accountcore.APIProtocolResponses:
					wantProtocols = []protocolcore.ProtocolID{protocolcore.ProtocolOpenAIResponses}
				case accountcore.APIProtocolAdaptive:
					wantProtocols = []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages, protocolcore.ProtocolOpenAIResponses, protocolcore.ProtocolOpenAIChatCompletions}
					if tc.platform == capability.PlatformZhipu {
						wantProtocols = []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages, protocolcore.ProtocolOpenAIChatCompletions}
					}
				}
				wantCredentials[accountcore.UpstreamProtocolsKey] = wantProtocols
				if protocol != accountcore.APIProtocolAdaptive {
					wantCredentials["api_base_urls"] = map[string]any{protocol: "https://relay.example.test/v1"}
				}

				created, err := svc.CreateAccount(ctx, &accountcore.CreateAccountInput{
					Name: "国产平台账号", Platform: tc.platform, Type: capability.AccountTypeAPIKey,
					Credentials: credentials, SkipDefaultGroupBind: true,
				})
				require.NoError(t, err)
				require.Equal(t, wantProtocols, created.UpstreamProtocols())
				require.Equal(t, wantCredentials, repo.accounts[created.ID].Credentials)

				// 普通字段编辑也会重新校验账号，必须允许已有自适应账号正常保存。
				updated, err := svc.UpdateAccount(ctx, created.ID, &accountcore.UpdateAccountInput{Name: "已编辑"})
				require.NoError(t, err)
				require.Equal(t, "已编辑", repo.accounts[created.ID].Name)
				require.Equal(t, wantCredentials, updated.Credentials)

				// 从传统 Chat 账号切换到目标协议，覆盖编辑与批量更新的凭据合并。
				repo.accounts[created.ID].Credentials = cnAccountTestCredentials(tc.platform, tc.mode, accountcore.APIProtocolChatCompletions)
				updated, err = svc.UpdateAccount(ctx, created.ID, &accountcore.UpdateAccountInput{Credentials: credentials})
				require.NoError(t, err)
				require.Equal(t, wantCredentials, updated.Credentials)
				repo.accounts[created.ID].Credentials = cnAccountTestCredentials(tc.platform, tc.mode, accountcore.APIProtocolChatCompletions)
				result, err := svc.BulkUpdateAccounts(ctx, &accountcore.BulkUpdateAccountsInput{
					AccountIDs: []int64{created.ID}, Credentials: credentials,
				})
				require.NoError(t, err)
				require.Equal(t, 1, result.Success)
				require.Len(t, repo.bulkUpdates, 1)
				require.Equal(t, wantProtocols, repo.bulkUpdates[0].ProtocolUpdates[created.ID][accountcore.UpstreamProtocolsKey])
			})
		}
	}
}

// TestCNProviderBulkProtocolValidationBeforeWrite 混合平台批量修改须在任何写入前拒绝非法组合。
func TestCNProviderBulkProtocolValidationBeforeWrite(t *testing.T) {
	t.Parallel()
	repo := &accountServiceTestRepo{accounts: map[int64]*accountcore.Record{
		1: {ID: 1, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, Credentials: cnAccountTestCredentials(capability.PlatformKimi, accountcore.AccountModePayG, accountcore.APIProtocolAdaptive)},
		2: {ID: 2, Platform: capability.PlatformZhipu, Type: capability.AccountTypeAPIKey, Credentials: cnAccountTestCredentials(capability.PlatformZhipu, accountcore.AccountModePayG, accountcore.APIProtocolAdaptive)},
	}}
	svc := newOriginalAccountEditor(repo)
	_, err := svc.BulkUpdateAccounts(context.Background(), &accountcore.BulkUpdateAccountsInput{
		AccountIDs: []int64{1, 2}, Credentials: map[string]any{"api_protocol": accountcore.APIProtocolResponses},
	})
	require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
	require.Equal(t, "CN_PROVIDER_PROTOCOL_INVALID", apperror.Reason(err))
	require.Empty(t, repo.bulkUpdates)
	for _, account := range repo.accounts {
		require.Equal(t, accountcore.APIProtocolAdaptive, account.Credentials["api_protocol"])
	}
}

// TestCNProviderLegacyCredentialDefaults 保留历史读取默认值，普通编辑不补写缺失字段。
func TestCNProviderLegacyCredentialDefaults(t *testing.T) {
	t.Parallel()
	for _, platform := range []string{capability.PlatformDeepseek, capability.PlatformKimi, capability.PlatformZhipu} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			repo := &accountServiceTestRepo{accounts: map[int64]*accountcore.Record{
				1: {ID: 1, Platform: platform, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "sk-test"}},
			}}
			svc := newOriginalAccountEditor(repo)
			updated, err := svc.UpdateAccount(ctx, 1, &accountcore.UpdateAccountInput{Name: "历史账号"})
			require.NoError(t, err)
			require.Equal(t, accountcore.AccountModePayG, updated.GetAccountMode())
			require.Equal(t, []protocolcore.ProtocolID{protocolcore.ProtocolOpenAIChatCompletions}, updated.UpstreamProtocols())
			require.NotContains(t, updated.Credentials, "account_mode")
			require.NotContains(t, updated.Credentials, "api_protocol")
			created, err := svc.CreateAccount(ctx, &accountcore.CreateAccountInput{
				Name: "缺省账号", Platform: platform, Type: capability.AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "sk-test"}, SkipDefaultGroupBind: true,
			})
			require.NoError(t, err)
			require.Equal(t, accountcore.AccountModePayG, created.Credentials["account_mode"])
			require.Equal(t, []protocolcore.ProtocolID{protocolcore.ProtocolOpenAIChatCompletions}, created.UpstreamProtocols())
		})
	}
}
