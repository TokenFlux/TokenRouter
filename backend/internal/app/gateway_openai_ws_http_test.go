package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	httptestkit "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/testkit"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	wshttp "github.com/TokenFlux/TokenRouter/internal/gateway/ws/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/testutil"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// A Responses WebSocket can fail before upgrade, during its first request, or
// between later turns on the same connection. These cases distinguish HTTP
// admission errors from WebSocket close reasons, verify provider switching
// before useful output, and check which request metadata reaches completion
// recording.
//
// The upstream fixtures publish payloads and completion signals through
// buffered channels so assertions can observe forwarding without reading
// handler state concurrently. Turn fixtures retain the initial connection
// identity while exposing mutable key and group repositories to later
// authentication checks. Tests wait for terminal events, recorded warnings, and
// handler exit separately because those observations describe different stages;
// a successful read alone does not establish that cleanup or risk-control
// persistence has finished.

// openAIResponsesWSUsageLogCase supplies a first frame and optional recording
// inputs.
type openAIResponsesWSUsageLogCase struct {
	firstPayload string // First client request frame.
	// userAgent supplies the dial header when nonnil; an empty string explicitly
	// tests completion recording without a usable agent value.
	userAgent *string
	// groupMapping enables pricing configuration when nonempty and maps requested
	// model names before the frame reaches the upstream fixture.
	groupMapping map[string]string
}

// openAIResponsesWSUsageLogResult pairs recorded usage with the upstream
// request frame.
type openAIResponsesWSUsageLogResult struct {
	// log is the original pointer published by Create after the terminal event;
	// assertions inspect the recorder result rather than a copied fixture value.
	log                  *usage.UsageLog
	upstreamFirstPayload []byte // First frame received upstream.
}

// openAIWSUsageHandlerProviderRepoStub exposes one execution provider to
// WebSocket selection.
type openAIWSUsageHandlerProviderRepoStub struct {
	// ExecutionProviderStore supplies unimplemented methods through embedding;
	// tests must stay within the selection and lookup methods overridden below.
	gatewayprovider.ExecutionProviderStore

	// provider supplies copied outer values to selection and lookup; nested
	// credentials and extra maps remain shared with the stored fixture record.
	provider gatewayprovider.ExecutionProvider
}

// openAIWSUsageHandlerUsageLogRepoStub captures completion writes through an
// optional channel.
type openAIWSUsageHandlerUsageLogRepoStub struct {
	// UsageLogRepository supplies unimplemented methods through embedding;
	// this fixture only overrides creation used by completion recording.
	usage.UsageLogRepository
	// created receives shared log pointers when nonnil; callers must buffer or
	// drain it because Create sends without selecting on context cancellation.
	created chan *usage.UsageLog
}

// openAIWSUsageHandlerPricingConfigRepoStub supplies model mapping and group
// platform fixtures.
type openAIWSUsageHandlerPricingConfigRepoStub struct {
	// PricingConfigRepository supplies unimplemented methods through embedding;
	// tests use only the configuration and platform reads implemented below.
	routing.PricingConfigRepository
	// modelConfigs is returned directly by ListAll, including its backing array
	// and nested mapping values, so readers must avoid unintended fixture mutation.
	modelConfigs   []routingtestkit.Configuration
	groupPlatforms map[int64]string // Stored group platform names.
}

// openAIWSPassthroughHandlerHarness exposes an upgraded client and mutable
// per-turn authentication fixtures.
type openAIWSPassthroughHandlerHarness struct {
	// clientConn is already upgraded; test cleanup closes it before the local
	// handler server so a waiting handler can observe connection termination.
	clientConn *coderws.Conn
	// handlerDone closes after ResponsesWebSocket returns, allowing assertions to
	// distinguish a client close event from completion of handler cleanup.
	handlerDone <-chan struct{}
	// Recorded moderation warning store.
	moderationRepo *contentModerationHandlerTestRepo
	// gatewayCache stores session blocks in the Redis test fixture, whose
	// registered cleanup owns the Redis client and supporting test server.
	gatewayCache session.GatewayCache
	// apiKey supplies the initial connection identity; keys and groups expose
	// separately mutable repositories that later turns reread to check access.
	apiKey *apikey.APIKey // Initial authenticated key snapshot.
	keys   *wsTurnKeys    // Mutable authentication key repository.
	groups *wsTurnGroups  // Mutable group permission repository.
}

// wsTurnKeys stores the authentication record reread between WebSocket turns.
// Its mutex protects replacement and copying while the established connection
// retains its earlier authentication snapshot.
type wsTurnKeys struct {
	// APIKeyRepository supplies unimplemented methods through embedding; tests
	// use only the authentication lookup overridden by this mutable fixture.
	apikey.APIKeyRepository
	// mu protects key reads and replacement; GetByKeyForAuth copies key while
	// holding mu so revocation cannot interleave with publication of that copy.
	mu  sync.Mutex     // Key record access lock.
	key *apikey.APIKey // Current authentication key record.
}

// wsTurnGroups stores protocol permissions reread between WebSocket turns. Its
// mutex protects permission replacement and the copied slice returned to
// authentication.
type wsTurnGroups struct {
	// GroupRepository supplies unimplemented methods through embedding; tests
	// use only the lightweight group lookup overridden by this fixture.
	routing.GroupRepository
	// mu protects group reads and replacement of AllowedProtocols; GetByIDLite
	// copies group and its protocol slice while holding mu before publication.
	mu    sync.Mutex    // Group permission access lock.
	group routing.Group // Current group permission record.
}

