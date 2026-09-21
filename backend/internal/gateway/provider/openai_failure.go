// OpenAI 失败识别复用平台解析；通用失败值不反向依赖具体供应商。
package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// IsOpenAIRequestBodyTooLarge 判断当前失败是否仍可通过更换账号发送相同报文。
func IsOpenAIRequestBodyTooLarge(e *forward.UpstreamFailoverError) bool {
	return e != nil && e.Reason == "openai_request_body_too_large"
}

// IsOpenAICapacityShed 保留请求级瞬态标记与平台报文识别的共同条件。
func IsOpenAICapacityShed(e *forward.UpstreamFailoverError) bool {
	return e != nil && e.RequestScopedTransient && openai.IsOpenAIRequestScopedCapacityShed("", e.ResponseBody)
}
