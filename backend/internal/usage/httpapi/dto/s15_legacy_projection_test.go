package dto_test

import (
	"github.com/TokenFlux/TokenRouter/internal/service" // 旧记录只作为迁移回归夹具输入，实际 DTO 由新模块生成。
	native "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
)

func UsageLogFromService(v *service.UsageLog) *native.UsageLog {
	return native.FromUsage(service.UsageLogView(v))
}
func UsageLogFromServiceAdmin(v *service.UsageLog) *native.AdminUsageLog {
	return native.FromUsageAdmin(service.UsageLogView(v))
}
func UsageLogTimingFromService(v *service.OpsRequestTiming) *native.UsageLogTiming {
	return native.TimingFromOps(v)
}
func UsageCleanupTaskFromService(v *service.UsageCleanupTask) *native.UsageCleanupTask {
	return native.CleanupFromUsage(v)
}

// UsageLog 仅供旧记录投影断言使用，新 HTTP 类型由 usage 唯一拥有。
type UsageLog = native.UsageLog
