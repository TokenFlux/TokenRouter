package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
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

func (p *OpenAIResponseOutput) PassthroughOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount) upstreamopenai.PassthroughOptions {
	stream := upstreamopenai.StreamOptions{
		NativeOpenAI: account != nil && account.Record.Platform == capability.PlatformOpenAI,
		MaxLineSize:  openAIResponseDefaultMaxLineSize,
		TTFTMode:     func() string { return p.TTFTMode(ctx) },
		Logf: func(format string, args ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, args...)
		},
		MarkCommitted:       func() { MarkResponseCommitted(c) },
		ClientOutputStarted: func(started bool) bool { return OpenAIStreamClientOutputStarted(c, started) },
		ClearDisconnect:     func() { gatewayprovider.ClearProxyStreamDisconnect(p.ProxyCircuit, account) },
		RecordDisconnect: func(err error, id string) {
			gatewayprovider.RecordProxyStreamDisconnect(p.ProxyCircuit, account, err, id)
		},
		TerminalSideEffects: func(body []byte, message string, headers http.Header, model string) {
			p.TerminalAccountEffects(c, account, body, message, headers, model)
		},
		Failover: func(id string, body []byte, message string) error {
			return p.NewStreamFailure(c, account, true, id, body, message)
		},
		RecordError: func(id, kind string, body []byte, message string) {
			p.RecordStreamError(c, account, true, id, kind, body, message)
		},
		CompactFallback: func(body []byte, message string) error { return NewOpenAICompactFailure(c, body, message) },
		ErrorRule: func(body []byte, message string) (int, string, string, bool) {
			return ApplyOpenAIStreamFailedErrorRule(c, account.Record.Platform, body, message)
		},
		CapacitySuppressed: func(id, event string) {
			LogOpenAICapacityFailoverSuppressed(ctx, account, "passthrough_sse", id, event)
		},
		MarkCyber: func(value upstreamopenai.CyberObservation) {
			MarkOpsCyberPolicy(c, moderationflow.Mark{Code: value.Code, Message: value.Message, Body: value.Body, UpstreamStatus: value.UpstreamStatus, UpstreamInTok: value.UpstreamInTok, UpstreamOutTok: value.UpstreamOutTok})
		},
		RestoreNamespace: func(body []byte) ([]byte, error) { return RestoreOpenAIResponsesNamespacePayload(c, body) },
		RestoreToolNames: func(body []byte, event string) []byte {
			return RestoreCodexToolNamesFromSSEContext(c, body, event)
		},
		EmptyCompleted: func(id string) error {
			return NewOpenAIResponsesEmptyCompletedFailoverError(c, ExecutionErrorAccount(account), id)
		},
		BuildOpenAIResponseFailedSSE:        gatewayprovider.BuildOpenAIResponseFailedSSE,
		WrapOpenAIUpstreamWarningIfCyber:    gatewayprovider.WrapOpenAIUpstreamWarningIfCyber,
		TruncateString:                      logredact.TruncateUTF8,
		OpenAIStreamDataStartsTTFT:          gatewayprovider.OpenAIStreamDataStartsTTFT,
		OpenAIStreamEventIsTerminalWithType: openai.OpenAIStreamEventIsTerminalWithType,
	}
	if account != nil {
		stream.AccountID = account.Record.ID
	}
	if p.Options.Configured && p.Options.MaxLineSize > 0 {
		stream.MaxLineSize = p.Options.MaxLineSize
	}
	stream.MarkTime = func(moment upstreamopenai.StreamTime) {
		switch moment {
		case upstreamopenai.StreamTimeFlush:
			MarkOpsTimestamp(c, telemetry.FirstDownstreamFlushAt)
		case upstreamopenai.StreamTimeData:
			MarkOpsTimestamp(c, telemetry.FirstSSEDataAt)
		case upstreamopenai.StreamTimeVisible:
			MarkOpsTimestamp(c, telemetry.FirstVisibleOutputAt)
		}
	}
	nonstream := p.NonStreamOptions(ctx, c, account)
	nonstream.TerminalFailover = func(resp *http.Response, event string, body []byte, message, model string) error {
		if failure := p.nonStreamingTerminalFailure(c, resp, account, true, event, body, message, model); failure != nil {
			return failure
		}
		return nil
	}
	return upstreamopenai.PassthroughOptions{
		StreamOptions: stream,
		NonStream:     nonstream,
		Headers: func(dst, src http.Header) {
			WriteOpenAIPassthroughResponseHeaders(dst, src, p.Headers)
		},
		StartKeepalive: func(headers http.Header) func() {
			// 原 Header 在启动心跳前已设置；这里只把本次输出适配中的元数据同步至 HTTP。
			for key, values := range headers {
				c.Writer.Header()[key] = append([]string(nil), values...)
			}
			if p.Options.Configured && p.Options.StreamKeepaliveInterval > 0 {
				return StartOpenAISSEKeepalive(c, time.Duration(p.Options.StreamKeepaliveInterval)*time.Second)
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
			return p.NewStreamFailureWithModel(c, account, true, id, body, message, model)
		},
	}
}
