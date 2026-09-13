package scheduler

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

// ConcurrencyError 保留槽位类别和超时区分。
type ConcurrencyError struct {
	SlotType  string
	IsTimeout bool
}

func (e *ConcurrencyError) Error() string {
	if e.IsTimeout {
		return fmt.Sprintf("timeout waiting for %s concurrency slot", e.SlotType)
	}
	return fmt.Sprintf("%s concurrency limit reached", e.SlotType)
}

// WaitQueueFullError 表示用户等待队列已满。
type WaitQueueFullError struct {
	SlotType string
}

func (e *WaitQueueFullError) Error() string {
	return "Too many pending requests, please retry later"
}

// 等待退避沿用原有边界和随机源。
const (
	InitialBackoff    = 100 * time.Millisecond
	MaxBackoff        = 2 * time.Second
	backoffMultiplier = 1.5
)

// WaitObserver 同步通知输出；Begin 仅在实际进入等待后运行，保留快速获取路径。
type WaitObserver struct {
	Interval  time.Duration
	Begin     func() error
	Heartbeat func() error
}

// WaitForSlot 保留首次尝试、退避、父取消与等待超时的原有次序。
func (s *ConcurrencyService) WaitForSlot(parent context.Context, slotType string, id int64, limit int, timeout time.Duration, immediate bool, observer WaitObserver) (func(), error) {
	operation, finish, err := s.runtime.Enter(parent, "slot-wait")
	if err != nil {
		return nil, err
	}
	defer finish()
	ctx, cancel := context.WithTimeout(operation, timeout)
	defer cancel()
	acquire := func() (*AcquireResult, error) {
		if slotType == "user" {
			return s.AcquireUserSlot(ctx, id, limit)
		}
		return s.AcquireAccountSlot(ctx, id, limit)
	}
	if immediate {
		result, err := acquire()
		if err != nil {
			return nil, err
		}
		if result.Acquired {
			return result.ReleaseFunc, nil
		}
	}
	if observer.Begin != nil {
		if err := observer.Begin(); err != nil {
			return nil, err
		}
	}
	var ping <-chan time.Time
	if observer.Heartbeat != nil && observer.Interval > 0 {
		t := time.NewTicker(observer.Interval)
		defer t.Stop()
		ping = t.C
	}
	backoff := InitialBackoff
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := parent.Err(); err != nil {
				return nil, err
			}
			if operation.Err() != nil {
				return nil, operation.Err()
			}
			return nil, &ConcurrencyError{SlotType: slotType, IsTimeout: true}
		case <-ping:
			if err := observer.Heartbeat(); err != nil {
				return nil, err
			}
		case <-timer.C:
			result, err := acquire()
			if err != nil {
				return nil, err
			}
			if result.Acquired {
				return result.ReleaseFunc, nil
			}
			backoff = NextBackoff(backoff)
			timer.Reset(backoff)
		}
	}
}

// UserAcquireOptions 仅包含本次准入及释放所需的独立输入。
type UserAcquireOptions struct {
	UserID   int64
	APIKeyID int64
	Limit    int
	Timeout  time.Duration
	Mode     ReleaseMode
	Observer WaitObserver
}

// AcquireUser 拥有实际取得的用户槽和 Key 统计槽，等待名额在离开队列时归还。
func (s *ConcurrencyService) AcquireUser(ctx context.Context, options UserAcquireOptions) (*Lease, WaitResult, error) {
	result, err := s.AcquireUserSlot(ctx, options.UserID, options.Limit)
	if err != nil {
		return nil, WaitResult{}, err
	}
	var waited WaitResult
	release := result.ReleaseFunc
	if !result.Acquired {
		limit := CalculateMaxWait(options.Limit) - options.Limit
		if limit < 1 {
			limit = 1
		}
		waited, err = s.EnterUserWait(ctx, options.UserID, limit)
		if err != nil {
			return nil, waited, err
		}
		if !waited.Allowed {
			return nil, waited, &WaitQueueFullError{SlotType: "user"}
		}
		defer waited.Release()
		release, err = s.WaitForSlot(ctx, "user", options.UserID, options.Limit, options.Timeout, false, options.Observer)
		if err != nil {
			return nil, waited, err
		}
	}
	// 先接管用户槽，统计槽采用原有尽力登记语义；取消绑定仍由调用方选择。
	lease := NewLease(context.Background(), ReleaseOnCompletion, release)
	if options.APIKeyID > 0 {
		keyRelease := s.TrackAPIKeySlot(ctx, options.APIKeyID)
		// 保留用户槽先释放、统计槽随后释放的兼容次序。
		combined := NewLease(ctx, options.Mode, func() {
			lease.Release()
			if keyRelease != nil {
				keyRelease()
			}
		})
		return combined, waited, nil
	}
	return NewLease(ctx, options.Mode, lease.Release), waited, nil
}

func NextBackoff(current time.Duration) time.Duration {
	// 指数退避：当前时间 * 1.5
	next := time.Duration(float64(current) * backoffMultiplier)
	if next > MaxBackoff {
		next = MaxBackoff
	}
	// 添加 ±20% 的随机抖动（jitter 范围 0.8 ~ 1.2）
	// 抖动可以分散多个请求的重试时间点，避免同时冲击 Redis
	jitter := 0.8 + rand.Float64()*0.4
	jittered := time.Duration(float64(next) * jitter)
	if jittered < InitialBackoff {
		return InitialBackoff
	}
	if jittered > MaxBackoff {
		return MaxBackoff
	}
	return jittered
}
