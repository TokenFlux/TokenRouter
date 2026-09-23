package service

import (
	"context"
	"errors"
	"time"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"go.uber.org/zap"
)

func resolveOpenAIProxyStreamCircuitSettings(s *OpenAIGatewayService) egress.ProxyStreamCircuitSettings {
	settings := egress.DefaultProxyStreamCircuitSettings()
	if s == nil || s.cfg == nil {
		return settings
	}
	cfg := s.cfg.Gateway.OpenAIProxyStreamCircuit
	settings.Disabled = cfg.Disabled
	if cfg.FailureThreshold > 0 {
		settings.FailureThreshold = cfg.FailureThreshold
	}
	if cfg.WindowSeconds > 0 {
		settings.FailureWindow = time.Duration(cfg.WindowSeconds) * time.Second
	}
	if cfg.TTLSeconds > 0 {
		settings.QuarantineTTL = time.Duration(cfg.TTLSeconds) * time.Second
	}
	return settings
}

func (s *OpenAIGatewayService) getOpenAIProxyStreamCircuit() *egress.ProxyStreamCircuit {
	if s == nil {
		return nil
	}
	s.openaiProxyStreamCircuitOnce.Do(func() {
		if s.openaiProxyStreamCircuit == nil {
			s.openaiProxyStreamCircuit = egress.NewProxyStreamCircuit(resolveOpenAIProxyStreamCircuitSettings(s))
		}
	})
	return s.openaiProxyStreamCircuit
}

func openAIProxyStreamCircuitProxyID(account *gatewayprovider.ExecutionAccount) (int64, bool) {
	if account == nil || account.Record.Platform != capability.PlatformOpenAI || account.Record.ProxyID == nil || *account.Record.ProxyID <= 0 {
		return 0, false
	}
	return *account.Record.ProxyID, true
}

func (s *OpenAIGatewayService) recordOpenAIProxyStreamDisconnect(account *gatewayprovider.ExecutionAccount, streamErr error, upstreamRequestID string) {
	proxyID, ok := openAIProxyStreamCircuitProxyID(account)
	if !ok || streamErr == nil || errors.Is(streamErr, context.Canceled) || errors.Is(streamErr, context.DeadlineExceeded) {
		return
	}
	circuit := s.getOpenAIProxyStreamCircuit()
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

func (s *OpenAIGatewayService) clearOpenAIProxyStreamDisconnect(account *gatewayprovider.ExecutionAccount) {
	proxyID, ok := openAIProxyStreamCircuitProxyID(account)
	if !ok {
		return
	}
	if circuit := s.getOpenAIProxyStreamCircuit(); circuit != nil {
		circuit.RecordSuccess(proxyID)
	}
}
