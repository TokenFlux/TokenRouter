// 消费上限是可恢复的账号状态；读取保持既有账期与软性重授权标记。
package account

import (
	"strings"
	"time"
)

const grokSpendingLimitProbeCooldown = 10 * time.Minute

func GrokSpendingLimitResetAt(account *Record, now time.Time) time.Time {
	if account != nil {
		if billing, err := ParseGrokBillingSnapshot(account.Extra); err == nil && billing != nil {
			for _, raw := range []string{billing.PeriodEnd, billing.BillingPeriodEnd} {
				if resetAt, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil && resetAt.After(now) {
					return resetAt
				}
			}
		}
	}
	return now.Add(grokSpendingLimitProbeCooldown)
}
func GrokNeedsReauth(account *Record) bool {
	if account == nil {
		return false
	}
	if account.Status == StatusError {
		msg := strings.ToLower(account.ErrorMessage)
		if strings.Contains(msg, "spending limit") || strings.Contains(msg, "reauthorize") {
			return true
		}
	}
	if v, ok := account.Extra["grok_needs_reauth"].(bool); ok && v {
		return true
	}
	if s, ok := account.Extra["grok_needs_reauth"].(string); ok {
		return strings.EqualFold(s, "true") || s == "1"
	}
	return false
}
