package service

import (
	"time"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
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
