package openai

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	// OpenAIUpstreamHTTP2StreamErrorCode 表示请求开始后上游 HTTP/2 响应流被重置。
	OpenAIUpstreamHTTP2StreamErrorCode = "upstream_http2_stream_error"
	OpenAIUpstreamStreamReadErrorCode  = "upstream_stream_read_error"
	// OpenAIUpstreamStreamTruncatedCode 表示上游 SSE 流在任何终止信号前干净关闭。
	// 干净 EOF 不携带传输错误；没有此分类时，截断生成会被误认为成功。
	OpenAIUpstreamStreamTruncatedCode = "upstream_stream_truncated"
)

// ErrOpenAIUpstreamStreamTruncated 表示上游 SSE 流在任何终止信号前以 EOF 结束，且没有读取错误。
var ErrOpenAIUpstreamStreamTruncated = errors.New("upstream stream ended before any terminal chunk")

type UpstreamStreamReadError struct {
	cause         error
	clientCode    string
	clientMessage string
}

func (e *UpstreamStreamReadError) Error() string {
	return fmt.Sprintf("stream usage incomplete: %v", e.cause)
}

func (e *UpstreamStreamReadError) Unwrap() error { return e.cause }

func NewUpstreamStreamReadError(err error) error {
	code, message := ClassifyUpstreamStreamReadError(err)
	return &UpstreamStreamReadError{
		cause:         err,
		clientCode:    code,
		clientMessage: message,
	}
}

// ShouldClassifyUpstreamStreamReadError 只把真实上游传输读取失败交给重试逻辑，
// 客户端取消、请求超时和本地响应体大小限制必须保留原始语义。
func ShouldClassifyUpstreamStreamReadError(err error, localBodyLimit error, contexts ...context.Context) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, localBodyLimit) {
		return false
	}
	for _, ctx := range contexts {
		if ctx != nil && ctx.Err() != nil {
			return false
		}
	}
	return true
}

// OpenAIUpstreamStreamReadErrorDetails 返回上游流读取失败对应的稳定、安全对客分类。
func OpenAIUpstreamStreamReadErrorDetails(err error) (code, message string, ok bool) {
	var streamErr *UpstreamStreamReadError
	if !errors.As(err, &streamErr) || streamErr == nil {
		return "", "", false
	}
	return streamErr.clientCode, streamErr.clientMessage, true
}

func ClassifyUpstreamStreamReadError(err error) (code, message string) {
	if err != nil {
		if errors.Is(err, ErrOpenAIUpstreamStreamTruncated) {
			return OpenAIUpstreamStreamTruncatedCode, "Upstream response stream ended before completion"
		}
		lower := strings.ToLower(err.Error())
		// net/http 的 HTTP/2 流错误类型未导出，只匹配其稳定传输特征，
		// 绝不把包含 stream ID 等细节的原始错误返回客户端。
		if strings.Contains(lower, "stream error: stream id ") ||
			(strings.Contains(lower, "http2:") && strings.Contains(lower, "stream")) {
			return OpenAIUpstreamHTTP2StreamErrorCode, "Upstream HTTP/2 stream failed"
		}
	}
	return OpenAIUpstreamStreamReadErrorCode, "Upstream response stream was interrupted"
}
