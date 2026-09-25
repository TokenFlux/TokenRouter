//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	gin "github.com/gin-gonic/gin"
)

const maxAPIKeyAuthorizationHeaderBytes = apikey.MaxAPIKeyCredentialBytes + 128

func abortTeamAPIKeyError(c *gin.Context, err error) bool { return keyhttp.AbortTeamError(c, err) }
