// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// GeminiQuotaUsageReader 只转交原统计查询，S08 改绑 SQL 来源。
func GeminiQuotaUsageReader(source service.UsageLogRepository) account.GeminiQuotaUsageReader {
	return service.LegacyGeminiUsageReader(source)
}
