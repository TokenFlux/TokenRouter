package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ResponsesFailedError 对齐 OpenAI Responses 协议 error 子对象。
type ResponsesFailedError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResponsesFailedBody 对齐 apicompat.makeResponsesCompletedEvent 输出的 response 子对象字段集。
// Output 用空 slice（不是 nil）确保 marshal 为 `[]` 而非 `null`。
// CreatedAt 不带 omitempty：严格客户端把它当必填字段，缺失会以
// `missing field 'created_at'` 反序列化失败——那正是本文件要避免的"客户端读不懂终止事件"。
type ResponsesFailedBody struct {
	ID        string               `json:"id"`
	Object    string               `json:"object"`
	CreatedAt int64                `json:"created_at"`
	Model     string               `json:"model,omitempty"`
	Status    string               `json:"status"`
	Output    []any                `json:"output"`
	Error     ResponsesFailedError `json:"error"`
}

// ResponsesFailedEvent 是写入 SSE data 行的顶层结构。
// 故意不带 sequence_number：spec 标记可选，且本函数被调用时无法可靠拿到 last seq。
type ResponsesFailedEvent struct {
	Type     string              `json:"type"`
	Response ResponsesFailedBody `json:"response"`
}

// WriteResponsesFailedSSE 在流已经开始后，按 OpenAI Responses 协议写出 response.failed SSE 事件。
//
// 必要性：一旦 SSE 头和任意数据（例如等待槽位时的 ping comment）已经 flush，
// HTTP 200 状态码就被固化。此后若网关需要回报错误，只能继续通过 SSE 事件传达。
// 通用的 `event: error` 帧不是 Responses 协议规定的终止事件，
// Codex CLI 等严格 SDK 会因为没收到 `response.completed/failed/incomplete/cancelled`
// 而抛出 "stream closed before response.completed"。
//
// 字段集对齐 apicompat.makeResponsesCompletedEvent：id/object/model/status/output/error。
// 故意不写 sequence_number：本函数被调用时无法可靠拿到当前流的 last sequence，
// 而 OpenAI spec 将 sequence_number 设为可选；省略避免破坏单调性约束。
//
// 返回 true 表示已尝试 SSE 写出（不论 Write 是否成功，caller 都应直接 return）。
// 返回 false 表示 writer 不支持 Flusher，无法以 SSE 形式回报错误；
// 此时 caller 也无法回退到 JSON（HTTP 200 已固化），通常意味着连接已经损坏，
// 应当让请求处理函数 return，由上层关闭连接。
func WriteResponsesFailedSSE(c *gin.Context, errType, code, message, requestID, model string) bool {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return false
	}

	payload, err := json.Marshal(ResponsesFailedEvent{
		Type: "response.failed",
		Response: ResponsesFailedBody{
			ID:        SynthesizeResponseID(requestID),
			Object:    "response",
			CreatedAt: time.Now().Unix(),
			Model:     strings.TrimSpace(model),
			Status:    "failed",
			Output:    []any{},
			Error: ResponsesFailedError{
				Code:    MapResponsesErrorCode(errType, code),
				Message: message,
			},
		},
	})
	if err != nil {
		_ = c.Error(err)
		return true
	}

	if _, err := fmt.Fprintf(c.Writer, "event: response.failed\ndata: %s\n\n", payload); err != nil {
		_ = c.Error(err)
		return true
	}
	flusher.Flush()
	return true
}

// InboundIsResponses 判断当前请求是否落在任意 Responses 路由上
// （不区分 root 还是 compact 变体）。
//
// 不能直接用 GetInboundEndpoint(c) == EndpointResponses 比较，因为
// GetInboundEndpoint/NormalizeInboundEndpoint 会把 compact 变体归一化为
// 单独的 EndpointResponsesCompact（而不是 EndpointResponses），
// 而本函数在这里只关心“是不是 Responses 家族的请求”，
// 不需要区分 root/compact，所以不能用那个等值比较。
//
// 这里改用 FullPath 的后缀/子串判断，一次性覆盖 root 和 compact 的所有变体：
//   - /v1/responses
//   - /v1/responses/compact
//   - /responses
//   - /responses/compact
//   - /backend-api/codex/responses
//   - /backend-api/codex/responses/compact
//
// 对于通配路由（如 "/v1/responses/*action"）注册的 FullPath 本身就带有
// "/responses/" 子串（例如 "/v1/responses/*action"），所以下面的
// strings.Contains(p, "/responses/") 分支同样能覆盖这些通配路由，
// 不需要额外处理通配符本身。
func InboundIsResponses(c *gin.Context) bool {
	if c == nil {
		return false
	}
	p := strings.TrimRight(c.FullPath(), "/")
	if p == "" && c.Request != nil && c.Request.URL != nil {
		p = strings.TrimRight(c.Request.URL.Path, "/")
	}
	if p == "" {
		return false
	}
	return strings.HasSuffix(p, "/responses") || strings.Contains(p, "/responses/")
}

// MapResponsesErrorCode 把内部 errType 映射为 Responses 协议常见的 error.code。
// 无明确映射时原样返回，保证至少可读。
func MapResponsesErrorCode(errType string, code ...string) string {
	if len(code) > 0 && code[0] != "" {
		return code[0]
	}
	switch errType {
	case "rate_limit_error":
		return "rate_limit_exceeded"
	case "invalid_request_error":
		return "invalid_request"
	case "permission_error":
		return "permission_denied"
	case "authentication_error":
		return "authentication_failed"
	case "upstream_error":
		return "upstream_error"
	case "server_error", "api_error", "":
		return "server_error"
	default:
		return errType
	}
}

// SynthesizeResponseID 使用显式关联 ID，避免读取旧业务 context。
func SynthesizeResponseID(requestID string) string {
	if requestID = strings.TrimSpace(requestID); requestID != "" {
		return "resp_" + strings.ReplaceAll(requestID, "-", "")
	}
	return "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}
