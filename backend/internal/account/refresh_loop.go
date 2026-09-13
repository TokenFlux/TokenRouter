package account

import (
	"context"
	"errors"
	"sync"
	"time"
)

// RefreshLoop 只拥有周期刷新启停，候选和交换规则由同一刷新用例的 cycle 提供。
// 构造不创建后台任务；仅第一次启动有效，停止后不能重新启动。
type RefreshLoop struct {
	mu               sync.Mutex
	started, stopped bool
	cycle            func(context.Context)
	activity         operationActivity
}

func NewRefreshLoop(cycle func(context.Context)) *RefreshLoop { return &RefreshLoop{cycle: cycle} }
func (l *RefreshLoop) StartContext(parent context.Context, interval time.Duration) (bool, error) {
	if l == nil || l.cycle == nil {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started || l.stopped {
		return false, nil
	}
	if interval <= 0 {
		return false, errors.New("token refresh interval must be positive")
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, done, err := l.activity.begin(parent, ErrRefreshStopped)
	if err != nil {
		return false, err
	}
	l.started = true
	go func() {
		defer done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		if ctx.Err() == nil {
			l.cycle(ctx)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				l.cycle(ctx)
			}
		}
	}()
	return true, nil
}

// StopContext 等待实际轮次退出，错误结果固定，不以超时返回替代轮次完成。
func (l *RefreshLoop) StopContext(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	l.stopped = true
	l.mu.Unlock()
	return l.activity.stop(ctx, "token refresh")
}
