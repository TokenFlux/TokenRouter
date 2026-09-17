//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

// 仅保留已有 unit 断言需要的旧入口，退出 S16。
// parseCreativeModelSettings 解析持久化设置；任何异常都按空白名单处理，避免误放行模型。
func parseCreativeModelSettings(raw string) []CreativeModelSetting {
	return creative.ParseCreativeModelSettings(raw)
}
