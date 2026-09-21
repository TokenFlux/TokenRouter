//go:build unit

package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestRequiresBillableGrokChatUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		account *Account
		models  []string
		want    bool
	}{
		{name: "grok platform", account: &Account{Platform: capability.PlatformGrok}, models: []string{"alias"}, want: true},
		{name: "compatible Grok model", account: &Account{Platform: capability.PlatformOpenAI}, models: []string{"grok-4.5"}, want: true},
		{name: "mapped Grok model", account: &Account{Platform: capability.PlatformOpenAI}, models: []string{"alias", "grok-4.5"}, want: true},
		{name: "namespaced Grok model", account: &Account{Platform: capability.PlatformOpenAI}, models: []string{"x-ai/grok-4.5"}, want: true},
		{name: "ordinary OpenAI model", account: &Account{Platform: capability.PlatformOpenAI}, models: []string{"gpt-5.4"}, want: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, testCase.want, requiresBillableGrokChatUsage(testCase.account, testCase.models...))
		})
	}
}

func TestHasBillableGrokChatUsageRequiresAggregateToken(t *testing.T) {
	t.Parallel()

	require.False(t, hasBillableGrokChatUsage(openai.ForwardUsage{}))
	require.False(t, hasBillableGrokChatUsage(openai.ForwardUsage{ImageInputTokens: 2, ImageOutputTokens: 1}))
	require.True(t, hasBillableGrokChatUsage(openai.ForwardUsage{InputTokens: 1}))
	require.True(t, hasBillableGrokChatUsage(openai.ForwardUsage{OutputTokens: 1}))
	require.True(t, hasBillableGrokChatUsage(openai.ForwardUsage{CacheCreationInputTokens: 1}))
	require.True(t, hasBillableGrokChatUsage(openai.ForwardUsage{CacheReadInputTokens: 1}))
}
