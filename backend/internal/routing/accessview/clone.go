// 本文件维护 accessview 的所属能力；兼容入口复用唯一实现。
package accessview

import (
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// CloneGroupAdvancedSchedulerOverrides 委托所属模块的唯一实现。
func CloneGroupAdvancedSchedulerOverrides(overrides GroupAdvancedSchedulerOverrides) GroupAdvancedSchedulerOverrides {
	return policy.CloneGroupAdvancedSchedulerOverrides(overrides)
}
