// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	errors "errors"
)

var ErrRefreshStopped = errors.New("oauth refresh coordinator is stopped")

type refreshActivity = operationActivity

// beginRefresh 将原取消语义和账号活动拥有者连接，停止后不再认领。
func (api *OAuthRefreshAPI) beginRefresh(parent context.Context) (context.Context, func(), error) {
	return api.activity.begin(parent, ErrRefreshStopped)
}

// StopContext 复用首次停止结果，超时不能报告已排空。
func (api *OAuthRefreshAPI) StopContext(ctx context.Context) error {
	if api == nil {
		return nil
	}
	return api.activity.stop(ctx, "oauth refresh")
}
