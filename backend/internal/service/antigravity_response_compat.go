// 旧 HTTP/config 转接只组合本请求的 sink 和策略；流算法与状态归原生平台。
package service

import (
	"errors"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/gin-gonic/gin"
)

func (s *AntigravityGatewayService) antigravityResponseAdapter(c *gin.Context) *antigravity.ResponseAdapter {
	opts := antigravity.ResponseOptions{ReverseTools: func(body []byte) []byte { return reverseToolNamesIfPresent(c, body) }, ClaudeError: func(status int, kind, message string) error { return s.writeClaudeError(c, status, kind, message) }, CompatError: func(status int, kind, message string) error {
		return s.writeAntigravityCompatError(c, status, kind, message)
	}, MapCollectionError: func(err error) error { return s.mapAntigravityCompatCollectionError(c, err) }, Failover: func(body []byte) error {
		return &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, RetryableOnSameAccount: true}
	}, IsFailover: func(err error) bool { var value *forwardcore.UpstreamFailoverError; return errors.As(err, &value) }, MarkCommitted: func() { gatewayhttp.MarkResponseCommitted(c) }}
	if s.settingService != nil && s.settingService.Antigravity != nil {
		opts.MaxLineSize = s.settingService.Antigravity.MaxLineSize
		opts.StreamDataIntervalTimeout = s.settingService.Antigravity.StreamDataIntervalTimeout
		opts.StreamKeepaliveInterval = s.settingService.Antigravity.StreamKeepaliveInterval
	}
	return &antigravity.ResponseAdapter{Options: opts}
}
func newAntigravityClientWriter(w gin.ResponseWriter, flusher http.Flusher, prefix string) *antigravity.ClientWriter {
	return antigravity.NewClientWriter(w, flusher, prefix)
}
func (s *AntigravityGatewayService) handleGeminiStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time) (*antigravity.StreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleGeminiStreamingResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime)
}

func (s *AntigravityGatewayService) handleClaudeStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) (*antigravity.StreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleClaudeStreamingResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime, originalModel)
}
func (s *AntigravityGatewayService) extractImageInputSize(body []byte) string {
	return s.antigravityResponseAdapter(nil).ExtractImageInputSize(body)
}

func (s *AntigravityGatewayService) handleChatCompletionsStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	includeUsage bool,
) (*antigravity.StreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleChatCompletionsStreamingFromAntigravity(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime, originalModel, includeUsage)
}
func (s *AntigravityGatewayService) handleResponsesStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*antigravity.StreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleResponsesStreamingFromAntigravity(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime, originalModel, clientToolMapping)
}
func (s *AntigravityGatewayService) streamUpstreamResponse(c *gin.Context, resp *http.Response, startTime time.Time) *antigravity.StreamResult {
	return s.antigravityResponseAdapter(c).StreamUpstreamResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime)
}
func (s *AntigravityGatewayService) extractSSEUsage(line string, usage *upstream.TokenUsage) {
	s.antigravityResponseAdapter(nil).ExtractSSEUsage(line, usage)
}

func (s *AntigravityGatewayService) unwrapV1InternalResponse(body []byte) ([]byte, error) {
	return s.antigravityResponseAdapter(nil).UnwrapV1InternalResponse(body)
}
