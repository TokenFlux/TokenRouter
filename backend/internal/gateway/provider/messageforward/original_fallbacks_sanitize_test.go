//go:build unit

package messageforward

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildUpstreamRequest_OAuthMimicHaiku_StripsFallbacksEndToEnd(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 601, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-tok"},
		Status:      billing.StatusActive,
		Schedulable: true},
	}
	// 客户端默认透传 "fallbacks":"default"（Claude Code / SDK / OpenCode 等）
	body := []byte(`{"model":"claude-haiku-4-5","fallbacks":"default","messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", false, true, // mimicClaudeCode=true
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := claude.GetHeaderRaw(req.Header, "anthropic-beta")

	require.False(t, gjson.GetBytes(outBody, "fallbacks").Exists(),
		"OAuth mimic 端到端：mimic beta 集合不含 fallback beta → outgoing body 必须没有 fallbacks，"+
			"否则上游报 fallbacks: Extra inputs are not permitted")
	require.False(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaServerSideFallback),
		"修复策略是剥字段而非注入 beta：outgoing anthropic-beta 不得含 server-side-fallback beta")
	require.True(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaContextManagement),
		"mimic beta 集合本身不受影响")
}

// API-key passthrough + 客户端 header 未带 fallback beta → strip

func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_StripsFallbacksWhenClientHeaderMissingBeta(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// 客户端仅带 oauth beta，不带 server-side-fallback-2026-07-01
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20")

	body := []byte(`{"model":"claude-haiku-4-5","fallbacks":"default","messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughAccountForBetaTest(), body, "token",
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "fallbacks").Exists(),
		"API-key passthrough + 客户端未带 fallback beta → strip body 字段")
}

// API-key passthrough + 客户端 header 带 fallback beta → 保留（不过度删除）

func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_PreservesFallbacksWhenClientHeaderHasBeta(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,server-side-fallback-2026-07-01")

	// 模型数组形态：有 beta 时必须原样保留
	body := []byte(`{"model":"claude-opus-4-7","fallbacks":["claude-opus-4-6","claude-sonnet-4-6"],"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughAccountForBetaTest(), body, "token",
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.True(t, gjson.GetBytes(outBody, "fallbacks").Exists(),
		"客户端 header 带 server-side-fallback beta → 字段保留（不过度删除）")
	fallbacks := gjson.GetBytes(outBody, "fallbacks").Array()
	require.Len(t, fallbacks, 2)
	require.Equal(t, "claude-opus-4-6", fallbacks[0].String())
	require.Equal(t, "claude-sonnet-4-6", fallbacks[1].String())
}

// ============================================================================
// Bedrock 对称 strip
// ============================================================================

// fallback beta token 不在 bedrockSupportedBetaTokens 白名单内（会被
// filterBedrockBetaTokens 过滤），因此条件 strip 实际总会剥除——这是预期。
