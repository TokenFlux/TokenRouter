// 兼容审计类型和脱敏入口；业务实现由 audit 唯一提供。
package service

import (
	"sync"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/audit"
)

type AuditLog = audit.AuditLog
type AuditLogFilter = audit.AuditLogFilter
type AuditLogList = audit.AuditLogList
type AuditLogRepository = audit.AuditLogRepository

const AuditAuthMethodJWT = audit.AuditAuthMethodJWT
const AuditAuthMethodAdminAPIKey = audit.AuditAuthMethodAdminAPIKey
const AuditRequestBodyCaptureLimit = audit.AuditRequestBodyCaptureLimit
const AuditActionLogin = audit.AuditActionLogin
const AuditActionLogin2FA = audit.AuditActionLogin2FA
const AuditActionRegister = audit.AuditActionRegister
const AuditActionTokenRefresh = audit.AuditActionTokenRefresh
const AuditActionSessionBindingMismatch = audit.AuditActionSessionBindingMismatch
const AuditActionStepUpVerify = audit.AuditActionStepUpVerify
const AuditActionAuditLogClear = audit.AuditActionAuditLogClear
const AuditActionUserSubscriptionRevoke = audit.AuditActionUserSubscriptionRevoke

var ErrAuditLogNotFound = audit.ErrAuditLogNotFound

// LegacyAuditSensitiveKeys 只投影尚未清零的账号/支付清单，S12/S16 退出。
func LegacyAuditSensitiveKeys() []string {
	keys := append([]string(nil), SensitiveCredentialKeys...)
	for _, fields := range providerSensitiveConfigFields {
		for k := range fields {
			keys = append(keys, k)
		}
	}
	return keys
}

var legacyAuditRedactor atomic.Pointer[audit.Redactor]
var legacyAuditRedactorOnce sync.Once

func currentAuditRedactor() *audit.Redactor {
	legacyAuditRedactorOnce.Do(func() { legacyAuditRedactor.CompareAndSwap(nil, audit.NewRedactor(LegacyAuditSensitiveKeys())) })
	return legacyAuditRedactor.Load()
}

// BindAuditRedactor 让旧捕获入口使用 app 装配的同一个只读策略。
func BindAuditRedactor(r *audit.Redactor) { legacyAuditRedactor.Store(r) }
func RedactAuditBody(raw []byte, contentType string) string {
	return currentAuditRedactor().RedactBody(raw, contentType)
}
func RedactAuditQuery(raw string) string      { return audit.RedactAuditQuery(raw) }
func MaskAuditCredential(raw string) string   { return audit.MaskAuditCredential(raw) }
func isAuditSensitiveBodyKey(key string) bool { return currentAuditRedactor().IsSensitiveKey(key) }

// CurrentAuditRedactor 返回装配绑定的同一只读脱敏策略。
func CurrentAuditRedactor() *audit.Redactor { return currentAuditRedactor() }
