// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// SanitizeStoredCredentials 委托所属模块的唯一实现。
func SanitizeStoredCredentials(platform string, creds map[string]any) map[string]any {
	return acctcore.SanitizeStoredCredentials(platform, creds)
}
