// 本文件维护 billing 的所属能力；兼容入口复用唯一实现。
package billing

import (
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
)

// ParseNotifyEmails 委托身份邮箱格式兼容。
func ParseNotifyEmails(raw string) []NotifyEmailSummary { return contact.ParseNotifyEmails(raw) }

// MarshalNotifyEmails 委托身份邮箱序列化。
func MarshalNotifyEmails(entries []NotifyEmailSummary) string {
	return contact.MarshalNotifyEmails(entries)
}
