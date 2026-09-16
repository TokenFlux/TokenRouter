package antigravity

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// StreamingProcessor 保留平台 SSE 解包与 hook 接入，转换状态只有一份。
type StreamingProcessor struct {
	*bridge.GeminiToAnthropicStreamProcessor
}
type UsageMapHook = bridge.UsageMapHook

// NewStreamingProcessor 注入原来的 ID 生成来源。
func NewStreamingProcessor(model string) *StreamingProcessor {
	return &StreamingProcessor{bridge.NewGeminiToAnthropicStreamProcessor(model, geminiConversionRuntime())}
}

// ProcessLine 处理 SSE 行，返回 Claude SSE 事件
func (p *StreamingProcessor) ProcessLine(line string) []byte {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data:") {
		return nil
	}

	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return nil
	}

	// 解包 v1internal 响应
	var v1Resp V1InternalResponse
	if err := json.Unmarshal([]byte(data), &v1Resp); err != nil {
		// 尝试直接解析为 GeminiResponse
		var directResp GeminiResponse
		if err2 := json.Unmarshal([]byte(data), &directResp); err2 != nil {
			return nil
		}
		v1Resp.Response = directResp
		v1Resp.ResponseID = directResp.ResponseID
		v1Resp.ModelVersion = directResp.ModelVersion
	}

	result := p.ProcessResponse(&bridge.GeminiResponseInput{Response: v1Resp.Response, ResponseID: v1Resp.ResponseID, ModelVersion: v1Resp.ModelVersion})
	for _, message := range p.TakeDiagnostics() {
		log.Printf("%s", message)
	}
	return result
}
