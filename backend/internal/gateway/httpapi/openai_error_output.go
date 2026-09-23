package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h OpenAIErrorOutput) WriteAnthropicStreamingError(c *gin.Context, status int, kind, message string, started bool) {
	if !started {
		WriteAnthropicError(c, status, kind, "", message)
		return
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		payload, _ := json.Marshal(gin.H{"type": "error", "error": gin.H{"type": kind, "message": message}})
		// 保留此兼容入口的写失败处置；不得附带通用 Messages 的额外观测或终态改写。
		_, _ = fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", payload)
		flusher.Flush()
	}
}

func (h OpenAIErrorOutput) WriteStreamingErrorWithCode(
	c *gin.Context,
	status int,
	errType string,
	code string,
	message string,
	streamStarted bool,
	countTowardsSLA bool,
) {
	// body-signal compact 心跳可能已把响应头提交为 200：先停心跳（建立
	// happens-before，接管 ResponseWriter），并升级为流内错误处理。
	if h.stopCompact(c) {
		streamStarted = true
	}
	if streamStarted {
		if countTowardsSLA {
			h.markFailure(c, errType, code, message, status)
		} else {
			h.markStream(c, errType, message, status)
		}
		// /v1/responses 的严格 SDK（Codex CLI）要求终止事件必须属于
		// response.completed/failed/incomplete/cancelled 集合。
		// 通用 `event: error` 帧不被识别为终止事件，会导致
		// "stream closed before response.completed"。
		if InboundIsResponses(c) {
			if WriteResponsesFailedSSE(c, errType, code, message, ErrorRequestID(c), ErrorRequestModel(c)) {
				return
			}
		}
		// 流已开始，发送原协议错误事件后结束。
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			errorObject := gin.H{"type": errType, "message": message}
			if code != "" {
				errorObject["code"] = code
			}
			payload, err := json.Marshal(gin.H{"error": errorObject})
			if err != nil {
				payload = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
			}
			errorEvent := "event: error\ndata: " + string(payload) + "\n\n"
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}

	// 普通响应保持原 JSON 形状和状态码。
	if code == "" {
		h.WriteError(c, status, errType, message)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{
		"type": errType, "code": code, "message": message,
	}})
}

// OpenAIErrorOutput 不保存请求和 Handler，回调只负责同步的 HTTP 观测与心跳接管。
type OpenAIErrorOutput struct {
	stopCompact func(*gin.Context) bool
	markStream  func(*gin.Context, string, string, int)
	markFailure func(*gin.Context, string, string, string, int)
	metadata    func(*gin.Context) (string, string)
}

// DefaultOpenAIErrorOutput 供生产 HTTP/WS/媒体适配复用原有输出与观测顺序。
func DefaultOpenAIErrorOutput() OpenAIErrorOutput {
	return OpenAIErrorOutput{stopCompact: StopOpenAICompactSSEKeepaliveCommitted, markStream: MarkOpsStreamError, markFailure: MarkOpsStreamFailure, metadata: func(c *gin.Context) (string, string) { return ErrorRequestID(c), ErrorRequestModel(c) }}
}

func (h OpenAIErrorOutput) WriteError(c *gin.Context, status int, kind, message string) {
	writeOpenAIRequestError(c, status, kind, message, h.stopCompact, h.markStream, h.metadata)
}
func (h OpenAIErrorOutput) StreamError(c *gin.Context, status int, kind, message string, started bool) {
	h.WriteStreamingErrorWithCode(c, status, kind, "", message, started, false)
}

// EnsureFallback 保留没有上游错误对象时的原兜底入口。
func (h OpenAIErrorOutput) EnsureFallback(c *gin.Context, started bool) bool {
	return h.EnsureResponse(c, started, nil)
}
func (h OpenAIErrorOutput) WriteFailoverExhausted(c *gin.Context, failure *OpenAIFailoverError, started bool, rules ErrorRuleMatcher, hooks FailoverErrorHooks) {
	WriteOpenAIFailoverExhausted(c, failure, started, rules, hooks, h.StreamError)
}
