package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	catalogtestkit "github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Text endpoint rejection can occur before provider selection, after a
// credential refresh, or after upstream output has already reached the client.
// The tests distinguish those stages through scheduler counters, recorded
// provider calls, HTTP status, response frames, and completion writes instead
// of relying on an endpoint's final status alone.
//
// Cancellation fixtures publish refresh entry before the test cancels the
// request and waits for handler return. Retry fixtures keep provider identity
// with every observed call so same-provider retries remain distinguishable from
// switches. Mixed-platform cases use protocol-specific upstream bodies and a
// shared transport, then inspect both the client result and the recorded
// provider and settlement. Error-output cases separately exercise official
// terminal frames, heartbeat-only output, and sanitized client messages because
// those states permit different fallback responses.

// mockTempUnscheduler retains provider IDs and error pointers from
// temporary-unscheduling calls.
type mockTempUnscheduler struct {
	// calls retains the original failover-error pointers in invocation order;
	// serial tests inspect this slice after the handler has returned.
	calls []tempUnscheduleCall
}

// tempUnscheduleCall pairs a selected provider with its retained failover
// error.
type tempUnscheduleCall struct {
	providerID int64 // Selected provider record ID.
	// failoverErr is the original pointer supplied by failover handling, so
	// assertions inspect it after the call without concurrent error mutation.
	failoverErr *forwardcore.UpstreamFailoverError
}

// countingGatewaySchedulerCache counts snapshot reads while delegating their
// contents to fakeSchedulerCache.
type countingGatewaySchedulerCache struct {
	// fakeSchedulerCache supplies snapshot results, and snapshotCalls counts
	// every GetSnapshot invocation before delegation so cancellation tests can
	// detect selection reads independently of the returned provider data.
	*fakeSchedulerCache              // Delegated snapshot data fixture.
	snapshotCalls       atomic.Int64 // Snapshot read invocation count.
}

// cyberSessionBlockHandlerCacheStub holds one block flag and counts block
// checks. Tests access this mutable fixture serially; session ownership and
// provider lookups return fixed misses.
type cyberSessionBlockHandlerCacheStub struct {
	blocked   bool // Single session block flag.
	readCalls int  // Session block read count.
}

// openAIHTTPPassthroughFailoverUpstream records provider calls and returns
// scripted HTTP 502 responses.
type openAIHTTPPassthroughFailoverUpstream struct {
	// UpstreamTransport supplies unimplemented methods through embedding;
	// tests use the ordinary and fingerprinted execution methods overridden below.
	httpclient.
		UpstreamTransport
	// mu protects providerIDs appends and snapshots so transport calls and
	// observation can overlap without exposing a partially updated call sequence.
	mu          sync.Mutex // Provider call observation lock.
	providerIDs []int64    // Observed provider execution order.
}

// openAIHTTPPassthroughAuthFailoverUpstream distinguishes the healthy provider
// from scripted authentication failures.
type openAIHTTPPassthroughAuthFailoverUpstream struct {
	// UpstreamTransport supplies unimplemented methods through embedding;
	// tests use the ordinary and fingerprinted execution methods overridden below.
	httpclient.
		UpstreamTransport
	// mu protects providerIDs appends and snapshots so transport calls and
	// observation can overlap without exposing a partially updated call sequence.
	mu          sync.Mutex // Provider call observation lock.
	providerIDs []int64    // Observed provider execution order.
	// statusCode is returned for providers other than healthy ID 9911; tests
	// set it before transport use to script unauthorized or forbidden responses.
	statusCode int
}

// openAIHTTPPassthroughSSERateLimitUpstream records calls and emits rate-limit
// failures inside HTTP 200 streams.
type openAIHTTPPassthroughSSERateLimitUpstream struct {
	// UpstreamTransport supplies unimplemented methods through embedding;
	// tests use the ordinary and fingerprinted execution methods overridden below.
	httpclient.
		UpstreamTransport
	// mu protects providerIDs appends and snapshots so transport calls and
	// observation can overlap without exposing a partially updated call sequence.
	mu          sync.Mutex // Provider call observation lock.
	providerIDs []int64    // Observed provider execution order.
}

// openAIResponsesFailoverProviderRepo supplies platform-filtered provider
// values and ID rereads for failover tests.
type openAIResponsesFailoverProviderRepo struct {
	// ExecutionProviderStore supplies unimplemented methods through embedding;
	// tests use only the ID and platform reads overridden below.
	gatewayprovider.ExecutionProviderStore

	// providers supplies copied outer values to selection and rereads; nested
	// maps and pointers stay shared with these stored fixture records.
	providers []gatewayprovider.ExecutionProvider
}

// openAIResponsesFailoverCancelUpstream records calls and returns scripted HTTP
// 520 responses. Its optional first-call callback runs after the recording
// mutex is released to simulate request cancellation.
type openAIResponsesFailoverCancelUpstream struct {
	// UpstreamTransport supplies unimplemented methods through embedding;
	// tests use the ordinary and fingerprinted execution methods overridden below.
	httpclient.
		UpstreamTransport
	// mu protects providerIDs appends and snapshots so transport calls and
	// observation can overlap without exposing a partially updated call sequence.
	mu          sync.Mutex // Provider call observation lock.
	providerIDs []int64    // Observed provider execution order.
	// onFirstDo runs once for the first recorded call, after mu is released;
	// set this optional callback before transport use to avoid concurrent replacement.
	onFirstDo func()
}

// mixedHTTPNoSearch supplies an absent searcher for mixed-platform text
// requests.
type mixedHTTPNoSearch struct{}

// newEmptyGenericSelectionFixture constructs generic selection with default
// options and absent data sources.
func newEmptyGenericSelectionFixture() *selection.Generic {
	return selection.NewGeneric(selection.GenericDependencies{}, selection.DefaultOptions())
}

// TestChatCompletionsChecksFinalOutput validates each selected provider target
// before forwarding. Image-only declarations reject Chat, while a provider
// mapping to a text target keeps the image-named client alias executable.
func TestChatCompletionsChecksFinalOutput(t *testing.T) {
	for _, model := range []string{"gpt-image-1", "gpt-image-1.5", "gpt-image-2"} {
		t.Run(model, func(t *testing.T) {
			ctx := routing.WithModelAttributes(context.Background(), &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalogtestkit.OfflineView}})
			target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Platform: "openai", Type: "apikey"}}
			err := gatewayhttp.CheckTextModelPricing(ctx, nil, nil, target, model, model, nil, capability.ProtocolOpenAIChatCompletions)
			if model == "gpt-image-1.5" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "MODEL_CAPABILITY_UNSUPPORTED")
			}
			target.Record.Credentials = map[string]any{"model_mapping": map[string]any{model: "gpt-4o"}}
			err = gatewayhttp.CheckTextModelPricing(ctx, nil, nil, target, model, model, nil, capability.ProtocolOpenAIChatCompletions)
			require.NoError(t, err)
		})
	}
}

// TestChatCompletionsChecksGroupMappedImageOutput supplies a group alias whose
// selected upstream target produces only images. The model rejection occurs
// after both mapping stages, including when the client alias has no image marker.
func TestChatCompletionsChecksGroupMappedImageOutput(t *testing.T) {
	ctx := routing.WithModelAttributes(context.Background(), &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalogtestkit.OfflineView}})
	target := &gatewayprovider.ExecutionProvider{Record: providercore.Record{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_mapping": map[string]any{"group-alias": "gpt-image-2"}}}}
	err := gatewayhttp.CheckTextModelPricing(ctx, nil, nil, target, "draw", "group-alias", nil, capability.ProtocolOpenAIChatCompletions)
	require.ErrorContains(t, err, "MODEL_CAPABILITY_UNSUPPORTED")
}

// TempUnscheduleRetryableError appends the provider ID and original error
// pointer for later assertions. This fixture performs no provider mutation and
// expects callers to serialize access to its observation slice.
func (m *mockTempUnscheduler) TempUnscheduleRetryableError(_ context.Context, providerID int64, failoverErr *forwardcore.UpstreamFailoverError) {
	m.calls = append(m.calls, tempUnscheduleCall{providerID: providerID, failoverErr: failoverErr})
}

// GetSnapshot increments its atomic read counter before delegating to
// fakeSchedulerCache. The delegated snapshot, cache-hit flag, and error pass
// through so tests can observe selection attempts without replacing snapshot
// behavior.
func (c *countingGatewaySchedulerCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]scheduler.SnapshotProvider, bool, error) {
	c.snapshotCalls.Add(1)
	return c.fakeSchedulerCache.GetSnapshot(ctx, bucket)
}

// TestGatewayHandlerPreCancelledCompatibleRequestsDoNotSelectProvider invokes
// compatible Responses and Chat handlers with already-cancelled contexts. It
// checks zero scheduler snapshot reads and an absent selected-provider marker.
func TestGatewayHandlerPreCancelledCompatibleRequestsDoNotSelectProvider(t *testing.T) {
	groupID := int64(9100)
	group := &routing.Group{ID: groupID, Hydrated: true, Status: billing.StatusActive}
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9101, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeAPIKey,
			Status: billing.StatusActive, Schedulable: true, Concurrency: 1,
			ProviderGroups: []providercore.GroupMembership{{ProviderID: 9101, GroupID: groupID}},
		},
	}
	schedulerCache := &countingGatewaySchedulerCache{fakeSchedulerCache: &fakeSchedulerCache{providers: []*gatewayprovider.ExecutionProvider{provider}}}
	schedulerSnapshot := scheduler.NewSnapshotService(schedulerCache, nil, nil, nil, nil, scheduler.SnapshotBindings{})
	gatewayService, gatewayServiceChoices, messages := newGenericExecutionAndSelectionFixture(
		nil, &fakeGroupRepo{group: group}, nil, nil, nil,
		schedulerSnapshot, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, responseHeaderFilterForTest(nil),
	)
	gatewayService.Recorder = newHTTPCompletionFixture(nil, nil,
		nil, nil, nil, nil, nil, false)

	cfg := &config.Config{}
	billingCacheService := newBillingEligibilityFixture(cfg)
	billingCacheService.Start()
	t.Cleanup(billingCacheService.Stop)
	h := newMessageEndpointsFixture(gatewayService, messages, newFundingAdmissionFixture(billingCacheService, cfg), gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&fakeConcurrencyCache{}, scheduler.Diagnostics{
		Logf:  logging.LegacyPrintf,
		Event: logging.Event,
	},
	), gatewayhttp.SSEPingFormatClaude, 0), gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(cfg).MaxBodyBytes, MaxSwitches: 1, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(nil,
		nil, nil), gatewayServiceChoices,
	)
	apiKey := &apikey.APIKey{
		ID: 9102, UserID: 9103, GroupID: &groupID, Group: group, Status: billing.StatusActive,
		User: &identity.User{ID: 9103, Concurrency: 10, Balance: 100},
	}

	tests := []struct {
		name string             // Compatible request case name.
		path string             // Client endpoint request path.
		body string             // JSON client request body.
		call func(*gin.Context) // Endpoint handler under test.
	}{
		{
			name: "responses", path: "/v1/responses", body: `{"model":"claude-test","input":"hello","stream":false}`,
			call: h.Responses,
		},
		{
			name: "chat completions", path: "/v1/chat/completions", body: `{"model":"claude-test","messages":[{"role":"user","content":"hello"}],"stream":false}`,
			call: h.ChatCompletions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedulerCache.snapshotCalls.Store(0)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			ctx = requeststate.WithGroup(ctx, group)
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			c.Request = req
			c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

			tt.call(c)

			require.Zero(t, schedulerCache.snapshotCalls.Load(), "a cancelled request must stop before the provider selector")
			_, selected := c.Get(gatewayhttp.OpsProviderIDKey)
			require.False(t, selected)
		})
	}
}

// TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedJSON413 presents a
// body-limit error carrying secret upstream text. It checks HTTP 413, the safe
// invalid-request message, and absence of the secret.
func TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedJSON413(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{}).openAIAttemptSupport().HandleFailoverExhausted(c, bodyLimitFailoverTestError(), false)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	errBody, ok := envelope["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", errBody["type"])
	require.Equal(t, "Request payload is too large", errBody["message"])
	require.NotContains(t, rec.Body.String(), "must-not-leak")
}

// TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedResponsesSSE presents a
// body-limit failure in streaming mode. It checks a Responses failed event with
// safe invalid-request details and no upstream secret.
func TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedResponsesSSE(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{}).openAIAttemptSupport().HandleFailoverExhausted(c, bodyLimitFailoverTestError(), true)

	body := rec.Body.String()
	require.True(t, strings.HasPrefix(body, "event: response.failed\n"))
	require.Contains(t, body, `"code":"invalid_request"`)
	require.Contains(t, body, `"message":"Request payload is too large"`)
	require.NotContains(t, body, "must-not-leak")
}

// TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedAnthropicError renders
// the same body-limit failure as Anthropic JSON and SSE. It checks each
// protocol's safe invalid-request message and absence of secret upstream text.
func TestOpenAIBodyLimitFailoverExhausted_ReturnsRedactedAnthropicError(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(nil))

		newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{}).openAIAttemptSupport().HandleAnthropicFailoverExhausted(c, bodyLimitFailoverTestError(), false)

		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		var envelope map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
		errBody, ok := envelope["error"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "invalid_request_error", errBody["type"])
		require.Equal(t, "Request payload is too large", errBody["message"])
		require.NotContains(t, rec.Body.String(), "must-not-leak")
	})

	t.Run("sse", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(nil))

		newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{}).openAIAttemptSupport().HandleAnthropicFailoverExhausted(c, bodyLimitFailoverTestError(), true)

		body := rec.Body.String()
		require.True(t, strings.HasPrefix(body, "event: error\n"))
		require.Contains(t, body, `"type":"invalid_request_error"`)
		require.Contains(t, body, `"message":"Request payload is too large"`)
		require.NotContains(t, body, "must-not-leak")
	})
}

// bodyLimitFailoverTestError creates a retryable provider body-limit failure
// with distinct upstream and client messages. The secret-bearing response body
// lets tests detect accidental exposure through JSON or stream error output.
func bodyLimitFailoverTestError() *forwardcore.UpstreamFailoverError {
	return &forwardcore.UpstreamFailoverError{
		StatusCode:         http.StatusRequestEntityTooLarge,
		ResponseBody:       []byte(`{"error":{"message":"proxy limit secret=must-not-leak"}}`),
		Scope:              forwardcore.GatewayFailureScopeProvider,
		Reason:             forwardcore.GatewayFailureReason("openai_request_body_too_large"),
		NextProviderAction: forwardcore.NextProviderRetry,
		ClientStatusCode:   http.StatusRequestEntityTooLarge,
		ClientMessage:      "Request payload is too large",
	}
}

// TestOpenAIResponses_CompactUnauthorizedLogsFailed submits an unauthenticated
// compact request with a Codex User-Agent. It checks HTTP 401 and a warning log
// containing the response status and compact path.
func TestOpenAIResponses_CompactUnauthorizedLogsFailed(t *testing.T) {
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"gpt-5.3-codex"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.125.0")

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.Responses(c)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.True(t, logSink.ContainsMessageAtLevel("codex.remote_compact.failed", "warn"))
	require.True(t, logSink.ContainsFieldValue("status_code", "401"))
	require.True(t, logSink.ContainsFieldValue("path", "/v1/responses/compact"))
}

// TestResponsesCredentialFailoverLoop exercises revoked, missing, and unusable
// Grok credentials together with refresh and post-forward cancellation. It
// checks safe failures or healthy output, provider mutations, selection counts,
// upstream calls, and scheduler feedback for the applicable cases.
func TestResponsesCredentialFailoverLoop(t *testing.T) {
	t.Run("revoked provider selects healthy provider", func(t *testing.T) {
		h, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "revoked")
		defer cleanup()
		_ = h

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), "resp_healthy")
		require.Equal(t, []int64{801}, repo.errorIDs())
		require.Equal(t, []int64{802}, upstream.providerHits())
		requestURLs, authorization := upstream.requests()
		require.Equal(t, []string{xai.DefaultCLIBaseURL + "/responses"}, requestURLs)
		require.Equal(t, []string{"Bearer healthy-access"}, authorization)
	})

	t.Run("provider configuration stops before healthy provider", func(t *testing.T) {
		h, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "provider")
		defer cleanup()

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
		require.Empty(t, repo.errorIDs())
		require.Empty(t, upstream.providerHits())
		require.Equal(t, 1, repo.selectorCalls())
		require.Zero(t, h.Input.Choices.SnapshotOpenAIProviderSchedulerMetrics().RuntimeStatsProviderCount,
			"provider-scoped auth failure must not penalize the selected provider")
	})

	t.Run("parent cancellation stops before healthy provider", func(t *testing.T) {
		_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "cancel")
		defer cleanup()

		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		done := make(chan struct{})
		// Run ingress separately so refresh can block while this test cancels
		// its request; close done after handler return before reading observations.
		go func() {
			defer close(done)
			router.ServeHTTP(recorder, req)
		}()

		// Cancel after refresh announces entry, then join the handler so the
		// mutation and upstream assertions observe the completed cancellation path.
		select {
		case <-time.After(2 * time.Second):
			t.Fatal("credential refresh did not start")
		case <-findHandlerRefresherStarted(router):
			cancel()
		}
		select {
		case <-time.After(2 * time.Second):
			t.Fatal("handler did not stop after cancellation")
		case <-done:
		}

		require.Empty(t, repo.errorIDs())
		require.Empty(t, upstream.providerHits())
	})

	t.Run("post-mapping cancellation stops before scheduler mutation or reselection", func(t *testing.T) {
		h, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "postmap_cancel")
		defer cleanup()
		ctx, cancel := context.WithCancel(context.Background())
		// Install the payment-failure cancellation callback under the transport
		// lock before forwarding so its failure snapshot contains both controls.
		upstream.mu.Lock()
		upstream.failProviderID = 801
		upstream.cancelRequest = cancel
		upstream.mu.Unlock()

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)

		require.Equal(t, []int64{801}, upstream.providerHits())
		require.Empty(t, repo.errorIDs())
		require.Equal(t, 1, repo.selectorCalls())
		require.Zero(t, h.Input.Choices.SnapshotOpenAIProviderSchedulerMetrics().RuntimeStatsProviderCount)
	})

	t.Run("pre-cancelled request never invokes a provider selector", func(t *testing.T) {
		tests := []struct {
			name   string // Cancelled endpoint case name.
			method string // Client HTTP request method.
			path   string // Client endpoint request path.
			body   string // JSON client request body.
		}{
			{name: "responses", method: http.MethodPost, path: "/openai/v1/responses", body: `{"model":"grok","input":"hello","stream":false}`},
			{name: "messages", method: http.MethodPost, path: "/openai/v1/messages", body: `{"model":"grok","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`},
			{name: "chat completions", method: http.MethodPost, path: "/openai/v1/chat/completions", body: `{"model":"grok","messages":[{"role":"user","content":"hello"}],"stream":false}`},
			{name: "grok media", method: http.MethodGet, path: "/openai/v1/videos/request-1"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "revoked")
				defer cleanup()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				recorder := httptest.NewRecorder()
				req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body)).WithContext(ctx)
				req.Header.Set("Content-Type", "application/json")

				router.ServeHTTP(recorder, req)

				require.Zero(t, repo.selectorCalls())
				require.Empty(t, upstream.providerHits())
			})
		}
	})

	t.Run("credential state mutation failures stop before reselection", func(t *testing.T) {
		for _, mode := range []string{"mutation_set_error", "mutation_temp", "mutation_cache"} {
			t.Run(mode, func(t *testing.T) {
				_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, mode)
				defer cleanup()

				recorder := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
				req.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(recorder, req)

				require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
				require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
				require.Empty(t, upstream.providerHits())
				require.Equal(t, 1, repo.selectorCalls())
			})
		}
	})

	t.Run("missing credential provider stops before upstream or reselection", func(t *testing.T) {
		_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "nil_provider")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
		require.Equal(t, 1, repo.selectorCalls())
		require.Empty(t, upstream.providerHits())
		require.Empty(t, repo.errorIDs())
	})
}

// TestResponsesGrok429FailoverIsBounded returns a rate limit from one or two
// Grok providers. It checks a healthy follow-up or bounded exhaustion,
// rate-limit mutations, and redacted failure text.
func TestResponsesGrok429FailoverIsBounded(t *testing.T) {
	t.Run("first rate limited provider selects healthy provider", func(t *testing.T) {
		_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "first_429")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), "resp_healthy")
		require.Equal(t, []int64{801, 802}, upstream.providerHits())
		require.Equal(t, []int64{801}, repo.rateLimitedProviderIDs())
	})

	t.Run("two rate limited providers stop without sweeping the pool", func(t *testing.T) {
		_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "all_429")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
		require.Equal(t, []int64{801, 802}, upstream.providerHits())
		require.Equal(t, []int64{801, 802}, repo.rateLimitedProviderIDs())
		require.NotContains(t, recorder.Body.String(), "expired")
		require.NotContains(t, recorder.Body.String(), "healthy-access")
		require.NotContains(t, recorder.Body.String(), "rate limited")
	})
}

// TestResponsesGrok402FailoverCooldown returns payment-required from the first
// Grok provider, then sends another request. It checks temporary unscheduling
// and repeated use of the healthy provider during cooldown.
func TestResponsesGrok402FailoverCooldown(t *testing.T) {
	_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "first_402")
	defer cleanup()

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Contains(t, recorder.Body.String(), "resp_healthy")
	require.Equal(t, []int64{801, 802}, upstream.providerHits())
	require.Equal(t, []int64{801}, repo.setTempIDs)
	before := repo.selectorCalls()

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"again","stream":false}`))
	secondReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(second, secondReq)

	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Equal(t, before+1, repo.selectorCalls())
	require.Equal(t, []int64{801, 802, 802}, upstream.providerHits(), "cooldown must exclude the 402 provider from later requests")
}

// TestResponsesGrok429FailoverHandlesMixedStatuses combines rate limits with
// server errors and an API-key provider. It checks the observed provider
// sequence and success or safe failure under each retry order.
func TestResponsesGrok429FailoverHandlesMixedStatuses(t *testing.T) {
	t.Run("429 then 500 stops after the bounded followup", func(t *testing.T) {
		_, _, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "mixed_429_500")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
		require.Equal(t, []int64{801, 802}, upstream.providerHits())
		require.NotContains(t, recorder.Body.String(), "upstream unavailable")
	})

	t.Run("500 then 429 permits one healthy followup", func(t *testing.T) {
		_, _, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "mixed_500_429")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, []int64{801, 802, 803}, upstream.providerHits())
	})

	t.Run("OAuth 429 then API-key failure cannot bypass the bound", func(t *testing.T) {
		_, _, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "oauth_429_apikey_500")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusBadGateway, recorder.Code, recorder.Body.String())
		require.Equal(t, []int64{801, 802}, upstream.providerHits())
	})
}

// TestGrokMedia429FailoverIsBounded submits video generation to a rate-limited
// Grok provider pool. It checks one healthy follow-up or HTTP 429 after two
// calls, with redacted rate-limit text.
func TestGrokMedia429FailoverIsBounded(t *testing.T) {
	t.Run("first 429 selects one healthy followup", func(t *testing.T) {
		_, _, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "first_429")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/videos/generations", bytes.NewBufferString(`{"model":"grok-imagine-video","prompt":"waves"}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		require.Equal(t, []int64{801, 802}, upstream.providerHits())
	})

	t.Run("second 429 stops without sweeping a third provider", func(t *testing.T) {
		_, _, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "all_429")
		defer cleanup()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/openai/v1/videos/generations", bytes.NewBufferString(`{"model":"grok-imagine-video","prompt":"waves"}`))
		req.Header.Set("Content-Type", "application/json")

		router.ServeHTTP(recorder, req)

		require.Equal(t, http.StatusTooManyRequests, recorder.Code, recorder.Body.String())
		require.Equal(t, []int64{801, 802}, upstream.providerHits())
		require.NotContains(t, recorder.Body.String(), "rate limited")
	})
}

