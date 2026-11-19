package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestSplitContentModerationTextKeepsOverlapAndTail supplies text longer than
// one small chunk with a two-rune overlap. It checks the exact two chunks,
// including repeated overlap and the final original text tail.
func TestSplitContentModerationTextKeepsOverlapAndTail(t *testing.T) {
	chunks := splitContentModerationText("abcdefghij", 6, 2)

	require.Equal(t, []string{"abcdef", "efghij"}, chunks)
}

// TestContentModerationTextBatchingDoesNotCreateOverlapOnlyTail supplies an
// exact full chunk and a longer count-only example. It checks that the exact
// tail creates one batch and that the count helper matches selected overlapping
// chunk totals.
func TestContentModerationTextBatchingDoesNotCreateOverlapOnlyTail(t *testing.T) {
	var batches [][]string
	forEachContentModerationTextBatch("abcdef", 6, 2, 2, func(_ int, chunks []string) {
		batches = append(batches, append([]string(nil), chunks...))
	})

	require.Equal(t, [][]string{{"abcdef"}}, batches)
	require.Equal(t, 1, countContentModerationTextChunks("abcdef", 6, 2))
	require.Equal(t, 3, countContentModerationTextChunks("abcdefghijk", 6, 2))
}

// TestContentModerationBatchFailureFallsBackToSingleTextUnits makes the local
// endpoint reject a multi-text batch as unsupported. It checks one batch call
// plus two individual calls, an allowed decision, and complete recorded audit
// units.
func TestContentModerationBatchFailureFallsBackToSingleTextUnits(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload struct {
			Input json.RawMessage `json:"input"` // JSON audit input field value
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		var batch []string
		// A failed array decode selects the ordinary reply path, allowing scalar
		// retries to succeed while this fixture rejects decoded multi-text batches.
		if json.Unmarshal(payload.Input, &batch) == nil && len(batch) > 1 {
			http.Error(w, "batch unsupported", http.StatusBadRequest)
			return
		}
		require.NoError(t, json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{CategoryScores: map[string]float64{"sexual": 0.01}}}}))
	}))
	defer server.Close()

	text := strings.Repeat("a", maxModerationInputRunes+100)
	svc, repo := newExpandedModerationTestService(t, server.URL, true)
	body, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}})
	require.NoError(t, err)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.False(t, decision.Blocked)
	require.Equal(t, int64(3), calls.Load())
	logs := requireContentModerationLogCount(t, repo, 1)
	require.True(t, logs[0].AuditComplete)
	require.Equal(t, 2, logs[0].TextUnitCount)
}

// TestContentModerationTransientBatchFailureDoesNotFanOut makes the local
// endpoint fail a multi-chunk request with HTTP 503 and retry disabled. It
// checks one call, allowed admission, and both failed units in an incomplete
// record.
func TestContentModerationTransientBatchFailureDoesNotFanOut(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	text := strings.Repeat("a", maxModerationInputRunes+100)
	svc, repo := newExpandedModerationTestService(t, server.URL, false)
	body, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "content": text}}})
	require.NoError(t, err)

	decision, err := svc.Check(context.Background(), ContentModerationCheckInput{Protocol: ContentModerationProtocolOpenAIChat, Body: body})

	require.NoError(t, err)
	require.True(t, decision.Allowed)
	require.Equal(t, int64(1), calls.Load())
	logs := requireContentModerationLogCount(t, repo, 1)
	require.False(t, logs[0].AuditComplete)
	require.Equal(t, 2, logs[0].FailedUnitCount)
}

// TestContentModerationCallRoutesThroughProxy uses an unresolvable upstream
// name and a local forward proxy answering the audit request. It checks proxy
// request URI form and a positive proxied count after the successful call.
func TestContentModerationCallRoutesThroughProxy(t *testing.T) {
	var proxied atomic.Int64
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A forward proxy receives an absolute URI for an HTTP target.
		if !strings.HasPrefix(r.RequestURI, "http://moderation-proxy-test.invalid") {
			t.Errorf("expected absolute-URI proxy request, got %q", r.RequestURI)
		}
		proxied.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Reply from the forward proxy with an empty-score result; request URI and
		// proxied count are observed by this case, while response-write errors are
		// ignored.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{Flagged: false}}})
	}))
	defer proxySrv.Close()

	proxyAddr := strings.TrimPrefix(proxySrv.URL, "http://")
	host, portStr, ok := strings.Cut(proxyAddr, ":")
	if !ok {
		t.Fatalf("unexpected proxy addr: %s", proxyAddr)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse proxy port: %v", err)
	}

	proxyRepo := &contentModerationTestProxyRepo{proxies: map[int64]*Proxy{
		7: {ID: 7, Name: "audit-proxy", Protocol: "http", Host: host, Port: port, Status: StatusActive},
	}}
	svc := newTestModeration(nil, nil, nil, nil, nil, nil, nil)
	svc.Start()
	svc.SetProxyRepository(proxyRepo)

	cfg := defaultContentModerationConfig()
	cfg.BaseURL = "http://moderation-proxy-test.invalid"
	cfg.ProxyID = moderationProxyIDPtr(7)
	cfg.normalize()

	httpStatus := 0
	if _, err := svc.callModerationOnceWithInput(context.Background(), cfg, "sk-test", "hello", &httpStatus); err != nil {
		t.Fatalf("expected moderation call via proxy to succeed, got: %v", err)
	}
	if proxied.Load() == 0 {
		t.Fatal("expected request to be routed through the proxy server")
	}
}

