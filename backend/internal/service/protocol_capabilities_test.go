package service

import (
	"context"
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

// 目录、保存与实际路线共享同一矩阵，显式空集合和平台拒绝均不能被默认值覆盖。
func TestProtocolNativeMatrixAndSave(t *testing.T) {
	for _, tc := range []struct {
		platform, kind, auth string
		count                int
	}{
		{capability.PlatformAnthropic, capability.AccountTypeAPIKey, "", 1}, {capability.PlatformAnthropic, capability.AccountTypeBedrock, "", 1},
		{capability.PlatformOpenAI, capability.AccountTypeAPIKey, "", 8}, {capability.PlatformOpenAI, capability.AccountTypeOAuth, "", 5},
		{capability.PlatformOpenAI, capability.AccountTypeOAuth, accountcore.OpenAIAuthModePersonalAccessToken, 3}, {capability.PlatformOpenAI, capability.AccountTypeOAuth, accountcore.OpenAIAuthModeAgentIdentity, 4},
		{capability.PlatformDeepseek, capability.AccountTypeAPIKey, "", 3}, {capability.PlatformKimi, capability.AccountTypeAPIKey, "", 3}, {capability.PlatformZhipu, capability.AccountTypeAPIKey, "", 2},
		{capability.PlatformGemini, capability.AccountTypeAPIKey, "", 2}, {capability.PlatformGemini, capability.AccountTypeServiceAccount, "", 2}, {capability.PlatformGemini, capability.AccountTypeOAuth, "", 1},
		{capability.PlatformAntigravity, capability.AccountTypeOAuth, "", 1}, {capability.PlatformAntigravity, capability.AccountTypeAPIKey, "", 0},
		{capability.PlatformGrok, capability.AccountTypeAPIKey, "", 11}, {capability.PlatformGrok, capability.AccountTypeOAuth, "", 11}, {capability.PlatformQoder, capability.AccountTypeCosy, "", 1},
	} {
		t.Run(tc.platform+"/"+tc.kind+"/"+tc.auth, func(t *testing.T) {
			account := &Account{Platform: tc.platform, Type: tc.kind, Credentials: map[string]any{"auth_mode": tc.auth}}
			options := account.NativeProtocolOptions()
			require.Len(t, options, tc.count)
			for _, protocol := range options {
				account.Credentials[accountcore.UpstreamProtocolsKey] = []string{string(protocol)}
				require.NoError(t, NormalizeAccountProtocols(account))
				require.Equal(t, []protocolcore.ProtocolID{protocol}, account.UpstreamProtocols())
				target, ok := ResolveProtocolRoute(account, nil, protocol)
				require.True(t, ok)
				require.Equal(t, protocol, target)
			}
			account.Credentials[accountcore.UpstreamProtocolsKey] = []string{}
			require.NoError(t, NormalizeAccountProtocols(account))
			require.Empty(t, account.UpstreamProtocols())
			account.Credentials[accountcore.UpstreamProtocolsKey] = []string{"unknown"}
			require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(NormalizeAccountProtocols(account)))
		})
	}
}

func TestProtocolRouteNativeFirstAndExplicitFallback(t *testing.T) {
	account := &Account{ID: 1, Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []string{"anthropic_messages", "openai_responses"}, "api_base_urls": map[string]any{"anthropic": "https://relay.example/messages", "responses": "https://relay.example/responses"}}}
	group := &routing.Group{Platform: capability.PlatformDeepseek, AllowedProtocols: []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages}, ProtocolFallbacks: map[protocolcore.ProtocolID]protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages: protocolcore.ProtocolOpenAIResponses}}
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), protocolcore.ProtocolAnthropicMessages)
	selected, err := accountForProtocolAttempt(ctx, account)
	require.NoError(t, err)
	require.Equal(t, accountcore.APIProtocolAnthropic, selected.GetAPIProtocol())
	require.Empty(t, account.attemptRoute.Protocol())
	require.Equal(t, "https://relay.example/messages", selected.GetAnthropicProtocolBaseURL())
	account.Credentials[accountcore.UpstreamProtocolsKey] = []string{"openai_responses"}
	selected, err = accountForProtocolAttempt(ctx, account)
	require.NoError(t, err)
	require.Equal(t, accountcore.APIProtocolResponses, selected.GetAPIProtocol())
	require.Equal(t, "https://relay.example/responses", selected.GetCNProtocolBaseURL(accountcore.APIProtocolResponses))
	// 转换目标无需向客户端开放；下一次切号重新使用该候选的集合。
	require.False(t, group.AllowsClientProtocol(protocolcore.ProtocolOpenAIResponses))
	next := *account
	next.Credentials = map[string]any{accountcore.UpstreamProtocolsKey: []string{"openai_chat_completions"}}
	require.False(t, next.allowsProtocolRequest(ctx))
	delete(group.ProtocolFallbacks, protocolcore.ProtocolAnthropicMessages)
	ctx = requeststate.WithGroup(ctx, group)
	require.False(t, account.allowsProtocolRequest(ctx))
}

