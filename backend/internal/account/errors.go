// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var (
	ErrAccountNotFound      = billing.ErrAccountNotFound
	ErrAccountNilInput      = apperror.BadRequest("ACCOUNT_NIL_INPUT", "account input cannot be nil")
	ErrAccountNotInFallback = apperror.BadRequest("ACCOUNT_NOT_IN_FALLBACK", "account is not in proxy fallback state")
)

// DiscardDeprecatedExtra 保持原写入边界的废弃键清理。
func DiscardDeprecatedExtra(extra map[string]any) {
	delete(extra, "upstream_billing_probe")
	delete(extra, "upstream_billing_probe_enabled")
	delete(extra, "openai_long_context_billing_enabled")
}
