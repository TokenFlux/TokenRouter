// 本文件只投影旧 HTTP/配置/图片观测，平台响应处理没有 Gin 依赖。
package service

import (
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/gin-gonic/gin"
)

func (s *GeminiMessagesCompatService) geminiResponseAdapter(c *gin.Context) *gemininative.ResponseAdapter {
	return &gemininative.ResponseAdapter{Options: gemininative.ResponseOptions{GoogleError: func(status int, message string) error { return s.writeGoogleError(c, status, message) },
		ReadBody: func(body io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(body, s.cfg, c, openAITooLargeError)
		}, WriteHeaders: func(dst, src http.Header) { provider.WriteFilteredHeaders(dst, src, s.responseHeaderFilter) }, DebugHeaders: s.cfg != nil && s.cfg.Gateway.GeminiDebugResponseHeaders, HasHeaderFilter: s.responseHeaderFilter != nil,
		ObserveImages: func(body []byte) { observeGeminiImageOutputs(c, body) }, ReverseTools: func(body []byte) []byte { return reverseToolNamesIfPresent(c, body) }, ClaudeError: func(status int, kind, message string) error { return s.writeClaudeError(c, status, kind, message) }, ChatError: func(status int, kind, message string) error {
			return s.writeChatCompletionsError(c, status, kind, message)
		}, CompatError: func(protocol gemininative.OpenAICompatProtocol, status int, kind, message string) error {
			return s.writeGeminiOpenAICompatError(c, protocol, status, kind, message)
		},
	}}
}
