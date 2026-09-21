package app

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestPasskeyServiceDisabledFailsClosed(t *testing.T) {
	svc, err := providePasskey(&config.Config{}, nil, nil, nil)
	require.NoError(t, err)
	require.False(t, svc.Enabled())

	// 部署未显式配置 RP 安全边界时，公开登录入口也必须拒绝服务。
	_, _, err = svc.BeginLogin(context.Background())
	require.ErrorIs(t, err, identity.ErrPasskeysDisabled)
}
