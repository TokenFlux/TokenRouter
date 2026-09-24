package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
)

// 旧网关仅投影配置、动态设置和原有检测替身，不复制身份优先级。
func (s *OpenAIGatewayService) nativeOpenAIClientPolicyFixture() *provider.OpenAIProbePolicy {
	policy := &provider.OpenAIProbePolicy{Available: s != nil, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent}
	if s == nil {
		return policy
	}
	policy.Profiles = s.tlsFPProfileService
	if s.tlsFPRouterService != nil {
		policy.Routers = s.tlsFPRouterService
	}
	policy.ForceCLI = s.cfg != nil && s.cfg.Gateway.ForceCodexCLI
	if s.settingService != nil {
		policy.AllowClaudeCode = s.settingService.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled
		policy.BrowserUserAgent = s.settingService.Gateway.GetOpenAICodexUserAgent
	}
	if s.codexDetector != nil {
		policy.Detect = func(read func() (string, string), value *account.Record, allowed []string, match egress.TLSFingerprintRouterMatchResult) account.CodexClientRestrictionDetectionResult {
			return s.codexDetector.DetectClient(read, account.CloneRecord(value), allowed, match.Matched)
		}
	}
	return policy
}
