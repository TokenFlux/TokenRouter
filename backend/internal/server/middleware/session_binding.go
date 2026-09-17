// 本文件维护 middleware 的所属能力；兼容入口复用唯一实现。
package middleware

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	gin "github.com/gin-gonic/gin"
)

// SessionBindingContext 委托身份 HTTP 适配，保留旧调用签名。

// SecurityClientIP 委托身份 HTTP 适配。
func SecurityClientIP(c *gin.Context) string { return identityhttp.SecurityClientIP(c) }
