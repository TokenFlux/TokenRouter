package requeststate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unsafe"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

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
	case capability.PlatformGemini:
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
		if protocol == capability.PlatformAnthropic {
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
// HTTP 入口通过 ParseGatewayRequest 解析一次，执行和会话计算复用同一结果，
// 避免为读取 model、stream、messages 和 metadata 反复解析请求体。
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
// protocol 指定请求协议格式（capability.PlatformAnthropic / capability.PlatformGemini），
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
