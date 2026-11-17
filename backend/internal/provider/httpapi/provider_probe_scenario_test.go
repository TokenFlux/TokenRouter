package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Provider probes select their upstream protocol, credential source and client
// identity before emitting test events. Scenarios cover test_events.go and the
// TestService invoked by test_handler.go, using provider/provider OpenAI, Kimi,
// Zhipu, and DeepSeek, Grok and Qoder executors with controlled streams and
// health-write observations. Explicit test kinds and protocols override
// compatibility selection without replacing stored provider configuration.
//
// Fixture records, requests and profile pointers have different copy rules:
// store reads clone supported provider containers, some Kimi, Zhipu, and
// DeepSeek reads are shallow, captured request bytes are copied and configured
// response bodies are consumed once. Automatic TLS-route fixtures warm local
// caches from stable records and register Stop callbacks; their nil cache
// backends create no subscription for those callbacks to stop. Rate-limit and
// task-recovery assertions inspect logical fixture dispatch and mutated
// observations. Actual persistence and concurrent-store guarantees remain with
// the corresponding use cases and store tests.

// compactionTestV2SSESuccessBody supplies native compaction output plus its
// terminal event, allowing manual-probe scenarios to exercise complete stream validation.
const compactionTestV2SSESuccessBody = "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"compaction\",\"id\":\"cmp_probe\",\"encrypted_content\":\"blob\"}}\n\n" +
	"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"output\":[]}}\n\n"

// textAPIKeyTestRepo supplies one nonnil provider through shallow serial record
// reads.
type textAPIKeyTestRepo struct {
	// provider is borrowed and required by GetByID's shallow copy. Configure this
	// record before serial calls and keep its nested values stable while readers
	// use them.
	provider *providercore.Record // selected provider fixture record
}

// textAPIKeyTestHTTP captures one admitted protocol request and returns a
// controlled stopping error.
type textAPIKeyTestHTTP struct {
	// request is a metadata clone of the captured HTTP request. Request.Clone
	// copies headers and URL, while its Body remains the consumed original reader.
	request *http.Request // captured HTTP metadata clone
	body    string        // captured request body text
}

// grokProviderTestRateLimitRepo records reset observations beyond its embedded
// no-op Grok store.
type grokProviderTestRateLimitRepo struct {
	*grokTestStoreFixture           // borrowed no-op Grok store operations
	rateLimitedCalls      int       // serial logical limit-write count
	resetAt               time.Time // selected fixture recovery timestamp
}

// grokTestStoreFixture supplies configured provider clones and accepts unused
// health writes.
type grokTestStoreFixture struct {
	// providersByID borrows configured records for CloneRecord reads. Initialize
	// the map before requests and keep its records stable while the serial fixture
	// copies them.
	providersByID map[int64]*providercore.Record // configured provider fixture pointers
}

// grokTestTransportFixture captures a borrowed request and body before
// returning configured response state.
type grokTestTransportFixture struct {
	// resp is returned after the request body has been captured successfully.
	// Body-read errors return before it; the caller consumes and closes its
	// configured response body.
	resp *http.Response // borrowed fixed consumable response
	// lastReq is assigned before its body is read. lastBody changes only after
	// a successful read, so read failures can leave the latest borrowed request
	// beside bytes from the previous serial call; successful captures consume
	// the request reader.
	lastReq  *http.Request // last borrowed HTTP request
	lastBody []byte        // last captured request body bytes
}

// grokTestTargetLoader holds a borrowed target selected by the controlled Grok
// scenario.
type grokTestTargetLoader struct {
	// target is retained and returned for every load. The test initializes its
	// executor and record before invoking shared service dispatch through this
	// loader.
	target providercore.TestTarget // borrowed configured test target
}

// grokTestFailureStoreFixture records serial rate-limit and temporary-block
// dispatch from Grok failures.
type grokTestFailureStoreFixture struct {
	// tempUnschedCalls: serial logical temporary-block count; rateLimitedCalls:
	// serial logical limit-write count
	tempUnschedCalls, rateLimitedCalls int
	lastRateLimitedID                  int64     // last limit-write provider identifier
	lastRateLimitResetAt               time.Time // last limit-write recovery timestamp
}

// probeAgentStore holds serial Agent Identity credentials and records logical
// permanent-error writes.
type probeAgentStore struct {
	provideradapter.OpenAIProviderTestStore // unused embedded provider-test store operations
	// provider holds borrowed serial state. GetByID clones it, while
	// UpdateCredentials replaces this record's credential map after accepted task
	// registration; callers inspect that same stored fixture object afterward.
	provider      *providercore.Record // selected provider fixture record
	setErrorCalls int                  // serial logical permanent-error count
}

// probeAgentInvalidations records serial provider identifiers after accepted
// task credential maintenance.
type probeAgentInvalidations struct {
	providerIDs []int64 // captured fixture invalidation provider identifiers
}

// queuedHTTPUpstream consumes ordered response pointers while retaining serial
// request and TLS observations.
type queuedHTTPUpstream struct {
	// responses is consumed from the front by the serial transport. Its entries
	// retain response pointers and bodies, so each queued request needs a separate
	// readable fixture body.
	responses []*http.Response // ordered consumable response pointers
	// requests and tlsFlags are appended together before response selection,
	// including exhausted-queue failures. The request pointers remain borrowed;
	// tests inspect these aligned serial observations after execution.
	requests []*http.Request // captured borrowed HTTP request pointers
	tlsFlags []bool          // captured TLS-profile presence flags
}

// openAIProbeStore records serial provider health and configuration dispatch
// for OpenAI scenarios. Extra observation can block on its optional channel;
// record reads use its embedded configured clone fixture.
type openAIProbeStore struct {
	openAIProbeRecords // configured provider clone lookup
	// updateExtraCalls is optional and publication is blocking when configured.
	// Tests supply channel capacity or consume each observation; its copied
	// top-level map can still share nested values.
	updateExtraCalls chan map[string]any // optional blocking Extra observation channel
	// updatedExtra and bulkUpdatedPayload retain supplied maps after their serial
	// fixture calls. Keep those maps stable through assertions; bulkUpdatedIDs
	// receives its own copy of the submitted ID slice.
	updatedExtra       map[string]any                  // borrowed latest Extra update map
	bulkUpdatedIDs     []int64                         // copied bulk-update provider identifiers
	bulkUpdatedPayload providercore.ProviderBulkUpdate // borrowed captured bulk-update payload
	rateLimitedID      int64                           // captured limit-write provider identifier
	rateLimitedAt      *time.Time                      // copied limit reset timestamp
	clearedErrorID     int64                           // captured error-clear provider identifier
	setErrorID         int64                           // captured error-write provider identifier
	setErrorMsg        string                          // captured provider error message
}

// openAIProbeOutput pairs a borrowed HTTP request and shared recorder for
// synchronous event execution.
type openAIProbeOutput struct {
	// Request and recorder are shared with execution helpers and event assertions.
	// The test finishes synchronous output before inspecting the recorder and keeps
	// request metadata stable while helpers read it.
	Request  *http.Request              // borrowed fixture HTTP request
	recorder *httptest.ResponseRecorder // shared fixture response recorder
}

// openAIProbeRecords indexes stable provider fixture pointers for cloned
// store-backed test dispatch.
type openAIProbeRecords struct {
	// providersByID retains configured record pointers for CloneRecord reads.
	// Tests finish population setup before request dispatch and keep these serial
	// inputs stable while the fixture copies them.
	providersByID map[int64]*providercore.Record // configured provider fixture pointers
}

// openAIProbeTransport captures serial request bytes and TLS selections with
// ordered or fixed response pointers. Captured body slices are copied,
// request/profile pointers remain borrowed and configured response bodies are
// consumed by callers.
type openAIProbeTransport struct {
	// requests records every admitted request by pointer; bodies receives a
	// separate copied byte slice only after a nonnil body is read successfully.
	// Bodyless or failed reads can therefore leave these capture lengths different.
	requests []*http.Request // captured borrowed HTTP request pointers
	bodies   [][]byte        // copied captured request body slices
	// responses is consumed from the front by the serial transport. Its entries
	// retain response pointers and bodies, so each queued request needs a separate
	// readable fixture body.
	responses []*http.Response // ordered consumable response pointers
	// resp is the fixed response used when err is nil and responses has no
	// remaining entries. Its body remains consumable, so configure fresh response
	// state before reuse.
	resp *http.Response // borrowed fixed consumable response
	// err is returned after request and readable-body capture, before response
	// selection. Captured observations remain available for assertions even when
	// this configured transport failure wins.
	err error // configured fixture callback failure
	// lastReq borrows the latest request and can be replaced even on failed body
	// capture. Its successfully read body is replaced with a reader over lastBody,
	// so keep those captured bytes stable through later request-body assertions.
	lastReq  *http.Request // last borrowed HTTP request
	lastBody []byte        // last captured request body bytes
	// lastTLSProfile retains the profile pointer supplied to DoWithTLS. Keep that
	// object stable during assertions; cache-backed policy may have constructed it
	// from copied profile values before transport capture.
	lastTLSProfile *tlsfingerprint.Profile // borrowed latest selected TLS profile
}

// automaticProbeProfileStore supplies stable borrowed profiles to the local
// cache-owner fixture.
type automaticProbeProfileStore struct {
	egress.TLSFingerprintProfileRepository // unused embedded profile storage operations
	// values is returned directly by List during cache initialization. Configure
	// the slice and its pointed-to profiles before Start, then keep them stable
	// while the fixture service copies their values.
	values []*egress.TLSFingerprintProfile // borrowed stable cache initialization values
}

// automaticProbeRouterStore supplies stable borrowed router records to the
// local cache-owner fixture.
type automaticProbeRouterStore struct {
	egress.TLSFingerprintRouterRepository // unused embedded router storage operations
	// values is returned directly by List during route-cache initialization.
	// Configure the slice and its routers before Start and keep those borrowed
	// records stable while the service loads them.
	values []*egress.TLSFingerprintRouter // borrowed stable cache initialization values
}

// qoderProviderTestSessionProviderStub returns borrowed session state and
// captures serial provider invalidation IDs.
type qoderProviderTestSessionProviderStub struct {
	// session is returned by pointer when err is nil. Probe preparation can mutate
	// its Identity with user-info results, and serial assertions inspect that same
	// object after execution.
	session *qoder.SessionContext // borrowed mutable Qoder session
	// err takes precedence over session when GetSession is invoked. A configured
	// failure returns nil session; otherwise callers borrow the configured session
	// pointer for probe preparation.
	err         error   // configured fixture callback failure
	invalidated []int64 // captured session invalidation provider identifiers
}

// qoderProviderTestClientStub captures serial request bytes and returns
// controlled wrapped-stream data. Input header maps are borrowed, while
// recorded body slices are copied for later assertions.
type qoderProviderTestClientStub struct {
	// request and requests retain constructed request pointers after capture,
	// while bodies receives independent copies of each input byte slice. Configure
	// the fixed response body before calls and read captures after serial execution.
	request  *http.Request   // captured borrowed HTTP request
	requests []*http.Request // captured borrowed HTTP request pointers
	body     string          // configured wrapped-stream response text
	bodies   [][]byte        // copied captured request body slices
	// err is returned after request bytes and headers are captured. A configured
	// failure bypasses fixed stream response or doer invocation while retaining
	// those observations.
	err error // configured fixture callback failure
	// headers retains the map supplied by stream request preparation. Callers keep
	// that map stable while the serial fixture and request assertions inspect the
	// recorded values.
	headers map[string]string // borrowed supplied request header map
}

// qoderProviderTestOAuthClientStub captures the session token used for serial
// user-info preparation.
type qoderProviderTestOAuthClientStub struct {
	token string // captured user-info session token
	// err is returned after token text is captured for user-info assertions. A nil
	// failure supplies the fixed fixture user identity to the probe preparation
	// path.
	err error // configured fixture callback failure
}

// qoderHTTPUpstreamRecorder records serial transport choices and supplies fixed
// user-info or stream responses.
type qoderHTTPUpstreamRecorder struct {
	body string // configured stream response text
	// userInfoBody supplies matching user-info GET response text. An empty value
	// selects the fixed default user object; ordinary stream requests instead use
	// body.
	userInfoBody string // configured user-info response text
	// userInfoStatusCode overrides the status for matching user-info GET requests
	// when nonzero. Zero retains the fixture default HTTP 200; other requests use
	// their ordinary stream status.
	userInfoStatusCode int // optional user-info HTTP status
	// proxyURL, providerID, providerConcurrency and profileSet describe the latest
	// serial transport call. requests retains the history of borrowed request
	// pointers; tests finish execution before comparing these observations.
	proxyURL            string          // captured selected transport proxy
	providerID          int64           // captured TokenRouter provider identifier
	providerConcurrency int             // captured provider request capacity
	profileSet          bool            // captured TLS-profile presence flag
	requests            []*http.Request // captured borrowed HTTP request pointers
}

// qoderTestOutputFixture pairs a borrowed execution context and shared HTTP
// recorder for Qoder scenarios.
type qoderTestOutputFixture struct {
	// Context and recorder are retained by synchronous Qoder execution helpers.
	// The test supplies a usable context and inspects the shared recorder after
	// event output finishes.
	context.Context                            // borrowed fixture execution context
	recorder        *httptest.ResponseRecorder // shared fixture response recorder
}

// qoderTargetFixture holds the borrowed target selected for shared Qoder test
// preparation.
type qoderTargetFixture struct {
	// target is returned for every fixture load. Its executor and provider record
	// are configured by the test before shared preparation and kept usable through
	// synchronous execution.
	target providercore.TestTarget // borrowed configured test target
}

// TestProviderTestService_AdaptiveChatOnlyProvidersTestChatAndAnthropicEndpoints
// runs adaptive Zhipu against controlled Chat and Messages streams. Both
// endpoint credentials and URLs match configuration, and one start plus one
// completion encloses the two successful checks.
func TestProviderTestService_AdaptiveChatOnlyProvidersTestChatAndAnthropicEndpoints(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(301, capability.PlatformZhipu)
	svc, upstream := adaptiveNativeProtocolTestService(
		provider,
		adaptiveTextAPIKeyChatTestResponse(),
		adaptiveTextAPIKeyAnthropicTestResponse(),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "glm-4.7", "hello", providercore.ProviderTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, "http://anthropic.example/v1/messages", upstream.requests[1].URL.String())
	require.Equal(t, "Bearer sk-adaptive-test", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, "sk-adaptive-test", anthropic.GetHeaderRaw(upstream.requests[1].Header, "x-api-key"))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_start"`))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
	require.Contains(t, recorder.Body.String(), "Verified through native /v1/messages")
}

