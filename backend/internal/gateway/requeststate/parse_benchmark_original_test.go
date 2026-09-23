//go:build unit

package requeststate_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/stretchr/testify/require"
)

// parseGatewayRequestOld 是基于完整 json.Unmarshal 的旧实现，用于 benchmark 对比基线。
// 核心路径：先 Unmarshal 到 map[string]any，再逐字段提取。
func parseGatewayRequestOld(body []byte, protocol string) (*requeststate.ParsedRequest, error) {
	parsed := &requeststate.ParsedRequest{
		Body: requeststate.NewRequestBodyRef(body),
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	// model
	if raw, ok := req["model"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid model field type")
		}
		parsed.Model = s
	}

	// stream
	if raw, ok := req["stream"]; ok {
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid stream field type")
		}
		parsed.Stream = b
	}

	// metadata.user_id
	if meta, ok := req["metadata"].(map[string]any); ok {
		if uid, ok := meta["user_id"].(string); ok {
			parsed.MetadataUserID = uid
		}
	}

	// thinking.type
	if thinking, ok := req["thinking"].(map[string]any); ok {
		if thinkType, ok := thinking["type"].(string); ok && thinkType == "enabled" {
			parsed.ThinkingEnabled = true
		}
	}

	// max_tokens
	if raw, ok := req["max_tokens"]; ok {
		if n, ok := parseIntegralNumber(raw); ok {
			parsed.MaxTokens = n
		}
	}

	return requeststate.ParseGatewayRequest(parsed.Body, protocol)
}

// buildSmallJSON 构建 ~500B 的小型测试 JSON
func buildSmallJSON() []byte {
	return []byte(`{"model":"claude-sonnet-4-5","stream":true,"max_tokens":4096,"metadata":{"user_id":"user-abc123"},"thinking":{"type":"enabled","budget_tokens":2048},"system":"You are a helpful assistant.","messages":[{"role":"user","content":"What is the meaning of life?"},{"role":"assistant","content":"The meaning of life is a philosophical question."},{"role":"user","content":"Can you elaborate?"}]}`)
}

// buildLargeJSON 构建 ~50KB 的大型测试 JSON（大量 messages）
func buildLargeJSON() []byte {
	b := []byte(`{"model":"claude-sonnet-4-5","stream":true,"max_tokens":8192,"metadata":{"user_id":"user-xyz789"},"system":[{"type":"text","text":"You are a detailed assistant.","cache_control":{"type":"ephemeral"}}],"messages":[`)

	msgCount := 200
	for i := 0; i < msgCount; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		if i%2 == 0 {
			b = fmt.Appendf(b, `{"role":"user","content":"This is user message number %d with some extra padding text to make the message reasonably long for benchmarking purposes. Lorem ipsum dolor sit amet."}`, i)
		} else {
			b = fmt.Appendf(b, `{"role":"assistant","content":[{"type":"text","text":"This is assistant response number %d. I will provide a detailed answer with multiple sentences to simulate real conversation content for benchmark testing."}]}`, i)
		}
	}

	return append(b, ']', '}')
}

func BenchmarkParseGatewayRequest_Old_Small(b *testing.B) {
	data := buildSmallJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseGatewayRequestOld(data, "")
	}
}

func BenchmarkParseGatewayRequest_New_Small(b *testing.B) {
	data := buildSmallJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(data), "")
	}
}

func BenchmarkParseGatewayRequest_Old_Large(b *testing.B) {
	data := buildLargeJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parseGatewayRequestOld(data, "")
	}
}

func TestOpenAIBodyHasThinkingEnabled(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "enabled", body: `{"thinking":{"type":"enabled"}}`, want: true},
		{name: "adaptive", body: `{"thinking":{"type":"adaptive"}}`, want: true},
		{name: "uppercase", body: `{"thinking":{"type":"ENABLED"}}`, want: true},
		{name: "disabled", body: `{"thinking":{"type":"disabled"}}`, want: false},
		{name: "missing type", body: `{"thinking":{"budget_tokens":1024}}`, want: false},
		{name: "missing thinking", body: `{"model":"glm-5.1"}`, want: false},
		{name: "invalid json", body: `{not json`, want: false},
		{name: "empty body", body: ``, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, requeststate.OpenAIBodyHasThinkingEnabled([]byte(tt.body)))
		})
	}
}

func BenchmarkParseGatewayRequest_New_Large(b *testing.B) {
	data := buildLargeJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(data), "")
	}
}