// TestResponsesWebSocketCredentialFailoverLoop exercises revoked credentials,
// invalid provider configuration, and cancellation during refresh. It checks
// healthy-provider output or close reasons together with selection,
// error-recording, and upstream-hit counts.
func TestResponsesWebSocketCredentialFailoverLoop(t *testing.T) {
	dial := func(t *testing.T, router *gin.Engine) (*coderws.Conn, func()) {
		t.Helper()
		server := httptest.NewServer(router)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", nil)
		cancel()
		require.NoError(t, err)
		return conn, func() {
			// Terminate the client before closing the local server so a waiting
			// handler can leave its socket read; teardown errors are discarded.
			_ = conn.CloseNow()
			server.Close()
		}
	}
	writeFirst := func(t *testing.T, conn *coderws.Conn) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"grok","input":"hello","stream":false}`)))
	}

	t.Run("revoked provider selects healthy provider", func(t *testing.T) {
		_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "revoked")
		defer cleanup()
		conn, closeConn := dial(t, router)
		defer closeConn()
		writeFirst(t, conn)

		readCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, payload, err := conn.Read(readCtx)
		cancel()
		require.NoError(t, err)
		require.Contains(t, string(payload), "resp_healthy")
		require.Equal(t, []int64{801}, repo.errorIDs())
		require.Equal(t, 2, repo.selectorCalls())
		require.Equal(t, []int64{802}, upstream.providerHits())
	})

	t.Run("provider configuration stops", func(t *testing.T) {
		_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "provider")
		defer cleanup()
		conn, closeConn := dial(t, router)
		defer closeConn()
		writeFirst(t, conn)

		readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _, err := conn.Read(readCtx)
		cancel()
		var closeErr coderws.CloseError
		require.ErrorAs(t, err, &closeErr)
		require.Contains(t, closeErr.Reason, forwardcore.GrokCredentialUnavailableClientMessage)
		require.Equal(t, 1, repo.selectorCalls())
		require.Empty(t, upstream.providerHits())
	})

	t.Run("parent cancellation prevents reselection", func(t *testing.T) {
		_, repo, upstream, router, cleanup := newGrokCredentialFailoverHandler(t, "cancel")
		defer cleanup()
		conn, closeConn := dial(t, router)
		writeFirst(t, conn)
		// Wait until refresh has entered the cancellable path before closing
		// the client; otherwise cancellation could precede the operation being tested.
		select {
		case <-findHandlerRefresherStarted(router):
		case <-time.After(2 * time.Second):
			t.Fatal("credential refresh did not start")
		}
		// Closing both client and server interrupts refresh through request
		// cancellation before the assertions inspect reselection and provider errors.
		closeConn()

		require.Eventually(t, func() bool { return repo.selectorCalls() == 1 }, 2*time.Second, 20*time.Millisecond)
		require.Empty(t, repo.errorIDs())
		require.Empty(t, upstream.providerHits())
	})
}

// TestOpsWebSocketCredentialFailoverSuccessDoesNotCreateRequestError supplies a
// provider-auth event to an otherwise successful upgrade-shaped request. It
// checks a captured HTTP 200 Ops entry retaining the upstream event and its
// phase.
func TestOpsWebSocketCredentialFailoverSuccessDoesNotCreateRequestError(t *testing.T) {
	queue := newOpsCaptureQueue(2)

	ops := opscore.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	router := gin.New()
	router.Use(gatewayhttp.OpsErrorLoggerMiddleware(ops, queue, gatewayhttp.OpsObservationAccess{}))
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		c.Set(gatewayhttp.OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
			Stage: string(forwardcore.GatewayFailureStageProviderAuth), Scope: string(forwardcore.GatewayFailureScopeProvider),
			Reason: string(forwardcore.GrokCredentialReasonRevoked), Message: "Grok OAuth credentials require provider action",
		}})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), queue.health.Length)
	job := <-queue.jobs
	require.Equal(t, http.StatusOK, job.entry.StatusCode)
	require.Equal(t, string(forwardcore.GatewayFailureStageProviderAuth), job.entry.ErrorPhase)
	require.NotNil(t, job.entry.UpstreamErrorsJSON)
	events, err := opscore.ParseOpsUpstreamErrors(*job.entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, string(forwardcore.GatewayFailureStageProviderAuth), events[0].Stage)
}

// TestOpsWebSocketCredentialFailoverExhaustedIsRecorded presents exhausted
// credentials without a live socket. It checks that Ops captures
// service-unavailable details even though the outer recorder remains HTTP 200.
func TestOpsWebSocketCredentialFailoverExhaustedIsRecorded(t *testing.T) {
	queue := newOpsCaptureQueue(2)

	ops := opscore.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	router := gin.New()
	router.Use(gatewayhttp.OpsErrorLoggerMiddleware(ops, queue, gatewayhttp.OpsObservationAccess{}))
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		c.Set(gatewayhttp.OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
			Stage: string(forwardcore.GatewayFailureStageProviderAuth), Scope: string(forwardcore.GatewayFailureScopeProvider),
			Reason: string(forwardcore.GrokCredentialReasonRevoked), Message: "Grok OAuth credentials require provider action",
		}})
		closeOpenAIWSFailoverExhausted(c, nil, &forwardcore.UpstreamFailoverError{
			Stage:              forwardcore.GatewayFailureStageProviderAuth,
			Scope:              forwardcore.GatewayFailureScopeProvider,
			Reason:             forwardcore.GrokCredentialReasonRevoked,
			NextProviderAction: forwardcore.NextProviderStop,
		})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), queue.health.Length)
	job := <-queue.jobs
	require.Equal(t, "provider_auth", job.entry.ErrorPhase)
	require.Equal(t, http.StatusServiceUnavailable, job.entry.StatusCode)
	require.Equal(t, forwardcore.GrokCredentialUnavailableClientMessage, job.entry.ErrorMessage)
}

// TestOpenAIResponsesWebSocket_SetsClientTransportWSWhenUpgradeValid sends
// an unauthenticated GET request with upgrade headers to Responses. It checks
// HTTP Unauthorized while the request still retains the WebSocket
// client-transport marker.
func TestOpenAIResponsesWebSocket_SetsClientTransportWSWhenUpgradeValid(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	c.Request.Header.Set("Upgrade", "websocket")
	c.Request.Header.Set("Connection", "Upgrade")

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.ResponsesWebSocket(c)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Equal(t, gatewayhttp.OpenAIClientTransportWS, gatewayhttp.GetOpenAIClientTransport(c))
}

// TestOpenAIResponsesWebSocket_InvalidUpgradeDoesNotSetTransport sends an
// ordinary HTTP request to the WebSocket entry. It checks upgrade-required
// status and an unchanged unknown transport marker.
func TestOpenAIResponsesWebSocket_InvalidUpgradeDoesNotSetTransport(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)

	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{})
	h.ResponsesWebSocket(c)

	require.Equal(t, http.StatusUpgradeRequired, w.Code)
	require.Equal(t, gatewayhttp.OpenAIClientTransportUnknown, gatewayhttp.GetOpenAIClientTransport(c))
}

// TestOpenAIResponsesWebSocket_IngressCapacityRejected scripts an ingress-lease
// refusal before upgrade. It checks dialing fails with no established
// connection and exposes TooManyRequests in the returned HTTP response.
func TestOpenAIResponsesWebSocket_IngressCapacityRejected(t *testing.T) {
	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return false, nil
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, response, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.Error(t, err)
	require.Nil(t, clientConn)
	require.NotNil(t, response)
	require.Equal(t, http.StatusTooManyRequests, response.StatusCode)
	// Admission status is already captured; discard body-close errors because
	// this assertion concerns the HTTP rejection rather than transport teardown.
	_ = response.Body.Close()
}

// TestOpenAIResponsesWebSocket_IngressLeaseBackendUnavailableBeforeUpgrade
// makes ingress admission return a backend error. It checks that dialing fails
// with an HTTP service-unavailable response before a socket is established.
func TestOpenAIResponsesWebSocket_IngressLeaseBackendUnavailableBeforeUpgrade(t *testing.T) {
	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return false, errors.New("redis unavailable")
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, response, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.Error(t, err)
	require.Nil(t, clientConn)
	require.NotNil(t, response)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	// Admission status is already captured; discard body-close errors because
	// this assertion concerns the HTTP rejection rather than transport teardown.
	_ = response.Body.Close()
}

// TestOpenAIResponsesWebSocket_FirstMessageTimeoutUsesConfig opens a connection
// without sending its first frame. It checks bounded read failure and the
// configured one-second timeout in the captured warning.
func TestOpenAIResponsesWebSocket_FirstMessageTimeoutUsesConfig(t *testing.T) {
	logSink, restore := captureHandlerStructuredLog(t)
	defer restore()

	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAI.ResponsesWS.ClientFirstMessageTimeoutSeconds = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() { _ = clientConn.CloseNow() }()

	started := time.Now()
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	elapsed := time.Since(started)

	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	// The tolerance permits scheduling variation while distinguishing the configured
	// first-frame timeout from immediate rejection or the longer client read deadline.
	require.GreaterOrEqual(t, elapsed, 500*time.Millisecond)
	require.Less(t, elapsed, 4*time.Second)
	require.Eventually(t, func() bool {
		readTimeout, ok := logSink.FieldValueForMessage("openai.websocket_read_first_message_failed", "read_timeout")
		return ok && readTimeout == time.Second &&
			logSink.ContainsMessageAtLevel("openai.websocket_read_first_message_failed", "warn")
	}, time.Second, 10*time.Millisecond)
}

// TestOpenAIResponsesWebSocket_IngressLeaseReleasedOnEarlyReturn sends an
// invalid binary first frame after ingress admission. It checks a
// policy-violation close and eventual release of the claimed ingress lease.
func TestOpenAIResponsesWebSocket_IngressLeaseReleasedOnEarlyReturn(t *testing.T) {
	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageBinary, []byte("not a response.create frame"))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.ReleaseIngressCalled) == 1
	}, time.Second, 10*time.Millisecond)
}

// TestOpenAIResponsesWebSocket_IngressLeaseReleasedWhenUpgradeFails omits the
// required WebSocket key after ingress admission. It checks failed upgrade
// status and eventual release of the ingress lease.
func TestOpenAIResponsesWebSocket_IngressLeaseReleasedWhenUpgradeFails(t *testing.T) {
	cache := &httptestkit.ConcurrencyHooks{
		AcquireIngressLeaseFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	h.Input.Config = &config.Config{}
	h.Input.Config.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey = 1
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	req, err := http.NewRequest(http.MethodGet, wsServer.URL+"/openai/v1/responses", nil)
	require.NoError(t, err)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Version", "13")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	// Upgrade failure is inspected through status and lease release; body-close
	// errors are discarded after the response has supplied those observations.
	_ = resp.Body.Close()
	require.NotEqual(t, http.StatusSwitchingProtocols, resp.StatusCode)
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&cache.ReleaseIngressCalled) == 1
	}, time.Second, 10*time.Millisecond)
}

// TestOpenAIResponsesWebSocket_RejectsMessageIDAsPreviousResponseID supplies a
// message identifier where a response identifier is required. It checks a
// policy-violation close mentioning the invalid previous_response_id.
func TestOpenAIResponsesWebSocket_RejectsMessageIDAsPreviousResponseID(t *testing.T) {
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, nil)
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(
		`{"type":"response.create","model":"gpt-5.4","stream":false,"previous_response_id":"msg_abc123"}`,
	))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	require.Error(t, err)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	require.Contains(t, strings.ToLower(closeErr.Reason), "previous_response_id")
}

// TestOpenAIResponsesWebSocket_PreviousResponseIDKindLoggedBeforeAcquireFailure
// supplies a valid response identifier and fails user-slot acquisition. It
// checks the internal-error close reason from concurrency admission after
// first-frame validation.
func TestOpenAIResponsesWebSocket_PreviousResponseIDKindLoggedBeforeAcquireFailure(t *testing.T) {
	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
			return false, errors.New("user slot unavailable")
		},
	}
	h := newOpenAIHandlerForPreviousResponseIDValidation(t, cache)
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(
		`{"type":"response.create","model":"gpt-5.4","stream":false,"previous_response_id":"resp_prev_123"}`,
	))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = clientConn.Read(readCtx)
	cancelRead()
	require.Error(t, err)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusInternalError, closeErr.Code)
	require.Contains(t, strings.ToLower(closeErr.Reason), "failed to acquire user concurrency slot")
}

// TestOpenAIResponsesWebSocket_ContentModerationBlocksFirstFrame returns a
// flagged moderation result for the first request. It checks the configured
// block message and a persisted flagged log containing the submitted prompt.
func TestOpenAIResponsesWebSocket_ContentModerationBlocksFirstFrame(t *testing.T) {
	moderationServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/moderations", r.URL.Path)
		// Supply the flagged moderation response; the downstream policy and log
		// assertions expose an unsuccessful exchange without a separate fixture write assertion.
		_, _ = w.Write([]byte(`{"results":[{"category_scores":{"sexual":0.9}}]}`))
	}))
	defer moderationServer.Close()

	cfg := &moderation.ContentModerationConfig{
		Enabled:      true,
		Mode:         moderation.ContentModerationModePreBlock,
		BaseURL:      moderationServer.URL,
		Model:        "omni-moderation-latest",
		APIKeys:      []string{"sk-test"},
		SampleRate:   100,
		AllGroups:    true,
		BlockMessage: "内容审计测试阻断",
	}
	rawCfg, err := json.Marshal(cfg)
	require.NoError(t, err)

	repo := &contentModerationHandlerTestRepo{}
	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:      "true",
		moderation.SettingKeyContentModerationConfig: string(rawCfg),
	}}
	moderationSvc := newHTTPModeration(t, settingRepo,
		repo,
	)
	moderationSvc.Start()
	decision, err := moderationSvc.Check(context.Background(), moderation.ContentModerationCheckInput{
		UserID:   1,
		Endpoint: "/v1/responses",
		Provider: "openai",
		Model:    "gpt-5.5",
		Protocol: moderation.ContentModerationProtocolOpenAIResponses,
		Body:     []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"bad prompt"}]}]}`),
	})
	require.NoError(t, err)
	require.True(t, decision.Blocked)
	require.Eventually(t, func() bool {
		return len(repo.logSnapshot()) == 1
	}, time.Second, 10*time.Millisecond)
	// Discard the completed direct-check log before exercising ingress so the
	// next single-log assertion identifies the WebSocket request rather than setup work.
	repo.resetLogs()
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source:    &gatewayExecutionFixture{},
		Funding:   &admission.FundingAdmission{},
		Keys:      &apikey.APIKeyService{},
		Moderator: moderationSvc,
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(&httptestkit.ConcurrencyHooks{}, scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, time.Second), Availability: newExecutionAvailabilityForTest(nil, nil, nil), Choices: newEmptyCompatibleSelectionFixture(),
	})
	wsServer := newOpenAIWSHandlerTestServer(t, h, authctx.AuthSubject{UserID: 1, Concurrency: 1})
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{
		"type":"response.create",
		"model":"gpt-5.5",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"bad prompt"}]}]
	}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, payload, readErr := clientConn.Read(readCtx)
	cancelRead()
	// Blocking may arrive as an error frame or a policy close; both paths
	// must expose the configured message before the persisted moderation log is checked.
	if readErr == nil {
		require.Contains(t, string(payload), "content_policy_violation")
		require.Contains(t, string(payload), "内容审计测试阻断")
	} else {
		var closeErr coderws.CloseError
		require.ErrorAs(t, readErr, &closeErr)
		require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
		require.Contains(t, closeErr.Reason, "内容审计测试阻断")
	}
	var logs []moderation.ContentModerationLog
	require.Eventually(t, func() bool {
		logs = repo.logSnapshot()
		return len(logs) == 1
	}, time.Second, 10*time.Millisecond)
	require.True(t, logs[0].Flagged)
	require.Equal(t, moderation.ContentModerationActionBlock, logs[0].Action)
	require.Equal(t, "bad prompt", logs[0].InputExcerpt)
}