// TestProviderTestService_AdaptiveDeepSeekAlsoTestsResponsesEndpoint runs
// adaptive DeepSeek through Chat, Messages and Responses fixtures. Its
// Responses request uses the provider-specific path, OpenAI transport profile
// and stream fields, with one final completion event.
func TestProviderTestService_AdaptiveDeepSeekAlsoTestsResponsesEndpoint(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(302, capability.PlatformDeepseek)
	svc, upstream := adaptiveNativeProtocolTestService(
		provider,
		adaptiveTextAPIKeyChatTestResponse(),
		adaptiveTextAPIKeyAnthropicTestResponse(),
		adaptiveTextAPIKeyResponsesTestResponse(),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "deepseek-chat", "", providercore.ProviderTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "http://responses.example/responses", upstream.requests[2].URL.String())
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.requests[2].Context()))
	require.Equal(t, "Bearer sk-adaptive-test", upstream.requests[2].Header.Get("Authorization"))
	require.True(t, gjson.GetBytes(upstream.bodies[2], "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "store").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "instructions").Exists())
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
	require.Contains(t, recorder.Body.String(), "Verified through native /responses")
}

// TestProviderTestService_AdaptiveKimiAlsoTestsResponsesEndpoint runs adaptive
// Kimi through three configured native endpoints. Its Responses request uses
// the v1 path and expected stream fields, with one final successful completion
// event.
func TestProviderTestService_AdaptiveKimiAlsoTestsResponsesEndpoint(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(306, capability.PlatformKimi)
	svc, upstream := adaptiveNativeProtocolTestService(
		provider,
		adaptiveTextAPIKeyChatTestResponse(),
		adaptiveTextAPIKeyAnthropicTestResponse(),
		adaptiveTextAPIKeyResponsesTestResponse(),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "k3-256k", "", providercore.ProviderTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 3)
	require.Equal(t, "http://responses.example/v1/responses", upstream.requests[2].URL.String())
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.requests[2].Context()))
	require.Equal(t, "Bearer sk-adaptive-test", upstream.requests[2].Header.Get("Authorization"))
	require.True(t, gjson.GetBytes(upstream.bodies[2], "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "store").Bool())
	require.False(t, gjson.GetBytes(upstream.bodies[2], "instructions").Exists())
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
	require.Contains(t, recorder.Body.String(), "Verified through native /responses")
}

// TestProviderTestService_AdaptiveStopsAndNamesFailingEndpoint injects a
// missing Messages route after successful Chat. The adaptive check stops after
// two requests and emits an endpoint-specific error with no terminal success
// event.
func TestProviderTestService_AdaptiveStopsAndNamesFailingEndpoint(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(303, capability.PlatformDeepseek)
	svc, upstream := adaptiveNativeProtocolTestService(
		provider,
		adaptiveTextAPIKeyChatTestResponse(),
		newJSONResponse(http.StatusNotFound, `{"error":{"message":"missing messages route"}}`),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "deepseek-chat", "", providercore.ProviderTestModeDefault)

	require.Error(t, err)
	require.Contains(t, err.Error(), "Adaptive Anthropic endpoint returned 404")
	require.Len(t, upstream.requests, 2)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
}

// TestProviderTestService_AdaptiveRejectsInvalidAnthropicSuccessBody returns
// HTML with HTTP 200 from an adaptive Messages endpoint. Missing message_stop
// makes the check fail after two requests, and SSE output reports failure
// rather than completion.
func TestProviderTestService_AdaptiveRejectsInvalidAnthropicSuccessBody(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(305, capability.PlatformKimi)
	svc, upstream := adaptiveNativeProtocolTestService(
		provider,
		adaptiveTextAPIKeyChatTestResponse(),
		newJSONResponse(http.StatusOK, `<html>not an Anthropic stream</html>`),
	)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "kimi-k2.5", "", providercore.ProviderTestModeDefault)

	require.Error(t, err)
	require.Contains(t, err.Error(), "Adaptive Anthropic stream ended before message_stop")
	require.Len(t, upstream.requests, 2)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.NotContains(t, recorder.Body.String(), `"type":"test_complete"`)
}

// TestProviderTestService_FixedTextAPIKeyChatProtocolStillTestsOnlyChatEndpoint
// selects a fixed Chat protocol on a Zhipu record. The test sends one request
// to its configured Chat path and emits one successful completion.
func TestProviderTestService_FixedTextAPIKeyChatProtocolStillTestsOnlyChatEndpoint(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(304, capability.PlatformZhipu)
	provider.Credentials["api_protocol"] = providercore.APIProtocolChatCompletions
	provider.Credentials["base_url"] = "http://fixed-chat.example/v1"
	svc, upstream := adaptiveNativeProtocolTestService(provider, adaptiveTextAPIKeyChatTestResponse())
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "glm-4.7", "", providercore.ProviderTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://fixed-chat.example/v1/chat/completions", upstream.requests[0].URL.String())
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
}

// TestProviderTestService_FixedTextAPIKeyAnthropicUsesNativeEndpointWithoutBetaQuery
// selects fixed Anthropic protocol and its configured Zhipu base URL. The
// request uses the native Messages path and API-key header with no beta query
// string.
func TestProviderTestService_FixedTextAPIKeyAnthropicUsesNativeEndpointWithoutBetaQuery(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(306, capability.PlatformZhipu)
	provider.Credentials["api_protocol"] = providercore.APIProtocolAnthropic
	provider.Credentials["base_url"] = "https://open.bigmodel.cn/api/anthropic"
	svc, upstream := adaptiveNativeProtocolTestService(provider, adaptiveTextAPIKeyAnthropicTestResponse())
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "glm-4.7", "", providercore.ProviderTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic/v1/messages", upstream.requests[0].URL.String())
	require.Empty(t, upstream.requests[0].URL.RawQuery)
	require.Equal(t, "sk-adaptive-test", anthropic.GetHeaderRaw(upstream.requests[0].Header, "x-api-key"))
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

// TestProviderTestService_FixedTextAPIKeyAnthropicUsesProviderDefault selects
// Anthropic protocol while removing protocol-specific base URLs from a Zhipu
// record. One request uses the platform's fixed Messages default and the test
// succeeds.
func TestProviderTestService_FixedTextAPIKeyAnthropicUsesProviderDefault(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(307, capability.PlatformZhipu)
	provider.Credentials["api_protocol"] = providercore.APIProtocolAnthropic
	delete(provider.Credentials, "api_base_urls")
	svc, upstream := adaptiveNativeProtocolTestService(provider, adaptiveTextAPIKeyAnthropicTestResponse())
	c, _ := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "glm-4.7", "", providercore.ProviderTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://open.bigmodel.cn/api/anthropic/v1/messages", upstream.requests[0].URL.String())
}

// TestProviderTestService_FixedTextAPIKeyAnthropicRejectsOpenAIBaseURLAndMarksAuthErrors
// checks a misconfigured Zhipu URL and a separate forbidden response. URL
// mismatch sends no request, while an admitted 403 records provider error state
// through the fixture store.
func TestProviderTestService_FixedTextAPIKeyAnthropicRejectsOpenAIBaseURLAndMarksAuthErrors(t *testing.T) {
	t.Run("misconfigured base URL", func(t *testing.T) {
		provider := adaptiveNativeProtocolTestProvider(308, capability.PlatformZhipu)
		provider.Credentials["api_protocol"] = providercore.APIProtocolAnthropic
		provider.Credentials["base_url"] = "https://open.bigmodel.cn/api/paas/v4"
		svc, upstream := adaptiveNativeProtocolTestService(provider)
		c, recorder := newTestContext()

		err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "glm-4.7", "", providercore.ProviderTestModeDefault)

		require.Error(t, err)
		require.Empty(t, upstream.requests)
		require.Contains(t, recorder.Body.String(), "looks like an OpenAI-compatible endpoint")
	})

	t.Run("forbidden marks provider error", func(t *testing.T) {
		provider := adaptiveNativeProtocolTestProvider(309, capability.PlatformZhipu)
		provider.Credentials["api_protocol"] = providercore.APIProtocolAnthropic
		provider.Credentials["base_url"] = "https://open.bigmodel.cn/api/anthropic"
		svc, _ := adaptiveNativeProtocolTestService(provider, newJSONResponse(http.StatusForbidden, `{"error":{"message":"invalid key"}}`))
		c, _ := newTestContext()

		err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "glm-4.7", "", providercore.ProviderTestModeDefault)

		require.Error(t, err)
		repo := testassert.MustType[*openAIProbeStore](svc.Store)
		require.Equal(t, provider.ID, repo.setErrorID)
	})
}

// TestProviderTestService_AdaptiveSelectedProtocolTestsOnlyThatEndpoint selects
// Anthropic explicitly for an adaptive DeepSeek record. One configured Messages
// request runs inside one start/completion pair, protecting per-test protocol
// selection from full adaptive enumeration.
func TestProviderTestService_AdaptiveSelectedProtocolTestsOnlyThatEndpoint(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(310, capability.PlatformDeepseek)
	svc, upstream := adaptiveNativeProtocolTestService(provider, adaptiveTextAPIKeyAnthropicTestResponse())
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequestType(t, svc, c, provider.ID, "deepseek-chat", "", providercore.ProviderTestTypeText, providercore.ProviderTestModeDefault, providercore.APIProtocolAnthropic)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "http://anthropic.example/v1/messages", upstream.requests[0].URL.String())
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_start"`))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), `"type":"test_complete"`))
}

// TestProviderTestService_SelectedProtocolMustBeEnabled selects Anthropic for a
// Zhipu provider whose enabled set contains only Chat. Preparation rejects the
// selection locally and captures no upstream request.
func TestProviderTestService_SelectedProtocolMustBeEnabled(t *testing.T) {
	provider := adaptiveNativeProtocolTestProvider(311, capability.PlatformZhipu)
	provider.Credentials[providercore.UpstreamProtocolsKey] = []any{string(protocol.ProtocolOpenAIChatCompletions)}
	svc, upstream := adaptiveNativeProtocolTestService(provider)
	c, recorder := newTestContext()

	err := executeOpenAIProbeRequestType(t, svc, c, provider.ID, "glm-4.7", "", providercore.ProviderTestTypeText, providercore.ProviderTestModeDefault, providercore.APIProtocolAnthropic)

	require.Error(t, err)
	require.Empty(t, upstream.requests)
	require.Contains(t, recorder.Body.String(), "Test protocol anthropic is not supported for this provider")
}

// TestProviderTestServiceKimiZhipuDeepSeekUsesConfiguredProtocol varies Kimi
// Chat, Zhipu Messages and DeepSeek Responses provider configuration. The
// injected 400 response stops execution after capture, whose URL, auth header
// and model match the selected protocol.
func TestProviderTestServiceKimiZhipuDeepSeekUsesConfiguredProtocol(t *testing.T) {
	tests := []struct {
		name       string               // scenario description text
		provider   *providercore.Record // selected provider fixture record
		wantPath   string               // expected upstream URL path
		wantModel  string               // expected outgoing model identifier
		wantHeader string               // expected authorization header name
	}{
		{
			name: "Kimi Chat Completions",
			provider: &providercore.Record{
				ID: 1, Platform: capability.PlatformKimi, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "kimi-key", "base_url": "https://relay.example/v1"},
			},
			wantPath:   "/v1/chat/completions",
			wantModel:  "kimi-k2.5",
			wantHeader: "Authorization",
		},
		{
			name: "智谱 Anthropic Messages",
			provider: &providercore.Record{
				ID: 2, Platform: capability.PlatformZhipu, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "zhipu-key", "api_protocol": providercore.APIProtocolAnthropic, "base_url": "https://relay.example/anthropic"},
			},
			wantPath:   "/anthropic/v1/messages",
			wantModel:  "glm-4.7",
			wantHeader: "x-api-key",
		},
		{
			name: "DeepSeek Responses",
			provider: &providercore.Record{
				ID: 3, Platform: capability.PlatformDeepseek, Type: capability.ProviderTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "deepseek-key", "api_protocol": providercore.APIProtocolResponses, "base_url": "https://relay.example"},
			},
			wantPath: "/v1/responses",
			// Responses providers use the OpenAI probe and its default model when none is supplied.
			wantModel:  openai.DefaultTestModel,
			wantHeader: "Authorization",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &textAPIKeyTestRepo{provider: test.provider}
			upstream := &textAPIKeyTestHTTP{}
			service := newTextAPIKeyProtocolTestCore(repo.GetByID, upstream)
			recorder := httptest.NewRecorder()
			require.Error(t, service.Test(t.Context(), providercore.TestRequest{ProviderID: test.provider.ID, Prompt: "hi", Mode: providercore.ProviderTestModeDefault, Type: func() *string { kind := providercore.ProviderTestTypeText; return &kind }()}, NewTestEventSink(recorder)))

			require.NotNil(t, upstream.request)
			require.Equal(t, "relay.example", upstream.request.URL.Hostname())
			require.Equal(t, test.wantPath, upstream.request.URL.Path)
			require.NotEmpty(t, anthropic.GetHeaderRaw(upstream.request.Header, test.wantHeader))
			require.Equal(t, test.wantModel, gjson.Get(upstream.body, "model").String())
		})
	}
}

// TestProviderTestService_TestProviderConnection_GrokUsesXAIResponses probes
// Grok OAuth with a configured model mapping and controlled text stream. The
// CLI Responses endpoint receives mapped model, token and client headers, and
// emits a successful completion.
func TestProviderTestService_TestProviderConnection_GrokUsesXAIResponses(t *testing.T) {
	provider := &providercore.Record{
		ID:          13,
		Name:        "grok-oauth",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			"model_mapping": map[string]any{
				"grok": "grok-latest",
			},
		},
	}
	repo := &grokTestStoreFixture{
		providersByID: map[int64]*providercore.Record{provider.ID: provider},
	}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &provideradapter.GrokProviderTest{
		Store:     repo,
		Tokens:    &providercore.GrokTokenSource{Repository: repo, Policy: providercore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}

	rec := httptest.NewRecorder()

	err := executeGrokProviderTest(t, svc, provider, rec, "grok", "", providercore.ProviderTestModeDefault, providercore.ProviderTestTypeText)
	require.NoError(t, err)

	require.Equal(t, "https://cli-chat-proxy.grok.com/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer grok-access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, xai.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "application/json, text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "grok-latest", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "input").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Exists())
	require.NotContains(t, rec.Body.String(), "claude")
	require.Contains(t, rec.Body.String(), `"model":"grok-latest"`)
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

// TestProviderTestService_TestProviderConnection_GrokUsesCustomTextPrompt
// probes Grok OAuth using an explicit text prompt. The outgoing Responses input
// retains that prompt and the controlled completed stream returns success.
func TestProviderTestService_TestProviderConnection_GrokUsesCustomTextPrompt(t *testing.T) {
	provider := &providercore.Record{
		ID:          17,
		Name:        "grok-custom-prompt",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	repo := &grokTestStoreFixture{providersByID: map[int64]*providercore.Record{provider.ID: provider}}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n")),
	}}
	svc := &provideradapter.GrokProviderTest{
		Store:     repo,
		Tokens:    &providercore.GrokTokenSource{Repository: repo, Policy: providercore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()

	err := executeGrokProviderTest(t, svc, provider, recorder, "grok-4.5", "describe this provider in one sentence", providercore.ProviderTestModeDefault, providercore.ProviderTestTypeText)
	require.NoError(t, err)
	require.Equal(t, "describe this provider in one sentence", gjson.GetBytes(upstream.lastBody, "input").String())
}

// TestProviderTestService_TestProviderConnection_GrokExplicitImageUsesMediaEndpoint
// requests an explicit image test for a Grok API-key alias. Its media request
// retains model and prompt, authenticates with the API key and emits the
// returned image data URL.
func TestProviderTestService_TestProviderConnection_GrokExplicitImageUsesMediaEndpoint(t *testing.T) {
	provider := &providercore.Record{
		ID:          18,
		Name:        "grok-image-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "grok-api-key"},
	}
	repo := &grokTestStoreFixture{providersByID: map[int64]*providercore.Record{provider.ID: provider}}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aGVsbG8=","mime_type":"image/png"}]}`)),
	}}
	svc := &provideradapter.GrokProviderTest{
		Store: repo, Transport: upstream, OperatorValidator: (egress.OperatorURLPolicy{}).Validate,
		Attributes: func(_ context.Context, value *providercore.Record, _ int64, model, protocol string) (modelrules.Resolution, error) {
			require.Equal(t, "custom-image-alias", model)
			require.Equal(t, "openai_images_generations", protocol)
			image := []string{"image"}
			return testkit.OfflineView().Resolve(value.ModelAttributeTarget(model, protocol), modelcatalog.Attributes{OutputModalities: &image}), nil
		},
	}
	recorder := httptest.NewRecorder()

	err := executeGrokProviderTest(t, svc, provider, recorder, "custom-image-alias", "draw a lighthouse", providercore.ProviderTestModeDefault, providercore.ProviderTestTypeImage)
	require.NoError(t, err)
	require.Equal(t, "https://api.x.ai/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer grok-api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "custom-image-alias", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "draw a lighthouse", gjson.GetBytes(upstream.lastBody, "prompt").String())
	require.Contains(t, recorder.Body.String(), "data:image/png;base64,aGVsbG8=")
}

