package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func (s *OpenAIGatewayService) validateUpstreamBaseURL(raw string) (string, error) {
	normalized, err := s.validateOutboundURL(raw)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return normalized, nil
}

// validateOutboundURL 按安全配置校验网关主动连接的 URL。
func (s *OpenAIGatewayService) validateOutboundURL(raw string) (string, error) {
	if s == nil || s.cfg == nil {
		return egress.ValidateURLFormat(raw, false)
	}
	if !s.cfg.Security.URLAllowlist.Enabled {
		return egress.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
	}
	return egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
}

// isOfficialOpenAIModelsBaseURL 只识别官方 OpenAI 主机，避免兼容中继误用官方字段语义。
func isOfficialOpenAIModelsBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && strings.EqualFold(u.Hostname(), "api.openai.com")
}

// shouldPreserveOpenAIResponsesNoneReasoningEffort 判断请求是否仍需保留官方目录的 none 占位值。
func shouldPreserveOpenAIResponsesNoneReasoningEffort(account *Account) bool {
	if account == nil {
		return false
	}
	if account.IsOpenAIPassthroughEnabled() {
		return true
	}
	if account.IsOpenAIOAuthLike() {
		return true
	}
	if !account.IsOpenAIApiKey() {
		return false
	}
	baseURL := strings.TrimSpace(account.GetCredential("base_url"))
	return baseURL == "" || isOfficialOpenAIModelsBaseURL(baseURL)
}

// filterOpenAIResponsesNoneReasoningEffortForAccount 删除兼容上游不应接收的目录占位值。
// 官方 OpenAI 请求保留 none，避免改变其原生请求语义。
func filterOpenAIResponsesNoneReasoningEffortForAccount(account *Account, body []byte) ([]byte, error) {
	if len(body) == 0 || shouldPreserveOpenAIResponsesNoneReasoningEffort(account) {
		return body, nil
	}

	out := body
	for _, path := range []string{"reasoning.effort", "reasoning_effort"} {
		effort := gjson.GetBytes(out, path)
		if effort.Type != gjson.String || !strings.EqualFold(strings.TrimSpace(effort.String()), "none") {
			continue
		}
		next, err := sjson.DeleteBytes(out, path)
		if err != nil {
			return body, fmt.Errorf("strip %s none placeholder: %w", path, err)
		}
		out = next
	}
	if reasoning := gjson.GetBytes(out, "reasoning"); reasoning.IsObject() && len(reasoning.Map()) == 0 {
		next, err := sjson.DeleteBytes(out, "reasoning")
		if err != nil {
			return body, fmt.Errorf("strip empty reasoning object: %w", err)
		}
		out = next
	}
	return out, nil
}

// deleteOpenAIResponsesNoneReasoningEffortFromObject 删除 WS bridge 中的 none 占位字段。
func deleteOpenAIResponsesNoneReasoningEffortFromObject(account *Account, body map[string]any) {
	if body == nil || shouldPreserveOpenAIResponsesNoneReasoningEffort(account) {
		return
	}
	if effort, ok := body["reasoning_effort"].(string); ok && strings.EqualFold(strings.TrimSpace(effort), "none") {
		delete(body, "reasoning_effort")
	}
	reasoning, ok := body["reasoning"].(map[string]any)
	if !ok {
		return
	}
	if effort, ok := reasoning["effort"].(string); ok && strings.EqualFold(strings.TrimSpace(effort), "none") {
		delete(reasoning, "effort")
	}
	if len(reasoning) == 0 {
		delete(body, "reasoning")
	}
}

// IsOpenAIResponsesCompactPath 判断请求是否指向旧版 /responses/compact 端点或其可转发子路径。
func IsOpenAIResponsesCompactPath(c *gin.Context) bool {
	return isOpenAIResponsesCompactPath(c)
}

func IsOpenAIResponsesCompactPathForTest(c *gin.Context) bool {
	return IsOpenAIResponsesCompactPath(c)
}

func OpenAICompactSessionSeedKeyForTest() string {
	return openAICompactSessionSeedKey
}

func isOpenAIResponsesCompactPath(c *gin.Context) bool {
	suffix := strings.TrimSpace(gatewayhttp.OpenAIResponsesRequestPathSuffix(c))
	return suffix == "/compact" || strings.HasPrefix(suffix, "/compact/")
}

