package service

import "context"

// GetOpenAIAPIKeyHealthBreakerSettings 委托账号模块唯一缓存。
func (s *SettingService) GetOpenAIAPIKeyHealthBreakerSettings(ctx context.Context) (*OpenAIAPIKeyHealthBreakerSettings, error) {
	return s.AccountSettings().GetOpenAIAPIKeyHealthBreakerSettings(ctx)
}
