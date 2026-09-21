package forward

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"

	"github.com/tidwall/gjson"
)

const upstreamResponseModelMaxLength = 200

// ResponseObserver 记录单次转发或 WS turn 的模型与实际档位。终态优先；
// 冲突标记供 response_model 计费回退使用，不改变出站请求档位。
type ResponseObserver struct {
	first    string
	terminal string
	conflict bool

	firstTier         string
	firstTierConflict bool
	terminalTier      string
}

func (o *ResponseObserver) Observe(model string, terminal bool) {
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

func (o *ResponseObserver) ObserveOpenAI(payload []byte, eventType string) {
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
	tier := NormalizeObservedOpenAIServiceTier(firstValidTrimmedGJSONModel(payload, "response.service_tier", "service_tier"))
	o.ObserveServiceTier(tier, terminal)
}

func (o *ResponseObserver) ObserveAnthropic(payload []byte) {
	model := firstValidTrimmedGJSONModel(payload, "message.model", "model")
	if model != "" {
		o.Observe(model, false)
	}
	tier := normalizeObservedAnthropicSpeed(firstValidTrimmedGJSONModel(payload, "message.usage.speed", "usage.speed"))
	o.ObserveServiceTier(tier, false)
}

// ObserveServiceTier 记录上游声明的服务档位；终止事件优先，互相矛盾的非终止
// 声明全部作废，避免把不确定的档位用于计费。
func (o *ResponseObserver) ObserveServiceTier(tier string, terminal bool) {
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
func (o *ResponseObserver) ServiceTier() string {
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

// NormalizeObservedOpenAIServiceTier 仅接受上游已知档位，保留 fast 的标准化规则。
func NormalizeObservedOpenAIServiceTier(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "priority", "fast":
		return tierpolicy.OpenAIFastTierPriority
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

func (o *ResponseObserver) ObserveGemini(payload []byte) {
	model := firstValidTrimmedGJSONModel(
		payload,
		"modelVersion",
		"response.modelVersion",
		"response.response.modelVersion",
	)
	// Gemini 流没有统一的模型终态声明，保留最近分块的模型。
	o.Observe(model, true)
}

func (o *ResponseObserver) Model() string {
	if o == nil {
		return ""
	}
	if o.terminal != "" {
		return o.terminal
	}
	return o.first
}

func (o *ResponseObserver) Conflict() bool {
	return o != nil && o.conflict
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
			// 仅在发现候选声明后验证整帧，跳过常见的无模型增量帧。
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
