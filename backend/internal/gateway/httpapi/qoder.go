package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ParsedRequest 是 HTTP 读取与严格字段验证后的只读请求投影。
type ParsedRequest struct {
	Body      []byte
	Model     string
	Stream    bool
	StartedAt time.Time
}

// HTTPFailure 只描述协议错误，不携带可变业务实体。
type HTTPFailure struct {
	Status        int
	Type, Message string
	RetryAfter    int
}

func (e *HTTPFailure) Error() string { return e.Message }

// QoderChatHandler 的装配回调只加载已有认证/路由上下文；执行循环始终由 gateway 拥有。
type QoderChatHandler struct {
	Preflight func(*gin.Context) error
	UseCase   *gateway.QoderUseCase
	Prepare   func(*gin.Context, ParsedRequest) (gateway.Request, gateway.RequestPorts, error)
	Failure   func(*gin.Context, error) *HTTPFailure
}

// ChatCompletions 保留原 Chat URL 的读取、错误 envelope 和 SSE 收尾。
func (h *QoderChatHandler) ChatCompletions(c *gin.Context) {
	start := time.Now()
	if h.Preflight != nil {
		if err := h.Preflight(c); err != nil {
			h.fail(c, err, false)
			return
		}
	}
	body, err := httpx.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			h.writeError(c, &HTTPFailure{Status: 413, Type: "invalid_request_error", Message: BodyTooLargeMessage(maxErr.Limit)}, false)
			return
		}
		h.writeError(c, &HTTPFailure{Status: 400, Type: "invalid_request_error", Message: "Failed to read request body"}, false)
		return
	}
	if len(body) == 0 {
		h.writeError(c, &HTTPFailure{Status: 400, Type: "invalid_request_error", Message: "Request body is empty"}, false)
		return
	}
	if !gjson.ValidBytes(body) {
		h.writeError(c, &HTTPFailure{Status: 400, Type: "invalid_request_error", Message: "Failed to parse request body"}, false)
		return
	}
	model := gjson.GetBytes(body, "model")
	if !model.Exists() || model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		h.writeError(c, &HTTPFailure{Status: 400, Type: "invalid_request_error", Message: "model is required"}, false)
		return
	}
	stream, valid := ParseOpenAICompatibleStream(body)
	if !valid {
		h.writeError(c, &HTTPFailure{Status: 400, Type: "invalid_request_error", Message: InvalidStreamFieldTypeMessage}, false)
		return
	}
	request, ports, err := h.Prepare(c, ParsedRequest{Body: body, Model: strings.TrimSpace(model.String()), Stream: stream, StartedAt: start})
	if err != nil {
		h.fail(c, err, stream)
		return
	}
	output := &gateway.OutputTracker{Sink: ResponseSink{Writer: c.Writer}}
	err = h.UseCase.Run(c.Request.Context(), request, ports, output)
	if err != nil {
		h.fail(c, err, stream)
	}
}
func (h *QoderChatHandler) fail(c *gin.Context, err error, stream bool) {
	if c.Request.Context().Err() != nil {
		return
	}
	failure := &HTTPFailure{Status: 502, Type: "upstream_error", Message: "Upstream request failed"}
	if h.Failure != nil {
		failure = h.Failure(c, err)
		if failure == nil {
			return
		}
	} else {
		var typed *HTTPFailure
		if errors.As(err, &typed) {
			failure = typed
		}
	}
	h.writeError(c, failure, stream)
}
func (h *QoderChatHandler) writeError(c *gin.Context, f *HTTPFailure, stream bool) {
	if f.RetryAfter > 0 {
		c.Header("Retry-After", strconv.Itoa(f.RetryAfter))
	}
	if stream && c.Writer.Written() {
		_, _ = c.Writer.WriteString(`data: {"error":{"type":` + strconv.Quote(f.Type) + `,"message":` + strconv.Quote(f.Message) + "}}\n\ndata: [DONE]\n\n")
		c.Writer.Flush()
		return
	}
	c.JSON(f.Status, gin.H{"error": gin.H{"type": f.Type, "message": f.Message}})
}

const InvalidStreamFieldTypeMessage = "invalid stream field type"

// ParseOpenAICompatibleStream 不宽松接受数字或字符串，保持既有缺省与 null 语义。
func ParseOpenAICompatibleStream(body []byte) (bool, bool) {
	v := gjson.GetBytes(body, "stream")
	if v.Exists() && v.Type != gjson.True && v.Type != gjson.False {
		return false, false
	}
	return v.Bool(), true
}

// BodyLimitLabel 和 BodyTooLargeMessage 供旧入口委托同一错误文本。
func BodyLimitLabel(limit int64) string {
	if limit >= 1024*1024 {
		return fmt.Sprintf("%dMB", limit/(1024*1024))
	}
	return fmt.Sprintf("%dB", limit)
}
func BodyTooLargeMessage(limit int64) string {
	return fmt.Sprintf("Request body too large, limit is %s", BodyLimitLabel(limit))
}
