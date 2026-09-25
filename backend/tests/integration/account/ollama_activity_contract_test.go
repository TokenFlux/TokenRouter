package account_test

import (
	"fmt"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestScheduleOllamaCloudUsageActivityOnlyForOllama(t *testing.T) {
	deferred, activity := gatewaytestkit.DeferredActivityRecorder(t)
	ollama := ollamaUsageAccount(1)
	other := ollamaUsageAccount(2)
	other.Record.Credentials["base_url"] = "https://api.openai.com"

	(&accountprovider.TransportHealth{Deferred: deferred}).Attempt(gatewayprovider.ExecutionRecord(ollama))
	(&accountprovider.TransportHealth{Deferred: deferred}).Attempt(gatewayprovider.ExecutionRecord(other))
	(&accountprovider.TransportHealth{}).Attempt(gatewayprovider.ExecutionRecord(ollama))

	require.NoError(t, deferred.Stop())
	_, ok := activity.Load(int64(1))
	require.True(t, ok)
	_, ok = activity.Load(int64(2))
	require.False(t, ok)
}

func ollamaUsageAccount(id int64) *gatewayprovider.ExecutionAccount {
	return &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: id, Name: fmt.Sprintf("ollama-%d", id), Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": fmt.Sprintf("key-%d", id)},
		Extra:       map[string]any{}, Status: billing.StatusActive, Schedulable: true, Concurrency: 1},
	}
}
