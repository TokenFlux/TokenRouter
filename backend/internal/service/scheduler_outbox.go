// 调度 outbox 契约由 scheduler 唯一拥有；旧入口不持有运行状态。
package service

import "github.com/TokenFlux/TokenRouter/internal/scheduler"

type SchedulerOutboxEvent = scheduler.SchedulerOutboxEvent
type SchedulerOutboxRepository = scheduler.SchedulerOutboxRepository
type SchedulerOutboxCleanupLease = scheduler.SchedulerOutboxCleanupLease