func normalizeOpenAICodexCompactReasoningEffortForAccount(c *gin.Context, account *Account, body []byte) ([]byte, bool, error) {
	if account == nil || !account.IsOpenAIOAuthLike() || !isOpenAIResponsesCompactPath(c) {
		return body, false, nil
	}

	requestedModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	effectiveModel := account.GetMappedModel(requestedModel)
	return normalizeOpenAICodexCompactReasoningEffort(body, effectiveModel)
}

// normalizeOpenAICodexCompactReasoningEffort 将 GPT-5.6 compact 暂不接受的
// max 档位降级为 xhigh，并保留 reasoning 下的其他字段。
func normalizeOpenAICodexCompactReasoningEffort(body []byte, effectiveModel string) ([]byte, bool, error) {
	if !modelidentity.IsGPT56(effectiveModel) ||
		!strings.EqualFold(strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String()), "max") {
		return body, false, nil
	}

	// Codex Ultra 在客户端编排层会下发 max；ChatGPT compact 端点目前只接受到
	// xhigh。这里只降级 OpenAI OAuth 的 GPT-5.6 compact 子请求，普通 Responses、
	// API Key 请求和其他平台的 OAuth 请求保留 max。
	normalized, err := sjson.SetBytes(body, "reasoning.effort", "xhigh")
	if err != nil {
		return body, false, fmt.Errorf("normalize codex compact reasoning effort: %w", err)
	}
	return normalized, true, nil
}

func resolveOpenAICompactSessionID(c *gin.Context) string {
	if c != nil {
		if sessionID := strings.TrimSpace(c.GetHeader("session_id")); sessionID != "" {
			return sessionID
		}
		if conversationID := strings.TrimSpace(c.GetHeader("conversation_id")); conversationID != "" {
			return conversationID
		}
		if seed, ok := c.Get(openAICompactSessionSeedKey); ok {
			if seedStr, ok := seed.(string); ok && strings.TrimSpace(seedStr) != "" {
				return strings.TrimSpace(seedStr)
			}
		}
	}
	return uuid.NewString()
}

func (s *OpenAIGatewayService) replaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	return s09wire.ReplaceModelInResponseBody(body, fromModel, toModel)
}

func deriveOpenAIReasoningEffortFromModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}

	modelID := strings.TrimSpace(model)
	if strings.Contains(modelID, "/") {
		parts := strings.Split(modelID, "/")
		modelID = parts[len(parts)-1]
	}

	parts := strings.FieldsFunc(strings.ToLower(modelID), func(r rune) bool {
		switch r {
		case '-', '_', ' ':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return ""
	}

	// 国产模型的 max 是显式请求档位，不把同名模型后缀推导成 usage 档位。
	if parts[len(parts)-1] == "max" && !capability.IsOpenAIModelAtLeastVersion(modelID, 5, 6) {
		return ""
	}
	return capability.NormalizeRecordedOpenAIEffortForModel(parts[len(parts)-1], modelID)
}

// deriveOpenAIReasoningEffortFromModelCandidates 依次对每个候选模型做后缀推导，
// 返回第一个非空结果。
func deriveOpenAIReasoningEffortFromModelCandidates(models []string) string {
	for _, model := range models {
		if value := deriveOpenAIReasoningEffortFromModel(model); value != "" {
			return value
		}
	}
	return ""
}

// 旧 HTTP 解码入口只持有目标视图；字段扫描和补丁算法只有一份。
type openAIRequestView struct{ requeststate.OpenAIRequestView }

func newOpenAIRequestView(body []byte) openAIRequestView {
	return openAIRequestView{OpenAIRequestView: requeststate.NewOpenAIRequestView(body)}
}
func (v openAIRequestView) Decode(c *gin.Context) (map[string]any, error) {
	return getOpenAIRequestBodyMap(c, v.Bytes())
}

func extractOpenAIRequestMetaFromBody(body []byte) (model string, stream bool, promptCacheKey string) {
	view := newOpenAIRequestView(body)
	return view.Model, view.Stream, view.PromptCacheKey
}

func normalizeOpenAIResponsesWebSocketCompatibilityBody(body []byte, account *Account, responsesLite bool) ([]byte, bool, error) {
	if account == nil || !account.IsOpenAI() {
		return body, false, nil
	}
	normalized := body
	changed := false
	if account.IsOpenAIOAuthLike() {
		var err error
		normalized, changed, err = normalizeOpenAIResponsesLegacyIngress(body)
		if err != nil {
			return body, false, err
		}
	}
	if next, normalizedReasoningContent, err := openai.NormalizeOpenAIResponsesReasoningContentReplay(normalized); err != nil {
		return body, false, err
	} else if normalizedReasoningContent {
		normalized = next
		changed = true
	}
	if account.IsOpenAIApiKey() {
		if next, normalizedParallel, err := openai.NormalizeOpenAIParallelToolCallsWithoutTools(normalized, responsesLite); err != nil {
			return body, false, err
		} else if normalizedParallel {
			normalized = next
			changed = true
		}
		if next, normalizedReasoning, err := openai.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(normalized, false); err != nil {
			return body, false, err
		} else if normalizedReasoning {
			normalized = next
			changed = true
		}
	}
	if sanitized, idsChanged, err := sanitizeOpenAIResponsesInputItemIDs(normalized); err != nil {
		return body, false, fmt.Errorf("sanitize websocket Responses input item IDs: %w", err)
	} else if idsChanged {
		normalized = sanitized
		changed = true
	}
	if account != nil && account.IsOpenAI() && account.IsOAuth() {
		if reasoningBody, reasoningChanged, err := openai.NormalizeOpenAIResponsesReasoningMode(normalized); err != nil {
			return body, false, err
		} else if reasoningChanged {
			normalized = reasoningBody
			changed = true
		}
	}
	if account != nil && account.IsOpenAIOAuthLike() {
		oauthBody, oauthChanged, err := s09wire.NormalizeOpenAIOAuthResponsesCompatibilityBody(normalized)
		if err != nil {
			return body, false, err
		}
		normalized = oauthBody
		changed = changed || oauthChanged
		for _, field := range openAIChatGPTInternalUnsupportedFields {
			if !gjson.GetBytes(normalized, field).Exists() {
				continue
			}
			next, deleteErr := sjson.DeleteBytes(normalized, field)
			if deleteErr != nil {
				return body, false, fmt.Errorf("normalize websocket body delete %s: %w", field, deleteErr)
			}
			normalized = next
			changed = true
		}
	}
	needsOrphanCleanup := account != nil && account.IsOpenAIOAuthLike() &&
		gjson.GetBytes(normalized, "input").IsArray()
	if needsOrphanCleanup || openAIResponsesInputMayNeedTruncation(normalized) {
		var reqBody map[string]any
		if err := wirejson.DecodeUseNumber(normalized, &reqBody); err != nil {
			return body, false, fmt.Errorf("normalize websocket Responses body: %w", err)
		}
		mapChanged := false
		if needsOrphanCleanup {
			if input, ok := reqBody["input"].([]any); ok && sanitizeOpenAIResponsesOrphanToolOutputs(
				reqBody,
				input,
				strings.TrimSpace(openai.FirstNonEmptyString(reqBody["previous_response_id"])) != "",
			) {
				mapChanged = true
			}
		}
		if truncateOpenAIResponsesInputText(reqBody) {
			mapChanged = true
		}
		if mapChanged {
			next, err := wirejson.Marshal(reqBody)
			if err != nil {
				return body, false, fmt.Errorf("serialize normalized websocket Responses body: %w", err)
			}
			normalized = next
			changed = true
		}
	}
	if schemaBody, schemaChanged, err := openai.NormalizeOpenAIResponseFormatSchemasBody(normalized); err != nil {
		return body, false, err
	} else if schemaChanged {
		normalized = schemaBody
		changed = true
	}
	if openAIRequestBodyImageGenerationToolNeedsNormalization(normalized) {
		var reqBody map[string]any
		if err := json.Unmarshal(normalized, &reqBody); err != nil {
			return body, false, fmt.Errorf("normalize websocket image tool body: %w", err)
		}
		if openai.NormalizeOpenAIResponsesImageGenerationTools(reqBody) {
			next, err := json.Marshal(reqBody)
			if err != nil {
				return body, false, fmt.Errorf("serialize normalized websocket image tool body: %w", err)
			}
			normalized = next
			changed = true
		}
	}
	if account != nil {
		if schemaBody, schemaChanged, err := sanitizeOpenAIResponsesToolSchemasForPlatform(normalized, account.Platform); err != nil {
			return body, false, fmt.Errorf("normalize websocket tool schemas: %w", err)
		} else if schemaChanged {
			normalized = schemaBody
			changed = true
		}
	}
	// Keep this last: earlier compatibility passes may filter or rebuild input.
	// Remote compaction v2 requires one trigger as the final input item.
	if triggerBody, triggerChanged, err := NormalizeCompactionTriggerInputOrder(normalized); err != nil {
		return body, false, fmt.Errorf("normalize websocket compaction trigger order: %w", err)
	} else if triggerChanged {
		normalized = triggerBody
		changed = true
	}
	return normalized, changed, nil
}

// extractOpenAIReasoningEffortFromBody 按优先级传入模型候选（如 upstreamModel,
// billingModel, originalModel）。显式 effort 只做格式归一化并如实记录；body 未携带
// effort 时才从模型后缀推导并执行模型能力判断。OAuth 的 normalizeCodexModel 会
// 剥掉 upstreamModel 的 effort 后缀，因此推导时必须保留原始模型候选。
func extractOpenAIReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	reasoningEffort := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if reasoningEffort == "" {
		reasoningEffort = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if reasoningEffort != "" {
		normalized := s09wire.NormalizeRecordedReasoningEffort(reasoningEffort)
		if normalized == "" {
			return nil
		}
		return &normalized
	}

	value := deriveOpenAIReasoningEffortFromModelCandidates(modelCandidates)
	if value == "" {
		return nil
	}
	return &value
}

// CanonicalRequestedReasoningEffort 提取策略改写前客户端请求的推理档位。
// 显式字段优先（包括 none）；缺失显式字段时再从模型名末尾的档位后缀推导。
func CanonicalRequestedReasoningEffort(body []byte, modelCandidates ...string) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if raw == "" {
		raw = strings.TrimSpace(gjson.GetBytes(body, "output_config.effort").String())
	}
	if raw != "" {
		canonical := routing.NormalizeRequestedOpenAIReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}
	for _, model := range modelCandidates {
		if effort := canonicalReasoningEffortFromModelSuffix(model); effort != "" {
			return &effort
		}
	}
	if model := strings.TrimSpace(gjson.GetBytes(body, "model").String()); model != "" {
		if effort := canonicalReasoningEffortFromModelSuffix(model); effort != "" {
			return &effort
		}
	}
	return nil
}

