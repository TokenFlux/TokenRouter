package provider

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
)

// GatewayBetaPolicy 只投影平台规则值，不复制其算法或状态。
func GatewayBetaPolicy(value *anthropic.BetaPolicySettings) *gateway.BetaPolicySettings {
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

// AnthropicBetaPolicy 保留旧平台执行入口所需类型，规则存取由 gateway 拥有。
func AnthropicBetaPolicy(value *gateway.BetaPolicySettings) *anthropic.BetaPolicySettings {
	if value == nil {
		return nil
	}
	result := &anthropic.BetaPolicySettings{}
	if value.Rules != nil {
		result.Rules = make([]anthropic.BetaPolicyRule, len(value.Rules))
		for i, rule := range value.Rules {
			result.Rules[i] = anthropic.BetaPolicyRule(rule)
			result.Rules[i].ModelWhitelist = slices.Clone(rule.ModelWhitelist)
		}
	}
	return result
}
