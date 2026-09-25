//go:build unit

package httpapi_test

import (
	"bufio"
	"crypto/rand"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	"net/http"
	"strings"
	"time"
)

// 输出契约使用原来的扫描缓冲和真实 HTTP Adapter，不模拟转换结果。
func conversionResponseFixture(resp *http.Response) forward.Response {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 500*1024*1024)
	return forward.Response{StatusCode: resp.StatusCode, Close: func() { _ = resp.Body.Close() }, Runtime: bridge.Runtime{Now: time.Now, ReadRandom: rand.Read}, RequestID: resp.Header.Get("x-request-id"), Headers: resp.Header, Lines: scanner}
}
func toolAnthropicSSEStream() string {
	return strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","content":[],"model":"glm-4.7","usage":{"input_tokens":10}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"lookup","input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"status\"}"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
}