func canonicalReasoningEffortFromModelSuffix(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if slash := strings.LastIndexByte(model, '/'); slash >= 0 {
		model = model[slash+1:]
	}
	parts := strings.FieldsFunc(strings.ToLower(model), func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	if len(parts) == 0 {
		return ""
	}
	return routing.NormalizeMaxReasoningEffort(parts[len(parts)-1])
}

// extractEffectiveOpenAIReasoningEffortFromBody 从最终上游请求体读取实际转发档位。
// 原请求提供非空 effort、但最终请求体已不再携带时，不允许再从模型后缀补值；
// 空字符串、空白字符串和 null 沿用既有语义，视为未提供。
func extractEffectiveOpenAIReasoningEffortFromBody(upstreamBody, originalBody []byte, modelCandidates ...string) *string {
	if strings.TrimSpace(gjson.GetBytes(originalBody, "reasoning.effort").String()) != "" ||
		strings.TrimSpace(gjson.GetBytes(originalBody, "reasoning_effort").String()) != "" {
		return extractOpenAIReasoningEffortFromBody(upstreamBody)
	}
	return extractOpenAIReasoningEffortFromBody(upstreamBody, modelCandidates...)
}

func extractOpenAIServiceTier(reqBody map[string]any) *string {
	if reqBody == nil {
		return nil
	}
	raw, ok := reqBody["service_tier"].(string)
	if !ok {
		return nil
	}
	return s09wire.NormalizeServiceTier(raw)
}

func extractOpenAIServiceTierFromBody(body []byte) *string {
	if len(body) == 0 {
		return nil
	}
	return s09wire.NormalizeServiceTier(gjson.GetBytes(body, "service_tier").String())
}

// evaluateOpenAIFastPolicy 返回指定账号、模型和 service_tier 应执行的动作及错误消息。
// 策略服务不可用或没有规则命中时返回 pass，调用方可安全地直接放行。
//
// 匹配规则：
//   - Scope 按账号类型过滤（all / oauth / apikey / bedrock）
//   - UserIDs 非空时按 API Key 所属的可信用户 ID 过滤
//   - ServiceTier 必须为空、all 或等于归一化后的 tier
//   - ModelWhitelist 将规则限制到指定模型，FallbackAction 处理未匹配模型
//   - 用户专属规则优先于全局规则，两组内部均保持配置顺序并首条命中
//
// 与 Claude BetaPolicy 的差异（保留首条匹配 short-circuit）：
//   - BetaPolicy 处理的是 anthropic-beta header 中的 token 集合，不同
//     规则可能针对不同 token，filter 需要累加成 set；block 则 first-match。
//   - OpenAI fast policy 操作的是单个字段 service_tier：filter 即删字段，
//     没有可累加的对象。一次请求只携带一个 service_tier，规则的 tier
//     维度天然互斥；同一 (scope, tier) 下若多条规则的 model whitelist
//     发生重叠，admin 可通过规则顺序明确意图。因此采用 first-match 而
//     非 BetaPolicy 那样的"block 覆盖 filter 覆盖 pass"语义。
func (s *OpenAIGatewayService) evaluateOpenAIFastPolicy(ctx context.Context, account *Account, model, serviceTier string) (action, errMsg string) {
	if s == nil || s.settingService == nil {
		return anthropic.BetaPolicyActionPass, ""
	}
	tier := strings.ToLower(strings.TrimSpace(serviceTier))
	if tier == "" {
		return anthropic.BetaPolicyActionPass, ""
	}
	settings := openAIFastPolicySettingsFromContext(ctx)
	if settings == nil {
		fetched, err := s.settingService.Gateway.GetOpenAIFastPolicySettings(ctx)
		if err != nil || fetched == nil {
			return anthropic.BetaPolicyActionPass, ""
		}
		settings = fetched
	}
	return tierpolicy.Evaluate(settings, openAIFastPolicyUserID(ctx), account != nil && account.IsOAuth(), account != nil && account.IsBedrock(), model, tier)
}

// openAIFastPolicyUserID 从可信请求上下文读取 API Key 所属用户 ID。
func openAIFastPolicyUserID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	access, _ := apikey.AccessSnapshotFromContext(ctx)
	userID := access.PayerUserID
	if userID <= 0 {
		return 0
	}
	return userID
}

