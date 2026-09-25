package provider_test

import (
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 以下合同直接验证所属模块，保留原输入与断言。
// gatewayprovider.ApplyCodexClientMetadata：用账号真实 device_id 注入 installation 标识，幂等、不覆盖既有项、不伪造。
func TestApplyCodexClientMetadata(t *testing.T) {
	// 仅 OpenAI OAuth 账号才有 device_id（GetOpenAIDeviceID 的门控）。
	acc := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Extra: map[string]any{"openai_device_id": "dev-xyz"}}}

	body := map[string]any{}
	require.True(t, gatewayprovider.ApplyCodexClientMetadata(body, acc))
	cm, ok := body["client_metadata"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "dev-xyz", cm["x-codex-installation-id"])
	// 幂等
	require.False(t, gatewayprovider.ApplyCodexClientMetadata(body, acc))

	// OAuth 账号但无 device_id → 不写入（不伪造）
	body2 := map[string]any{}
	require.False(t, gatewayprovider.ApplyCodexClientMetadata(body2, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}))
	_, ok = body2["client_metadata"]
	require.False(t, ok)

	// 既有 client_metadata（如 turn metadata）保留，仅补 installation 键
	body3 := map[string]any{"client_metadata": map[string]any{"x-codex-turn-metadata": "t"}}
	require.True(t, gatewayprovider.ApplyCodexClientMetadata(body3, acc))
	cm3, _ := body3["client_metadata"].(map[string]any)
	require.Equal(t, "t", cm3["x-codex-turn-metadata"])
	require.Equal(t, "dev-xyz", cm3["x-codex-installation-id"])
}
