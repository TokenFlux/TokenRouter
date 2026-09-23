//go:build unit

package provider

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAccountIsSchedulableForModel_AntigravityRateLimits(t *testing.T) {
	now := time.Now()
	future := now.Add(10 * time.Minute)

	account := &accountcore.Record{
		ID:          1,
		Name:        "acc",
		Platform:    capability.PlatformAntigravity,
		Status:      billing.StatusActive,
		Schedulable: true,
	}

	account.RateLimitResetAt = &future
	require.False(t, (ModelPolicy{Record: account}).Schedulable(context.Background(), "claude-sonnet-4-5"))
	require.False(t, (ModelPolicy{Record: account}).Schedulable(context.Background(), "gemini-3-flash"))

	account.RateLimitResetAt = nil
	require.True(t, (ModelPolicy{Record: account}).Schedulable(context.Background(), "claude-sonnet-4-5"))
	require.True(t, (ModelPolicy{Record: account}).Schedulable(context.Background(), "gemini-3-flash"))
}