// openAIFastPolicyCtxKey 是 context 中预取的 OpenAIFastPolicySettings 缓存
// 键，仅用于 WebSocket 长会话内多帧复用同一份策略快照，避免每帧 DB 命中。
//
// Trade-off：策略变更不会影响当前 WS session（只影响新 session）。这是
// 有意为之 —— 对长会话来说，"策略一致性"比"立刻生效"更重要，且 Claude
// BetaPolicy 的 gin.Context 缓存也是同样取舍。需要 hot-reload 时管理员
// 可以通过踢断 session 强制刷新。
type openAIFastPolicyCtxKeyType struct{}

var openAIFastPolicyCtxKey = openAIFastPolicyCtxKeyType{}

// withOpenAIFastPolicyContext 将一份 settings 快照绑定到 context，供该 ctx
// 衍生 goroutine 中的 evaluateOpenAIFastPolicy 复用。
func withOpenAIFastPolicyContext(ctx context.Context, settings *tierpolicy.OpenAIFastPolicySettings) context.Context {
	if ctx == nil || settings == nil {
		return ctx
	}
	return context.WithValue(ctx, openAIFastPolicyCtxKey, settings)
}

func openAIFastPolicySettingsFromContext(ctx context.Context) *tierpolicy.OpenAIFastPolicySettings {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(openAIFastPolicyCtxKey).(*tierpolicy.OpenAIFastPolicySettings); ok {
		return v
	}
	return nil
}

// openAIFastModeDecision 描述最终应写入、删除或拒绝的 OpenAI Fast 决策。
type openAIFastModeDecision struct {
	Tier        string
	DeleteField bool
	Blocked     *tierpolicy.BlockedError
}

// openAIGroupFastPolicy 只信任认证链路完整加载的分组，并限于 OpenAI 账号。
func openAIGroupFastPolicy(ctx context.Context, account *Account) string {
	if ctx == nil || account == nil || !account.IsOpenAI() {
		return routing.GroupOpenAIFastPolicyFollowRequest
	}
	group, _ := requeststate.GroupFromContext(ctx)
	if !routing.IsGroupContextValid(group) || !routing.GroupSupportsOpenAIFast(group.Platform) {
		return routing.GroupOpenAIFastPolicyFollowRequest
	}
	return group.EffectiveOpenAIFastPolicy()
}

// isOpenAIAcceleratedTier 同时覆盖 Fast 和 Ultra Fast。
func isOpenAIAcceleratedTier(tier string) bool {
	return tier == tierpolicy.OpenAIFastTierPriority || tier == tierpolicy.OpenAIFastTierUltrafast
}

