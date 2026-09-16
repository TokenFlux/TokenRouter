package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unsafe"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	antigravity "github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	// 这些字节模式用于 fast-path 判断，避免每次 []byte("...") 产生临时分配。

	// Fast-path patterns for empty text blocks: {"type":"text","text":""}

	sessionUserAgentProductPattern = regexp.MustCompile(`([A-Za-z0-9._-]+)/[A-Za-z0-9._-]+`)
	sessionUserAgentVersionPattern = regexp.MustCompile(`\bv?\d+(?:\.\d+){1,3}\b`)
)

// SessionContext 粘性会话上下文，用于区分不同来源的请求。
// 仅在 GenerateSessionHash 第 3 级 fallback（消息内容 hash）时混入，
// 避免不同用户发送相同消息产生相同 hash 导致账号集中。
type SessionContext struct {
	ClientIP  string
	UserAgent string
	APIKeyID  int64
}

type jsonRange struct {
	start int        // 原始请求体中的起始偏移（闭区间）
	end   int        // 原始请求体中的结束偏移（开区间）
	kind  gjson.Type // JSON 值类型，用于调用方做轻量分支
}

type RequestBodyRef struct {
	data []byte
}

func NewRequestBodyRef(data []byte) *RequestBodyRef {
	return &RequestBodyRef{data: data}
}

func (b *RequestBodyRef) Bytes() []byte {
	if b == nil {
		return nil
	}
	return b.data
}

func (b *RequestBodyRef) Len() int {
	if b == nil {
		return 0
	}
	return len(b.data)
}

func (b *RequestBodyRef) Replace(data []byte) {
	if b == nil {
		return
	}
	b.data = data
}

func missingJSONRange() jsonRange {
	return jsonRange{start: -1, end: -1}
}

func rangeFromResult(r gjson.Result) jsonRange {
	if r.Raw == "" || r.Index <= 0 {
		return missingJSONRange()
	}
	end := r.Index + len(r.Raw)
	if end < r.Index {
		return missingJSONRange()
	}
	return jsonRange{start: r.Index, end: end, kind: r.Type}
}

func (r jsonRange) exists() bool {
	return r.start >= 0 && r.end >= r.start
}

// clearGatewayRequestDerivedState 清空绑定当前 body 的轻量派生字段，防止 ReplaceBody 后读到旧值。
func clearGatewayRequestDerivedState(parsed *ParsedRequest) {
	if parsed == nil {
		return
	}
	parsed.Model = ""
	parsed.Stream = false
	parsed.MetadataUserID = ""
	parsed.HasSystem = false
	parsed.ThinkingEnabled = false
	parsed.OutputEffort = ""
	parsed.MaxTokens = 0
	parsed.systemRange = missingJSONRange()
	parsed.messagesRange = missingJSONRange()
	parsed.inputRange = missingJSONRange()
}

func clearGatewayRequestRanges(parsed *ParsedRequest) {
	if parsed == nil {
		return
	}
	parsed.HasSystem = false
	parsed.systemRange = missingJSONRange()
	parsed.messagesRange = missingJSONRange()
	parsed.inputRange = missingJSONRange()
}

func setGatewayRequestRanges(parsed *ParsedRequest, protocol string, jsonStr string) {
	if parsed == nil {
		return
	}
	switch protocol {
	case domain.PlatformGemini:
		if sysParts := gjson.Get(jsonStr, "systemInstruction.parts"); sysParts.Exists() && sysParts.IsArray() {
			parsed.systemRange = rangeFromResult(sysParts)
		}
		if contents := gjson.Get(jsonStr, "contents"); contents.Exists() && contents.IsArray() {
			parsed.messagesRange = rangeFromResult(contents)
		}
	default:
		if sys := gjson.Get(jsonStr, "system"); sys.Exists() {
			parsed.HasSystem = true
			parsed.systemRange = rangeFromResult(sys)
		}
		if msgs := gjson.Get(jsonStr, "messages"); msgs.Exists() && msgs.IsArray() {
			parsed.messagesRange = rangeFromResult(msgs)
		}
		if protocol == "responses" {
			if input := gjson.Get(jsonStr, "input"); input.Exists() {
				parsed.inputRange = rangeFromResult(input)
			}
		}
	}
}

const claudeCodeLongContextModelSuffix = "[1m]"

