// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

// ApplyKeyUsageReset 将消费字段附加到调用方的同一条更新语句，不提交事务。
func ApplyKeyUsageReset(builder *dbent.APIKeyUpdate, input billing.KeyUsageReset) {
	if input.ResetQuota {
		builder.SetQuotaUsed(input.QuotaUsed)
	}

	if input.ResetWindows {
		builder.
			SetUsage5h(input.Windows.Usage5h).
			SetUsage1d(input.Windows.Usage1d).
			SetUsage7d(input.Windows.Usage7d)

		// Rate limit window start times
		if input.Windows.Window5hStart != nil {
			builder.SetWindow5hStart(*input.Windows.Window5hStart)
		} else {
			builder.ClearWindow5hStart()
		}
		if input.Windows.Window1dStart != nil {
			builder.SetWindow1dStart(*input.Windows.Window1dStart)
		} else {
			builder.ClearWindow1dStart()
		}
		if input.Windows.Window7dStart != nil {
			builder.SetWindow7dStart(*input.Windows.Window7dStart)
		} else {
			builder.ClearWindow7dStart()
		}
	}
}
