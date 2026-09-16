package service

import (
	"strings"

	rawwire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	upstreamResponseModelObserverContextKey = "upstream_response_model_observer"
	upstreamResponseModelMaxLength          = 200
)

// upstreamResponseModelObserver tracks one forwarding attempt (or one WS turn).
// A terminal declaration wins over an earlier declaration; otherwise the first
// declaration is retained. Observation never affects the forwarding path.
//
// Billing normally ignores the observed model as well; the only exception is a
// channel explicitly configured with billing_model_source = response_model,
// where a conflict flag makes billing fall back to the baseline model
// (see responseModelBillingDeclaration).
//
// The same observer also records the service tier the upstream reports having
// used (OpenAI service_tier, Anthropic usage.speed). The observed tier stays
// separate from the final outbound request tier until usage recording resolves
// the billable tier for the selected credential protocol.
type upstreamResponseModelObserver struct {
	first    string
	terminal string
	conflict bool

	firstTier         string
	firstTierConflict bool
	terminalTier      string
}

func (o *upstreamResponseModelObserver) Observe(model string, terminal bool) {
	model = normalizeObservedUpstreamResponseModel(model)
	if model == "" {
		return
	}
	current := o.Model()
	if current != "" && !strings.EqualFold(current, model) {
		o.conflict = true
	}
	if terminal {
		o.terminal = model
		return
	}
	if o.first == "" {
		o.first = model
	}
}

func normalizeObservedUpstreamResponseModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	runes := []rune(model)
	if len(runes) > upstreamResponseModelMaxLength {
		model = string(runes[:upstreamResponseModelMaxLength])
	}
	return model
}

func (o *upstreamResponseModelObserver) ObserveOpenAI(payload []byte, eventType string) {
	model := firstValidTrimmedGJSONModel(payload, "response.model", "model")
	terminal := isUpstreamResponseModelTerminalEvent(eventType)
	// 上游只有携带 model 的事件才同时提供可信的 service_tier；无 model 的
	// 增量帧不能作为计费依据。
	if model == "" {
		return
	}
	o.Observe(model, terminal)
	// Responses 的非终止事件通常只是回显请求档位，只有终止事件和无类型的
	// Chat Completions/非流式 JSON 才能作为实际处理档位的证据。
	if !terminal && strings.TrimSpace(eventType) != "" {
		return
	}
	tier := normalizeObservedOpenAIServiceTier(firstValidTrimmedGJSONModel(payload, "response.service_tier", "service_tier"))
	o.ObserveServiceTier(tier, terminal)
}

func (o *upstreamResponseModelObserver) ObserveAnthropic(payload []byte) {
	model := firstValidTrimmedGJSONModel(payload, "message.model", "model")
	if model != "" {
		o.Observe(model, false)
	}
	tier := normalizeObservedAnthropicSpeed(firstValidTrimmedGJSONModel(payload, "message.usage.speed", "usage.speed"))
	o.ObserveServiceTier(tier, false)
}

// ObserveServiceTier 记录上游声明的服务档位；终止事件优先，互相矛盾的非终止
// 声明全部作废，避免把不确定的档位用于计费。
func (o *upstreamResponseModelObserver) ObserveServiceTier(tier string, terminal bool) {
	if o == nil || tier == "" {
		return
	}
	if terminal {
		o.terminalTier = tier
		return
	}
	if o.firstTier == "" {
		o.firstTier = tier
		return
	}
	if o.firstTier != tier {
		o.firstTierConflict = true
	}
}

// ServiceTier 返回无歧义的上游实际服务档位；没有声明或声明冲突时返回空。
func (o *upstreamResponseModelObserver) ServiceTier() string {
	if o == nil {
		return ""
	}
	if o.terminalTier != "" {
		return o.terminalTier
	}
	if o.firstTierConflict {
		return ""
	}
	return o.firstTier
}

func normalizeObservedOpenAIServiceTier(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "priority", "fast":
		return OpenAIFastTierPriority
	case "default", "flex", "scale":
		return value
	default:
		return ""
	}
}

