//go:build unit

package messageforward

import (
	"context"
	"io"
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

func TestApplyClaudeCodeOAuthMimicryToBody_HaikuRewritesSystem(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 405, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"model":"claude-haiku-4-5","system":"Pi project instructions","messages":[{"role":"user","content":"hello"}]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})

	out := svc.mimic(
		context.Background(), (*requestBoundaryFixture)(nil), &AttemptState{},

		account, body, "Pi project instructions", "claude-haiku-4-5",
	)

	system := gjson.GetBytes(out, "system").Array()
	require.Len(t, system, 3)
	require.Contains(t, system[0].Get("text").String(), "x-anthropic-billing-header:")
	require.Equal(t, claude.ClaudeCodeSystemPrompt, system[1].Get("text").String())
	require.Contains(t, gjson.GetBytes(out, "messages.0.content.0.text").String(), "Pi project instructions")
	require.Equal(t, "claude-haiku-4-5-20251001", gjson.GetBytes(out, "model").String())
}

func TestApplyClaudeCodeOAuthMimicryToBody_FableOmitsRefusedExpansion(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 406, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}
	body := []byte(`{"model":"claude-fable-5","system":"Project instructions","messages":[{"role":"user","content":"hello"}]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})

	out := svc.mimic(
		context.Background(), (*requestBoundaryFixture)(nil), &AttemptState{},

		account, body, "Project instructions", "claude-fable-5",
	)

	system := gjson.GetBytes(out, "system").Array()
	require.Len(t, system, 2)
	require.Contains(t, system[0].Get("text").String(), "x-anthropic-billing-header:")
	require.Equal(t, claude.ClaudeCodeSystemPrompt, system[1].Get("text").String())
	require.NotContains(t, string(out), claude.ClaudeCodeSystemPromptExpansion)
	require.Contains(t, gjson.GetBytes(out, "messages.0.content.0.text").String(), "Project instructions")
	require.Equal(t, "Understood. I will follow these instructions.", gjson.GetBytes(out, "messages.1.content.0.text").String())
	require.Equal(t, "hello", gjson.GetBytes(out, "messages.2.content").String())
}

// ============================================================================
// passthrough 集成测试：buildUpstreamRequest-
// AnthropicAPIKeyPassthrough 与 buildCountTokensRequestAnthropicAPIKeyPassthrough
// 路径上 sanitize 是否生效。
// ============================================================================

// passthrough 集成测试不设 base_url，避开 validateUpstreamBaseURL 对 cfg.Security 的依赖。
// targetURL 会走默认 claudeAPIURL，sanitize 逻辑与 baseURL 是否存在无关。

func newAnthropicAPIKeyPassthroughAccountForBetaTest() *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 501,
		Name:     "anthropic-apikey-passthrough-ctxmgmt-test",
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "upstream-key",
		},
		Extra:       map[string]any{"anthropic_passthrough": true},
		Status:      billing.StatusActive,
		Schedulable: true},
	}
}

func readUpstreamBodyForTest(t *testing.T, req *http.Request) []byte {
	t.Helper()
	require.NotNil(t, req.Body)
	b, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return b
}

func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_StripsContextManagementWhenClientHeaderMissingBeta(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	// 客户端仅带 oauth beta，不带 context-management-2025-06-27
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughAccountForBetaTest(), body, "token",
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"API-key passthrough + 客户端未带 context-management beta → strip body 字段")
}

func TestBuildUpstreamRequestAnthropicAPIKeyPassthrough_PreservesContextManagementWhenClientHeaderHasBeta(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,context-management-2025-06-27")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildPassthroughRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughAccountForBetaTest(), body, "token",
	)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"API-key passthrough + 客户端带 context-management beta → 字段保留（不过度删除）")
}

func TestBuildCountTokensRequestAnthropicAPIKeyPassthrough_StripsContextManagementWhenClientHeaderMissingBeta(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,token-counting-2024-11-01")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughAccountForBetaTest(), body, "token", "apikey", "", false, true,
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"count_tokens passthrough + 客户端未带 context-management beta → strip")
}

// ============================================================================
// 集成测试：buildUpstreamRequest
// 全路径验证上游 outgoing body 与 anthropic-beta header 严格对称。
// 这个测试能挡住未来某人忘调 sanitize / 将 sanitize 挪到请求构造之后 等 regression。
// ============================================================================

func TestBuildUpstreamRequest_OAuthMimicHaiku_PreservesContextManagementEndToEnd(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 401, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-tok"},
		Status:      billing.StatusActive,
		Schedulable: true},
	}
	// Haiku + mimic CC 使用完整 beta，其中包含 context-management；body 必须对称保留。
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", false, true, // mimicClaudeCode=true
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := claude.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"OAuth mimic + Haiku 端到端：outgoing body 必须保留 context_management")
	require.True(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaContextManagement),
		"对称约束：outgoing anthropic-beta header 必须包含 context-management beta")
	require.True(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaClaudeCode),
		"Haiku mimic 必须携带 claude-code beta")
}

func TestBuildUpstreamRequest_APIKeyHaiku_RemainsUnmimicked(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 404, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-ant-xxx"},
		Status:      billing.StatusActive, Schedulable: true},
	}
	body := []byte(`{"model":"claude-haiku-4-5","system":"API-key client system","thinking":{"type":"enabled"},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true, InjectAPIKeyBeta: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"sk-ant-xxx", "apikey", "claude-haiku-4-5", false, false,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.Equal(t, "API-key client system", gjson.GetBytes(outBody, "system").String())
	require.Equal(t, claude.APIKeyHaikuBetaHeader, claude.GetHeaderRaw(req.Header, "anthropic-beta"))
	require.False(t, claude.AnthropicBetaTokensContains(claude.GetHeaderRaw(req.Header, "anthropic-beta"), claude.BetaOAuth))
	require.NotContains(t, string(outBody), "x-anthropic-billing-header:")
}

func TestBuildUpstreamRequest_OAuthMimicNonHaiku_PreservesContextManagementEndToEnd(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 402, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-tok"},
		Status:      billing.StatusActive,
		Schedulable: true},
	}
	// sonnet + mimic CC → final beta = FullClaudeCodeMimicryBetas（含 context-management）→
	// body 保留。
	body := []byte(`{"model":"claude-sonnet-4-6","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"oauth-tok", "oauth", "claude-sonnet-4-6", false, true,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := claude.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"OAuth mimic + non-haiku：outgoing body 必须保留 context_management。")
	require.True(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaContextManagement),
		"对称约束：outgoing anthropic-beta header 同时含 context-management beta")
}

func TestBuildUpstreamRequest_OAuthTransparentHaikuWithRealCCBeta_PreservesField(t *testing.T) {
	// 端到端验证：真 CC 客户端 + haiku + 客户端 header 带 context-management beta
	// → final beta 透传 → 不应该过度删除 body 字段

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta",
		"claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 403, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-tok"},
		Status:      billing.StatusActive, Schedulable: true},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", false, false, // mimicClaudeCode=false（真 CC）
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := claude.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaContextManagement),
		"真 CC 透传路径：客户端 header 中的 context-management beta 必须保留")
	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"回归保护：真 CC + haiku + 客户端带 beta token 时，clear_thinking_20251015 功能不能静默失效")
}

// count_tokens 主路径 E2E 集成测试

func TestBuildCountTokensRequest_OAuthMimicHaiku_PreservesContextManagementEndToEnd(t *testing.T) {
	// count_tokens 继续注入 BetaContextManagement 和 BetaTokenCounting；
	// sanitize 看到最终 beta header 含 context-management beta 后保留字段。

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 411, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-tok"},
		Status:      billing.StatusActive, Schedulable: true},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"oauth-tok", "oauth", "claude-haiku-4-5", true, false, // mimicClaudeCode=true
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	outBeta := claude.GetHeaderRaw(req.Header, "anthropic-beta")

	require.True(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaContextManagement),
		"count_tokens mimic 始终注入 context-management beta")
	require.True(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"对称约束：final beta 含 token 时 body 字段保留")
	require.True(t, claude.AnthropicBetaTokensContains(outBeta, claude.BetaTokenCounting),
		"count_tokens 路径必须含 token-counting beta")
}

func TestBuildCountTokensRequest_OAuthMimic_DropsInjectedMaxTokens(t *testing.T) {
	// OAuth mimic 会为普通 messages 请求注入 max_tokens=128000，count_tokens 上游不接受该生成参数。

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 413, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "oauth-tok"},
		Status:      billing.StatusActive, Schedulable: true},
	}
	normalized, _ := claude.NormalizeClaudeOAuthRequestBody(
		[]byte(`{"model":"claude-sonnet-4-5","messages":[]}`),
		"claude-sonnet-4-5", claude.ClaudeOAuthNormalizeOptions{},
	)
	require.Equal(t, int64(128000), gjson.GetBytes(normalized, "max_tokens").Int(),
		"前置条件：OAuth mimic 注入 Claude Code 默认 max_tokens")

	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		account, normalized,
		"oauth-tok", "oauth", "claude-sonnet-4-5", true, false,
	)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "max_tokens").Exists(),
		"count_tokens 上游请求体不得包含 max_tokens")
}

func TestBuildCountTokensRequest_APIKeyHaiku_StripsContextManagementEndToEnd(t *testing.T) {
	// API-key + haiku + 客户端 header 不带 context-management beta → final beta 不含 → strip

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 412, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-ant-xxx"},
		Status:      billing.StatusActive, Schedulable: true},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"sk-ant-xxx", "apikey", "claude-haiku-4-5", false, false,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.False(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"count_tokens API-key + 客户端未带 beta token → body strip")
}

// count_tokens passthrough preserve 测试

func TestBuildCountTokensRequestAnthropicAPIKeyPassthrough_PreservesContextManagementWhenClientHeaderHasBeta(t *testing.T) {

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("Anthropic-Beta", "oauth-2025-04-20,context-management-2025-06-27,token-counting-2024-11-01")

	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildCountRequest(
		context.Background(), c, &AttemptState{},

		newAnthropicAPIKeyPassthroughAccountForBetaTest(), body, "token", "apikey", "", false, true,
	)
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(readUpstreamBodyForTest(t, req), "context_management").Exists(),
		"count_tokens passthrough + 客户端带 context-management beta → 字段保留")
}

func TestBuildUpstreamRequest_APIKeyHaikuWithContextManagement_StripsField(t *testing.T) {
	// API-key + haiku + body 带 context_management + 客户端 header 未带 context-management beta
	// → final beta 不含 → body 字段被 strip

	c := &requestBoundaryFixture{}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 404, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-ant-xxx"},
		Status:      billing.StatusActive, Schedulable: true},
	}
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[]},"messages":[]}`)
	svc := NewRuntime(Dependencies{}, Options{Configured: true})
	req, _, err := svc.buildRequest(
		context.Background(), c, &AttemptState{},

		account, body,
		"sk-ant-xxx", "apikey", "claude-haiku-4-5", false, false,
	)
	require.NoError(t, err)

	outBody := readUpstreamBodyForTest(t, req)
	require.False(t, gjson.GetBytes(outBody, "context_management").Exists(),
		"API-key + haiku + 客户端未带 beta token → body 字段必须被 strip")
}
