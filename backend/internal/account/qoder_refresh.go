// 本文件拥有 Qoder 刷新资格与凭据合并；供应商响应由平台适配投影。
package account

import (
	"strconv"
	"strings"
	"time"
)

func CanRefreshQoder(account *Record) bool {
	return account != nil && account.Platform == PlatformQoder && account.Type == AccountTypeCosy
}
func NeedsRefreshQoder(account *Record, _ time.Duration) bool {
	if !CanRefreshQoder(account) {
		return false
	}
	if strings.TrimSpace(account.GetCredential("pat")) != "" {
		return false
	}
	if strings.TrimSpace(account.GetCredential("refresh_token")) == "" {
		return false
	}
	if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
		return time.Until(*expiresAt) < 15*time.Minute
	}
	// Qoder 的 RateLimitResetAt 也承载 upstream 月度 quota / agent-limit 调度信号，
	// 不能像 OpenAI 一样把通用限流状态当成后台 token refresh 触发器。
	return false
}
func QoderTokenCacheKey(account *Record) string {
	if account == nil {
		return "qoder:account:0"
	}
	return "qoder:account:" + strconv.FormatInt(account.ID, 10)
}
func MergeQoderRefreshCredentials(oldCredentials, newCredentials map[string]any, site, refreshMode string, expiresAt time.Time) map[string]any {
	newCredentials = MergeCredentials(oldCredentials, newCredentials)
	newCredentials["site"] = string(site)
	newCredentials["refresh_mode"] = refreshMode
	if site == "cn" {
		// 合并旧凭据后再次清理，避免刷新把历史随机机器字段带回国内账号。
		delete(newCredentials, "machine_token")
		delete(newCredentials, "machine_type")
	}
	if !expiresAt.IsZero() {
		newCredentials["expires_at"] = expiresAt.UTC().Format(time.RFC3339)
	}
	refreshToken := strings.TrimSpace(qoderOldRefreshToken(oldCredentials))
	if strings.TrimSpace(stringFromCredentialValue(newCredentials["refresh_token"])) == "" {
		if refreshToken != "" {
			newCredentials["refresh_token"] = refreshToken
		}
	}
	// 目前观测到的 Qoder refresh 响应没有可靠的新过期时间。
	// 不保留导入时的旧 expires_at，否则 NeedsRefresh 会立刻把刚刷新的账号
	// 判定为即将过期，并可能形成刷新循环。
	if expiresAt.IsZero() {
		delete(newCredentials, "expires_at")
	}
	return newCredentials
}
func stringFromCredentialValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}
func qoderOldRefreshToken(v map[string]any) string {
	return stringFromCredentialValue(v["refresh_token"])
}

// NeedsRefreshQoderAfterFailure 使用失败时的凭据身份判断请求期刷新。
// 已轮换凭据不重复消费刷新令牌；同一失败身份不受临近过期窗口限制。
func NeedsRefreshQoderAfterFailure(value *Record, failedCredentials string, ttl time.Duration) bool {
	if !CanRefreshQoder(value) {
		return false
	}
	if strings.TrimSpace(value.GetCredential("refresh_token")) == "" {
		return false
	}
	if failedCredentials != "" {
		return QoderRefreshCredentialsHash(value.Credentials) == failedCredentials
	}
	return NeedsRefreshQoder(value, ttl)
}
