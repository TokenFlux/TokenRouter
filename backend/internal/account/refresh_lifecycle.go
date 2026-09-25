// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"errors"
)

var ErrRefreshStopped = errors.New("oauth refresh coordinator is stopped")

type refreshActivity = operationActivity

// beginRefresh 将原取消语义和账号活动拥有者连接，停止后不再认领。
func (api *OAuthRefreshAPI) beginRefresh(parent context.Context) (context.Context, func(), error) {
	return api.activity.begin(parent, ErrRefreshStopped)
}

// checkRefreshActive 在取得账号锁后复核停止屏障，避免先释放锁、后取消等待者时开启新交换。
func (api *OAuthRefreshAPI) checkRefreshActive(ctx context.Context) error {
	api.activity.mu.Lock()
	defer api.activity.mu.Unlock()
	if api.activity.stopped {
		return context.Canceled
	}
	return ctx.Err()
}

// StopContext 复用首次停止结果，超时不能报告已排空。
func (api *OAuthRefreshAPI) StopContext(ctx context.Context) error {
	if api == nil {
		return nil
	}
	return api.activity.stop(ctx, "oauth refresh")
}