// TestProviderTestService_TestProviderConnection_GrokDefaultsEmptyModelTo45
// probes Grok OAuth with empty model input and a mapping for the default name.
// Default selection occurs after mapping, so the outgoing model stays the
// current default.
func TestProviderTestService_TestProviderConnection_GrokDefaultsEmptyModelTo45(t *testing.T) {
	provider := &providercore.Record{
		ID:          16,
		Name:        "grok-oauth-default-model",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
			// The default model is selected after provider mapping, so this mapping key is skipped for empty input.
			"model_mapping": map[string]any{"grok-4.5": "grok-4.3"},
		},
	}
	repo := &grokTestStoreFixture{providersByID: map[int64]*providercore.Record{provider.ID: provider}}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &provideradapter.GrokProviderTest{
		Store:     repo,
		Tokens:    &providercore.GrokTokenSource{Repository: repo, Policy: providercore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()

	err := executeGrokProviderTest(t, svc, provider, recorder, "", "", providercore.ProviderTestModeDefault)

	require.NoError(t, err)
	require.Equal(t, xai.DefaultResponsesModel, gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, recorder.Body.String(), `"model":"grok-4.5"`)
}

// TestProviderTestService_Grok429PersistsRateLimitReset returns Grok 429 with
// Retry-After forty-five seconds. The fixture records one rate-limit write and
// a reset deadline near that supplied wait.
func TestProviderTestService_Grok429PersistsRateLimitReset(t *testing.T) {
	provider := &providercore.Record{
		ID:          14,
		Name:        "grok-oauth-limited",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	baseRepo := &grokTestStoreFixture{providersByID: map[int64]*providercore.Record{provider.ID: provider}}
	repo := &grokProviderTestRateLimitRepo{grokTestStoreFixture: baseRepo}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := &provideradapter.GrokProviderTest{
		Store:     repo,
		Tokens:    &providercore.GrokTokenSource{Repository: repo, Policy: providercore.GrokProviderRefreshPolicy()},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()

	err := executeGrokProviderTest(t, svc, provider, recorder, "grok", "", providercore.ProviderTestModeDefault, providercore.ProviderTestTypeText)

	require.Error(t, err)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, time.Now().Add(45*time.Second), repo.resetAt, time.Second)
}

// TestProviderTestService_Grok429WithoutQuotaHeadersUsesFallback returns Grok
// 429 without quota or retry headers. One rate-limit write uses the controlled
// two-minute recovery fallback near the test's observed start time.
func TestProviderTestService_Grok429WithoutQuotaHeadersUsesFallback(t *testing.T) {
	provider := &providercore.Record{
		ID: 15, Name: "grok-oauth-limited-no-headers", Platform: capability.PlatformGrok,
		Type: capability.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "grok-access-token",
			"refresh_token": "grok-refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}
	baseRepo := &grokTestStoreFixture{providersByID: map[int64]*providercore.Record{provider.ID: provider}}
	repo := &grokProviderTestRateLimitRepo{grokTestStoreFixture: baseRepo}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"quota exhausted"}}`)),
	}}
	svc := &provideradapter.GrokProviderTest{
		Store: repo, Tokens: &providercore.GrokTokenSource{Repository: repo, Policy: providercore.GrokProviderRefreshPolicy()}, Transport: upstream,
	}
	recorder := httptest.NewRecorder()
	before := time.Now()

	err := executeGrokProviderTest(t, svc, provider, recorder, "grok", "", providercore.ProviderTestModeDefault, providercore.ProviderTestTypeText)

	require.Error(t, err)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.WithinDuration(t, before.Add(2*time.Minute), repo.resetAt, time.Second)
}

// TestProviderTestServiceGrokAPIKeyUsesXAIResponses probes a configured xAI
// API-key record with a completed Responses stream. URL and authorization match
// the API-key route and the event sink contains successful completion.
func TestProviderTestServiceGrokAPIKeyUsesXAIResponses(t *testing.T) {
	provider := &providercore.Record{
		ID:          54,
		Name:        "grok-api-key",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeAPIKey,
		Concurrency: 2,
		Credentials: map[string]any{
			"api_key":  "xai-test-key",
			"base_url": "https://api.x.ai/v1",
		},
	}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &provideradapter.GrokProviderTest{Transport: upstream}
	recorder := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: recorder}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/54/test", nil)

	err := executeGrokProbe(t, svc, c, provider, "grok")
	require.NoError(t, err)
	require.Equal(t, "https://api.x.ai/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer xai-test-key", upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

// TestProviderTestServiceGrokAPIKeyAllowsConfiguredHTTPWhenGlobalPolicyDoes
// permits an HTTP relay through the operator URL policy. The API-key request
// reaches that relay with its bearer key and omits the CLI client-version
// header.
func TestProviderTestServiceGrokAPIKeyAllowsConfiguredHTTPWhenGlobalPolicyDoes(t *testing.T) {
	provider := &providercore.Record{
		ID:          55,
		Name:        "grok-api-key-http",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "third-party-key",
			"base_url": "http://grok.example.test/v1",
		},
	}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"data: {\"type\":\"response.completed\"}\n\n",
		)),
	}}
	svc := &provideradapter.GrokProviderTest{OperatorValidator: (egress.OperatorURLPolicy{AllowInsecureHTTP: true}).Validate, Transport: upstream}
	recorder := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: recorder}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/55/test", nil)

	err := executeGrokProbe(t, svc, c, provider, "grok")
	require.NoError(t, err)
	require.Equal(t, "http://grok.example.test/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer third-party-key", upstream.lastReq.Header.Get("Authorization"))
	require.Empty(t, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

// TestProviderTestServiceGrokOAuthPaymentRequiredTemporarilyUnschedulesProvider
// returns spending-limit 402 from a Grok OAuth probe. The fixture records a
// ten-minute rate-limit write, no temporary-unschedulable write and an endpoint
// error event.
func TestProviderTestServiceGrokOAuthPaymentRequiredTemporarilyUnschedulesProvider(t *testing.T) {
	provider := healthyGrokOAuthGatewayTestProvider(56, "access-token")
	repo := &grokTestFailureStoreFixture{}
	upstream := &grokTestTransportFixture{resp: &http.Response{
		StatusCode: http.StatusPaymentRequired,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"code":"personal-team-blocked:spending-limit"}`)),
	}}
	svc := &provideradapter.GrokProviderTest{
		Store:     repo,
		Tokens:    &providercore.GrokTokenSource{},
		Transport: upstream,
	}
	recorder := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: recorder}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/56/test", nil)
	before := time.Now()

	err := executeGrokProbe(t, svc, c, provider, "grok")

	require.Error(t, err)
	require.Zero(t, repo.tempUnschedCalls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, provider.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, before.Add(10*time.Minute), repo.lastRateLimitResetAt, time.Second)
	require.Contains(t, recorder.Body.String(), `"type":"error"`)
	require.Contains(t, recorder.Body.String(), "Grok Responses API returned 402")
}

// TestProviderTestServiceOpenAICompactAgentIdentityUsesFreshAssertion probes
// native compaction using local Agent Identity credentials. The request carries
// an assertion plus account/FedRAMP headers, and its authorization text omits
// the raw private key.
func TestProviderTestServiceOpenAICompactAgentIdentityUsesFreshAssertion(t *testing.T) {
	key, privateKey := newProbeAgentKey(t)
	provider := providercore.Record{
		ID:          21,
		Name:        "agent-identity",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode":                  providercore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":           key.RuntimeID,
			"agent_private_key":          privateKey,
			"task_id":                    key.TaskID,
			"chatgpt_account_id":         "provider-agent-test",
			"chatgpt_account_is_fedramp": true,
		},
	}
	repo := &probeAgentStore{provider: &provider}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream, EnsureTask: probeAgentTasks(repo, "", nil).Ensure}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/21/test", bytes.NewReader(nil))

	require.NoError(t, executeOpenAIProbeRequest(t, svc, c, provider.ID, "gpt-5.4", "", providercore.ProviderTestModeCompact))
	require.Equal(t, "AgentAssertion", strings.SplitN(upstream.lastReq.Header.Get("Authorization"), " ", 2)[0])
	require.Equal(t, "provider-agent-test", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "true", upstream.lastReq.Header.Get("x-openai-fedramp"))
	require.NotContains(t, upstream.lastReq.Header.Get("Authorization"), privateKey)
}

// TestProviderTestServiceOpenAICompactAgentIdentityRecoversInvalidTaskOnce
// returns invalid_task_id before one successful compaction response. One local
// registration installs the new task ID and triggers invalidation, with two
// requests and no permanent provider error write.
func TestProviderTestServiceOpenAICompactAgentIdentityRecoversInvalidTaskOnce(t *testing.T) {
	key, privateKey := newProbeAgentKey(t)
	provider := &providercore.Record{
		ID:          22,
		Name:        "agent-identity-recovery",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode":          providercore.OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   key.RuntimeID,
			"agent_private_key":  privateKey,
			"task_id":            "task-compact-old",
			"chatgpt_account_id": "provider-agent-compact-recovery",
		},
	}
	repo := &probeAgentStore{provider: provider}
	registerCalls := 0
	registerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registerCalls++
		// A response-write failure remains visible to the foreground registration
		// client and its probe assertions; this callback records arrival before attempting its fixed reply.
		_, _ = io.WriteString(w, `{"task_id":"task-compact-new"}`)
	}))
	// Task recovery uses this local registration endpoint during the foreground
	// probe. Close it after assertions to join server ownership before the fixture leaves scope.
	defer registerServer.Close()

	upstream := &openAIProbeTransport{responses: []*http.Response{
		{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_task_id"}}`))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody))},
	}}
	invalidator := &probeAgentInvalidations{}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream, EnsureTask: probeAgentTasks(repo, registerServer.URL, invalidator).Ensure}
	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/22/test", bytes.NewReader(nil))

	require.NoError(t, executeOpenAIProbeRequest(t, svc, c, provider.ID, "gpt-5.4", "", providercore.ProviderTestModeCompact))
	require.Equal(t, 1, registerCalls)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "task-compact-new", provider.GetCredential("task_id"))
	require.Equal(t, 0, repo.setErrorCalls)
	require.Equal(t, []int64{provider.ID}, invalidator.providerIDs)
}

// TestProviderTestService_TestProviderConnection_OpenAICompactOAuthUsesNativeV2AndDoesNotPersistSupport
// probes OAuth native compaction with a complete compaction stream. Codex URL,
// client headers and trigger payload match the request path, while manual
// testing leaves capability Extra writes absent.
func TestProviderTestService_TestProviderConnection_OpenAICompactOAuthUsesNativeV2AndDoesNotPersistSupport(t *testing.T) {
	updateCalls := make(chan map[string]any, 1)
	provider := providercore.Record{
		ID:          1,
		Name:        "openai-oauth",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{provider.ID: &provider}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-probe"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes: probeModelAttributes,
		Store:      repo,
		Transport:  upstream,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/1/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "gpt-5.4", "", providercore.ProviderTestModeCompact)
	require.NoError(t, err)

	require.Equal(t, "https://chatgpt.com/backend-api/codex/responses", upstream.lastReq.URL.String())
	require.Equal(t, "chatgpt.com", upstream.lastReq.Host)
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, openai.CodexCLIVersion, upstream.lastReq.Header.Get("Version"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("Session_Id"))
	require.Contains(t, upstream.lastReq.Header.Get("x-codex-beta-features"), "remote_compaction_v2")
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, openai.CodexCLIUserAgent, upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, openai.CodexDefaultOriginator, upstream.lastReq.Header.Get("Originator"))
	require.Equal(t, "chatgpt-acc", upstream.lastReq.Header.Get("chatgpt-account-id"))
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	input := gjson.GetBytes(upstream.lastBody, "input").Array()
	require.NotEmpty(t, input)
	require.Equal(t, "compaction_trigger", input[len(input)-1].Get("type").String())

	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")
	require.Contains(t, rec.Body.String(), `"type":"test_complete"`)
}

// TestProviderTestService_TestProviderConnection_OpenAICompactOAuth404DoesNotChangeCapability
// returns 404 during a manual OAuth compaction test with configured capability.
// The test emits failure and records no capability Extra update from this
// manual result.
func TestProviderTestService_TestProviderConnection_OpenAICompactOAuth404DoesNotChangeCapability(t *testing.T) {
	updateCalls := make(chan map[string]any, 1)
	provider := providercore.Record{
		ID:          2,
		Name:        "openai-oauth",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra: map[string]any{
			"openai_compact_mode": "force_on",
		},
		Credentials: map[string]any{
			"access_token":       "oauth-token",
			"chatgpt_account_id": "chatgpt-acc",
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{provider.ID: &provider}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`404 page not found`)),
	}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes: probeModelAttributes,
		Store:      repo,
		Transport:  upstream,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/2/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "gpt-5.4", "", providercore.ProviderTestModeCompact)
	require.Error(t, err)

	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")
	require.Contains(t, rec.Body.String(), `"type":"error"`)
}

// TestProviderTestService_TestProviderConnection_OpenAICompactAPIKeyUsesNativeResponsesPath
// probes API-key native compaction with a separate legacy mapping configured.
// The request uses Responses and the requested model, advertises v2 beta and
// records no capability write.
func TestProviderTestService_TestProviderConnection_OpenAICompactAPIKeyUsesNativeResponsesPath(t *testing.T) {
	updateCalls := make(chan map[string]any, 1)
	provider := providercore.Record{
		ID:          3,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":               "sk-test",
			"base_url":              "https://example.com/v1",
			"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{provider.ID: &provider}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/3/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "gpt-5.4", "", providercore.ProviderTestModeCompact)
	require.NoError(t, err)

	require.Equal(t, "https://example.com/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Contains(t, upstream.lastReq.Header.Get("x-codex-beta-features"), "remote_compaction_v2")
	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")
}

// TestProviderTestService_TestProviderConnection_OpenAICompactAPIKeyDefaultBaseURLUsesResponsesPath
// probes native compaction with an API key and no custom root. The request uses
// the default OpenAI Responses URL and leaves manual capability-write capture
// empty.
func TestProviderTestService_TestProviderConnection_OpenAICompactAPIKeyDefaultBaseURLUsesResponsesPath(t *testing.T) {
	updateCalls := make(chan map[string]any, 1)
	provider := providercore.Record{
		ID:          4,
		Name:        "openai-apikey-default",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{provider.ID: &provider}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(compactionTestV2SSESuccessBody)),
	}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/4/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "gpt-5.4", "", providercore.ProviderTestModeCompact)
	require.NoError(t, err)
	require.Equal(t, "https://api.openai.com/v1/responses", upstream.lastReq.URL.String())
	require.Empty(t, updateCalls)
}

