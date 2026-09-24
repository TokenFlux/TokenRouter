package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	responseprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) StreamOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, reasoningEffort string) openai.StreamOptions {
	observer := UpstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = BeginUpstreamResponseModelObservation(c)
	}
	options := openai.StreamOptions{

		NativeOpenAI: account != nil && account.Record.Platform == capability.PlatformOpenAI,

		StageFirstOutput: account != nil && account.Record.Platform == capability.PlatformOpenAI,

		CodexFailureTerminal: account != nil && account.View().IsOpenAIOAuthLike(),

		GrokIdlePolicy: account != nil && account.Record.Platform == capability.PlatformGrok,

		MaxLineSize: openAIResponseDefaultMaxLineSize,

		TTFTMode: func() string { return p.TTFTMode(ctx) },

		Observe: observer.ObserveOpenAI,

		Logf: func(format string, values ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, values...)
		},

		IsCommitted: func() bool { return IsResponseCommitted(c) },

		MarkCommitted: func() { MarkResponseCommitted(c) },

		ClientOutputStarted: func(started bool) bool { return OpenAIStreamClientOutputStarted(c, started) },

		StagedHeadersCommitted: func(headers http.Header) { p.Turns.Commit(c, account, headers) },

		ClearDisconnect: func() { gatewayprovider.ClearProxyStreamDisconnect(p.ProxyCircuit, account) },

		RecordDisconnect: func(err error, requestID string) {
			gatewayprovider.RecordProxyStreamDisconnect(p.ProxyCircuit, account, err, requestID)
		},

		TerminalSideEffects: func(body []byte, message string, headers http.Header, model string) {
			p.TerminalAccountEffects(c, account, body, message, headers, model)
		},

		Failover: func(requestID string, body []byte, message string) error {
			return p.NewStreamFailure(c, account, false, requestID, body, message)
		},

		FailoverWithModel: func(requestID string, body []byte, message, model string, headers http.Header) error {
			return p.NewStreamFailureWithModel(c, account, false, requestID, body, message, model, headers)
		},

		MarkSafeFailover: func(err error) {
			if failure, ok := err.(*forwardcore.UpstreamFailoverError); ok {
				failure.SafeToFailoverAfterWrite = true
			}
		},

		RecordError: func(requestID, kind string, body []byte, message string) {
			p.RecordStreamError(c, account, false, requestID, kind, body, message)
		},

		CompactFallback: func(body []byte, message string) error { return NewOpenAICompactFailure(c, body, message) },

		ErrorRule: func(body []byte, message string) (int, string, string, bool) {
			return ApplyOpenAIStreamFailedErrorRule(c, account.Record.Platform, body, message)
		},

		CapacitySuppressed: func(requestID, eventType string) {
			LogOpenAICapacityFailoverSuppressed(ctx, account, "native_sse", requestID, eventType)
		},

		MarkCyber: func(value openai.CyberObservation) {
			MarkOpsCyberPolicy(c, moderationflow.Mark{
				Code:           value.Code,
				Message:        value.Message,
				Body:           value.Body,
				UpstreamStatus: value.UpstreamStatus,
				UpstreamInTok:  value.UpstreamInTok,
				UpstreamOutTok: value.UpstreamOutTok,
			})
		},

		ToolCorrector: p.Corrector,

		RestoreClientTools: func(body []byte) ([]byte, error) { return RestoreGrokResponsesClientToolPayload(c, body) },

		RestoreNamespace: func(body []byte) ([]byte, error) { return RestoreOpenAIResponsesNamespacePayload(c, body) },

		RestoreToolNames: func(body []byte, eventType string) []byte {
			return RestoreCodexToolNamesFromSSEContext(c, body, eventType)
		},

		EmptyCompleted: func(requestID string) error {
			return NewOpenAIResponsesEmptyCompletedFailoverError(c, ExecutionErrorAccount(account), requestID)
		},

		CountSearch: grok.CountGrokNativeSearchCallsInSSEDataDedup,

		StreamTimeout: func(model string) {
			if p.Observer != nil {
				p.Observer.Core.HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(account), model)

			}
		},

		IdleCooldown: func() {
			p.GrokHealth.TempUnschedule(ctx, account.View(), (2 * time.Minute), "grok stream idle timeout")
		},

		IdleFailover: func(interval time.Duration) error { return gatewayprovider.GrokStreamIdleFailure(account, interval) },

		FirstOutputError: func(start time.Time, model, effort string, timeout time.Duration, phase string, headers http.Header) error {
			return p.FirstOutputFailure(ctx, c, account, start, model, effort, timeout, phase, headers)
		},

		KeepaliveBytes: func(count int) { RecordOpenAIStreamKeepaliveBytes(c, count) },

		BuildOpenAIResponseFailedSSE: gatewayprovider.BuildOpenAIResponseFailedSSE,

		WrapOpenAIUpstreamWarningIfCyber: gatewayprovider.WrapOpenAIUpstreamWarningIfCyber,

		TruncateString: logredact.TruncateUTF8,

		OpenAIStreamDataStartsTTFT: gatewayprovider.OpenAIStreamDataStartsTTFT,

		OpenAIStreamEventIsTerminalWithType: responseprotocol.OpenAIStreamEventIsTerminalWithType,
	}
	if account != nil {
		options.AccountID = account.Record.ID
	}
	if options.NativeOpenAI {
		options.FirstOutputTimeout = p.FirstOutputTimeout(reasoningEffort)
	}
	if p.Options.Configured {
		if p.Options.MaxLineSize > 0 {
			options.MaxLineSize = p.Options.MaxLineSize
		}
		if p.Options.StreamDataIntervalTimeout > 0 {
			options.StreamInterval = time.Duration(p.Options.StreamDataIntervalTimeout) * time.Second
		}
		if p.Options.StreamKeepaliveInterval > 0 {
			options.KeepaliveInterval = time.Duration(p.Options.StreamKeepaliveInterval) * time.Second
		}
	}
	if options.GrokIdlePolicy {
		seconds := 0
		if p.Options.Configured {
			seconds = p.Options.StreamDataIntervalTimeout
		}
		options.StreamInterval = grok.ResolveStreamIdleTimeout(seconds)
	}
	options.PrepareHeaders = func(headers http.Header, staged bool, output http.Header) http.Header {
		var pending http.Header
		if staged {
			if p.Headers != nil {
				pending = provider.FilterHeaders(headers, p.Headers)
			} else if requestID := strings.TrimSpace(headers.Get("x-request-id")); requestID != "" {
				pending = http.Header{"X-Request-Id": {requestID}}
			}
		} else if p.Headers != nil {
			provider.WriteFilteredHeaders(output, headers, p.Headers)
		}
		if staged {
			StageCodexTurnState(&pending, headers)
		} else {
			p.Turns.Relay(c, account, headers)
		}
		return pending
	}
	options.MarkTime = func(moment openai.StreamTime) {
		switch moment {
		case openai.StreamTimeFlush:
			MarkOpsTimestamp(c, telemetry.FirstDownstreamFlushAt)
		case openai.StreamTimeData:
			MarkOpsTimestamp(c, telemetry.FirstSSEDataAt)
		case openai.StreamTimeVisible:
			MarkOpsTimestamp(c, telemetry.FirstVisibleOutputAt)
		}
	}
	return options
}