// TestGrokOAuthCredentialFailoverAcrossHTTPHandlers exercises Messages, Chat
// Completions, raw chat fallback, and video generation. It checks
// healthy-provider recovery or credential exhaustion with recorded mutations
// and secret-free client messages.
func TestGrokOAuthCredentialFailoverAcrossHTTPHandlers(t *testing.T) {
	endpoints := []struct {
		name   string // Credential endpoint case name.
		method string // Client HTTP request method.
		path   string // Client endpoint request path.
		body   string // JSON client request body.
	}{
		{name: "messages", method: http.MethodPost, path: "/openai/v1/messages", body: `{"model":"grok","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`},
		{name: "chat completions", method: http.MethodPost, path: "/openai/v1/chat/completions", body: `{"model":"grok","messages":[{"role":"user","content":"hello"}],"stream":false}`},
		{name: "chat completions raw fallback", method: http.MethodPost, path: "/openai/v1/chat/completions", body: `{"model":"grok","messages":[{"role":"user","content":"hello"}],"stop":["END"],"stream":false}`},
		{name: "grok media", method: http.MethodPost, path: "/openai/v1/videos/generations", body: `{"model":"grok-imagine-video","prompt":"waves"}`},
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint.name+" revoked selects healthy", func(t *testing.T) {
			_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "revoked")
			defer cleanup()
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(endpoint.method, endpoint.path, bytes.NewBufferString(endpoint.body))
			req.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, []int64{801}, repo.errorIDs())
			require.Equal(t, []int64{802}, upstream.providerHits())
		})

		t.Run(endpoint.name+" all providers exhausted safely", func(t *testing.T) {
			_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "all_revoked")
			defer cleanup()
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(endpoint.method, endpoint.path, bytes.NewBufferString(endpoint.body))
			req.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
			require.NotContains(t, recorder.Body.String(), "revoked-refresh")
			require.NotContains(t, recorder.Body.String(), "healthy-refresh")
			require.Equal(t, []int64{801, 802}, repo.errorIDs())
			require.Empty(t, upstream.providerHits())
		})
	}
}

// TestGrokOAuthMissingSelectedRowRetriesHealthyProviderWithoutMutation makes
// the selected provider disappear during its reread. It checks healthy upstream
// execution and empty credential-error and temporary-unscheduling observations.
func TestGrokOAuthMissingSelectedRowRetriesHealthyProviderWithoutMutation(t *testing.T) {
	_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "missing_row")
	defer cleanup()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/responses", bytes.NewBufferString(`{"model":"grok","input":"hello","stream":false}`))
	req.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, []int64{802}, upstream.providerHits())
	require.Empty(t, repo.errorIDs())
	require.Empty(t, repo.setTempIDs)
}

// TestGatewayChatCredentialStopDoesNotSelectAnotherProviderAndReturnsSafe503
// supplies a credential failure that stops provider switching. It checks
// exhausted state with zero switches and a safe HTTP 503 despite supplied
// secret-bearing client details.
func TestGatewayChatCredentialStopDoesNotSelectAnotherProviderAndReturnsSafe503(t *testing.T) {
	stopErr := &forwardcore.UpstreamFailoverError{
		Stage:              forwardcore.GatewayFailureStageProviderAuth,
		Scope:              forwardcore.GatewayFailureScopeShared,
		Reason:             forwardcore.GrokCredentialReasonProviderConfig,
		NextProviderAction: forwardcore.NextProviderStop,
		ClientStatusCode:   http.StatusTeapot,
		ClientMessage:      "invalid_client client_secret=must-not-leak",
	}
	state := failover.NewFailoverState[*forwardcore.UpstreamFailoverError](3, false, gatewaytelemetry.Failover)
	action := state.HandleFailoverError(context.Background(), &mockTempUnscheduler{}, 71, capability.PlatformGrok, 0, stopErr)

	require.Equal(t, failover.FailoverExhausted, action)
	require.Zero(t, state.SwitchCount)
	require.Empty(t, state.FailedProviderIDs)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	(gatewayhttp.MessagesErrorOutput{}).ChatExhausted(c, state.LastFailoverErr, false)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
	require.NotContains(t, recorder.Body.String(), "invalid_client")
	require.NotContains(t, recorder.Body.String(), "client_secret")
}

// TestGatewayChatAntigravityCredentialFailureReturnsActionableMessage renders a
// typed Antigravity credential rejection with secret upstream text. It checks
// the actionable HTTP 502 message and absence of bearer and refresh-token
// details.
func TestGatewayChatAntigravityCredentialFailureReturnsActionableMessage(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	(gatewayhttp.MessagesErrorOutput{}).ChatExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:         http.StatusUnauthorized,
		Stage:              forwardcore.GatewayFailureStageProviderAuth,
		Scope:              forwardcore.GatewayFailureScopeProvider,
		Reason:             forwardcore.AntigravityCredentialRejectedReason,
		NextProviderAction: forwardcore.NextProviderRetry,
		ClientStatusCode:   http.StatusBadGateway,
		ClientMessage:      forwardcore.AntigravityCredentialRejectedClientMessage,
		ResponseBody:       []byte(`{"error":{"message":"Invalid bearer token","refresh_token":"must-not-leak"}}`),
	}, false)

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), forwardcore.AntigravityCredentialRejectedClientMessage)
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "bearer")
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "refresh_token")
}

// TestOpenAIAccessStateCredentialFailureUsesTypedSafeResponse supplies an
// access-state failure containing workspace and token details. It checks the
// typed HTTP 502 client message and absence of those upstream details.
func TestOpenAIAccessStateCredentialFailureUsesTypedSafeResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{}).openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:         http.StatusForbidden,
		Stage:              forwardcore.GatewayFailureStageProviderAuth,
		Scope:              forwardcore.GatewayFailureScopeProvider,
		Reason:             forwardcore.OpenAIUpstreamAccessStateReason,
		NextProviderAction: forwardcore.NextProviderRetry,
		ClientStatusCode:   http.StatusBadGateway,
		ClientMessage:      "Upstream access is temporarily unavailable, please retry later",
		ResponseBody:       []byte(`{"error":{"message":"Your workspace is deactivated","token":"must-not-leak"}}`),
	}, false)

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "Upstream access is temporarily unavailable")
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "deactivated")
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

// TestOpenAICapacityFailoverExhaustionPreservesMessageAsServerError renders an
// overload failure through OpenAI, Responses, and Anthropic outputs. It checks
// HTTP 503 and protocol-appropriate server-error types while retaining the
// public overload message.
func TestOpenAICapacityFailoverExhaustionPreservesMessageAsServerError(t *testing.T) {
	message := "Our servers are currently overloaded. Please try again later."
	failoverErr := &forwardcore.UpstreamFailoverError{
		StatusCode:              http.StatusBadRequest,
		ResponseBody:            []byte(`{"error":{"code":"server_is_overloaded","message":"` + message + `"}}`),
		RetryableOnSameProvider: true,
		RequestScopedTransient:  true,
		ClientStatusCode:        http.StatusServiceUnavailable,
		ClientMessage:           message,
	}

	t.Run("native_openai", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{}).openAIAttemptSupport().HandleFailoverExhausted(c, failoverErr, false)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "server_error", gjson.Get(recorder.Body.String(), "error.type").String())
		require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
		require.NotContains(t, recorder.Body.String(), "server_is_overloaded")
	})

	t.Run("responses_compat", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(c, failoverErr, false)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "server_error", gjson.Get(recorder.Body.String(), "error.code").String())
		require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
	})

	t.Run("anthropic_compat", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{}).openAIAttemptSupport().HandleAnthropicFailoverExhausted(c, failoverErr, false)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Equal(t, "api_error", gjson.Get(recorder.Body.String(), "error.type").String())
		require.Equal(t, message, gjson.Get(recorder.Body.String(), "error.message").String())
	})
}

// TestResponsesFailoverExhaustedAfterForwardedTerminalMarksOpsWithoutDuplicateFrame
// starts with an official failed frame, no frame, or heartbeat-only output. It
// checks retained official text, one generated terminal frame where needed, and
// the corresponding Ops stream-error details.
func TestResponsesFailoverExhaustedAfterForwardedTerminalMarksOpsWithoutDuplicateFrame(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	official := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"official failure\"}}}\n\n"
	_, err := c.Writer.Write([]byte(official))
	require.NoError(t, err)
	gatewayhttp.MarkOpsStreamError(c, "server_error", "official failure", http.StatusBadGateway)

	(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:   http.StatusBadGateway,
		ResponseBody: []byte(`{"error":{"message":"fallback failure"}}`),
	}, true)

	require.Equal(t, official, recorder.Body.String())
	streamErr, ok := gatewayhttp.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "official failure", streamErr.Message)

	markerRecorder := httptest.NewRecorder()
	markerContext, _ := gin.CreateTestContext(markerRecorder)
	(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(markerContext, &forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusTooManyRequests,
	}, true)
	require.Contains(t, markerRecorder.Body.String(), "event: response.failed")
	require.Equal(t, 1, strings.Count(markerRecorder.Body.String(), "event: response.failed"))
	streamErr, ok = gatewayhttp.GetOpsStreamError(markerContext)
	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, streamErr.IntendedStatus)
	require.Equal(t, "rate_limit_error", streamErr.ErrType)

	heartbeatRecorder := httptest.NewRecorder()
	heartbeatContext, _ := gin.CreateTestContext(heartbeatRecorder)
	heartbeat := ": keepalive\n\n"
	written, err := heartbeatRecorder.Write([]byte(heartbeat))
	require.NoError(t, err)
	gatewayhttp.RecordStreamHeartbeat(heartbeatContext, written)
	(gatewayhttp.MessagesErrorOutput{}).ResponsesExhausted(heartbeatContext, &forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
	}, true)
	require.True(t, strings.HasPrefix(heartbeatRecorder.Body.String(), heartbeat))
	require.Equal(t, 1, strings.Count(heartbeatRecorder.Body.String(), "event: response.failed"))
}

// TestGatewayChatInferenceExhaustionRestoresRetryAfter renders a rate-limit
// failure through Chat error output. It checks HTTP 429 and retention of the
// supplied numeric Retry-After header.
func TestGatewayChatInferenceExhaustionRestoresRetryAfter(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	(gatewayhttp.MessagesErrorOutput{}).ChatExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"45"}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "45", recorder.Header().Get("Retry-After"))
}

// TestCredentialFailoverExhaustionReturnsFixedSafe503 supplies
// revoked-credential failure details containing refresh-token text. It checks
// the fixed HTTP 503 message and absence of the supplied secret-bearing
// strings.
func TestCredentialFailoverExhaustionReturnsFixedSafe503(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		Stage:              forwardcore.GatewayFailureStageProviderAuth,
		Scope:              forwardcore.GatewayFailureScopeProvider,
		Reason:             forwardcore.GrokCredentialReasonRevoked,
		NextProviderAction: forwardcore.NextProviderRetry,
		ClientStatusCode:   http.StatusTeapot,
		ClientMessage:      "invalid_grant refresh_token=must-not-leak",
	}, false)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), forwardcore.GrokCredentialUnavailableClientMessage)
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "invalid_grant")
	require.NotContains(t, strings.ToLower(recorder.Body.String()), "refresh_token")
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

// TestInferenceFailoverExhaustionRestoresRetryAfter renders a rate-limit
// failure through OpenAI attempt support. It checks HTTP 429 and retention of
// the supplied numeric Retry-After header.
func TestInferenceFailoverExhaustionRestoresRetryAfter(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"17"}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, "17", recorder.Header().Get("Retry-After"))
}

// TestFailoverExhaustionRejectsSecretBearingRetryAfter supplies a token-shaped
// value in a rate-limit Retry-After header. It checks HTTP 429 with that header
// omitted and no secret in the body.
func TestFailoverExhaustionRejectsSecretBearingRetryAfter(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{"refresh_token=must-not-leak"}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Empty(t, recorder.Header().Get("Retry-After"))
	require.NotContains(t, recorder.Body.String(), "must-not-leak")
}

// TestFailoverExhaustionRejectsFarFutureRetryAfterDate supplies an HTTP date
// thirty days ahead in Retry-After. It checks that rate-limit status remains
// while the far-future header is omitted.
func TestFailoverExhaustionRejectsFarFutureRetryAfterDate(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode: http.StatusTooManyRequests,
		ResponseHeaders: http.Header{
			// Thirty days supplies a far-future value for header rejection.
			"Retry-After": []string{time.Now().Add(30 * 24 * time.Hour).UTC().Format(http.TimeFormat)},
		},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Empty(t, recorder.Header().Get("Retry-After"))
}

// TestFailoverExhaustionAllowsBoundedRetryAfterDate supplies an HTTP date one
// hour ahead in Retry-After. It checks that rate-limit status and the supplied
// header value reach the client.
func TestFailoverExhaustionAllowsBoundedRetryAfterDate(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	// One hour supplies a near-future HTTP date accepted by this case.
	retryAfter := time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)

	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:      http.StatusTooManyRequests,
		ResponseHeaders: http.Header{"Retry-After": []string{retryAfter}},
	}, false)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	require.Equal(t, retryAfter, recorder.Header().Get("Retry-After"))
}

