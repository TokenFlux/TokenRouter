package testkit

import (
	_ "embed"
	"encoding/json"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/stretchr/testify/require"
)

const LegacyEncryptionKey = "0123456789abcdef0123456789abcdef"

// 固定历史密文保留旧格式解密契约，不重新调用旧加密入口生成。
//
//go:embed testdata/legacy_webhook_configs.json
var legacyConfigsJSON []byte

func LegacyConfig(t *testing.T, name string) string {
	t.Helper()
	var fixtures map[string]string
	require.NoError(t, json.Unmarshal(legacyConfigsJSON, &fixtures))
	value, ok := fixtures[name]
	require.True(t, ok, "unknown legacy webhook fixture %q", name)
	return value
}

// LegacyLoadBalancer 使用上述历史密文对应的固定测试密钥。
func LegacyLoadBalancer(client *dbent.Client) payment.LoadBalancer {
	return payment.NewDefaultLoadBalancer(paymentpostgres.NewInstanceStore(client), []byte(LegacyEncryptionKey))
}
