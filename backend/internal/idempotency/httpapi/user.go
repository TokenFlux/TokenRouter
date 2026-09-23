// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package httpapi

import (
	context "context"
	strconv "strconv"
	time "time"

	idempotency "github.com/TokenFlux/TokenRouter/internal/idempotency"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

func (e *Executor) ExecuteUserIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	coordinator := e.coordinator
	if coordinator == nil {
		data, err := execute(c.Request.Context())
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		response.Success(c, data)
		return
	}

	actorScope := "user:0"
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		actorScope = "user:" + strconv.FormatInt(subject.UserID, 10)
	}

	result, err := coordinator.Execute(c.Request.Context(), idempotency.IdempotencyExecuteOptions{
		Scope:          scope,
		ActorScope:     actorScope,
		Method:         c.Request.Method,
		Route:          c.FullPath(),
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		Payload:        payload,
		RequireKey:     true,
		TTL:            ttl,
	}, execute)
	if err != nil {
		if response.ErrorCode(err) == response.ErrorCode(idempotency.ErrIdempotencyStoreUnavail) {
			e.coordinator.RecordIdempotencyStoreUnavailable(c.FullPath(), scope, "handler_fail_close")
			logger.LegacyPrintf("handler.idempotency", "[Idempotency] store unavailable: method=%s route=%s scope=%s strategy=fail_close", c.Request.Method, c.FullPath(), scope)
		}
		if retryAfter := idempotency.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	response.Success(c, result.Data)
}
