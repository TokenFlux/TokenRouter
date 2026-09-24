//go:build unit

package anthropic_test

import (
	"net/http"

	"strings"
	"testing"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAnthropicBetaTokensContains_EmptyInputs(t *testing.T) {
	require.False(t, claude.AnthropicBetaTokensContains("", "context-management-2025-06-27"))
	require.False(t, claude.AnthropicBetaTokensContains("oauth-2025-04-20", ""))
}

func TestAnthropicBetaTokensContains_SingleToken(t *testing.T) {
	require.True(t, claude.AnthropicBetaTokensContains("context-management-2025-06-27", "context-management-2025-06-27"))
}

func TestAnthropicBetaTokensContains_MultiTokenComma(t *testing.T) {
	header := "oauth-2025-04-20,context-management-2025-06-27,interleaved-thinking-2025-05-14"
	require.True(t, claude.AnthropicBetaTokensContains(header, "context-management-2025-06-27"))
	require.True(t, claude.AnthropicBetaTokensContains(header, "oauth-2025-04-20"))
	require.False(t, claude.AnthropicBetaTokensContains(header, "fast-mode-2026-02-01"))
}

func TestAnthropicBetaTokensContains_ToleratesWhitespace(t *testing.T) {
	header := "oauth-2025-04-20 , context-management-2025-06-27 ,  interleaved-thinking-2025-05-14"
	require.True(t, claude.AnthropicBetaTokensContains(header, "context-management-2025-06-27"))
}

func TestAnthropicBetaTokensContains_SubstringNotMatched(t *testing.T) {
	// 严格 token 比较，不应被子串误匹配
	require.False(t, claude.AnthropicBetaTokensContains("context-management-2025-06-27-rev2", "context-management-2025-06-27"),
		"必须按 token 边界匹配，不允许 prefix 子串误命中")
}

// ============================================================================
// sanitizeAnthropicBodyForBetaTokens
// ============================================================================

func TestSanitizeAnthropicBodyForBetaTokens_NoFieldNoChange(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "oauth-2025-04-20")
	require.False(t, changed)
	require.Equal(t, string(body), string(out))
}

func TestSanitizeAnthropicBodyForBetaTokens_FieldKeptWhenBetaPresent(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body,
		"oauth-2025-04-20,context-management-2025-06-27,interleaved-thinking-2025-05-14")
	require.False(t, changed)
	require.True(t, gjson.GetBytes(out, "context_management").Exists())
	require.Equal(t, "clear_thinking_20251015",
		gjson.GetBytes(out, "context_management.edits.0.type").String())
}

func TestSanitizeAnthropicBodyForBetaTokens_FieldStrippedWhenBetaMissing(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "oauth-2025-04-20,interleaved-thinking-2025-05-14")
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "context_management").Exists(),
		"header 不含 context-management beta 时必须 strip 同名字段")
}

func TestSanitizeAnthropicBodyForBetaTokens_FieldStrippedWhenBetaEmpty(t *testing.T) {
	body := []byte(`{"context_management":{"edits":[]},"messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "")
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "context_management").Exists())
}

func TestSanitizeAnthropicBodyForBetaTokens_EmptyBody(t *testing.T) {
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens([]byte{}, "")
	require.False(t, changed)
	require.Empty(t, out)

	out, changed = claude.SanitizeAnthropicBodyForBetaTokens(nil, "")
	require.False(t, changed)
	require.Empty(t, out)
}

// ★ 关键回归断言：能力维度 sanitize 解决了 "真 CC + haiku" 路径的过度删除问题。
// 真实 Claude Code CLI 2.1.87+ 客户端 header 含 context-management beta；
// 即使 model 是 haiku，sanitize 也不应剥离功能字段。

func TestSanitizeAnthropicBodyForBetaTokens_HaikuRealCCClientPreservesField(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[]}`)
	// 真 Claude Code CLI 2.1.87+ 客户端 header 含 context-management beta
	clientBeta := "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27"
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, clientBeta)
	require.False(t, changed,
		"真 CC 客户端 header 含 context-management beta 时，haiku body 字段必须保留（功能不丢）")
	require.True(t, gjson.GetBytes(out, "context_management").Exists())
}

