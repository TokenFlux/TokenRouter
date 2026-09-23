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

type IdempotencyStoreUnavailableMode int

const (
	IdempotencyStoreUnavailableFailClose IdempotencyStoreUnavailableMode = iota
	IdempotencyStoreUnavailableFailOpen
)

func (e *Executor) ExecuteAdminIdempotent(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) (*idempotency.IdempotencyExecuteResult, error) {
	coordinator := e.coordinator
	if coordinator == nil {
		data, err := execute(c.Request.Context())
		if err != nil {
			return nil, err
		}
		return &idempotency.IdempotencyExecuteResult{Data: data}, nil
	}

	return coordinator.Execute(c.Request.Context(), idempotency.IdempotencyExecuteOptions{
		Scope:          scope,
		ActorScope:     AdminActorScope(c),
		Method:         c.Request.Method,
		Route:          c.FullPath(),
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		Payload:        payload,
		RequireKey:     true,
		TTL:            ttl,
	}, execute)
}

func AdminActorScope(c *gin.Context) string {
	actorScope := "admin:0"
	if subject, ok := middleware2.GetAuthSubjectFromContext(c); ok {
		actorScope = "admin:" + strconv.FormatInt(subject.UserID, 10)
	}
	return actorScope
}

func (e *Executor) ExecuteAdminIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	e.ExecuteAdminIdempotentJSONWithMode(c, scope, payload, ttl, IdempotencyStoreUnavailableFailClose, execute)
}

func (e *Executor) ExecuteAdminIdempotentJSONFailOpenOnStoreUnavailable(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	e.ExecuteAdminIdempotentJSONWithMode(c, scope, payload, ttl, IdempotencyStoreUnavailableFailOpen, execute)
}

func (e *Executor) ExecuteAdminIdempotentJSONWithMode(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	mode IdempotencyStoreUnavailableMode,
	execute func(context.Context) (any, error),
) {
	result, err := e.ExecuteAdminIdempotent(c, scope, payload, ttl, execute)
	if err != nil {
		if response.ErrorCode(err) == response.ErrorCode(idempotency.ErrIdempotencyStoreUnavail) {
			strategy := "fail_close"
			if mode == IdempotencyStoreUnavailableFailOpen {
				strategy = "fail_open"
			}
			e.coordinator.RecordIdempotencyStoreUnavailable(c.FullPath(), scope, "handler_"+strategy)
			logger.LegacyPrintf("handler.idempotency", "[Idempotency] store unavailable: method=%s route=%s scope=%s strategy=%s", c.Request.Method, c.FullPath(), scope, strategy)
			if mode == IdempotencyStoreUnavailableFailOpen {
				data, fallbackErr := execute(c.Request.Context())
				if fallbackErr != nil {
					response.ErrorFrom(c, fallbackErr)
					return
				}
				c.Header("X-Idempotency-Degraded", "store-unavailable")
				response.Success(c, data)
				return
			}
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