// TestOpenAIRecordForwardResultCyberWarning_RecordsWSV2TerminalWarning passes a
// successful result carrying a terminal upstream warning. It checks one
// moderation warning with provider, model, user email, and warning text.
func TestOpenAIRecordForwardResultCyberWarning_RecordsWSV2TerminalWarning(t *testing.T) {
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
	result := &forwardcore.OpenAIResult{
		Model: "gpt-5.4",
		UpstreamWarning: &forwardcore.UpstreamWarning{
			ResponseBody: []byte(`{"type":"response.failed","response":{"error":{"message":"This request may pose a cybersecurity risk."}}}`),
			Message:      "This request may pose a cybersecurity risk.",
		},
	}

	h.openAIAttemptSupport().RecordOpenAICyberWarning(c, nil, apiKey, provider, result.Model, result.UpstreamWarning.StatusCode, result.UpstreamWarning.ResponseBody, result.UpstreamWarning.Message)

	require.Len(t, repo.cyberWarnings, 1)
	warning := repo.cyberWarnings[0]
	require.Equal(t, "gpt-5.4", warning.Model)
	require.Equal(t, "user@example.com", warning.UserEmail)
	require.Equal(t, int64(2001), *warning.ProviderID)
	require.Contains(t, warning.WarningText, "cybersecurity risk")
}

// TestOpenAIRecordForwardErrorCyberWarning_RecordsWSV2TerminalWarning wraps an
// upstream warning inside a forwarding error. It checks warning recognition and
// the recorded provider, model, user email, status, and text.
func TestOpenAIRecordForwardErrorCyberWarning_RecordsWSV2TerminalWarning(t *testing.T) {
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
	err = fmt.Errorf("openai ws fallback: missing_final_response: %w", &openAIHandlerTestWarningError{
		warning: &forwardcore.UpstreamWarning{
			ResponseBody: []byte(`{"type":"response.failed","error":{"type":"safety_error","message":"This request has been flagged for potentially high-risk cyber activity."}}`),
			Message:      "This request has been flagged for potentially high-risk cyber activity.",
		},
		err: errors.New("no terminal response payload"),
	})

	recorded := h.openAIAttemptSupport().RecordOpenAIForwardErrorCyberWarning(c, nil, apiKey, provider, "gpt-5.4", 502, err)

	require.True(t, recorded)
	require.Len(t, repo.cyberWarnings, 1)
	warning := repo.cyberWarnings[0]
	require.Equal(t, "gpt-5.4", warning.Model)
	require.Equal(t, "user@example.com", warning.UserEmail)
	require.Equal(t, int64(2001), *warning.ProviderID)
	require.Equal(t, 502, warning.UpstreamStatus)
	require.Contains(t, warning.WarningText, "high-risk cyber")
}

// TestOpenAIResponsesWebSocket_PassthroughUsageLogPersistsUserAgentAndReasoningEffort
// sends an explicit reasoning effort and User-Agent. It checks the retained
// agent, normalized effort, and WebSocket flag in the completion log.
func TestOpenAIResponsesWebSocket_PassthroughUsageLogPersistsUserAgentAndReasoningEffort(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"reasoning":{"effort":"HIGH"}}`,
		userAgent:    testStringPtr("codex_cli_rs/0.125.0 test"),
	})

	require.NotNil(t, got.log.UserAgent)
	require.Equal(t, "codex_cli_rs/0.125.0 test", *got.log.UserAgent)
	require.NotNil(t, got.log.ReasoningEffort)
	require.Equal(t, "high", *got.log.ReasoningEffort)
	require.True(t, got.log.OpenAIWSMode)
}

// TestOpenAIResponsesWebSocket_PassthroughUsageLogInfersReasoningFromInitialRequestModel
// maps a model ending in xhigh to an upstream model. It checks the mapped frame
// and a nil recorded effort when the request has no explicit reasoning field.
func TestOpenAIResponsesWebSocket_PassthroughUsageLogInfersReasoningFromInitialRequestModel(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4-xhigh","stream":false}`,
		userAgent:    testStringPtr("codex_cli_rs/0.125.0 mapped"),
		groupMapping: map[string]string{
			"gpt-5.4-xhigh": "gpt-5.4",
		},
	})

	require.Equal(t, "gpt-5.4", gjson.GetBytes(got.upstreamFirstPayload, "model").String(),
		"上游首帧应使用分组映射后的模型")
	require.Nil(t, got.log.ReasoningEffort, "模型映射前后的后缀都不生成 effort")
}

