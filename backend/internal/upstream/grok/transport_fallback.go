package grok

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/mod/semver"
)

// transport_fallback.go retries eligible Grok CLI access denials against the
// public API while retaining failed response evidence.
// Grok CLI requests carrying bearer authentication and CLI token identity can
// receive a compatibility denial that the transport considers for public-API
// replay. Eligibility checks the hostname, replayable body and HTTP 403 status
// before a bounded body probe looks for a recognized denial message. Credential
// selection belongs to the caller; candidate detection uses the request headers
// and response evidence.
//
// Reading that denial must leave ordinary provider policy a usable response when
// recovery fails. Complete bounded reads close the source and install a buffered
// reader; partial reads or excess bytes replay their prefix before continuing
// the original reader. The second request clones URL and headers, targets the
// official API and removes selected CLI identity fields. A 2xx recovery closes
// the denial response and returns the recovered response; unsuccessful recovery
// retains the denial for its caller.

// These constants select CLI identity, the public recovery host and the bounded
// error-body size used during denial inspection.
const (
	grokCLIProxyHost      = CLIProxyHost
	grokOfficialAPIHost   = "api.x.ai"
	grokCLIStableVersion  = CLIClientVersion
	grokFallbackBodyLimit = 64 << 10
)

// AccessDeniedFallbackTransport recovers recognized replayable CLI denials
// through the official API.
type AccessDeniedFallbackTransport struct {
	// Base performs both original and recovery exchanges; callers configure it
	// before use and retain its concurrency and transport-resource policy.
	Base http.RoundTripper // shared underlying transport
}

// prefixedReadCloser replays captured bytes while retaining the original body's
// close operation.
type prefixedReadCloser struct {
	// Reader and Closer retain the same response source: Reader prepends consumed
	// bytes before that source, and Closer releases the source when caller cleanup
	// runs.
	io.Reader // prefix and source reader
	io.Closer // original response closer
}

// ClientWithAccessDeniedFallback shallow-copies client and wraps the copy's
// transport in CLI denial recovery. Nil clients return nil and a nil Transport
// uses http.DefaultTransport. The copied client keeps the same Jar, callback
// captures and base transport references, so their synchronization and cleanup
// remain with their existing owners.
func ClientWithAccessDeniedFallback(client *http.Client) *http.Client {
	if client == nil {
		return nil
	}
	clone := *client
	base := clone.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	clone.Transport = &AccessDeniedFallbackTransport{Base: base}
	return &clone
}

// RoundTrip sends req and considers one official-API replay for a recognized CLI
// denial. The original request needs GetBody, CLI host and token identity,
// Bearer authentication and HTTP 403 before its response body is examined. Base
// must be configured as a usable RoundTripper.
//
// Bounded inspection keeps error bytes readable for caller policy. Request
// rebuilding or replay failure returns that denial; a non-2xx replay response is
// closed. Successful replay closes the denial and transfers the new response
// body to the caller. The original request URL and headers stay available
// through cloning.
func (t *AccessDeniedFallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.Base.RoundTrip(req)
	if err != nil || !IsCLIAccessDeniedFallbackCandidate(req, resp) {
		return resp, err
	}

	body, ok := bufferSmallResponseBody(resp, grokFallbackBodyLimit)
	if !ok || !IsCLICompatibilityAccessDenied(body) {
		// The probe failed its read cap or the captured body lacks a compatibility
		// marker. Keep the restored denial for ordinary provider policy.
		return resp, nil
	}

	fallbackReq, err := newGrokOfficialAPIFallbackRequest(req)
	if err != nil {
		// Body recreation failed before another exchange began. The captured denial
		// remains readable so caller policy can still handle the initial CLI refusal.
		return resp, nil
	}
	fallbackResp, fallbackErr := t.Base.RoundTrip(fallbackReq)
	if fallbackErr != nil {
		slog.Debug("grok_cli_access_denied_api_fallback_failed", "path", req.URL.EscapedPath(), "error", fallbackErr)
		// Replay transport failure leaves the first denial as the response. Its
		// captured bytes and original headers remain available for the caller's
		// provider decision.
		return resp, nil
	}
	if fallbackResp.StatusCode < http.StatusOK || fallbackResp.StatusCode >= http.StatusMultipleChoices {
		if fallbackResp.Body != nil {
			// The unsuccessful replay response is discarded in favor of the original
			// denial, so release its transport body before returning.
			_ = fallbackResp.Body.Close()
		}
		// A non-success replay has been discarded after body cleanup. Return the
		// initial CLI denial as the selected response for caller presentation and
		// provider policy.
		return resp, nil
	}

	if resp.Body != nil {
		// Recovery succeeded with a new response; release the retained denial body
		// before transferring the recovered response to the caller.
		_ = resp.Body.Close()
	}
	slog.Warn("grok_cli_access_denied_api_fallback_succeeded", "method", req.Method, "path", req.URL.EscapedPath())
	return fallbackResp, nil
}

// IsCLICompatibilityAccessDenied recognizes case-insensitive access denied text
// or the selected structured chat-permission prefix. Structured matching
// requires permission_denied in code and a string error; unrelated authorization
// failures return false for ordinary provider handling.
func IsCLICompatibilityAccessDenied(body []byte) bool {
	lower := bytes.ToLower(body)
	if bytes.Contains(lower, []byte("access denied")) {
		return true
	}
	var payload struct {
		// Code and Error identify the structured permission denial together. Code
		// selects permission_denied, and Error must start with the configured
		// chat-access denial message.
		Code  string `json:"code"`  // permission error code
		Error string `json:"error"` // chat permission message
	}
	if err := json.Unmarshal(body, &payload); err != nil || !strings.EqualFold(strings.TrimSpace(payload.Code), "permission_denied") {
		return false
	}
	const chatEndpointDeniedPrefix = "access to the chat endpoint is denied. please ensure you're using the correct credentials. if you believe this is a mistake, please"
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(payload.Error)), chatEndpointDeniedPrefix)
}

