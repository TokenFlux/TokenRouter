// 本文件组合 Anthropic wire Header，不读取业务服务或配置。
package anthropic

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// GetBetaHeader 处理anthropic-beta header
// 对于OAuth账号，需要确保包含oauth-2025-04-20
func GetBetaHeader(modelID string, clientBetaHeader string) string {
	// 如果客户端传了anthropic-beta
	if clientBetaHeader != "" {
		// 已包含oauth beta则直接返回
		if strings.Contains(clientBetaHeader, BetaOAuth) {
			return clientBetaHeader
		}

		// 需要添加oauth beta
		parts := strings.Split(clientBetaHeader, ",")
		for i, p := range parts {
			parts[i] = strings.TrimSpace(p)
		}

		// 在claude-code-20250219后面插入oauth beta
		claudeCodeIdx := -1
		for i, p := range parts {
			if p == BetaClaudeCode {
				claudeCodeIdx = i
				break
			}
		}

		if claudeCodeIdx >= 0 {
			// 在claude-code后面插入
			newParts := make([]string, 0, len(parts)+1)
			newParts = append(newParts, parts[:claudeCodeIdx+1]...)
			newParts = append(newParts, BetaOAuth)
			newParts = append(newParts, parts[claudeCodeIdx+1:]...)
			return strings.Join(newParts, ",")
		}

		// 没有claude-code，放在第一位
		return BetaOAuth + "," + clientBetaHeader
	}

	// OAuth 真实客户端透传且客户端没传 beta 时，根据模型生成默认值。
	// Haiku 的透传默认值不补 claude-code beta；mimic 路径不会调用本分支。
	if strings.Contains(strings.ToLower(modelID), "haiku") {
		return HaikuBetaHeader
	}

	return DefaultBetaHeader
}
func RequestNeedsBetaFeatures(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if tools.Exists() && tools.IsArray() && len(tools.Array()) > 0 {
		return true
	}
	thinkingType := gjson.GetBytes(body, "thinking.type").String()
	if strings.EqualFold(thinkingType, "enabled") || strings.EqualFold(thinkingType, "adaptive") {
		return true
	}
	return false
}
func DefaultAPIKeyBetaHeader(body []byte) string {
	modelID := gjson.GetBytes(body, "model").String()
	if strings.Contains(strings.ToLower(modelID), "haiku") {
		return APIKeyHaikuBetaHeader
	}
	return APIKeyBetaHeader
}
func ApplyClaudeOAuthHeaderDefaults(req *http.Request) {
	if req == nil {
		return
	}
	if GetHeaderRaw(req.Header, "Accept") == "" {
		SetHeaderRaw(req.Header, "Accept", "application/json")
	}
	for key, value := range DefaultHeaders {
		if value == "" {
			continue
		}
		if GetHeaderRaw(req.Header, key) == "" {
			SetHeaderRaw(req.Header, ResolveWireCasing(key), value)
		}
	}
}
func MergeAnthropicBeta(required []string, incoming string) string {
	seen := make(map[string]struct{}, len(required)+8)
	out := make([]string, 0, len(required)+8)

	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	for _, r := range required {
		add(r)
	}
	for _, p := range strings.Split(incoming, ",") {
		add(p)
	}
	return strings.Join(out, ",")
}
func MergeAnthropicBetaDropping(required []string, incoming string, drop map[string]struct{}) string {
	merged := MergeAnthropicBeta(required, incoming)
	if merged == "" || len(drop) == 0 {
		return merged
	}
	out := make([]string, 0, 8)
	for _, p := range strings.Split(merged, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := drop[p]; ok {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, ",")
}

// ComputeFinalAnthropicBeta 计算发往上游的最终 anthropic-beta header 值。
//
// 设计动机：将原本在 buildUpstreamRequest 内联在一起、依赖 req.Header 的
// anthropic-beta 计算逻辑抽成纯函数。这样调用方可以在 NewRequest 之前
// 就提前拿到最终 beta header，进而能按它对 body 做能力维度 sanitize，避免
// 之前因顺序依赖导致的能力维度 sanitize 无法部署问题。新版 Claude Code CLI
// 已取消 cch 签名字段，本路径不再对 body 做 CCH 签名。
//
// 返回 (value, shouldSet)：
//   - shouldSet=false 意为“不主动设置 anthropic-beta header”，与原代码“
//     API-key 账号 + 客户端未传 anthropic-beta + InjectBetaForAPIKey 未开启或
//     RequestNeedsBetaFeatures=false”的行为对齐。
//   - shouldSet=true 时 value 可能为空字符串（例如客户端透传的 beta 被 dropSet
//     全部过滤掉），这与原代码中 SetHeaderRaw 的结果一致。
//
// clientHeaders 是客户端原始 HTTP header（通常为 c.Request.Header）；nil 时按“客户端
// 未传”处理。body 是已经 metadata 重写 / billing version sync 之后但未 sanitize 上游
// 不兼容字段之前的版本。
func ComputeFinalAnthropicBeta(
	tokenType string,
	mimicClaudeCode bool,
	modelID string,
	clientHeaders http.Header,
	body []byte,
	effectiveDropSet map[string]struct{},
	injectAPIKeyBeta bool,
) (string, bool) {
	clientBeta := ""
	if clientHeaders != nil {
		clientBeta = GetHeaderRaw(clientHeaders, "anthropic-beta")
	}

	if tokenType == "oauth" {
		if mimicClaudeCode {
			// mimic 路径跳过白名单透传，incomingBeta 始终为空；所有模型都必须
			// 携带完整 Claude Code beta 集合，避免 Haiku 被识别为第三方客户端。
			return MergeAnthropicBetaDropping(FullClaudeCodeMimicryBetas(), "", effectiveDropSet), true
		}
		// 真 Claude Code 客户端透传路径
		return StripBetaTokensWithSet(GetBetaHeader(modelID, clientBeta), effectiveDropSet), true
	}

	// API-key 账号
	if clientBeta != "" {
		return StripBetaTokensWithSet(clientBeta, effectiveDropSet), true
	}
	if injectAPIKeyBeta {
		if RequestNeedsBetaFeatures(body) {
			if beta := DefaultAPIKeyBetaHeader(body); beta != "" {
				return beta, true
			}
		}
	}
	return "", false
}

// ComputeFinalCountTokensAnthropicBeta 是 count_tokens 路径上 anthropic-beta header 的
// 计算纯函数。语义与 ComputeFinalAnthropicBeta 对齐，但备份了 count_tokens 独有的
// 两条特殊规则：
//
//   - OAuth mimic：requiredBetas 为 FullClaudeCodeMimicryBetas + BetaTokenCounting；
//     count_tokens 另外保留客户端 beta，而 messages mimic 会忽略客户端 beta。
//   - OAuth 透传 + 客户端未传 anthropic-beta：补齐 CountTokensBetaHeader
//   - OAuth 透传 + 客户端传了：补齐 BetaTokenCounting（如果未含）
//
// 返回语义同 ComputeFinalAnthropicBeta。
func ComputeFinalCountTokensAnthropicBeta(
	tokenType string,
	mimicClaudeCode bool,
	modelID string,
	clientHeaders http.Header,
	body []byte,
	effectiveDropSet map[string]struct{},
	injectAPIKeyBeta bool,
) (string, bool) {
	clientBeta := ""
	if clientHeaders != nil {
		clientBeta = GetHeaderRaw(clientHeaders, "anthropic-beta")
	}

	if tokenType == "oauth" {
		if mimicClaudeCode {
			// 与原代码严格等价：original buildCountTokensRequest 在 count_tokens mimic
			// 分支上**不**会跳过白名单透传（与 messages mimic 路径不同），所以
			// incomingBeta = req.Header[anthropic-beta] = 客户端透传过来的 client beta。
			// 重构后直接从 clientHeaders 拿同一个值，保持行为一致。
			requiredBetas := append(FullClaudeCodeMimicryBetas(), BetaTokenCounting)
			return MergeAnthropicBetaDropping(requiredBetas, clientBeta, effectiveDropSet), true
		}
		if clientBeta == "" {
			return CountTokensBetaHeader, true
		}
		beta := GetBetaHeader(modelID, clientBeta)
		if !strings.Contains(beta, BetaTokenCounting) {
			beta = beta + "," + BetaTokenCounting
		}
		return StripBetaTokensWithSet(beta, effectiveDropSet), true
	}

	// API-key 账号
	if clientBeta != "" {
		return StripBetaTokensWithSet(clientBeta, effectiveDropSet), true
	}
	if injectAPIKeyBeta {
		if RequestNeedsBetaFeatures(body) {
			if beta := DefaultAPIKeyBetaHeader(body); beta != "" {
				return beta, true
			}
		}
	}
	return "", false
}

// StripBetaTokens removes the given beta tokens from a comma-separated header value.
func StripBetaTokens(header string, tokens []string) string {
	if header == "" || len(tokens) == 0 {
		return header
	}
	return StripBetaTokensWithSet(header, BuildBetaTokenSet(tokens))
}
func StripBetaTokensWithSet(header string, drop map[string]struct{}) string {
	if header == "" || len(drop) == 0 {
		return header
	}
	parts := strings.Split(header, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := drop[p]; ok {
			continue
		}
		out = append(out, p)
	}
	if len(out) == len(parts) {
		return header // no change, avoid allocation
	}
	return strings.Join(out, ",")
}

// MergeDropSets merges the static DefaultDroppedBetasSet with dynamic policy filter tokens.
// Returns DefaultDroppedBetasSet directly when policySet is empty (zero allocation).
func MergeDropSets(policySet map[string]struct{}, extra ...string) map[string]struct{} {
	if len(policySet) == 0 && len(extra) == 0 {
		return DefaultDroppedBetasSet
	}
	m := make(map[string]struct{}, len(DefaultDroppedBetasSet)+len(policySet)+len(extra))
	for t := range DefaultDroppedBetasSet {
		m[t] = struct{}{}
	}
	for t := range policySet {
		m[t] = struct{}{}
	}
	for _, t := range extra {
		m[t] = struct{}{}
	}
	return m
}

// DroppedBetaSet returns DroppedBetas as a set, with optional extra tokens.
func DroppedBetaSet(extra ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(DefaultDroppedBetasSet)+len(extra))
	for t := range DefaultDroppedBetasSet {
		m[t] = struct{}{}
	}
	for _, t := range extra {
		m[t] = struct{}{}
	}
	return m
}