// TestOpenAIResponsesWebSocket_StripsPreviousResponseIDWhenStickyPreviousMisses
// sends ordinary input with an unmatched previous response. It checks that the
// upstream frame drops the identifier while retaining the input.
func TestOpenAIResponsesWebSocket_StripsPreviousResponseIDWhenStickyPreviousMisses(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"previous_response_id":"resp_other_group","input":[{"type":"input_text","text":"hello"}]}`,
	})

	require.False(t, gjson.GetBytes(got.upstreamFirstPayload, "previous_response_id").Exists(),
		"跨组 sticky miss 时首包应剥离 previous_response_id，避免上游会话链鉴权失败")
	require.Equal(t, "hello", gjson.GetBytes(got.upstreamFirstPayload, "input.0.text").String())
}

// TestOpenAIResponsesWebSocket_KeepsFunctionCallOutputPreviousResponseIDWhenStickyPreviousMisses
// sends a function-call output with an unmatched previous response. It checks
// that the upstream frame retains the response identifier for the tool-result
// turn.
func TestOpenAIResponsesWebSocket_KeepsFunctionCallOutputPreviousResponseIDWhenStickyPreviousMisses(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"previous_response_id":"resp_tool_chain","input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`,
	})

	require.Equal(t, "resp_tool_chain", gjson.GetBytes(got.upstreamFirstPayload, "previous_response_id").String(),
		"工具续链无法用完整 input 重建，sticky miss 时也应保留 previous_response_id")
}

// TestOpenAIResponsesWebSocket_PassthroughUsageLogLeavesUserAgentNilWhenMissing
// sends an empty User-Agent with explicit reasoning effort. It checks a nil
// agent in the completion log and retention of the explicit effort.
func TestOpenAIResponsesWebSocket_PassthroughUsageLogLeavesUserAgentNilWhenMissing(t *testing.T) {
	got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload: `{"type":"response.create","model":"gpt-5.4","stream":false,"reasoning":{"effort":"medium"}}`,
		userAgent:    testStringPtr(""),
	})

	require.Nil(t, got.log.UserAgent, "空入站 User-Agent 不应由上游握手 UA 或默认 UA 兜底")
	require.NotNil(t, got.log.ReasoningEffort)
	require.Equal(t, "medium", *got.log.ReasoningEffort)
}

// newOpenAIWSHandlerTestServer serves Responses WebSocket requests with fixed
// key and user context. It returns a live httptest server whose caller must
// close it after closing any established client connection.
func newOpenAIWSHandlerTestServer(t *testing.T, h *gatewayHTTPEndpointsFixture, subject authctx.AuthSubject) *httptest.Server {
	t.Helper()
	groupID := int64(2)
	apiKey := &apikey.APIKey{
		ID:      101,
		GroupID: &groupID,
		User:    &identity.User{ID: subject.UserID},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), subject)
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	return httptest.NewServer(router)
}

// ListSchedulableByPlatform returns the fixture provider when its platform
// matches or the filter is empty. A mismatched nonempty platform returns no
// candidates; the returned provider value retains its nested references.
func (s *openAIWSUsageHandlerProviderRepoStub) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	if platform != "" && s.provider.Record.Platform != platform {
		return nil, nil
	}
	return []gatewayprovider.ExecutionProvider{s.provider}, nil
}

// ListSchedulableByGroupIDAndPlatform applies the platform filter without
// restricting the fixture by group. It delegates to ListSchedulableByPlatform
// and preserves that method's empty-filter and shared-reference behavior.
func (s *openAIWSUsageHandlerProviderRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return s.ListSchedulableByPlatform(ctx, platform)
}

// GetByID returns an outer copy of the fixture provider when its ID matches. An
// unknown ID returns nil without error, and nested provider references remain
// shared.
func (s *openAIWSUsageHandlerProviderRepoStub) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	if s.provider.Record.ID != id {
		return nil, nil
	}
	provider := s.provider
	return &provider, nil
}

// Create reports a successful usage write and optionally publishes the original
// log pointer. A configured channel must have capacity or a receiver because
// sending blocks and does not observe context cancellation.
func (s *openAIWSUsageHandlerUsageLogRepoStub) Create(ctx context.Context, log *usage.UsageLog) (bool, error) {
	if s.created != nil {
		// Publish the recorder-owned pointer before returning success so the
		// receiving assertion observes the actual write rather than a reconstructed log.
		s.created <- log
	}
	return true, nil
}

// ListAll returns the stored pricing configuration slice without copying its
// backing array or nested values. Callers that modify the returned
// configurations also modify this fixture's shared data.
func (s *openAIWSUsageHandlerPricingConfigRepoStub) ListAll(ctx context.Context) ([]routingtestkit.Configuration, error) {
	return s.modelConfigs, nil
}

// GetGroupPlatforms builds a new map for the requested group IDs. It trims
// stored platform values and omits blank results, allowing tests to distinguish
// a missing platform from a configured platform.
func (s *openAIWSUsageHandlerPricingConfigRepoStub) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform := strings.TrimSpace(s.groupPlatforms[groupID]); platform != "" {
			out[groupID] = platform
		}
	}
	return out, nil
}