// resolveOpenAIFastModeDecision 统一解析系统策略与单 Key 策略。
// 系统先裁决原始 tier；Key 改写后再裁决一次，避免 force_on 绕过系统 filter/block。
func (s *OpenAIGatewayService) resolveOpenAIFastModeDecision(
	ctx context.Context,
	account *Account,
	model string,
	rawTier string,
	hasField bool,
) openAIFastModeDecision {
	normTier := s09wire.ServiceTierValue(rawTier)
	groupPolicy := openAIGroupFastPolicy(ctx, account)
	switch groupPolicy {
	case routing.GroupOpenAIFastPolicyForcePriority:
		normTier, hasField = tierpolicy.OpenAIFastTierPriority, true
	case routing.GroupOpenAIFastPolicyForceUltrafast:
		normTier, hasField = tierpolicy.OpenAIFastTierUltrafast, true
	}
	applySystemAction := func(tier string) (openAIFastModeDecision, bool) {
		action, errMsg := s.evaluateOpenAIFastPolicy(ctx, account, model, tier)
		switch action {
		case anthropic.BetaPolicyActionBlock:
			if errMsg == "" {
				errMsg = fmt.Sprintf("openai service_tier=%s is not allowed for model %s", tier, model)
			}
			return openAIFastModeDecision{Blocked: &tierpolicy.BlockedError{Message: errMsg}}, true
		case anthropic.BetaPolicyActionFilter:
			return openAIFastModeDecision{DeleteField: true}, true
		case tierpolicy.OpenAIFastPolicyActionForcePriority:
			return openAIFastModeDecision{Tier: tierpolicy.OpenAIFastTierPriority}, true
		case tierpolicy.OpenAIFastPolicyActionForceUltrafast:
			return openAIFastModeDecision{Tier: tierpolicy.OpenAIFastTierUltrafast}, true
		default:
			return openAIFastModeDecision{}, false
		}
	}

	// 原始请求已命中的非 pass 系统动作直接生效，Key 策略不能覆盖。
	if normTier != "" {
		if decision, handled := applySystemAction(normTier); handled {
			return decision
		}
	}

	// 全局先裁决；分组关闭后，单 Key 不得重新开启。
	if groupPolicy == routing.GroupOpenAIFastPolicyForceOff {
		if isOpenAIAcceleratedTier(normTier) {
			return openAIFastModeDecision{DeleteField: hasField}
		}
		return openAIFastModeDecision{Tier: normTier}
	}
	candidateTier := normTier
	policy := apiKeyFastModePolicyFromContext(ctx)
	keyPolicyApplicable := false
	switch policy {
	case apikey.APIKeyFastModePolicyForceOn:
		keyPolicyApplicable = s.openAIAPIKeyFastModeForceOnSupported(ctx, account, model)
	case apikey.APIKeyFastModePolicyForceOff:
		// 强制关闭只净化真正代表 Fast 的 priority，不依赖定价文件中的能力标记。
		keyPolicyApplicable = account != nil && account.IsOpenAI()
	}
	candidateChanged := false
	if keyPolicyApplicable {
		switch policy {
		case apikey.APIKeyFastModePolicyForceOn:
			// 单 Key 开启 Fast 不降低分组强制的 Ultra Fast。
			if groupPolicy != routing.GroupOpenAIFastPolicyForceUltrafast {
				candidateTier = tierpolicy.OpenAIFastTierPriority
			}
		case apikey.APIKeyFastModePolicyForceOff:
			// flex 是低优先级模式，auto/default/scale 也是官方合法 tier，均需保留。
			if isOpenAIAcceleratedTier(normTier) {
				candidateTier = ""
			}
		}
		candidateChanged = candidateTier != normTier
	}

	// Key 注入或改写出的 tier 必须重新接受系统策略裁决。
	if candidateTier != "" {
		if candidateChanged {
			if decision, handled := applySystemAction(candidateTier); handled {
				return decision
			}
		}
		return openAIFastModeDecision{Tier: candidateTier}
	}
	if policy == apikey.APIKeyFastModePolicyForceOff && keyPolicyApplicable && isOpenAIAcceleratedTier(normTier) {
		return openAIFastModeDecision{DeleteField: hasField}
	}
	return openAIFastModeDecision{}
}

// applyOpenAIFastPolicyToBody 对原始请求体应用系统策略和单 Key Fast 策略。
//
// Rationale for normalize-on-pass: chat-completions / messages 入口在调用本
// 函数之前已经通过 normalizeResponsesBodyServiceTier 把 service_tier 归一化
// 到了上游可识别值；passthrough（OpenAI 自动透传） / native /responses 等
// 入口没有这一前置步骤，pass 路径下若不在此处归一化，"fast" 就会被原样
// 透传到 OpenAI 上游导致 400/拒绝。把归一化收敛到本函数，所有入口行为一致。
func (s *OpenAIGatewayService) applyOpenAIFastPolicyToBody(ctx context.Context, account *Account, model string, body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	tierResult := gjson.GetBytes(body, "service_tier")
	decision := s.resolveOpenAIFastModeDecision(ctx, account, model, tierResult.String(), tierResult.Exists())
	if decision.Blocked != nil {
		return body, decision.Blocked
	}
	if decision.DeleteField {
		trimmed, err := sjson.DeleteBytes(body, "service_tier")
		if err != nil {
			return body, fmt.Errorf("strip service_tier from body: %w", err)
		}
		return trimmed, nil
	}
	if decision.Tier != "" && (!tierResult.Exists() || decision.Tier != tierResult.String()) {
		updated, err := sjson.SetBytes(body, "service_tier", decision.Tier)
		if err != nil {
			return body, fmt.Errorf("apply service_tier to body: %w", err)
		}
		return updated, nil
	}
	return body, nil
}