// ContainsBetaToken checks if a comma-separated header value contains the given token.
func ContainsBetaToken(header, token string) bool {
	if header == "" || token == "" {
		return false
	}
	for _, p := range strings.Split(header, ",") {
		if strings.TrimSpace(p) == token {
			return true
		}
	}
	return false
}
func FilterBetaTokens(tokens []string, filterSet map[string]struct{}) []string {
	if len(tokens) == 0 || len(filterSet) == 0 {
		return tokens
	}
	kept := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if _, filtered := filterSet[token]; !filtered {
			kept = append(kept, token)
		}
	}
	return kept
}
func BuildBetaTokenSet(tokens []string) map[string]struct{} {
	m := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		if t == "" {
			continue
		}
		m[t] = struct{}{}
	}
	return m
}

// ApplyClaudeCodeMimicHeaders forces "Claude Code-like" request headers.
// This mirrors opencode-anthropic-auth behavior: do not trust downstream
// headers when using Claude Code-scoped OAuth credentials.
func ApplyClaudeCodeMimicHeaders(req *http.Request, isStream bool) {
	if req == nil {
		return
	}
	// Start with the standard defaults (fill missing).
	ApplyClaudeOAuthHeaderDefaults(req)
	// Then force key headers to match Claude Code fingerprint regardless of what the client sent.
	// 使用 ResolveWireCasing 确保 key 与真实 wire format 一致（如 "x-app" 而非 "X-App"）
	for key, value := range DefaultHeaders {
		if value == "" {
			continue
		}
		SetHeaderRaw(req.Header, ResolveWireCasing(key), value)
	}
	// Real Claude CLI uses Accept: application/json (even for streaming).
	SetHeaderRaw(req.Header, "Accept", "application/json")
	if isStream {
		SetHeaderRaw(req.Header, "x-stainless-helper-method", "stream")
	}
	// Real Claude CLI 每个请求都会生成一个新的 UUID 放在 x-client-request-id。
	// 上游会以此作为会话/请求指纹的一部分，缺失或重复都可能触发第三方判定。
	if GetHeaderRaw(req.Header, "x-client-request-id") == "" {
		SetHeaderRaw(req.Header, "x-client-request-id", uuid.NewString())
	}
}

// 缺省 drop 集合只初始化一次，调用方需要扩展时使用副本。
var DefaultDroppedBetasSet = BuildBetaTokenSet(DroppedBetas)
