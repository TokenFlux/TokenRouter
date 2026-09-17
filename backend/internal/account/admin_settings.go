package account

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
)

// AdminSettings 明确区分账号健康阈值和通知目的地。
type AdminSettings struct {
	AccountQuotaNotifyEnabled   bool            `json:"account_quota_notify_enabled"`
	AccountQuotaNotifyEmails    []contact.Entry `json:"account_quota_notify_emails"`
	AccountSchedulingThresholds map[string]int  `json:"account_scheduling_thresholds"`
}

const SettingKeyAccountQuotaNotifyEnabled = "account_quota_notify_enabled"
const SettingKeyAccountQuotaNotifyEmails = "account_quota_notify_emails"

// PrepareAdminSettings 不执行处置或发送通知，保留旧邮箱序列化和 nil 阈值语义。
func PrepareAdminSettings(value AdminSettings) (map[string]string, error) {
	values := map[string]string{SettingKeyAccountQuotaNotifyEnabled: strconv.FormatBool(value.AccountQuotaNotifyEnabled), SettingKeyAccountQuotaNotifyEmails: contact.MarshalNotifyEmails(value.AccountQuotaNotifyEmails)}
	if value.AccountSchedulingThresholds != nil {
		normalized, err := ValidateAndNormalizeAccountSchedulingThresholds(value.AccountSchedulingThresholds)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(normalized)
		if err != nil {
			return nil, fmt.Errorf("marshal account scheduling thresholds: %w", err)
		}
		values[SettingKeyAccountSchedulingThresholds] = string(raw)
	}
	return values, nil
}
