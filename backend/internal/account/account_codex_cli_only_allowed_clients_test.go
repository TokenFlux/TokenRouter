package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAccount_GetCodexCLIOnlyAllowedClients(t *testing.T) {
	t.Run("OAuth 账号读取 []any 字符串列表", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code"}},
		}
		require.Equal(t, []string{"claude_code"}, account.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("OAuth 账号读取 []string 列表", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []string{"claude_code"}},
		}
		require.Equal(t, []string{"claude_code"}, account.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("[]string 跳过空白元素", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []string{"claude_code", "", "  "}},
		}
		require.Equal(t, []string{"claude_code"}, account.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("跳过非字符串与空白元素", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code", 123, "", "  "}},
		}
		require.Equal(t, []string{"claude_code"}, account.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("非 OAuth 账号返回空", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Extra:    map[string]any{"codex_cli_only_allowed_clients": []any{"claude_code"}},
		}
		require.Empty(t, account.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("Extra 为空返回空", func(t *testing.T) {
		account := &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
		require.Empty(t, account.GetCodexCLIOnlyAllowedClients())
	})

	t.Run("字段缺失返回空", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeOAuth,
			Extra:    map[string]any{},
		}
		require.Empty(t, account.GetCodexCLIOnlyAllowedClients())
	})
}