// ============================================================================
// computeFinalAnthropicBeta — 关键路径
// ============================================================================

func TestComputeFinalAnthropicBeta_OAuthMimic_NonHaiku_IncludesContextManagement(t *testing.T) {
	s := false
	final, ok := claude.ComputeFinalAnthropicBeta("oauth", true, "claude-sonnet-4-6", http.Header{}, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaContextManagement),
		"OAuth mimic non-haiku 必须注入完整 CC mimicry beta，含 context-management-2025-06-27")
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaOAuth))
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaClaudeCode))
}

func TestComputeFinalAnthropicBeta_OAuthMimic_Haiku_IncludesFullClaudeCodeBetas(t *testing.T) {
	s := false
	final, ok := claude.ComputeFinalAnthropicBeta("oauth", true, "claude-haiku-4-5", http.Header{}, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.Equal(t, strings.Join(claude.FullClaudeCodeMimicryBetas(), ","), final)
	for _, beta := range claude.FullClaudeCodeMimicryBetas() {
		require.Truef(t, claude.AnthropicBetaTokensContains(final, beta),
			"OAuth mimic Haiku 必须包含完整 Claude Code beta 集合，缺少 %s", beta)
	}
}

func TestComputeFinalAnthropicBeta_OAuthMimic_IgnoresClientBeta(t *testing.T) {
	// mimic 路径下原代码白名单透传被跳过，client beta 应被忽略
	s := false
	hdr := http.Header{}
	hdr.Set("anthropic-beta", "custom-experimental-beta")
	final, ok := claude.ComputeFinalAnthropicBeta("oauth", true, "claude-sonnet-4-6", hdr, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.False(t, strings.Contains(final, "custom-experimental-beta"),
		"mimic 路径必须忽略客户端 anthropic-beta header")
}

func TestComputeFinalAnthropicBeta_OAuthTransparent_NonHaiku_PreservesClientContextManagement(t *testing.T) {
	// 真 CC 客户端透传：客户端 header 中的 context-management beta 必须保留
	s := false
	hdr := http.Header{}
	hdr.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20,context-management-2025-06-27")
	final, ok := claude.ComputeFinalAnthropicBeta("oauth", false, "claude-sonnet-4-6", hdr, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaContextManagement))
}

func TestComputeFinalAnthropicBeta_OAuthTransparent_Haiku_RealCCPreservesContextManagement(t *testing.T) {
	// haiku 透传 + 客户端带 context-management beta → 必须保留
	// （能力维度核心场景：避免 model-name 误删客户端透传的功能 beta）
	s := false
	hdr := http.Header{}
	hdr.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20,context-management-2025-06-27,interleaved-thinking-2025-05-14")
	final, ok := claude.ComputeFinalAnthropicBeta("oauth", false, "claude-haiku-4-5", hdr, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaContextManagement),
		"真 CC + haiku + 客户端带 context-management beta → 透传必须保留")
}

