package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

// 目录、保存与实际路线共享同一矩阵，显式空集合和平台拒绝均不能被默认值覆盖。
func TestProtocolNativeMatrixAndSave(t *testing.T) {
	for _, tc := range []struct {
		platform, kind, auth string
		count                int
	}{
		{PlatformAnthropic, AccountTypeAPIKey, "", 1}, {PlatformAnthropic, AccountTypeBedrock, "", 1},
		{PlatformOpenAI, AccountTypeAPIKey, "", 8}, {PlatformOpenAI, AccountTypeOAuth, "", 5},
		{PlatformOpenAI, AccountTypeOAuth, OpenAIAuthModePersonalAccessToken, 3}, {PlatformOpenAI, AccountTypeOAuth, OpenAIAuthModeAgentIdentity, 4},
		{PlatformDeepseek, AccountTypeAPIKey, "", 3}, {PlatformKimi, AccountTypeAPIKey, "", 3}, {PlatformZhipu, AccountTypeAPIKey, "", 2},
		{PlatformGemini, AccountTypeAPIKey, "", 2}, {PlatformGemini, AccountTypeServiceAccount, "", 2}, {PlatformGemini, AccountTypeOAuth, "", 1},
		{PlatformAntigravity, AccountTypeOAuth, "", 1}, {PlatformAntigravity, AccountTypeAPIKey, "", 0},
		{PlatformGrok, AccountTypeAPIKey, "", 11}, {PlatformGrok, AccountTypeOAuth, "", 11}, {PlatformQoder, AccountTypeCosy, "", 1},
	} {
		t.Run(tc.platform+"/"+tc.kind+"/"+tc.auth, func(t *testing.T) {
			account := &Account{Platform: tc.platform, Type: tc.kind, Credentials: map[string]any{"auth_mode": tc.auth}}
			options := account.NativeProtocolOptions()
			require.Len(t, options, tc.count)
			for _, protocol := range options {
				account.Credentials[upstreamProtocolsKey] = []string{string(protocol)}
				require.NoError(t, NormalizeAccountProtocols(account))
				require.Equal(t, []domain.ProtocolID{protocol}, account.UpstreamProtocols())
				target, ok := ResolveProtocolRoute(account, nil, protocol)
				require.True(t, ok)
				require.Equal(t, protocol, target)
			}
			account.Credentials[upstreamProtocolsKey] = []string{}
			require.NoError(t, NormalizeAccountProtocols(account))
			require.Empty(t, account.UpstreamProtocols())
			account.Credentials[upstreamProtocolsKey] = []string{"unknown"}
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(NormalizeAccountProtocols(account)))
		})
	}
}

func TestProtocolRouteNativeFirstAndExplicitFallback(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{upstreamProtocolsKey: []string{"anthropic_messages", "openai_responses"}, "api_base_urls": map[string]any{"anthropic": "https://relay.example/messages", "responses": "https://relay.example/responses"}}}
	group := &Group{Platform: PlatformDeepseek, AllowedProtocols: []domain.ProtocolID{domain.ProtocolAnthropicMessages}, ProtocolFallbacks: map[domain.ProtocolID]domain.ProtocolID{domain.ProtocolAnthropicMessages: domain.ProtocolOpenAIResponses}}
	ctx := WithClientProtocol(context.WithValue(context.Background(), ctxkey.Group, group), domain.ProtocolAnthropicMessages)
	selected, err := accountForProtocolAttempt(ctx, account)
	require.NoError(t, err)
	require.Equal(t, APIProtocolAnthropic, selected.GetAPIProtocol())
	require.Empty(t, account.resolvedProtocol)
	require.Equal(t, "https://relay.example/messages", selected.GetAnthropicProtocolBaseURL())
	account.Credentials[upstreamProtocolsKey] = []string{"openai_responses"}
	selected, err = accountForProtocolAttempt(ctx, account)
	require.NoError(t, err)
	require.Equal(t, APIProtocolResponses, selected.GetAPIProtocol())
	require.Equal(t, "https://relay.example/responses", selected.GetCNProtocolBaseURL(APIProtocolResponses))
	// 转换目标无需向客户端开放；下一次切号重新使用该候选的集合。
	require.False(t, group.AllowsClientProtocol(domain.ProtocolOpenAIResponses))
	next := *account
	next.Credentials = map[string]any{upstreamProtocolsKey: []string{"openai_chat_completions"}}
	require.False(t, next.allowsProtocolRequest(ctx))
	delete(group.ProtocolFallbacks, domain.ProtocolAnthropicMessages)
	require.False(t, account.allowsProtocolRequest(ctx))
}

