package service

import (
	"context"
	"encoding/json"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"testing"
)

// 分组加速、单 Key 和全局规则在 HTTP/WS 中必须保持一致。
func TestGroupOpenAIFastPolicyHTTPAndWS(t *testing.T) {
	for _, tt := range []struct {
		group, tier, key, action, want string
		blocked                        bool
	}{
		{group: "follow_request", tier: "ultrafast", want: "ultrafast"},
		{group: "force_priority", tier: "ultrafast", want: "priority"},
		{group: "force_ultrafast", want: "ultrafast"},
		{group: "force_ultrafast", key: "force_on", want: "ultrafast"},
		{group: "force_ultrafast", key: "force_off"},
		{group: "force_off", tier: "ultrafast"},
		{group: "force_off", tier: "fast"},
		{group: "force_off", key: "force_on"},
		{group: "force_off", tier: "flex", want: "flex"},
		{group: "force_ultrafast", action: "filter"},
		{group: "force_ultrafast", action: "block", blocked: true},
		{group: "force_off", tier: "priority", action: "force_ultrafast", want: "ultrafast"},
	} {
		t.Run(tt.group+"/"+tt.tier+"/"+tt.key+"/"+tt.action, func(t *testing.T) {
			settings := DefaultOpenAIFastPolicySettings()
			if tt.action != "" {
				settings.Rules = []OpenAIFastPolicyRule{{ServiceTier: "all", Scope: "all", Action: tt.action}}
			}
			svc := newOpenAIGatewayServiceWithSettings(t, settings)
			svc.resolver = fastModeTestResolver()
			ctx := context.WithValue(fastModeTestContext(tt.key, "gpt-5.5"), ctxkey.Group, &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, OpenAIFastPolicy: tt.group})
			payload := map[string]any{"model": "gpt-5.5", "type": "response.create"}
			if tt.tier != "" {
				payload["service_tier"] = tt.tier
			}
			body, _ := json.Marshal(payload)
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			httpBody, httpErr := svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.5", body)
			wsBody, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(ctx, account, "gpt-5.5", body)
			require.NoError(t, err)
			if tt.blocked {
				require.Error(t, httpErr)
				require.NotNil(t, blocked)
				return
			}
			require.NoError(t, httpErr)
			require.Nil(t, blocked)
			for _, out := range [][]byte{httpBody, wsBody} {
				require.Equal(t, tt.want, gjson.GetBytes(out, "service_tier").String())
				require.Equal(t, tt.want != "", gjson.GetBytes(out, "service_tier").Exists())
			}
		})
	}
}

// 全局新动作同时允许出现在主动作与其它模型动作并持久化往返。
func TestGlobalForceUltrafastPersists(t *testing.T) {
	svc := &SettingService{settingRepo: &openAIFastPolicyRepoStub{}}
	settings := &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{ServiceTier: "all", Scope: "all", Action: "force_ultrafast", ModelWhitelist: []string{"gpt-*"}, FallbackAction: "force_ultrafast"}}}
	require.NoError(t, svc.SetOpenAIFastPolicySettings(context.Background(), settings))
	got, err := svc.GetOpenAIFastPolicySettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, settings, got)
}
