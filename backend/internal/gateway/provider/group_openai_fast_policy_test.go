package provider_test

import (
	"encoding/json"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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
			settings := tierpolicy.Default()
			if tt.action != "" {
				settings.Rules = []tierpolicy.OpenAIFastPolicyRule{{ServiceTier: "all", Scope: "all", Action: tt.action}}
			}
			svc := newFastPolicyContract(t, settings)
			svc.Prices = fastModeTestResolver()
			ctx := requeststate.WithGroup(fastModeTestContext(tt.key, "gpt-5.5"), &routing.Group{ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Hydrated: true, OpenAIFastPolicy: tt.group})
			payload := map[string]any{"model": "gpt-5.5", "type": "response.create"}
			if tt.tier != "" {
				payload["service_tier"] = tt.tier
			}
			body, _ := json.Marshal(payload)
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}}
			httpBody, httpErr := tierpolicy.ApplyBody(body, svc.Input(ctx, account, "gpt-5.5"))
			wsBody, blocked, err := gatewayws.ApplyServiceTierFrame(body, "gpt-5.5", svc.Input(ctx, account, "gpt-5.5"))
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