// Claude Code 将 [1m] 作为客户端上下文选择器，转发前应移除泄漏到模型名末尾的
// 单个或重复后缀。
func normalizeClaudeCodeLongContextModel(model string) string {
	for len(model) > len(claudeCodeLongContextModelSuffix) &&
		strings.EqualFold(model[len(model)-len(claudeCodeLongContextModelSuffix):], claudeCodeLongContextModelSuffix) {
		model = model[:len(model)-len(claudeCodeLongContextModelSuffix)]
	}
	return model
}

// parseGatewayRequestCurrentBody 只做标量和 raw range 轻量解析，不恢复 system/messages 对象图。
func parseGatewayRequestCurrentBody(parsed *ParsedRequest, protocol string) error {
	if parsed == nil || parsed.Body == nil {
		return fmt.Errorf("empty request body")
	}

	bodyBytes := parsed.Body.Bytes()
	if !gjson.ValidBytes(bodyBytes) {
		return DescribeInvalidJSON(bodyBytes)
	}

	// 只在当前函数内零拷贝读取 JSON 字段；ReplaceBody 后必须重新进入本函数刷新派生状态。
	jsonStr := *(*string)(unsafe.Pointer(&bodyBytes))
	clearGatewayRequestDerivedState(parsed)
	parsed.protocol = protocol

	modelResult := gjson.Get(jsonStr, "model")
	if modelResult.Exists() {
		if modelResult.Type != gjson.String {
			return fmt.Errorf("invalid model field type")
		}
		parsed.Model = modelResult.String()
		if protocol == domain.PlatformAnthropic {
			normalizedModel := normalizeClaudeCodeLongContextModel(parsed.Model)
			if normalizedModel != parsed.Model {
				normalizedBody, err := sjson.SetBytes(bodyBytes, "model", normalizedModel)
				if err != nil {
					return fmt.Errorf("normalize model field: %w", err)
				}
				parsed.Body.Replace(normalizedBody)
				bodyBytes = normalizedBody
				jsonStr = *(*string)(unsafe.Pointer(&bodyBytes))
				parsed.Model = normalizedModel
			}
		}
	}

	streamResult := gjson.Get(jsonStr, "stream")
	if streamResult.Exists() {
		if streamResult.Type != gjson.True && streamResult.Type != gjson.False {
			return fmt.Errorf("invalid stream field type")
		}
		parsed.Stream = streamResult.Bool()
	}

	parsed.MetadataUserID = gjson.Get(jsonStr, "metadata.user_id").String()

	thinkingType := gjson.Get(jsonStr, "thinking.type").String()
	parsed.ThinkingEnabled = thinkingType == "enabled" || thinkingType == "adaptive"

	parsed.OutputEffort = strings.TrimSpace(gjson.Get(jsonStr, "output_config.effort").String())

	maxTokensResult := gjson.Get(jsonStr, "max_tokens")
	if maxTokensResult.Exists() && maxTokensResult.Type == gjson.Number {
		f := maxTokensResult.Float()
		if !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) &&
			f <= float64(math.MaxInt) && f >= float64(math.MinInt) {
			parsed.MaxTokens = int(f)
		}
	}

	setGatewayRequestRanges(parsed, protocol, jsonStr)
	return nil
}

func refreshGatewayRequestRanges(parsed *ParsedRequest, protocol string) error {
	return parseGatewayRequestCurrentBody(parsed, protocol)
}

// DescribeInvalidJSON 为校验失败的请求体生成诊断错误。它只在失败路径使用
// encoding/json 复解析并定位首个非法字节，错误仅包含长度、偏移和字符信息，
// 不包含请求体内容，因此调用方可以安全包装或记录。
func DescribeInvalidJSON(body []byte) error {
	var raw json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		var syntaxErr *json.SyntaxError
		if errors.As(err, &syntaxErr) {
			return fmt.Errorf("invalid json (len=%d, offset=%d): %s", len(body), syntaxErr.Offset, syntaxErr.Error())
		}
		return fmt.Errorf("invalid json (len=%d): %w", len(body), err)
	}
	// gjson 拒绝但 encoding/json 接受的边缘情况只报告长度。
	return fmt.Errorf("invalid json (len=%d)", len(body))
}

