package service

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// GatewayAdminSettings 只投影旧展示结构，不复制规则。

// GatewayAdminRules 为尚未清零的旧测试构造保留平台投影，生产由 app 注入。
func (s *SettingService) GatewayAdminRules() gateway.AdminSettingsRules {
	if s.gatewayAdminRules != nil {
		return *s.gatewayAdminRules
	}
	return gateway.AdminSettingsRules{GrokDefaultTextModel: xai.DefaultTextModel, NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, ValidateClaudePromptBlocks: ValidateClaudeOAuthSystemPromptBlocksConfig}
}

// SetGatewayAdminRules 仅在构造期间绑定，不支持运行中替换规则。
func (s *SettingService) SetGatewayAdminRules(rules *gateway.AdminSettingsRules) {
	s.gatewayAdminRules = rules
}
