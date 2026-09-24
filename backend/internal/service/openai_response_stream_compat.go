// 原入站适配只投影一次尝试的配置、HTTP 观察和账号副作用；逐帧状态由原生读取器持有。
package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeResponseStreamOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, reasoningEffort string) openai.StreamOptions {
	observer := gatewayhttp.UpstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = gatewayhttp.BeginUpstreamResponseModelObservation(c)
	}
	options := openai.StreamOptions{

		NativeOpenAI: account != nil && account.Record.Platform == capability.PlatformOpenAI,

		StageFirstOutput: account != nil && account.Record.Platform == capability.PlatformOpenAI,

		CodexFailureTerminal: account != nil && account.View().IsOpenAIOAuthLike(),

		GrokIdlePolicy: account != nil && account.Record.Platform == capability.PlatformGrok,

		MaxLineSize: defaultMaxLineSize,

		TTFTMode: func() string { return s.openAITTFTMode(ctx) },

		Observe: observer.ObserveOpenAI,

		Logf: func(format string, values ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, values...)
		},

		IsCommitted: func() bool { return gatewayhttp.IsResponseCommitted(c) },

		MarkCommitted: func() { gatewayhttp.MarkResponseCommitted(c) },

		ClientOutputStarted: func(started bool) bool { return gatewayhttp.OpenAIStreamClientOutputStarted(c, started) },

		StagedHeadersCommitted: func(headers http.Header) { s.turnStateHeaders.Commit(c, account, headers) },

		ClearDisconnect: func() { s.clearOpenAIProxyStreamDisconnect(account) },

		RecordDisconnect: func(err error, requestID string) { s.recordOpenAIProxyStreamDisconnect(account, err, requestID) },

		TerminalSideEffects: func(body []byte, message string, headers http.Header, model string) {
			s.handleOpenAIStreamTerminalAccountSideEffects(c, account, body, message, headers, model)
		},

		Failover: func(requestID string, body []byte, message string) error {
			return s.newOpenAIStreamFailoverError(c, account, false, requestID, body, message)
		},

		FailoverWithModel: func(requestID string, body []byte, message, model string, headers http.Header) error {
			return s.newOpenAIStreamFailoverErrorWithModel(c, account, false, requestID, body, message, model, headers)
		},

		MarkSafeFailover: func(err error) {
			if failure, ok := err.(*forwardcore.UpstreamFailoverError); ok {
				failure.SafeToFailoverAfterWrite = true
			}
		},

		RecordError: func(requestID, kind string, body []byte, message string) {
			s.recordOpenAIStreamUpstreamError(c, account, false, requestID, kind, body, message)
		},

		CompactFallback: func(body []byte, message string) error { return newOpenAICompactFallbackSignal(c, body, message) },

		ErrorRule: func(body []byte, message string) (int, string, string, bool) {
			return applyOpenAIStreamFailedErrorPassthroughRule(c, account.Record.Platform, body, message)
		},

		CapacitySuppressed: func(requestID, eventType string) {
			logOpenAICapacityFailoverSuppressed(ctx, account, "native_sse", requestID, eventType)
		},

		MarkCyber: func(value openai.CyberObservation) {
			gatewayhttp.MarkOpsCyberPolicy(c, moderationflow.Mark{
				Code:           value.Code,
				Message:        value.Message,
				Body:           value.Body,
				UpstreamStatus: value.UpstreamStatus,
				UpstreamInTok:  value.UpstreamInTok,
				UpstreamOutTok: value.UpstreamOutTok,
			})
		},

		ToolCorrector: s.toolCorrector,

		RestoreClientTools: func(body []byte) ([]byte, error) { return restoreGrokResponsesClientToolPayload(c, body) },

		RestoreNamespace: func(body []byte) ([]byte, error) { return restoreOpenAIResponsesNamespacePayload(c, body) },

		RestoreToolNames: func(body []byte, eventType string) []byte {
			return restoreCodexToolNamesFromSSEContext(c, body, eventType)
		},

		EmptyCompleted: func(requestID string) error {
			return gatewayhttp.NewOpenAIResponsesEmptyCompletedFailoverError(c, upstreamErrorAccount(account), requestID)
		},

		CountSearch: grok.CountGrokNativeSearchCallsInSSEDataDedup,

		StreamTimeout: func(model string) {
			if s.healthObserver != nil {
				s.healthObserver.Core.HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(account), model)

			}
		},

		IdleCooldown: func() { s.tempUnscheduleGrok(ctx, account, grokStreamIdleCooldown, "grok stream idle timeout") },

		IdleFailover: func(interval time.Duration) error { return grokStreamIdleFailoverError(account, interval) },

		FirstOutputError: func(start time.Time, model, effort string, timeout time.Duration, phase string, headers http.Header) error {
			return s.newOpenAIFirstOutputTimeoutError(ctx, c, account, start, model, effort, timeout, phase, headers)
		},

		KeepaliveBytes: func(count int) { gatewayhttp.RecordOpenAIStreamKeepaliveBytes(c, count) },

		BuildOpenAIResponseFailedSSE: gatewayprovider.BuildOpenAIResponseFailedSSE,

		WrapOpenAIUpstreamWarningIfCyber: gatewayprovider.WrapOpenAIUpstreamWarningIfCyber,

		TruncateString: logredact.TruncateUTF8,

		OpenAIStreamDataStartsTTFT: gatewayprovider.OpenAIStreamDataStartsTTFT,

		OpenAIStreamEventIsTerminalWithType: openAIStreamEventIsTerminalWithType,
	}
	if account != nil {
		options.AccountID = account.Record.ID
	}
	if options.NativeOpenAI {
		options.FirstOutputTimeout = s.openAIFirstOutputTimeout(reasoningEffort)
	}
	if s.cfg != nil {
		if s.cfg.Gateway.MaxLineSize > 0 {
			options.MaxLineSize = s.cfg.Gateway.MaxLineSize
		}
		if s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
			options.StreamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
		}
		if s.cfg.Gateway.StreamKeepaliveInterval > 0 {
			options.KeepaliveInterval = time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
		}
	}
	if options.GrokIdlePolicy {
		seconds := 0
		if s.cfg != nil {
			seconds = s.cfg.Gateway.StreamDataIntervalTimeout
		}
		options.StreamInterval = grok.ResolveStreamIdleTimeout(seconds)
	}
	options.PrepareHeaders = func(headers http.Header, staged bool, output http.Header) http.Header {
		var pending http.Header
		if staged {
			if s.responseHeaderFilter != nil {
				pending = provider.FilterHeaders(headers, s.responseHeaderFilter)
			} else if requestID := strings.TrimSpace(headers.Get("x-request-id")); requestID != "" {
				pending = http.Header{"X-Request-Id": {requestID}}
			}
		} else if s.responseHeaderFilter != nil {
			provider.WriteFilteredHeaders(output, headers, s.responseHeaderFilter)
		}
		if staged {
			gatewayhttp.StageCodexTurnState(&pending, headers)
		} else {
			s.turnStateHeaders.Relay(c, account, headers)
		}
		return pending
	}
	options.MarkTime = func(moment openai.StreamTime) {
		switch moment {
		case openai.StreamTimeFlush:
			gatewayhttp.MarkOpsTimestamp(c, telemetry.FirstDownstreamFlushAt)
		case openai.StreamTimeData:
			gatewayhttp.MarkOpsTimestamp(c, telemetry.FirstSSEDataAt)
		case openai.StreamTimeVisible:
			gatewayhttp.MarkOpsTimestamp(c, telemetry.FirstVisibleOutputAt)
		}
	}
	return options
}