// TestOpenAIResponsesWebSocket_FailoverOnUpstreamUsageLimitEvent returns a
// usage-limit event from the first provider. It checks completion from the
// second provider, delivery to both upstreams, and rate-limit marking of the
// first.
func TestOpenAIResponsesWebSocket_FailoverOnUpstreamUsageLimitEvent(t *testing.T) {
	// Buffer one request per upstream so its handler can send the terminal
	// event before the test consumes the forwarding observations after client completion.
	firstHitCh := make(chan []byte, 1)
	secondHitCh := make(chan []byte, 1)

	firstUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			firstHitCh <- payload
		}

		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		// Emit the scripted terminal event; downstream completion and timeout
		// assertions detect delivery failure, so this fixture discards the write error.
		_ = conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"error","error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"The usage limit has been reached"}}`))
		cancelWrite()
	}))
	defer firstUpstream.Close()

	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			secondHitCh <- payload
		}

		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		// Emit the scripted terminal event; downstream completion and timeout
		// assertions detect delivery failure, so this fixture discards the write error.
		_ = conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_ws_failover_ok","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}}`))
		cancelWrite()
		// End the scripted exchange after its terminal write; delivery is
		// observed separately, so the close-handshake error does not replace that result.
		_ = conn.Close(coderws.StatusNormalClosure, "done")
	}))
	defer secondUpstream.Close()

	groupID := int64(4202)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 9902,
				Name:        "openai-ws-rate-limited",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				Credentials: map[string]any{
					"api_key":  "sk-first",
					"base_url": firstUpstream.URL,
				},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"responses_ws_connection_mode":                  "per_session",
				},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 9903,
				Name:        "openai-ws-healthy",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    2,
				Credentials: map[string]any{
					"api_key":  "sk-second",
					"base_url": secondUpstream.URL,
				},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"responses_ws_connection_mode":                  "per_session",
				},
			},
		},
	}

	cfg := &config.Config{}

	cfg.Billing.FallbackRateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	cfg.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.WriteTimeoutSeconds = 3
	cfg.Gateway.Failover.MaxProviderSwitches = 3

	providerRepo := &openAIWSFailoverHandlerProviderRepoStub{providers: providers}
	rateLimitSvc := newAppHealthObserverFixture(providerRepo, cfg)
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	completionInput10 := billingtestkit.Calculator(nil, nil)
	completionInput11 := &providercore.DeferredService{}
	gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo,
		nil,
		cfg,
		nil,
		nil, rateLimitSvc,

		nil,
		nil, completionInput11, newOpenAIExecutionCredentialsForTest(providerRepo,

			nil), nil,
		nil,
		nil,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewaySvc.Recorder = newHTTPCompletionFixture(cfg, nil, completionInput10, billingCacheSvc, completionInput11, nil, completionHealth{rateLimitSvc.Core}, true)

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
		AcquireProviderSlotFn: func(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
	}
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source: gatewaySvc, Credentials: gatewaySvcCredentialPort,
		Funding: newFundingAdmissionFixture(billingCacheSvc, cfg),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, time.Second),
		MaxSwitches: 3, Availability: newExecutionAvailabilityForTest(providerRepo,

			nil, cfg), Choices: gatewaySvcChoices,
	})

	apiKey := &apikey.APIKey{
		ID:      1802,
		GroupID: &groupID,
		User:    &identity.User{ID: 1702, Status: billing.StatusActive},
		Group:   &routing.Group{ID: groupID, Status: billing.StatusActive},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
		&coderws.DialOptions{CompressionMode: coderws.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.4","stream":false}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	_, event, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	require.Equal(t, "resp_ws_failover_ok", gjson.GetBytes(event, "response.id").String())

	// Both upstreams must receive a request; bounded independent waits distinguish
	// successful recovery from a client event produced without the expected provider switch.
	select {
	case <-firstHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("等待第一个上游收到首帧超时")
	}
	select {
	case <-secondHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("等待第二个上游收到重放首帧超时")
	}
	require.Equal(t, []int64{int64(9902)}, providerRepo.rateLimitedIDs)
}

// TestOpenAIResponsesWebSocket_FirstOutputTimeoutWithoutDownstreamReusesClientForOneFailover
// leaves the first upstream silent after receiving its request. It checks
// recovered output on the same client, one connection per provider, handler
// exit, and no penalty for the healthy provider.
func TestOpenAIResponsesWebSocket_FirstOutputTimeoutWithoutDownstreamReusesClientForOneFailover(t *testing.T) {
	// Buffer one request per upstream so its handler can send the terminal
	// event before the test consumes the forwarding observations after client completion.
	firstHitCh := make(chan []byte, 1)
	secondHitCh := make(chan []byte, 1)
	var firstConnections atomic.Int32
	var secondConnections atomic.Int32

	firstUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstConnections.Add(1)
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			firstHitCh <- payload
		}

		// Keep the silent upstream alive after receiving the frame so the
		// gateway first-output deadline can trigger failover before fixture shutdown wins.
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer firstUpstream.Close()

	secondUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondConnections.Add(1)
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			return
		}
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr == nil {
			secondHitCh <- payload
		}

		for _, event := range []string{
			`{"type":"response.created","response":{"id":"resp_ws_timeout_b","model":"gpt-5.4"}}`,
			`{"type":"response.output_text.delta","response_id":"resp_ws_timeout_b","delta":"recovered"}`,
			`{"type":"response.completed","response":{"id":"resp_ws_timeout_b","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}}`,
		} {
			writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
			writeErr := conn.Write(writeCtx, coderws.MessageText, []byte(event))
			cancelWrite()
			if writeErr != nil {
				return
			}
		}
		readCtx, cancelRead = context.WithTimeout(r.Context(), 3*time.Second)
		// Wait for client-side termination after the scripted response so the
		// upstream stays available through the turn; the final read result is discarded.
		_, _, _ = conn.Read(readCtx)
		cancelRead()
	}))
	defer secondUpstream.Close()

	groupID := int64(4212)
	providers := []gatewayprovider.ExecutionProvider{
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 9912,
				Name:        "openai-ws-first-semantic-timeout",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				Credentials: map[string]any{"api_key": "sk-first", "base_url": firstUpstream.URL},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"responses_ws_connection_mode":                  "per_session",
				},
			},
		},
		{
			Record: providercore.Record{
				LoadLocation: time.LoadLocation, ID: 9913,
				Name:        "openai-ws-failover-healthy",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    2,
				Credentials: map[string]any{"api_key": "sk-second", "base_url": secondUpstream.URL},
				Extra: map[string]any{
					"openai_apikey_responses_websockets_v2_enabled": true,
					"responses_ws_connection_mode":                  "per_session",
				},
			},
		},
	}

	cfg := &config.Config{}

	cfg.Billing.FallbackRateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAI.FirstOutputTimeoutSeconds = 1

	cfg.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.IngressInterTurnIdleTimeoutSeconds = 3
	cfg.Gateway.Failover.MaxProviderSwitches = 3

	providerRepo := &openAIWSFailoverHandlerProviderRepoStub{providers: providers}
	rateLimitSvc := newAppHealthObserverFixture(providerRepo, cfg)
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	completionInput12 := billingtestkit.Calculator(nil, nil)
	completionInput13 := &providercore.DeferredService{}
	gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo, nil, cfg, nil, nil, rateLimitSvc,
		nil, nil, completionInput13, newOpenAIExecutionCredentialsForTest(providerRepo,
			nil), nil, nil, nil, nil, nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewaySvc.Recorder = newHTTPCompletionFixture(cfg, nil, completionInput12, billingCacheSvc, completionInput13, nil, completionHealth{rateLimitSvc.Core}, true)

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		AcquireProviderSlotFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source: gatewaySvc, Credentials: gatewaySvcCredentialPort,
		Funding: newFundingAdmissionFixture(billingCacheSvc, cfg),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, time.Second),
		MaxSwitches: 3, Availability: newExecutionAvailabilityForTest(providerRepo,

			nil, cfg), Choices: gatewaySvcChoices,
	})

	apiKey := &apikey.APIKey{
		ID:      1812,
		GroupID: &groupID,
		User:    &identity.User{ID: 1712, Status: billing.StatusActive},
		Group:   &routing.Group{ID: groupID, Status: billing.StatusActive},
	}
	handlerDone := make(chan struct{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		h.ResponsesWebSocket(c)
		// Publish handler return after its socket processing finishes so exit
		// assertions cannot mistake a terminal client event for completed handler cleanup.
		close(handlerDone)
	})
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
		&coderws.DialOptions{CompressionMode: coderws.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.4","stream":false}`))
	cancelWrite()
	require.NoError(t, err)

	var eventTypes []string
	readCtx, cancelRead := context.WithTimeout(context.Background(), 6*time.Second)
	for {
		_, event, readErr := clientConn.Read(readCtx)
		require.NoError(t, readErr)
		eventType := gjson.GetBytes(event, "type").String()
		eventTypes = append(eventTypes, eventType)
		if eventType == "response.completed" {
			require.Equal(t, "resp_ws_timeout_b", gjson.GetBytes(event, "response.id").String())
			break
		}
	}
	cancelRead()
	require.Contains(t, eventTypes, "response.output_text.delta")
	require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))

	// Wait for handler exit separately from payload delivery because a completed
	// response can be observed while connection processing and cleanup still continue.
	select {
	case <-handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket handler did not finish after healthy failover turn")
	}
	// Both upstreams must receive a request; bounded independent waits distinguish
	// successful recovery from a client event produced without the expected provider switch.
	select {
	case <-firstHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("first upstream did not receive replayable request")
	}
	select {
	case <-secondHitCh:
	case <-time.After(3 * time.Second):
		t.Fatal("second upstream did not receive replayed request")
	}
	require.Equal(t, int32(1), firstConnections.Load())
	require.Equal(t, int32(1), secondConnections.Load())
	require.NotContains(t, providerRepo.rateLimitedIDs, int64(9913), "healthy failover provider must not be penalized")
}