// writeOpenAIFastPolicyBlockedResponse 保留策略观察，具体 HTTP/SSE 输出委托 Adapter。
func writeOpenAIFastPolicyBlockedResponse(c *gin.Context, err *tierpolicy.BlockedError) {
	if c == nil || err == nil {
		return
	}
	gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	gatewayhttp.WriteForwardFastPolicyBlocked(c, err.Message, gatewayhttp.StopOpenAICompactSSEKeepaliveCommitted, func(c *gin.Context, status int, kind, message string) {
		gatewayhttp.WriteOpenAICompactSSEFailureMessage(c, status, kind, message, gatewayhttp.MarkOpsStreamError)
	})
}

// applyOpenAIFastPolicyToWSResponseCreate 针对单个 client -> upstream WebSocket
// 帧评估 OpenAI fast policy，该帧的顶层 "type" 必须是 "response.create"。
// 该函数镜像 HTTP 侧 applyOpenAIFastPolicyToBody 的契约，但作用于
// Realtime/Responses WS payload：
//
//   - pass：保留 service_tier，并将 "fast" 等别名归一化为 "priority"
//   - filter：返回删除顶层 service_tier 的副本
//   - force_priority：保留 service_tier，并将其强制改写为 "priority"
//   - block：返回 (frame, *OpenAIFastBlockedError)
//
// 所有帧先校验 Ultra；只有 "type" 字段严格等于 "response.create" 的帧
// 会继续执行 fast policy 检查或修改。其它帧类型（包括空字符串）
// 在通过 Ultra 校验后原样透传。OpenAI Realtime client-event 规范要求设置
// "type"，因此空 type 被视为畸形帧，本层不拦截，由上游负责拒绝。
//
// service_tier 位于 response.create 顶层，与 Responses HTTP body 形态一致
// （参见 openai_gateway_chat_completions.go:304、extractOpenAIServiceTierFromBody
// 以及 openai_ws_forwarder_ingress_session_test.go:402 的测试样例）。因此这里只需
// 检查或剥离顶层字段；当前 schema 没有嵌套形式。
//
// 调用方负责传入用于上游请求的 model；该 helper 不会重新推导。
func (s *OpenAIGatewayService) applyOpenAIFastPolicyToWSResponseCreate(
	ctx context.Context,
	account *Account,
	model string,
	frame []byte,
) ([]byte, *tierpolicy.BlockedError, error) {
	if len(frame) == 0 {
		return frame, nil, nil
	}
	if !gjson.ValidBytes(frame) {
		return frame, nil, nil
	}
	// WS 会话允许逐帧切换参数，因此每个客户端帧都必须在上游转发前拒绝 Ultra。
	if err := validateOpenAIReasoningEffort(frame, model); err != nil {
		return frame, nil, err
	}
	frameType := strings.TrimSpace(gjson.GetBytes(frame, "type").String())
	// Strict match: only response.create is policy-checked. Empty / other
	// types pass through untouched so we never accidentally strip fields
	// from response.cancel, conversation.item.create, or any future
	// client-event the spec adds. The Realtime spec requires "type" on
	// every client event, so an empty type is malformed input — let the
	// upstream reject it rather than guessing at our layer.
	if frameType != "response.create" {
		return frame, nil, nil
	}
	tierResult := gjson.GetBytes(frame, "service_tier")
	decision := s.resolveOpenAIFastModeDecision(ctx, account, model, tierResult.String(), tierResult.Exists())
	if decision.Blocked != nil {
		return frame, decision.Blocked, nil
	}
	if decision.DeleteField {
		trimmed, err := sjson.DeleteBytes(frame, "service_tier")
		if err != nil {
			return frame, nil, fmt.Errorf("strip service_tier from ws frame: %w", err)
		}
		return trimmed, nil, nil
	}
	if decision.Tier != "" && (!tierResult.Exists() || decision.Tier != tierResult.String()) {
		updated, err := sjson.SetBytes(frame, "service_tier", decision.Tier)
		if err != nil {
			return frame, nil, fmt.Errorf("apply service_tier in ws frame: %w", err)
		}
		return updated, nil, nil
	}
	return frame, nil, nil
}

