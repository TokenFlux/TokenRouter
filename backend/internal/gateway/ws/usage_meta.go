package ws

import (
	"strings"
	"sync/atomic"

	"github.com/tidwall/gjson"
)

// UsageDecoder 复用协议与网关策略的唯一档位解析，不读取账号或配置。
type UsageDecoder interface {
	ServiceTier([]byte) *string
	ReasoningEffort([]byte, ...string) *string
	RequestedReasoningEffort([]byte, ...string) *string
}

// UsageMeta 保存双向 relay 共享的原子会话缺省值，turn 独立快照仍由 TurnPayload 拥有。
type UsageMeta struct {
	decoder                  UsageDecoder
	ServiceTier              atomic.Pointer[string]
	ReasoningEffort          atomic.Pointer[string]
	RequestedReasoningEffort atomic.Pointer[string]

	// 上下行共享会话缺省模型；每轮模型链仍由原快照固化。
	sessionRequestModel atomic.Pointer[string]
}

func NewUsageMeta(initialRequestModel string, firstFrame []byte, decoder UsageDecoder) *UsageMeta {
	meta := &UsageMeta{decoder: decoder}
	model := strings.TrimSpace(initialRequestModel)
	if model == "" {
		model = RequestModelForFrame(firstFrame)
	}
	meta.sessionRequestModel.Store(&model)
	return meta
}

func (m *UsageMeta) InitFromFirstFrame(policyOutput []byte, mappedModel string) {
	if m == nil {
		return
	}
	m.ServiceTier.Store(m.decoder.ServiceTier(policyOutput))
	m.ReasoningEffort.Store(m.decoder.ReasoningEffort(policyOutput, mappedModel, m.LoadSessionRequestModel()))
}

// CaptureRequestedReasoningEffort 在策略和模型改写前保存客户端档位。
func (m *UsageMeta) CaptureRequestedReasoningEffort(originalBody []byte, modelCandidates ...string) {
	if m == nil {
		return
	}
	candidates := append([]string{m.LoadSessionRequestModel()}, modelCandidates...)
	m.RequestedReasoningEffort.Store(m.decoder.RequestedReasoningEffort(originalBody, candidates...))
}

func (m *UsageMeta) UpdateSessionRequestModel(payload []byte) {
	if m == nil {
		return
	}
	if model := RequestModelFromSessionFrame(payload); model != "" {
		m.sessionRequestModel.Store(&model)
	}
}

func (m *UsageMeta) RequestModelForFrame(payload []byte) string {
	if m == nil {
		return RequestModelForFrame(payload)
	}
	if model := RequestModelForFrame(payload); model != "" {
		return model
	}
	return m.LoadSessionRequestModel()
}

func (m *UsageMeta) UpdateFromResponseCreate(policyOutput []byte, mappedModel string, RequestModelForFrame string) {
	if m == nil {
		return
	}
	m.ServiceTier.Store(m.decoder.ServiceTier(policyOutput))
	m.ReasoningEffort.Store(m.decoder.ReasoningEffort(policyOutput, mappedModel, RequestModelForFrame))
}

func RequestModelForFrame(payload []byte) string {
	if len(payload) == 0 || strings.TrimSpace(gjson.GetBytes(payload, "type").String()) != "response.create" {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(payload, "model").String())
}

func RequestModelFromSessionFrame(payload []byte) string {
	if len(payload) == 0 || strings.TrimSpace(gjson.GetBytes(payload, "type").String()) != "session.update" {
		return ""
	}
	return strings.TrimSpace(gjson.GetBytes(payload, "session.model").String())
}

// LoadSessionRequestModel 对上下行提供同一原子读取入口，空值保持原回退语义。
func (m *UsageMeta) LoadSessionRequestModel() string {
	if m == nil {
		return ""
	}
	if value := m.sessionRequestModel.Load(); value != nil {
		return *value
	}
	return ""
}
