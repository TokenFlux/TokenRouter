package account

import (
	"log/slog"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
)

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	AccountQuotaNotifyEmails    []contact.Entry
	AccountQuotaNotifyEnabled   bool
	AccountSchedulingThresholds map[string]int
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}

	result.AccountQuotaNotifyEnabled = settings[SettingKeyAccountQuotaNotifyEnabled] == "true"
	if raw := strings.TrimSpace(settings[SettingKeyAccountQuotaNotifyEmails]); raw != "" {
		result.AccountQuotaNotifyEmails = contact.ParseNotifyEmails(raw)
	}
	if result.AccountQuotaNotifyEmails == nil {
		result.AccountQuotaNotifyEmails = []contact.Entry{}
	}
	result.AccountSchedulingThresholds = DefaultAccountSchedulingThresholds()
	if raw := strings.TrimSpace(settings[SettingKeyAccountSchedulingThresholds]); raw != "" {
		if thresholds, err := ParseAccountSchedulingThresholdsSetting(raw); err != nil {
			slog.Warn("[Setting] parseSettings: unmarshal account_scheduling_thresholds failed", "error", err)
		} else {
			result.AccountSchedulingThresholds = thresholds
		}
	}
	return result
}
