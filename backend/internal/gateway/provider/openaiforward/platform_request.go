package openaiforward

import (
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ResponsesEndpoint 保留 DeepSeek 原生路径与其余兼容平台的版本段规则。
func ResponsesEndpoint(platform, base string) string {
	if platform == capability.PlatformDeepseek {
		return httpclient.BuildOpenAIEndpointURL(base, "/responses")
	}
	return httpclient.BuildOpenAIEndpointURL(base, "/v1/responses")
}

// NormalizeCNResponsesBody 仅对已确认的原生 CN Responses 请求清理服务端状态字段。
func NormalizeCNResponsesBody(native bool, body []byte) []byte {
	if !native {
		return body
	}
	return openai.StatelessResponsesRequest(body)
}