func normalizeObservedAnthropicSpeed(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "fast", "standard":
		return value
	default:
		return ""
	}
}

func (o *upstreamResponseModelObserver) ObserveGemini(payload []byte) {
	model := firstValidTrimmedGJSONModel(
		payload,
		"modelVersion",
		"response.modelVersion",
		"response.response.modelVersion",
	)
	// Gemini streaming has no universal terminal event carrying modelVersion;
	// treating each declaration as terminal retains the latest chunk.
	o.Observe(model, true)
}

func (o *upstreamResponseModelObserver) Model() string {
	if o == nil {
		return ""
	}
	if o.terminal != "" {
		return o.terminal
	}
	return o.first
}

func (o *upstreamResponseModelObserver) Conflict() bool {
	return o != nil && o.conflict
}

func beginUpstreamResponseModelObservation(c *gin.Context) *upstreamResponseModelObserver {
	observer := &upstreamResponseModelObserver{}
	if c != nil {
		c.Set(upstreamResponseModelObserverContextKey, observer)
	}
	return observer
}

func upstreamResponseModelObserverFromContext(c *gin.Context) *upstreamResponseModelObserver {
	if c == nil {
		return nil
	}
	value, ok := c.Get(upstreamResponseModelObserverContextKey)
	if !ok {
		return nil
	}
	observer, _ := value.(*upstreamResponseModelObserver)
	return observer
}

func observedUpstreamResponseServiceTier(c *gin.Context) string {
	return upstreamResponseModelObserverFromContext(c).ServiceTier()
}

// resolvedOpenAIUpstreamServiceTierFromObserver preserves the final outbound
// request tier. The observed response tier remains separate on
// OpenAIForwardResult.UpstreamResponseServiceTier and is reconciled once, at
// usage time, where the account protocol is available. In particular, the
// private ChatGPT Codex backend commonly reports default even for effective
// Fast turns, while public API response tiers remain authoritative.
func resolvedOpenAIUpstreamServiceTierFromObserver(_ *upstreamResponseModelObserver, outboundBodyTier *string) *string {
	return outboundBodyTier
}
func resolvedOpenAIUpstreamServiceTier(c *gin.Context, outboundBodyTier *string) *string {
	return resolvedOpenAIUpstreamServiceTierFromObserver(upstreamResponseModelObserverFromContext(c), outboundBodyTier)
}

// observeOpenAIServiceTierInContext 将原始 OpenAI 响应事件写入当前请求的
// observer；模型审计字段仍保持 fork 既有关闭状态。
func observeOpenAIServiceTierInContext(c *gin.Context, payload []byte, eventType string) {
	if c == nil || len(payload) == 0 {
		return
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	observer.ObserveOpenAI(payload, eventType)
}

// observeOpenAISSEBody 逐帧记录 Responses SSE 中的实际服务档位。
func observeOpenAISSEBody(c *gin.Context, body string) {
	if c == nil || strings.TrimSpace(body) == "" {
		return
	}
	forEachOpenAISSEFrame(body, func(eventType string, payload []byte) {
		observeOpenAIServiceTierInContext(c, payload, eventType)
	})
}

func openAIChatCompletionServiceTierEventType(payload []byte) string {
	return rawwire.OpenAIChatCompletionServiceTierEventType(payload)
}

func firstValidTrimmedGJSONModel(payload []byte, paths ...string) string {
	if len(payload) == 0 {
		return ""
	}
	for _, path := range paths {
		value := gjson.GetBytes(payload, path)
		if !value.Exists() || value.Type != gjson.String {
			continue
		}
		if model := strings.TrimSpace(value.String()); model != "" {
			// Validate only after finding a candidate. This avoids a full validation
			// pass on the common model-free delta path while still rejecting malformed
			// payloads that appear to declare a model.
			if !gjson.ValidBytes(payload) {
				return ""
			}
			return model
		}
	}
	return ""
}

func isUpstreamResponseModelTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}