// runOpenAIResponsesWebSocketUsageLogCase sends a first frame through a local
// upstream and returns its recorded usage and forwarded payload. Optional group
// mapping configures pricing-based model rewriting; a nonnil userAgent supplies
// the dial header, including an explicitly empty value.
//
// The upstream publishes its first payload before reporting terminal write
// success, while completion recording publishes through a separate channel. The
// helper waits for the client event, usage log, payload, and upstream result
// independently with bounded deadlines. Those waits preserve each observation's
// identity rather than treating a completed client event as proof of a finished
// recorder. Deferred cleanup closes the client before its server.
func runOpenAIResponsesWebSocketUsageLogCase(t *testing.T, tc openAIResponsesWSUsageLogCase) openAIResponsesWSUsageLogResult {
	t.Helper()

	// Keep payload and terminal status separate so a received frame does not
	// imply a successful response write; buffering lets upstream finish before assertions drain them.
	upstreamPayloadCh := make(chan []byte, 1)
	upstreamErrCh := make(chan error, 1)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{
			CompressionMode: coderws.CompressionContextTakeover,
		})
		if err != nil {
			upstreamErrCh <- err
			return
		}
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() {
			_ = conn.CloseNow()
		}()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, payload, readErr := conn.Read(readCtx)
		cancelRead()
		if readErr != nil {
			upstreamErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			upstreamErrCh <- errors.New("unexpected upstream websocket message type")
			return
		}
		upstreamPayloadCh <- payload

		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		writeErr := conn.Write(writeCtx, coderws.MessageText, []byte(
			`{"type":"response.completed","response":{"id":"resp_usage_e2e","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}}`,
		))
		cancelWrite()
		if writeErr != nil {
			upstreamErrCh <- writeErr
			return
		}
		// End the scripted exchange after its terminal write; delivery is
		// observed separately, so the close-handshake error does not replace that result.
		_ = conn.Close(coderws.StatusNormalClosure, "done")
		upstreamErrCh <- nil
	}))
	defer upstreamServer.Close()

	groupID := int64(4201)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9901,
			Name:        "openai-ws-passthrough-usage-e2e",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{
				"api_key":  "sk-test",
				"base_url": upstreamServer.URL,
			},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"responses_ws_connection_mode":                  "per_session",
			},
		},
	}

	cfg := &config.Config{}

	cfg.Billing.FallbackRateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	cfg.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.WriteTimeoutSeconds = 3

	providerRepo := &openAIWSUsageHandlerProviderRepoStub{provider: provider}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *usage.UsageLog, 1)}

	var pricingConfigSvc *routing.PricingConfigService
	if len(tc.groupMapping) > 0 {
		pricingConfigSvc = routingtestkit.NewPricingConfigService(&openAIWSUsageHandlerPricingConfigRepoStub{
			modelConfigs: []routingtestkit.Configuration{{
				ID:           7701,
				Name:         "openai-ws-e2e-channel",
				Status:       billing.StatusActive,
				GroupIDs:     []int64{groupID},
				ModelMapping: tc.groupMapping,
			}},
			groupPlatforms: map[int64]string{groupID: capability.PlatformOpenAI},
		}, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation},
		)
	}

	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	completionInput14 := billingtestkit.Calculator(nil, nil)
	completionInput15 := &providercore.DeferredService{}
	gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo,
		nil,
		cfg,
		nil,
		nil, nil,

		nil,
		nil, completionInput15, newOpenAIExecutionCredentialsForTest(providerRepo,

			nil), nil,
		nil,
		pricingConfigSvc,

		nil,
		nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewaySvc.Recorder = newHTTPCompletionFixture(cfg, usageRepo, completionInput14, billingCacheSvc, completionInput15, pricingConfigSvc, nil, true)

	cache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn: func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
		AcquireProviderSlotFn: func(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
			return true, nil
		},
	}
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source: gatewaySvc, Credentials: gatewaySvcCredentialPort,
		Funding: newFundingAdmissionFixture(billingCacheSvc, cfg),
		Keys:    &apikey.APIKeyService{},
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		), gatewayhttp.SSEPingFormatNone, time.Second), Availability: newExecutionAvailabilityForTest(providerRepo,

			pricingConfigSvc, cfg), Choices: gatewaySvcChoices,
	})

	apiKey := &apikey.APIKey{
		ID:      1801,
		GroupID: &groupID,
		User:    &identity.User{ID: 1701, Status: billing.StatusActive},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	handlerServer := httptest.NewServer(router)
	defer handlerServer.Close()

	headers := http.Header{}
	if tc.userAgent != nil {
		headers.Set("User-Agent", *tc.userAgent)
	}
	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(
		dialCtx,
		"ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses",
		&coderws.DialOptions{HTTPHeader: headers, CompressionMode: coderws.CompressionContextTakeover},
	)
	cancelDial()
	require.NoError(t, err)
	// Close the fixture client after the asserted exchange; teardown errors do
	// not change the event or admission result observed by these assertions.
	defer func() {
		_ = clientConn.CloseNow()
	}()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(tc.firstPayload))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, event, err := clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	// Finish the client exchange before waiting for recorder and upstream reports;
	// those independent results establish completion even if the close handshake fails.
	_ = clientConn.Close(coderws.StatusNormalClosure, "done")

	// Completion recording and upstream processing publish independently; wait for
	// each result so a terminal client frame cannot conceal missing persistence or forwarding.
	var usageLog *usage.UsageLog
	select {
	case usageLog = <-usageRepo.created:
		require.NotNil(t, usageLog)
	case <-time.After(3 * time.Second):
		t.Fatal("等待 WebSocket usage log 写入超时")
	}

	var upstreamFirstPayload []byte
	select {
	case upstreamFirstPayload = <-upstreamPayloadCh:
	case <-time.After(3 * time.Second):
		t.Fatal("等待上游 WebSocket 首帧超时")
	}

	select {
	case upstreamErr := <-upstreamErrCh:
		require.NoError(t, upstreamErr)
	case <-time.After(3 * time.Second):
		t.Fatal("等待上游 WebSocket 结束超时")
	}

	return openAIResponsesWSUsageLogResult{
		log:                  usageLog,
		upstreamFirstPayload: upstreamFirstPayload,
	}
}

// testStringPtr returns a pointer to the supplied string value.
func testStringPtr(v string) *string {
	return &v
}

// GetByKeyForAuth reads the current fixture key under its mutex and returns an
// API-key copy. A removed key returns ErrAPIKeyNotFound; the requested key
// string does not select a different record.
func (r *wsTurnKeys) GetByKeyForAuth(context.Context, string) (*apikey.APIKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.key == nil {
		return nil, apikey.ErrAPIKeyNotFound
	}
	return apikey.CopyAPIKey(r.key), nil
}

// remove clears the current key while holding the repository mutex. Subsequent
// authentication lookups return ErrAPIKeyNotFound, while previously copied
// connection context still refers to the earlier record.
func (r *wsTurnKeys) remove() {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Remove the repository record rather than the retained connection snapshot
	// so the next turn must detect revocation through its authentication lookup.
	r.key = nil
}

// GetByIDLite returns the fixture group with a separate allowed-protocol slice.
// It ignores the requested ID and copies under the repository mutex, so a
// subsequent revokeWS call changes later lookups without replacing an earlier
// result's protocol list.
//
// The outer group value is copied first, and AllowedProtocols receives its own
// backing array before publication. Other nested group references keep the
// ordinary shallow-copy behavior. This boundary matters because the
// connection's initial group snapshot and the repository's next-turn
// permissions must remain distinguishable after revocation.
func (r *wsTurnGroups) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value := r.group
	// Copy protocol storage before unlocking so later permission replacement cannot
	// alter the allowed-protocol view already returned for an earlier authentication lookup.
	value.AllowedProtocols = append([]capability.ProtocolID(nil), r.group.AllowedProtocols...)
	return &value, nil
}

// revokeWS replaces allowed protocols under the repository mutex with HTTP
// Responses alone. Later group lookups therefore lose Responses WebSocket
// permission while snapshots already returned keep their copied protocol
// slices.
func (r *wsTurnGroups) revokeWS() {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Keep HTTP Responses permission while removing WebSocket permission under
	// the same lock used by lookups, isolating revocation to the follow-up protocol.
	r.group.AllowedProtocols = []capability.ProtocolID{capability.ProtocolOpenAIResponses}
}

// newOpenAIWSPassthroughHandlerHarness opens a Responses WebSocket backed by a
// caller-supplied upstream URL and Redis gateway cache. It enables warning
// recording and session blocking, then exposes key and group stores that tests
// can mutate between turns.
//
// The HTTP middleware installs an initial authentication snapshot, while
// subsequent lookups copy the current repository records. handlerDone closes
// only after ResponsesWebSocket returns, giving callers an explicit exit
// observation in addition to socket events. Test cleanup registers server
// closure before client closure so the client closes first. Redis and
// moderation cleanup follow their respective test-fixture ownership.
func newOpenAIWSPassthroughHandlerHarness(t *testing.T, upstreamURL string) *openAIWSPassthroughHandlerHarness {
	t.Helper()
	gatewayCache := testutil.NewRedisGatewayCache(t)

	settingRepo := &contentModerationHandlerSettingRepo{values: map[string]string{
		moderation.SettingKeyRiskControlEnabled:          "true",
		moderation.SettingKeyCyberSessionBlockEnabled:    "true",
		moderation.SettingKeyCyberSessionBlockTTLSeconds: "60",
		moderation.SettingKeyContentModerationConfig:     `{"enabled":true,"mode":"observe","cyber_warning_enabled":true,"all_groups":true}`,
	}}
	moderationRepo := &contentModerationHandlerTestRepo{}
	moderationSvc := newHTTPModeration(t, settingRepo, moderationRepo)
	moderationSvc.Start()
	settingSvc := gatewaytestkit.RuntimeReaders(settingRepo)

	groupID := int64(4301)
	provider := gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation, ID: 9951,
			Name:        "openai-ws-passthrough-cyber",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.ProviderTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Credentials: map[string]any{"api_key": "sk-test", "base_url": upstreamURL},
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
				"responses_ws_connection_mode":                  "per_session",
			},
		},
	}
	cfg := &config.Config{}

	cfg.Billing.FallbackRateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	cfg.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.WriteTimeoutSeconds = 3
	cfg.Gateway.OpenAI.ResponsesWS.IngressInterTurnIdleTimeoutSeconds = 3

	providerRepo := &openAIWSUsageHandlerProviderRepoStub{provider: provider}
	usageRepo := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *usage.UsageLog, 2)}
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()
	completionInput16 := billingtestkit.Calculator(nil, nil)
	completionInput17 := &providercore.DeferredService{}
	gatewaySvc, gatewaySvcChoices, gatewaySvcCredentialPort := newOpenAIExecutionAndSelectionFixture(
		providerRepo, gatewayCache, cfg, nil, nil, nil, nil, nil, completionInput17, newOpenAIExecutionCredentialsForTest(providerRepo, nil), nil, nil, nil, settingSvc, nil, responseHeaderFilterForTest(cfg), nil, nil, nil,
	)
	gatewaySvc.Recorder = newHTTPCompletionFixture(cfg, usageRepo, completionInput16, billingCacheSvc, completionInput17, nil, nil, true)

	concurrencyCache := &httptestkit.ConcurrencyHooks{
		AcquireUserSlotFn:     func(context.Context, int64, int, string) (bool, error) { return true, nil },
		AcquireProviderSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
	}
	apiKey := &apikey.APIKey{
		ID:      1851,
		UserID:  1751,
		Status:  "active",
		Name:    "ws-cyber-key",
		Key:     "sk-handler-cyber-test",
		GroupID: &groupID,
		User:    &identity.User{ID: 1751, Status: billing.StatusActive},
	}
	apiKey.Group = &routing.Group{ID: groupID, Status: "active", AllowedProtocols: []capability.ProtocolID{capability.ProtocolResponsesWebSocket}}
	keys := &wsTurnKeys{key: apikey.CopyAPIKey(apiKey)}
	groups := &wsTurnGroups{group: *apiKey.Group}
	keyService := testkit.NewService(keys, nil, groups, nil, nil, nil, nil)
	h := newGatewayHTTPEndpoints(gatewayHTTPFixtureInput{
		Source: gatewaySvc, Credentials: gatewaySvcCredentialPort,
		Funding:     newFundingAdmissionFixture(billingCacheSvc, cfg),
		Keys:        keyService,
		Moderator:   moderationSvc,
		Concurrency: gatewayhttp.NewConcurrencyHelper(scheduler.NewConcurrencyService(concurrencyCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}), gatewayhttp.SSEPingFormatNone, time.Second), Availability: newExecutionAvailabilityForTest(providerRepo, nil, cfg), Choices: gatewaySvcChoices,
	})

	handlerDone := make(chan struct{})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", func(c *gin.Context) {
		h.ResponsesWebSocket(c)
		// Publish handler return after its socket processing finishes so exit
		// assertions cannot mistake a terminal client event for completed handler cleanup.
		close(handlerDone)
	})
	handlerServer := httptest.NewServer(router)
	t.Cleanup(handlerServer.Close)

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(handlerServer.URL, "http")+"/openai/v1/responses", nil)
	cancelDial()
	require.NoError(t, err)
	// Cleanup runs in reverse registration order, closing this client before
	// the listener closes, letting socket reads terminate; teardown errors are discarded.
	t.Cleanup(func() { _ = clientConn.CloseNow() })

	return &openAIWSPassthroughHandlerHarness{
		clientConn:     clientConn,
		handlerDone:    handlerDone,
		moderationRepo: moderationRepo,
		gatewayCache:   gatewayCache,
		apiKey:         apiKey,
		keys:           keys,
		groups:         groups,
	}
}