// TestOpsRecoveredCredentialFailoverDoesNotCreateRequestError supplies
// inference and credential events before a successful response. It checks a
// captured HTTP 200 Ops entry with both events and the credential phase.
func TestOpsRecoveredCredentialFailoverDoesNotCreateRequestError(t *testing.T) {
	queue := newOpsCaptureQueue(2)

	ops := opscore.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	router := gin.New()
	router.Use(gatewayhttp.OpsErrorLoggerMiddleware(ops, queue, gatewayhttp.OpsObservationAccess{}))
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		c.Set(gatewayhttp.OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
			{Stage: string(forwardcore.GatewayFailureStageInference), UpstreamStatusCode: http.StatusForbidden, Message: "earlier inference failure"},
			{
				Stage: string(forwardcore.GatewayFailureStageProviderAuth), Scope: string(forwardcore.GatewayFailureScopeProvider),
				Reason: string(forwardcore.GrokCredentialReasonRevoked), Message: "Grok OAuth credentials require provider action",
			},
		})
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), queue.health.Length)
	job := <-queue.jobs
	require.Equal(t, http.StatusOK, job.entry.StatusCode)
	require.Equal(t, string(forwardcore.GatewayFailureStageProviderAuth), job.entry.ErrorPhase)
	require.NotNil(t, job.entry.UpstreamErrorsJSON)
	events, err := opscore.ParseOpsUpstreamErrors(*job.entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, string(forwardcore.GatewayFailureStageProviderAuth), events[1].Stage)
}

// TestHandleGroupSelectionBusinessError wraps the Claude-Code-only group
// rejection before passing it to the business-error writer. It checks
// recognition, HTTP 403, permission_error, and the policy's own message.
func TestHandleGroupSelectionBusinessError(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	var status int
	var errType string
	var message string
	handled := gatewayhttp.WriteGroupSelectionBusinessError(
		c,
		fmt.Errorf("select provider: %w", routing.ErrClaudeCodeOnly),
		false,
		keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{},
		func(gotStatus int, gotErrType string, gotMessage string, _ bool) {
			status = gotStatus
			errType = gotErrType
			message = gotMessage
		},
	)

	require.True(t, handled)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "permission_error", errType)
	require.Equal(t, routing.ErrClaudeCodeOnly.Error(), message)
}

// TestOpenAIResponsesRequiredCapability varies image intent and the selected
// platform. It checks that OpenAI image intent requires Responses capability
// while Grok image intent and ordinary text use text-generation capability.
func TestOpenAIResponsesRequiredCapability(t *testing.T) {
	tests := []struct {
		name        string // Endpoint capability case name.
		imageIntent bool   // Explicit image request intent.
		platform    string // Selected upstream provider platform.
		// Expected endpoint capability value.
		want providercore.OpenAIEndpointCapability
	}{
		{
			name:        "OpenAI explicit image intent requires Responses",
			imageIntent: true,
			platform:    capability.PlatformOpenAI,
			want:        providercore.OpenAIEndpointCapabilityResponses,
		},
		{
			name:        "Grok explicit image intent keeps chat capability",
			imageIntent: true,
			platform:    capability.PlatformGrok,
			want:        providercore.OpenAIEndpointCapabilityTextGeneration,
		},
		{
			name:     "non-image intent keeps chat capability",
			platform: capability.PlatformOpenAI,
			want:     providercore.OpenAIEndpointCapabilityTextGeneration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, textflow.ResponsesCapability(tt.imageIntent, tt.platform))
		})
	}
}

// TestOpenAIEnsureForwardErrorResponse_ResponsesRouteCyberWarningEmitsOriginalMessage
// starts a Responses stream before a wrapped cyber warning. It checks a failed
// event carrying the upstream warning text and invalid-request code.
func TestOpenAIEnsureForwardErrorResponse_ResponsesRouteCyberWarningEmitsOriginalMessage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
	// Seed heartbeat output in the in-memory recorder before error rendering;
	// its bytes are inspected below, so this setup discards the write result.
	_, _ = c.Writer.WriteString(":\n\n")

	message := "This request has been flagged for potentially high-risk cyber activity."
	err := &openAIHandlerTestWarningError{
		warning: &forwardcore.UpstreamWarning{
			StatusCode:   http.StatusForbidden,
			ResponseBody: []byte(`{"type":"response.failed","error":{"message":"` + message + `"}}`),
			Message:      message,
		},
		err: errors.New("upstream response failed"),
	}

	wrote := gatewayhttp.DefaultOpenAIErrorOutput().EnsureResponse(c, false, err)

	require.True(t, wrote)
	body := w.Body.String()
	assert.Contains(t, body, "event: response.failed\n")
	assert.Contains(t, body, `"code":"invalid_request"`)
	assert.Contains(t, body, message)
	assert.NotContains(t, body, "Upstream request failed")
}

// TestOpenAIForwardErrorAlreadyCommunicated varies writer growth and the
// forwarding error's content. It checks recognition after a terminal upstream
// failure, and rejection when output is absent or contains only a heartbeat.
func TestOpenAIForwardErrorAlreadyCommunicated(t *testing.T) {
	t.Run("upstream response failed after write", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
		before := c.Writer.Size()
		// Seed recorder output before communication detection; the test reads
		// writer growth and error recognition, so the setup write result is discarded.
		_, _ = c.Writer.WriteString(`event: response.failed
data: {"type":"response.failed","error":{"message":"This content was flagged"}}

`)

		reported := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, before, errors.New("upstream response failed: This content was flagged"))

		require.True(t, reported)
	})

	t.Run("no write still needs fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)

		reported := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, c.Writer.Size(), errors.New("upstream response failed: This content was flagged"))

		require.False(t, reported)
	})

	t.Run("generic error after write still needs fallback", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointResponses, nil)
		before := c.Writer.Size()
		// Seed heartbeat bytes before communication detection; the generic read
		// error must still need a fallback despite this recorder write.
		_, _ = c.Writer.WriteString(":\n\n")

		reported := gatewayhttp.OpenAIForwardErrorAlreadyCommunicated(c, before, errors.New("stream read error: unexpected EOF"))

		require.False(t, reported)
	})
}

// TestOpenAIHandleFailoverExhausted_CyberWarningPassesThroughMessage renders a
// forbidden upstream cyber warning. It checks HTTP 403 and the warning's text
// in place of the generic provider-exhaustion message.
func TestOpenAIHandleFailoverExhausted_CyberWarningPassesThroughMessage(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	gatewayhttp.SetOpsRequestContext(c, "gpt-5.4", false)

	message := "This content was flagged for possible cybersecurity risk. If this seems wrong, try rephrasing your request."
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.openAIAttemptSupport().HandleFailoverExhausted(c, &forwardcore.UpstreamFailoverError{
		StatusCode:   http.StatusForbidden,
		ResponseBody: []byte(`{"error":{"message":"` + message + `"}}`),
	}, false)

	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), message)
	assert.NotContains(t, w.Body.String(), "All available providers exhausted")
}

// TestShouldLogOpenAIForwardFailureAsWarn varies nil context, missing output,
// fallback output, and an existing response. It checks warning eligibility only
// for an already-written response when no fallback was written.
func TestShouldLogOpenAIForwardFailureAsWarn(t *testing.T) {
	t.Run("fallback_written_should_not_downgrade", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		require.False(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(c, true))
	})

	t.Run("context_nil_should_not_downgrade", func(t *testing.T) {
		require.False(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(nil, false))
	})

	t.Run("response_not_written_should_not_downgrade", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		require.False(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(c, false))
	})

	t.Run("response_already_written_should_downgrade", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.String(http.StatusForbidden, "already written")
		require.True(t, gatewayhttp.ShouldLogOpenAIForwardFailureAsWarn(c, false))
	})
}

// TestOpenAIResponses_MissingDependencies_ReturnsServiceUnavailable supplies
// authentication to an empty endpoint fixture. It checks panic-free HTTP 503
// with an api_error and the dependency-unavailable message.
func TestOpenAIResponses_MissingDependencies_ReturnsServiceUnavailable(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      10,
		GroupID: &groupID,
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	// Keep authenticated ingress available while execution dependencies are
	// absent so the response specifically tests dependency rejection before forwarding.
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	require.NotPanics(t, func() {
		h.Responses(c)
	})

	require.Equal(t, http.StatusServiceUnavailable, w.Code)

	var parsed map[string]any
	err := json.Unmarshal(w.Body.Bytes(), &parsed)
	require.NoError(t, err)

	errorObj, ok := parsed["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "api_error", errorObj["type"])
	assert.Equal(t, "Service temporarily unavailable", errorObj["message"])
}

// TestOpenAIResponses_SetsClientTransportHTTP submits an unauthenticated HTTP
// Responses request. It checks HTTP 401 while confirming that the
// client-transport marker is set to HTTP.
func TestOpenAIResponses_SetsClientTransportHTTP(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.Responses(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, gatewayhttp.OpenAIClientTransportHTTP, gatewayhttp.GetOpenAIClientTransport(c))
}

// TestOpenAIResponses_RejectsMessageIDAsPreviousResponseID supplies a message
// identifier in an authenticated HTTP Responses request. It checks HTTP 400 and
// continuation guidance requiring a response identifier.
func TestOpenAIResponses_RejectsMessageIDAsPreviousResponseID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.4","stream":false,"previous_response_id":"msg_123456","input":[{"type":"input_text","text":"hello"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: 1},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "previous_response_id must be a response.id")
}

// TestOpenAIResponses_AcceptsHTTPContinuationPreviousResponseIDBeforeRouting
// registers ownership for the requesting user and key before ingress. It checks
// passage beyond bad-request continuation validation without WebSocket-only
// guidance.
func TestOpenAIResponses_AcceptsHTTPContinuationPreviousResponseIDBeforeRouting(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.4","stream":false,"previous_response_id":"resp_123456","input":[{"type":"input_text","text":"hello"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: 1},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	require.NoError(t, h.Input.Source.Responses.Lineage.Store.BindHTTPResponseOwner(context.Background(), groupID, "resp_123456", 1, 101, h.Input.Source.WebSockets.OpenAIHTTPResponseStickyTTL()))
	h.Responses(c)

	require.NotEqual(t, http.StatusBadRequest, w.Code)
	require.NotContains(t, w.Body.String(), "Responses WebSocket v2")
}

// TestOpenAIResponses_RejectsHTTPContinuationOwnedByAnotherUser binds a
// response to a different user and key in the same group. It checks HTTP 400
// and unavailable-continuation guidance for the requester.
func TestOpenAIResponses_RejectsHTTPContinuationOwnedByAnotherUser(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.4","stream":false,"previous_response_id":"resp_other_tenant","input":"hello"}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      202,
		UserID:  2,
		GroupID: &groupID,
		User:    &identity.User{ID: 2},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 2, Concurrency: 1})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	require.NoError(t, h.Input.Source.Responses.Lineage.Store.BindHTTPResponseOwner(context.Background(), groupID, "resp_other_tenant", 1, 101, h.Input.Source.WebSockets.OpenAIHTTPResponseStickyTTL()))
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "previous_response_id is not available for this user")
}

// TestOpenAIResponses_RejectsUnownedHTTPContinuation supplies an authenticated
// request with a response identifier absent from the ownership store. It checks
// HTTP 400 and the unavailable-for-this-user continuation message.
func TestOpenAIResponses_RejectsUnownedHTTPContinuation(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.4","stream":false,"previous_response_id":"resp_unknown","input":"hello"}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 101, UserID: 1, GroupID: &groupID, User: &identity.User{ID: 1}})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1, Concurrency: 1})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "previous_response_id is not available for this user")
}

// TestOpenAIResponses_FunctionCallOutputHTTPGuidanceDoesNotSuggestPreviousResponseReuse
// sends an incomplete function-call output through HTTP Responses. It checks
// WebSocket v2 guidance and absence of advice to reuse previous_response_id.
func TestOpenAIResponses_FunctionCallOutputHTTPGuidanceDoesNotSuggestPreviousResponseReuse(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(
		`{"model":"gpt-5.4","stream":false,"input":[{"type":"function_call_output","output":"{}"}]}`,
	))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(2)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: 1},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
		UserID:      1,
		Concurrency: 1,
	})

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Responses(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "Responses WebSocket v2")
	require.NotContains(t, w.Body.String(), "reuse previous_response_id")
}

// GetSessionProviderID returns a fixed cache miss for provider lookup.
func (s *cyberSessionBlockHandlerCacheStub) GetSessionProviderID(context.Context, int64, string) (int64, error) {
	return 0, errors.New("not found")
}

// SetSessionProviderID acknowledges provider binding without storing any
// fixture state.
func (s *cyberSessionBlockHandlerCacheStub) SetSessionProviderID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

