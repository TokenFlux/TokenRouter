package provider_test

import (
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestNormalizeOpenAIResponsesWebSocketCompatibilityBodyStripsReasoningContentOnlyForOpenAI(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"gpt-5.6-sol","store":true,"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"keep"}],"content":[{"type":"reasoning_text","text":"remove"}]}]}`)
	for _, accountType := range []string{capability.AccountTypeAPIKey, capability.AccountTypeOAuth} {
		normalized, changed, err := gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI,
			Type: accountType},
		}), false)
		require.NoError(t, err)
		require.True(t, changed)
		require.False(t, gjson.GetBytes(normalized, "input.0.content").Exists())
		require.Equal(t, "keep", gjson.GetBytes(normalized, "input.0.summary.0.text").String())
	}

	normalized, changed, err := gatewayprovider.NormalizeOpenAIResponsesWebSocketCompatibilityBody(body, gatewayprovider.ExecutionProtocolRecord(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformZhipu,
		Type: capability.AccountTypeAPIKey},
	}), false)
	require.NoError(t, err)
	require.False(t, changed)
	require.JSONEq(t, string(body), string(normalized))
}
