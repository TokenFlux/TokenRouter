package service

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	context "context"
	"strconv"

	strings "strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// replaceModelInBody 替换请求体中的model字段
// 优先使用定点修改，尽量保持客户端原始字段顺序。
func (s *GatewayService) replaceModelInBody(body []byte, newModel string) []byte {
	return ReplaceModelInBody(body, newModel)
}

type claudeOAuthNormalizeOptions = claude.ClaudeOAuthNormalizeOptions

func sanitizeSystemText(text string) string { return claude.SanitizeSystemText(text) }

func normalizeClaudeOAuthRequestBody(body []byte, modelID string, opts claudeOAuthNormalizeOptions) ([]byte, string) {
	return claude.NormalizeClaudeOAuthRequestBody(body, modelID, opts)
}

func (s *GatewayService) buildOAuthMetadataUserID(parsed *ParsedRequest, account *Account, fp *Fingerprint) string {
	if parsed == nil || account == nil {
		return ""
	}
	if parsed.MetadataUserID != "" {
		return ""
	}

	userID := strings.TrimSpace(account.GetClaudeUserID())
	if userID == "" && fp != nil {
		userID = fp.ClientID
	}
	if userID == "" {
		// Fall back to a random, well-formed client id so we can still satisfy
		// Claude Code OAuth requirements when account metadata is incomplete.
		userID = generateClientID()
	}

	// session_id 用"会话级稳定种子"派生（账号 + 客户端区分因子 + 首条 user 文本）：
	// 随对话在尾部追加 messages 时保持不变，贴近真实 CC 进程级稳定的 session_id。
	// 不复用 GenerateSessionHash —— 后者是粘性路由键、按设计逐轮变化（见其测试）。
	var firstUserText string
	if parsed.Body != nil {
		firstUserText = extractFirstUserText(parsed.Body.Bytes())
	}
	seed := buildStableSessionSeed(account.ID, sessionContextDiscriminator(parsed.SessionContext), firstUserText)
	sessionID := generateSessionUUID(seed)

	// 根据指纹 UA 版本选择输出格式
	var uaVersion string
	if fp != nil {
		uaVersion = ExtractCLIVersion(fp.UserAgent)
	}
	accountUUID := strings.TrimSpace(account.GetExtraString("account_uuid"))
	return FormatMetadataUserID(userID, accountUUID, sessionID, uaVersion)
}

// applyClaudeCodeOAuthMimicryToBody 将"非 Claude Code 客户端 + Claude OAuth 账号"
// 路径上原本只在 /v1/messages 里做的完整伪装应用到任意 body 上。
//
// 这是 /v1/messages 主路径上 rewriteSystemForNonClaudeCode +
// normalizeClaudeOAuthRequestBody 流程的通用版，供 OpenAI 协议兼容层
// (ForwardAsChatCompletions / ForwardAsResponses) 复用。
//
// 未抽离之前，OpenAI 协议兼容层仅做 injectClaudeCodePrompt（前置追加），
// 而仓内 /v1/messages 路径自己的注释明确说过"仅前置追加无法通过 Anthropic
// 第三方检测"；那条注释就是本函数存在的根因。
//
// 参数：
//   - ctx / c：用于读取指纹和 gateway settings；c 可为 nil（如 count_tokens）。
//   - account：必须是 OAuth 账号，且调用方已判断不是 Claude Code 客户端。
//   - body：已经 marshal 成 Anthropic /v1/messages 格式的请求体。
//   - systemRaw：body 中原始 system 字段（用于判断是否需要 rewrite）。
//   - model：最终会发给上游的模型 ID（用于模型规范化 + metadata 版本选择）。
//
// 返回：改写后的 body。即使中间任何一步失败，也会退化成原 body（不会 panic）。
func (s *GatewayService) applyClaudeCodeOAuthMimicryToBody(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	systemRaw any,
	model string,
) []byte {
	adapter := &mimicExecutionAdapter{messageExecutionAdapter: newMessageExecutionAdapter(s, c, account), systemRaw: systemRaw}
	return forwardcore.Mimic(ctx, adapter, account != nil && account.IsOAuth(), body, model)
}

// buildOAuthMetadataUserIDFromBody 是 buildOAuthMetadataUserID 的变体，
// 适用于调用方手上没有 ParsedRequest 的场景（如 OpenAI 协议兼容层）。
//
// 与 buildOAuthMetadataUserID 的唯一区别：
//   - session hash 从 body 本体按同样规则重算，而不是读取 ParsedRequest 缓存值。
//   - 如果 body 里已经存在 metadata.user_id，则返回空（由 ensureClaudeOAuthMetadataUserID
//     自行决定是否覆盖）。
func (s *GatewayService) buildOAuthMetadataUserIDFromBody(
	ctx context.Context,
	account *Account,
	fp *Fingerprint,
	body []byte,
) string {
	_ = ctx
	if account == nil {
		return ""
	}
	if existing := gjson.GetBytes(body, "metadata.user_id").String(); existing != "" {
		return ""
	}

	userID := strings.TrimSpace(account.GetClaudeUserID())
	if userID == "" && fp != nil {
		userID = fp.ClientID
	}
	if userID == "" {
		userID = generateClientID()
	}

	// 与 buildOAuthMetadataUserID 一致：用会话级稳定种子，避免整 body 哈希导致
	// 每轮（甚至每个 token 变化）都重算出不同的 session_id。
	var clientDiscriminator string
	if fp != nil {
		clientDiscriminator = fp.ClientID
	}
	seed := buildStableSessionSeed(account.ID, clientDiscriminator, extractFirstUserText(body))
	sessionID := generateSessionUUID(seed)

	var uaVersion string
	if fp != nil {
		uaVersion = ExtractCLIVersion(fp.UserAgent)
	}
	accountUUID := strings.TrimSpace(account.GetExtraString("account_uuid"))
	return FormatMetadataUserID(userID, accountUUID, sessionID, uaVersion)
}

