// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package admin

import (
	context "context"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	gin "github.com/gin-gonic/gin"
	time "time"
)

func executeAdminIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	idempotencyhttp.ExecuteAdminIdempotentJSON(c, scope, payload, ttl, execute)
}

func executeAdminIdempotentJSONFailOpenOnStoreUnavailable(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	idempotencyhttp.ExecuteAdminIdempotentJSONFailOpenOnStoreUnavailable(c, scope, payload, ttl, execute)
}
