//go:build unit

// 原私有测试入口只委托已迁实现，生产构建不保留无消费者的包装。
package service

import (
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

const grokTokenRefreshSkewMin = accountcore.GrokTokenRefreshSkewMin

func grokTokenRefreshWindowWithJitter(accountID int64, refreshWindow time.Duration) time.Duration {
	return accountcore.GrokTokenRefreshWindowWithJitter(accountID, refreshWindow)
}