// TestOpenAIResponsesWebSocketV2PassthroughCyberMarkIsConsumedAfterTurn
// receives a terminal cyber-policy failure from upstream. It checks warning
// persistence and the Redis session block before asserting that the
// same-session follow-up closes without reaching upstream.
func TestOpenAIResponsesWebSocketV2PassthroughCyberMarkIsConsumedAfterTurn(t *testing.T) {
	// Observe upstream exit and follow-up delivery separately because socket
	// termination alone cannot establish whether the second frame was forwarded.
	upstreamDone := make(chan struct{})
	secondUpstreamFrame := make(chan []byte, 1)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal upstream handler exit even on an early read or write failure;
		// the outer test waits before deciding whether a follow-up reached this socket.
		defer close(upstreamDone)
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		require.NoError(t, err)
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, _, err = conn.Read(readCtx)
		cancelRead()
		require.NoError(t, err)

		failed := []byte(`{"type":"response.failed","response":{"id":"resp_cyber_handler","model":"gpt-5.4","error":{"code":"cyber_policy","message":"blocked by upstream policy"},"usage":{"input_tokens":11,"output_tokens":3}}}`)
		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		err = conn.Write(writeCtx, coderws.MessageText, failed)
		cancelWrite()
		require.NoError(t, err)

		readCtx, cancelRead = context.WithTimeout(r.Context(), 3*time.Second)
		_, second, err := conn.Read(readCtx)
		cancelRead()
		if err != nil {
			return
		}
		secondUpstreamFrame <- append([]byte(nil), second...)

		completed := []byte(`{"type":"response.completed","response":{"id":"resp_cyber_handler_turn_2","model":"gpt-5.4","usage":{"input_tokens":1,"output_tokens":1}}}`)
		writeCtx, cancelWrite = context.WithTimeout(r.Context(), 3*time.Second)
		err = conn.Write(writeCtx, coderws.MessageText, completed)
		cancelWrite()
		require.NoError(t, err)
	}))
	defer upstreamServer.Close()
	harness := newOpenAIWSPassthroughHandlerHarness(t, upstreamServer.URL)

	requestPayload := `{"type":"response.create","model":"gpt-5.4","prompt_cache_key":"cyber-session-1","input":"test"}`
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err := harness.clientConn.Write(writeCtx, coderws.MessageText, []byte(requestPayload))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, event, err := harness.clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "response.failed", gjson.GetBytes(event, "type").String())

	require.Eventually(t, func() bool {
		warnings := harness.moderationRepo.cyberWarningSnapshot()
		// The warning receives gateway 502 status because the terminal WebSocket
		// event carries no HTTP status; wait for persistence before checking session blocking.
		return len(warnings) == 1 && warnings[0].WarningText == "blocked by upstream policy" &&
			warnings[0].UpstreamStatus == http.StatusBadGateway
	}, 3*time.Second, 10*time.Millisecond, "handler AfterTurn must call recordCyberPolicyIfMarked and write the risk-control event")

	keyCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	keyCtx.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(requestPayload))
	blockKey := gatewayhttp.CyberSessionExplicitBlockKey(harness.apiKey.ID, keyCtx, []byte(requestPayload))
	require.NotEmpty(t, blockKey)
	store, ok := harness.gatewayCache.(session.CyberSessionBlockStore)
	require.True(t, ok)
	require.Eventually(t, func() bool {
		matched, findErr := store.FindCyberSessionBlocked(context.Background(), []string{blockKey})
		return findErr == nil && matched == blockKey
	}, 3*time.Second, 10*time.Millisecond, "handler AfterTurn must write the cyber session block table")

	writeCtx, cancelWrite = context.WithTimeout(context.Background(), 3*time.Second)
	err = harness.clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.4","prompt_cache_key":"cyber-session-1","input":"follow-up"}`))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead = context.WithTimeout(context.Background(), 3*time.Second)
	_, _, err = harness.clientConn.Read(readCtx)
	cancelRead()
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
	// The blocked follow-up closes with the fixed English session-policy reason,
	// allowing the assertion to distinguish session blocking from other admission failures.
	require.Equal(t, "This session is blocked by the security policy. Start a new session.", closeErr.Reason)
	// Join the handler and upstream before inspecting buffered frames so a
	// still-running sender cannot make an absent follow-up observation appear conclusive.
	select {
	case <-harness.handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket handler did not exit")
	}
	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream websocket did not exit")
	}
	select {
	case second := <-secondUpstreamFrame:
		t.Fatalf("blocked follow-up reached upstream: %s", second)
	default:
	}
}

