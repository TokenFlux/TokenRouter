package account

import (
	"context"
	"errors"
	"fmt"
)

// SettingKeyOllamaCloudUsageSettings 保留已发布的配置键。
const SettingKeyOllamaCloudUsageSettings = "ollama_cloud_usage_settings"

// GetOllamaCloudUsageSettings 在设置缺失时返回默认关闭的配置。
func (s *RuntimeSettings) GetOllamaCloudUsageSettings(ctx context.Context) (*OllamaCloudUsageSettings, error) {
	defaults := DefaultOllamaCloudUsageSettings()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOllamaCloudUsageSettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return defaults, nil
		}
		return nil, fmt.Errorf("get Ollama Cloud usage settings: %w", err)
	}
	return DecodeOllamaCloudUsageSettings(raw)
}

// SetOllamaCloudUsageSettings 先完成原校验和编码，再持久化单键配置。
func (s *RuntimeSettings) SetOllamaCloudUsageSettings(ctx context.Context, settings *OllamaCloudUsageSettings) error {
	if s == nil || s.settingRepo == nil {
		return ErrOllamaCloudUsageUnavailable
	}
	data, err := EncodeOllamaCloudUsageSettings(settings)
	if err != nil {
		return err
	}
	return s.settingRepo.Set(ctx, SettingKeyOllamaCloudUsageSettings, data)
}
