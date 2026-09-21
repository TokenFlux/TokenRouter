//go:build unit

package account_test

import (
	"testing"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestIsOpenAIOAuthServableModel(t *testing.T) {
	require.True(t, account.IsOpenAIOAuthServableModel("gpt-5.4-high"))
	require.True(t, account.IsOpenAIOAuthServableModel("  gpt-5.3-codex  "))
	require.True(t, account.IsOpenAIOAuthServableModel("claude-3-5-haiku-20241022"))
	require.True(t, account.IsOpenAIOAuthServableModel("DeepThink-x"))  // 非黑名单前缀，保持允许。
	require.False(t, account.IsOpenAIOAuthServableModel("DeepSeek-V4")) // 大小写不敏感。
	require.False(t, account.IsOpenAIOAuthServableModel("qwen3-235b-thinking"))
	require.False(t, account.IsOpenAIOAuthServableModel("k3"))
	require.False(t, account.IsOpenAIOAuthServableModel("k3-256k"))
	require.False(t, account.IsOpenAIOAuthServableModel("provider/k3"))
	require.True(t, account.IsOpenAIOAuthServableModel("my-k3-alias"))   // 自定义别名继续 fail-open。
	require.True(t, account.IsOpenAIOAuthServableModel("deepseekcoder")) // 无连字符时不匹配黑名单前缀。
}
