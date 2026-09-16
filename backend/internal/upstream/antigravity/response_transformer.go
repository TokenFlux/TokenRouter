package antigravity

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// TransformGeminiToClaude 将 Gemini 响应转换为 Claude 格式（非流式）
func TransformGeminiToClaude(geminiResp []byte, originalModel string) ([]byte, *ClaudeUsage, error) {
	// 解包 v1internal 响应
	var v1Resp V1InternalResponse
	if err := json.Unmarshal(geminiResp, &v1Resp); err != nil {
		// 尝试直接解析为 GeminiResponse
		var directResp GeminiResponse
		if err2 := json.Unmarshal(geminiResp, &directResp); err2 != nil {
			return nil, nil, fmt.Errorf("parse gemini response: %w", err)
		}
		v1Resp.Response = directResp
		v1Resp.ResponseID = directResp.ResponseID
		v1Resp.ModelVersion = directResp.ModelVersion
	} else if len(v1Resp.Response.Candidates) == 0 {
		// 第一次解析成功但 candidates 为空，说明是直接的 GeminiResponse 格式
		var directResp GeminiResponse
		if err2 := json.Unmarshal(geminiResp, &directResp); err2 != nil {
			return nil, nil, fmt.Errorf("parse gemini response as direct: %w", err2)
		}
		v1Resp.Response = directResp
		v1Resp.ResponseID = directResp.ResponseID
		v1Resp.ModelVersion = directResp.ModelVersion
	}

	// 使用处理器转换
	processor := NewNonStreamingProcessor()
	claudeResp := processor.Process(&v1Resp.Response, v1Resp.ResponseID, originalModel)

	// 序列化
	respBytes, err := json.Marshal(claudeResp)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal claude response: %w", err)
	}

	return respBytes, &claudeResp.Usage, nil
}

// fallbackCounter 降级伪随机 ID 的全局计数器，混入 seed 避免高并发下 UnixNano 相同导致碰撞。
var fallbackCounter uint64

// generateRandomID 生成密码学安全的随机 ID
func generateRandomID() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	id := make([]byte, 12)
	randBytes := make([]byte, 12)
	if _, err := rand.Read(randBytes); err != nil {
		// 避免在请求路径里 panic：极端情况下熵源不可用时降级为伪随机。
		// 这里主要用于生成响应/工具调用的临时 ID，安全要求不高但需尽量避免碰撞。
		cnt := atomic.AddUint64(&fallbackCounter, 1)
		seed := uint64(time.Now().UnixNano()) ^ cnt
		seed ^= uint64(len(err.Error())) << 32
		for i := range id {
			seed ^= seed << 13
			seed ^= seed >> 7
			seed ^= seed << 17
			id[i] = chars[int(seed)%len(chars)]
		}
		return string(id)
	}
	for i, b := range randBytes {
		id[i] = chars[int(b)%len(chars)]
	}
	return string(id)
}

// generateAnthropicMsgID 生成 Anthropic 官方格式的消息 ID：msg_01 + 22 位 Base62。
func generateAnthropicMsgID() string {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const idLen = 22
	randomBytes := make([]byte, idLen)
	if _, err := rand.Read(randomBytes); err != nil {
		return "msg_01" + generateRandomID() + generateRandomID()[:10]
	}
	b := make([]byte, idLen)
	for i := range b {
		b[i] = charset[int(randomBytes[i])%len(charset)]
	}
	return "msg_01" + string(b)
}

// NonStreamingProcessor 只适配旧构造及诊断，响应状态由 bridge 唯一持有。
type NonStreamingProcessor struct {
	*bridge.GeminiToAnthropicResponseProcessor
}

func NewNonStreamingProcessor() *NonStreamingProcessor {
	return &NonStreamingProcessor{bridge.NewGeminiToAnthropicResponseProcessor(geminiConversionRuntime())}
}
func (p *NonStreamingProcessor) Process(response *GeminiResponse, responseID, model string) *ClaudeResponse {
	result := p.GeminiToAnthropicResponseProcessor.Process(response, responseID, model)
	for _, message := range p.TakeDiagnostics() {
		log.Printf("%s", message)
	}
	return result
}

// geminiConversionRuntime 保留原随机 ID、失败回退及全局计数器的唯一来源。
func geminiConversionRuntime() bridge.GeminiConversionRuntime {
	return bridge.GeminiConversionRuntime{RandomID: generateRandomID, MessageID: generateAnthropicMsgID}
}
