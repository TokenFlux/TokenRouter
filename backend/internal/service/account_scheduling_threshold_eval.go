// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

// EvaluateAccountSchedulingThreshold 只投影账号，纯规则已迁入 account。
func EvaluateAccountSchedulingThreshold(v *Account, thresholds map[string]int, now time.Time) account.AccountSchedulingThresholdDecision {
	return account.EvaluateAccountSchedulingThreshold(AccountRecordView(v), thresholds, now)
}
func parseSchedulingTime(raw string) (time.Time, error) { return account.ParseSchedulingTime(raw) }
