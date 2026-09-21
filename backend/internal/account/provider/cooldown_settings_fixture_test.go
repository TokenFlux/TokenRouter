//go:build unit

package provider

import (
	"context"
	"errors"
)

// 设置替身只存取单键，不复制运行配置缓存。
type cooldownSettingsStore struct{ data map[string]string }

var errCooldownSettingMissing = errors.New("setting missing")

func newCooldownSettingsStore() *cooldownSettingsStore {
	return &cooldownSettingsStore{data: map[string]string{}}
}
func (s *cooldownSettingsStore) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.data[key]
	if !ok {
		return "", errCooldownSettingMissing
	}
	return value, nil
}
func (s *cooldownSettingsStore) Set(_ context.Context, key, value string) error {
	s.data[key] = value
	return nil
}