// ParsedRequest 保存网关请求的预解析结果
//
// 性能优化说明：
// 原实现在多个位置重复解析请求体（Handler、Service 各解析一次）：
// 1. gateway_handler.go 解析获取 model 和 stream
// 2. gateway_service.go 再次解析获取 system、messages、metadata
// 3. GenerateSessionHash 又一次解析获取会话哈希所需字段
//
// 新实现一次解析，多处复用：
// 1. 在 Handler 层统一调用 ParseGatewayRequest 一次性解析
// 2. 将解析结果 ParsedRequest 传递给 Service 层
// 3. 避免重复 json.Unmarshal，减少 CPU 和内存开销
type ParsedRequest struct {
	Body            *RequestBodyRef // 原始请求体引用（保留用于转发）；替换内容请走 ReplaceBody
	Model           string          // 请求的模型名称
	Stream          bool            // 是否为流式请求
	MetadataUserID  string          // metadata.user_id（用于会话亲和）
	HasSystem       bool            // 是否包含 system 字段（包含 null 也视为显式传入）
	ThinkingEnabled bool            // 是否开启 thinking（部分平台会影响最终模型名）
	OutputEffort    string          // output_config.effort（Claude API 的推理强度控制）
	MaxTokens       int             // max_tokens 值（用于探测请求拦截）
	SessionContext  *SessionContext // 可选：请求上下文区分因子（nil 时行为不变）

	protocol      string    // 当前 Body 的协议格式，用于 Body 替换后刷新 raw range
	systemRange   jsonRange // system/systemInstruction.parts 的 raw JSON 范围，绑定 Body 当前内容
	messagesRange jsonRange // messages/contents 的 raw JSON 范围，绑定 Body 当前内容
	inputRange    jsonRange // Responses API input 的 raw JSON 范围，绑定 Body 当前内容

	// GroupID 请求所属分组 ID（来自 API Key）
	GroupID *int64

	// OnUpstreamAccepted 上游接受请求后立即调用（用于提前释放串行锁）
	// 流式请求在收到 2xx 响应头后调用，避免持锁等流完成
	OnUpstreamAccepted func()
}

// NormalizeSessionUserAgent reduces UA noise for sticky-session and digest hashing.
// It preserves the set of product names from Product/Version tokens while
// discarding version-only changes and incidental comments.
func NormalizeSessionUserAgent(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	matches := sessionUserAgentProductPattern.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return normalizeSessionUserAgentFallback(raw)
	}

	products := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		product := strings.ToLower(strings.TrimSpace(match[1]))
		if product == "" {
			continue
		}
		if _, exists := seen[product]; exists {
			continue
		}
		seen[product] = struct{}{}
		products = append(products, product)
	}
	if len(products) == 0 {
		return normalizeSessionUserAgentFallback(raw)
	}
	sort.Strings(products)
	return strings.Join(products, "+")
}

func normalizeSessionUserAgentFallback(raw string) string {
	normalized := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	normalized = sessionUserAgentVersionPattern.ReplaceAllString(normalized, "")
	return strings.Join(strings.Fields(normalized), " ")
}

