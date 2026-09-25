// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"errors"
	"time"
)

// CreationOptions 提供构造账号值所需的时钟、时区和 seed，不加载完整应用配置。
type CreationOptions struct {
	Now          func() time.Time
	LoadLocation func(string) (*time.Location, error)
	NewSeed      func() string
}

func BuildAccountForCreate(input *CreateAccountInput, accountExtra map[string]any, options CreationOptions) (*Record, error) {
	// 受管会话状态由系统维护，废弃字段不得通过通用账号接口写入。
	DiscardDeprecatedAccountExtra(accountExtra)
	if err := NormalizeUpstreamUsageExtra(accountExtra); err != nil {
		return nil, err
	}
	delete(accountExtra, OllamaCloudUsageSessionExtraKey)
	delete(accountExtra, OllamaCloudUsageAutoRefreshExtraKey)
	delete(accountExtra, OllamaCloudUsageSnapshotExtraKey)
	delete(accountExtra, CNUsageMonitorSnapshotExtraKey)
	accountExtra = PrepareCodexFingerprintExtraForCreate(input.Platform, input.Type, accountExtra, options.NewSeed)
	account := &Record{
		Now: options.Now, LoadLocation: options.LoadLocation,
		Name:        input.Name,
		Notes:       NormalizeAccountNotes(input.Notes),
		Platform:    input.Platform,
		Type:        input.Type,
		Credentials: input.Credentials,
		Extra:       accountExtra,
		ProxyID:     input.ProxyID,
		Concurrency: NormalizeAccountConcurrency(input.Platform, input.Type, input.Concurrency),
		Priority:    input.Priority,
		Status:      StatusActive,
		Schedulable: true,
	}
	if err := NormalizeCNProviderCredentials(account, true); err != nil {
		return nil, err
	}
	if err := NormalizeOpenAIAPIKeyConfiguration(account); err != nil {
		return nil, err
	}
	if err := NormalizeAccountProtocols(account); err != nil {
		return nil, err
	}
	// 预计算固定时间重置的下次重置时间
	if account.Extra != nil {
		if err := ValidateQuotaResetConfig(account.Extra, options.LoadLocation); err != nil {
			return nil, err
		}
		ComputeQuotaResetAt(account.Extra, options.Now(), options.LoadLocation)
		NormalizeFixedQuotaWindows(account.Extra, options.Now(), options.LoadLocation)
	}
	if input.ExpiresAt != nil && *input.ExpiresAt > 0 {
		expiresAt := time.Unix(*input.ExpiresAt, 0)
		account.ExpiresAt = &expiresAt
	}
	if input.AutoPauseOnExpired != nil {
		account.AutoPauseOnExpired = *input.AutoPauseOnExpired
	} else {
		account.AutoPauseOnExpired = true
	}
	if input.RateMultiplier != nil {
		if *input.RateMultiplier < 0 {
			return nil, errors.New("rate_multiplier must be >= 0")
		}
		account.RateMultiplier = input.RateMultiplier
	}
	if input.LoadFactor != nil && *input.LoadFactor > 0 {
		if *input.LoadFactor > 10000 {
			return nil, errors.New("load_factor must be <= 10000")
		}
		account.LoadFactor = input.LoadFactor
	}
	if err := ValidateGeminiThirdPartyBaseURL(account); err != nil {
		return nil, err
	}
	return account, nil
}
