//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"time"
)

// 原窗口选择测试通过兼容投影验证唯一账号规则。
func cnProviderQuotaSnapshotReset(v *Account, now time.Time) *time.Time {
	return account.CNProviderQuotaSnapshotReset(AccountRecordView(v), now)
}
