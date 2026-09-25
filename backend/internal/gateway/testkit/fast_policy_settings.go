// FastPolicySettingsRepo 只提供档位策略测试用到的设置存取。
package testkit

import (
	"context"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

type FastPolicySettingsRepo struct {
	Values map[string]string
}

func (s *FastPolicySettingsRepo) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}
func (s *FastPolicySettingsRepo) GetValue(ctx context.Context, key string) (string, error) {
	if v, ok := s.Values[key]; ok {
		return v, nil
	}
	return "", settingscore.ErrSettingNotFound
}
func (s *FastPolicySettingsRepo) Set(ctx context.Context, key, value string) error {
	if s.Values == nil {
		s.Values = map[string]string{}
	}
	s.Values[key] = value
	return nil
}

// GetMultiple 按请求键读取既有夹具数据，缺省设置继续由生产读取器处理。
func (s *FastPolicySettingsRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	Values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.Values[key]; ok {
			Values[key] = value
		}
	}
	return Values, nil
}
func (s *FastPolicySettingsRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}
func (s *FastPolicySettingsRepo) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}
func (s *FastPolicySettingsRepo) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}
