// 版本检查在原回填位置执行，不引入新的缓存版本。
package account

import (
	"context"
)

// CheckTokenVersion 检查 account 的 token 版本是否已过时，并返回最新的 account
// 用于解决异步刷新任务与请求线程的竞态条件：
// 如果刷新任务已更新 token 并删除缓存，此时请求线程的旧 account 对象不应写入缓存
//
// 返回值:
//   - latestAccount: 从 DB 获取的最新 account（如果查询失败则返回 nil）
//   - isStale: true 表示 token 已过时（应使用 latestAccount），false 表示可以使用当前 account
func CheckTokenVersion(ctx context.Context, account *Record, repo RefreshRepository, observers ...func(string, ...any)) (latestAccount *Record, isStale bool) {
	debug := func(string, ...any) {}
	if len(observers) > 0 && observers[0] != nil {
		debug = observers[0]
	}

	if account == nil || repo == nil {
		return nil, false
	}

	currentVersion := account.GetCredentialAsInt64("_token_version")

	latestAccount, err := repo.GetByID(ctx, account.ID)
	if err != nil || latestAccount == nil {
		// 查询失败，默认允许缓存，不返回 latestAccount
		return nil, false
	}

	latestVersion := latestAccount.GetCredentialAsInt64("_token_version")

	// 情况1: 当前 account 没有版本号，但 DB 中已有版本号
	// 说明异步刷新任务已更新 token，当前 account 已过时
	if currentVersion == 0 && latestVersion > 0 {
		debug("token_version_stale_no_current_version",
			"account_id", account.ID,
			"latest_version", latestVersion)
		return latestAccount, true
	}

	// 情况2: 两边都没有版本号，说明从未被异步刷新过，允许缓存
	if currentVersion == 0 && latestVersion == 0 {
		return latestAccount, false
	}

	// 情况3: 比较版本号，如果 DB 中的版本更新，当前 account 已过时
	if latestVersion > currentVersion {
		debug("token_version_stale",
			"account_id", account.ID,
			"current_version", currentVersion,
			"latest_version", latestVersion)
		return latestAccount, true
	}

	return latestAccount, false
}
