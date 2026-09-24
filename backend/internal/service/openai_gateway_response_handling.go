package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"

	"go.uber.org/zap"
)

// openaiStreamingResult streaming response result
type openaiStreamingResult struct {
	// 原生执行结果独立报告语义输出、用量存在和 HTTP/重试提交，旧资金入口不读取新增字段。
	served, hasUsage, httpCommitted, retryCommitted, clientDisconnected, observedOnly bool
	firstSemanticOutput                                                               *time.Duration
	usage                                                                             *s09openai.ForwardUsage
	firstTokenMs                                                                      *int
	responseID                                                                        string
	imageCount                                                                        int
	imageOutputSizes                                                                  []string
	searchCount                                                                       int
}

type openaiNonStreamingResult struct {
	served bool
	*s09openai.ForwardUsage
	usage            *s09openai.ForwardUsage
	responseID       string
	imageCount       int
	imageOutputSizes []string
	searchCount      int
}

func (s *OpenAIGatewayService) handleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *gatewayprovider.ExecutionAccount, startTime time.Time, originalModel, mappedModel string) (*openaiStreamingResult, error) {
	return s.handleStreamingResponseWithReasoning(ctx, resp, c, account, startTime, originalModel, mappedModel, "")
}

// 旧调用保持失败时的返回约定；新单次执行读取同一解析器保存的已观测结果。
func (s *OpenAIGatewayService) handleStreamingResponseWithReasoning(ctx context.Context, resp *http.Response, c *gin.Context, account *gatewayprovider.ExecutionAccount, startTime time.Time, originalModel, mappedModel, reasoningEffort string) (*openaiStreamingResult, error) {
	v, err := s.readStreamingResponseObservation(ctx, resp, c, account, startTime, originalModel, mappedModel, reasoningEffort)
	if err != nil && v != nil && v.observedOnly {
		return nil, err
	}
	return v, err
}
func (s *OpenAIGatewayService) readStreamingResponseObservation(ctx context.Context, resp *http.Response, c *gin.Context, account *gatewayprovider.ExecutionAccount, startTime time.Time, originalModel, mappedModel, reasoningEffort string) (*openaiStreamingResult, error) {
	result, err := openai.ReadStreamingResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeResponseStreamOptions(ctx, c, account, reasoningEffort), startTime, originalModel, mappedModel, reasoningEffort)
	if result == nil {
		return nil, err
	}
	return &openaiStreamingResult{
		served:              result.Served,
		hasUsage:            result.HasUsage,
		httpCommitted:       result.HttpCommitted,
		retryCommitted:      result.RetryCommitted,
		clientDisconnected:  result.ClientDisconnected,
		observedOnly:        result.ObservedOnly,
		firstSemanticOutput: result.FirstSemanticOutput,
		usage:               result.Usage,
		firstTokenMs:        result.FirstTokenMs,
		responseID:          result.ResponseID,
		imageCount:          result.ImageCount,
		imageOutputSizes:    result.ImageOutputSizes,
		searchCount:         result.SearchCount,
	}, err
}

func (s *OpenAIGatewayService) bindHTTPResponseAccount(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, responseID string) {
	if s == nil || account == nil || account.Record.ID <= 0 {
		return
	}
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return
	}
	store := s.ResponseStateStore()
	if store == nil {
		return
	}
	groupID := getOpenAIGroupIDFromContext(c)
	ttl := s.OpenAIHTTPResponseStickyTTL()
	gatewayprovider.LogOpenAIWSBindResponseAccountWarn(groupID, account.Record.ID, responseID, store.BindResponseAccount(ctx, groupID, responseID, account.Record.ID, ttl))
	if owner, ok := gatewayhttp.ResponseOwnerFromContext(c); ok {
		if err := store.BindHTTPResponseOwner(ctx, groupID, responseID, owner.UserID, owner.APIKeyID, s.OpenAIHTTPResponseStickyTTL()); err != nil {
			logging.L().Warn(
				"openai.http_bind_response_owner_failed",
				zap.Int64("group_id", groupID),
				zap.Int64("account_id", account.Record.ID),
				zap.Int64("user_id", owner.UserID),
				zap.Int64("api_key_id", owner.APIKeyID),
				zap.String("response_id", gatewayprovider.TruncateOpenAIWSLogValue(responseID, gatewayprovider.OpenAIWSIDValueMaxLen)),
				zap.Error(err),
			)
		}
	}
}

func (s *OpenAIGatewayService) handleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *gatewayprovider.ExecutionAccount, originalModel, mappedModel string) (*openaiNonStreamingResult, error) {
	result, err := openai.ReadNonStreamingResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeNonStreamOptions(ctx, c, account), originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResult{served: result.Served, ForwardUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes, searchCount: result.SearchCount}, err
}

func isEventStreamResponse(header http.Header) bool {
	contentType := strings.ToLower(header.Get("Content-Type"))
	return strings.Contains(contentType, "text/event-stream")
}

func (s *OpenAIGatewayService) handleSSEToJSON(ctx context.Context, resp *http.Response, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, originalModel, mappedModel string) (*openaiNonStreamingResult, error) {
	result, err := openai.ReadSSEAsJSON(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeNonStreamOptions(ctx, c, account), body, originalModel, mappedModel)
	if result == nil {
		return nil, err
	}
	return &openaiNonStreamingResult{served: result.Served, ForwardUsage: result.Usage, usage: result.Usage, responseID: result.ResponseID, imageCount: result.ImageCount, imageOutputSizes: result.ImageOutputSizes, searchCount: result.SearchCount}, err
}

func (s *OpenAIGatewayService) writeOpenAINonStreamingProtocolError(resp *http.Response, c *gin.Context, message string) error {
	message = logredact.SanitizeUpstreamQueries(strings.TrimSpace(message))
	if message == "" {
		message = "Upstream returned an invalid non-streaming response"
	}
	gatewayhttp.SetOpsUpstreamError(c, http.StatusBadGateway, message, "")
	// body-signal compact 心跳可能已把响应头提交为 200，此时只能以
	// response.failed 终止事件回传错误，不能再写 JSON+状态码。
	if gatewayhttp.OpenAICompactClientWantsStream(c) && gatewayhttp.StopOpenAICompactSSEKeepaliveCommitted(c) {
		gatewayhttp.WriteOpenAICompactSSEFailureMessage(c, http.StatusBadGateway, "upstream_error", message, gatewayhttp.MarkOpsStreamError)
		return fmt.Errorf("non-streaming openai protocol error: %s", message)
	}
	provider.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusBadGateway, gin.H{
		"error": gin.H{
			"type":    "upstream_error",
			"message": message,
		},
	})
	return fmt.Errorf("non-streaming openai protocol error: %s", message)
}