// TestContentModerationProxyResolveFailureDoesNotFallBackToDirect fails stored
// proxy lookup while a directly reachable local upstream is available. It
// checks the resolution error and zero direct requests after the attempted
// audit.
func TestContentModerationProxyResolveFailureDoesNotFallBackToDirect(t *testing.T) {
	var direct atomic.Int64
	directSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// A reply-capable direct endpoint exposes accidental routing through its
		// counter. The rejection case ignores callback write errors because a correct
		// resolution failure never reaches this endpoint.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{Flagged: false}}})
	}))
	defer directSrv.Close()

	proxyRepo := &contentModerationTestProxyRepo{getByIDErr: errors.New("proxy deleted")}
	svc := newTestModeration(nil, nil, nil, nil, nil, nil, nil)
	svc.Start()
	svc.SetProxyRepository(proxyRepo)

	cfg := defaultContentModerationConfig()
	cfg.BaseURL = directSrv.URL
	cfg.ProxyID = moderationProxyIDPtr(9)
	cfg.normalize()

	httpStatus := 0
	_, err := svc.callModerationOnceWithInput(context.Background(), cfg, "sk-test", "hello", &httpStatus)
	if err == nil || !strings.Contains(err.Error(), "resolve moderation proxy") {
		t.Fatalf("expected proxy resolve error, got: %v", err)
	}
	if direct.Load() != 0 {
		t.Fatal("must not fall back to direct connection when proxy resolution fails")
	}
}

// TestContentModerationProxyURLResolutionCached resolves the same stored proxy
// five times inside the ordinary cache lifetime. It checks a single fixture
// repository lookup, without dialing that proxy endpoint.
func TestContentModerationProxyURLResolutionCached(t *testing.T) {
	proxyRepo := &contentModerationTestProxyRepo{proxies: map[int64]*Proxy{
		3: {ID: 3, Name: "p", Protocol: "http", Host: "127.0.0.1", Port: 8080, Status: StatusActive},
	}}
	svc := newTestModeration(nil, nil, nil, nil, nil, nil, nil)
	svc.Start()
	svc.SetProxyRepository(proxyRepo)

	for i := range 5 {
		if _, err := svc.resolveModerationProxyURL(context.Background(), 3); err != nil {
			t.Fatalf("resolve attempt %d failed: %v", i, err)
		}
	}
	if got := proxyRepo.getCalls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 repository lookup thanks to caching, got %d", got)
	}
}