// TestProviderTestService_TestProviderConnection_OpenAILegacyCompactUsesDedicatedPathWithoutState
// probes legacy compaction using a separate compact model mapping. The request
// uses the dedicated compact path and mapped model, while capability state
// capture remains empty.
func TestProviderTestService_TestProviderConnection_OpenAILegacyCompactUsesDedicatedPathWithoutState(t *testing.T) {
	updateCalls := make(chan map[string]any, 1)
	provider := providercore.Record{
		ID:          5,
		Name:        "openai-legacy-compact",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":               "sk-test",
			"base_url":              "https://example.com/v1",
			"compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.4-openai-compact"},
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{provider.ID: &provider}}, updateExtraCalls: updateCalls}
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"legacy_compact_probe","status":"completed"}`)),
	}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}

	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/5/test", bytes.NewReader(nil))

	err := executeOpenAIProbeRequest(t, svc, c, provider.ID, "gpt-5.4", "", providercore.ProviderTestModeLegacyCompact)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/v1/responses/compact", upstream.lastReq.URL.String())
	require.Equal(t, "gpt-5.4-openai-compact", gjson.GetBytes(upstream.lastBody, "model").String())

	require.Empty(t, updateCalls, "手动压缩测试不应写入能力状态")
}

// TestManualCompactionTestsPreserveConfigurationAcrossOutcomes varies
// compaction modes and success, authentication, missing-route and rate-limit
// responses. Capability choices stay configured while 401 and 429 still reach
// their separate health-write fixtures.
func TestManualCompactionTestsPreserveConfigurationAcrossOutcomes(t *testing.T) {
	for _, mode := range []string{providercore.ProviderTestModeCompact, providercore.ProviderTestModeLegacyCompact} {
		for _, status := range []int{200, 401, 404, 429} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				extra := map[string]any{"openai_compact_mode": "force_off", providercore.OpenAINativeCompactionV2ModeExtraKey: "force_on"}
				provider := &providercore.Record{
					ID: 13, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive,
					Credentials: map[string]any{"access_token": "test"}, Extra: extra,
				}
				repo := &openAIProbeStore{}
				body := compactionTestV2SSESuccessBody
				if mode == providercore.ProviderTestModeLegacyCompact {
					body = `{"id":"legacy_test","status":"completed"}`
				}
				if status != 200 {
					body = fmt.Sprintf(`{"error":{"type":"usage_limit_reached","message":"test failure","resets_at":%d}}`, time.Now().Add(time.Hour).Unix())
				}
				resp := newJSONResponse(status, body)
				upstream := &openAIProbeTransport{resp: resp}
				svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
				c, _ := newTestContext()
				err := executeOpenAIProbe(t, svc, c, provider, "gpt-5.4", "", mode)
				if status == 200 {
					require.NoError(t, err)
				} else {
					require.Error(t, err)
				}
				require.Equal(t, "force_off", provider.Extra["openai_compact_mode"])
				require.Equal(t, "force_on", provider.Extra[providercore.OpenAINativeCompactionV2ModeExtraKey])
				for _, key := range providercore.DeprecatedOpenAIProviderExtraKeys {
					require.NotContains(t, repo.updatedExtra, key)
				}
				require.NotContains(t, repo.updatedExtra, "openai_compact_mode")
				require.NotContains(t, repo.updatedExtra, providercore.OpenAINativeCompactionV2ModeExtraKey)
				if status == 401 {
					require.Equal(t, provider.ID, repo.setErrorID)
				}
				if status == 429 {
					require.Equal(t, provider.ID, repo.rateLimitedID)
				}
			})
		}
	}
}

// TestProviderTestService_OpenAIImageOAuthHandlesOutputItemDoneFallback
// supplies image result in output_item.done followed by completed output with
// no image item. The OAuth image test emits that saved image data and
// successful completion through the event sink.
func TestProviderTestService_OpenAIImageOAuthHandlesOutputItemDoneFallback(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/1/test", nil)

	upstream := &openAIProbeTransport{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
			},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_123\",\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\",\"revised_prompt\":\"draw a cat\",\"output_format\":\"png\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"created_at\":1710000006,\"tool_usage\":{\"image_gen\":{\"images\":1}},\"output\":[]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Transport: upstream}
	provider := &providercore.Record{
		ID:       53,
		Name:     "openai-oauth",
		Extra:    map[string]any{providercore.CodexImageResponsesModelKey: "gpt-6.1-sol"},
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		},
	}

	err := executeOpenAIImageOAuthProbe(t, svc, c, context.Background(), provider, "gpt-image-2", "draw a cat")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "gpt-image-2", gjson.GetBytes(upstream.lastBody, "tools.0.model").String())
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Contains(t, rec.Body.String(), "Calling Codex /responses image tool")
	require.Contains(t, rec.Body.String(), "data:image/png;base64,aGVsbG8=")
	require.Contains(t, rec.Body.String(), "\"success\":true")
}

// TestProviderTestService_OpenAIImageOAuthForwardsTLSProfile probes OAuth image
// generation with provider fingerprint enablement. The controlled transport
// receives a nonnil TLS profile and the OpenAI request-context profile while
// the image stream succeeds.
func TestProviderTestService_OpenAIImageOAuthForwardsTLSProfile(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/1/test", nil)

	upstream := &openAIProbeTransport{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_456\",\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\",\"output_format\":\"png\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		},
	}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes: probeModelAttributes,
		Transport:  upstream,
		ResolveTLS: (&provideradapter.OpenAIProbePolicy{ManualProfiles: &egressadapter.TLSProfiles{}}).ResolveTestTLS,
	}
	provider := &providercore.Record{
		ID:       55,
		Name:     "openai-oauth-tls",
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token": "token-123",
		},
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}

	err := executeOpenAIImageOAuthProbe(t, svc, c, context.Background(), provider, "gpt-image-2", "draw a cat")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastTLSProfile)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
}

// TestProviderTestService_OpenAIImageAPIKeyUsesConfiguredV1BaseURL probes
// API-key image generation against a configured v1 root. The outgoing image
// path and authorization match, and returned base64 image data appears in
// successful event output.
func TestProviderTestService_OpenAIImageAPIKeyUsesConfiguredV1BaseURL(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/1/test", nil)

	upstream := &openAIProbeTransport{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			Body: io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aGVsbG8=","revised_prompt":"draw a cat"}]}`)),
		},
	}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:       54,
		Name:     "openai-apikey",
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://image-upstream.example/v1",
		},
	}

	err := executeOpenAIImageAPIKeyProbe(t, svc, c, context.Background(), provider, "gpt-image-2", "draw a cat")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "https://image-upstream.example/v1/images/generations", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer test-api-key", upstream.lastReq.Header.Get("Authorization"))
	require.Contains(t, rec.Body.String(), "data:image/png;base64,aGVsbG8=")
	require.Contains(t, rec.Body.String(), "\"success\":true")
}

// TestProviderTestService_OpenAIExplicitImageTypeDoesNotInspectModelName
// selects image testing for a model alias containing no image marker. Endpoint
// selection follows the explicit kind and the request retains the supplied
// alias and image prompt.
func TestProviderTestService_OpenAIExplicitImageTypeDoesNotInspectModelName(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/1/test", nil)

	upstream := &openAIProbeTransport{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"b64_json":"aGVsbG8="}]}`)),
		},
	}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes: func(context.Context, *providercore.Record, int64, string, string) (modelrules.Resolution, error) {
			image := []string{"image"}
			return modelrules.Resolution{Attributes: modelcatalog.Attributes{OutputModalities: &image}}, nil
		},
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:       56,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://image-upstream.example/v1",
		},
	}

	err := executeOpenAIProbe(t, svc, c, provider, "custom-model-alias", "draw a lighthouse", providercore.ProviderTestModeDefault, providercore.ProviderTestTypeImage)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://image-upstream.example/v1/images/generations", upstream.lastReq.URL.String())
	body, err := io.ReadAll(upstream.lastReq.Body)
	require.NoError(t, err)
	require.Equal(t, "custom-model-alias", gjson.GetBytes(body, "model").String())
	require.Equal(t, "draw a lighthouse", gjson.GetBytes(body, "prompt").String())
}

// TestProviderTestService_OpenAIExplicitTextTypeDoesNotInspectModelName selects
// text testing for an image-named model ID. The outgoing request still uses
// Responses text input, retaining the supplied model and prompt.
func TestProviderTestService_OpenAIExplicitTextTypeDoesNotInspectModelName(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &openAIProbeOutput{recorder: rec}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/1/test", nil)

	upstream := &openAIProbeTransport{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n")),
		},
	}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:       57,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://text-upstream.example/v1",
		},
	}

	err := executeOpenAIProbe(t, svc, c, provider, "gpt-image-2", "reply briefly", providercore.ProviderTestModeDefault, providercore.ProviderTestTypeText)
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://text-upstream.example/v1/responses", upstream.lastReq.URL.String())
	body, err := io.ReadAll(upstream.lastReq.Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-2", gjson.GetBytes(body, "model").String())
	require.Equal(t, "reply briefly", gjson.GetBytes(body, "input.0.content.0.text").String())
}

// TestProviderTestService_OpenAIImageAPIKeyHandlesURLAndEmptyImages returns a
// valid HTTPS image URL, malformed native JSON, or an invalid URL. Valid output
// emits image and success. Shared validation rejects malformed image arrays and
// error envelopes before preview output, while URL presentation rejects unsupported schemes.
func TestProviderTestService_OpenAIImageAPIKeyHandlesURLAndEmptyImages(t *testing.T) {
	cases := []struct {
		name    string // scenario description text
		body    string // configured or captured fixture text
		wantErr bool   // expected execution error presence
		want    string // expected event body text
	}{
		{"url 返回", `{"data":[{"url":"https://cdn.example/cat.png"}]}`, false, `"image_url":"https://cdn.example/cat.png"`},
		{"没有图片数据", `{"data":[{"revised_prompt":"draw a cat"}]}`, true, "invalid_upstream_response"},
		{"空图片数组", `{"data":[]}`, true, "invalid_upstream_response"},
		{"后续图片损坏", `{"data":[{"b64_json":"image"},{"url":7}]}`, true, "invalid_upstream_response"},
		{"HTTP 200 错误体", `{"error":{"type":"server_error","code":"server_error","message":"failed"}}`, true, "server_error"},
		{"拒绝非 https 链接", `{"data":[{"url":"javascript:alert(1)"}]}`, true, "Upstream returned no image data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c := &openAIProbeOutput{recorder: rec}
			c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/providers/1/test", nil)
			upstream := &openAIProbeTransport{
				resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(tc.body)),
				},
			}
			svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Transport: upstream, ValidateURL: (egress.OperatorURLPolicy{}).Validate}
			provider := &providercore.Record{
				ID:          55,
				Name:        "openai-apikey",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-api-key", "base_url": "https://image-upstream.example/v1"},
			}

			err := executeOpenAIImageAPIKeyProbe(t, svc, c, context.Background(), provider, "gpt-image-2", "draw a cat")
			require.Equal(t, tc.wantErr, err != nil)
			require.Contains(t, rec.Body.String(), tc.want)
			require.Equal(t, !tc.wantErr, strings.Contains(rec.Body.String(), `"success":true`))
			if tc.wantErr {
				require.Contains(t, rec.Body.String(), `"type":"error"`)
				require.NotContains(t, rec.Body.String(), `"type":"image"`)
			}
		})
	}
}

// TestProviderTestServiceSkipsShadow loads a credential shadow with its parent
// absent from the store. The probe returns the parent-resolution error before
// it can construct a usable credential request.
func TestProviderTestServiceSkipsShadow(t *testing.T) {
	pid := int64(100)
	shadow := &providercore.Record{
		ID:               200,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.ProviderTypeOAuth,
		ParentProviderID: &pid,
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{shadow.ID: shadow}}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo}
	c, _ := newTestContext()

	err := executeOpenAIProbeRequest(t, svc, c, 200, "", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "resolve spark shadow parent")
}

// TestProviderTestService_OpenAISuccessPersistsSnapshotFromHeaders returns a
// completed OAuth stream with reversed primary/secondary window durations. The
// captured Extra maps percentages to five-hour and seven-day windows and event
// output reports completion.
func TestProviderTestService_OpenAISuccessPersistsSnapshotFromHeaders(t *testing.T) {
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	resp.Header.Set("x-codex-primary-used-percent", "88")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "42")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIProbeStore{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
	provider := &providercore.Record{
		ID:          89,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(req.Context()))
	require.Equal(t, "responses=experimental", req.Header.Get("OpenAI-Beta"))
	require.Equal(t, openai.CodexDefaultOriginator, req.Header.Get("Originator"))
	require.Equal(t, openai.CodexCLIUserAgent, req.Header.Get("User-Agent"))
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 42.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, 88.0, repo.updatedExtra["codex_7d_used_percent"])
	require.Contains(t, recorder.Body.String(), "test_complete")
}

// TestProviderTestService_OpenAIOAuthTestDoesNotRedirectBareGPT56 probes an
// explicitly supplied bare model name using a controlled completed stream. The
// outgoing request retains that exact model and custom prompt after OAuth test
// preparation.
func TestProviderTestService_OpenAIOAuthTestDoesNotRedirectBareGPT56(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Transport: upstream}
	provider := &providercore.Record{
		ID:          90,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.6", "connection probe", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)

	body, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.6", gjson.GetBytes(body, "model").String())
	require.Equal(t, "connection probe", gjson.GetBytes(body, "input.0.content.0.text").String())
}

// TestProviderTestService_OpenAIOAuthUsesConfiguredUserAgent probes OAuth with
// a stored custom client user agent. The transport receives that exact agent
// and its recognized originator while the completed stream succeeds.
func TestProviderTestService_OpenAIOAuthUsesConfiguredUserAgent(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Transport: upstream}
	provider := &providercore.Record{
		ID:          890,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"user_agent":   "codex-tui/9.9.0 test-terminal",
		},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "codex-tui/9.9.0 test-terminal", upstream.requests[0].Header.Get("User-Agent"))
	require.Equal(t, "codex-tui", upstream.requests[0].Header.Get("Originator"))
}

// TestProviderTestService_OpenAIShadowUsesParentCredentialsAndShadowModel loads
// a Spark shadow with its parent fixture record. The outgoing request uses
// parent token/account hints and the shadow's requested model, then emits
// success.
func TestProviderTestService_OpenAIShadowUsesParentCredentialsAndShadowModel(t *testing.T) {
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))

	parentID := int64(100)
	parent := &providercore.Record{
		ID:       parentID,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"access_token":       "parent-token",
			"chatgpt_account_id": "org-parent",
		},
	}
	shadow := &providercore.Record{
		ID:               200,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.ProviderTypeOAuth,
		Status:           billing.StatusActive,
		ParentProviderID: &parentID,
		QuotaDimension:   providercore.QuotaDimensionSpark,
		Concurrency:      2,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gpt-5.3-codex-spark": "gpt-5.3-codex-spark",
			},
		},
	}

	repo := &openAIProbeStore{
		openAIProbeRecords: openAIProbeRecords{
			providersByID: map[int64]*providercore.Record{
				parentID: parent,
				200:      shadow,
			},
		},
	}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}

	err := executeOpenAIProbeRequest(t, svc, ctx, shadow.ID, "gpt-5.3-codex-spark", "", "")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	req := upstream.requests[0]
	require.Equal(t, "Bearer parent-token", req.Header.Get("Authorization"))
	require.Equal(t, "org-parent", req.Header.Get("chatgpt-account-id"))
	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.3-codex-spark", gjson.GetBytes(body, "model").String())
	require.Contains(t, recorder.Body.String(), `"success":true`)
}

// TestProviderTestService_OpenAIStreamEOFBeforeCompletedFails ends an OAuth
// stream after one text delta with no completed event. The test returns error
// and its SSE output mentions the missing terminal event without success.
func TestProviderTestService_OpenAIStreamEOFBeforeCompletedFails(t *testing.T) {
	ctx, recorder := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"hi"}

`))

	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Transport: upstream}
	provider := &providercore.Record{
		ID:          90,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), "response.completed")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