func TestProtocolConversionAccountConstraints(t *testing.T) {
	for _, tc := range []struct {
		platform, kind, auth string
		source, target       domain.ProtocolID
		want                 bool
	}{
		{PlatformOpenAI, AccountTypeOAuth, "", domain.ProtocolImagesEdits, domain.ProtocolOpenAIResponses, true},
		{PlatformOpenAI, AccountTypeAPIKey, "", domain.ProtocolImagesEdits, domain.ProtocolOpenAIResponses, false},
		{PlatformGrok, AccountTypeAPIKey, "", domain.ProtocolResponsesWebSocket, domain.ProtocolOpenAIResponses, true},
		{PlatformGrok, AccountTypeOAuth, "", domain.ProtocolWebSearch, domain.ProtocolOpenAIResponses, true},
		{PlatformOpenAI, AccountTypeOAuth, OpenAIAuthModePersonalAccessToken, domain.ProtocolAlphaSearch, domain.ProtocolOpenAIResponses, true},
		{PlatformOpenAI, AccountTypeOAuth, "", domain.ProtocolAlphaSearch, domain.ProtocolOpenAIResponses, false},
		{PlatformOpenAI, AccountTypeAPIKey, "", domain.ProtocolEmbeddings, domain.ProtocolOpenAIResponses, false},
		{PlatformGrok, AccountTypeAPIKey, "", domain.ProtocolTTS, domain.ProtocolOpenAIResponses, false},
	} {
		t.Run(string(tc.source)+"/"+tc.platform+"/"+tc.kind+"/"+tc.auth, func(t *testing.T) {
			require.Equal(t, tc.want, domain.SupportsProtocolConversion(tc.platform, tc.kind, tc.auth, tc.source, tc.target))
		})
	}
}

func TestProtocolSaveEntrypointsAndBulkRejectBeforeWrite(t *testing.T) {
	repo := &accountServiceTestRepo{accounts: map[int64]*Account{}}
	svc := &adminServiceImpl{accountRepo: repo}
	created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{Name: "native", Platform: PlatformKimi, Type: AccountTypeAPIKey, SkipDefaultGroupBind: true, Credentials: map[string]any{"api_key": "test", upstreamProtocolsKey: []string{"anthropic_messages", "openai_responses", "openai_chat_completions"}, "api_base_urls": map[string]any{"responses": "https://relay.example/v1"}}})
	require.NoError(t, err)
	require.NotContains(t, created.Credentials, "api_protocol")
	require.Len(t, created.UpstreamProtocols(), 3)
	updated, err := svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Name: "renamed"})
	require.NoError(t, err)
	require.Len(t, updated.UpstreamProtocols(), 3)
	require.Equal(t, map[string]any{"responses": "https://relay.example/v1"}, updated.Credentials["api_base_urls"])
	rotated, err := svc.UpdateAccount(context.Background(), created.ID, &UpdateAccountInput{Credentials: map[string]any{"api_key": "rotated"}})
	require.NoError(t, err)
	require.Len(t, rotated.UpstreamProtocols(), 3)
	require.Equal(t, map[string]any{"responses": "https://relay.example/v1"}, rotated.Credentials["api_base_urls"])
	repo.accounts[99] = &Account{ID: 99, Platform: PlatformZhipu, Type: AccountTypeAPIKey, Credentials: map[string]any{upstreamProtocolsKey: []string{"openai_chat_completions"}}}
	_, err = svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{created.ID, 99}, Credentials: map[string]any{upstreamProtocolsKey: []string{"openai_responses"}}})
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Empty(t, repo.bulkUpdates)
}

func TestProtocolImagePolicyAndBatchBinding(t *testing.T) {
	group := &Group{Platform: PlatformOpenAI, AllowedProtocols: []domain.ProtocolID{}, ResponsesImagePolicy: "enabled"}
	require.NoError(t, normalizeGroupProtocolPolicy(group, nil))
	require.False(t, GroupAllowsImageGeneration(group))
	require.True(t, GroupAllowsResponsesImages(group))
	require.Equal(t, codexImageGenerationExplicitToolPolicyAllow, groupResponsesExplicitToolPolicy(group, codexImageGenerationExplicitToolPolicyStrip))
	group.ResponsesImagePolicy = "block"
	require.Equal(t, codexImageGenerationExplicitToolPolicyStrip, groupResponsesExplicitToolPolicy(group, codexImageGenerationExplicitToolPolicyAllow))
	for _, tc := range []struct {
		kind   string
		target domain.ProtocolID
	}{{AccountTypeAPIKey, domain.ProtocolGeminiBatch}, {AccountTypeServiceAccount, domain.ProtocolVertexBatch}} {
		a := &Account{Platform: PlatformGemini, Type: tc.kind, Credentials: map[string]any{upstreamProtocolsKey: []domain.ProtocolID{tc.target}}}
		target, ok := ResolveProtocolRoute(a, nil, domain.ProtocolImageBatches)
		require.True(t, ok)
		require.Equal(t, tc.target, target)
	}
}

func TestProtocolAuxiliaryModelURLAndIndependentTransports(t *testing.T) {
	account := &Account{Platform: PlatformKimi, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": "anthropic", "base_url": "https://relay.example/custom/anthropic", "api_key": "test"}}
	require.NoError(t, NormalizeAccountProtocols(account))
	require.Equal(t, "https://relay.example/custom", account.GetOpenAIFormatBaseURL())
	for _, protocol := range []domain.ProtocolID{domain.ProtocolResponsesWebSocket, domain.ProtocolResponsesCompact} {
		a := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{upstreamProtocolsKey: []domain.ProtocolID{protocol}}}
		ctx := WithClientProtocol(context.Background(), protocol)
		require.True(t, supportsOpenAIRequestCapability(ctx, a, OpenAIEndpointCapabilityResponses))
		require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityTextGeneration))
	}
	group := &Group{Platform: PlatformOpenAI, AllowedProtocols: []domain.ProtocolID{domain.ProtocolImagesEdits}, ResponsesImagePolicy: "block"}
	require.Equal(t, []string{CreativeOperationEdit, CreativeOperationInpaint}, creativeOperationsForGroup(group))
	require.Nil(t, responsesPolicyGroup(WithClientProtocol(context.Background(), domain.ProtocolImagesEdits), group))
}
