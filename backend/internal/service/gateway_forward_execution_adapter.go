package service

import (
	"bufio"
	"crypto/rand"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// forwardResponse 保留每条转换流的扫描器、64KiB 初始缓冲与既有大小限制。
func (s *GatewayService) forwardResponse(resp *http.Response) forwardcore.Response {
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)
	return forwardcore.Response{
		StatusCode: resp.StatusCode,
		Close:      func() { _ = resp.Body.Close() },
		Runtime:    bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		RequestID:  resp.Header.Get("x-request-id"),
		Headers:    resp.Header,
		Lines:      scanner,
	}
}
func (s *GatewayService) forwardOutput(c *gin.Context, responses bool) forwardcore.Output {
	return gatewayhttp.ForwardConversionOutput{
		Context: c, Filter: s.responseHeaderFilter, Responses: responses,
		Reverse: func(body []byte) []byte { return reverseToolNamesIfPresent(c, body) },
		Commit:  func() { MarkResponseCommitted(c) },
		Diagnostic: func(level, message string, err error, requestID, event string) {
			fields := []zap.Field{zap.String("request_id", requestID)}
			if err != nil {
				fields = append(fields, zap.Error(err))
			}
			if event != "" {
				fields = append(fields, zap.String("event_type", event))
			}
			if level == "info" {
				logger.L().Info(message, fields...)
			} else {
				logger.L().Warn(message, fields...)
			}
		},
	}
}

// legacyForwardExecutionResult 显式投影同步结果的全部字段，不复制转换状态。
func legacyForwardExecutionResult(v *forwardcore.Result) *ForwardResult {
	if v == nil {
		return nil
	}
	return &ForwardResult{
		RequestID:                   v.RequestID,
		UpstreamHeaders:             v.UpstreamHeaders,
		Usage:                       v.Usage,
		Model:                       v.Model,
		UpstreamModel:               v.UpstreamModel,
		Stream:                      v.Stream,
		Duration:                    v.Duration,
		FirstTokenMs:                v.FirstTokenMs,
		ClientDisconnect:            v.ClientDisconnect,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		UpstreamResponseServiceTier: v.UpstreamResponseServiceTier,
		ServiceTier:                 v.ServiceTier,
		ImageCount:                  v.ImageCount,
		ImageSize:                   v.ImageSize,
		ImageInputSize:              v.ImageInputSize,
		ImageOutputSize:             v.ImageOutputSize,
		ImageOutputSizes:            v.ImageOutputSizes,
		ImageSizeSource:             v.ImageSizeSource,
		ImageSizeBreakdown:          v.ImageSizeBreakdown,
		SearchCount:                 v.SearchCount,
		AudioUsage:                  v.AudioUsage,
	}
}

// nativeForwardExecutionResult 显式投影同步结果的全部字段，不复制转换状态。
func nativeForwardExecutionResult(v *ForwardResult) *forwardcore.Result {
	if v == nil {
		return nil
	}
	return &forwardcore.Result{
		RequestID:                   v.RequestID,
		UpstreamHeaders:             v.UpstreamHeaders,
		Usage:                       v.Usage,
		Model:                       v.Model,
		UpstreamModel:               v.UpstreamModel,
		Stream:                      v.Stream,
		Duration:                    v.Duration,
		FirstTokenMs:                v.FirstTokenMs,
		ClientDisconnect:            v.ClientDisconnect,
		ReasoningEffort:             v.ReasoningEffort,
		RequestedReasoningEffort:    v.RequestedReasoningEffort,
		UpstreamResponseServiceTier: v.UpstreamResponseServiceTier,
		ServiceTier:                 v.ServiceTier,
		ImageCount:                  v.ImageCount,
		ImageSize:                   v.ImageSize,
		ImageInputSize:              v.ImageInputSize,
		ImageOutputSize:             v.ImageOutputSize,
		ImageOutputSizes:            v.ImageOutputSizes,
		ImageSizeSource:             v.ImageSizeSource,
		ImageSizeBreakdown:          v.ImageSizeBreakdown,
		SearchCount:                 v.SearchCount,
		AudioUsage:                  v.AudioUsage,
	}
}