// TestProviderTestService_RunTestBackgroundRequestOverridesHeader
// runs the background test use case with an explicit prompt and agent string.
// The result reports success and transport headers retain the supplied agent
// and recognized originator.
func TestProviderTestService_RunTestBackgroundRequestOverridesHeader(t *testing.T) {
	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`))
	provider := providercore.Record{
		ID:          901,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
		},
	}
	repo := &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{provider.ID: &provider}}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes: probeModelAttributes,
		Store:      repo,
		Transport:  upstream,
	}

	result, err := openAIProbeCore(svc).RunTestBackgroundRequest(context.Background(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-5.4", Prompt: "hi", UserAgent: "codex-tui/9.9.1 test-terminal"})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "codex-tui/9.9.1 test-terminal", upstream.requests[0].Header.Get("User-Agent"))
	require.Equal(t, "codex-tui", upstream.requests[0].Header.Get("Originator"))
}

// TestProviderTestService_OpenAI429PersistsSnapshotAndRateLimitState returns
// 429 with quota headers and a reset-bearing error for a stale-error provider.
// Snapshot percentages and rate-limit writes are captured, and the observed
// record clears its stale error.
func TestProviderTestService_OpenAI429PersistsSnapshotAndRateLimitState(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_at":1777283883}}`)
	resp.Header.Set("x-codex-primary-used-percent", "100")
	resp.Header.Set("x-codex-primary-reset-after-seconds", "604800")
	resp.Header.Set("x-codex-primary-window-minutes", "10080")
	resp.Header.Set("x-codex-secondary-used-percent", "100")
	resp.Header.Set("x-codex-secondary-reset-after-seconds", "18000")
	resp.Header.Set("x-codex-secondary-window-minutes", "300")

	repo := &openAIProbeStore{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
	provider := &providercore.Record{
		ID:          88,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      providercore.StatusError,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.NotEmpty(t, repo.updatedExtra)
	require.Equal(t, 100.0, repo.updatedExtra["codex_5h_used_percent"])
	require.Equal(t, provider.ID, repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.Equal(t, provider.ID, repo.clearedErrorID)
	require.Equal(t, billing.StatusActive, provider.Status)
	require.Empty(t, provider.ErrorMessage)
	require.NotNil(t, provider.RateLimitResetAt)
}

// TestProviderTestService_OpenAI429BodyOnlyPersistsRateLimitAndClearsStaleError
// returns a reset-bearing 429 body with no quota headers. Rate-limit recovery
// clears the observed stale error and installs its reset deadline while Extra
// snapshot capture remains empty.
func TestProviderTestService_OpenAI429BodyOnlyPersistsRateLimitAndClearsStaleError(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_at":"1777283883"}}`)

	repo := &openAIProbeStore{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
	provider := &providercore.Record{
		ID:           77,
		Platform:     capability.PlatformOpenAI,
		Type:         capability.ProviderTypeOAuth,
		Status:       providercore.StatusError,
		ErrorMessage: "Access forbidden (403): provider may be suspended or lack permissions",
		Concurrency:  1,
		Credentials:  map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, provider.ID, repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.Equal(t, provider.ID, repo.clearedErrorID)
	require.Equal(t, billing.StatusActive, provider.Status)
	require.Empty(t, provider.ErrorMessage)
	require.NotNil(t, provider.RateLimitResetAt)
	require.Empty(t, repo.updatedExtra)
}

// TestProviderTestService_OpenAI429SyncsObservedPlanType returns a
// reset-bearing 429 that reports a different subscription plan. The fixture
// captures the selected plan credential update and the observed record receives
// that plan and reset state.
func TestProviderTestService_OpenAI429SyncsObservedPlanType(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","plan_type":"free","resets_at":1777283883}}`)

	repo := &openAIProbeStore{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
	provider := &providercore.Record{
		ID:          81,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token", "plan_type": "plus"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, []int64{provider.ID}, repo.bulkUpdatedIDs)
	require.Equal(t, "free", repo.bulkUpdatedPayload.Credentials["plan_type"])
	require.Equal(t, "free", provider.Credentials["plan_type"])
	require.Equal(t, provider.ID, repo.rateLimitedID)
	require.NotNil(t, provider.RateLimitResetAt)
}

// TestProviderTestService_OpenAI429ActiveProviderDoesNotClearError returns a
// reset-bearing 429 for an already active provider. Rate-limit state is
// recorded while the error-clear fixture remains unused and active status stays
// visible.
func TestProviderTestService_OpenAI429ActiveProviderDoesNotClearError(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached","resets_in_seconds":3600}}`)

	repo := &openAIProbeStore{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
	provider := &providercore.Record{
		ID:          78,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, provider.ID, repo.rateLimitedID)
	require.NotNil(t, repo.rateLimitedAt)
	require.Zero(t, repo.clearedErrorID)
	require.Equal(t, billing.StatusActive, provider.Status)
	require.NotNil(t, provider.RateLimitResetAt)
}

// TestProviderTestService_OpenAI429WithoutResetSignalDoesNotMutateRuntimeState
// returns 429 with no usable reset observation for a stale-error provider. No
// rate-limit or error-clear call occurs, and its stored error fields remain on
// the observed record.
func TestProviderTestService_OpenAI429WithoutResetSignalDoesNotMutateRuntimeState(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"type":"usage_limit_reached","message":"limit reached"}}`)

	repo := &openAIProbeStore{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
	provider := &providercore.Record{
		ID:           79,
		Platform:     capability.PlatformOpenAI,
		Type:         capability.ProviderTypeOAuth,
		Status:       providercore.StatusError,
		ErrorMessage: "stale 403",
		Concurrency:  1,
		Credentials:  map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Zero(t, repo.rateLimitedID)
	require.Nil(t, repo.rateLimitedAt)
	require.Zero(t, repo.clearedErrorID)
	require.Equal(t, providercore.StatusError, provider.Status)
	require.Equal(t, "stale 403", provider.ErrorMessage)
	require.Nil(t, provider.RateLimitResetAt)
}

// TestProviderTestService_OpenAI401SetsPermanentErrorOnly returns
// authentication failure from an OAuth probe. The store captures the
// authentication error text, while rate-limit and stale-error clearing
// observations remain absent.
func TestProviderTestService_OpenAI401SetsPermanentErrorOnly(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusUnauthorized, `{"error":"bad token"}`)

	repo := &openAIProbeStore{}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: repo, Transport: upstream}
	provider := &providercore.Record{
		ID:          80,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, provider.ID, repo.setErrorID)
	require.Contains(t, repo.setErrorMsg, "Authentication failed (401)")
	require.Zero(t, repo.rateLimitedID)
	require.Zero(t, repo.clearedErrorID)
	require.Nil(t, provider.RateLimitResetAt)
}

// TestProviderTestService_DeepSeekResponsesRoutesToOpenAIProbe loads a DeepSeek
// API-key record configured for Responses through the shared test service. One
// request reaches its configured Responses URL and the controlled completed
// stream succeeds.
func TestProviderTestService_DeepSeekResponsesRoutesToOpenAIProbe(t *testing.T) {
	ctx, _ := newTestContext()

	resp := newJSONResponse(http.StatusOK, "")
	resp.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\"}\n\n"))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{resp}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:          93,
		Platform:    capability.PlatformDeepseek,
		Type:        capability.ProviderTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"base_url":     "https://relay.example.com/v1",
			"api_protocol": providercore.APIProtocolResponses,
		},
		Extra: map[string]any{},
	}
	repo := &openAIProbeStore{
		openAIProbeRecords: openAIProbeRecords{
			providersByID: map[int64]*providercore.Record{93: provider},
		},
	}
	svc.Store = repo

	err := executeOpenAIProbeRequest(t, svc, ctx, provider.ID, "gpt-5.4", "", providercore.ProviderTestTypeText)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "https://relay.example.com/v1/responses", upstream.requests[0].URL.String())
}

// TestProviderTestService_OpenAIAPIKeySelectedChatUsesChatCompletionsPath
// forces Chat routing for an API-key text probe with a custom prompt. URL,
// headers and messages match Chat format, and streamed text plus
// endpoint-specific success appear in event output.
func TestProviderTestService_OpenAIAPIKeySelectedChatUsesChatCompletionsPath(t *testing.T) {
	ctx, recorder := newTestContext()

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:          91,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example/v1",
		},
		Extra: map[string]any{providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModeForceChatCompletions)},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "hello", "")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-test", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "gpt-5.4", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	body := recorder.Body.String()
	require.Contains(t, body, "pong")
	require.Contains(t, body, "Verified through /v1/chat/completions")
	require.Contains(t, body, `"success":true`)
	require.NotContains(t, body, "当前测试接口仅支持 Responses API 路径")
}

// TestProviderTestService_OpenAIChatCompletionsPathReturns4xx returns HTTP 400
// from an API-key Chat probe. The failure identifies the Chat endpoint in both
// returned error and event output, with no successful completion.
func TestProviderTestService_OpenAIChatCompletionsPathReturns4xx(t *testing.T) {
	ctx, recorder := newTestContext()

	upstream := &openAIProbeTransport{resp: newJSONResponse(http.StatusBadRequest, `{"error":{"message":"bad request"}}`)}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:          92,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModeForceChatCompletions)},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Chat Completions API (/v1/chat/completions) returned 400")
	require.Contains(t, recorder.Body.String(), "/v1/chat/completions")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

// TestProviderTestService_OpenAIChatCompletionsPathTimeout injects
// context.DeadlineExceeded from the controlled Chat transport. The returned
// error retains the selected endpoint and injected cause, and event output
// excludes success.
func TestProviderTestService_OpenAIChatCompletionsPathTimeout(t *testing.T) {
	ctx, recorder := newTestContext()

	upstream := &openAIProbeTransport{err: context.DeadlineExceeded}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:          93,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModeForceChatCompletions)},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Chat Completions API (/v1/chat/completions) request failed")
	require.Contains(t, err.Error(), context.DeadlineExceeded.Error())
	require.Contains(t, recorder.Body.String(), "/v1/chat/completions")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

// TestProviderTestService_OpenAIChatCompletionsPathRejectsNonJSONStream returns
// invalid JSON inside a nominally successful Chat stream. The test reports an
// invalid endpoint response and emits failure rather than a success event.
func TestProviderTestService_OpenAIChatCompletionsPathRejectsNonJSONStream(t *testing.T) {
	ctx, recorder := newTestContext()

	upstream := &openAIProbeTransport{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: not-json\n\n")),
	}}
	svc := &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{}).Validate,
	}
	provider := &providercore.Record{
		ID:          94,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://compat-upstream.example",
		},
		Extra: map[string]any{providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModeForceChatCompletions)},
	}

	err := executeOpenAIProbe(t, svc, ctx, provider, "gpt-5.4", "", "")
	require.Error(t, err)
	require.Equal(t, "https://compat-upstream.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.Contains(t, err.Error(), "Invalid Chat Completions response from /v1/chat/completions")
	require.Contains(t, recorder.Body.String(), "/v1/chat/completions")
	require.NotContains(t, recorder.Body.String(), `"success":true`)
}

// TestProviderTestServiceExplicitProtocolDoesNotMutateProvider selects
// Responses or Chat against the opposite stored routing choice and injects
// request failure. Capture confirms the selected per-test path while the source
// record retains its configured route value.
func TestProviderTestServiceExplicitProtocolDoesNotMutateProvider(t *testing.T) {
	for _, protocol := range []string{"responses", "chat_completions"} {
		t.Run(protocol, func(t *testing.T) {
			provider := providercore.Record{ID: 901, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "sk-test", "base_url": "https://example.com/v1"}, Extra: map[string]any{"openai_text_route_mode": "force_chat_completions"}}
			if protocol == "chat_completions" {
				provider.Extra["openai_text_route_mode"] = "force_responses"
			}
			original := provider.Extra["openai_text_route_mode"]
			upstream := &openAIProbeTransport{resp: &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":"capture"}`))}}
			svc := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: &openAIProbeStore{openAIProbeRecords: openAIProbeRecords{providersByID: map[int64]*providercore.Record{provider.ID: &provider}}}, Transport: upstream, ValidateURL: (egress.OperatorURLPolicy{}).Validate}
			c, _ := newTestContext()
			c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
			require.Error(t, executeOpenAIProbeRequestType(t, svc, c, provider.ID, "gpt-5.4", "hi", "text", "default", protocol))
			path := "/v1/responses"
			if protocol == "chat_completions" {
				path = "/v1/chat/completions"
			}
			require.Equal(t, path, upstream.lastReq.URL.Path)
			require.Equal(t, original, provider.Extra["openai_text_route_mode"])
		})
	}
}

// TestProviderTestService_AutomaticOpenAIProbeUsesMatchedRoute runs a
// background OAuth probe through an exact TLS-router match. The selected route
// supplies its TLS profile, upstream user agent and originator to the captured
// request.
func TestProviderTestService_AutomaticOpenAIProbeUsesMatchedRoute(t *testing.T) {
	provider := providercore.Record{
		ID:          1001,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
			"openai_oauth_client_policy": providercore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Name:                    "probe-client",
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "probe-client/1.0",
			TLSFingerprintProfileID: 20,
			UpstreamUserAgent:       "codex_vscode/0.144.1 probe-terminal",
			UpstreamOriginator:      "codex_vscode",
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t,
		[]providercore.Record{provider},
		upstream,
		router,
		map[int64]*egress.TLSFingerprintProfile{
			10: {ID: 10, Name: "fixed"},
			20: {ID: 20, Name: "routed"},
		},
		nil,
	)

	result, err := svc.RunTestBackgroundRequest(context.Background(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-5.4", Prompt: "hi", UserAgent: "probe-client/1.0"})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "routed", upstream.lastTLSProfile.Name)
	require.Equal(t, "codex_vscode/0.144.1 probe-terminal", upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "codex_vscode", upstream.lastReq.Header.Get("Originator"))
}

// TestProviderTestService_AutomaticOpenAIProbeRejectsClientPolicyLocally varies
// TLS-router and Codex-client policy rejections for background probes. Results
// report the selected policy and reason with failed status, while no upstream
// request is captured.
func TestProviderTestService_AutomaticOpenAIProbeRejectsClientPolicyLocally(t *testing.T) {
	tests := []struct {
		name       string                       // scenario description text
		policy     string                       // configured automatic-client policy selector
		userAgent  string                       // supplied automatic-probe user agent
		runDefault bool                         // use background default-agent entry point
		reason     string                       // expected local rejection reason
		router     *egress.TLSFingerprintRouter // configured TLS-router fixture record
		extra      map[string]any               // configured provider feature settings
	}{
		{
			name:      "TLS 路由未命中",
			policy:    providercore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			userAgent: "curl/8.0",
			reason:    providercore.CodexClientRestrictionReasonNotMatchedTLSRouter,
			router: &egress.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []egress.TLSFingerprintRouterRule{{
					Enabled:   true,
					MatchType: egress.TLSRouterMatchPrefix,
					Pattern:   "allowed-client/",
				}},
			},
			extra: map[string]any{"tls_fingerprint_router_id": int64(9)},
		},
		{
			name:      "Codex 官方身份未命中",
			policy:    providercore.OpenAIOAuthClientPolicyCodexOnly,
			userAgent: "custom-client/1.0",
			reason:    providercore.CodexClientRestrictionReasonNotMatchedUA,
		},
		{
			name:       "定时测试执行相同策略",
			policy:     providercore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			runDefault: true,
			reason:     providercore.CodexClientRestrictionReasonNotMatchedTLSRouter,
			router: &egress.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []egress.TLSFingerprintRouterRule{{
					Enabled:   true,
					MatchType: egress.TLSRouterMatchPrefix,
					Pattern:   "different-client/",
				}},
			},
			extra: map[string]any{"tls_fingerprint_router_id": int64(9)},
		},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			extra := map[string]any{"openai_oauth_client_policy": test.policy}
			maps.Copy(extra, test.extra)
			provider := providercore.Record{
				ID:       int64(1100 + i),
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeOAuth,
				Extra:    extra,
			}
			upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
			svc := newOpenAIAutomaticProbeTestService(t, []providercore.Record{provider}, upstream, test.router, nil, nil)

			var result *providercore.ScheduledTestResult
			var err error
			if test.runDefault {
				result, err = svc.RunTestBackground(context.Background(), provider.ID, "gpt-5.4")
			} else {
				result, err = svc.RunTestBackgroundRequest(context.Background(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-5.4", Prompt: "hi", UserAgent: test.userAgent})
			}

			require.NoError(t, err)
			require.Equal(t, "failed", result.Status)
			require.Contains(t, result.ErrorMessage, "policy="+test.policy)
			require.Contains(t, result.ErrorMessage, "reason="+test.reason)
			require.Empty(t, upstream.requests)
		})
	}
}

