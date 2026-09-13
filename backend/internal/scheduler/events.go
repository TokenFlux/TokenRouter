// 本文件拥有调度事件契约，旧入口仅委托唯一实现。
package scheduler

const (
	SchedulerOutboxEventAccountChanged       = "account_changed"
	SchedulerOutboxEventAccountGroupsChanged = "account_groups_changed"
	SchedulerOutboxEventAccountBulkChanged   = "account_bulk_changed"
	SchedulerOutboxEventAccountLastUsed      = "account_last_used"
	SchedulerOutboxEventGroupChanged         = "group_changed"
	SchedulerOutboxEventFullRebuild          = "full_rebuild"
)

// GroupPayload 保留空分组的 untyped nil，避免改变持久化去重指纹。
func GroupPayload(groupIDs []int64) any {
	if len(groupIDs) == 0 {
		return nil
	}
	return map[string]any{"group_ids": groupIDs}
}
