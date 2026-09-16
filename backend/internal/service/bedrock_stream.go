// 旧流式入口仅投影技术参数并转换结果，二进制帧算法只有原生包一份。
package service

import (
	"io"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

func extractBedrockChunkData(payload []byte) []byte { return native.ExtractBedrockChunkData(payload) }
func transformBedrockInvocationMetrics(data []byte) []byte {
	return native.TransformBedrockInvocationMetrics(data)
}

type bedrockEventStreamDecoder = native.BedrockEventStreamDecoder

func newBedrockEventStreamDecoder(r io.Reader) *bedrockEventStreamDecoder {
	return native.NewBedrockEventStreamDecoder(r)
}
func extractEventStreamHeaderValue(headers []byte, targetName string) string {
	return native.ExtractEventStreamHeaderValue(headers, targetName)
}
