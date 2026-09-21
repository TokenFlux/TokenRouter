package account

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	qoderRefreshLockWait = 3 * time.Second
	qoderRefreshLockPoll = 100 * time.Millisecond
)

// ErrQoderRefreshInProgress 表示锁持有者尚未发布新凭据，不能重用失败凭据。
var ErrQoderRefreshInProgress = errors.New("qoder refresh in progress")

// WaitForQoderRefresh 先立即回读，再按原间隔等待锁持有者发布；不再交换令牌。
// readChanged 在原存储中读取最新身份，返回已轮换与读取错误。
func WaitForQoderRefresh(ctx context.Context, readChanged func(context.Context) (bool, error)) error {
	waitCtx, cancel := context.WithTimeout(ctx, qoderRefreshLockWait)
	defer cancel()
	var lastErr error
	if changed, err := readChanged(waitCtx); changed {
		return nil
	} else if err != nil {
		lastErr = err
	}
	ticker := time.NewTicker(qoderRefreshLockPoll)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("%w: %v", ErrQoderRefreshInProgress, lastErr)
			}
			return fmt.Errorf("%w: %v", ErrQoderRefreshInProgress, waitCtx.Err())
		case <-ticker.C:
			changed, err := readChanged(waitCtx)
			if changed {
				return nil
			}
			if err != nil {
				lastErr = err
			}
		}
	}
}
