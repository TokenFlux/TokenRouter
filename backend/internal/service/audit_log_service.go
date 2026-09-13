// 旧构造只投影保留期读取，队列与规则由 audit 唯一持有。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/audit"
)

type AuditLogService = audit.AuditLogService

func NewAuditLogService(repo AuditLogRepository, settings *SettingService) *AuditLogService {
	var retention audit.RetentionReader
	if settings != nil {
		retention = settings.GetAuditLogRetentionDays
	}
	return audit.NewAuditLogService(repo, retention)
}
