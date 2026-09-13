// 旧审计中间件入口委托所属 HTTP Adapter，S15/S16 清理。
package middleware

import (
	audit "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type AuditLogMiddleware = audit.AuditLogMiddleware

const ContextKeyAuthEmail = audit.ContextKeyAuthEmail
const ContextKeySessionID = audit.ContextKeySessionID

func SetAuditAction(c *gin.Context, s string)              { audit.SetAuditAction(c, s) }
func SetAuditActor(c *gin.Context, id int64, email string) { audit.SetAuditActor(c, id, email) }
func SkipAudit(c *gin.Context)                             { audit.SkipAudit(c) }
func MaskedRequestCredential(c *gin.Context) string        { return audit.MaskedRequestCredential(c) }
func NewAuditLogMiddleware(s *service.AuditLogService) AuditLogMiddleware {
	return audit.NewAuditLogMiddleware(s, service.CurrentAuditRedactor())
}
