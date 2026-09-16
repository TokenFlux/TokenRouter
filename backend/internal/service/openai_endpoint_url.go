// 旧端点 helper 委托同一 HTTP 技术实现，S15/S16 清理。
package service

import "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

func buildOpenAIEndpointURL(base string, endpoint string) string {
	return httpclient.BuildOpenAIEndpointURL(base, endpoint)
}
func buildOpenAIResponsesInputTokensURL(base string) string {
	return httpclient.BuildOpenAIResponsesInputTokensURL(base)
}
func openAIBaseURLHasVersionSuffix(raw string) bool {
	return httpclient.OpenAIBaseURLHasVersionSuffix(raw)
}
