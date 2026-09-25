package provider

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"go.uber.org/zap"
)

func openAIProxyStreamCircuitProxyID(account *ExecutionAccount) (int64, bool) {
	if account == nil || account.Record.Platform != capability.PlatformOpenAI || account.Record.ProxyID == nil || *account.Record.ProxyID <= 0 {
		return 0, false
	}
	return *account.Record.ProxyID, true
}

func RecordProxyStreamDisconnect(circuit *egress.ProxyStreamCircuit, account *ExecutionAccount, streamErr error, upstreamRequestID string) {
	proxyID, ok := openAIProxyStreamCircuitProxyID(account)
	if !ok || streamErr == nil || errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
		return
	}

	tripped, until := circuit.RecordFailure(proxyID, time.Now())
	if !tripped {
		return
	}
	logging.L().With(zap.String("component", "service.openai_gateway")).Warn(
		"openai.proxy_quarantined_stream_disconnect",
		zap.Int64("proxy_id", proxyID),
		zap.Int64("account_id", account.Record.ID),
		zap.Time("until", until),
		zap.String("upstream_request_id", upstreamRequestID),
		zap.String("error", logredact.SanitizeUpstreamQueries(streamErr.Error())),
	)
}

func ClearProxyStreamDisconnect(circuit *egress.ProxyStreamCircuit, account *ExecutionAccount) {
	proxyID, ok := openAIProxyStreamCircuitProxyID(account)
	if !ok {
		return
	}
	if circuit != nil {
		circuit.RecordSuccess(proxyID)
	}
}
