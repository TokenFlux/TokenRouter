package provider

import (
	"net/http"
	"strings"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestUpstreamRequestIDFromHeaders_UnconfiguredAccountRecordsNothing(t *testing.T) {
	h := http.Header{}
	h.Set("X-Client-Request-ID", "sub2api-client")
	h.Set("X-Request-ID", "sub2api-local")
	h.Set("X-Oneapi-Request-Id", "oneapi-1")
	h.Set("Request-Id", "req_official")
	h.Set("xai-request-id", "xai-1")
	h.Set("x-goog-request-id", "goog-1")

	require.Equal(t, "", UpstreamRequestIDFromHeaders(nil, h))
	for _, platform := range []string{capability.PlatformAnthropic, capability.PlatformOpenAI, capability.PlatformGemini, capability.PlatformAntigravity, capability.PlatformGrok} {
		require.Equal(t, "", UpstreamRequestIDFromHeaders(&accountcore.Record{Platform: platform}, h), platform)
	}
	blank := &accountcore.Record{Platform: capability.PlatformOpenAI, Extra: map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: "   "}}
	require.Equal(t, "", UpstreamRequestIDFromHeaders(blank, h))
}

func TestUpstreamRequestIDFromHeaders_ReadsOnlyConfiguredHeader(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformOpenAI,
		Extra:    map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: " x-oneapi-request-id "},
	}
	h := http.Header{}
	h.Set("X-Request-ID", "passthrough-from-real-upstream")
	require.Equal(t, "", UpstreamRequestIDFromHeaders(account, h))

	h.Set("X-Oneapi-Request-Id", " oneapi-2 ")
	require.Equal(t, "oneapi-2", UpstreamRequestIDFromHeaders(account, h))
	require.Equal(t, "", UpstreamRequestIDFromHeaders(account, nil))

	official := &accountcore.Record{Platform: capability.PlatformAnthropic, Extra: map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: "request-id"}}
	only := http.Header{}
	only.Set("Request-Id", "req_official")
	require.Equal(t, "req_official", UpstreamRequestIDFromHeaders(official, only))
}

func TestUsageUpstreamRequestIDPtr(t *testing.T) {
	account := &accountcore.Record{Extra: map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: "X-Request-ID"}}
	h := http.Header{}
	h.Set("X-Request-ID", strings.Repeat("a", 200))
	require.Nil(t, usageUpstreamRequestIDPtr(account, h, true))
	require.Nil(t, usageUpstreamRequestIDPtr(account, http.Header{}, false))
	require.Nil(t, usageUpstreamRequestIDPtr(nil, h, false))
	require.Nil(t, usageUpstreamRequestIDPtr(&accountcore.Record{}, h, false))

	got := usageUpstreamRequestIDPtr(account, h, false)
	require.NotNil(t, got)
	require.Len(t, *got, maxUsageUpstreamRequestIDLen)
}

func TestValidateUpstreamRequestIDHeaderExtra(t *testing.T) {
	require.NoError(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(nil))
	require.NoError(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(map[string]any{}))

	blank := map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: "   "}
	require.NoError(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(blank))
	_, present := blank[accountcore.AccountExtraUpstreamRequestIDHeader]
	require.False(t, present, "blank header name must be removed")

	valid := map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: " X-Oneapi-Request-Id "}
	require.NoError(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(valid))
	require.Equal(t, "X-Oneapi-Request-Id", valid[accountcore.AccountExtraUpstreamRequestIDHeader])

	require.Error(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: 1}))
	require.Error(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: "X Request Id"}))
	require.Error(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: "X-Request-Id:"}))
	require.Error(t, accountcore.ValidateUpstreamRequestIDHeaderExtra(map[string]any{accountcore.AccountExtraUpstreamRequestIDHeader: strings.Repeat("x", 65)}))
}
