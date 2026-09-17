//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

func creativeModelSettingsIndex(settings []CreativeModelSetting) map[string][]string {
	return creative.CreativeModelSettingsIndex(settings)
}
func creativeOperationsForModel(settings map[string][]string, groupID int64, model string, supported []string) ([]string, bool) {
	return creative.CreativeOperationsForModel(settings, groupID, model, supported)
}