// TestContentModerationTestAPIKeysProxySemantics tests an explicit credential
// first through the saved proxy and then with a zero direct override. It checks
// the corresponding local servers receive requests and that direct selection
// leaves the prior proxy count unchanged.
func TestContentModerationTestAPIKeysProxySemantics(t *testing.T) {
	var proxied atomic.Int64
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Reply from the selected proxy/direct fixture and observe its request
		// counter. This callback ignores response-write errors; successful probe
		// status and routing counts are the assertions for these inputs.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{Flagged: false}}})
	}))
	defer proxySrv.Close()
	proxyAddr := strings.TrimPrefix(proxySrv.URL, "http://")
	// This fixture parser assumes an IPv4 host:port server URL and ignores its
	// delimiter and numeric errors; later proxy-call assertions consume the constructed address.
	host, portStr, _ := strings.Cut(proxyAddr, ":")
	port, _ := strconv.Atoi(portStr)

	savedCfg := defaultContentModerationConfig()
	savedCfg.BaseURL = "http://moderation-proxy-test.invalid"
	savedCfg.ProxyID = moderationProxyIDPtr(7)
	savedCfg.APIKeys = []string{"sk-saved"}
	rawCfg, err := json.Marshal(savedCfg)
	if err != nil {
		t.Fatalf("marshal cfg: %v", err)
	}

	settingRepo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: string(rawCfg),
	}}
	proxyRepo := &contentModerationTestProxyRepo{proxies: map[int64]*Proxy{
		7: {ID: 7, Name: "audit-proxy", Protocol: "http", Host: host, Port: port, Status: StatusActive},
	}}
	svc := newTestModeration(settingRepo, nil, nil, nil, nil, nil, nil)
	svc.Start()
	svc.SetProxyRepository(proxyRepo)

	// An omitted ProxyID sends the key test through the saved proxy.
	result, err := svc.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{APIKeys: []string{"sk-input"}})
	if err != nil {
		t.Fatalf("test with saved proxy: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Status == "error" {
		t.Fatalf("expected key test via proxy to succeed, got %+v", result.Items)
	}
	if proxied.Load() == 0 {
		t.Fatal("expected test request to route through the saved proxy")
	}

	// Zero selects a direct connection to the local upstream server.
	var direct atomic.Int64
	directSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Reply from the selected proxy/direct fixture and observe its request
		// counter. This callback ignores response-write errors; successful probe
		// status and routing counts are the assertions for these inputs.
		_ = json.NewEncoder(w).Encode(moderationAPIResponse{Results: []moderationAPIResult{{Flagged: false}}})
	}))
	defer directSrv.Close()

	before := proxied.Load()
	result, err = svc.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{
		APIKeys: []string{"sk-input"},
		BaseURL: directSrv.URL,
		ProxyID: moderationProxyIDPtr(0),
	})
	if err != nil {
		t.Fatalf("test with forced direct: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].Status == "error" {
		t.Fatalf("expected forced-direct test to succeed, got %+v", result.Items)
	}
	if direct.Load() == 0 {
		t.Fatal("expected forced-direct test to reach the base URL directly")
	}
	if proxied.Load() != before {
		t.Fatal("forced-direct test must not route through the proxy")
	}
}

// TestBuildModerationTestInputAllowsMultipleImages supplies a prompt and two
// valid base64 data references to probe construction. It checks successful
// construction, an accepted image count of two, and a nonnil input value.
func TestBuildModerationTestInputAllowsMultipleImages(t *testing.T) {
	input, count, err := buildModerationTestInput("check image", []string{
		"data:image/png;base64,Zmlyc3Q=",
		"data:image/png;base64,c2Vjb25k",
	})

	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NotNil(t, input)
}

// TestBuildContentModerationTestAuditResult_UsesConfiguredThresholdsOnly
// supplies provider Flagged true with a score below the local default
// threshold. It checks local false flagging, maximum and composite display
// scores, and the effective threshold.
func TestBuildContentModerationTestAuditResult_UsesConfiguredThresholdsOnly(t *testing.T) {
	result := buildContentModerationTestAuditResult(&moderationAPIResult{
		Flagged: true,
		CategoryScores: map[string]float64{
			"harassment": 0.65,
		},
	}, nil)

	require.NotNil(t, result)
	require.False(t, result.Flagged)
	require.Equal(t, "harassment", result.HighestCategory)
	require.Equal(t, 0.65, result.HighestScore)
	require.Equal(t, 0.65, result.CompositeScore)
	require.Equal(t, 0.98, result.Thresholds["harassment"])
}

// TestContentModerationCallModeration_400DoesNotFreezeAPIKey returns a
// request-shape HTTP 400 while retry count is high for a fresh key. It checks
// one request, error status, no failure increment, and no freeze on that
// initially unfrozen state.
func TestContentModerationCallModeration_400DoesNotFreezeAPIKey(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusBadRequest)
		// Send a diagnostic body after the chosen HTTP status; this fixture ignores
		// body-write errors because the selected case observes received status and
		// key-health classification.
		_, _ = w.Write([]byte(`{"error":{"message":"Number of images (5) exceeds maximum of 1","type":"invalid_request_error","param":"input","code":"too_many_images"}}`))
	}))
	defer server.Close()

	cfg := defaultContentModerationConfig()
	cfg.BaseURL = server.URL
	cfg.APIKeys = []string{"sk-test"}
	cfg.RetryCount = 5
	svc := newTestModeration(nil, nil, nil, nil, nil, nil, nil)
	svc.Start()

	_, err := svc.callModeration(context.Background(), cfg, "hello")

	require.Error(t, err)
	require.Equal(t, 1, requestCount)
	status := svc.apiKeyStatusForHash(0, moderationAPIKeyHash("sk-test"), maskSecretTail("sk-test"), true, defaultContentModerationAPIKeyPriority, "")
	require.Equal(t, "error", status.Status)
	require.Equal(t, http.StatusBadRequest, status.LastHTTPStatus)
	require.Zero(t, status.FailureCount)
	require.Nil(t, status.FrozenUntil)
}