// newOpenAIFastPolicyWSEventID returns a Realtime-style event_id for a
// server-emitted error event. Matches the loose "evt_<rand>" convention used
// by upstream Realtime servers; the exact value is not load-bearing and is
// only required for client-side log correlation. We reuse the existing
// google/uuid dependency rather than pulling a new one.
func newOpenAIFastPolicyWSEventID() string {
	id, err := uuid.NewRandom()
	if err != nil {
		// Extremely unlikely; fall back to a fixed prefix so the field is
		// still non-empty and the schema stays self-consistent.
		return "evt_openai_fast_policy"
	}
	// Strip dashes so it visually matches "evt_<hex>" rather than UUID v4
	// canonical form, mirroring what real Realtime traces look like.
	return "evt_" + strings.ReplaceAll(id.String(), "-", "")
}

// buildOpenAIFastPolicyBlockedWSEvent renders an OpenAI Realtime/Responses
// style "error" event payload for a request blocked by the OpenAI fast
// policy. The shape mirrors Realtime error events as observed in upstream
// traces and per the spec's server "error" event:
//
//	{
//	  "event_id": "evt_<random>",
//	  "type": "error",
//	  "error": {
//	    "type": "invalid_request_error",
//	    "code": "policy_violation",
//	    "message": "..."
//	  }
//	}
//
// event_id lets clients correlate the rejection in their logs; "code" gives
// programmatic clients a stable identifier (HTTP-side equivalent is the
// 403 permission_error JSON body).
func buildOpenAIFastPolicyBlockedWSEvent(err *tierpolicy.BlockedError) []byte {
	if err == nil {
		return nil
	}
	eventID := newOpenAIFastPolicyWSEventID()
	payload, mErr := json.Marshal(map[string]any{
		"event_id": eventID,
		"type":     "error",
		"error": map[string]any{
			"type":    "invalid_request_error",
			"code":    "policy_violation",
			"message": err.Message,
		},
	})
	if mErr != nil {
		// Fallback to a minimal hand-rolled payload; Marshal of the literal
		// shape above should never fail in practice.
		return []byte(`{"event_id":"` + eventID + `","type":"error","error":{"type":"invalid_request_error","code":"policy_violation","message":"openai fast policy blocked this request"}}`)
	}
	return payload
}

func getOpenAIRequestBodyMap(_ *gin.Context, body []byte) (map[string]any, error) {
	var reqBody map[string]any
	if err := wirejson.DecodeUseNumber(body, &reqBody); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	return reqBody, nil
}

// extractOpenAIReasoningEffort 的模型候选语义同 extractOpenAIReasoningEffortFromBody。
func extractOpenAIReasoningEffort(reqBody map[string]any, modelCandidates ...string) *string {
	if value, present := openai.GetOpenAIReasoningEffortFromReqBody(reqBody); present {
		if value == "" {
			return nil
		}
		return &value
	}

	value := deriveOpenAIReasoningEffortFromModelCandidates(modelCandidates)
	if value == "" {
		return nil
	}
	return &value
}

// CanonicalRequestedReasoningEffortFromReqBody 是 map 形态请求体的同等入口。
func CanonicalRequestedReasoningEffortFromReqBody(reqBody map[string]any, modelCandidates ...string) *string {
	if reqBody == nil {
		return CanonicalRequestedReasoningEffort(nil, modelCandidates...)
	}
	raw := ""
	if reasoning, ok := reqBody["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			raw = strings.TrimSpace(effort)
		}
	}
	if raw == "" {
		if effort, ok := reqBody["reasoning_effort"].(string); ok {
			raw = strings.TrimSpace(effort)
		}
	}
	if raw == "" {
		if outputConfig, ok := reqBody["output_config"].(map[string]any); ok {
			if effort, ok := outputConfig["effort"].(string); ok {
				raw = strings.TrimSpace(effort)
			}
		}
	}
	if raw != "" {
		canonical := routing.NormalizeRequestedOpenAIReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}
	return CanonicalRequestedReasoningEffort(nil, modelCandidates...)
}

func appendOpenAIResponsesRequestPathSuffix(base, suffix string) string {
	return openai.AppendResponsesPathSuffix(base, suffix)
}
