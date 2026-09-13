// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountSchedulerDiagnostics 仅投影原只读诊断能力，S07 迁移其执行算法。
func AccountSchedulerDiagnostics(source *service.AdvancedSchedulerScoreDiagnosticService) accounthttp.AccountSchedulerDiagnostics {
	return source
}