// RefreshSessionTTL acknowledges session lifetime refresh without retaining an
// expiry.
func (s *cyberSessionBlockHandlerCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

// DeleteSessionProviderID acknowledges provider-binding deletion without
// changing this block fixture.
func (s *cyberSessionBlockHandlerCacheStub) DeleteSessionProviderID(context.Context, int64, string) error {
	return nil
}

// SetSessionOwnerGroupID reports successful owner binding without retaining
// group ownership.
func (s *cyberSessionBlockHandlerCacheStub) SetSessionOwnerGroupID(context.Context, int64, string, string, int64, time.Duration) (bool, error) {
	return true, nil
}

// GetSessionOwnerGroupID returns a fixed cache miss for group ownership.
func (s *cyberSessionBlockHandlerCacheStub) GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error) {
	return 0, errors.New("not found")
}

// RefreshSessionOwnerTTL acknowledges owner lifetime refresh without retaining
// an expiry.
func (s *cyberSessionBlockHandlerCacheStub) RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error {
	return nil
}

// SetCyberSessionBlocked sets the fixture's single block flag regardless of the
// supplied session or duration. Callers serialize its use because the flag has
// no synchronization or expiration behavior.
func (s *cyberSessionBlockHandlerCacheStub) SetCyberSessionBlocked(context.Context, string, time.Duration) error {
	s.blocked = true
	return nil
}

// IsCyberSessionBlocked increments the read count and returns the fixture's
// single block flag. It ignores the session key, allowing serial assertions to
// identify whether group scoping consulted block storage.
func (s *cyberSessionBlockHandlerCacheStub) IsCyberSessionBlocked(context.Context, string) (bool, error) {
	s.readCalls++
	return s.blocked, nil
}

// TestOpenAIRecordCyberWarning_RecordsStructuredResponseBody supplies a saved
// request snapshot and structured warning body. It checks recorded user,
// provider, endpoint, prompt excerpt, and extracted warning text.
func TestOpenAIRecordCyberWarning_RecordsStructuredResponseBody(t *testing.T) {
	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, moderation.ContentModerationProtocolOpenAIResponses, []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"bad cyber prompt"}]}]}`))
	apiKey := &apikey.APIKey{
		ID:     101,
		Name:   "test-key",
		UserID: 1001,
		User:   &identity.User{ID: 1001, Email: "user@example.com"},
	}
	provider := &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 2001,
			Name: "openai-1",
		},
	}

	h.openAIAttemptSupport().RecordOpenAICyberWarning(
		c,
		nil,
		apiKey,
		provider,
		"gpt-5.4",
		400,
		[]byte(`{"error":{"message":"This request may pose a cybersecurity risk."}}`),
		"",
	)

	require.Len(t, repo.cyberWarnings, 1)
	warning := repo.cyberWarnings[0]
	require.Equal(t, "user@example.com", warning.UserEmail)
	require.Equal(t, int64(2001), *warning.ProviderID)
	require.Equal(t, "/v1/responses", warning.Endpoint)
	require.Equal(t, "bad cyber prompt", warning.PromptExcerpt)
	require.Contains(t, warning.WarningText, "cybersecurity risk")
}

// TestOpenAIRecordCyberWarning_UsesExplicitPromptExcerpt supplies different
// context and call-specific prompt excerpts. It checks that the call-specific
// first-turn excerpt is stored in the cyber warning.
func TestOpenAIRecordCyberWarning_UsesExplicitPromptExcerpt(t *testing.T) {
	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAICyberWarningPromptExcerpt(c, "second turn prompt")

	apiKey := &apikey.APIKey{ID: 101, Name: "test-key", UserID: 1001, User: &identity.User{ID: 1001, Email: "user@example.com"}}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 2001, Name: "openai-1"}}

	h.openAIAttemptSupport().RecordOpenAICyberWarningWithPromptExcerpt(
		c,
		nil,
		apiKey,
		provider,
		"gpt-5.4",
		400,
		[]byte(`{"error":{"message":"This request may pose a cybersecurity risk."}}`),
		"",
		"first turn prompt",
	)

	require.Len(t, repo.cyberWarnings, 1)
	require.Equal(t, "first turn prompt", repo.cyberWarnings[0].PromptExcerpt)
}

// TestOpenAIRecordCyberWarning_RequestSnapshotUsesCurrentToolOutput saves user
// text, a function call, and its output in one snapshot. It checks selection of
// the current tool output, complete input metadata, and retained upstream
// status.
func TestOpenAIRecordCyberWarning_RequestSnapshotUsesCurrentToolOutput(t *testing.T) {
	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           true,
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.SetOpenAICyberWarningRequestSnapshot(c, moderation.ContentModerationProtocolOpenAIResponses, []byte(`{
		"model":"gpt-5.4",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest cyber prompt"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"done"}
		]
	}`))

	apiKey := &apikey.APIKey{ID: 101, Name: "test-key", UserID: 1001, User: &identity.User{ID: 1001, Email: "user@example.com"}}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 2001, Name: "openai-1"}}

	h.openAIAttemptSupport().RecordOpenAICyberWarning(
		c,
		nil,
		apiKey,
		provider,
		"gpt-5.4",
		http.StatusOK,
		[]byte(`{"type":"response.failed","error":{"message":"This request has been flagged for potentially high-risk cyber activity."}}`),
		"",
	)

	require.Len(t, repo.cyberWarnings, 1)
	require.Equal(t, "done", repo.cyberWarnings[0].PromptExcerpt)
	require.Equal(t, moderation.ContentModerationSourceTool, repo.cyberWarnings[0].Source)
	require.True(t, repo.cyberWarnings[0].ContentComplete)
	require.Len(t, repo.cyberWarnings[0].InputItems, 1)
	require.Equal(t, "done", repo.cyberWarnings[0].InputItems[0].Text)
	require.Equal(t, http.StatusOK, repo.cyberWarnings[0].UpstreamStatus)
}

// TestOpenAIRecordCyberPolicyIfMarked_SkipsSideEffectsOutOfScope marks an
// upstream cyber-policy failure for a group outside moderation scope. It checks
// an unhandled result, empty warnings, and an unset recorded marker.
func TestOpenAIRecordCyberPolicyIfMarked_SkipsSideEffectsOutOfScope(t *testing.T) {
	cfg := moderation.ContentModerationConfig{
		CyberWarningEnabled: true,
		CyberWindowHours:    720,
		AllGroups:           false,
		GroupIDs:            []int64{101},
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo, repo)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Moderator: moderationSvc})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	gatewayhttp.MarkOpsCyberPolicy(c, moderationflow.Mark{
		Message:        "Request blocked by upstream cyber policy",
		Body:           `{"response":{"error":{"code":"cyber_policy","message":"Request blocked by upstream cyber policy"}}}`,
		UpstreamStatus: http.StatusOK,
	})
	outOfScopeGroupID := int64(202)
	apiKey := &apikey.APIKey{
		ID:      101,
		Name:    "test-key",
		UserID:  1001,
		GroupID: &outOfScopeGroupID,
		User:    &identity.User{ID: 1001, Email: "user@example.com"},
		Group:   &routing.Group{ID: outOfScopeGroupID, Name: "out-of-scope"},
	}
	provider := &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, ID: 2001, Name: "openai-1"}}

	handled := h.openAIAttemptSupport().RecordCyberPolicyIfMarked(c, apiKey, provider, nil, "gpt-5.4", true, "cyber-session-key", routing.PricingUsageFields{}, "payload-hash")

	require.False(t, handled)
	require.Empty(t, repo.cyberWarnings)
	require.False(t, c.GetBool(gatewayhttp.CyberPolicyRecordedKey))
}

// TestOpenAIRejectCyberSessionBlocked_OnlyChecksRiskControlGroups supplies a
// blocked session to in-scope and out-of-scope groups. It checks cache-read
// counts and a forbidden policy response only for the selected group.
func TestOpenAIRejectCyberSessionBlocked_OnlyChecksRiskControlGroups(t *testing.T) {
	selectedGroupID := int64(101)
	outOfScopeGroupID := int64(202)
	cfg := moderation.ContentModerationConfig{
		AllGroups: false,
		GroupIDs:  []int64{selectedGroupID},
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:          "true",
		moderation.SettingKeyContentModerationConfig:     string(rawCfg),
		moderation.SettingKeyCyberSessionBlockEnabled:    "true",
		moderation.SettingKeyCyberSessionBlockTTLSeconds: "3600",
	}}
	cache := &cyberSessionBlockHandlerCacheStub{blocked: true}
	gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
		nil, cache, nil, nil, nil, nil,
		nil, nil, nil, newOpenAIExecutionCredentialsForTest(nil,
			nil), nil, nil, nil, gatewaytestkit.RuntimeReaders(settingRepo), nil, responseHeaderFilterForTest(nil), nil, nil, nil,
	)
	gatewaySvc.Recorder = newHTTPCompletionFixture(nil, nil, nil,
		nil, nil, nil, nil, true)

	moderationSvc := newHTTPModeration(t, settingRepo, nil)
	moderationSvc.Start()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source: gatewaySvc, Credentials: gatewaySvcCredentialPort,
		Moderator: moderationSvc, Availability: newExecutionAvailabilityForTest(nil,

			nil, nil), Choices: gatewaySvcChoices,
	})
	body := []byte(`{"prompt_cache_key":"cyber-scope-session"}`)
	tests := []struct {
		name        string // Risk-control scope case name.
		groupID     int64  // Requesting key group ID.
		wantBlocked bool   // Expected session block result.
		wantReads   int    // Expected block storage reads.
	}{
		{name: "未纳入风控的分组放行", groupID: outOfScopeGroupID, wantBlocked: false, wantReads: 0},
		{name: "已纳入风控的分组保持屏蔽", groupID: selectedGroupID, wantBlocked: true, wantReads: 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cache.readCalls = 0
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			groupID := tc.groupID
			apiKey := &apikey.APIKey{ID: 1001, GroupID: &groupID}

			blocked := h.rejectIfCyberSessionBlocked(c, apiKey, body, "gpt-5.4", gatewayhttp.CyberBlockResponses)

			require.Equal(t, tc.wantBlocked, blocked)
			require.Equal(t, tc.wantReads, cache.readCalls)
			if tc.wantBlocked {
				require.Equal(t, http.StatusForbidden, w.Code)
				require.Contains(t, w.Body.String(), "session_blocked_by_cyber_policy")
			} else {
				require.Empty(t, w.Body.String())
			}
		})
	}
}

// Do records the selected provider and returns a JSON HTTP 502 fixture
// response. It ignores request and proxy inputs; the caller owns the returned
// response body and closes it after reading.
func (u *openAIHTTPPassthroughFailoverUpstream) Do(_ *http.Request, _ string, providerID int64, _ int) (*http.Response, error) {
	// Append the selected identity under the observation lock so concurrent
	// calls and copied snapshots agree on the recorded provider invocation order.
	u.mu.Lock()
	u.providerIDs = append(u.providerIDs, providerID)
	u.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"temporary upstream failure"}}`)),
	}, nil
}

// DoWithTLS delegates request execution to Do and ignores the TLS fingerprint
// profile. The returned scripted response has Do's provider-recording behavior,
// and the caller retains its response-body cleanup obligation.
func (u *openAIHTTPPassthroughFailoverUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// calls returns a copy of the recorded provider-ID sequence under its mutex.
// The caller can inspect or modify the returned slice after unlocking without
// changing this transport's stored observations.
func (u *openAIHTTPPassthroughFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.providerIDs...)
}

// Do records the selected provider and returns success for provider 9911. Other
// IDs receive statusCode with a credential-rejection body; the caller owns and
// closes each returned response body.
func (u *openAIHTTPPassthroughAuthFailoverUpstream) Do(_ *http.Request, _ string, providerID int64, _ int) (*http.Response, error) {
	// Append the selected identity under the observation lock so concurrent
	// calls and copied snapshots agree on the recorded provider invocation order.
	u.mu.Lock()
	u.providerIDs = append(u.providerIDs, providerID)
	u.mu.Unlock()
	if providerID == 9911 {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_healthy","object":"response","model":"gpt-5.2","status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)),
		}, nil
	}
	return &http.Response{
		StatusCode: u.statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream credential rejected"}}`)),
	}, nil
}

// DoWithTLS delegates request execution to Do and ignores the TLS fingerprint
// profile. The returned scripted response has Do's provider-recording behavior,
// and the caller retains its response-body cleanup obligation.
func (u *openAIHTTPPassthroughAuthFailoverUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// calls returns a copy of the recorded provider-ID sequence under its mutex.
// The caller can inspect or modify the returned slice after unlocking without
// changing this transport's stored observations.
func (u *openAIHTTPPassthroughAuthFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.providerIDs...)
}

