// 旧 HTTP/config 转接只组合本请求的 sink 和策略；流算法与状态归原生平台。
package service

import (
	"errors"
	"net/http"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/gin-gonic/gin"
)

type antigravityStreamResult = native.StreamResult
type antigravityClientWriter = native.ClientWriter

func (s *AntigravityGatewayService) antigravityResponseAdapter(c *gin.Context) *native.ResponseAdapter {
	opts := native.ResponseOptions{ReverseTools: func(body []byte) []byte { return reverseToolNamesIfPresent(c, body) }, ClaudeError: func(status int, kind, message string) error { return s.writeClaudeError(c, status, kind, message) }, CompatError: func(status int, kind, message string) error {
		return s.writeAntigravityCompatError(c, status, kind, message)
	}, MapCollectionError: func(err error) error { return s.mapAntigravityCompatCollectionError(c, err) }, Failover: func(body []byte) error {
		return &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, RetryableOnSameAccount: true}
	}, IsFailover: func(err error) bool { var value *UpstreamFailoverError; return errors.As(err, &value) }, MarkCommitted: func() { MarkResponseCommitted(c) }}
	if s.settingService != nil && s.settingService.cfg != nil {
		opts.MaxLineSize = s.settingService.cfg.Gateway.MaxLineSize
		opts.StreamDataIntervalTimeout = s.settingService.cfg.Gateway.StreamDataIntervalTimeout
		opts.StreamKeepaliveInterval = s.settingService.cfg.Gateway.StreamKeepaliveInterval
	}
	return &native.ResponseAdapter{Options: opts}
}
func newAntigravityClientWriter(w gin.ResponseWriter, flusher http.Flusher, prefix string) *antigravityClientWriter {
	return native.NewClientWriter(w, flusher, prefix)
}
func (s *AntigravityGatewayService) handleGeminiStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time) (*antigravityStreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleGeminiStreamingResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime)
}

func (s *AntigravityGatewayService) handleClaudeStreamingResponse(c *gin.Context, resp *http.Response, startTime time.Time, originalModel string) (*antigravityStreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleClaudeStreamingResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime, originalModel)
}
func (s *AntigravityGatewayService) extractImageInputSize(body []byte) string {
	return s.antigravityResponseAdapter(nil).ExtractImageInputSize(body)
}
func isImageGenerationModel(model string) bool { return native.IsImageGenerationModel(model) }
func (s *AntigravityGatewayService) handleChatCompletionsStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	includeUsage bool,
) (*antigravityStreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleChatCompletionsStreamingFromAntigravity(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime, originalModel, includeUsage)
}
func (s *AntigravityGatewayService) handleResponsesStreamingFromAntigravity(
	c *gin.Context,
	resp *http.Response,
	startTime time.Time,
	originalModel string,
	clientToolMapping apicompat.ResponsesClientToolMapping,
) (*antigravityStreamResult, error) {
	return s.antigravityResponseAdapter(c).HandleResponsesStreamingFromAntigravity(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime, originalModel, clientToolMapping)
}
func (s *AntigravityGatewayService) streamUpstreamResponse(c *gin.Context, resp *http.Response, startTime time.Time) *antigravityStreamResult {
	return s.antigravityResponseAdapter(c).StreamUpstreamResponse(upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), resp, startTime)
}
func (s *AntigravityGatewayService) extractSSEUsage(line string, usage *ClaudeUsage) {
	s.antigravityResponseAdapter(nil).ExtractSSEUsage(line, usage)
}

func (s *AntigravityGatewayService) unwrapV1InternalResponse(body []byte) ([]byte, error) {
	return s.antigravityResponseAdapter(nil).UnwrapV1InternalResponse(body)
}
