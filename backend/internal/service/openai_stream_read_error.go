// 读取错误仅引用原生唯一实现，本地大小限制仍由 HTTP 入口明确传入。
package service

import (
	"context"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

const OpenAIUpstreamHTTP2StreamErrorCode = native.OpenAIUpstreamHTTP2StreamErrorCode
const OpenAIUpstreamStreamReadErrorCode = native.OpenAIUpstreamStreamReadErrorCode
const OpenAIUpstreamStreamTruncatedCode = native.OpenAIUpstreamStreamTruncatedCode

var ErrOpenAIUpstreamStreamTruncated = native.ErrOpenAIUpstreamStreamTruncated

func newOpenAIUpstreamStreamReadError(err error) error { return native.NewUpstreamStreamReadError(err) }
func shouldClassifyOpenAIUpstreamStreamReadError(err error, contexts ...context.Context) bool {
	return native.ShouldClassifyUpstreamStreamReadError(err, ErrUpstreamResponseBodyTooLarge, contexts...)
}
func OpenAIUpstreamStreamReadErrorDetails(err error) (code, message string, ok bool) {
	return native.OpenAIUpstreamStreamReadErrorDetails(err)
}
func classifyOpenAIUpstreamStreamReadError(err error) (code, message string) {
	return native.ClassifyUpstreamStreamReadError(err)
}