// Do records the selected provider and returns an HTTP 200 event stream with a
// rate-limit failed event. Retry-After is one second, and the caller owns and
// closes the response body.
func (u *openAIHTTPPassthroughSSERateLimitUpstream) Do(_ *http.Request, _ string, providerID int64, _ int) (*http.Response, error) {
	// Append the selected identity under the observation lock so concurrent
	// calls and copied snapshots agree on the recorded provider invocation order.
	u.mu.Lock()
	u.providerIDs = append(u.providerIDs, providerID)
	u.mu.Unlock()
	body := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_rate_limited"}}`,
		"",
		"event: response.failed",
		`data: {"type":"response.failed","response":{"id":"resp_rate_limited","status":"failed","error":{"type":"invalid_request_error","code":"rate_limit_exceeded","message":"Concurrency limit exceeded for provider, please retry later"}}}`,
		"",
	}, "\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"Retry-After":  []string{"1"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}, nil
}

// DoWithTLS delegates request execution to Do and ignores the TLS fingerprint
// profile. The returned scripted response has Do's provider-recording behavior,
// and the caller retains its response-body cleanup obligation.
func (u *openAIHTTPPassthroughSSERateLimitUpstream) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// calls returns a copy of the recorded provider-ID sequence under its mutex.
// The caller can inspect or modify the returned slice after unlocking without
// changing this transport's stored observations.
func (u *openAIHTTPPassthroughSSERateLimitUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.providerIDs...)
}

// TestOpenAIResponses_APIKeyPassthroughPool5xxRetriesThenExhaustsMaxSwitches
// scripts gateway failures for a pooled provider and its fallback. It checks
// two calls to the first provider, one switch, and a safe HTTP 502 response.
func TestOpenAIResponses_APIKeyPassthroughPool5xxRetriesThenExhaustsMaxSwitches(t *testing.T) {
	groupID := int64(4203)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 9910, Name: "pool-api-key", Platform: capability.PlatformOpenAI,
				Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 1,
				Credentials: map[string]any{
					"api_key":                      "sk-pool",
					"base_url":                     "https://api.example.test",
					"pool_mode":                    true,
					"pool_mode_retry_count":        float64(1),
					"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
				},
				Extra: map[string]any{"openai_passthrough": true},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 9911, Name: "fallback-api-key", Platform: capability.PlatformOpenAI,
				Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 2,
				Credentials: map[string]any{
					"api_key":  "sk-fallback",
					"base_url": "https://api.example.test",
				},
				Extra: map[string]any{"openai_passthrough": true},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Billing.FallbackRateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	// Allow one provider switch after any same-provider pool retry; the
	// recorded call sequence distinguishes these two budgets.
	cfg.Gateway.Failover.MaxProviderSwitches = 1

	providerRepo := &openAIWSFailoverHandlerProviderRepoStub{providers: providers}
	upstream := &openAIHTTPPassthroughFailoverUpstream{}
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	t.Cleanup(billingCacheSvc.Stop)
	completionInput4 := billingtestkit.Calculator(nil, nil)
	completionInput5 := &providercore.DeferredService{}
	gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo,
		nil,
		cfg,
		nil,
		nil, nil,

		upstream,
		nil, completionInput5, newOpenAIExecutionCredentialsForTest(providerRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewaySvc.Recorder = newHTTPCompletionFixture(cfg, nil, completionInput4, billingCacheSvc, completionInput5, nil, nil, true)

	h := newGatewayHTTPEndpointsFromDeps(
		gatewaySvc, gatewaySvcCredentialPort, scheduler.NewConcurrencyService(nil, scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		), newFundingAdmissionFixture(billingCacheSvc, cfg), testkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg, nil, newExecutionAvailabilityForTest(providerRepo,

			nil, cfg), gatewaySvcChoices,
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.2","input":"hello","stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID: 1803, GroupID: &groupID,
		User:  &identity.User{ID: 1703, Status: billing.StatusActive},
		Group: &routing.Group{ID: groupID, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1703, Concurrency: 0})

	h.Responses(c)

	require.Equal(t, []int64{9910, 9910, 9911}, upstream.calls())
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream service temporarily unavailable", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
}

// TestOpenAIResponses_APIKeyPassthroughPoolAuthFailureRetriesThenSwitchesToHealthyProvider
// varies HTTP 401 and HTTP 403 failures from the pooled provider. It checks one
// same-provider retry followed by healthy-provider completion with HTTP 200.
func TestOpenAIResponses_APIKeyPassthroughPoolAuthFailureRetriesThenSwitchesToHealthyProvider(t *testing.T) {
	tests := []struct {
		name       string // Authentication failure case name.
		statusCode int    // Scripted upstream response status.
	}{
		{name: "401", statusCode: http.StatusUnauthorized},
		{name: "403", statusCode: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(4203)
			providers := []gatewayprovider.ExecutionProvider{
				{
					Record: providercore.Record{
						LoadLocation: time.LoadLocation, ID: 9910, Name: "pool-api-key", Platform: capability.PlatformOpenAI,
						Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 1,
						Credentials: map[string]any{
							"api_key":                      "sk-pool",
							"base_url":                     "https://api.example.test",
							"pool_mode":                    true,
							"pool_mode_retry_count":        float64(1),
							"pool_mode_retry_status_codes": []any{float64(tt.statusCode)},
						},
						Extra: map[string]any{"openai_passthrough": true},
					},
				},
				{
					Record: providercore.Record{
						LoadLocation: time.LoadLocation, ID: 9911, Name: "fallback-api-key", Platform: capability.PlatformOpenAI,
						Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 2,
						Credentials: map[string]any{
							"api_key":  "sk-fallback",
							"base_url": "https://api.example.test",
						},
						Extra: map[string]any{"openai_passthrough": true},
					},
				},
			}
			cfg := &config.Config{}
			cfg.Billing.FallbackRateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			// Allow one provider switch after any same-provider
			// pool retry; the recorded call sequence distinguishes
			// these two budgets.
			cfg.Gateway.Failover.MaxProviderSwitches = 1

			providerRepo := &openAIWSFailoverHandlerProviderRepoStub{providers: providers}
			upstream := &openAIHTTPPassthroughAuthFailoverUpstream{statusCode: tt.statusCode}
			rateLimitSvc := newAppHealthObserverFixture(providerRepo, cfg)
			billingCacheSvc := newBillingEligibilityFixture(cfg)
			billingCacheSvc.Start()
			t.Cleanup(billingCacheSvc.Stop)
			completionInput6 := billingtestkit.Calculator(nil, nil)
			completionInput7 := &providercore.DeferredService{}
			gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
				providerRepo,
				nil,
				cfg,
				nil,
				nil, rateLimitSvc,

				upstream,
				nil, completionInput7, newOpenAIExecutionCredentialsForTest(providerRepo,

					nil), nil,
				nil,
				nil,

				nil,
				nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
			)
			gatewaySvc.Recorder = newHTTPCompletionFixture(cfg, nil, completionInput6, billingCacheSvc, completionInput7, nil, completionHealth{rateLimitSvc.Core}, true)

			h := newGatewayHTTPEndpointsFromDeps(
				gatewaySvc, gatewaySvcCredentialPort, scheduler.NewConcurrencyService(nil, scheduler.Diagnostics{
					Logf: logging.LegacyPrintf,

					Event: logging.Event,
				},
				), newFundingAdmissionFixture(billingCacheSvc, cfg), testkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
				nil,
				nil,
				nil,
				nil,
				cfg, nil, newExecutionAvailabilityForTest(providerRepo,

					nil, cfg), gatewaySvcChoices,
			)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.2","input":"hello","stream":false}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
				ID: 1803, GroupID: &groupID,
				User:  &identity.User{ID: 1703, Status: billing.StatusActive},
				Group: &routing.Group{ID: groupID, Status: billing.StatusActive},
			})
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1703, Concurrency: 0})

			h.Responses(c)

			require.Equal(t, []int64{9910, 9910, 9911}, upstream.calls())
			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, "resp_healthy", gjson.GetBytes(rec.Body.Bytes(), "id").String())
		})
	}
}

// TestOpenAIResponses_APIKeyPassthroughSSERateLimitUsesConfiguredPoolRetry
// returns an HTTP 200 stream containing a rate-limit failure. It checks two
// calls to the pooled provider, no provider rate-limit mutation, and a safe
// HTTP 429 with Retry-After.
func TestOpenAIResponses_APIKeyPassthroughSSERateLimitUsesConfiguredPoolRetry(t *testing.T) {
	groupID := int64(4204)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 9912, Name: "pool-sse-rate-limit", Platform: capability.PlatformOpenAI,
				Type: capability.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Priority: 1,
				Credentials: map[string]any{
					"api_key":                      "sk-pool",
					"base_url":                     "https://api.example.test",
					"pool_mode":                    true,
					"pool_mode_retry_count":        float64(1),
					"pool_mode_retry_status_codes": []any{float64(http.StatusTooManyRequests)},
				},
				Extra: map[string]any{"openai_passthrough": true},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Billing.FallbackRateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	// Allow one provider switch after any same-provider pool retry; the
	// recorded call sequence distinguishes these two budgets.
	cfg.Gateway.Failover.MaxProviderSwitches = 1

	providerRepo := &openAIWSFailoverHandlerProviderRepoStub{providers: providers}
	upstream := &openAIHTTPPassthroughSSERateLimitUpstream{}
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	t.Cleanup(billingCacheSvc.Stop)
	completionInput8 := billingtestkit.Calculator(nil, nil)
	completionInput9 := &providercore.DeferredService{}
	gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo,
		nil,
		cfg,
		nil,
		nil, nil,

		upstream,
		nil, completionInput9, newOpenAIExecutionCredentialsForTest(providerRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewaySvc.Recorder = newHTTPCompletionFixture(cfg, nil, completionInput8, billingCacheSvc, completionInput9, nil, nil, true)

	h := newGatewayHTTPEndpointsFromDeps(
		gatewaySvc, gatewaySvcCredentialPort, scheduler.NewConcurrencyService(nil, scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		), newFundingAdmissionFixture(billingCacheSvc, cfg), testkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg, nil, newExecutionAvailabilityForTest(providerRepo,

			nil, cfg), gatewaySvcChoices,
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.2","input":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID: 1804, GroupID: &groupID,
		User:  &identity.User{ID: 1704, Status: billing.StatusActive},
		Group: &routing.Group{ID: groupID, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1704, Concurrency: 0})

	h.Responses(c)

	require.Equal(t, []int64{9912, 9912}, upstream.calls())
	require.Empty(t, providerRepo.rateLimitedIDs)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Equal(t, "1", rec.Header().Get("Retry-After"))
	require.Equal(t, "rate_limit_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.Equal(t, "Upstream rate limit exceeded, please retry later", gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
}

// TestOpenAIHTTPResourceBindingSharesImageCapacity binds supplied concurrency
// resources to the endpoint fixture. It checks shared object identities,
// capacity rejection across wrappers, and reacquisition after an idempotent
// double release.
func TestOpenAIHTTPResourceBindingSharesImageCapacity(t *testing.T) {
	resources := &gatewayhttp.OpenAIHTTPResources{
		Concurrency:  gatewayhttp.NewConcurrencyHelper(nil, gatewayhttp.SSEPingFormatNone, 0),
		Images:       &scheduler.ImageConcurrencyLimiter{},
		ImageOptions: &gatewayhttp.OpenAIImageAdmissionOptions{Enabled: true, Limit: 1},
	}
	cfg := &config.Config{Gateway: config.GatewayConfig{Images: config.GatewayImagesConfig{Concurrency: config.ImageConcurrencyConfig{
		Enabled:               true,
		MaxConcurrentRequests: 1,
	}}}}
	h := newGatewayHTTPEndpointsFromDeps(nil, nil, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, resources)
	require.Same(t, resources.Images, h.Input.Images)
	require.Same(t, resources.Concurrency, h.Input.Concurrency)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	release, ok := resources.AcquireImage(c, false)
	require.True(t, ok)
	require.NotNil(t, release)
	blocked, ok := h.httpResources().AcquireImage(c, false)
	require.False(t, ok)
	require.Nil(t, blocked)
	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	release()
	release()
	acquired, ok := h.httpResources().AcquireImage(c, false)
	require.True(t, ok)
	require.NotNil(t, acquired)
	acquired()
}

// GetByID returns an outer copy of the provider with the requested ID. Nested
// references remain shared; an absent ID returns ErrNoAvailableProviders so
// failover tests can distinguish missing rows.
func (r openAIResponsesFailoverProviderRepo) GetByID(_ context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	for i := range r.providers {
		if r.providers[i].Record.ID == id {
			provider := r.providers[i]
			return &provider, nil
		}
	}
	return nil, scheduler.ErrNoAvailableProviders
}

// ListSchedulableByGroupIDAndPlatform delegates to platform filtering and
// ignores group membership. It returns copied outer provider values with shared
// nested references, matching the fixture's platform-only selection setup.
func (r openAIResponsesFailoverProviderRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, _ int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.providersForPlatform(platform), nil
}

// ListSchedulableByPlatform returns the fixture's platform-filtered provider
// values. An empty filter includes every provider, and schedulability checks
// are left to consumers of these copied outer values.
func (r openAIResponsesFailoverProviderRepo) ListSchedulableByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.providersForPlatform(platform), nil
}

// ListSchedulableUngroupedByPlatform delegates to platform filtering without
// checking group membership. Its result keeps the same outer-copy and
// shared-nested-reference behavior as the other selection reads.
func (r openAIResponsesFailoverProviderRepo) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return r.providersForPlatform(platform), nil
}