func TestProtocolConversionAccountConstraints(t *testing.T) {
	for _, tc := range []struct {
		platform, kind, auth string
		source, target       protocolcore.ProtocolID
		want                 bool
	}{
		{capability.PlatformOpenAI, capability.AccountTypeOAuth, "", protocolcore.ProtocolImagesEdits, protocolcore.ProtocolOpenAIResponses, true},
		{capability.PlatformOpenAI, capability.AccountTypeAPIKey, "", protocolcore.ProtocolImagesEdits, protocolcore.ProtocolOpenAIResponses, false},
		{capability.PlatformGrok, capability.AccountTypeAPIKey, "", protocolcore.ProtocolResponsesWebSocket, protocolcore.ProtocolOpenAIResponses, true},
		{capability.PlatformGrok, capability.AccountTypeOAuth, "", protocolcore.ProtocolWebSearch, protocolcore.ProtocolOpenAIResponses, true},
		{capability.PlatformOpenAI, capability.AccountTypeOAuth, accountcore.OpenAIAuthModePersonalAccessToken, protocolcore.ProtocolAlphaSearch, protocolcore.ProtocolOpenAIResponses, true},
		{capability.PlatformOpenAI, capability.AccountTypeOAuth, "", protocolcore.ProtocolAlphaSearch, protocolcore.ProtocolOpenAIResponses, false},
		{capability.PlatformOpenAI, capability.AccountTypeAPIKey, "", protocolcore.ProtocolEmbeddings, protocolcore.ProtocolOpenAIResponses, false},
		{capability.PlatformGrok, capability.AccountTypeAPIKey, "", protocolcore.ProtocolTTS, protocolcore.ProtocolOpenAIResponses, false},
	} {
		t.Run(string(tc.source)+"/"+tc.platform+"/"+tc.kind+"/"+tc.auth, func(t *testing.T) {
			require.Equal(t, tc.want, capability.SupportsProtocolConversion(tc.platform, tc.kind, tc.auth, tc.source, tc.target))
		})
	}
}

func TestProtocolImagePolicyAndBatchBinding(t *testing.T) {
	group := &routing.Group{Platform: capability.PlatformOpenAI, AllowedProtocols: []protocolcore.ProtocolID{}, ResponsesImagePolicy: "enabled"}
	require.NoError(t, routing.NormalizeGroupProtocolPolicy(group, nil))
	*group = *routing.CloneGroup(group)
	require.False(t, GroupAllowsImageGeneration(group))
	require.True(t, GroupAllowsResponsesImages(group))
	require.Equal(t, codexImageGenerationExplicitToolPolicyAllow, groupResponsesExplicitToolPolicy(group, codexImageGenerationExplicitToolPolicyStrip))
	group.ResponsesImagePolicy = "block"
	require.Equal(t, codexImageGenerationExplicitToolPolicyStrip, groupResponsesExplicitToolPolicy(group, codexImageGenerationExplicitToolPolicyAllow))
	for _, tc := range []struct {
		kind   string
		target protocolcore.ProtocolID
	}{{capability.AccountTypeAPIKey, protocolcore.ProtocolGeminiBatch}, {capability.AccountTypeServiceAccount, protocolcore.ProtocolVertexBatch}} {
		a := &Account{Platform: capability.PlatformGemini, Type: tc.kind, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []protocolcore.ProtocolID{tc.target}}}
		target, ok := ResolveProtocolRoute(a, nil, protocolcore.ProtocolImageBatches)
		require.True(t, ok)
		require.Equal(t, tc.target, target)
	}
}

func TestProtocolAuxiliaryModelURLAndIndependentTransports(t *testing.T) {
	account := &Account{Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": "anthropic", "base_url": "https://relay.example/custom/anthropic", "api_key": "test"}}
	require.NoError(t, NormalizeAccountProtocols(account))
	require.Equal(t, "https://relay.example/custom", account.GetOpenAIFormatBaseURL())
	for _, protocol := range []protocolcore.ProtocolID{protocolcore.ProtocolResponsesWebSocket, protocolcore.ProtocolResponsesCompact} {
		a := &Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []protocolcore.ProtocolID{protocol}}}
		ctx := requeststate.WithClientProtocol(context.Background(), protocol)
		require.True(t, supportsOpenAIRequestCapability(ctx, a, accountcore.OpenAIEndpointCapabilityResponses))
		require.False(t, a.SupportsOpenAIEndpointCapability(accountcore.OpenAIEndpointCapabilityTextGeneration))
	}
	group := &routing.Group{Platform: capability.PlatformOpenAI, AllowedProtocols: []protocolcore.ProtocolID{protocolcore.ProtocolImagesEdits}, ResponsesImagePolicy: "block"}
	require.Equal(t, []string{creative.CreativeOperationEdit, creative.CreativeOperationInpaint}, creativeOperationsForGroup(group))
	require.Nil(t, responsesPolicyGroup(requeststate.WithClientProtocol(context.Background(), protocolcore.ProtocolImagesEdits), group))
}
