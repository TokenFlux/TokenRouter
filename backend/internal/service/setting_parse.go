package service

import (
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
)

// parseSettings 解析设置到结构体
func (s *SettingService) parseSettings(values map[string]string) *SystemSettings {
	return composite.Parse(values, s.compositeReadOptions())
}

// normalizeOpenAITTFTMode 将未知值收敛到默认的语义事件口径。
func normalizeOpenAITTFTMode(mode string) string { return gateway.NormalizeOpenAITTFTMode(mode) }
