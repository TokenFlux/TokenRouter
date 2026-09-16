// 图片产出只按 wire 事实计数，旧资金消费者委托同一解析器。
package service

import wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

type openAIImageOutputCounter = wire.OpenAIImageOutputCounter

func newOpenAIImageOutputCounter() *openAIImageOutputCounter {
	return wire.NewOpenAIImageOutputCounter()
}

func countOpenAIResponseImageOutputsFromJSONBytes(body []byte) int {
	return wire.CountOpenAIResponseImageOutputsFromJSONBytes(body)
}
func collectOpenAIResponseImageOutputSizesFromJSONBytes(body []byte) []string {
	return wire.CollectOpenAIResponseImageOutputSizesFromJSONBytes(body)
}
func countOpenAIImageOutputsFromSSEBody(body string) int {
	return wire.CountOpenAIImageOutputsFromSSEBody(body)
}
func collectOpenAIImageOutputSizesFromSSEBody(body string) []string {
	return wire.CollectOpenAIImageOutputSizesFromSSEBody(body)
}
