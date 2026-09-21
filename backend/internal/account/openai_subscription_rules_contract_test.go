package account_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/require"
)

func TestShouldApplyChatGPTAccountInfoPlanType(t *testing.T) {
	require.False(t, account.ShouldApplyChatGPTAccountInfoPlanType("pro", "self_serve_business_usage_based"))
	require.False(t, account.ShouldApplyChatGPTAccountInfoPlanType("free", "team"))
	require.False(t, account.ShouldApplyChatGPTAccountInfoPlanType("", ""))
	require.True(t, account.ShouldApplyChatGPTAccountInfoPlanType("", "pro"))
}

func TestChatGPTAccountInfoBelongsToTokenAccount(t *testing.T) {
	require.False(t, account.ChatGPTAccountInfoBelongsToTokenAccount(
		&account.OpenAITokenInfo{ChatGPTAccountID: "personal-a"},
		&openai.ChatGPTAccountInfo{AccountID: "workspace-b"},
	))
	require.True(t, account.ChatGPTAccountInfoBelongsToTokenAccount(
		&account.OpenAITokenInfo{ChatGPTAccountID: "personal-a"},
		&openai.ChatGPTAccountInfo{AccountID: "PERSONAL-A"},
	))
	// 任一侧缺少 ID 时无法区分，保持既有行为。
	require.True(t, account.ChatGPTAccountInfoBelongsToTokenAccount(
		&account.OpenAITokenInfo{},
		&openai.ChatGPTAccountInfo{AccountID: "workspace-b"},
	))
	require.True(t, account.ChatGPTAccountInfoBelongsToTokenAccount(
		&account.OpenAITokenInfo{ChatGPTAccountID: "personal-a"},
		&openai.ChatGPTAccountInfo{},
	))
}
