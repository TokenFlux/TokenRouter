package site

import (
	"context"
	"strings"
)

// ReadFrontendURL 保留数据库优先和按需取得启动回退值的原顺序。
func ReadFrontendURL(ctx context.Context, store interface {
	GetValue(context.Context, string) (string, error)
}, fallback func() string) string {
	val, err := store.GetValue(ctx, SettingKeyFrontendURL)
	if err == nil && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback()
}
