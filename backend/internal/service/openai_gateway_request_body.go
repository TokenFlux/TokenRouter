package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"
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
		return urlvalidator.ValidateURLFormat(raw, false)
	}
	if !s.cfg.Security.URLAllowlist.Enabled {
		return urlvalidator.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
	}
	return urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.UpstreamHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
}

// buildOpenAIResponsesURL 组装 OpenAI Responses 端点。
// - base 以 /v1 结尾：追加 /responses
// - base 以其他版本段结尾（如 /v4）：追加 /responses
// - base 已是 /responses：原样返回
// - 其他情况：追加 /v1/responses
func buildOpenAIResponsesURL(base string) string {
	return buildOpenAIEndpointURL(base, "/v1/responses")
}

// buildOpenAIResponsesURLForPlatform 组装平台对应的 Responses 端点。
// DeepSeek 原生 Responses 使用 /responses，其它 OpenAI 兼容平台沿用 /v1/responses。
func buildOpenAIResponsesURLForPlatform(platform, base string) string {
	if platform == PlatformDeepseek {
		return buildOpenAIEndpointURL(base, "/responses")
	}
	return buildOpenAIResponsesURL(base)
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

func normalizeDeepSeekResponsesRequestBody(account *Account, body []byte) []byte {
	if account == nil || !account.UsesNativeCNResponses() {
		return body
	}
	return s09wire.StatelessResponsesRequest(body)
}

func trimOpenAIEncryptedReasoningItems(reqBody map[string]any) bool {
	return s09wire.TrimEncryptedReasoningItems(reqBody)
}

func SanitizeOpenAICrossModeFailoverReasoning(body []byte) (sanitized []byte, changed bool, err error) {
	return nativeopenai.SanitizeOpenAICrossModeFailoverReasoning(body)
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

func NormalizeOpenAICompactRequestBodyForTest(body []byte) ([]byte, bool, error) {
	return normalizeOpenAICompactRequestBody(body)
}

func isOpenAIResponsesCompactPath(c *gin.Context) bool {
	suffix := strings.TrimSpace(openAIResponsesRequestPathSuffix(c))
	return suffix == "/compact" || strings.HasPrefix(suffix, "/compact/")
}

func normalizeOpenAICompactRequestBody(body []byte) ([]byte, bool, error) {
	return nativeopenai.NormalizeOpenAICompactRequestBody(body)
}

func normalizeOpenAIParallelToolCallsWithoutTools(body []byte, responsesLite bool) ([]byte, bool, error) {
	return nativeopenai.NormalizeOpenAIParallelToolCallsWithoutTools(body, responsesLite)
}

func normalizeOpenAIResponsesReasoningContentReplay(body []byte) ([]byte, bool, error) {
	return nativeopenai.NormalizeOpenAIResponsesReasoningContentReplay(body)
}

func normalizeOpenAIAPIKeyStoreFalseReasoningReplay(body []byte, knownStoreFalse bool) ([]byte, bool, error) {
	return nativeopenai.NormalizeOpenAIAPIKeyStoreFalseReasoningReplay(body, knownStoreFalse)
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
	if !isOpenAIGPT56Model(effectiveModel) ||
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

// openAIResponsesRequestPathSuffix 返回可拼接到上游 /responses URL 后面的子路径。
// 不可转发的子路径返回空串（退化为裸 /responses）；真正的拒绝由入口守卫
// IsForwardableOpenAIResponsesRequestPath 负责。这样即便将来新增路由漏挂守卫，
// 拼进上游 URL 的也只会是合规片段。
func openAIResponsesRequestPathSuffix(c *gin.Context) string {
	return gatewayhttp.OpenAIResponsesRequestPathSuffix(c)
}
func IsForwardableOpenAIResponsesRequestPath(c *gin.Context) bool {
	return gatewayhttp.IsForwardableOpenAIResponsesRequestPath(c)
}
func IsOpenAIResponsesInputTokensRequestPath(c *gin.Context) bool {
	return gatewayhttp.IsOpenAIResponsesInputTokensRequestPath(c)
}

func appendOpenAIResponsesRequestPathSuffix(baseURL, suffix string) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	// 兜底：调用方漏了校验时，这里也不会把不合规的片段拼进上游 URL。
	trimmedSuffix, ok := sanitizedUpstreamPathSuffix(suffix)
	if !ok || trimmedBase == "" || trimmedSuffix == "" {
		return trimmedBase
	}
	return trimmedBase + trimmedSuffix
}

func (s *OpenAIGatewayService) replaceModelInResponseBody(body []byte, fromModel, toModel string) []byte {
	return s09wire.ReplaceModelInResponseBody(body, fromModel, toModel)
}

func getOpenAIReasoningEffortFromReqBody(reqBody map[string]any) (value string, present bool) {
	return nativeopenai.GetOpenAIReasoningEffortFromReqBody(reqBody)
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
	if parts[len(parts)-1] == "max" && !isOpenAIModelAtLeastVersion(modelID, 5, 6) {
		return ""
	}
	return normalizeOpenAIReasoningEffortForModel(parts[len(parts)-1], modelID)
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

func normalizeOpenAIOAuthResponsesCompatibilityBody(body []byte) ([]byte, bool, error) {
	return s09wire.NormalizeOpenAIOAuthResponsesCompatibilityBody(body)
}

func normalizeOpenAIResponsesReasoningMode(body []byte) ([]byte, bool, error) {
	return nativeopenai.NormalizeOpenAIResponsesReasoningMode(body)
}

func normalizeOpenAIResponseFormatSchemasBody(body []byte) ([]byte, bool, error) {
	return nativeopenai.NormalizeOpenAIResponseFormatSchemasBody(body)
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
	if next, normalizedReasoningContent, err := normalizeOpenAIResponsesReasoningContentReplay(normalized); err != nil {
		return body, false, err
	} else if normalizedReasoningContent {
		normalized = next
		changed = true
	}
	if account.IsOpenAIApiKey() {
		if next, normalizedParallel, err := normalizeOpenAIParallelToolCallsWithoutTools(normalized, responsesLite); err != nil {
			return body, false, err
		} else if normalizedParallel {
			normalized = next
			changed = true
		}
		if next, normalizedReasoning, err := normalizeOpenAIAPIKeyStoreFalseReasoningReplay(normalized, false); err != nil {
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
		if reasoningBody, reasoningChanged, err := normalizeOpenAIResponsesReasoningMode(normalized); err != nil {
			return body, false, err
		} else if reasoningChanged {
			normalized = reasoningBody
			changed = true
		}
	}
	if account != nil && account.IsOpenAIOAuthLike() {
		oauthBody, oauthChanged, err := normalizeOpenAIOAuthResponsesCompatibilityBody(normalized)
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
		if err := decodeOpenAIJSONUseNumber(normalized, &reqBody); err != nil {
			return body, false, fmt.Errorf("normalize websocket Responses body: %w", err)
		}
		mapChanged := false
		if needsOrphanCleanup {
			if input, ok := reqBody["input"].([]any); ok && sanitizeOpenAIResponsesOrphanToolOutputs(
				reqBody,
				input,
				strings.TrimSpace(firstNonEmptyString(reqBody["previous_response_id"])) != "",
			) {
				mapChanged = true
			}
		}
		if truncateOpenAIResponsesInputText(reqBody) {
			mapChanged = true
		}
		if mapChanged {
			next, err := marshalOpenAIUpstreamJSON(reqBody)
			if err != nil {
				return body, false, fmt.Errorf("serialize normalized websocket Responses body: %w", err)
			}
			normalized = next
			changed = true
		}
	}
	if schemaBody, schemaChanged, err := normalizeOpenAIResponseFormatSchemasBody(normalized); err != nil {
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
		if normalizeOpenAIResponsesImageGenerationTools(reqBody) {
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

func normalizeOpenAIPassthroughOAuthBody(body []byte, compact bool) ([]byte, bool, error) {
	return nativeopenai.NormalizeOpenAIPassthroughOAuthBody(body, compact)
}

func detectOpenAIPassthroughInstructionsRejectReason(reqModel string, body []byte) string {
	return nativeopenai.DetectOpenAIPassthroughInstructionsRejectReason(reqModel, body)
}

func isOpenAICodexModel(model string) bool { return nativeopenai.IsOpenAICodexModel(model) }

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
		normalized := normalizeOpenAIReasoningEffort(reasoningEffort)
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
		canonical := normalizeRequestedOpenAIReasoningEffort(raw)
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
	return NormalizeMaxReasoningEffort(parts[len(parts)-1])
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
	return normalizeOpenAIServiceTier(raw)
}

func extractOpenAIServiceTierFromBody(body []byte) *string {
	if len(body) == 0 {
		return nil
	}
	return normalizeOpenAIServiceTier(gjson.GetBytes(body, "service_tier").String())
}

func normalizeOpenAIServiceTier(raw string) *string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return nil
	}
	if value == "fast" {
		value = "priority"
	}
	// 放过 OpenAI 官方文档定义的合法 tier 值，以及 Codex/API 新增的 ultrafast。
	// Codex 客户端会发 priority、flex 或 ultrafast；直连 OpenAI SDK 的用户还会
	// 透传 auto/default/scale。真未知值仍返回 nil，由
	// normalizeResponsesBodyServiceTier 从 body 中删除。
	switch value {
	case "priority", "flex", "auto", "default", "scale", OpenAIFastTierUltrafast:
		return &value
	default:
		return nil
	}
}

// OpenAIFastBlockedError 表示请求被 OpenAI Fast 策略的 block 动作拒绝。
// ErrInvalidOpenAIServiceTier 表示请求携带了未知的 service_tier。handler 会将其
// 转换为 400 invalid_request_error，避免静默剥离字段而掩盖客户端意图。
type ErrInvalidOpenAIServiceTier struct {
	Value string
}

func (e *ErrInvalidOpenAIServiceTier) Error() string {
	return fmt.Sprintf("invalid service_tier %q: must be one of auto, default, fast, flex, priority, scale, ultrafast", e.Value)
}

const invalidOpenAIServiceTierValueMaxLen = 64

func boundInvalidOpenAIServiceTierValue(raw string) string {
	if len(raw) <= invalidOpenAIServiceTierValueMaxLen {
		return raw
	}
	return raw[:invalidOpenAIServiceTierValueMaxLen] + "..."
}

// ValidateOpenAIServiceTierField 校验 OpenAI 兼容请求体中的 service_tier 字段。
//
// 空值或 null 保持兼容；fast 归一化为 priority；priority、flex、auto、default、
// scale、ultrafast 原样通过。显式的非字符串、空字符串或未知值返回校验错误。
func ValidateOpenAIServiceTierField(body []byte) (string, error) {
	tierResult := gjson.GetBytes(body, "service_tier")
	if !tierResult.Exists() || tierResult.Type == gjson.Null {
		return "", nil
	}
	if tierResult.Type != gjson.String {
		return "", &ErrInvalidOpenAIServiceTier{Value: "<non-string>"}
	}
	raw := strings.TrimSpace(tierResult.String())
	if raw == "" {
		return "", &ErrInvalidOpenAIServiceTier{Value: raw}
	}
	norm := normalizedOpenAIServiceTierValue(raw)
	if norm == "" {
		return "", &ErrInvalidOpenAIServiceTier{Value: boundInvalidOpenAIServiceTierValue(raw)}
	}
	return norm, nil
}

// OpenAIFastBlockedError 表示请求被 OpenAI Fast 策略的 block 动作拒绝。
type OpenAIFastBlockedError struct {
	Message string
}

func (e *OpenAIFastBlockedError) Error() string { return e.Message }

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
		return BetaPolicyActionPass, ""
	}
	tier := strings.ToLower(strings.TrimSpace(serviceTier))
	if tier == "" {
		return BetaPolicyActionPass, ""
	}
	settings := openAIFastPolicySettingsFromContext(ctx)
	if settings == nil {
		fetched, err := s.settingService.GetOpenAIFastPolicySettings(ctx)
		if err != nil || fetched == nil {
			return BetaPolicyActionPass, ""
		}
		settings = fetched
	}
	return evaluateOpenAIFastPolicyWithSettings(settings, openAIFastPolicyUserID(ctx), account, model, tier)
}

// evaluateOpenAIFastPolicyWithSettings 是策略求值的纯函数核心，让 WS 等长会话
// 只预取一次配置，避免每一帧都访问 settingService。
func evaluateOpenAIFastPolicyWithSettings(settings *OpenAIFastPolicySettings, userID int64, account *Account, model, tier string) (action, errMsg string) {
	if settings == nil {
		return BetaPolicyActionPass, ""
	}
	isOAuth := account != nil && account.IsOAuth()
	isBedrock := account != nil && account.IsBedrock()

	// 用户专属规则先于全局规则。规则组内仍按配置顺序首条命中，允许
	// 管理员为某位用户配置例外，而不被先出现的全局规则覆盖。
	for _, userScoped := range []bool{true, false} {
		for _, rule := range settings.Rules {
			if (len(rule.UserIDs) > 0) != userScoped || !openAIFastPolicyUserMatches(rule.UserIDs, userID) {
				continue
			}
			if !betaPolicyScopeMatches(rule.Scope, isOAuth, isBedrock) {
				continue
			}
			ruleTier := strings.ToLower(strings.TrimSpace(rule.ServiceTier))
			if ruleTier != "" && ruleTier != OpenAIFastTierAny && ruleTier != tier {
				continue
			}
			eff := BetaPolicyRule{
				Action:               rule.Action,
				ErrorMessage:         rule.ErrorMessage,
				ModelWhitelist:       rule.ModelWhitelist,
				FallbackAction:       rule.FallbackAction,
				FallbackErrorMessage: rule.FallbackErrorMessage,
			}
			return resolveRuleAction(eff, model)
		}
	}
	return BetaPolicyActionPass, ""
}

// openAIFastPolicyUserID 从可信请求上下文读取 API Key 所属用户 ID。
func openAIFastPolicyUserID(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	userID, _ := ctx.Value(ctxkey.UserID).(int64)
	if userID <= 0 {
		return 0
	}
	return userID
}

// openAIFastPolicyUserMatches 判断全局规则或指定用户规则是否匹配当前用户。
func openAIFastPolicyUserMatches(ruleUserIDs []int64, userID int64) bool {
	if len(ruleUserIDs) == 0 {
		return true
	}
	for _, ruleUserID := range ruleUserIDs {
		if ruleUserID == userID {
			return true
		}
	}
	return false
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
func withOpenAIFastPolicyContext(ctx context.Context, settings *OpenAIFastPolicySettings) context.Context {
	if ctx == nil || settings == nil {
		return ctx
	}
	return context.WithValue(ctx, openAIFastPolicyCtxKey, settings)
}

func openAIFastPolicySettingsFromContext(ctx context.Context) *OpenAIFastPolicySettings {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(openAIFastPolicyCtxKey).(*OpenAIFastPolicySettings); ok {
		return v
	}
	return nil
}

// openAIFastModeDecision 描述最终应写入、删除或拒绝的 OpenAI Fast 决策。
type openAIFastModeDecision struct {
	Tier        string
	DeleteField bool
	Blocked     *OpenAIFastBlockedError
}

// openAIGroupFastPolicy 只信任认证链路完整加载的分组，并限于 OpenAI 账号。
func openAIGroupFastPolicy(ctx context.Context, account *Account) string {
	if ctx == nil || account == nil || !account.IsOpenAI() {
		return GroupOpenAIFastPolicyFollowRequest
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	if !IsGroupContextValid(group) || !groupSupportsOpenAIFast(group.Platform) {
		return GroupOpenAIFastPolicyFollowRequest
	}
	return group.EffectiveOpenAIFastPolicy()
}

// isOpenAIAcceleratedTier 同时覆盖 Fast 和 Ultra Fast。
func isOpenAIAcceleratedTier(tier string) bool {
	return tier == OpenAIFastTierPriority || tier == OpenAIFastTierUltrafast
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
	normTier := normalizedOpenAIServiceTierValue(rawTier)
	groupPolicy := openAIGroupFastPolicy(ctx, account)
	switch groupPolicy {
	case GroupOpenAIFastPolicyForcePriority:
		normTier, hasField = OpenAIFastTierPriority, true
	case GroupOpenAIFastPolicyForceUltrafast:
		normTier, hasField = OpenAIFastTierUltrafast, true
	}
	applySystemAction := func(tier string) (openAIFastModeDecision, bool) {
		action, errMsg := s.evaluateOpenAIFastPolicy(ctx, account, model, tier)
		switch action {
		case BetaPolicyActionBlock:
			if errMsg == "" {
				errMsg = fmt.Sprintf("openai service_tier=%s is not allowed for model %s", tier, model)
			}
			return openAIFastModeDecision{Blocked: &OpenAIFastBlockedError{Message: errMsg}}, true
		case BetaPolicyActionFilter:
			return openAIFastModeDecision{DeleteField: true}, true
		case OpenAIFastPolicyActionForcePriority:
			return openAIFastModeDecision{Tier: OpenAIFastTierPriority}, true
		case OpenAIFastPolicyActionForceUltrafast:
			return openAIFastModeDecision{Tier: OpenAIFastTierUltrafast}, true
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
	if groupPolicy == GroupOpenAIFastPolicyForceOff {
		if isOpenAIAcceleratedTier(normTier) {
			return openAIFastModeDecision{DeleteField: hasField}
		}
		return openAIFastModeDecision{Tier: normTier}
	}
	candidateTier := normTier
	policy := apiKeyFastModePolicyFromContext(ctx)
	keyPolicyApplicable := false
	switch policy {
	case APIKeyFastModePolicyForceOn:
		keyPolicyApplicable = s.openAIAPIKeyFastModeForceOnSupported(ctx, account, model)
	case APIKeyFastModePolicyForceOff:
		// 强制关闭只净化真正代表 Fast 的 priority，不依赖定价文件中的能力标记。
		keyPolicyApplicable = account != nil && account.IsOpenAI()
	}
	candidateChanged := false
	if keyPolicyApplicable {
		switch policy {
		case APIKeyFastModePolicyForceOn:
			// 单 Key 开启 Fast 不降低分组强制的 Ultra Fast。
			if groupPolicy != GroupOpenAIFastPolicyForceUltrafast {
				candidateTier = OpenAIFastTierPriority
			}
		case APIKeyFastModePolicyForceOff:
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
	if policy == APIKeyFastModePolicyForceOff && keyPolicyApplicable && isOpenAIAcceleratedTier(normTier) {
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
func writeOpenAIFastPolicyBlockedResponse(c *gin.Context, err *OpenAIFastBlockedError) {
	if c == nil || err == nil {
		return
	}
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	gatewayhttp.WriteForwardFastPolicyBlocked(c, err.Message, StopOpenAICompactSSEKeepaliveCommitted, writeOpenAICompactSSEFailureMessage)
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
) ([]byte, *OpenAIFastBlockedError, error) {
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
func buildOpenAIFastPolicyBlockedWSEvent(err *OpenAIFastBlockedError) []byte {
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

func openAIJSONValueMayContainImageInput(value gjson.Result) bool {
	return s09wire.JSONValueMayContainImageInput(value)
}

func openAIRequestBodyMayContainEmptyBase64InputImage(body []byte) bool {
	return nativeopenai.OpenAIRequestBodyMayContainEmptyBase64InputImage(body)
}

func sanitizeEmptyBase64InputImagesInOpenAIBody(body []byte) ([]byte, bool, error) {
	return nativeopenai.SanitizeEmptyBase64InputImagesInOpenAIBody(body)
}

func sanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(reqBody map[string]any) bool {
	return nativeopenai.SanitizeEmptyBase64InputImagesInOpenAIRequestBodyMap(reqBody)
}

func getOpenAIRequestBodyMap(_ *gin.Context, body []byte) (map[string]any, error) {
	var reqBody map[string]any
	if err := decodeOpenAIJSONUseNumber(body, &reqBody); err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}
	return reqBody, nil
}

// extractOpenAIReasoningEffort 的模型候选语义同 extractOpenAIReasoningEffortFromBody。
func extractOpenAIReasoningEffort(reqBody map[string]any, modelCandidates ...string) *string {
	if value, present := getOpenAIReasoningEffortFromReqBody(reqBody); present {
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
		canonical := normalizeRequestedOpenAIReasoningEffort(raw)
		if canonical == "" {
			return nil
		}
		return &canonical
	}
	return CanonicalRequestedReasoningEffort(nil, modelCandidates...)
}

func normalizeOpenAIReasoningEffort(raw string) string {
	return s09wire.NormalizeRecordedReasoningEffort(raw)
}
