//go:build unit

package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/stretchr/testify/require"
)

func TestResolveUsageBillingRequestID_ForcedWebSearchBeatsClientID(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "client-shared-id")
	got := CompletionRequestID(ctx, "web_search:uuid-1")
	require.Equal(t, "web_search:uuid-1", got)
}

func TestResolveUsageBillingRequestID_ClientWinsOverPlainUpstream(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "client-shared-id")
	got := CompletionRequestID(ctx, "resp_abc")
	require.Equal(t, "client:client-shared-id", got)
}

func TestIsForcedUsageBillingRequestID(t *testing.T) {
	t.Parallel()
	require.True(t, completion.ForcedRequestID("web_search:x"))
	require.True(t, completion.ForcedRequestID("grok-video:task-1"))
	require.True(t, completion.ForcedRequestID("grok_audio:up-1"))
	require.True(t, completion.ForcedRequestID("grok_realtime:sess-1"))
	require.False(t, completion.ForcedRequestID("resp_abc"))
}

func TestStableGrokAudioBillingRequestID(t *testing.T) {
	t.Parallel()
	require.Equal(t, "grok_audio:up-1", StableAudioBillingRequestID("up-1"))
	require.Equal(t, "grok_audio:up-1", StableAudioBillingRequestID("grok_audio:up-1"))
	got := StableAudioBillingRequestID("")
	require.True(t, strings.HasPrefix(got, "grok_audio:"))
	require.Greater(t, len(got), len("grok_audio:"))
}

func TestStableGrokRealtimeBillingRequestID(t *testing.T) {
	t.Parallel()
	require.Equal(t, "grok_realtime:s1", StableRealtimeBillingRequestID("s1"))
	require.Equal(t, "grok_realtime:s1", StableRealtimeBillingRequestID("grok_realtime:s1"))
	got := StableRealtimeBillingRequestID("")
	require.True(t, strings.HasPrefix(got, "grok_realtime:"))
}

func TestResolveUsageBillingRequestID_ForcedGrokAudioBeatsClientID(t *testing.T) {
	t.Parallel()
	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "client-shared-id")
	got := CompletionRequestID(ctx, StableAudioBillingRequestID("up-9"))
	require.Equal(t, "grok_audio:up-9", got)
}
