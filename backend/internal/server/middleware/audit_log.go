// 审计中间件和请求辅助函数委托 audit/httpapi。
package middleware

import (
	audit "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	"github.com/gin-gonic/gin"
)

type AuditLogMiddleware = audit.AuditLogMiddleware

const ContextKeyAuthEmail = audit.ContextKeyAuthEmail
const ContextKeySessionID = audit.ContextKeySessionID

func SetAuditAction(c *gin.Context, s string)              { audit.SetAuditAction(c, s) }
func SetAuditActor(c *gin.Context, id int64, email string) { audit.SetAuditActor(c, id, email) }
func SkipAudit(c *gin.Context)                             { audit.SkipAudit(c) }
func MaskedRequestCredential(c *gin.Context) string        { return audit.MaskedRequestCredential(c) }