// providersForPlatform allocates a result slice containing provider values that
// match the platform filter. Empty platform includes every fixture record;
// nested provider references remain shared with stored values.
func (r openAIResponsesFailoverProviderRepo) providersForPlatform(platform string) []gatewayprovider.ExecutionProvider {
	out := make([]gatewayprovider.ExecutionProvider, 0, len(r.providers))
	for _, provider := range r.providers {
		if platform == "" || provider.Record.Platform == platform {
			out = append(out, provider)
		}
	}
	return out
}

// Do records a provider call and returns an HTTP 520 fixture response. A
// configured onFirstDo callback runs for the first recorded call after
// unlocking; callers set that callback before concurrent transport use and
// close the returned response body.
//
// The mutex decides which call is first, while the callback can cancel the
// request independently of response construction. Cancellation therefore leaves
// the scripted response available for the gateway's post-forward handling. The
// callback runs outside the recording lock, so observation methods can read the
// call sequence while cancellation proceeds.
func (u *openAIResponsesFailoverCancelUpstream) Do(_ *http.Request, _ string, providerID int64, _ int) (*http.Response, error) {
	// Append the selected identity under the observation lock so concurrent
	// calls and copied snapshots agree on the recorded provider invocation order.
	u.mu.Lock()
	u.providerIDs = append(u.providerIDs, providerID)
	first := len(u.providerIDs) == 1
	u.mu.Unlock()
	// Invoke cancellation after releasing the call-recording lock so the
	// request can stop without blocking observations of the already recorded attempt.
	if first && u.onFirstDo != nil {
		u.onFirstDo()
	}
	return &http.Response{
		StatusCode: 520,
		Header:     http.Header{"Content-Type": []string{"text/html"}},
		Body:       io.NopCloser(bytes.NewBufferString("<html>520: unknown error</html>")),
	}, nil
}

// DoWithTLS delegates request execution to Do and ignores the TLS fingerprint
// profile. The returned scripted response has Do's provider-recording behavior,
// and the caller retains its response-body cleanup obligation.
func (u *openAIResponsesFailoverCancelUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	providerID int64,
	providerConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// calls returns a copy of the recorded provider-ID sequence under its mutex.
// The caller can inspect or modify the returned slice after unlocking without
// changing this transport's stored observations.
func (u *openAIResponsesFailoverCancelUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.providerIDs...)
}

// newOpenAIResponsesFailoverTestHandler assembles two OAuth providers against
// the supplied transport and a ten-switch budget. The first provider includes
// secret proxy credentials for log-redaction assertions, and test cleanup stops
// the started billing fixture.
func newOpenAIResponsesFailoverTestHandler(t *testing.T, upstream httpclient.UpstreamTransport) *gatewayHTTPEndpointsFixture {
	t.Helper()
	proxyID := int64(11)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 1,
				Name:        "responses-provider-1",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 0,
				Priority:    0,
				Credentials: map[string]any{"access_token": "token-1"},
				ProxyID:     &proxyID,
				Proxy: &egress.Proxy{
					ID:       proxyID,
					Name:     "responses-proxy",
					Protocol: "http",
					Host:     "proxy.example.com",
					Port:     8080,
					Username: "proxy-user-secret",
					Password: "proxy-password-secret",
				},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 2,
				Name:        "responses-provider-2",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 0,
				Priority:    1,
				Credentials: map[string]any{"access_token": "token-2"},
			},
		},
	}
	providerRepo := openAIResponsesFailoverProviderRepo{providers: providers}
	cfg := &config.Config{}
	gatewayService, gatewayServiceChoices, gatewayServiceCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo,
		nil,
		cfg,
		nil,
		nil,

		nil,

		upstream,
		nil,
		nil, newOpenAIExecutionCredentialsForTest(providerRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewayService.Recorder = newHTTPCompletionFixture(cfg, nil,

		nil,

		nil,

		nil,

		nil, nil, true)

	billingService := newBillingEligibilityFixture(cfg)
	billingService.Start()
	t.Cleanup(billingService.Stop)
	concurrencyService := scheduler.NewConcurrencyService(nil, scheduler.Diagnostics{
		Logf:  logging.LegacyPrintf,
		Event: logging.Event,
	},
	)
	handler := newGatewayHTTPEndpointsFromDeps(
		gatewayService, gatewayServiceCredentialPort,
		concurrencyService, newFundingAdmissionFixture(billingService, cfg), testkit.NewService(nil, nil, nil, nil, nil, nil, cfg),
		nil,
		nil,
		nil,
		nil,
		cfg, nil, newExecutionAvailabilityForTest(providerRepo,

			nil, cfg), gatewayServiceChoices,
	)
	handler.Input.MaxSwitches = 10
	return handler
}

// newOpenAIResponsesFailoverTestContext builds a fixed authenticated
// nonstreaming Responses request and recorder. A nonnil ctx replaces the
// request context, letting callers cancel upstream processing; nil keeps the
// httptest request context.
func newOpenAIResponsesFailoverTestContext(t *testing.T, ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	groupID := int64(3131)
	body := []byte(`{"model":"gpt-5.4","stream":false,"input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      99,
		GroupID: &groupID,
		Group: &routing.Group{
			ID: groupID,
		},
		User: &identity.User{ID: 100},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 100, Concurrency: 0})
	return c, rec
}

// TestOpenAIGatewayHandlerResponses_FailoverAbortsWhenClientDisconnected
// cancels the request during the first HTTP 520 response. It checks one
// provider call, client-closed classification, no response body or final
// upstream-status marker, and the retained failover event.
func TestOpenAIGatewayHandlerResponses_FailoverAbortsWhenClientDisconnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := &openAIResponsesFailoverCancelUpstream{onFirstDo: cancel}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	c, rec := newOpenAIResponsesFailoverTestContext(t, ctx)

	handler.Responses(c)

	require.Equal(t, []int64{1}, upstream.calls(), "客户端断开后不应再切换到提供商 2")
	require.Equal(t, gatewayhttp.StatusClientClosedRequest, c.Writer.Status(), "应按 499 归类")
	require.Zero(t, rec.Body.Len(), "不应写入 502 错误响应体")

	_, hasFinalUpstreamErr := c.Get(gatewayhttp.OpsUpstreamStatusCodeKey)
	require.False(t, hasFinalUpstreamErr, "不应记录 failover 耗尽的上游错误终态")

	// The failed attempt retains its HTTP 520 event even when cancellation
	// prevents exhausted-output handling, so attempt history and final status are checked separately.
	rawEvents, ok := c.Get(gatewayhttp.OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*opscore.OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "failover", events[0].Kind)
	require.Equal(t, 520, events[0].UpstreamStatusCode)
}

// TestOpenAIGatewayHandlerResponses_FailoverContinuesForConnectedClient returns
// HTTP 520 from two providers while the client remains connected. It checks
// HTTP 502, switching logs with public proxy attributes, and absent proxy
// credentials.
func TestOpenAIGatewayHandlerResponses_FailoverContinuesForConnectedClient(t *testing.T) {
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	upstream := &openAIResponsesFailoverCancelUpstream{}
	handler := newOpenAIResponsesFailoverTestHandler(t, upstream)
	c, rec := newOpenAIResponsesFailoverTestContext(t, nil)

	handler.Responses(c)

	require.Equal(t, []int64{1, 2}, upstream.calls(), "在线客户端应正常切换提供商")
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
	require.True(t, logSink.ContainsMessageAtLevel("openai.upstream_failover_switching", "warn"))
	require.True(t, logSink.ContainsFieldValue("proxy_id", "11"))
	require.True(t, logSink.ContainsFieldValue("proxy_name", "responses-proxy"))
	require.True(t, logSink.ContainsFieldValue("proxy_host", "proxy.example.com"))
	require.True(t, logSink.ContainsFieldValue("proxy_port", "8080"))
	require.False(t, logSink.ContainsFieldValue("proxy_username", "proxy-user-secret"))
	require.False(t, logSink.ContainsFieldValue("proxy_password", "proxy-password-secret"))
}

// newServiceTierHandlerTest builds an empty OpenAI execution fixture with one
// available user-slot grant. It supplies funding, key, and image components for
// request validation before usable upstream execution is configured.
func newServiceTierHandlerTest(t *testing.T) *gatewayHTTPEndpointsFixture {
	t.Helper()
	return newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:  &gatewayExecutionFixture{},
		Funding: newFundingAdmissionFixture(newBillingEligibilityFixture(&config.Config{}), &config.Config{}),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(
			&httptestkit.ConcurrencySequence{UserSeq: []bool{true}}, scheduler.Diagnostics{
				Logf:  logging.LegacyPrintf,
				Event: logging.Event,
			},
		), gatewayhttp.SSEPingFormatNone, 0),
		Config: &config.Config{},
		Images: &scheduler.ImageConcurrencyLimiter{}, Availability: newExecutionAvailabilityForTest(nil, nil, nil), Choices: newEmptyCompatibleSelectionFixture(),
	})
}

// runOpenAIHandlerServiceTierTest invokes the supplied endpoint callback with a
// JSON request and fixed authenticated group. It returns the response recorder
// after synchronous handler execution so callers can inspect validation errors.
func runOpenAIHandlerServiceTierTest(t *testing.T, path, body string, handler func(h *gatewayHTTPEndpointsFixture, c *gin.Context)) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(6401)
	userID := int64(6402)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      6403,
		GroupID: &groupID,
		Group: &routing.Group{
			ID: groupID,
		},
		User: &identity.User{ID: userID, Status: billing.StatusActive},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: userID, Concurrency: 1})

	handler(newServiceTierHandlerTest(t), c)
	return rec
}

// TestOpenAIGatewayHandlerResponses_InvalidServiceTierRejected400 submits
// unknown, empty, and incorrectly typed Responses service tiers. It checks HTTP
// 400 with invalid-request type and service_tier guidance for every body.
func TestOpenAIGatewayHandlerResponses_InvalidServiceTierRejected400(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.5","input":"hi","service_tier":"turbo"}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":"SPEED"}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":""}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":123}`,
		`{"model":"gpt-5.5","input":"hi","service_tier":{}}`,
	} {
		rec := runOpenAIHandlerServiceTierTest(t, "/v1/responses", body, func(h *gatewayHTTPEndpointsFixture, c *gin.Context) {
			h.Responses(c)
		})
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid_request_error", "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid service_tier", "body=%s", body)
	}
}

// TestOpenAIGatewayHandlerChatCompletions_InvalidServiceTierRejected400 submits
// unsupported, empty, and array-valued Chat service tiers. It checks HTTP 400
// with invalid-request type and service_tier guidance for every body.
func TestOpenAIGatewayHandlerChatCompletions_InvalidServiceTierRejected400(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":"turbo"}`,
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":"ultra"}`,
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":""}`,
		`{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}],"service_tier":["priority"]}`,
	} {
		rec := runOpenAIHandlerServiceTierTest(t, "/v1/chat/completions", body, func(h *gatewayHTTPEndpointsFixture, c *gin.Context) {
			h.ChatCompletions(c)
		})
		require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid_request_error", "body=%s", body)
		require.Contains(t, rec.Body.String(), "invalid service_tier", "body=%s", body)
	}
}

