// 透传兼容层只提供原 HTTP、监控和账号策略，原生读取器不反向读取旧实体。
package service

import (
	"context"
	"net/http"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func (s *OpenAIGatewayService) nativePassthroughOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount) upstreamopenai.PassthroughOptions {
	stream := upstreamopenai.StreamOptions{
		NativeOpenAI: account != nil && account.Record.Platform == capability.PlatformOpenAI,
		MaxLineSize:  defaultMaxLineSize,
		TTFTMode:     func() string { return s.openAITTFTMode(ctx) },
		Logf: func(format string, args ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, args...)
		},
		MarkCommitted:       func() { gatewayhttp.MarkResponseCommitted(c) },
		ClientOutputStarted: func(started bool) bool { return gatewayhttp.OpenAIStreamClientOutputStarted(c, started) },
		ClearDisconnect:     func() { s.clearOpenAIProxyStreamDisconnect(account) },
		RecordDisconnect:    func(err error, id string) { s.recordOpenAIProxyStreamDisconnect(account, err, id) },
		TerminalSideEffects: func(body []byte, message string, headers http.Header, model string) {
			s.handleOpenAIStreamTerminalAccountSideEffects(c, account, body, message, headers, model)
		},
		Failover: func(id string, body []byte, message string) error {
			return s.newOpenAIStreamFailoverError(c, account, true, id, body, message)
		},
		RecordError: func(id, kind string, body []byte, message string) {
			s.recordOpenAIStreamUpstreamError(c, account, true, id, kind, body, message)
		},
		CompactFallback: func(body []byte, message string) error { return newOpenAICompactFallbackSignal(c, body, message) },
		ErrorRule: func(body []byte, message string) (int, string, string, bool) {
			return applyOpenAIStreamFailedErrorPassthroughRule(c, account.Record.Platform, body, message)
		},
		CapacitySuppressed: func(id, event string) {
			logOpenAICapacityFailoverSuppressed(ctx, account, "passthrough_sse", id, event)
		},
		MarkCyber: func(value upstreamopenai.CyberObservation) {
			gatewayhttp.MarkOpsCyberPolicy(c, moderationflow.Mark{Code: value.Code, Message: value.Message, Body: value.Body, UpstreamStatus: value.UpstreamStatus, UpstreamInTok: value.UpstreamInTok, UpstreamOutTok: value.UpstreamOutTok})
		},
		RestoreNamespace: func(body []byte) ([]byte, error) { return restoreOpenAIResponsesNamespacePayload(c, body) },
		RestoreToolNames: func(body []byte, event string) []byte { return restoreCodexToolNamesFromSSEContext(c, body, event) },
		EmptyCompleted: func(id string) error {
			return gatewayhttp.NewOpenAIResponsesEmptyCompletedFailoverError(c, upstreamErrorAccount(account), id)
		},
		BuildOpenAIResponseFailedSSE:        gatewayprovider.BuildOpenAIResponseFailedSSE,
		WrapOpenAIUpstreamWarningIfCyber:    gatewayprovider.WrapOpenAIUpstreamWarningIfCyber,
		TruncateString:                      logredact.TruncateUTF8,
		OpenAIStreamDataStartsTTFT:          gatewayprovider.OpenAIStreamDataStartsTTFT,
		OpenAIStreamEventIsTerminalWithType: openAIStreamEventIsTerminalWithType,
	}
	if account != nil {
		stream.AccountID = account.Record.ID
	}
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		stream.MaxLineSize = s.cfg.Gateway.MaxLineSize
	}
	stream.MarkTime = func(moment upstreamopenai.StreamTime) {
		switch moment {
		case upstreamopenai.StreamTimeFlush:
			gatewayhttp.MarkOpsTimestamp(c, telemetry.FirstDownstreamFlushAt)
		case upstreamopenai.StreamTimeData:
			gatewayhttp.MarkOpsTimestamp(c, telemetry.FirstSSEDataAt)
		case upstreamopenai.StreamTimeVisible:
			gatewayhttp.MarkOpsTimestamp(c, telemetry.FirstVisibleOutputAt)
		}
	}
	nonstream := s.nativeNonStreamOptions(ctx, c, account)
	nonstream.TerminalFailover = func(resp *http.Response, event string, body []byte, message, model string) error {
		if failure := s.nonStreamingTerminalFailureFailover(c, resp, account, true, event, body, message, model); failure != nil {
			return failure
		}
		return nil
	}
	return upstreamopenai.PassthroughOptions{
		StreamOptions: stream,
		NonStream:     nonstream,
		Headers: func(dst, src http.Header) {
			gatewayhttp.WriteOpenAIPassthroughResponseHeaders(dst, src, s.responseHeaderFilter)
		},
		StartKeepalive: func(headers http.Header) func() {
			// 原 Header 在启动心跳前已设置；这里只把本次输出适配中的元数据同步至 HTTP。
			for key, values := range headers {
				c.Writer.Header()[key] = append([]string(nil), values...)
			}
			if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
				return gatewayhttp.StartOpenAISSEKeepalive(c, time.Duration(s.cfg.Gateway.StreamKeepaliveInterval)*time.Second)
			}
			return func() {}
		},
		MissingUsage: func(resp *http.Response, usage *openai.ForwardUsage, event string, disconnected bool) {
			if resp == nil {
				return
			}
			var id int64
			if account != nil {
				id = account.Record.ID
			}
			gatewaytelemetry.SuccessMissingUsage(ctx, id, resp.StatusCode, usage, event, disconnected)
		},
		MissingTerminal: func(id string) {
			logging.FromContext(ctx).With(zap.String("component", "service.openai_gateway"), zap.Int64("account_id", account.Record.ID), zap.String("upstream_request_id", id)).Info("OpenAI passthrough 上游流在未收到 [DONE] 时结束，疑似断流")
		},
		PassthroughFailoverWithModel: func(id string, body []byte, message, model string) error {
			return s.newOpenAIStreamFailoverErrorWithModel(c, account, true, id, body, message, model)
		},
	}
}
