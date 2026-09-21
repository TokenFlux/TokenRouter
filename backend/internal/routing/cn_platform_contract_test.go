//go:build unit

package routing

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestNormalizeOpenAICompatiblePlatform_SchedulerExactMatch 回归保护：
// grok 与国产供应商原样保留，其余归一为 openai —— 保证 kimi/zhipu/deepseek 分组请求
// 精确匹配同名账号（与 openai/grok 当前行为一致），不会错误并入 openai 池。
func TestNormalizeOpenAICompatiblePlatform_SchedulerExactMatch(t *testing.T) {
	t.Parallel()
	require.Equal(t, capability.PlatformGrok, NormalizeOpenAICompatiblePlatform(capability.PlatformGrok))
	require.Equal(t, capability.PlatformKimi, NormalizeOpenAICompatiblePlatform(capability.PlatformKimi))
	require.Equal(t, capability.PlatformZhipu, NormalizeOpenAICompatiblePlatform(capability.PlatformZhipu))
	require.Equal(t, capability.PlatformDeepseek, NormalizeOpenAICompatiblePlatform(capability.PlatformDeepseek))
	// 其他平台（含空、anthropic、未知）一律归一为 openai。
	require.Equal(t, capability.PlatformOpenAI, NormalizeOpenAICompatiblePlatform(""))
	require.Equal(t, capability.PlatformOpenAI, NormalizeOpenAICompatiblePlatform(capability.PlatformAnthropic))
	require.Equal(t, capability.PlatformOpenAI, NormalizeOpenAICompatiblePlatform("something-else"))
}
