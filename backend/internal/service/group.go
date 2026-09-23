// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// computePeakAwareMultipliers 把"基础 token 倍率 base"（已含系统/分组/用户级倍率，但不含高峰）
// 拆分为最终 token 倍率与图片按次倍率：图片按次倍率基于 base 现算、不受高峰影响；token 倍率在 base 上叠加高峰因子。
// gateway_service.recordUsageCore 与 openai_gateway_service.RecordUsage 共用此函数，
// 锁死"高峰因子只乘入 token 倍率、图片按次倍率不受影响"这一叠加顺序——任何调换都会被 group_peak_rate_test 覆盖。
func computePeakAwareMultipliers(apiKey *apikey.APIKey, base float64, now time.Time) (text, image float64) {
	return completion.ComputePeakAwareMultipliers(gatewaycapture.ProjectCompletionKey(apiKey), base, now)
}
