package gateway_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/stretchr/testify/require"
)

func TestSettingService_GetClaudeOAuthSystemPromptInjectionSettings(t *testing.T) {
	t.Run("defaults to enabled with empty prompt", func(t *testing.T) {
		svc := gateway.NewRuntimeSettings(&promptSettingsFixture{data: map[string]string{}}, nil, nil)

		enabled, prompt, blocks := svc.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())

		require.True(t, enabled)
		require.Empty(t, prompt)
		require.Empty(t, blocks)
	})

	t.Run("uses configured switch prompt and blocks", func(t *testing.T) {
		const customPrompt = "custom prompt\n\nkeep spacing"
		const customBlocks = `[{"type":"text","text":"custom block","cache_control":true}]`
		svc := gateway.NewRuntimeSettings(&promptSettingsFixture{data: map[string]string{
			gateway.SettingKeyEnableClaudeOAuthSystemPromptInjection: "false",
			gateway.SettingKeyClaudeOAuthSystemPrompt:                customPrompt,
			gateway.SettingKeyClaudeOAuthSystemPromptBlocks:          customBlocks,
		}}, nil, nil)

		enabled, prompt, blocks := svc.GetClaudeOAuthSystemPromptInjectionSettings(context.Background())

		require.False(t, enabled)
		require.Equal(t, customPrompt, prompt)
		require.Equal(t, customBlocks, blocks)
	})
}

// promptSettingsFixture 只提供本测试读取的转发设置，不持有缓存实现。
type promptSettingsFixture struct {
	gateway.RuntimeSettingsStore
	data map[string]string
}

func (s *promptSettingsFixture) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.data[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}
