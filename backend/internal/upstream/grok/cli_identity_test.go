package grok

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveCLIVersionDefaultsToPinnedClientVersion clears the environment
// version override. It asserts the bundled client version is selected and
// both the bundled and minimum versions satisfy the supported-version
// predicate.
func TestResolveCLIVersionDefaultsToPinnedClientVersion(t *testing.T) {
	setGrokTestOverride(t, "CLIVersion", "")
	// The bundled version and minimum accepted override are separate constants.
	// These assertions protect that distinction when the default release advances beyond the minimum.
	require.Equal(t, CLIClientVersion, ResolveCLIVersion())
	require.True(t, IsSupportedCLIVersion(CLIClientVersion))
	require.True(t, IsSupportedCLIVersion(CLIStableVersion))
}

// TestResolveCLIVersionAcceptsValidOverride supplies a canonical prerelease
// version above the configured minimum. It asserts ResolveCLIVersion returns
// that override unchanged rather than selecting the bundled client version.
func TestResolveCLIVersionAcceptsValidOverride(t *testing.T) {
	setGrokTestOverride(t, "CLIVersion", "0.2.95-alpha.1")
	require.Equal(t, "0.2.95-alpha.1", ResolveCLIVersion())
}

// TestResolveCLIVersionRejectsUnsafeOrTooOld supplies old, noncanonical,
// incomplete, and control-character version overrides. It asserts each listed
// invalid value selects CLIClientVersion, covering validation before identity
// headers are populated.
func TestResolveCLIVersionRejectsUnsafeOrTooOld(t *testing.T) {
	for _, version := range []string{
		"0.2.92",
		"0.2.93-beta.1",
		"0.2.95\r\nX-Injected: true",
		"0.2.093",
		"0.3",
		"1",
	} {
		t.Run(version, func(t *testing.T) {
			setGrokTestOverride(t, "CLIVersion", version)
			require.Equal(t, CLIClientVersion, ResolveCLIVersion())
		})
	}
}

// TestApplyCLIProxyHeaders starts with the CLI proxy target and an existing
// User-Agent. It asserts the resolved version, client identifier, token-auth
// identity, and workspace User-Agent replace the proxy request's identity
// values.
func TestApplyCLIProxyHeaders(t *testing.T) {
	setGrokTestOverride(t, "CLIVersion", "")

	req, err := http.NewRequest(http.MethodPost, "https://cli-chat-proxy.grok.com/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "legacy-client/1.0")

	ApplyCLIProxyHeaders(req)

	require.Equal(t, CLIClientVersion, req.Header.Get("x-grok-client-version"))
	require.Equal(t, CLIClientIdentifier, req.Header.Get("x-grok-client-identifier"))
	require.Equal(t, CLITokenAuth, req.Header.Get("X-XAI-Token-Auth"))
	require.Equal(t, CLIUserAgent(CLIClientVersion), req.Header.Get("User-Agent"))
}

// TestApplyCLIProxyHeadersLeavesAPIHostUnchanged targets the public API while
// a valid version override is present. It asserts no CLI-specific headers are
// added and the request's existing User-Agent is retained.
func TestApplyCLIProxyHeadersLeavesAPIHostUnchanged(t *testing.T) {
	setGrokTestOverride(t, "CLIVersion", "0.2.95")

	req, err := http.NewRequest(http.MethodPost, "https://api.x.ai/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "direct-api-client/1.0")

	ApplyCLIProxyHeaders(req)

	require.Empty(t, req.Header.Get("x-grok-client-version"))
	require.Empty(t, req.Header.Get("x-grok-client-identifier"))
	require.Empty(t, req.Header.Get("X-XAI-Token-Auth"))
	require.Equal(t, "direct-api-client/1.0", req.Header.Get("User-Agent"))
}
