//go:build unit

package service

import (
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 通过显式时钟验证过期，不向生产 API 暴露可变缓存或测试写入方法。
type runtimeBlockTestClock struct{ nanos atomic.Int64 }

func (c *runtimeBlockTestClock) Now() time.Time {
	if nanos := c.nanos.Load(); nanos != 0 {
		return time.Unix(0, nanos)
	}
	return time.Now()
}

func (c *runtimeBlockTestClock) Set(now time.Time) { c.nanos.Store(now.UnixNano()) }

func bindRuntimeBlockClockForTest(svc *OpenAIGatewayService) *runtimeBlockTestClock {
	clock := &runtimeBlockTestClock{}
	svc.BindRuntimeBlockState(account.NewRuntimeBlockState(clock.Now))
	return clock
}

func expireRuntimeRetryForTest(svc *OpenAIGatewayService, id int64) *runtimeBlockTestClock {
	clock := bindRuntimeBlockClockForTest(svc)
	clock.Set(time.Now().Add(-account.RuntimeRetryWindow - time.Second))
	svc.runtimeBlockState().RetryWindowActive(id)
	clock.nanos.Store(0)
	return clock
}

func bindExpiredRuntimeBlockForTest(svc *OpenAIGatewayService, id int64, expired time.Time) {
	clock := bindRuntimeBlockClockForTest(svc)
	clock.Set(expired.Add(-time.Minute))
	svc.runtimeBlockState().Block(id, expired, "原过期快照夹具")
	clock.nanos.Store(0)
}