// ParseGatewayRequest 解析网关请求体并返回结构化结果。
// protocol 指定请求协议格式（domain.PlatformAnthropic / domain.PlatformGemini），
// 不同协议使用不同的 system/messages 字段名。
func ParseGatewayRequest(body *RequestBodyRef, protocol string) (*ParsedRequest, error) {
	parsed := &ParsedRequest{Body: body}
	if err := parseGatewayRequestCurrentBody(parsed, protocol); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (p *ParsedRequest) raw(r jsonRange) []byte {
	if p == nil || p.Body == nil || !r.exists() {
		return nil
	}
	body := p.Body.Bytes()
	if r.end > len(body) {
		return nil
	}
	return body[r.start:r.end]
}

func (p *ParsedRequest) SystemRaw() []byte {
	return p.raw(p.systemRange)
}

func (p *ParsedRequest) MessagesRaw() []byte {
	return p.raw(p.messagesRange)
}

func (p *ParsedRequest) InputRaw() []byte {
	return p.raw(p.inputRange)
}

func (p *ParsedRequest) DecodeSystem(dst any) error {
	raw := p.SystemRaw()
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func (p *ParsedRequest) DecodeMessages(dst any) error {
	raw := p.MessagesRaw()
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, dst)
}

func (p *ParsedRequest) SystemValue() (any, bool) {
	raw := p.SystemRaw()
	if len(raw) == 0 {
		return nil, false
	}
	var system any
	if err := json.Unmarshal(raw, &system); err != nil {
		return nil, false
	}
	return system, true
}

// CloneForBody 为单次账号尝试创建独立 body 视图，避免 failover 复用已改写的 ParsedRequest。
func (p *ParsedRequest) CloneForBody(body []byte) (*ParsedRequest, error) {
	if p == nil {
		return nil, fmt.Errorf("parse request: empty request")
	}
	clone := *p
	clone.Body = NewRequestBodyRef(body)
	clone.OnUpstreamAccepted = nil
	if err := refreshGatewayRequestRanges(&clone, clone.protocol); err != nil {
		return nil, err
	}
	return &clone, nil
}

// ReplaceBody 统一刷新当前 body 和 raw range，保证后续 helper 读取的是最新请求体。
func (p *ParsedRequest) ReplaceBody(data []byte) error {
	if p == nil {
		return fmt.Errorf("parse request: empty request")
	}
	if p.Body == nil {
		p.Body = NewRequestBodyRef(data)
	} else {
		p.Body.Replace(data)
	}
	if err := refreshGatewayRequestRanges(p, p.protocol); err != nil {
		clearGatewayRequestRanges(p)
		return err
	}
	return nil
}

func sliceRawFromBody(body []byte, r gjson.Result) []byte {
	return protocolanthropic.SliceRawFromBody(body, r)
}

func StripEmptyTextBlocks(body []byte) []byte { return protocolanthropic.StripEmptyTextBlocks(body) }

// FilterThinkingBlocks 从请求体中移除不适合直发的 thinking block。
// 过滤失败时返回原 body，避免整流逻辑影响主请求。
// 主要用于避免无效 thinking block signature 触发上游 400。
//
// 策略：
//   - 当 thinking.type 不是 "enabled"/"adaptive"：移除所有 thinking 相关块
//   - 当 thinking.type 是 "enabled"/"adaptive"：仅移除缺失/无效 signature 的 thinking 块（避免 400）
//     例如缺失、空值或占位 signature 的块
//
// 调用方传入 mappedModel 时会按上游协议族分流：仅 Anthropic 官方语义执行过滤；
// DeepSeek/Kimi/GLM/MiniMax 等 passback-required 上游必须原样回传历史 thinking block。
// 未传 mappedModel 时保留旧行为，便于既有单元测试和纯工具调用继续使用。
func FilterThinkingBlocks(body []byte, mappedModel ...string) []byte {
	if len(mappedModel) > 0 && !ShouldPreFilterThinkingBlocks(mappedModel[0]) {
		return body
	}
	return filterThinkingBlocksInternal(body, false)
}

func FilterThinkingBlocksForRetry(body []byte, mappedModel ...string) []byte {
	if len(mappedModel) > 0 && !ShouldApplyRetryFilters(mappedModel[0]) {
		return body
	}
	return protocolanthropic.FilterThinkingBlocksForRetry(body)
}

func sanitizeAnthropicBodyForBetaTokens(body []byte, anthropicBetaHeader string) ([]byte, bool) {
	return claude.SanitizeAnthropicBodyForBetaTokens(body, anthropicBetaHeader)
}

func anthropicBetaTokensContains(header, token string) bool {
	return claude.AnthropicBetaTokensContains(header, token)
}

func FilterSignatureSensitiveBlocksForRetry(body []byte, mappedModel ...string) []byte {
	if len(mappedModel) > 0 && !ShouldApplyRetryFilters(mappedModel[0]) {
		return body
	}
	return protocolanthropic.FilterSignatureSensitiveBlocksForRetry(body)
}

func filterThinkingBlocksInternal(body []byte, _ bool) []byte {
	return protocolanthropic.FilterThinkingBlocksInternal(body, antigravity.DummyThoughtSignature)
}

// NormalizeClaudeOutputEffort 委托 wire 档位解析，字段读取时机保持在旧网关。
func NormalizeClaudeOutputEffort(raw string) *string {
	return protocol.NormalizeClaudeOutputEffort(raw)
}

// DefaultEffortForThinkingEnabled 给"开启 thinking 但协议层没有 effort 档位概念"
// 的国产模型族返回默认 effort，用于 usage_log.reasoning_effort 展示。
func DefaultEffortForThinkingEnabled(mappedModel string) *string {
	if ResolveThinkingProtocol(mappedModel) != ThinkingProtocolPassbackRequired {
		return nil
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mappedModel)), "deepseek-") {
		// DeepSeek 原生支持 reasoning_effort，不在这里注入默认值。
		return nil
	}
	effort := "high"
	return &effort
}

// OpenAIBodyHasThinkingEnabled 检测 OpenAI 协议请求体是否开启 thinking。
func OpenAIBodyHasThinkingEnabled(body []byte) bool {
	thinkingType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()))
	return thinkingType == "enabled" || thinkingType == "adaptive"
}

