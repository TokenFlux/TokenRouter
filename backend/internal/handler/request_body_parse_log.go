package handler

import (
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"go.uber.org/zap"
)

// parseFailureSnippetLen 限制解析失败日志中的首尾片段；256 字节足以观察结构，
// 同时避免输出完整用户请求体。
const parseFailureSnippetLen = 256

// logRequestBodyParseFailure 记录 JSON 解析或校验失败的真实原因。客户端仍收到固定
// 错误文案，服务端日志仅记录底层错误、字节偏移、长度和转义后的首尾片段，
// 便于区分非法 JSON、截断请求体和被提前消费的请求体。
//
// 使用 gjson.ValidBytes 直接校验的调用点可以传入 nil，此时从 body 推导诊断错误。
func logRequestBodyParseFailure(reqLog *zap.Logger, body []byte, err error) {
	if reqLog == nil {
		return
	}
	if err == nil {
		err = service.DescribeInvalidJSON(body)
	}

	head := body
	var tail []byte
	if len(body) > parseFailureSnippetLen {
		head = body[:parseFailureSnippetLen]
		tail = body[len(body)-parseFailureSnippetLen:]
	}

	fields := []zap.Field{
		zap.Error(err),
		zap.Int("body_len", len(body)),
		zap.String("body_head", sanitizeBodySnippet(head)),
	}
	if len(tail) > 0 {
		fields = append(fields, zap.String("body_tail", sanitizeBodySnippet(tail)))
	}
	reqLog.Warn("parse request body failed", fields...)
}

// sanitizeBodySnippet 转义控制字符和非法 UTF-8，确保片段始终是可打印的单行日志。
func sanitizeBodySnippet(b []byte) string {
	return strconv.Quote(string(b))
}