// TestProviderTestService_AutomaticOpenAIProbeFallsBackToProviderTLSProfile
// runs a background probe whose TLS router has no matching rule. The provider's
// configured profile is used and the controlled completed stream still reports
// success.
func TestProviderTestService_AutomaticOpenAIProbeFallsBackToProviderTLSProfile(t *testing.T) {
	provider := providercore.Record{
		ID:          1201,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
			"openai_oauth_client_policy": providercore.OpenAIOAuthClientPolicyAny,
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "other-client/1.0",
			TLSFingerprintProfileID: 20,
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t,
		[]providercore.Record{provider},
		upstream,
		router,
		map[int64]*egress.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}, 20: {ID: 20, Name: "unused-route"}},
		nil,
	)

	result, err := svc.RunTestBackgroundRequest(context.Background(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-5.4", Prompt: "hi", UserAgent: "unmatched-client/1.0"})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "fixed", upstream.lastTLSProfile.Name)
}

// TestProviderTestService_AutomaticOpenAIProbeEmptyUserAgentParticipatesInRouting
// runs background probes without an explicit agent for a shadow and an ordinary
// provider. Parent-derived or built-in client identity participates in router
// matching and selects the expected agent/profile.
func TestProviderTestService_AutomaticOpenAIProbeEmptyUserAgentParticipatesInRouting(t *testing.T) {
	parentID := int64(1301)
	parent := providercore.Record{
		ID:       parentID,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token": "parent-token",
			"user_agent":   "codex_vscode/0.144.1 parent-terminal",
		},
	}
	shadow := providercore.Record{
		ID:               1302,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.ProviderTypeOAuth,
		ParentProviderID: &parentID,
		QuotaDimension:   providercore.QuotaDimensionSpark,
		Concurrency:      1,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	defaultProvider := providercore.Record{
		ID:          1303,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "default-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}

	tests := []struct {
		name            string                // scenario description text
		providers       []providercore.Record // configured source provider records
		providerID      int64                 // captured TokenRouter provider identifier
		pattern         string                // configured router-match agent text
		routeProfileID  int64                 // selected route profile identifier
		expectedUA      string                // expected outgoing user agent
		expectedProfile string                // expected selected profile name
	}{
		{
			name:            "凭据提供商自定义 UA",
			providers:       []providercore.Record{parent, shadow},
			providerID:      shadow.ID,
			pattern:         "codex_vscode/0.144.1 parent-terminal",
			routeProfileID:  20,
			expectedUA:      "codex_vscode/0.144.1 parent-terminal",
			expectedProfile: "routed",
		},
		{
			name:            "内置 Codex UA",
			providers:       []providercore.Record{defaultProvider},
			providerID:      defaultProvider.ID,
			pattern:         openai.CodexCLIUserAgent,
			routeProfileID:  0,
			expectedUA:      openai.CodexCLIUserAgent,
			expectedProfile: "Built-in Default (Node.js 24.x)",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := &egress.TLSFingerprintRouter{
				ID:      9,
				Name:    "probe-router",
				Enabled: true,
				Rules: []egress.TLSFingerprintRouterRule{{
					Enabled:                 true,
					MatchType:               egress.TLSRouterMatchExact,
					Pattern:                 test.pattern,
					TLSFingerprintProfileID: test.routeProfileID,
				}},
			}
			upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
			svc := newOpenAIAutomaticProbeTestService(t,
				test.providers,
				upstream,
				router,
				map[int64]*egress.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}, 20: {ID: 20, Name: "routed"}},
				nil,
			)

			result, err := svc.RunTestBackground(context.Background(), test.providerID, "gpt-5.4")

			require.NoError(t, err)
			require.Equal(t, "success", result.Status)
			require.Equal(t, test.expectedUA, upstream.lastReq.Header.Get("User-Agent"))
			require.Equal(t, test.expectedProfile, upstream.lastTLSProfile.Name)
		})
	}
}

// TestProviderTestService_AutomaticOpenAIProbeMissingRouteProfileFallsBack
// matches a router rule whose referenced profile is missing. The background
// probe succeeds with the configured provider profile, protecting fallback
// selection from an unavailable route profile.
func TestProviderTestService_AutomaticOpenAIProbeMissingRouteProfileFallsBack(t *testing.T) {
	provider := providercore.Record{
		ID:          1401,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": int64(10),
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Enabled:                 true,
			MatchType:               egress.TLSRouterMatchExact,
			Pattern:                 "route-client/1.0",
			TLSFingerprintProfileID: 404,
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t,
		[]providercore.Record{provider},
		upstream,
		router,
		map[int64]*egress.TLSFingerprintProfile{10: {ID: 10, Name: "fixed"}},
		nil,
	)

	result, err := svc.RunTestBackgroundRequest(context.Background(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-5.4", Prompt: "hi", UserAgent: "route-client/1.0"})

	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Equal(t, "fixed", upstream.lastTLSProfile.Name)
}

// TestProviderTestService_AutomaticOpenAIProbeRoutesChatCompletionsAndImages
// runs background Chat and OAuth image probes through matched routers. The Chat
// endpoint receives its routed agent, while the image request receives its
// selected TLS profile and client headers.
func TestProviderTestService_AutomaticOpenAIProbeRoutesChatCompletionsAndImages(t *testing.T) {
	t.Run("Chat Completions", func(t *testing.T) {
		provider := providercore.Record{
			ID:          1501,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": "https://compat-upstream.example/v1",
			},
			Extra: map[string]any{
				providercore.ExtraKeyTextRouteMode: string(providercore.TextRouteModeForceChatCompletions),
				"tls_fingerprint_router_id":        int64(9),
			},
		}
		router := &egress.TLSFingerprintRouter{
			ID:      9,
			Name:    "probe-router",
			Enabled: true,
			Rules: []egress.TLSFingerprintRouterRule{{
				Enabled:           true,
				MatchType:         egress.TLSRouterMatchExact,
				Pattern:           "chat-client/1.0",
				UpstreamUserAgent: "chat-upstream/2.0",
			}},
		}
		upstream := &openAIProbeTransport{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
		}}
		cfg := &egress.OperatorURLPolicy{}
		svc := newOpenAIAutomaticProbeTestService(t, []providercore.Record{provider}, upstream, router, nil, cfg)

		result, err := svc.RunTestBackgroundRequest(context.Background(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-5.4", Prompt: "hi", UserAgent: "chat-client/1.0"})

		require.NoError(t, err)
		require.Equal(t, "success", result.Status)
		require.Equal(t, "chat-upstream/2.0", upstream.lastReq.Header.Get("User-Agent"))
		require.Contains(t, upstream.lastReq.URL.Path, "/chat/completions")
	})

	t.Run("OAuth 图片", func(t *testing.T) {
		provider := providercore.Record{
			ID:          1502,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeOAuth,
			Concurrency: 1,
			Credentials: map[string]any{"access_token": "test-token"},
			Extra: map[string]any{
				"enable_tls_fingerprint":    true,
				"tls_fingerprint_router_id": int64(9),
			},
		}
		router := &egress.TLSFingerprintRouter{
			ID:      9,
			Name:    "probe-router",
			Enabled: true,
			Rules: []egress.TLSFingerprintRouterRule{{
				Enabled:                 true,
				MatchType:               egress.TLSRouterMatchExact,
				Pattern:                 "image-client/1.0",
				TLSFingerprintProfileID: 20,
				UpstreamUserAgent:       "codex-tui/0.144.1 image-terminal",
				UpstreamOriginator:      "codex-tui",
			}},
		}
		upstream := &openAIProbeTransport{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"aGVsbG8=\",\"output_format\":\"png\"}}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n" +
					"data: [DONE]\n\n",
			)),
		}}
		svc := newOpenAIAutomaticProbeTestService(t,
			[]providercore.Record{provider},
			upstream,
			router,
			map[int64]*egress.TLSFingerprintProfile{20: {ID: 20, Name: "image-route"}},
			nil,
		)

		result, err := svc.RunTestBackgroundRequest(context.Background(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-image-2", Prompt: "draw", UserAgent: "image-client/1.0"})

		require.NoError(t, err)
		require.Equal(t, "success", result.Status)
		require.Equal(t, "image-route", upstream.lastTLSProfile.Name)
		require.Equal(t, "codex-tui/0.144.1 image-terminal", upstream.lastReq.Header.Get("User-Agent"))
		require.Equal(t, "codex-tui", upstream.lastReq.Header.Get("Originator"))
	})
}

// TestProviderTestService_ManualOpenAITestDoesNotEnforceAutomaticPolicy runs a
// manual OAuth test against a record with unmatched automatic-client
// restrictions. One upstream request is admitted and its controlled completed
// stream succeeds through the manual entry point.
func TestProviderTestService_ManualOpenAITestDoesNotEnforceAutomaticPolicy(t *testing.T) {
	provider := providercore.Record{
		ID:          1601,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "test-token"},
		Extra: map[string]any{
			"openai_oauth_client_policy": providercore.OpenAIOAuthClientPolicyTLSRouterMatchedOnly,
			"tls_fingerprint_router_id":  int64(9),
		},
	}
	router := &egress.TLSFingerprintRouter{
		ID:      9,
		Name:    "probe-router",
		Enabled: true,
		Rules: []egress.TLSFingerprintRouterRule{{
			Enabled:   true,
			MatchType: egress.TLSRouterMatchExact,
			Pattern:   "never-matched-by-manual-test",
		}},
	}
	upstream := &openAIProbeTransport{resp: successfulOpenAIAutomaticProbeResponse()}
	svc := newOpenAIAutomaticProbeTestService(t, []providercore.Record{provider}, upstream, router, nil, nil)
	c, _ := newTestContext()

	err := svc.Test(c.Request.Context(), providercore.TestRequest{ProviderID: provider.ID, Model: "gpt-5.4", Prompt: "hi", Mode: providercore.ProviderTestModeDefault}, NewTestEventSink(c.recorder))

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
}

// TestProviderTestService_QoderCosyUsesNativeTestPath runs a COSY session
// through wrapped Qoder reasoning and text deltas. Content output includes
// visible text and completion but omits reasoning-only text and generic type
// rejection.
func TestProviderTestService_QoderCosyUsesNativeTestPath(t *testing.T) {
	ctx, recorder := newQoderProviderTestContext()
	provider := &providercore.Record{
		ID:          7,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Concurrency: 1,
		Credentials: map[string]any{
			"security_oauth_token": "token",
			"machine_id":           "machine",
		},
	}
	client := &qoderProviderTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &provideradapter.QoderProviderTest{
		Sessions: &qoderProviderTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:   client,
		UserInfo: &qoderProviderTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, provider, "auto", "hi", "")

	require.NoError(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, `"type":"content"`)
	require.Contains(t, body, `"text":"OK"`)
	require.NotContains(t, body, "hidden thought")
	require.Contains(t, body, `"type":"test_complete"`)
	require.NotContains(t, body, "Unsupported provider type: cosy")
	require.NotNil(t, client.request)
}

// TestProviderTestService_QoderPATRebuildsSessionForConnectionTest probes a
// PAT-backed COSY record using a controlled session provider. Testing
// records one prior-session invalidation dispatch and succeeds through the
// supplied completed stream. The session fixture returns its configured pointer
// after that recorded call.
func TestProviderTestService_QoderPATRebuildsSessionForConnectionTest(t *testing.T) {
	ctx, _ := newQoderProviderTestContext()
	provider := &providercore.Record{
		ID:       12,
		Name:     "qoder-pat",
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-123",
		},
	}
	tokenSource := &qoderProviderTestSessionProviderStub{
		session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
	}
	svc := &provideradapter.QoderProviderTest{
		Sessions: tokenSource,
		Client: &qoderProviderTestClientStub{
			body: "data: {\"body\":\"[DONE]\"}\n\n",
		},
		UserInfo: &qoderProviderTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, provider, "", "", "")

	require.NoError(t, err)
	require.Equal(t, []int64{provider.ID}, tokenSource.invalidated)
}

// TestProviderTestService_QoderProbesUserInfoBeforeStream probes a COSY session
// with a controlled user-info client before completion. The client receives the
// session token and streaming uses the default auto model selector.
func TestProviderTestService_QoderProbesUserInfoBeforeStream(t *testing.T) {
	ctx, recorder := newQoderProviderTestContext()
	provider := &providercore.Record{
		ID:       11,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
	}
	client := &qoderProviderTestClientStub{
		body: "data: {\"body\":\"[DONE]\"}\n\n",
	}
	oauthClient := &qoderProviderTestOAuthClientStub{}
	svc := &provideradapter.QoderProviderTest{
		Sessions: &qoderProviderTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "session-token"}},
		},
		Client:   client,
		UserInfo: oauthClient,
	}

	err := executeQoderTest(t, ctx, svc, provider, "", "", "")

	require.NoError(t, err)
	require.Equal(t, "session-token", oauthClient.token)
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
	require.Equal(t, "auto", client.headers["x-model-key"])
}

// TestProviderTestService_QoderWrappedErrorIsVisible supplies a wrapped
// forbidden Qoder stream error with a signature code. The test returns failure
// and emits that normalized error text through the shared event sink.
func TestProviderTestService_QoderWrappedErrorIsVisible(t *testing.T) {
	ctx, recorder := newQoderProviderTestContext()
	provider := &providercore.Record{
		ID:       8,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
	}
	client := &qoderProviderTestClientStub{
		body: "data: {\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
	}
	svc := &provideradapter.QoderProviderTest{
		Sessions: &qoderProviderTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:   client,
		UserInfo: &qoderProviderTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, provider, "", "", "")

	require.Error(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, `"type":"error"`)
	require.Contains(t, body, "Qoder upstream error 101: Signature invalid")
	require.NotContains(t, body, "Unsupported provider type: cosy")
}

// TestProviderTestService_QoderReasoningOnlyDoesNotEmitContent supplies a
// wrapped reasoning delta followed by stream completion. The test succeeds with
// a terminal event while no content event or reasoning text appears.
func TestProviderTestService_QoderReasoningOnlyDoesNotEmitContent(t *testing.T) {
	ctx, recorder := newQoderProviderTestContext()
	provider := &providercore.Record{
		ID:       9,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
	}
	client := &qoderProviderTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &provideradapter.QoderProviderTest{
		Sessions: &qoderProviderTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:   client,
		UserInfo: &qoderProviderTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, provider, "", "", "")

	require.NoError(t, err)
	body := recorder.Body.String()
	require.NotContains(t, body, `"type":"content"`)
	require.NotContains(t, body, "hidden thought")
	require.Contains(t, body, `"type":"test_complete"`)
}

// TestProviderTestService_QoderUsesHTTPUpstreamForProxyAndTLS probes a COSY
// record with selected proxy and fingerprint enablement. The controlled
// transport receives its proxy URL, provider ID and TLS profile and event
// output includes visible text.
func TestProviderTestService_QoderUsesHTTPUpstreamForProxyAndTLS(t *testing.T) {
	ctx, recorder := newQoderProviderTestContext()
	proxyID := int64(12)
	provider := &providercore.Record{
		ID:          10,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Concurrency: 2,
		ProxyID:     &proxyID,
		Proxy: &egress.Proxy{
			ID:       proxyID,
			Protocol: "http",
			Host:     "proxy.example.com",
			Port:     8080,
		},
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}
	client := &qoderProviderTestClientStub{}
	upstream := &qoderHTTPUpstreamRecorder{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &provideradapter.QoderProviderTest{
		Sessions: &qoderProviderTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:    client,
		UserInfo:  &qoderProviderTestOAuthClientStub{},
		Transport: upstream,
		Profiles:  &egressadapter.TLSProfiles{},
	}

	err := executeQoderTest(t, ctx, svc, provider, "", "", "")

	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"text":"OK"`)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(10), upstream.providerID)
	require.True(t, upstream.profileSet)
	require.NotNil(t, client.request)
}

// TestProviderTestService_QoderDefaultUserInfoProbeUsesHTTPUpstream omits the
// separate user-info client so the controlled transport handles both
// preparation and stream requests. The session receives observed UID and proxy,
// concurrency and TLS choices are retained.
func TestProviderTestService_QoderDefaultUserInfoProbeUsesHTTPUpstream(t *testing.T) {
	ctx, recorder := newQoderProviderTestContext()
	proxyID := int64(12)
	provider := &providercore.Record{
		ID:          12,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Concurrency: 3,
		ProxyID:     &proxyID,
		Proxy: &egress.Proxy{
			ID:       proxyID,
			Protocol: "http",
			Host:     "proxy.example.com",
			Port:     8080,
		},
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}
	client := &qoderProviderTestClientStub{}
	upstream := &qoderHTTPUpstreamRecorder{
		userInfoBody: `{"id":"user-12","name":"Qoder User"}`,
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &provideradapter.QoderProviderTest{
		Sessions: &qoderProviderTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:    client,
		Transport: upstream,
		Profiles:  &egressadapter.TLSProfiles{},
	}

	err := executeQoderTest(t, ctx, svc, provider, "", "", "")

	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"text":"OK"`)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Contains(t, upstream.requests[0].URL.Path, qoder.UserInfoPath)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(12), upstream.providerID)
	require.Equal(t, 3, upstream.providerConcurrency)
	require.True(t, upstream.profileSet)
	// Check the Qoder session supplied to the probe through its session provider.
	sessionProvider, ok := svc.Sessions.(*qoderProviderTestSessionProviderStub)
	require.True(t, ok)
	require.Equal(t, "user-12", sessionProvider.session.Identity.UID)
}

