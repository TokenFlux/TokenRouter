// 本文件维护 billing 的所属能力；兼容入口复用唯一实现。
package billing

// KeyUsageReset 是由 Key 用例授权的显式消费重置。
type KeyUsageReset struct {
	ResetQuota, ResetWindows bool
	QuotaUsed                float64
	Windows                  APIKeyRateLimitData
}