// ApplyThinkingEnabledFallback 在调用方尚未解析出 effort 时，为启用 thinking 的
// 国产 passback-required 上游补默认 effort；已显式传入的 effort 永远不覆盖。
func ApplyThinkingEnabledFallback(effort *string, body []byte, mappedModel string) *string {
	if effort != nil {
		return effort
	}
	if !OpenAIBodyHasThinkingEnabled(body) {
		return nil
	}
	return DefaultEffortForThinkingEnabled(mappedModel)
}

// NormalizeGLMOpenAIReasoningEffort 将 OpenAI Chat Completions 的
// reasoning_effort 档位映射到 GLM/z.ai 原生 high/max 档位。
// 仅对映射后的 glm-* 模型生效，其它上游保持原请求不变。
func NormalizeGLMOpenAIReasoningEffort(body []byte, mappedModel string) ([]byte, bool) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mappedModel)), "glm-") {
		return body, false
	}

	path := "reasoning.effort"
	raw := strings.TrimSpace(gjson.GetBytes(body, path).String())
	if raw == "" {
		path = "reasoning_effort"
		raw = strings.TrimSpace(gjson.GetBytes(body, path).String())
	}
	if raw == "" {
		return body, false
	}

	mapped := normalizeGLMOpenAIReasoningEffort(raw)
	if isGLM53Model(mappedModel) && mapped == "high" && normalizeEffortToken(raw) == "low" {
		mapped = "low"
	}
	if mapped == "" || mapped == raw {
		return body, false
	}

	modified, err := sjson.SetBytes(body, path, mapped)
	if err != nil {
		return body, false
	}
	return modified, true
}

func normalizeEffortToken(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
}

func isGLM53Model(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "glm-5.3")
}

func normalizeGLMOpenAIReasoningEffort(raw string) string {
	value := normalizeEffortToken(raw)
	if value == "" {
		return ""
	}

	switch value {
	case "low", "medium", "high":
		return "high"
	case "xhigh", "extrahigh", "max", "ultracode":
		return "max"
	default:
		return ""
	}
}

// NormalizeGLM53AnthropicThinking 将客户端显式 thinking 强度映射到 GLM-5.3
// Anthropic 兼容档位；未提供强度或 thinking 偏好时保持请求不变，交由上游默认处理。
func NormalizeGLM53AnthropicThinking(body []byte, mappedModel string) ([]byte, bool) {
	if !isGLM53Model(mappedModel) {
		return body, false
	}

	raw := gjson.GetBytes(body, "output_config.effort").String()
	if strings.TrimSpace(raw) == "" {
		raw = gjson.GetBytes(body, "thinking.type").String()
	}

	var effort string
	switch normalizeEffortToken(raw) {
	case "disabled", "off", "none", "minimal", "low":
		effort = "low"
	case "enabled", "adaptive", "medium", "high":
		effort = "high"
	case "xhigh", "max", "ultra":
		effort = "max"
	default:
		return body, false
	}

	modified, err := sjson.SetBytes(body, "thinking.type", "enabled")
	if err != nil {
		return body, false
	}
	modified, err = sjson.SetBytes(modified, "output_config.effort", effort)
	if err != nil {
		return body, false
	}
	return modified, true
}

// =========================
// Thinking Budget Rectifier
// =========================

const BudgetRectifyBudgetTokens = claude.BudgetRectifyBudgetTokens
const BudgetRectifyMaxTokens = claude.BudgetRectifyMaxTokens
const BudgetRectifyMinMaxTokens = claude.BudgetRectifyMinMaxTokens

func isThinkingBudgetConstraintError(errMsg string) bool {
	return claude.IsThinkingBudgetConstraintError(errMsg)
}

func RectifyThinkingBudget(body []byte) ([]byte, bool) { return claude.RectifyThinkingBudget(body) }

// NormalizeChineseLLMThinking 修正国产 Anthropic 兼容上游的 thinking.type 差异。
// 当前仅 MiniMax M 系列需要把 Anthropic SDK 默认的 enabled 改成 adaptive。
func NormalizeChineseLLMThinking(body []byte, mappedModel string) ([]byte, bool) {
	modelLower := strings.ToLower(strings.TrimSpace(mappedModel))
	if !strings.HasPrefix(modelLower, "minimax-m") {
		return body, false
	}
	if gjson.GetBytes(body, "thinking.type").String() != "enabled" {
		return body, false
	}
	modified, err := sjson.SetBytes(body, "thinking.type", "adaptive")
	if err != nil {
		return body, false
	}
	return modified, true
}