// TestContentModerationCallModeration_FreezesByHTTPStatus returns selected
// authentication, throttle, and server statuses from local endpoints with
// retries disabled. It checks frozen display state, one failure, received
// status, and a remaining duration inside each fixture tolerance.
func TestContentModerationCallModeration_FreezesByHTTPStatus(t *testing.T) {
	tests := []struct {
		name       string // selected freeze scenario label
		statusCode int    // fixture response HTTP status
		// minFreeze and maxFreeze bound the remaining duration observed after the
		// request and status read. Their tolerance admits elapsed fixture work while
		// still distinguishing the selected status freeze classes.
		minFreeze time.Duration // minimum accepted remaining freeze
		maxFreeze time.Duration // maximum accepted remaining freeze
	}{
		{name: "401 freezes ten minutes", statusCode: http.StatusUnauthorized, minFreeze: 9*time.Minute + 55*time.Second, maxFreeze: 10*time.Minute + time.Second},
		{name: "403 freezes ten minutes", statusCode: http.StatusForbidden, minFreeze: 9*time.Minute + 55*time.Second, maxFreeze: 10*time.Minute + time.Second},
		{name: "429 freezes one minute", statusCode: http.StatusTooManyRequests, minFreeze: 55 * time.Second, maxFreeze: time.Minute + time.Second},
		{name: "529 freezes one minute", statusCode: 529, minFreeze: 55 * time.Second, maxFreeze: time.Minute + time.Second},
		{name: "500 freezes ten seconds", statusCode: http.StatusInternalServerError, minFreeze: 5 * time.Second, maxFreeze: 11 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				// Send a diagnostic body after the chosen HTTP status; this fixture ignores
				// body-write errors because the selected case observes received status and
				// key-health classification.
				_, _ = w.Write([]byte(`{"error":{"message":"upstream error"}}`))
			}))
			defer server.Close()

			cfg := defaultContentModerationConfig()
			cfg.BaseURL = server.URL
			cfg.APIKeys = []string{"sk-test"}
			cfg.RetryCount = 0
			svc := newTestModeration(nil, nil, nil, nil, nil, nil, nil)
			svc.Start()

			_, err := svc.callModeration(context.Background(), cfg, "hello")

			require.Error(t, err)
			status := svc.apiKeyStatusForHash(0, moderationAPIKeyHash("sk-test"), maskSecretTail("sk-test"), true, defaultContentModerationAPIKeyPriority, "")
			require.Equal(t, "frozen", status.Status)
			require.Equal(t, tt.statusCode, status.LastHTTPStatus)
			require.Equal(t, 1, status.FailureCount)
			require.NotNil(t, status.FrozenUntil)
			remaining := time.Until(*status.FrozenUntil)
			require.GreaterOrEqual(t, remaining, tt.minFreeze)
			require.LessOrEqual(t, remaining, tt.maxFreeze)
		})
	}
}

// TestContentModerationTestAPIKeys_400DoesNotFreezeAPIKey probes a fresh
// explicit credential against a local HTTP-400 endpoint. It checks the overall
// probe succeeds with an error item, received status, zero freeze-count
// increment, and no active freeze.
func TestContentModerationTestAPIKeys_400DoesNotFreezeAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// Send a diagnostic body after the chosen HTTP status; this fixture ignores
		// body-write errors because the selected case observes received status and
		// key-health classification.
		_, _ = w.Write([]byte(`{"error":{"message":"invalid moderation request"}}`))
	}))
	defer server.Close()

	svc := newTestModeration(
		&contentModerationTestSettingRepo{values: map[string]string{}},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	svc.Start()
	result, err := svc.TestAPIKeys(context.Background(), TestContentModerationAPIKeysInput{
		APIKeys: []string{"sk-test"},
		BaseURL: server.URL,
		Prompt:  "hello",
	})

	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Equal(t, "error", result.Items[0].Status)
	require.Equal(t, http.StatusBadRequest, result.Items[0].LastHTTPStatus)
	require.Zero(t, result.Items[0].FailureCount)
	require.Nil(t, result.Items[0].FrozenUntil)
}
