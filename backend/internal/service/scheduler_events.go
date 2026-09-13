// 调度事件类型由 scheduler 唯一拥有；旧调用者在 S15/S16 清零后删除本入口。
package service

import "github.com/TokenFlux/TokenRouter/internal/scheduler"

const (
	SchedulerOutboxEventAccountChanged       = scheduler.SchedulerOutboxEventAccountChanged
	SchedulerOutboxEventAccountGroupsChanged = scheduler.SchedulerOutboxEventAccountGroupsChanged
	SchedulerOutboxEventAccountBulkChanged   = scheduler.SchedulerOutboxEventAccountBulkChanged
	SchedulerOutboxEventAccountLastUsed      = scheduler.SchedulerOutboxEventAccountLastUsed
	SchedulerOutboxEventGroupChanged         = scheduler.SchedulerOutboxEventGroupChanged
	SchedulerOutboxEventFullRebuild          = scheduler.SchedulerOutboxEventFullRebuild
)