// IsCLIAccessDeniedFallbackCandidate checks replayability, host, identity and
// status before the response body is inspected. A nil request, URL or response
// fails eligibility; GetBody must be available even when the request has no
// body.
func IsCLIAccessDeniedFallbackCandidate(req *http.Request, resp *http.Response) bool {
	return req != nil && req.URL != nil && req.GetBody != nil && resp != nil &&
		resp.StatusCode == http.StatusForbidden &&
		strings.EqualFold(strings.TrimSpace(req.URL.Hostname()), grokCLIProxyHost) &&
		strings.EqualFold(strings.TrimSpace(req.Header.Get("X-XAI-Token-Auth")), "xai-grok-cli") &&
		strings.HasPrefix(strings.ToLower(strings.TrimSpace(req.Header.Get("Authorization"))), "bearer ")
}

// newGrokOfficialAPIFallbackRequest clones req for HTTPS api.x.ai and obtains a
// fresh body from GetBody. Callers establish replayability first. It clears
// request host overrides and selected CLI headers, retaining method, path, query
// and request context for the exchange.
func newGrokOfficialAPIFallbackRequest(req *http.Request) (*http.Request, error) {
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}
	fallbackReq := req.Clone(req.Context())
	fallbackReq.Body = body
	fallbackReq.URL = cloneURL(req.URL)
	fallbackReq.URL.Scheme = "https"
	fallbackReq.URL.Host = grokOfficialAPIHost
	fallbackReq.Host = ""
	fallbackReq.RequestURI = ""
	fallbackReq.Header = req.Header.Clone()
	// The official API replay uses bearer authentication; remove the selected CLI
	// identity fields while retaining the request's remaining headers.
	for _, header := range []string{
		"X-XAI-Token-Auth",
		"X-Grok-Client-Version",
		"X-Grok-Client-Surface",
		"X-UserID",
		"X-Email",
		"User-Agent",
	} {
		fallbackReq.Header.Del(header)
	}
	return fallbackReq, nil
}

// cloneURL shallow-copies a URL so transport target fields can be rewritten
// independently. Nil returns nil. Nested URL values retain their existing
// references, while this caller changes only scheme and host on the copy.
func cloneURL(value *url.URL) *url.URL {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// bufferSmallResponseBody captures at most limit plus one bytes and reports a
// complete bounded read. Nil responses, missing bodies and nonpositive limits
// return false. Complete reads close the source and install a buffered response
// body with updated ContentLength.
//
// A read error or oversized prefix keeps the original reader open and prepends
// every consumed byte through prefixedReadCloser. Later callers can therefore
// still read the denial after rejected inspection, and closing that replacement
// releases the source. The boolean describes successful inspection, not whether
// the body matches a compatibility error.
func bufferSmallResponseBody(resp *http.Response, limit int64) ([]byte, bool) {
	if resp == nil || resp.Body == nil || limit <= 0 {
		return nil, false
	}
	original := resp.Body
	// Read one extra byte to detect overflow before replacing the source with a
	// complete buffered denial response.
	body, err := io.ReadAll(io.LimitReader(original, limit+1))
	if err != nil || int64(len(body)) > limit {
		// Replay every byte already consumed before resuming the source, preserving
		// the denial for callers after an incomplete or oversized probe.
		resp.Body = &prefixedReadCloser{
			Reader: io.MultiReader(bytes.NewReader(body), original),
			Closer: original,
		}
		return nil, false
	}
	// A complete bounded read has all response bytes in memory; closing the source
	// releases transport occupancy before buffered replay.
	_ = original.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return body, true
}

// ApplyTransportCLIHeaders applies Grok Build identity when the request hostname
// matches the CLI proxy. It initializes a missing header map and uses the stable
// version when the configuration override fails canonical SemVer or
// minimum-version checks. Other targets keep their current headers.
func ApplyTransportCLIHeaders(req *http.Request) {
	if req == nil || req.URL == nil || !strings.EqualFold(strings.TrimSpace(req.URL.Hostname()), grokCLIProxyHost) {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	version := strings.TrimSpace(currentOverrides().CLIVersion)
	// Subscription requests need a supported CLI version, so invalid environment
	// overrides select the configured stable identity before transport dispatch.
	if !IsTransportCLIVersionSupported(version) {
		version = grokCLIStableVersion
	}
	req.Header.Set("X-XAI-Token-Auth", CLITokenAuth)
	req.Header.Set("x-grok-client-version", version)
	req.Header.Set("x-grok-client-identifier", CLIClientIdentifier)
	req.Header.Set("User-Agent", CLIUserAgent(version))
}

// IsTransportCLIVersionSupported accepts canonical unprefixed SemVer at or above
// CLIClientVersion. The function adds v for the semver package;
// already-prefixed, malformed or noncanonical versions return false and select
// the caller's stable identity.
func IsTransportCLIVersionSupported(version string) bool {
	canonical := "v" + version
	minimum := "v" + CLIClientVersion
	return semver.IsValid(canonical) &&
		semver.Canonical(canonical) == canonical &&
		semver.Compare(canonical, minimum) >= 0
}
