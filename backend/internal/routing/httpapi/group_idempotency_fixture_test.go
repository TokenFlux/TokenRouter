//go:build unit

package httpapi

import (
	"context"
	"errors"
	"time"

	idempotencytest "github.com/TokenFlux/TokenRouter/internal/idempotency/testkit"
)

// 失败注入只控制持久化响应，保留重放恢复的原副作用断言。
type failOnceMarkSucceededRepo struct {
	*idempotencytest.MemoryStore
	failNext bool
}

func (r *failOnceMarkSucceededRepo) MarkSucceeded(ctx context.Context, id int64, responseStatus int, responseBody string, expiresAt time.Time) error {
	if r.failNext {
		r.failNext = false
		return errors.New("mark succeeded failed")
	}
	return r.MemoryStore.MarkSucceeded(ctx, id, responseStatus, responseBody, expiresAt)
}
