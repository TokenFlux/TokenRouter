//go:build unit

package service

import (
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestResolveGrokStreamIdleTimeout(t *testing.T) {
	require.Equal(t, 90*time.Second, grok.ResolveStreamIdleTimeout(90))
	require.Equal(t, defaultGrokStreamIdleTimeout, grok.ResolveStreamIdleTimeout(0))
	require.Equal(t, defaultGrokStreamIdleTimeout, grok.ResolveStreamIdleTimeout(-1))
}

func TestGrokStreamIdleFailoverError(t *testing.T) {
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}}
	err := gatewayprovider.GrokStreamIdleFailure(account, 180*time.Second)
	require.NotNil(t, err)
	require.Equal(t, 502, err.StatusCode)
	require.True(t, err.SafeToFailoverAfterWrite)
	require.True(t, err.RetryableOnSameAccount)
	require.True(t, err.RequestScopedTransient)
	require.Equal(t, 1, err.SameAccountRetryMax)
	require.Contains(t, string(err.ResponseBody), "empty_upstream")
	require.WithinDuration(t, time.Now().Add(180*time.Second), err.SameAccountRetryDeadline, 2*time.Second)
}

func TestGrokStreamIdleFailoverErrorRequiresGrokAccount(t *testing.T) {
	openAI := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	err := gatewayprovider.GrokStreamIdleFailure(openAI, time.Second)
	require.False(t, err.RetryableOnSameAccount)
	require.True(t, err.RequestScopedTransient)
}