func TestComputeFinalAnthropicBeta_APIKey_PassesClientBetaThroughDropSet(t *testing.T) {
	s := false
	hdr := http.Header{}
	hdr.Set("anthropic-beta", "oauth-2025-04-20,custom-beta")
	final, ok := claude.ComputeFinalAnthropicBeta("apikey", false, "claude-sonnet-4-6", hdr, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.True(t, claude.AnthropicBetaTokensContains(final, "oauth-2025-04-20"))
	require.True(t, claude.AnthropicBetaTokensContains(final, "custom-beta"))
}

func TestComputeFinalAnthropicBeta_APIKey_NoClientBetaInjectOff_ShouldNotSet(t *testing.T) {
	s := false
	final, ok := claude.ComputeFinalAnthropicBeta("apikey", false, "claude-sonnet-4-6", http.Header{}, []byte(`{}`), nil, s)
	require.False(t, ok, "API-key + 客户端未传 + InjectBetaForAPIKey 关 → 不应主动设置 anthropic-beta")
	require.Equal(t, "", final)
}

func TestComputeFinalAnthropicBeta_APIKeyHaiku_StillUsesAPIKeyBetas(t *testing.T) {
	s := true
	body := []byte(`{"model":"claude-haiku-4-5","thinking":{"type":"enabled"},"messages":[]}`)
	final, ok := claude.ComputeFinalAnthropicBeta("apikey", false, "claude-haiku-4-5", http.Header{}, body, nil, s)
	require.True(t, ok)
	require.Equal(t, claude.APIKeyHaikuBetaHeader, final)
	require.False(t, claude.AnthropicBetaTokensContains(final, claude.BetaOAuth))
	require.False(t, claude.AnthropicBetaTokensContains(final, claude.BetaClaudeCode))
}

// ============================================================================
// computeFinalCountTokensAnthropicBeta
// ============================================================================

func TestComputeFinalCountTokensAnthropicBeta_OAuthMimic_AlwaysIncludesContextManagement(t *testing.T) {
	// count_tokens mimic 继续注入完整 mimicry beta，并额外携带 token-counting beta。
	s := false
	final, ok := claude.ComputeFinalCountTokensAnthropicBeta("oauth", true, "claude-haiku-4-5", http.Header{}, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaContextManagement),
		"count_tokens + mimic Haiku 必须保留 context-management beta")
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaTokenCounting),
		"count_tokens 路径必须含 token-counting beta")
}

// 重构等价性回归：
// 原 main buildCountTokensRequest 在 count_tokens mimic 分支上不跳过白名单透传
// （与 messages mimic 不同），incomingBeta 取自客户端透传。重构后必须从 clientHeaders
// 拿同一个值并 merge，否则会丢失客户端 beta。

func TestComputeFinalCountTokensAnthropicBeta_OAuthMimic_PreservesClientBeta(t *testing.T) {
	s := false
	hdr := http.Header{}
	hdr.Set("anthropic-beta", "custom-experimental-beta,context-1m-2025-08-07")
	final, ok := claude.ComputeFinalCountTokensAnthropicBeta("oauth", true, "claude-haiku-4-5", hdr, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.True(t, claude.AnthropicBetaTokensContains(final, "custom-experimental-beta"),
		"count_tokens mimic 不同于 messages mimic：原代码会保留客户端透传的 beta")
	require.True(t, claude.AnthropicBetaTokensContains(final, "context-1m-2025-08-07"),
		"客户端透传的其他 beta token 同样需要保留")
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaContextManagement),
		"同时 FullClaudeCodeMimicryBetas 不打折扣")
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaTokenCounting),
		"同时补齐 token-counting beta")
}

// messages mimic 路径反向验证：原代码会跳过白名单透传，
// 客户端 beta 不会进入 mimic 计算。重构后 messages computeFinalAnthropicBeta
// mimic 分支依然不该使用 clientBeta。

func TestComputeFinalAnthropicBeta_OAuthMimic_IgnoresClientBetaExplicit(t *testing.T) {
	s := false
	hdr := http.Header{}
	hdr.Set("anthropic-beta", "custom-experimental-beta")
	final, ok := claude.ComputeFinalAnthropicBeta("oauth", true, "claude-sonnet-4-6", hdr, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.False(t, claude.AnthropicBetaTokensContains(final, "custom-experimental-beta"),
		"messages mimic 原代码跳过白名单透传 → 客户端 beta 不进入计算。"+
			"与 count_tokens mimic 是不同的设计，不能合并为同一函数。")
}

func TestComputeFinalCountTokensAnthropicBeta_OAuthTransparent_NoClientBetaInjectsDefault(t *testing.T) {
	// 真 CC 客户端透传 + 客户端未传 anthropic-beta → 用 CountTokensBetaHeader 兜底
	s := false
	final, ok := claude.ComputeFinalCountTokensAnthropicBeta("oauth", false, "claude-haiku-4-5", http.Header{}, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.Equal(t, claude.CountTokensBetaHeader, final)
	// CountTokensBetaHeader 不含 context-management beta
	require.False(t, claude.AnthropicBetaTokensContains(final, claude.BetaContextManagement))
}

func TestComputeFinalCountTokensAnthropicBeta_OAuthTransparent_AppendsBetaTokenCounting(t *testing.T) {
	s := false
	hdr := http.Header{}
	hdr.Set("anthropic-beta", "oauth-2025-04-20,context-management-2025-06-27")
	final, ok := claude.ComputeFinalCountTokensAnthropicBeta("oauth", false, "claude-sonnet-4-6", hdr, []byte(`{}`), nil, s)
	require.True(t, ok)
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaTokenCounting),
		"客户端未带 token-counting beta 时必须补齐")
	require.True(t, claude.AnthropicBetaTokensContains(final, claude.BetaContextManagement),
		"客户端带的 context-management beta 必须保留")
}

