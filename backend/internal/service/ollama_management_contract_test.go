package service

import (
	"fmt"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestScheduleOllamaCloudUsageActivityOnlyForOllama(t *testing.T) {
	deferred, activity := newDeferredActivityRecorder(t)
	ollama := ollamaUsageAccount(1)
	other := ollamaUsageAccount(2)
	other.Credentials["base_url"] = "https://api.openai.com"

	scheduleOllamaCloudUsageActivity(deferred, ollama)
	scheduleOllamaCloudUsageActivity(deferred, other)
	scheduleOllamaCloudUsageActivity(nil, ollama)

	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(1))
	require.True(t, ok)
	_, ok = activity.Load(int64(2))
	require.False(t, ok)
}

func ollamaUsageAccount(id int64) *Account {
	return &Account{
		ID: id, Name: fmt.Sprintf("ollama-%d", id), Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": fmt.Sprintf("key-%d", id)},
		Extra:       map[string]any{}, Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
	}
}
