package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// openAICompactClientStreamKey 标记 body-signal compact 请求（Codex remote
// compact v2，见 #3777）的原始 body 携带 stream:true。白名单归一化会删除
// stream 字段并让上游走 unary /responses/compact（JSON），但客户端仍按
// Responses SSE 协议消费响应：它必须收到 response.output_item.done（其中恰好
// 一个 type=compaction 的 item）和 response.completed，否则报
// "stream closed before response.completed" 并无限重连（#3875）。
const openAICompactClientStreamKey = "openai_compact_client_stream"

// MarkOpenAICompactClientStream 由 handler 在 body-signal 提升时调用，记录
// 客户端的原始 stream 意图，供响应写回阶段决定是否合成 SSE。
func MarkOpenAICompactClientStream(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(openAICompactClientStreamKey, true)
}

// OpenAICompactClientStreamKeyForTest 暴露上下文键，仅供跨包 handler 测试断言。
func OpenAICompactClientStreamKeyForTest() string {
	return openAICompactClientStreamKey
}

func OpenAICompactClientWantsStream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value, ok := c.Get(openAICompactClientStreamKey)
	if !ok {
		return false
	}
	wants, _ := value.(bool)
	return wants
}

// WriteOpenAICompactSSEBridge 将 unary compact 的最终 JSON 响应按 Codex remote
// compact v2 的消费协议合成为最小 Responses SSE 流写回客户端。仅当请求被标记
// 为 body-signal 客户端流式、状态码为 2xx 且 body 是合法 JSON 对象时生效；
// 返回 false 表示未写出任何内容，调用方应按原路径写回。
//
// 若下游心跳已把响应头提交为 200（见 openAICompactSSEKeepalive），则本函数
// 必须接管一切写回：非 2xx 或不可合成的响应降级为 response.failed 终止事件，
// 不能再返回 false（否则调用方的 JSON 写回会与已提交的 SSE 流交错）。
func WriteOpenAICompactSSEBridge(c *gin.Context, statusCode int, finalResponse []byte, observe CompactStreamErrorObserver) bool {
	if c == nil || !OpenAICompactClientWantsStream(c) {
		return false
	}
	// 先停心跳再写回，避免注释行与最终事件交错；停止后经互斥锁与心跳
	// goroutine 建立 happens-before，可安全接管 ResponseWriter。
	committed := StopOpenAICompactSSEKeepaliveCommitted(c)
	if statusCode < 200 || statusCode >= 300 {
		if committed {
			WriteOpenAICompactSSEFailure(c, statusCode, finalResponse, observe)
			return true
		}
		return false
	}
	payload, ok := BuildOpenAICompactSSEPayload(finalResponse)
	if !ok {
		if committed {
			WriteOpenAICompactSSEFailure(c, http.StatusBadGateway, finalResponse, observe)
			return true
		}
		return false
	}
	if !committed {
		header := c.Writer.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("Connection", "keep-alive")
		header.Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(statusCode)
	}
	_, _ = c.Writer.Write(payload)
	c.Writer.Flush()
	return true
}

// WriteOpenAICompactSSEFailure 从上游错误 body 提取错误消息后，以
// response.failed 终止事件回传。仅用于心跳已提交 200、无法再按 HTTP 状态码
// 回传错误的场景。
func WriteOpenAICompactSSEFailure(c *gin.Context, statusCode int, errorBody []byte, observe CompactStreamErrorObserver) {
	message := ""
	if len(errorBody) > 0 {
		message = logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(errorBody)))
	}
	if message == "" {
		message = "Upstream compact request failed with HTTP " + strconv.Itoa(statusCode)
	}
	WriteOpenAICompactSSEFailureMessage(c, statusCode, "upstream_error", message, observe)
}

// WriteOpenAICompactSSEFailureMessage 写出 response.failed 终止事件。Codex 对
// 流式 Responses 请求把 response.failed 作为合法终止事件处理（普通 error 帧
// 不被识别，会退化为 "stream closed before response.completed" 盲重连）。
// 同时标记流内错误，保证挂在 200 流上的失败仍进入 ops 错误看板。
func WriteOpenAICompactSSEFailureMessage(c *gin.Context, statusCode int, errType, message string, observe CompactStreamErrorObserver) {
	if c == nil {
		return
	}
	if observe != nil {
		observe(c, errType, message, statusCode)
	}
	responseID := newCompactResponseID()
	payload, err := json.Marshal(compactFailedEvent{
		Response: compactFailedResponse{
			CreatedAt: time.Now().Unix(),
			Error:     compactFailedError{Code: errType, Message: message},
			ID:        responseID,
			Object:    "response",
			Output:    []json.RawMessage{},
			Status:    "failed",
		},
		Type: "response.failed",
	})
	if err != nil {
		return
	}
	_, _ = c.Writer.Write([]byte("event: response.failed\ndata: "))
	_, _ = c.Writer.Write(payload)
	_, _ = c.Writer.Write([]byte("\n\n"))
	c.Writer.Flush()
}

// CompactStreamErrorObserver 保留客户端流错误的原观测时点和分类。
type CompactStreamErrorObserver func(*gin.Context, string, string, int)

// 明确的终止帧字段保留 created_at、空 output 及原 JSON 键顺序。
type compactFailedEvent struct {
	Response compactFailedResponse `json:"response"`
	Type     string                `json:"type"`
}
type compactFailedResponse struct {
	CreatedAt int64              `json:"created_at"`
	Error     compactFailedError `json:"error"`
	ID        string             `json:"id"`
	Object    string             `json:"object"`
	Output    []json.RawMessage  `json:"output"`
	Status    string             `json:"status"`
}
type compactFailedError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func newCompactResponseID() string {
	return "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// BuildOpenAICompactSSEPayload 在 HTTP 边界注入响应 ID 生成，纯转换不读运行环境。
func BuildOpenAICompactSSEPayload(body []byte) ([]byte, bool) {
	return compact.StreamPayload(body, newCompactResponseID)
}
