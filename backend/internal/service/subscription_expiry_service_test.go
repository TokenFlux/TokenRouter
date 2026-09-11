package service

import (
	"context"
	"errors"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
	"testing"
)

type expirySettingsFixture struct {
	SettingRepository
	err error
}

func (r expirySettingsFixture) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, r.err
}

// 真实通知服务的 SMTP 状态必须映射为核心可识别的未配置状态。
func TestExpiryNotificationReadinessBridge(t *testing.T) {
	for _, failure := range []error{nil, errors.New("db down")} {
		repo := expirySettingsFixture{err: failure}
		adapter := LegacyExpiryNotifier{Service: NewNotificationEmailService(repo, NewEmailService(repo, nil))}
		err := adapter.Ready(context.Background())
		if failure == nil {
			require.ErrorIs(t, err, billing.ErrReminderTransportUnconfigured)
		} else {
			require.ErrorIs(t, err, failure)
		}
	}
}
