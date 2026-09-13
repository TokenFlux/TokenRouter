// 旧 handler 构造只委托 audit/httpapi，路由持有的实际 handler 来自新模块。
package admin

import (
	audit "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type AuditLogHandler = audit.AuditLogHandler

func NewAuditLogHandler(s *service.AuditLogService, t *service.TotpService) *AuditLogHandler {
	return audit.NewAuditLogHandler(s, t)
}