// TestProviderTestService_QoderUserInfoProbeRedactsSensitiveErrorBody returns
// user-info failure containing token, cookie and identity text. Event output
// reports normalized failure with redaction markers while those fixture secrets
// remain absent.
func TestProviderTestService_QoderUserInfoProbeRedactsSensitiveErrorBody(t *testing.T) {
	ctx, recorder := newQoderProviderTestContext()
	provider := &providercore.Record{
		ID:          13,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Concurrency: 1,
		Credentials: map[string]any{
			"security_oauth_token": "token",
		},
	}
	upstream := &qoderHTTPUpstreamRecorder{
		userInfoStatusCode: http.StatusInternalServerError,
		userInfoBody:       `{"message":"failed","securityOauthToken":"sec-secret","refresh_token":"rt-secret","uid":"uid-secret","cookie":"sid=secret"}`,
	}
	svc := &provideradapter.QoderProviderTest{
		Sessions: &qoderProviderTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:    &qoderProviderTestClientStub{},
		Transport: upstream,
		UserInfo:  nil,
	}

	err := executeQoderTest(t, ctx, svc, provider, "", "", "")

	require.Error(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, "qoder userinfo probe failed")
	require.NotContains(t, body, "sec-secret")
	require.NotContains(t, body, "rt-secret")
	require.NotContains(t, body, "uid-secret")
	require.NotContains(t, body, "sid=secret")
	require.Contains(t, body, "***")
}

// adaptiveNativeProtocolTestProvider constructs an active API-key fixture with
// independent protocol root selections. Its returned credential maps remain
// mutable for each scenario to choose fixed or adaptive endpoint behavior
// before execution.
func adaptiveNativeProtocolTestProvider(id int64, platform string) *providercore.Record {
	return &providercore.Record{
		ID:          id,
		Name:        "adaptive-cn-test",
		Platform:    platform,
		Type:        capability.ProviderTypeAPIKey,
		Status:      billing.StatusActive,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":      "sk-adaptive-test",
			"api_protocol": providercore.APIProtocolAdaptive,
			"api_base_urls": map[string]any{
				providercore.APIProtocolChatCompletions: "http://chat.example/v1",
				providercore.APIProtocolAnthropic:       "http://anthropic.example",
				providercore.APIProtocolResponses:       "http://responses.example",
			},
		},
	}
}

// adaptiveNativeProtocolTestService binds one provider to the controlled queued
// transport and permits its HTTP fixture roots. Supplied response pointers are
// consumed in order, and the returned executor delegates preparation through
// the shared probe helpers.
func adaptiveNativeProtocolTestService(provider *providercore.Record, responses ...*http.Response) (*provideradapter.OpenAIProviderTest, *openAIProbeTransport) {
	repo := &openAIProbeStore{
		openAIProbeRecords: openAIProbeRecords{
			providersByID: map[int64]*providercore.Record{provider.ID: provider},
		},
	}
	upstream := &openAIProbeTransport{responses: responses}
	return &provideradapter.OpenAIProviderTest{
		Attributes:  probeModelAttributes,
		Store:       repo,
		Transport:   upstream,
		ValidateURL: (egress.OperatorURLPolicy{AllowInsecureHTTP: true}).Validate,
	}, upstream
}

// adaptiveTextAPIKeyChatTestResponse constructs a fresh successful Chat stream
// with visible text and termination. The executor consumes and closes this
// body, so each expected request needs its own response instance.
func adaptiveTextAPIKeyChatTestResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`data: {"choices":[{"delta":{"content":"chat ok"},"finish_reason":"stop"}]}

data: [DONE]

`)),
	}
}

// adaptiveTextAPIKeyAnthropicTestResponse constructs a fresh successful
// Messages stream ending with message_stop. Its executor consumes and closes
// the body, so queued scenarios supply one response instance per expected
// request.
func adaptiveTextAPIKeyAnthropicTestResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`data: {"type":"content_block_delta","delta":{"text":"anthropic ok"}}

data: {"type":"message_stop"}

`)),
	}
}

// adaptiveTextAPIKeyResponsesTestResponse constructs a fresh successful
// Responses stream ending with response.completed. The executor consumes and
// closes its body, so callers allocate another response for any later probe
// request.
func adaptiveTextAPIKeyResponsesTestResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(`data: {"type":"response.output_text.delta","delta":"responses ok"}

data: {"type":"response.completed"}

`)),
	}
}

// GetByID returns a shallow copy of the configured nonnil provider regardless
// of requested ID. Credential and Extra maps remain borrowed, and this serial
// fixture captures protocol dispatch rather than storage lookup semantics.
func (r *textAPIKeyTestRepo) GetByID(context.Context, int64) (*providercore.Record, error) {
	copy := *r.provider
	return &copy, nil
}

// Do delegates protocol-request capture to the TLS-capable fixture path with no
// profile. The request needs a readable body, and returned response body
// ownership follows that controlled capture operation.
func (h *textAPIKeyTestHTTP) Do(req *http.Request, proxyURL string, providerID int64, concurrency int) (*http.Response, error) {
	return h.DoWithTLS(req, proxyURL, providerID, concurrency, nil)
}

// DoWithTLS consumes and captures the request body and a cloned request before
// returning a fixed 400. Body-read failure is ignored, and the nonnil request
// body is required by this serial capture fixture.
func (h *textAPIKeyTestHTTP) DoWithTLS(
	req *http.Request,
	_ string,
	_ int64,
	_ int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	// Request capture intentionally keeps bytes returned with a read failure.
	// The fixed 400 still stops dispatch, so this fixture does not test body-read error handling.
	payload, _ := io.ReadAll(req.Body)
	h.body = string(payload)
	h.request = req.Clone(req.Context())
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":"stop after request capture"}`)),
	}, nil
}

// SetRateLimited records one serial logical write and its selected reset
// timestamp. It ignores provider ID and context, allowing Grok error tests to
// inspect recovery timing without actual rate-limit storage.
func (r *grokProviderTestRateLimitRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.resetAt = resetAt
	return nil
}

// GetByID clones a matching fixture provider and returns a formatted error for
// missing IDs. CloneRecord copies its supported maps and slices, while
// unsupported mutable credential values can remain shared.
func (s *grokTestStoreFixture) GetByID(_ context.Context, id int64) (*providercore.Record, error) {
	value, ok := s.providersByID[id]
	if !ok {
		return nil, fmt.Errorf("provider %d missing", id)
	}
	return providercore.CloneRecord(value), nil
}

// UpdateExtra accepts the unused Grok fixture snapshot write and records no
// state.
func (*grokTestStoreFixture) UpdateExtra(context.Context, int64, map[string]any) error { return nil }

// SetRateLimited accepts the unused Grok fixture limit write and records no
// state.
func (*grokTestStoreFixture) SetRateLimited(context.Context, int64, time.Time) error { return nil }

// SetTempUnschedulable accepts the unused Grok fixture temporary-block write
// and records no state.
func (*grokTestStoreFixture) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}

// Do captures the borrowed request and consumed body bytes before returning its
// configured response. Body-read errors propagate, and each fixture response
// body is consumed by its production executor caller.
func (u *grokTestTransportFixture) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.lastReq = req
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.lastBody = body
	return u.resp, nil
}

// LoadTestTarget returns its configured borrowed target for every fixture
// request. Selection is controlled by the test, allowing the shared service to
// exercise event preparation without provider-store dispatch.
func (l grokTestTargetLoader) LoadTestTarget(context.Context, providercore.TestRequest) (providercore.TestTarget, error) {
	return l.target, nil
}

// executeGrokProviderTest runs a fixed Grok target through the shared test
// service and HTTP sink. Missing lookup dependencies use the offline catalogue,
// and the first optional kind selects explicit execution when the scenario needs it.
func executeGrokProviderTest(t *testing.T, executor *provideradapter.GrokProviderTest, value *providercore.Record, recorder *httptest.ResponseRecorder, model, prompt, mode string, types ...string) error {
	t.Helper()
	if executor.Attributes == nil {
		executor.Attributes = probeModelAttributes
	}
	core := providercore.NewTestService(grokTestTargetLoader{target: executor.Target(value)}, providercore.TestOptions{Now: time.Now, Error: func(string) {}, WriteError: func(error) {}})
	request := providercore.TestRequest{ProviderID: value.ID, Model: model, Prompt: prompt, Mode: mode}
	if len(types) > 0 {
		request.Type = &types[0]
	}
	return core.Test(t.Context(), request, NewTestEventSink(recorder))
}

// healthyGrokOAuthGatewayTestProvider constructs active OAuth credentials with
// whole-second expiry text roughly two refresh-skew windows ahead. The
// controlled probes therefore start beyond the ordinary refresh window while
// retaining the default CLI root and supplied access token.
func healthyGrokOAuthGatewayTestProvider(id int64, token string) *providercore.Record {
	return &providercore.Record{
		ID:          id,
		Name:        "grok",
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  token,
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(2 * providercore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		},
	}
}

// UpdateExtra accepts the Grok failure fixture's unused snapshot write and
// records no state.
func (f *grokTestFailureStoreFixture) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}

// SetRateLimited records serial provider ID and reset observations for a failed
// Grok request. It returns success without mutating provider storage, leaving
// assertions focused on health dispatch and selected recovery deadline.
func (f *grokTestFailureStoreFixture) SetRateLimited(_ context.Context, id int64, reset time.Time) error {
	f.rateLimitedCalls++
	f.lastRateLimitedID = id
	f.lastRateLimitResetAt = reset
	return nil
}

// SetTempUnschedulable counts the failure fixture's logical temporary-block
// write and records no deadline.
func (f *grokTestFailureStoreFixture) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	f.tempUnschedCalls++
	return nil
}

// executeGrokProbe creates a derived TestRun using the fixture request context
// and headers. It executes an explicit text test, reports the output-aware result
// and cancels its derived context before returning.
func executeGrokProbe(t *testing.T, executor *provideradapter.GrokProviderTest, output *openAIProbeOutput, value *providercore.Record, model string) error {
	t.Helper()
	run := provideradapter.NewTestRun(output.Request.Context(), output.Request.Header, NewTestEventSink(output.recorder))
	// Each helper owns the derived TestRun context. Release that cancellation
	// ownership after execution so the fixture request and output writer stay bounded to the call.
	defer run.Cancel()
	return run.Result(executor.Execute(run, value, model, "", providercore.ProviderTestTypeText))
}

// newProbeAgentKey generates local Ed25519 signing material with fixed runtime
// and task IDs. It returns both the signing key and base64 PKCS8 private-key
// text for controlled Agent Identity preparation.
func newProbeAgentKey(t *testing.T) (openai.AgentIdentityKey, string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(private)
	require.NoError(t, err)
	return openai.AgentIdentityKey{RuntimeID: "runtime-test", TaskID: "task-test", PrivateKey: private}, base64.StdEncoding.EncodeToString(der)
}

// GetByID clones the configured Agent Identity provider regardless of supplied
// ID. Supported credential containers become copied values, while unsupported
// mutable members remain subject to CloneRecord's sharing rules.
func (r *probeAgentStore) GetByID(context.Context, int64) (*providercore.Record, error) {
	return providercore.CloneRecord(r.provider), nil
}

// UpdateCredentials replaces the configured provider's credential map with the
// supplied borrowed map. This serial fixture allows task-registration helpers
// to update their observed record and capture later credential use.
func (r *probeAgentStore) UpdateCredentials(_ context.Context, _ int64, credentials map[string]any) error {
	r.provider.Credentials = credentials
	return nil
}

// Update rejects an unexpected full-record write from the Agent Identity
// fixture path.
func (*probeAgentStore) Update(context.Context, *providercore.Record) error {
	return fmt.Errorf("unexpected full provider update")
}

// UpdateExtra accepts the Agent Identity fixture's unused Extra write and
// records no state.
func (*probeAgentStore) UpdateExtra(context.Context, int64, map[string]any) error { return nil }

// SetError counts a serial logical authentication-error write without storing
// its message or identifier.
func (r *probeAgentStore) SetError(context.Context, int64, string) error {
	r.setErrorCalls++
	return nil
}

// Invalidate records the supplied provider identifier in a serial fixture
// observation slice.
func (r *probeAgentInvalidations) Invalidate(id int64) { r.providerIDs = append(r.providerIDs, id) }

// probeAgentTasks constructs task coordination using fixture rereads and
// production registration/persistence helpers. An optional invalidator records
// accepted credential maintenance, and the supplied endpoint selects the local
// registration server for recovery scenarios.
func probeAgentTasks(store *probeAgentStore, endpoint string, invalidator *probeAgentInvalidations) *provideradapter.ProbeTasks {
	options := providercore.OpenAITaskOptions{
		Read: store.GetByID,
		Register: func(ctx context.Context, value *providercore.Record) (string, error) {
			return provideradapter.RegisterAgentIdentityTask(ctx, value, endpoint)
		},
		Persist: func(ctx context.Context, value *providercore.Record, credentials map[string]any) error {
			_, err := providercore.PersistCredentials(ctx, store, value, credentials, nil)
			return err
		},
	}
	if invalidator != nil {
		options.Invalidate = invalidator.Invalidate
	}
	return &provideradapter.ProbeTasks{Coordinator: &providercore.OpenAITaskCoordinator{}, Options: options}
}

// executeOpenAIProbeRequestType passes explicit model, prompt, kind, mode and
// protocol through the shared test service. It uses the fixture HTTP context
// and sink, preserving per-test selection separate from source provider
// configuration.
func executeOpenAIProbeRequestType(t *testing.T, executor *provideradapter.OpenAIProviderTest, output *openAIProbeOutput, id int64, model, prompt, kind, mode, protocol string) error {
	t.Helper()
	return openAIProbeCore(executor).Test(output.Request.Context(), providercore.TestRequest{ProviderID: id, Model: model, Prompt: prompt, Mode: mode, Type: &kind, Protocol: protocol}, NewTestEventSink(output.recorder))
}

// Do rejects an unexpected non-TLS transport call in this ordered-response
// fixture.
func (u *queuedHTTPUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

// DoWithTLS records the borrowed request and profile-presence flag before
// consuming one queued response. An exhausted queue returns an error; callers
// supply responses with fresh bodies for every expected request.
func (u *queuedHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	u.tlsFlags = append(u.tlsFlags, profile != nil)
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

// newJSONResponse constructs a fresh response with the selected status, empty
// headers and owned body reader. Its consumer closes the body after parsing,
// and each call creates independent fixture response state.
func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// newTestContext returns a request and recorder wrapper for synchronous
// provider-test event capture. The wrapper and separately returned recorder
// share one output object, allowing execution helpers and assertions to observe
// the same stream.
func newTestContext() (*openAIProbeOutput, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	return &openAIProbeOutput{Request: httptest.NewRequest(http.MethodPost, "/test", nil), recorder: recorder}, recorder
}

// UpdateExtra retains the borrowed patch and optionally publishes a top-level
// copied observation to the fixture channel. Nested values remain shared, and a
// configured channel requires capacity or a receiver because publication is
// blocking.
func (r *openAIProbeStore) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updatedExtra = updates
	if r.updateExtraCalls != nil {
		copy := make(map[string]any, len(updates))
		maps.Copy(copy, updates)
		// Publish a copied top-level observation after retaining the borrowed update.
		// Tests supply channel capacity because this fixture send does not select cancellation.
		r.updateExtraCalls <- copy
	}
	return nil
}

// BulkUpdate copies selected provider IDs and borrows the supplied payload for
// serial observation. It reports all IDs updated without changing fixture rows,
// allowing plan-sync assertions to inspect dispatched credential intent.
func (r *openAIProbeStore) BulkUpdate(_ context.Context, ids []int64, updates providercore.ProviderBulkUpdate) (int64, error) {
	r.bulkUpdatedIDs = append([]int64(nil), ids...)
	r.bulkUpdatedPayload = updates
	return int64(len(ids)), nil
}

// SetRateLimited records the selected provider ID and an allocated copy of its
// reset timestamp. This serial substitute records health dispatch without
// writing actual provider or cache state.
func (r *openAIProbeStore) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedID = id
	r.rateLimitedAt = &resetAt
	return nil
}

// ClearError records the selected provider identifier for a serial logical
// health-clear invocation.
func (r *openAIProbeStore) ClearError(_ context.Context, id int64) error {
	r.clearedErrorID = id
	return nil
}

// SetError records the selected provider identifier and authentication failure
// message for serial assertions.
func (r *openAIProbeStore) SetError(_ context.Context, id int64, errorMsg string) error {
	r.setErrorID = id
	r.setErrorMsg = errorMsg
	return nil
}

