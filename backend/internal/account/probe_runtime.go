package account

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// ErrProbeStopped 表示拥有者已停止接纳探测，不能再启动网络或持久化工作。
var ErrProbeStopped = errors.New("account probe runtime stopped")

// ProbeRuntime 跟踪按需共享探测及关联维护任务；零值可用，构造不启动工作。
type ProbeRuntime struct {
	activity   operationActivity
	flights    singleflight.Group
	background sync.Map
}

// Run 保留等待方独立取消；共享工作使用自身预算，并受拥有者停止约束。
func (r *ProbeRuntime) Run(ctx context.Context, key string, budget time.Duration, probe func(context.Context) (any, error)) (any, error) {
	waitCtx, finishWait, err := r.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finishWait()
	result := r.flights.DoChan(key, func() (any, error) {
		shared, finish, err := r.activity.begin(context.WithoutCancel(ctx), ErrProbeStopped)
		// 等待者已被接纳，停止后的派生竞争统一作为本次查询取消。
		if err == ErrProbeStopped {
			return nil, context.Canceled
		}
		if err != nil {
			return nil, err
		}
		defer finish()
		shared, cancel := context.WithTimeout(shared, budget)
		defer cancel()
		return probe(shared)
	})
	select {
	case <-waitCtx.Done():
		return nil, waitCtx.Err()
	case result := <-result:
		return result.Val, result.Err
	}
}

// Schedule 在返回前登记任务；同 key 在途时合并，拒绝新任务时不遗留占位。
func (r *ProbeRuntime) Schedule(key string, budget time.Duration, work func(context.Context)) bool {
	if _, loaded := r.background.LoadOrStore(key, struct{}{}); loaded {
		return false
	}
	ctx, finish, err := r.activity.begin(context.Background(), ErrProbeStopped)
	if err != nil {
		r.background.Delete(key)
		return false
	}
	go func() {
		defer finish()
		defer r.background.Delete(key)
		ctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		work(ctx)
	}()
	return true
}

// StopContext 取消并等待所有已登记任务；重复调用保持首次停止结果。
func (r *ProbeRuntime) StopContext(ctx context.Context) error {
	return r.activity.stop(ctx, "account probes")
}
