package ws

import "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

// RequestUsageDecoder 将同一请求档位和服务层级解析用于每轮独立用量快照。
type RequestUsageDecoder struct{}

func (RequestUsageDecoder) ServiceTier(body []byte) *string {
	return requeststate.ExtractOpenAIServiceTierFromBody(body)
}

func (RequestUsageDecoder) ReasoningEffort(body []byte, models ...string) *string {
	return requeststate.ExtractOpenAIReasoningEffortFromBody(body, models...)
}

func (RequestUsageDecoder) RequestedReasoningEffort(body []byte, models ...string) *string {
	return requeststate.CanonicalRequestedReasoningEffort(body, models...)
}