// TestOpenAIResponsesWebSocketV2PassthroughNonCyberTurnAllowsFollowup completes
// two ordinary turns on one connection. It checks both response IDs, the
// forwarded follow-up frame, empty warnings, an absent session block, and
// eventual exit.
func TestOpenAIResponsesWebSocketV2PassthroughNonCyberTurnAllowsFollowup(t *testing.T) {
	// Observe upstream exit and follow-up delivery separately because socket
	// termination alone cannot establish whether the second frame was forwarded.
	upstreamDone := make(chan struct{})
	secondUpstreamFrame := make(chan []byte, 1)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Signal upstream handler exit even on an early read or write failure;
		// the outer test waits before deciding whether a follow-up reached this socket.
		defer close(upstreamDone)
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		require.NoError(t, err)
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() { _ = conn.CloseNow() }()

		readCtx, cancelRead := context.WithTimeout(r.Context(), 3*time.Second)
		_, _, err = conn.Read(readCtx)
		cancelRead()
		require.NoError(t, err)

		firstCompleted := []byte(`{"type":"response.completed","response":{"id":"resp_non_cyber_handler_turn_1","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}}`)
		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		err = conn.Write(writeCtx, coderws.MessageText, firstCompleted)
		cancelWrite()
		require.NoError(t, err)

		readCtx, cancelRead = context.WithTimeout(r.Context(), 3*time.Second)
		_, second, err := conn.Read(readCtx)
		cancelRead()
		require.NoError(t, err)
		secondUpstreamFrame <- append([]byte(nil), second...)

		secondCompleted := []byte(`{"type":"response.completed","response":{"id":"resp_non_cyber_handler_turn_2","model":"gpt-5.4","usage":{"input_tokens":3,"output_tokens":1}}}`)
		writeCtx, cancelWrite = context.WithTimeout(r.Context(), 3*time.Second)
		err = conn.Write(writeCtx, coderws.MessageText, secondCompleted)
		cancelWrite()
		require.NoError(t, err)

		readCtx, cancelRead = context.WithTimeout(r.Context(), 3*time.Second)
		// Wait for client-side termination after the scripted response so the
		// upstream stays available through the turn; the final read result is discarded.
		_, _, _ = conn.Read(readCtx)
		cancelRead()
	}))
	defer upstreamServer.Close()
	harness := newOpenAIWSPassthroughHandlerHarness(t, upstreamServer.URL)

	firstPayload := `{"type":"response.create","model":"gpt-5.4","prompt_cache_key":"non-cyber-session-1","input":"first"}`
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err := harness.clientConn.Write(writeCtx, coderws.MessageText, []byte(firstPayload))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
	_, firstEvent, err := harness.clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "resp_non_cyber_handler_turn_1", gjson.GetBytes(firstEvent, "response.id").String())

	secondPayload := `{"type":"response.create","model":"gpt-5.4","prompt_cache_key":"non-cyber-session-1","input":"follow-up"}`
	writeCtx, cancelWrite = context.WithTimeout(context.Background(), 3*time.Second)
	err = harness.clientConn.Write(writeCtx, coderws.MessageText, []byte(secondPayload))
	cancelWrite()
	require.NoError(t, err)

	readCtx, cancelRead = context.WithTimeout(context.Background(), 3*time.Second)
	_, secondEvent, err := harness.clientConn.Read(readCtx)
	cancelRead()
	require.NoError(t, err)
	require.Equal(t, "resp_non_cyber_handler_turn_2", gjson.GetBytes(secondEvent, "response.id").String())
	require.Empty(t, harness.moderationRepo.cyberWarningSnapshot())

	keyCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	keyCtx.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(firstPayload))
	blockKey := gatewayhttp.CyberSessionExplicitBlockKey(harness.apiKey.ID, keyCtx, []byte(firstPayload))
	require.NotEmpty(t, blockKey)
	store, ok := harness.gatewayCache.(session.CyberSessionBlockStore)
	require.True(t, ok)
	matched, findErr := store.FindCyberSessionBlocked(context.Background(), []string{blockKey})
	require.NoError(t, findErr)
	require.Empty(t, matched)

	require.NoError(t, harness.clientConn.Close(coderws.StatusNormalClosure, "done"))
	// Join the handler and upstream before inspecting buffered frames so a
	// still-running sender cannot make an absent follow-up observation appear conclusive.
	select {
	case <-harness.handlerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("non-cyber websocket handler did not exit")
	}
	select {
	case <-upstreamDone:
	case <-time.After(3 * time.Second):
		t.Fatal("non-cyber upstream websocket did not exit")
	}
	select {
	case second := <-secondUpstreamFrame:
		require.JSONEq(t, secondPayload, string(second))
	default:
		t.Fatal("non-cyber follow-up did not reach upstream")
	}
}

// TestOpenAIResponsesWebSocketDeletedKeyRejectsFollowup deletes the repository
// key after a completed first turn. It checks a policy-violation close and
// confirms that the second request does not reach upstream.
func TestOpenAIResponsesWebSocketDeletedKeyRejectsFollowup(t *testing.T) {
	testOpenAIWSRevokedAccessRejectsFollowup(t, func(h *openAIWSPassthroughHandlerHarness) { h.keys.remove() })
}

// TestOpenAIResponsesWebSocketDisabledGroupRejectsFollowup removes WebSocket
// permission after a completed first turn. It checks a policy-violation close
// and confirms that the second request does not reach upstream.
func TestOpenAIResponsesWebSocketDisabledGroupRejectsFollowup(t *testing.T) {
	testOpenAIWSRevokedAccessRejectsFollowup(t, func(h *openAIWSPassthroughHandlerHarness) { h.groups.revokeWS() })
}

// testOpenAIWSRevokedAccessRejectsFollowup completes one upstream turn, invokes
// revoke, and attempts another request on the same client. The callback
// receives mutable authentication fixtures and must apply the permission change
// before returning.
//
// The upstream reports whether its second read succeeded, then runs deferred
// socket cleanup. The helper checks that report and waits for
// ResponsesWebSocket to return through handlerDone under one overall deadline.
// Receiving reached establishes the forwarding observation, while handlerDone
// establishes handler return; neither signal joins the upstream handler's
// deferred closure. Deferred server closure releases listeners; harness
// cleanup closes the client and stops moderation, with Redis cleanup owned by
// the cache fixture.
func testOpenAIWSRevokedAccessRejectsFollowup(t *testing.T, revoke func(*openAIWSPassthroughHandlerHarness)) {
	t.Helper()
	// Buffer the second-read report so upstream can publish it before socket
	// cleanup; after a completed first turn, false confirms no follow-up frame.
	reached := make(chan bool, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			reached <- false
			return
		}
		// End the upstream socket when this exchange finishes so handler reads
		// can terminate; assertions inspect forwarded events rather than teardown errors.
		defer func() { _ = conn.CloseNow() }()
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if _, _, err = conn.Read(ctx); err != nil {
			reached <- false
			return
		}
		if err = conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_before_delete","model":"gpt-5.4","usage":{"input_tokens":2,"output_tokens":1}}}`)); err != nil {
			reached <- false
			return
		}
		_, _, err = conn.Read(ctx)
		reached <- err == nil
	}))
	defer upstream.Close()
	harness := newOpenAIWSPassthroughHandlerHarness(t, upstream.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := []byte(`{"type":"response.create","model":"gpt-5.4","input":"test"}`)
	require.NoError(t, harness.clientConn.Write(ctx, coderws.MessageText, payload))
	_, event, err := harness.clientConn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "resp_before_delete", gjson.GetBytes(event, "response.id").String())
	// Revoke repository access only after the first response has completed,
	// preserving the initial authenticated turn while forcing a fresh check on follow-up.
	revoke(harness)
	require.NoError(t, harness.clientConn.Write(ctx, coderws.MessageText, payload))
	_, _, err = harness.clientConn.Read(ctx)
	require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
	select {
	case forwarded := <-reached:
		require.False(t, forwarded, "撤销权限后的第二轮不能到达上游")
	case <-ctx.Done():
		t.Fatal("上游连接未结束")
	}
	// The second-read report establishes forwarding; wait separately for handler
	// return so the client close does not stand in for completed gateway cleanup.
	select {
	case <-harness.handlerDone:
	case <-ctx.Done():
		t.Fatal("撤销权限后的连接未结束")
	}
}

// TestOpenAIWSAssemblyUpgradeAndStopBoundaries exercises missing upgrade
// headers, unavailable dependencies, and stopped request activity. It checks
// the corresponding status and message while confirming that every rejection
// leaves the request body unread.
func TestOpenAIWSAssemblyUpgradeAndStopBoundaries(t *testing.T) {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("ws-entry-contract")}
	common := provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, GatewayCompletionRecorders{}, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h := provideResponsesWSHTTP(nil, nil, nil, nil, common, nil, nil, nil, activity, nil, nil, nil, nil)
	for _, step := range []struct {
		name    string // Admission boundary case name.
		upgrade bool   // Supply valid upgrade headers.
		stop    bool   // Stop shared request activity.
		status  int    // Expected HTTP response status.
		message string // Expected response body fragment.
	}{
		{"upgrade", false, false, http.StatusUpgradeRequired, "WebSocket upgrade required"},
		{"dependencies", true, false, http.StatusServiceUnavailable, "Service temporarily unavailable"},
		{"stop", true, true, http.StatusServiceUnavailable, "Service is shutting down"},
	} {
		t.Run(step.name, func(t *testing.T) {
			if step.stop {
				require.NoError(t, activity.StopContext(context.Background()))
			}
			writer := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(writer)
			body := &protocolGateTrackingReader{}
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", body)
			if step.upgrade {
				c.Request.Header.Set("Upgrade", "websocket")
				c.Request.Header.Set("Connection", "Upgrade")
			}
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ID: 9, UserID: 7})
			c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: 7})
			h.ResponsesWebSocket(c)
			require.Equal(t, step.status, writer.Code)
			require.Contains(t, writer.Body.String(), step.message)
			require.False(t, body.read)
		})
	}
}

// closeOpenAIWSFailoverExhausted presents a provider failure through the shared
// WebSocket close helper. It delegates error classification to
// FailoverPresentation and records stream failure in Ops even when conn is nil.
func closeOpenAIWSFailoverExhausted(c *gin.Context, conn *coderws.Conn, err *forwardcore.UpstreamFailoverError) {
	gatewayhttp.CloseResponsesWSFailure(c, conn, wshttp.FailoverPresentation(err), gatewayhttp.MarkOpsStreamFailure)
}