// TestOpenAICompatibleHandlersRejectInvalidStreamFieldType submits string and
// numeric stream values to both compatible and OpenAI handlers. It checks HTTP
// 400 and the shared invalid-stream-type message at each endpoint.
func TestOpenAICompatibleHandlersRejectInvalidStreamFieldType(t *testing.T) {
	tests := []struct {
		name string             // Stream validation case name.
		path string             // Client endpoint request path.
		body string             // JSON client request body.
		run  func(*gin.Context) // Endpoint handler invocation callback.
	}{
		{
			name: "gateway_responses_string_stream",
			path: "/v1/responses",
			body: `{"model":"gpt-5","stream":"true","input":"hello"}`,
			run: func(c *gin.Context) {
				newMessageEndpointsFixture(nil, nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil, nil).Responses(c)
			},
		},
		{
			name: "gateway_responses_number_stream",
			path: "/v1/responses",
			body: `{"model":"gpt-5","stream":1,"input":"hello"}`,
			run: func(c *gin.Context) {
				newMessageEndpointsFixture(nil, nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil, nil).Responses(c)
			},
		},
		{
			name: "gateway_chat_completions_string_stream",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-5","stream":"true","messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				newMessageEndpointsFixture(nil, nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil, nil).ChatCompletions(c)
			},
		},
		{
			name: "gateway_chat_completions_number_stream",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-5","stream":1,"messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				newMessageEndpointsFixture(nil, nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, nil, nil).ChatCompletions(c)
			},
		},
		{
			name: "openai_responses_string_stream",
			path: "/openai/v1/responses",
			body: `{"model":"gpt-5","stream":"true","input":"hello"}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).Responses(c)
			},
		},
		{
			name: "openai_responses_number_stream",
			path: "/openai/v1/responses",
			body: `{"model":"gpt-5","stream":1,"input":"hello"}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).Responses(c)
			},
		},
		{
			name: "openai_chat_completions_string_stream",
			path: "/openai/v1/chat/completions",
			body: `{"model":"gpt-5","stream":"true","messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).ChatCompletions(c)
			},
		},
		{
			name: "openai_chat_completions_number_stream",
			path: "/openai/v1/chat/completions",
			body: `{"model":"gpt-5","stream":1,"messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				newOpenAIHandlerForPreviousResponseIDValidation(t, nil).ChatCompletions(c)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newOpenAICompatibleStreamValidationContext(tt.path, tt.body, false)

			tt.run(c)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, gatewayhttp.InvalidStreamFieldTypeMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
			require.Contains(t, rec.Body.String(), "invalid_request_error")
		})
	}
}

// TestGatewayOpenAICompatibleHandlersAllowBooleanStreamToContinue submits
// boolean stream values to compatible endpoints with a Claude-Code-only group.
// It checks the later HTTP 403 policy rejection, showing that stream validation
// passed.
func TestGatewayOpenAICompatibleHandlersAllowBooleanStreamToContinue(t *testing.T) {
	tests := []struct {
		name string             // Stream validation case name.
		path string             // Client endpoint request path.
		body string             // JSON client request body.
		run  func(*gin.Context) // Endpoint handler invocation callback.
	}{
		{
			name: "responses_false",
			path: "/v1/responses",
			body: `{"model":"gpt-5","stream":false,"input":"hello"}`,
			run: func(c *gin.Context) {
				newMessageEndpointsFixture(&messageExecutionFixture{Routes: gatewayprovider.NewRoutePlanner(nil)}, nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(nil, nil, nil), newEmptyGenericSelectionFixture()).Responses(c)
			},
		},
		{
			name: "chat_completions_true",
			path: "/v1/chat/completions",
			body: `{"model":"gpt-5","stream":true,"messages":[{"role":"user","content":"hello"}]}`,
			run: func(c *gin.Context) {
				newMessageEndpointsFixture(&messageExecutionFixture{Routes: gatewayprovider.NewRoutePlanner(nil)}, nil, nil, nil, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 0, MaxGeminiSwitches: 0}, newExecutionAvailabilityForTest(nil, nil, nil), newEmptyGenericSelectionFixture()).ChatCompletions(c)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, rec := newOpenAICompatibleStreamValidationContext(tt.path, tt.body, true)

			tt.run(c)

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Contains(t, rec.Body.String(), routing.ErrClaudeCodeOnly.Error())
		})
	}
}

// newOpenAICompatibleStreamValidationContext builds an authenticated JSON
// request with a fixed group and recorder. claudeCodeOnly controls that group's
// client policy so tests can distinguish body validation from later policy
// rejection.
func newOpenAICompatibleStreamValidationContext(path, body string, claudeCodeOnly bool) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	groupID := int64(7)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		ID:      11,
		GroupID: &groupID,
		Group:   &routing.Group{ID: groupID, ClaudeCodeOnly: claudeCodeOnly},
		User:    &identity.User{ID: 13},
	})
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 13, Concurrency: 1})

	return c, rec
}

// TestOpenAITextAssemblyReadAndStopBoundaries invokes assembled Responses,
// Chat, and Messages handlers with missing dependencies, then stopped activity.
// It checks distinct service-unavailable messages and unread bodies in both
// states.
func TestOpenAITextAssemblyReadAndStopBoundaries(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("openai-text-contract")}
	h := provideOpenAITextHTTP(nil, nil, nil, nil, nil, nil, nil, nil, nil, provideOpenAITextAttemptRuntime(provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil, nil, nil, nil, nil, nil, nil, nil)), activity, nil, nil, nil, nil)
	for _, stopped := range []bool{false, true} {
		if stopped {
			require.NoError(t, activity.StopContext(context.Background()))
		}
		for name, entry := range map[string]gin.HandlerFunc{"responses": h.Responses, "chat": h.ChatCompletions, "messages": h.Messages} {
			t.Run(name+map[bool]string{false: "-running", true: "-stopped"}[stopped], func(t *testing.T) {
				writer := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(writer)
				body := &protocolGateTrackingReader{}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+name, body)
				c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7})
				c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
				entry(c)
				require.Equal(t, http.StatusServiceUnavailable, writer.Code)
				require.False(t, body.read)
				if stopped {
					require.Contains(t, writer.Body.String(), "Service is shutting down")
				} else {
					require.Contains(t, writer.Body.String(), "Service temporarily unavailable")
				}
			})
		}
	}
}

// Current returns no searcher for the mixed-platform execution fixture.
func (mixedHTTPNoSearch) Current() searchtools.Searcher { return nil }

// TestMixedGroupTextHTTP sends Messages, Responses, and Chat requests to
// Anthropic, OpenAI, Gemini, or an Anthropic-to-OpenAI failover case. It checks
// converted client text, provider call order, one settlement and log write, and
// final provider identity.
func TestMixedGroupTextHTTP(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/responses", "/v1/chat/completions"} {
		for _, platform := range []string{"anthropic", "openai", "gemini", "failover"} {
			t.Run(path+"/"+platform, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { mixedUpstreamResponse(w, r, platform == "failover") }))
				defer upstream.Close()
				groupID := int64(42)
				group := &routing.Group{ID: groupID, Hydrated: true, Name: "mixed", Status: "active", RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions}}
				cfg := &config.Config{}
				cfg.Billing.FallbackRateMultiplier = 1
				cfg.Security.URLAllowlist.Enabled = false
				cfg.Security.URLAllowlist.AllowInsecureHTTP = true
				cfg.Gateway.Failover.MaxProviderSwitches = 2
				providers := &mixedHTTPProviders{}
				for index, p := range []string{"anthropic", "openai", "gemini"} {
					record := &providercore.Record{ID: int64(index + 1), Name: p, Platform: p, Type: "apikey", Status: "active", Schedulable: true, Priority: index + 1, Concurrency: 1, GroupIDs: []int64{groupID}, Credentials: map[string]any{"api_key": "test-key", "base_url": upstream.URL}}
					// Declare each provider model before selection so a mixed group
					// can choose the intended platform through its production model policy.
					record.Credentials["model_whitelist"] = []string{map[string]string{"anthropic": "claude-sonnet-4-5-20250929", "openai": "gpt-5.4", "gemini": "gemini-2.5-flash"}[p]}
					if p == "openai" && platform == "failover" {
						// Map the failed Anthropic model to the OpenAI provider model
						// so the failover attempt can execute after cross-platform selection.
						record.Credentials["model_mapping"] = map[string]any{"claude-sonnet-4-5-20250929": "gpt-5.4"}
					}
					providers.values = append(providers.values, *gatewayprovider.NewExecutionProvider(record))
				}
				transport := &mixedHTTPTransport{}
				source, choices, credentials := newOpenAIExecutionAndSelectionFixture(providers, nil, cfg, nil, nil, nil, transport, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
				logs := &gatewaytestkit.UsageLogStore{Inserted: true}
				funds := &gatewaytestkit.SettlementStore{}
				recording := gatewaytestkit.NewRecording(logs, funds, nil, false)
				recording.Options.DefaultMultiplier = 1
				source.Recorder = recording.Core(nil, true)
				native := &gatewayhttp.UnifiedTextExecutor{OpenAI: source.Responses, Anthropic: gatewayhttp.NewMessagesExecutor(messageforward.NewRuntime(messageforward.Dependencies{Credentials: &providercore.MessageCredentialSource{}, Transport: transport, Deferred: &providercore.DeferredService{}, Search: searchtools.NewEmulator(mixedHTTPNoSearch{}, nil, nil, nil, nil, nil)}, messageforward.Options{Configured: true, AllowInsecureHTTP: true, ResponseReadLimit: 1 << 20}), nil), Gemini: &gatewayhttp.GeminiExecutor{Runtime: &googleforward.Gemini{Transport: transport, Options: googleforward.Options{Configured: true, AllowInsecureHTTP: true, ResponseReadLimit: 1 << 20}}}}
				eligibility := newBillingEligibilityFixture(cfg)
				t.Cleanup(eligibility.Stop)
				resources := provideOpenAIHTTPResources(scheduler.NewConcurrencyService(nil), cfg)
				endpoints := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{Source: source, Native: native, Credentials: credentials, Choices: choices, Config: cfg, Funding: newFundingAdmissionFixture(eligibility, cfg), Keys: testkit.NewService(nil, nil, nil, nil, nil, nil, cfg), Concurrency: resources.Concurrency, MaxSwitches: 2})
				user := &identity.User{ID: 7, Balance: 100, Status: "active"}
				key := &apikey.APIKey{ID: 8, UserID: 7, User: user, GroupID: &groupID, Group: group}
				router := gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(keyhttp.ContextKeyAPIKey), key)
					c.Set("gateway_effective_key", key)
					authctx.SetPrincipal(c, identity.Principal{UserID: 7}, 0, "")
					sourceProtocol := protocol.ProtocolAnthropicMessages
					if path == "/v1/responses" {
						sourceProtocol = protocol.ProtocolOpenAIResponses
					}
					if path == "/v1/chat/completions" {
						sourceProtocol = protocol.ProtocolOpenAIChatCompletions
					}
					c.Request = c.Request.WithContext(requeststate.WithClientProtocol(requeststate.WithGroup(c.Request.Context(), group), sourceProtocol))
				})
				router.POST("/v1/messages", endpoints.Messages)
				router.POST("/v1/responses", endpoints.Responses)
				router.POST("/v1/chat/completions", endpoints.ChatCompletions)
				model := map[string]string{"anthropic": "claude-sonnet-4-5-20250929", "openai": "gpt-5.4", "gemini": "gemini-2.5-flash", "failover": "claude-sonnet-4-5-20250929"}[platform]
				body := fmt.Sprintf(`{"model":%q,"max_tokens":20,"messages":[{"role":"user","content":"hello"}],"stream":false}`, model)
				if path == "/v1/responses" {
					body = fmt.Sprintf(`{"model":%q,"input":"hello","stream":false}`, model)
				}
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				request.Header.Set("Content-Type", "application/json")
				router.ServeHTTP(response, request)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "ok-mixed")
				expected := map[string]int64{"anthropic": 1, "openai": 2, "gemini": 3, "failover": 2}[platform]
				calls := transport.calls()
				require.NotEmpty(t, calls)
				require.Equal(t, expected, calls[len(calls)-1])
				if platform == "failover" {
					require.Equal(t, []int64{1, 2}, calls)
				} else {
					require.Len(t, calls, 1)
				}
				require.Equal(t, 1, funds.Calls, "一次成功只能提交一次资金扣费")
				require.Equal(t, 1, logs.Calls)
				require.Equal(t, expected, logs.LastLog.ProviderID)
				require.Equal(t, map[int64]string{1: "anthropic", 2: "openai", 3: "gemini"}[expected], logs.LastLog.Platform)
			})
		}
	}
}
