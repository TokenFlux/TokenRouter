// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	gin "github.com/gin-gonic/gin"
)

// AuthSubject 是旧 context 读取投影，新认证主体由 identity.Principal 提供。
type AuthSubject = identityhttp.AuthSubject

func GetAuthSubjectFromContext(c *gin.Context) (AuthSubject, bool) {
	return identityhttp.GetAuthSubjectFromContext(c)
}
func GetUserRoleFromContext(c *gin.Context) (string, bool) {
	return identityhttp.GetUserRoleFromContext(c)
}