func buildStableSessionSeed(accountID int64, clientDiscriminator, firstUserText string) string {
	return claude.BuildStableSessionSeed(accountID, clientDiscriminator, firstUserText)
}

// sessionContextDiscriminator 把请求上下文（客户端 IP / 归一化 UA / API Key ID）拼成
// 一个跨客户端的区分因子，避免不同用户的相同首条消息派生出相同 session_id。
func sessionContextDiscriminator(sc *SessionContext) string {
	if sc == nil {
		return ""
	}
	return sc.ClientIP + ":" + NormalizeSessionUserAgent(sc.UserAgent) + ":" + strconv.FormatInt(sc.APIKeyID, 10)
}

// GenerateSessionUUID creates a deterministic UUID4 from a seed string.
func GenerateSessionUUID(seed string) string {
	return generateSessionUUID(seed)
}

func generateSessionUUID(seed string) string { return upstream.GenerateSessionUUID(seed) }

func normalizeSystemParam(system any) any { return claude.NormalizeSystemParam(system) }

func systemIncludesClaudeCodePrompt(system any) bool {
	return claude.SystemIncludesClaudeCodePrompt(system)
}

func injectClaudeCodePrompt(body []byte, system any) []byte {
	return claude.InjectClaudeCodePrompt(body, system)
}

func rewriteSystemForNonClaudeCode(body []byte, system any) []byte {
	return claude.RewriteSystemForNonClaudeCode(body, system)
}

func rewriteSystemForNonClaudeCodeWithPrompt(body []byte, system any, expansionPrompt string) []byte {
	return claude.RewriteSystemForNonClaudeCodeWithPrompt(body, system, expansionPrompt)
}

func claudeOAuthSystemPromptBlocksForModel(model, configured string) string {
	return claude.ClaudeOAuthSystemPromptBlocksForModel(model, configured)
}

func ValidateClaudeOAuthSystemPromptBlocksConfig(raw string) error {
	return claude.ValidateClaudeOAuthSystemPromptBlocksConfig(raw)
}

func rewriteSystemForNonClaudeCodeWithPromptBlocks(body []byte, system any, expansionPrompt string, blocksConfig string) []byte {
	return claude.RewriteSystemForNonClaudeCodeWithPromptBlocks(body, system, expansionPrompt, blocksConfig)
}

func enforceCacheControlLimit(body []byte) []byte { return claude.EnforceCacheControlLimit(body) }

func injectAnthropicCacheControlTTL1h(body []byte) []byte {
	return claude.InjectAnthropicCacheControlTTL1h(body)
}

func (s *GatewayService) shouldInjectAnthropicCacheTTL1h(ctx context.Context, account *Account) bool {
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() || s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.IsAnthropicCacheTTL1hInjectionEnabled(ctx)
}

// shouldNormalizeClientDateline reports whether the request body's client
// dateline should be normalized before forwarding to Anthropic. The switch is
// scoped to Anthropic OAuth/SetupToken accounts only; API-Key accounts and
// non-Anthropic platforms bypass this step entirely.
func (s *GatewayService) shouldNormalizeClientDateline(ctx context.Context, account *Account) bool {
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() || s == nil || s.settingService == nil {
		return false
	}
	return s.settingService.IsClientDatelineNormalizationEnabled(ctx)
}

// normalizeClientDatelineIfEnabled applies dateline normalization to body when
// the switch is on and the account qualifies. Returns (nextBody, true) only
// when the body actually changed; otherwise returns (nil, false) so callers
// can skip the writeback.
func (s *GatewayService) normalizeClientDatelineIfEnabled(ctx context.Context, account *Account, body []byte) ([]byte, bool) {
	if !s.shouldNormalizeClientDateline(ctx, account) {
		return nil, false
	}
	next, _, changed := claude.NormalizeDateline(body)
	if !changed {
		return nil, false
	}
	return next, true
}

func (s *GatewayService) claudeOAuthSystemPromptInjectionSettings(ctx context.Context) (bool, string, string) {
	if s == nil || s.settingService == nil {
		return true, "", ""
	}
	return s.settingService.GetClaudeOAuthSystemPromptInjectionSettings(ctx)
}

func systemHasClaudeCodeBillingAttribution(body []byte) bool {
	return claude.SystemHasClaudeCodeBillingAttribution(body)
}

func isProxiedClaudeCodeRequest(body []byte, metadataUserID string) bool {
	return claude.IsProxiedClaudeCodeRequest(body, metadataUserID)
}
