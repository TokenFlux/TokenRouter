// 合成响应只属于 HTTP 兼容入口；其示例 token 不参与真实用量结算。
package httpapi

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/gin-gonic/gin"
)

// WriteInterceptStream 发送流式 mock 响应（用于请求拦截）
func WriteInterceptStream(c *gin.Context, model string, interceptType clientmeta.InterceptType) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 根据拦截类型决定响应内容
	var msgID string
	var outputTokens int
	var textDeltas []string

	switch interceptType {
	case clientmeta.InterceptTypeSuggestionMode:
		msgID = GenerateInterceptMessageID()
		outputTokens = 1
		textDeltas = []string{""} // 空内容
	default: // clientmeta.InterceptTypeWarmup
		msgID = GenerateInterceptMessageID()
		outputTokens = 2
		textDeltas = []string{"New", " Conversation"}
	}

	// 构造与 Anthropic 官方响应字段一致的 message_start 事件。
	messageStartJSON := `{"type":"message_start","message":{"model":` + strconv.Quote(model) + `,"id":` + strconv.Quote(msgID) + `,"type":"message","role":"assistant","content":[],"stop_reason":null,"stop_sequence":null,"stop_details":null,"usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0}}}`

	// 构造内容块事件。
	events := []string{
		`event: message_start` + "\n" + `data: ` + string(messageStartJSON),
		`event: content_block_start` + "\n" + `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
	}

	// 按原顺序追加文本增量。
	for _, text := range textDeltas {
		deltaJSON := `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":` + strconv.Quote(text) + `}}`
		events = append(events, `event: content_block_delta`+"\n"+`data: `+string(deltaJSON))
	}

	// message_delta 的 usage 只包含本次增量的输出 token。
	messageDeltaJSON := `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null},"usage":{"output_tokens":` + strconv.Itoa(outputTokens) + `}}`

	events = append(events,
		`event: content_block_stop`+"\n"+`data: {"index":0,"type":"content_block_stop"}`,
		`event: message_delta`+"\n"+`data: `+string(messageDeltaJSON),
		`event: message_stop`+"\n"+`data: {"type":"message_stop"}`,
	)

	for _, event := range events {
		_, _ = c.Writer.WriteString(event + "\n\n")
		c.Writer.Flush()
		time.Sleep(20 * time.Millisecond)
	}
}

// GenerateInterceptMessageID 生成 Anthropic 官方格式的消息 ID：msg_01 + 22 位 Base62。
func GenerateInterceptMessageID() string {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const idLen = 22
	randomBytes := make([]byte, idLen)
	if _, err := rand.Read(randomBytes); err != nil {
		// 极端情况下熵源不可用，仍保持客户端依赖的固定格式。
		return fmt.Sprintf("msg_01%022d", time.Now().UnixNano())
	}
	b := make([]byte, idLen)
	for i := range b {
		b[i] = charset[int(randomBytes[i])%len(charset)]
	}
	return "msg_01" + string(b)
}

// WriteInterceptResponse 发送非流式 mock 响应（用于请求拦截）
func WriteInterceptResponse(c *gin.Context, model string, interceptType clientmeta.InterceptType) {
	var msgID, text, stopReason string
	var outputTokens int

	switch interceptType {
	case clientmeta.InterceptTypeSuggestionMode:
		msgID = GenerateInterceptMessageID()
		text = ""
		outputTokens = 1
		stopReason = "end_turn"
	case clientmeta.InterceptTypeMaxTokensOneHaiku:
		msgID = GenerateInterceptMessageID()
		text = "#"
		outputTokens = 1
		stopReason = "max_tokens" // max_tokens=1 探测请求的 stop_reason 应为 max_tokens
	default: // clientmeta.InterceptTypeWarmup
		msgID = GenerateInterceptMessageID()
		text = "New Conversation"
		outputTokens = 2
		stopReason = "end_turn"
	}

	// 构建与 Anthropic 官方响应字段一致的完整响应。
	response := gin.H{
		"model":         model,
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"content":       []gin.H{{"type": "text", "text": text}},
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"stop_details":  nil,
		"usage": gin.H{
			"input_tokens":                10,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
			"cache_creation": gin.H{
				"ephemeral_5m_input_tokens": 0,
				"ephemeral_1h_input_tokens": 0,
			},
			"output_tokens": outputTokens,
		},
	}

	c.JSON(http.StatusOK, response)
}
