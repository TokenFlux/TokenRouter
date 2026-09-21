//go:build unit

package service

import (
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/gin-gonic/gin"
)

// 旧私有输出名称仅供既有契约测试调用，生产入口直接使用原生转换实现。
func (s *GatewayService) handleResponsesBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*forwardcore.MessagesResult, error) {
	result, err := forwardcore.ResponsesBuffered(s.forwardResponse(resp), s.forwardOutput(c, true), originalModel, mappedModel, reasoningEffort, startTime, clientToolMapping)
	return legacyForwardExecutionResult(result), err
}
func (s *GatewayService) handleResponsesStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	clientToolMapping bridge.ResponsesClientToolMapping,
) (*forwardcore.MessagesResult, error) {
	result, err := forwardcore.ResponsesStreaming(s.forwardResponse(resp), s.forwardOutput(c, true), originalModel, mappedModel, reasoningEffort, startTime, clientToolMapping)
	return legacyForwardExecutionResult(result), err
}
func (s *GatewayService) handleCCBufferedFromAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
) (*forwardcore.MessagesResult, error) {
	result, err := forwardcore.ChatBuffered(s.forwardResponse(resp), s.forwardOutput(c, false), originalModel, mappedModel, reasoningEffort, startTime)
	return legacyForwardExecutionResult(result), err
}
func (s *GatewayService) handleCCStreamingFromAnthropic(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
	includeUsage bool,
) (*forwardcore.MessagesResult, error) {
	result, err := forwardcore.ChatStreaming(s.forwardResponse(resp), s.forwardOutput(c, false), originalModel, mappedModel, reasoningEffort, startTime, includeUsage)
	return legacyForwardExecutionResult(result), err
}
