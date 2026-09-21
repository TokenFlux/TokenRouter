package gateway_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

type allowClaudeCodeSettingRepoStub struct {
	values map[string]string
	sets   map[string]string
}

func (s *allowClaudeCodeSettingRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unused")
}
func (s *allowClaudeCodeSettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settingscore.ErrSettingNotFound
}
func (s *allowClaudeCodeSettingRepoStub) Set(ctx context.Context, key, value string) error {
	if s.values == nil {
		s.values = map[string]string{}
	}
	if s.sets == nil {
		s.sets = map[string]string{}
	}
	s.values[key] = value
	s.sets[key] = value
	return nil
}
func (s *allowClaudeCodeSettingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unused")
}
func (s *allowClaudeCodeSettingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unused")
}
func (s *allowClaudeCodeSettingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unused")
}
func (s *allowClaudeCodeSettingRepoStub) Delete(ctx context.Context, key string) error {
	panic("unused")
}

func TestSettingService_IsOpenAIAllowClaudeCodeCodexPluginEnabled(t *testing.T) {
	t.Run("默认关闭（设置缺失）", func(t *testing.T) {
		svc := gateway.NewRuntimeSettings(&allowClaudeCodeSettingRepoStub{values: map[string]string{}}, settingscore.ErrSettingNotFound, nil)
		require.False(t, svc.IsOpenAIAllowClaudeCodeCodexPluginEnabled(context.Background()))
	})
	t.Run("值为 true 时开启", func(t *testing.T) {
		svc := gateway.NewRuntimeSettings(&allowClaudeCodeSettingRepoStub{values: map[string]string{
			gateway.SettingKeyOpenAIAllowClaudeCodeCodexPlugin: "true",
		}}, settingscore.ErrSettingNotFound, nil)
		require.True(t, svc.IsOpenAIAllowClaudeCodeCodexPluginEnabled(context.Background()))
	})
	t.Run("值非 true 时关闭", func(t *testing.T) {
		svc := gateway.NewRuntimeSettings(&allowClaudeCodeSettingRepoStub{values: map[string]string{
			gateway.SettingKeyOpenAIAllowClaudeCodeCodexPlugin: "false",
		}}, settingscore.ErrSettingNotFound, nil)
		require.False(t, svc.IsOpenAIAllowClaudeCodeCodexPluginEnabled(context.Background()))
	})
}

// 历史默认值应升级，管理员已明确设置的模型和缺失设置均保持不变。
func TestSettingService_MigrateGrokDefaultTextModel(t *testing.T) {
	t.Run("升级历史默认值", func(t *testing.T) {
		repo := &allowClaudeCodeSettingRepoStub{values: map[string]string{gateway.SettingKeyGrokDefaultTextModel: "grok-4.5"}}
		svc := gateway.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound, nil)
		require.NoError(t, svc.MigrateGrokDefaultTextModel(context.Background()))
		require.Equal(t, "grok-4.6", repo.sets[gateway.SettingKeyGrokDefaultTextModel])
	})
	t.Run("保留显式模型", func(t *testing.T) {
		repo := &allowClaudeCodeSettingRepoStub{values: map[string]string{gateway.SettingKeyGrokDefaultTextModel: "grok-4.3"}}
		svc := gateway.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound, nil)
		require.NoError(t, svc.MigrateGrokDefaultTextModel(context.Background()))
		require.Empty(t, repo.sets)
	})
	t.Run("缺失设置不写入", func(t *testing.T) {
		repo := &allowClaudeCodeSettingRepoStub{values: map[string]string{}}
		svc := gateway.NewRuntimeSettings(repo, settingscore.ErrSettingNotFound, nil)
		require.NoError(t, svc.MigrateGrokDefaultTextModel(context.Background()))
		require.Empty(t, repo.sets)
	})
}
