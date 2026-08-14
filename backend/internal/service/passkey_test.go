package service

import (
	"context"
	"strings"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizePasskeyName(t *testing.T) {
	require.Equal(t, defaultPasskeyName, normalizePasskeyName("   "))
	require.Equal(t, "Laptop", normalizePasskeyName("  Laptop  "))

	longName := strings.Repeat("密", maxPasskeyNameLength+10)
	// 数据库长度按字符计算，截断必须以 rune 为单位，不能切断 UTF-8 字节。
	require.Len(t, []rune(normalizePasskeyName(longName)), maxPasskeyNameLength)
}

func TestPasskeyServiceDisabledFailsClosed(t *testing.T) {
	svc, err := NewPasskeyService(&config.Config{}, nil, nil, nil)
	require.NoError(t, err)
	require.False(t, svc.Enabled())

	// 部署未显式配置 RP 安全边界时，公开登录入口也必须拒绝服务。
	_, _, err = svc.BeginLogin(context.Background())
	require.ErrorIs(t, err, ErrPasskeysDisabled)
}