// GetByID clones a configured record by its numeric fixture identifier. Missing
// IDs return a formatted error; supported provider containers are copied while
// unsupported mutable map values can remain shared.
func (r openAIProbeRecords) GetByID(_ context.Context, id int64) (*providercore.Record, error) {
	value, ok := r.providersByID[id]
	if !ok {
		return nil, fmt.Errorf("provider %d missing", id)
	}
	return providercore.CloneRecord(value), nil
}

// configureOpenAIProbe fills missing preparation, routing and TLS callbacks
// from a production probe policy. Existing callbacks remain selected, and
// available store lookup becomes the policy's credential-parent reader for this
// executor.
func configureOpenAIProbe(executor *provideradapter.OpenAIProviderTest) {
	policy := &provideradapter.OpenAIProbePolicy{Available: true, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent}
	if executor.Store != nil {
		policy.Read = executor.Store.GetByID
	}
	if executor.Prepare == nil {
		executor.Prepare = policy.Prepare
	}
	if executor.ApplyRouting == nil {
		executor.ApplyRouting = policy.ApplyTestRouting
	}
	if executor.ResolveTLS == nil {
		executor.ResolveTLS = policy.ResolveTestTLS
	}
}

// executeOpenAIProbe applies fixture policy defaults and runs OpenAI execution
// with the supplied record and choices. The derived TestRun uses HTTP context
// and headers, returns output-aware failure and is canceled before returning.
func executeOpenAIProbe(t *testing.T, executor *provideradapter.OpenAIProviderTest, output *openAIProbeOutput, value *providercore.Record, model, prompt, mode string, types ...string) error {
	t.Helper()
	configureOpenAIProbe(executor)
	run := provideradapter.NewTestRun(output.Request.Context(), output.Request.Header, NewTestEventSink(output.recorder))
	// Each helper owns the derived TestRun context. Release that cancellation
	// ownership after execution so the fixture request and output writer stay bounded to the call.
	defer run.Cancel()
	return run.Result(executor.Execute(run, value, model, prompt, mode, types...))
}

// executeOpenAIImageAPIKeyProbe applies policy defaults and invokes the API-key
// image branch with the supplied execution context. TestRun derives from the
// fixture HTTP request, and its deferred cancellation runs before this helper
// returns the output-aware result.
func executeOpenAIImageAPIKeyProbe(t *testing.T, executor *provideradapter.OpenAIProviderTest, output *openAIProbeOutput, ctx context.Context, value *providercore.Record, model, prompt string) error {
	t.Helper()
	configureOpenAIProbe(executor)
	run := provideradapter.NewTestRun(output.Request.Context(), output.Request.Header, NewTestEventSink(output.recorder))
	// Each helper owns the derived TestRun context. Release that cancellation
	// ownership after execution so the fixture request and output writer stay bounded to the call.
	defer run.Cancel()
	return run.Result(executor.ExecuteImageAPIKey(run, ctx, value, model, prompt))
}

// executeOpenAIImageOAuthProbe applies policy defaults and invokes the OAuth
// image branch with the supplied execution context. Its TestRun derives from
// fixture request headers and context, then releases derived cancellation
// ownership on return.
func executeOpenAIImageOAuthProbe(t *testing.T, executor *provideradapter.OpenAIProviderTest, output *openAIProbeOutput, ctx context.Context, value *providercore.Record, model, prompt string) error {
	t.Helper()
	configureOpenAIProbe(executor)
	run := provideradapter.NewTestRun(output.Request.Context(), output.Request.Header, NewTestEventSink(output.recorder))
	// Each helper owns the derived TestRun context. Release that cancellation
	// ownership after execution so the fixture request and output writer stay bounded to the call.
	defer run.Cancel()
	return run.Result(executor.ExecuteImageOAuth(run, ctx, value, model, prompt))
}

// openAIProbeCore connects provider reads and OpenAI/Kimi, Zhipu, and DeepSeek
// execution to one shared test service. A usable Store is required for request
// dispatch, and supplied policy callbacks are retained through
// configureOpenAIProbe.
func openAIProbeCore(executor *provideradapter.OpenAIProviderTest) *providercore.TestService {
	configureOpenAIProbe(executor)
	targets := &provideradapter.TestTargets{Read: executor.Store.GetByID, OpenAI: executor, Native: &provideradapter.NativeProtocolTest{Transport: executor.Transport, Store: executor.Store, ValidateURL: executor.ValidateURL, Responses: executor}}
	return providercore.NewTestService(targets, providercore.TestOptions{Now: time.Now, Error: func(string) {}, WriteError: func(error) {}})
}

// executeOpenAIProbeRequest passes model, prompt and mode through store-backed
// shared test dispatch. The fixture HTTP context and event sink carry execution
// and output, with kind omitted for compatibility selection.
func executeOpenAIProbeRequest(t *testing.T, executor *provideradapter.OpenAIProviderTest, output *openAIProbeOutput, id int64, model, prompt, mode string) error {
	t.Helper()
	return openAIProbeCore(executor).Test(output.Request.Context(), providercore.TestRequest{ProviderID: id, Model: model, Prompt: prompt, Mode: mode}, NewTestEventSink(output.recorder))
}

// DoWithTLS captures request, TLS profile and body observations before
// returning a selected response or failure. Body bytes are copied into capture
// so assertions can retain them. Reading and closing errors return before
// response selection, while request capture already exists.
//
// A successfully captured body is replaced with a fresh reader because later
// test assertions read the same request again. Configured transport error wins
// over queued responses; otherwise one queued response is consumed before the
// fixed fallback pointer is used. Request/profile pointers remain borrowed, and
// response bodies remain consumable caller-owned fixture resources.
func (f *openAIProbeTransport) DoWithTLS(req *http.Request, _ string, _ int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	f.lastReq = req
	f.requests = append(f.requests, req)
	f.lastTLSProfile = profile
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		f.lastBody = body
		f.bodies = append(f.bodies, append([]byte(nil), body...))
		// Close the consumed reader before installing a replay reader. Captured
		// bytes remain available if closing fails, while response selection is skipped.
		if err := req.Body.Close(); err != nil {
			return nil, err
		}
		// Later assertions read this same request body. Replay its captured bytes
		// so transport observation does not leave those consumers with an exhausted reader.
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	if f.err != nil {
		return nil, f.err
	}
	// Queued responses model retries with distinct bodies; fixed response supports
	// single-request scenarios. Consume the ordered queue before selecting that configured fixed result.
	if len(f.responses) > 0 {
		response := f.responses[0]
		f.responses = f.responses[1:]
		return response, nil
	}
	return f.resp, nil
}

// newTextAPIKeyProtocolTestCore binds supplied record lookup to Kimi, Zhipu,
// and DeepSeek dispatch with the production URL policy and OpenAI Responses
// executor. Empty observation callbacks keep tests focused on protocol requests
// and event results from the controlled transport.
func newTextAPIKeyProtocolTestCore(read func(context.Context, int64) (*providercore.Record, error), transport provideradapter.QoderTransport) *providercore.TestService {
	policy := egress.OperatorURLPolicy{}
	openaiExecutor := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Transport: transport, ValidateURL: policy.Validate}
	configureOpenAIProbe(openaiExecutor)
	targets := &provideradapter.TestTargets{Read: read, Native: &provideradapter.NativeProtocolTest{Transport: transport, ValidateURL: policy.Validate, Responses: openaiExecutor}}
	return providercore.NewTestService(targets, providercore.TestOptions{Now: time.Now, Error: func(string) {}, WriteError: func(error) {}})
}

// successfulOpenAIAutomaticProbeResponse constructs a fresh successful stream
// with one completed Responses event. Its executor consumes and closes the
// body; callers supply another response instance for a later automatic probe.
func successfulOpenAIAutomaticProbeResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed"}

`)),
	}
}

// newOpenAIAutomaticProbeTestService warms local TLS-profile and optional
// router caches for controlled background probes. Start loads the fixture
// repositories synchronously. The services have nil cache backends, so their
// registered Stop callbacks have no subscriber to stop or join. Record pointers
// borrow the supplied slice entries; keep that population stable during probes.
//
// The production probe policy selects client identity, routing and TLS using
// those caches, while the supplied transport controls response bytes. API-key
// URL validation requires the optional URL policy when reached. Store-backed
// shared dispatch then exercises automatic-policy behavior with these fixture
// dependencies rather than external network or profile storage.
func newOpenAIAutomaticProbeTestService(t *testing.T, values []providercore.Record, transport *openAIProbeTransport, router *egress.TLSFingerprintRouter, profiles map[int64]*egress.TLSFingerprintProfile, urlPolicy *egress.OperatorURLPolicy) *providercore.TestService {
	t.Helper()
	profileStore := &automaticProbeProfileStore{}
	for _, value := range profiles {
		profileStore.values = append(profileStore.values, value)
	}
	profileService := egressadapter.NewTLSProfiles(egress.NewTLSFingerprintProfileService(profileStore, nil))
	// Automatic policy reads profiles loaded during Start. Register the lifecycle
	// stop callback even though this fixture's nil cache backend has no update subscriber.
	profileService.Start()
	t.Cleanup(profileService.Stop)
	records := openAIProbeRecords{providersByID: make(map[int64]*providercore.Record, len(values))}
	for i := range values {
		records.providersByID[values[i].ID] = &values[i]
	}
	store := &openAIProbeStore{openAIProbeRecords: records}
	policy := &provideradapter.OpenAIProbePolicy{Available: true, Read: store.GetByID, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Profiles: profileService, ManualProfiles: profileService}
	if router != nil {
		routers := egress.NewTLSFingerprintRouterService(&automaticProbeRouterStore{values: []*egress.TLSFingerprintRouter{router}}, nil)
		// Router matching uses routes loaded during Start. Register Stop with the
		// test lifecycle; this nil cache backend supplies no subscription or work to join.
		routers.Start()
		t.Cleanup(routers.Stop)
		policy.Routers = routers
	}
	executor := &provideradapter.OpenAIProviderTest{Attributes: probeModelAttributes, Store: store, Transport: transport, Prepare: policy.Prepare, ApplyRouting: policy.ApplyTestRouting, ResolveTLS: policy.ResolveTestTLS, ValidateURL: func(raw string) (string, error) {
		if urlPolicy == nil {
			return "", errors.New("config is not available")
		}
		return urlPolicy.Validate(raw)
	}}
	return openAIProbeCore(executor)
}

// List returns the borrowed configured TLS-profile slice for local cache
// initialization. Callers treat its entries as stable after service start,
// because the fixture supplies no synchronization for later mutation.
func (s *automaticProbeProfileStore) List(context.Context) ([]*egress.TLSFingerprintProfile, error) {
	return s.values, nil
}

// List returns the borrowed configured TLS-router slice for local cache
// initialization. Callers configure entries before service start and keep them
// stable while the background test policy reads its cache.
func (s *automaticProbeRouterStore) List(context.Context) ([]*egress.TLSFingerprintRouter, error) {
	return s.values, nil
}

// GetSession returns its configured failure or borrowed Qoder session pointer.
// The production probe can fill user identity on that session, so serial
// fixture assertions inspect the same object after execution.
func (s *qoderProviderTestSessionProviderStub) GetSession(context.Context, *providercore.Record) (*qoder.SessionContext, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.session, nil
}

// Invalidate records the selected provider identifier in the Qoder session
// fixture's observation slice.
func (s *qoderProviderTestSessionProviderStub) Invalidate(providerID int64) {
	s.invalidated = append(s.invalidated, providerID)
}

// StreamRequestContext captures request and copied input bytes and borrows the
// supplied header map. A configured error ends the call; otherwise a fresh
// response body exposes the fixed wrapped-stream text for serial parsing.
func (s *qoderProviderTestClientStub) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, headers map[string]string) (*http.Response, error) {
	// The fixed method and URL are valid, and execution supplies a nonnil context.
	// Ignore request-construction error here; nil context would leave the captured request nil.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(bodyJSON)))
	s.request = req
	s.requests = append(s.requests, req)
	s.bodies = append(s.bodies, append([]byte(nil), bodyJSON...))
	s.headers = headers
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

// StreamRequestContextWithDoer records the same request, body and header
// observations before invoking the supplied doer. Configured errors bypass that
// callback; the callback is required for admitted requests and its response
// passes through to the probe caller for body consumption and closing.
func (s *qoderProviderTestClientStub) StreamRequestContextWithDoer(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, headers map[string]string, doer qoder.RequestDoer) (*http.Response, error) {
	// The fixed method and URL are valid, and execution supplies a nonnil context.
	// Ignore request-construction error here; nil context would leave the captured request nil.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(bodyJSON)))
	s.request = req
	s.requests = append(s.requests, req)
	s.bodies = append(s.bodies, append([]byte(nil), bodyJSON...))
	s.headers = headers
	if s.err != nil {
		return nil, s.err
	}
	return doer(req)
}

// GetUserInfo records supplied token text and returns configured failure or
// fixed user information. This serial fixture observes user-info preparation
// while leaving credential authentication and real transport to production
// adapter tests.
func (s *qoderProviderTestOAuthClientStub) GetUserInfo(_ context.Context, token string) (*qoder.UserInfo, error) {
	s.token = token
	if s.err != nil {
		return nil, s.err
	}
	return &qoder.UserInfo{ID: "user-1", Name: "Qoder User"}, nil
}

// Do delegates fixed Qoder endpoint response handling with no TLS profile
// selected. The serial fixture records transport choices and returns a fresh
// body that the production executor consumes and closes.
func (u *qoderHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, providerID, providerConcurrency, nil)
}

// DoWithTLS records borrowed request and transport selections for serial Qoder
// assertions. User-info GET requests select their configured body/status or
// default successful user object; other requests receive the configured wrapped
// stream body. Each result owns a fresh body for the probe caller to consume
// and close after parsing.
func (u *qoderHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.proxyURL = proxyURL
	u.providerID = providerID
	u.providerConcurrency = providerConcurrency
	u.profileSet = profile != nil
	u.requests = append(u.requests, req)
	body := u.body
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, qoder.UserInfoPath) {
		body = u.userInfoBody
		// User-info preparation can use a standard successful fixture identity when
		// no custom payload was configured, while stream response text remains independently selected.
		if body == "" {
			body = `{"id":"user-1","name":"Qoder User"}`
		}
	}
	statusCode := http.StatusOK
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, qoder.UserInfoPath) && u.userInfoStatusCode != 0 {
		statusCode = u.userInfoStatusCode
	}
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

// newQoderProviderTestContext returns a background context wrapper and recorder
// for synchronous Qoder tests. Both outputs share the same response recorder,
// keeping the event sink and fixture assertions on one stream.
func newQoderProviderTestContext() (*qoderTestOutputFixture, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	return &qoderTestOutputFixture{Context: context.Background(), recorder: recorder}, recorder
}

// LoadTestTarget returns the configured borrowed Qoder target for every fixture
// request. The test controls that target selection so shared service
// preparation and output can be exercised without provider-store routing.
func (l qoderTargetFixture) LoadTestTarget(context.Context, providercore.TestRequest) (providercore.TestTarget, error) {
	return l.target, nil
}

// executeQoderTest binds the production model-rewrite helper and runs the
// selected Qoder target through shared service preparation. It uses the wrapper
// context and HTTP sink, retaining controlled fixture transport and event
// observations.
func executeQoderTest(t *testing.T, output *qoderTestOutputFixture, executor *provideradapter.QoderProviderTest, value *providercore.Record, model, prompt, mode string) error {
	t.Helper()
	attachQoderHTTPFixtureCatalog(value)
	core := providercore.NewTestService(qoderTargetFixture{target: executor.Target(value)}, providercore.TestOptions{
		Now:        time.Now,
		Error:      func(message string) { log.Printf("Provider test error: %s", message) },
		WriteError: func(err error) { log.Printf("failed to write SSE event: %v", err) },
	})
	return core.Test(output.Context, providercore.TestRequest{ProviderID: value.ID, Model: model, Prompt: prompt, Mode: mode}, NewTestEventSink(output.recorder))
}

// probeModelAttributes resolves fixture provider models against the embedded
// catalog. Probe tests keep authentication and transport stubs independent of
// capability data while unknown identities retain an absent output declaration.
func probeModelAttributes(_ context.Context, value *providercore.Record, _ int64, model, protocol string) (modelrules.Resolution, error) {
	return testkit.OfflineView().Resolve(value.ModelAttributeTarget(model, protocol), modelcatalog.Attributes{}), nil
}
