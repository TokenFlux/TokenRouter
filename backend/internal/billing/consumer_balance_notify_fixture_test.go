//go:build unit

package billing

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
)

const (
	thresholdTypeFixed      = "fixed"
	thresholdTypePercentage = "percentage"
	quotaDimDaily           = "daily"
	quotaDimWeekly          = "weekly"
	quotaDimTotal           = "total"
)

// 通知判断测试只替换设置与投递端口，意外发送立即使原守卫断言失败。
type notifySettingsFixture struct{ data map[string]string }

func (s *notifySettingsFixture) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.data[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

func (s *notifySettingsFixture) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.data[key]; ok {
		return value, nil
	}
	return "", errors.New("setting not found")
}

type unexpectedAlertSender struct{}

func (unexpectedAlertSender) SendBalanceLowEmails([]string, int64, string, string, float64, float64, string, string) {
	panic("unexpected balance notification")
}

func (unexpectedAlertSender) SendQuotaAlertEmails([]string, int64, string, string, contract.QuotaDimension, float64, string) {
	panic("unexpected quota notification")
}

func newBalanceNotifyServiceForTest() (*BalanceNotifyService, *notifySettingsFixture) {
	settings := &notifySettingsFixture{data: make(map[string]string)}
	return NewBalanceNotifyService(unexpectedAlertSender{}, settings, nil, func(string, func()) {
		panic("unexpected notification dispatch")
	}), settings
}
