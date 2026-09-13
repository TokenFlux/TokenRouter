// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

var SensitiveCredentialKeys = acctcore.SensitiveCredentialKeys

// IsSensitiveCredentialKey 委托所属模块的唯一实现。
func IsSensitiveCredentialKey(key string) bool { return acctcore.IsSensitiveCredentialKey(key) }

// MergePreservingSensitiveCreds 委托所属模块的唯一实现。
func MergePreservingSensitiveCreds(existing, incoming map[string]any) map[string]any {
	return acctcore.MergePreservingSensitiveCreds(existing, incoming)
}
