package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account/transfer"
)

// 导入模板只保存允许的账号缺省配置，不保存认证身份。
type OpenAIOAuthImportAccountDefaults = transfer.OpenAIOAuthImportAccountDefaults
type OpenAIOAuthImportDefaults = transfer.OpenAIOAuthImportDefaults

// SettingKeyOpenAIOAuthImportDefaults 保留已有模板存储键。
const SettingKeyOpenAIOAuthImportDefaults = "openai_oauth_import_defaults"

// GetOpenAIOAuthImportDefaults 沿用导入模板的独立存取语义。
func (s *RuntimeSettings) GetOpenAIOAuthImportDefaults(ctx context.Context) (*OpenAIOAuthImportDefaults, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIOAuthImportDefaults)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return DefaultOpenAIOAuthImportDefaults(), nil
		}
		return nil, fmt.Errorf("get openai oauth import defaults: %w", err)
	}
	if value == "" {
		return DefaultOpenAIOAuthImportDefaults(), nil
	}

	var settings OpenAIOAuthImportDefaults
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		slog.Warn("failed to unmarshal openai oauth import defaults, falling back to defaults",
			"error", err,
			"key", SettingKeyOpenAIOAuthImportDefaults)
		return DefaultOpenAIOAuthImportDefaults(), nil
	}

	return FillOpenAIOAuthImportDefaults(&settings), nil
}

// SetOpenAIOAuthImportDefaults 沿用导入模板的独立存取语义。
func (s *RuntimeSettings) SetOpenAIOAuthImportDefaults(ctx context.Context, settings *OpenAIOAuthImportDefaults) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if err := ValidateOpenAIOAuthImportDefaults(settings); err != nil {
		return err
	}
	// 模板和账号写入使用同一兼容边界，不修改调用方持有的原始对象。
	normalized := *settings
	normalized.Extra = maps.Clone(settings.Extra)
	NormalizeLegacyOpenAIAccountExtra(normalized.Extra)

	data, err := json.Marshal(&normalized)
	if err != nil {
		return fmt.Errorf("marshal openai oauth import defaults: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyOpenAIOAuthImportDefaults, string(data))
}

// FillOpenAIOAuthImportDefaults 保留原模板校验及兼容规范化。
func FillOpenAIOAuthImportDefaults(settings *OpenAIOAuthImportDefaults) *OpenAIOAuthImportDefaults {
	if settings == nil {
		return DefaultOpenAIOAuthImportDefaults()
	}

	defaults := DefaultOpenAIOAuthImportDefaults()
	// 读取历史模板也只向调用方暴露明确配置，不再传播旧自动模式和探测字段。
	settings.Extra = maps.Clone(settings.Extra)
	NormalizeLegacyOpenAIAccountExtra(settings.Extra)
	if len(defaults.Credentials) > 0 {
		if settings.Credentials == nil {
			settings.Credentials = map[string]any{}
		}
		for key, value := range defaults.Credentials {
			// 已显式保存的键保持原样；空数组可用于表达“不限制模型”。
			if _, exists := settings.Credentials[key]; !exists {
				settings.Credentials[key] = value
			}
		}
	}
	return settings
}

// FindForbiddenImportField 保留原模板校验及兼容规范化。
func FindForbiddenImportField(fields map[string]any, forbidden map[string]struct{}) (string, bool) {
	for key := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := forbidden[normalized]; ok {
			return key, true
		}
	}
	return "", false
}

// ValidateOpenAIOAuthImportDefaults 保留原模板校验及兼容规范化。
func ValidateOpenAIOAuthImportDefaults(settings *OpenAIOAuthImportDefaults) error {
	if settings.Account.Concurrency != nil && *settings.Account.Concurrency < 0 {
		return fmt.Errorf("account.concurrency must be >= 0")
	}
	if settings.Account.Priority != nil && *settings.Account.Priority < 0 {
		return fmt.Errorf("account.priority must be >= 0")
	}
	if settings.Account.RateMultiplier != nil && *settings.Account.RateMultiplier < 0 {
		return fmt.Errorf("account.rate_multiplier must be >= 0")
	}
	if settings.Account.ExpiresAt != nil && *settings.Account.ExpiresAt < 0 {
		return fmt.Errorf("account.expires_at must be >= 0")
	}

	forbiddenCredentials := map[string]struct{}{
		"access_token":            {},
		"refresh_token":           {},
		"id_token":                {},
		"expires_at":              {},
		"email":                   {},
		"client_id":               {},
		"chatgpt_account_id":      {},
		"chatgpt_user_id":         {},
		"organization_id":         {},
		"plan_type":               {},
		"subscription_expires_at": {},
	}
	if field, ok := FindForbiddenImportField(settings.Credentials, forbiddenCredentials); ok {
		return fmt.Errorf("credentials.%s is not allowed in import defaults", field)
	}

	forbiddenExtra := map[string]struct{}{
		"email": {},
		"name":  {},
	}
	if field, ok := FindForbiddenImportField(settings.Extra, forbiddenExtra); ok {
		return fmt.Errorf("extra.%s is not allowed in import defaults", field)
	}

	return nil
}

// DefaultOpenAIOAuthImportDefaults 保留原内置模型白名单。
func DefaultOpenAIOAuthImportDefaults() *OpenAIOAuthImportDefaults {
	return &OpenAIOAuthImportDefaults{
		Credentials: map[string]any{
			"model_whitelist": []string{
				"gpt-5.2",
				"gpt-5.3",
				"gpt-5.3-spark",
				"gpt-5.4",
				"gpt-5.4-mini",
				"gpt-5.5",
			},
		},
	}
}
