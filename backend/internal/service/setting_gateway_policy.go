package service

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
)

// gatewayForwardingDBTimeout 仅保留旧调用的预算值，不再持有全局缓存。
const gatewayForwardingDBTimeout = gateway.ForwardingSettingsReadTimeout

// GatewaySettings 返回唯一网关规则读取器；原平台缺省通过明确投影供给。
func (s *SettingService) GatewaySettings() *gateway.RuntimeSettings {
	if s == nil {
		return nil
	}
	s.gatewaySettingsOnce.Do(func() {
		if s.gatewaySettings == nil {
			s.gatewaySettings = gateway.NewRuntimeSettings(s.settingRepo, ErrSettingNotFound, func() *gateway.BetaPolicySettings { return gatewayBetaPolicySettings(DefaultBetaPolicySettings()) }, gateway.ClientSettingsOptions{NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, DefaultUserAgentVersion: antigravity.GetDefaultUserAgentVersion})
		}
	})
	return s.gatewaySettings
}

// gatewayBetaPolicySettings 只投影平台规则值，不复制其算法或状态。
func gatewayBetaPolicySettings(value *BetaPolicySettings) *gateway.BetaPolicySettings {
	if value == nil {
		return nil
	}
	result := &gateway.BetaPolicySettings{}
	if value.Rules != nil {
		result.Rules = make([]gateway.BetaPolicyRule, len(value.Rules))
		for i, rule := range value.Rules {
			result.Rules[i] = gateway.BetaPolicyRule(rule)
			result.Rules[i].ModelWhitelist = slices.Clone(rule.ModelWhitelist)
		}
	}
	return result
}

// legacyBetaPolicySettings 保留旧平台执行入口所需类型，规则存取由 gateway 拥有。
func legacyBetaPolicySettings(value *gateway.BetaPolicySettings) *BetaPolicySettings {
	if value == nil {
		return nil
	}
	result := &BetaPolicySettings{}
	if value.Rules != nil {
		result.Rules = make([]BetaPolicyRule, len(value.Rules))
		for i, rule := range value.Rules {
			result.Rules[i] = BetaPolicyRule(rule)
			result.Rules[i].ModelWhitelist = slices.Clone(rule.ModelWhitelist)
		}
	}
	return result
}

// SetGatewaySettings 由 app 在开放路由前绑定唯一原生实例。
func (s *SettingService) SetGatewaySettings(value *gateway.RuntimeSettings) {
	s.gatewaySettings = value
}
