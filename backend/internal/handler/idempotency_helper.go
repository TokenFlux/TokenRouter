// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package handler

import (
	context "context"
	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	gin "github.com/gin-gonic/gin"
	time "time"
)

func executeUserIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	idempotencyhttp.ExecuteUserIdempotentJSON(c, scope, payload, ttl, execute)
}
