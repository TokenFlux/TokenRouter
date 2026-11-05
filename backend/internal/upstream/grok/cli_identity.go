package grok

import (
	"net/http"
	"strings"

	"golang.org/x/mod/semver"
)

// cli_identity.go selects Grok CLI versions and applies client identity
// headers to forwarding requests.
// Grok CLI proxy requests advertise a workspace version and client
// identifier. The configuration override is accepted only as canonical SemVer
// meeting the pinned minimum; other values select the bundled version. Proxy-
// specific headers apply by hostname, while runtime helpers can apply client
// identity to other forwarding requests.

// These constants identify Grok Build requests to the CLI chat proxy.
// Versions are bundled with the binary; explicit configuration can select a supported override.
const (
	// CLIProxyHost selects the target for proxy-specific client identity headers.
	CLIProxyHost = "cli-chat-proxy.grok.com"

	// CLIStableVersion is the minimum version accepted by local override validation.
	CLIStableVersion = "0.2.93"

	// CLITokenAuth supplies the proxy's fixed CLI token identity header value.
	CLITokenAuth = "xai-grok-cli"

	// CLIClientIdentifier is the x-grok-client-identifier value for Grok CLI requests.
	CLIClientIdentifier = "grok-shell"
)

// ResolveCLIVersion returns the trimmed configuration override when it passes
// IsSupportedCLIVersion. Blank, invalid, noncanonical, or too-old values
// select CLIClientVersion, keeping the advertised version within the accepted
// local version rule.
func ResolveCLIVersion() string {
	version := strings.TrimSpace(currentOverrides().CLIVersion)
	if !IsSupportedCLIVersion(version) {
		return CLIClientVersion
	}
	return version
}

// IsSupportedCLIVersion reports whether the supplied version is canonical
// SemVer at or above CLIStableVersion. Supply text without the v prefix or
// surrounding whitespace; prerelease ordering follows the semver comparison.
// Build metadata is rejected because semver.Canonical removes it, making the
// canonical result differ from the supplied version.
func IsSupportedCLIVersion(version string) bool {
	canonical := "v" + version
	minimum := "v" + CLIStableVersion
	return semver.IsValid(canonical) &&
		semver.Canonical(canonical) == canonical &&
		semver.Compare(canonical, minimum) >= 0
}

// CLIUserAgent formats the workspace identity using the supplied version.
// Blank trimmed text selects CLIClientVersion, while nonblank input is used
// as supplied. Call ResolveCLIVersion first when input needs version
// validation.
func CLIUserAgent(version string) string {
	if strings.TrimSpace(version) == "" {
		version = CLIClientVersion
	}
	return "xai-grok-workspace/" + version
}

// ApplyCLIProxyHeaders replaces client identity headers for CLIProxyHost. Nil
// requests, missing URLs, and other hostnames are ignored. Matching requests
// receive an allocated Header map when needed and the current resolved
// version regardless of their existing identity headers.
func ApplyCLIProxyHeaders(req *http.Request) {
	if req == nil || req.URL == nil || !strings.EqualFold(strings.TrimSpace(req.URL.Hostname()), CLIProxyHost) {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	version := ResolveCLIVersion()
	req.Header.Set("X-XAI-Token-Auth", CLITokenAuth)
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("x-grok-client-identifier", CLIClientIdentifier)
	req.Header.Set("User-Agent", CLIUserAgent(version))
}

// DefaultGrokUpstreamUserAgent returns the workspace User-Agent built from
// the currently resolved Grok CLI version.
func DefaultGrokUpstreamUserAgent() string {
	return CLIUserAgent(ResolveCLIVersion())
}

// ApplyDefaultGrokUpstreamHeaders sets the workspace User-Agent, resolved
// version, and client identifier on a request. Nil requests are ignored;
// nonnull requests need an allocated Header map and existing identity values
// are replaced.
func ApplyDefaultGrokUpstreamHeaders(req *http.Request) {
	if req == nil {
		return
	}
	// Runtime forwarding selects this client identity independent of the request
	// host; callers invoke the proxy-specific helper separately when token-auth is needed.
	req.Header.Set("User-Agent", DefaultGrokUpstreamUserAgent())
	req.Header.Set("x-grok-client-version", ResolveCLIVersion())
	req.Header.Set("x-grok-client-identifier", CLIClientIdentifier)
}

// ApplyGrokRuntimeHeaders applies default Grok client identity and a trimmed
// runtime Originator when provided. Nil requests are ignored and other
// requests need allocated headers. A blank runtime value leaves any existing
// Originator in place.
func ApplyGrokRuntimeHeaders(req *http.Request, runtimeOriginator string) {
	ApplyDefaultGrokUpstreamHeaders(req)
	if req == nil {
		return
	}
	// A blank runtime setting retains an existing Originator rather than clearing
	// it, allowing the caller's earlier header selection to remain effective.
	if originator := strings.TrimSpace(runtimeOriginator); originator != "" {
		req.Header.Set("Originator", originator)
	}
	req.Header.Set("User-Agent", DefaultGrokUpstreamUserAgent())
}
