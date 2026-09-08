package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"testing"
)

// 国产平台仅同步显式协议，不发送网络探测。
func TestSyncCNProviderTextProtocol(t *testing.T) {
	tests := []struct {
		name     string
		id       int64
		platform string
		protocol string
		wantMode string
	}{
		{name: "deepseek adaptive supports responses", id: 201, platform: PlatformDeepseek, protocol: APIProtocolAdaptive, wantMode: string(openai_compat.TextRouteModeForceResponses)},
		{name: "deepseek chat clears forced responses", id: 202, platform: PlatformDeepseek, protocol: APIProtocolChatCompletions, wantMode: string(openai_compat.TextRouteModeForceChatCompletions)},
		{name: "kimi adaptive supports responses", id: 203, platform: PlatformKimi, protocol: APIProtocolAdaptive, wantMode: string(openai_compat.TextRouteModeForceResponses)},
		{name: "kimi responses protocol supports responses", id: 205, platform: PlatformKimi, protocol: APIProtocolResponses, wantMode: string(openai_compat.TextRouteModeForceResponses)},
		{name: "zhipu adaptive falls back to chat", id: 204, platform: PlatformZhipu, protocol: APIProtocolAdaptive, wantMode: string(openai_compat.TextRouteModeForceChatCompletions)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updateCalls := make(chan map[string]any, 1)
			account := Account{
				ID: tc.id, Platform: tc.platform, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "sk-test", "api_protocol": tc.protocol},
				Extra:       map[string]any{openai_compat.ExtraKeyTextRouteMode: string(openai_compat.TextRouteModeForceResponses)},
			}
			repo := &snapshotUpdateAccountRepo{
				stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
				updateExtraCalls:      updateCalls,
			}
			svc := &AccountTestService{accountRepo: repo}

			svc.SyncCNProviderTextProtocol(context.Background(), account.ID)

			updates := <-updateCalls
			require.Equal(t, map[string]any{openai_compat.ExtraKeyTextRouteMode: tc.wantMode}, updates)
		})
	}
}
