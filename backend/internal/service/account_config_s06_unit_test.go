//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"time"
)

// 旧白盒用例只保留测试转接，生产实现归 account。
var deprecatedOpenAIAccountExtraKeys = acctcore.DeprecatedOpenAIAccountExtraKeys

func isInvalidGrantError(err error) bool { return acctcore.IsInvalidGrantError(err) }

func nextFixedDailyReset(hour int, tz *time.Location, after time.Time) time.Time {
	return acctcore.NextFixedDailyReset(hour, tz, after)
}

func lastFixedDailyReset(hour int, tz *time.Location, now time.Time) time.Time {
	return acctcore.LastFixedDailyReset(hour, tz, now)
}

func nextFixedWeeklyReset(day, hour int, tz *time.Location, after time.Time) time.Time {
	return acctcore.NextFixedWeeklyReset(day, hour, tz, after)
}

func lastFixedWeeklyReset(day, hour int, tz *time.Location, now time.Time) time.Time {
	return acctcore.LastFixedWeeklyReset(day, hour, tz, now)
}

func normalizeAccountConcurrency(platform, accountType string, concurrency int) int {
	return acctcore.NormalizeAccountConcurrency(platform, accountType, concurrency)
}

func normalizeGrokMediaEligibilityExtra(platform string, extra map[string]any) (map[string]any, error) {
	return acctcore.NormalizeGrokMediaEligibilityExtra(platform, extra)
}

func normalizeGrokMediaEligibilityUpdateExtra(account *Account, input *UpdateAccountInput, normalized map[string]any) (map[string]any, error) {
	return acctcore.NormalizeGrokMediaEligibilityUpdateExtra(protocolRecord(account), input, normalized)
}

func applyAntigravityPrivacyMode(account *Account, mode string) {
	v := privacyRecord(account)
	acctcore.ApplyAntigravityPrivacyMode(v, mode)
	if account != nil && v != nil {
		account.Extra = v.Extra
	}
}

const legacyOpenAICapabilitiesCredentialKey = acctcore.LegacyOpenAICapabilitiesCredentialKey

const legacyOpenAIResponsesModeExtraKey = acctcore.LegacyOpenAIResponsesModeExtraKey

func normalizeOpenAIAPIKeyConfigurationPatch(credentials, extra map[string]any) error {
	return acctcore.NormalizeOpenAIAPIKeyConfigurationPatch(credentials, extra)
}

func normalizeOpenAIAPIKeyConfiguration(account *Account) error {
	v := protocolRecord(account)
	err := acctcore.NormalizeOpenAIAPIKeyConfiguration(v)
	applyProtocolRecord(account, v)
	return err
}
