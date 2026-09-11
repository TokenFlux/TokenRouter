// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

type NotifyEmailEntry = billing.NotifyEmailSummary

func ParseNotifyEmails(raw string) []NotifyEmailEntry { return billing.ParseNotifyEmails(raw) }

func MarshalNotifyEmails(entries []NotifyEmailEntry) string {
	return billing.MarshalNotifyEmails(entries)
}
