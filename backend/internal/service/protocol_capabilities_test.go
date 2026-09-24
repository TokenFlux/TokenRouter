package service

import (
	"context"
	"testing"
	time "time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestProtocolRouteNativeFirstAndExplicitFallback(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformDeepseek, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []string{"anthropic_messages", "openai_responses"}, "api_base_urls": map[string]any{"anthropic": "https://relay.example/messages", "responses": "https://relay.example/responses"}}}}
	group := &routing.Group{Platform: capability.PlatformDeepseek, AllowedProtocols: []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages}, ProtocolFallbacks: map[protocolcore.ProtocolID]protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages: protocolcore.ProtocolOpenAIResponses}}
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), protocolcore.ProtocolAnthropicMessages)
	selected, err := gatewayprovider.AccountForProtocolAttempt(ctx, account)
	require.NoError(t, err)
	require.Equal(t, accountcore.APIProtocolAnthropic, gatewayprovider.ExecutionProtocolTarget(selected).GetAPIProtocol())
	require.Empty(t, account.Route.Protocol())
	require.Equal(t, "https://relay.example/messages", gatewayprovider.ExecutionProtocolTarget(selected).GetAnthropicProtocolBaseURL())
	account.Record.Credentials[accountcore.UpstreamProtocolsKey] = []string{"openai_responses"}
	selected, err = gatewayprovider.AccountForProtocolAttempt(ctx, account)
	require.NoError(t, err)
	require.Equal(t, accountcore.APIProtocolResponses, gatewayprovider.ExecutionProtocolTarget(selected).GetAPIProtocol())
	require.Equal(t, "https://relay.example/responses", gatewayprovider.ExecutionProtocolTarget(selected).GetCNProtocolBaseURL(accountcore.APIProtocolResponses))
	// 转换目标无需向客户端开放；下一次切号重新使用该候选的集合。
	require.False(t, group.AllowsClientProtocol(protocolcore.ProtocolOpenAIResponses))
	next := *account
	next.Record.Credentials = map[string]any{accountcore.UpstreamProtocolsKey: []string{"openai_chat_completions"}}
	require.False(t, gatewayprovider.ExecutionModelPolicy(&next).AllowsProtocol(ctx))
	delete(group.ProtocolFallbacks, protocolcore.ProtocolAnthropicMessages)
	ctx = requeststate.WithGroup(ctx, group)
	require.False(t, gatewayprovider.ExecutionModelPolicy(account).AllowsProtocol(ctx))
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
	require.False(t, gatewaymedia.GroupImagePermission(group != nil, group.AllowImageGeneration))
	require.True(t, routing.GroupAllowsResponsesImages(group))
	require.Equal(t, accountcore.CodexImagePolicyAllow, groupResponsesExplicitToolPolicy(group, accountcore.CodexImagePolicyStrip))
	group.ResponsesImagePolicy = "block"
	require.Equal(t, accountcore.CodexImagePolicyStrip, groupResponsesExplicitToolPolicy(group, accountcore.CodexImagePolicyAllow))
	for _, tc := range []struct {
		kind   string
		target protocolcore.ProtocolID
	}{{capability.AccountTypeAPIKey, protocolcore.ProtocolGeminiBatch}, {capability.AccountTypeServiceAccount, protocolcore.ProtocolVertexBatch}} {
		a := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini, Type: tc.kind, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []protocolcore.ProtocolID{tc.target}}}}
		target, ok := gatewayprovider.ExecutionModelPolicy(a).ProtocolRoute(nil, protocolcore.ProtocolImageBatches)
		require.True(t, ok)
		require.Equal(t, tc.target, target)
	}
}

func TestProtocolAuxiliaryModelURLAndIndependentTransports(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformKimi, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": "anthropic", "base_url": "https://relay.example/custom/anthropic", "api_key": "test"}}}
	require.NoError(t, gatewayprovider.NormalizeExecutionProtocols(account))
	require.Equal(t, "https://relay.example/custom", gatewayprovider.ExecutionProtocolTarget(account).GetOpenAIFormatBaseURL())
	for _, protocol := range []protocolcore.ProtocolID{protocolcore.ProtocolResponsesWebSocket, protocolcore.ProtocolResponsesCompact} {
		a := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{accountcore.UpstreamProtocolsKey: []protocolcore.ProtocolID{protocol}}}}
		ctx := requeststate.WithClientProtocol(context.Background(), protocol)
		require.True(t, gatewayprovider.
			SupportsRequestCapability(ctx, a, accountcore.OpenAIEndpointCapabilityResponses))
		require.False(t, accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(a), accountcore.OpenAIEndpointCapabilityTextGeneration))
	}
	group := &routing.Group{Platform: capability.PlatformOpenAI, AllowedProtocols: []protocolcore.ProtocolID{protocolcore.ProtocolImagesEdits}, ResponsesImagePolicy: "block"}
	require.Equal(t, []string{creative.CreativeOperationEdit, creative.CreativeOperationInpaint}, creativeOperationsForGroup(group))
	require.Nil(t, responsesPolicyGroup(requeststate.WithClientProtocol(context.Background(), protocolcore.ProtocolImagesEdits), group))
}