// ============================================================================
// normalizeClaudeOAuthRequestBody — 回归：context_management 补齐恢复原行为
// ============================================================================
//
// 重构后该函数不再按 model 名短路：thinking=enabled/adaptive 时补齐 context_management，
// 与 model 无关。strip 责任移交 sanitizeAnthropicBodyForBetaTokens（在
// buildUpstreamRequest 层按最终 beta header 执行）。

func TestNormalizeClaudeOAuthRequestBody_InjectsContextManagement_ThinkingEnabled(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","thinking":{"type":"enabled","budget_tokens":1000},"messages":[]}`)
	out, _ := claude.NormalizeClaudeOAuthRequestBody(body, "claude-sonnet-4-6", claude.ClaudeOAuthNormalizeOptions{})
	require.True(t, gjson.GetBytes(out, "context_management").Exists())
	require.Equal(t, "clear_thinking_20251015",
		gjson.GetBytes(out, "context_management.edits.0.type").String())
}

func TestNormalizeClaudeOAuthRequestBody_InjectsContextManagement_ThinkingAdaptive(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","thinking":{"type":"adaptive"},"messages":[]}`)
	out, _ := claude.NormalizeClaudeOAuthRequestBody(body, "claude-opus-4-7", claude.ClaudeOAuthNormalizeOptions{})
	require.True(t, gjson.GetBytes(out, "context_management").Exists())
}

func TestNormalizeClaudeOAuthRequestBody_HaikuStillInjects_StripDeferredToSanitize(t *testing.T) {
	// Haiku + thinking=enabled：normalize 阶段仍按 CLI mimicry 行为补齐字段；
	// 最终是否保留仍由 beta 能力对称的 sanitize 统一决定。
	body := []byte(`{"model":"claude-haiku-4-5","thinking":{"type":"enabled","budget_tokens":1000},"messages":[]}`)
	out, _ := claude.NormalizeClaudeOAuthRequestBody(body, "claude-haiku-4-5", claude.ClaudeOAuthNormalizeOptions{})
	require.True(t, gjson.GetBytes(out, "context_management").Exists(),
		"normalize 不再按 model 名短路；strip 责任移交 sanitize 层")
}

func TestNormalizeClaudeOAuthRequestBody_PreservesClientContextManagement(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","context_management":{"edits":[{"type":"custom_strategy"}]},"thinking":{"type":"enabled","budget_tokens":1000},"messages":[]}`)
	out, _ := claude.NormalizeClaudeOAuthRequestBody(body, "claude-opus-4-7", claude.ClaudeOAuthNormalizeOptions{})
	require.Equal(t, "custom_strategy",
		gjson.GetBytes(out, "context_management.edits.0.type").String(),
		"客户端透传的 context_management 内容必须原样保留")
}

func TestNormalizeClaudeOAuthRequestBody_NoThinking_NoInject(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[]}`)
	out, _ := claude.NormalizeClaudeOAuthRequestBody(body, "claude-sonnet-4-6", claude.ClaudeOAuthNormalizeOptions{})
	require.False(t, gjson.GetBytes(out, "context_management").Exists())
}

func TestNormalizeClaudeOAuthRequestBody_HaikuShortModelStillNormalizesToDatedID(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","messages":[]}`)
	out, modelID := claude.NormalizeClaudeOAuthRequestBody(body, "claude-haiku-4-5", claude.ClaudeOAuthNormalizeOptions{})
	require.Equal(t, "claude-haiku-4-5-20251001", modelID)
	require.Equal(t, "claude-haiku-4-5-20251001", gjson.GetBytes(out, "model").String())
}
