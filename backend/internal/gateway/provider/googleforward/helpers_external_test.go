package googleforward_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/googleforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// These fixture values supply an identifiable upstream compatibility error and
// image bytes for checking safe presentation and inline-image observation.
const (
	// geminiSkippedTestUpstreamMsg identifies the error inspected by policy
	// tests.
	geminiSkippedTestUpstreamMsg = "antigravity executor: invalid Gemini function call history"
	// geminiTestPNG is the base64 payload supplied to image observation
	// fixtures.
	geminiTestPNG = "iVBORw0KGgoAAAANSUhEUg=="
)

// antigravityCompatTokenCache returns a fixed token for compatibility
// forwarding fixtures.
type antigravityCompatTokenCache struct {
	token string // fixed fixture OAuth token
}

// httpUpstreamStub supplies a configured response/error pair for platform
// forwarding tests.
type httpUpstreamStub struct {
	// resp and err are returned together as configured. The response
	// pointer and body remain shared; repeated attempts needing fresh
	// readers use the indexed queue fixture.
	resp *http.Response // configured upstream response pointer
	err  error          // configured upstream exchange error
}

// queuedHTTPUpstreamStub records request bodies and returns indexed exchange
// fixture results.
type queuedHTTPUpstreamStub struct {
	// responses and errors use the same zero-based dispatch index; missing
	// entries are nil. requestBodies records every dispatch, while
	// callCount advances before onCall inspects or changes the fixture.
	responses     []*http.Response // indexed upstream response pointers
	errors        []error          // indexed upstream error values
	requestBodies [][]byte         // captured bytes per exchange
	callCount     int              // started fixture dispatch count
	// onCall runs after body capture and callCount increment but before
	// response/error lookup, so callbacks can update fixture inputs for the
	// current dispatch.
	//
	// per-exchange fixture observation callback
	onCall func(*http.Request, *queuedHTTPUpstreamStub)
}

// antigravitySettingRepoStub exposes missing settings and rejects unused
// fixture repository operations.
type antigravitySettingRepoStub struct{}

// geminiDependencies retains the supplied services used to assemble a Gemini
// test adapter.
type geminiDependencies struct {
	// providerRepo, tokenProvider, httpUpstream, healthObserver,
	// quotaPrecheck, and responseHeaderFilter are borrowed by
	// newGeminiFixture and its observer; the test keeps the installed
	// services usable during forwarding.
	//
	// cfg is copied through fixtureOptions; nil supplies the read cap with
	// Configured false, while supplied options enable configuration and
	// default nonpositive read limits.
	//
	// optional startup option pointer
	cfg *googleforward.Options
	// fixture provider persistence adapter
	providerRepo gatewayprovider.ExecutionProviderStore
	// fixture Gemini credential source
	tokenProvider *providercore.GeminiTokenSource
	// fixture HTTP exchange transport
	httpUpstream httpclient.UpstreamTransport
	// fixture explicit policy observer
	healthObserver *provideradapter.UpstreamHealth
	// fixture provider quota precheck
	quotaPrecheck *providercore.GeminiPrecheck
	// fixture response header policy
	responseHeaderFilter *egress.CompiledHeaderFilter
}

// antigravityDependencies retains shared fixture services used to assemble an
// Antigravity adapter.
type antigravityDependencies struct {
	// settingService, providerRepo, tokenProvider, httpUpstream,
	// healthObserver, cache, and internal500Cache remain borrowed by
	// assembled services; callers keep these fixtures usable through
	// synchronous retries and output.
	options googleforward.Options // fixture startup option values
	// fixture gateway runtime readers
	settingService *gatewayprovider.RuntimeReaders
	// fixture provider persistence adapter
	providerRepo gatewayprovider.ExecutionProviderStore
	// fixture Antigravity credential source
	tokenProvider *providercore.AntigravityTokenSource
	// fixture HTTP exchange transport
	httpUpstream httpclient.UpstreamTransport
	// fixture explicit policy observer
	healthObserver *provideradapter.UpstreamHealth
	cache          session.GatewayCache // fixture sticky binding cache
	// fixture internal-error counter cache
	internal500Cache providercore.Internal500CounterCache
}

// geminiCompatHTTPUpstreamStub records requests and supplies outer-copied
// responses for compatibility assertions.
type geminiCompatHTTPUpstreamStub struct {
	// response is copied as an outer value for each successful dispatch;
	// headers and body reader remain shared with the configured template.
	response *http.Response // configured upstream response template
	err      error          // configured upstream exchange error
	calls    int            // observed transport dispatch count
	// lastReq retains the latest dispatched request for wire assertions;
	// the test consumes its shared reader before discarding the fixture.
	lastReq *http.Request // last borrowed HTTP request
}

// GetAccessToken returns the fixed fixture token regardless of cache key. Tests
// use it to distinguish cached credentials from provider record tokens; this
// fixture tracks neither expiry nor misses.
func (c *antigravityCompatTokenCache) GetAccessToken(context.Context, string) (string, error) {
	return c.token, nil
}

// SetAccessToken accepts writes while retaining the fixed compatibility fixture
// token.
func (c *antigravityCompatTokenCache) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

// DeleteAccessToken accepts deletion while retaining the fixed compatibility
// fixture token.
func (c *antigravityCompatTokenCache) DeleteAccessToken(context.Context, string) error {
	return nil
}

// AcquireRefreshLock reports success for every fixture lock request. It records
// no ownership or synchronization, so this token cache is used for synchronous
// compatibility credential tests.
func (c *antigravityCompatTokenCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}

// ReleaseRefreshLock accepts release for the fixture lock with no retained
// ownership.
func (c *antigravityCompatTokenCache) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

// newAntigravityCompatibilityFixture installs a fixed cached OAuth token and
// supplied transport. Platform settings use the missing-setting fixture and
// configured response options, letting tests inspect token choice and upstream
// route.
func newAntigravityCompatibilityFixture(cfg googleforward.Options, upstream httpclient.UpstreamTransport) *googleforward.Antigravity {
	tokenProvider := newAntigravityTokenSourceForTest(&antigravityCompatTokenCache{token: "fresh-oauth-token"})
	return newAntigravityFixture(antigravityDependencies{
		tokenProvider:  tokenProvider,
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&antigravitySettingRepoStub{}), options: fixtureOptions(&cfg),
	})
}

// newAntigravityCompatContext creates a Gin request and recorder for compatible
// output. The supplied bytes become the inbound reader, and callers retain the
// context through synchronous forwarding and response assertions.
func newAntigravityCompatContext(method, path string, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	return c, recorder
}

// newAntigravityStreamFixture assembles response-only Antigravity dependencies
// with current test options. It supplies runtime settings but no exchange
// transport, so callers use its response adapter with their prepared response
// fixtures.
func newAntigravityStreamFixture(cfg *googleforward.Options) *googleforward.Antigravity {
	return newAntigravityFixture(antigravityDependencies{
		settingService: newExecutionReadersFixture(nil), options: fixtureOptions(cfg),
	})
}

// Do returns the configured response and error pair for this fixture. The
// response pointer and body stay shared, so tests needing multiple independent
// attempts supply an indexed response queue.
func (s *httpUpstreamStub) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.resp, s.err
}

// DoWithTLS returns the configured pair while ignoring transport inputs and TLS
// selection. Response and body remain shared with the fixture, and platform
// execution owns any successful response closure.
func (s *httpUpstreamStub) DoWithTLS(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.resp, s.err
}

// Do records request bytes and returns the response/error pair at the saved
// dispatch index. It restores the request reader before incrementing callCount
// and invoking onCall.
//
// The callback can change response or error fixtures before lookup, while the
// saved index keeps both result slices associated with the same dispatch.
// Missing entries are nil, and a wholly absent pair returns an unexpected-call
// error. Returned response handles remain shared with the configured queue and
// are closed by the consuming execution path.
func (s *queuedHTTPUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	if req != nil && req.Body != nil {
		// Capture available request bytes and rebuild the reader before
		// invoking observations so both the fixture callback and later
		// assertions can inspect the same sent body.
		body, _ := io.ReadAll(req.Body)
		s.requestBodies = append(s.requestBodies, body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	} else {
		s.requestBodies = append(s.requestBodies, nil)
	}

	idx := s.callCount
	s.callCount++
	if s.onCall != nil {
		s.onCall(req, s)
	}

	var resp *http.Response
	if idx < len(s.responses) {
		resp = s.responses[idx]
	}
	var err error
	if idx < len(s.errors) {
		err = s.errors[idx]
	}
	if resp == nil && err == nil {
		return nil, errors.New("unexpected upstream call")
	}
	return resp, err
}

// DoWithTLS delegates indexed response dispatch and request recording to Do.
func (s *queuedHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, providerID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, providerID, concurrency)
}

// Get panics when a fixture invokes the unused full-setting read operation.
func (s *antigravitySettingRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue reports ErrSettingNotFound for every requested key in this fixture.
// Runtime readers therefore use their defaults, while other settings repository
// operations expose unexpected fixture calls by panicking.
func (s *antigravitySettingRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	return "", settingscore.ErrSettingNotFound
}

// Set panics when a fixture invokes the unused setting write operation.
func (s *antigravitySettingRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple panics when a fixture invokes the unused batch setting read.
func (s *antigravitySettingRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}

// SetMultiple panics when a fixture invokes the unused batch setting write.
func (s *antigravitySettingRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

// GetAll panics when a fixture invokes the unused complete settings read.
func (s *antigravitySettingRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete panics when a fixture invokes the unused setting deletion operation.
func (s *antigravitySettingRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// fixtureOptions returns a copied configured option value when input exists.
// Nonpositive supplied read limits select 128 MiB; nil input returns that read
// cap with the configuration flag left false.
func fixtureOptions(cfg *googleforward.Options) googleforward.Options {
	if cfg == nil {
		return googleforward.Options{ResponseReadLimit: 128 << 20}
	}
	out := *cfg
	out.Configured = true
	if out.ResponseReadLimit <= 0 {
		out.ResponseReadLimit = 128 << 20
	}
	return out
}

// geminiQuotaLocation loads the Los Angeles zone for quota reset fixtures.
// Missing zone data selects a fixed UTC-minus-eight location so the reset
// helper remains available to the tests.
func geminiQuotaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		// When zone data is unavailable, use a fixed UTC-minus-eight
		// fixture location so quota tests can still calculate their
		// daily reset input.
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}

// nextGeminiDailyResetUnix returns the next provider daily reset as Unix
// seconds.
func nextGeminiDailyResetUnix() *int64 {
	value := providercore.GeminiDailyResetTime(time.Now(), geminiQuotaLocation()).Unix()
	return &value
}

// parseGeminiReset delegates response reset parsing with the daily fixture time
// callback.
func parseGeminiReset(body []byte) *int64 {
	return gemininative.ParseGeminiRateLimitResetTime(body, nextGeminiDailyResetUnix)
}

// newGeminiFixture borrows supplied token, transport, policy, quota, and header
// services. It installs a default error observer and binds persistence only
// when a provider store accompanies the fixture.
func newGeminiFixture(d geminiDependencies) *googleforward.Gemini {
	health := &provideradapter.GeminiErrorObserver{
		Other:      d.healthObserver,
		Precheck:   d.quotaPrecheck,
		DailyReset: nextGeminiDailyResetUnix,
		ResetTime:  parseGeminiReset,
	}
	if d.providerRepo != nil {
		health.SetRateLimited = d.providerRepo.SetRateLimited
	}
	return &googleforward.Gemini{
		Attributes:   &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}},
		Options:      fixtureOptions(d.cfg),
		Tokens:       d.tokenProvider,
		Health:       d.healthObserver,
		Errors:       health,
		Transport:    d.httpUpstream,
		HeaderFilter: d.responseHeaderFilter,
	}
}

// newAntigravityFixture assembles shared health, retry, and error services from
// borrowed fixture dependencies. Base URL and cooldown callbacks read the test
// environment when invoked, and optional runtime readers populate
// gateway/routing settings.
func newAntigravityFixture(d antigravityDependencies) *googleforward.Antigravity {
	o := d.options
	health := &providercore.AntigravityHealth{
		Store:     d.providerRepo,
		Counter:   d.internal500Cache,
		ModelKeys: provideradapter.AntigravityModelLimitKeys,
		Error:     slog.Error,
		Warn:      slog.Warn,
		Info:      slog.Info,
		Logf:      func(format string, args ...any) { logging.LegacyPrintf("service.antigravity_gateway", format, args...) },
	}
	retry := &provideradapter.AntigravityRetry{
		Health: health,
		BaseURL: func(value *providercore.Record) string {
			return antigravity.ResolveAntigravityForwardBaseURL("auto", provideradapter.AntigravityPaidTier(value))
		},
		BodyLimit: func() int64 {
			// Bound retry-error inspection to a default diagnostic prefix before
			// optional logging raises the captured-body limit for the fixture.
			limit := int64(512 << 10)
			if o.LogErrorBody && o.LogErrorBodyMaxBytes > int(limit) {
				limit = int64(o.LogErrorBodyMaxBytes)
			}
			return limit
		},
		TruncateString: logredact.TruncateUTF8,
		SafeURL:        logredact.SafeUpstreamURL,
	}
	if d.healthObserver != nil {
		retry.Policy = d.healthObserver.Core
	}
	observer := &provideradapter.AntigravityErrorObserver{
		Health:         health,
		Other:          d.healthObserver,
		LogConfig:      o.LogConfig,
		TruncateString: logredact.TruncateUTF8,
		ResetTime:      parseGeminiReset,
		DefaultDuration: func() time.Duration {
			seconds := 0
			return provideradapter.AntigravityFallbackDuration(seconds)
		},
	}
	if d.providerRepo != nil {
		observer.SetRateLimited = d.providerRepo.SetRateLimited
	}
	r := &googleforward.Antigravity{
		Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}},
		Options:    o,
		Tokens:     d.tokenProvider,
		Retry:      retry,
		Errors:     observer,
		Store:      d.providerRepo,
		Transport:  d.httpUpstream,
		Sticky:     d.cache,
	}
	if d.settingService != nil {
		r.Gateway = d.settingService.Gateway
		r.Routing = d.settingService.Routing
	}
	return r
}

// newExecutionReadersFixture wraps a supplied settings repository before
// building execution readers. Nil input selects the testkit default reader
// source, and returned services retain their repository for later setting
// queries.
func newExecutionReadersFixture(repo settingscore.Repository) *gatewayprovider.RuntimeReaders {
	if repo != nil {
		repo = settingscore.New(repo)
	}
	value := gatewaytestkit.RuntimeReaders(repo)
	return value
}

// geminiSkippedTestUpstreamBody returns JSON containing the identifiable
// compatibility fixture error message.
func geminiSkippedTestUpstreamBody() string {
	return `{"error":{"code":null,"message":"` + geminiSkippedTestUpstreamMsg + `","param":"","type":"invalid_request_error"}}`
}

// newGeminiErrorFixture builds one response template and Gemini adapter for
// error output tests. The returned transport records the request while the
// installed health observer uses a fresh policy store.
func newGeminiErrorFixture(status int, body string) (*googleforward.Gemini, *geminiCompatHTTPUpstreamStub) {
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: status,

			Header: http.Header{"Content-Type": []string{"application/json"}},

			Body: io.NopCloser(strings.NewReader(body)),
		},
	}
	svc := newGeminiFixture(geminiDependencies{
		httpUpstream: httpStub,

		cfg: &googleforward.Options{},

		healthObserver: newUpstreamHealthForTest(&gatewaytestkit.ErrorPolicyStore{}, &googleforward.Options{}, nil, providercore.HealthOptions{}, nil),
	})
	return svc, httpStub
}

// geminiPoolModeAPIKeyProvider constructs an API key target with pool handling
// enabled.
func geminiPoolModeAPIKeyProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           700,

			Platform: capability.PlatformGemini,

			Type: capability.ProviderTypeAPIKey,

			Credentials: map[string]any{
				"api_key":   "test-key",
				"pool_mode": true,
			},
		},
	}
}

// geminiCustomCodesAPIKeyProvider constructs a target admitting status 429 in
// custom error policy.
func geminiCustomCodesAPIKeyProvider() *gatewayprovider.ExecutionProvider {
	return &gatewayprovider.ExecutionProvider{
		Record: providercore.Record{
			LoadLocation: time.LoadLocation,
			ID:           701,

			Platform: capability.PlatformGemini,

			Type: capability.ProviderTypeAPIKey,

			Credentials: map[string]any{
				"api_key": "test-key",

				"custom_error_codes_enabled": true,

				"custom_error_codes": []any{float64(429)},
			},
		},
	}
}

// newGeminiImageTestContext creates fresh attempt state with a native Google
// HTTP adapter. Tests own the recorder/context through image observation, and
// each construction starts the attempt counter at zero.
func newGeminiImageTestContext(t *testing.T) *googleforward.AttemptForTest {
	t.Helper()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost,
		"/v1beta/models/nana-banana-2:generateContent", strings.NewReader("{}"))
	return &googleforward.AttemptForTest{Output: gatewayhttp.NewGoogleBoundary(c, googleforward.Options{ResponseReadLimit: 128 << 20}, false)}
}

// geminiImageResponse wraps supplied parts JSON in a successful model-candidate
// fixture.
func geminiImageResponse(parts string) string {
	return `{"candidates":[{"content":{"role":"model","parts":[` + parts + `]},"finishReason":"STOP"}]}`
}

// Do records the borrowed request and returns an outer copy of its response
// template. Configured errors or absent templates return failure; successful
// copies share headers and body with the fixture.
func (s *geminiCompatHTTPUpstreamStub) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	s.calls++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	if s.response == nil {
		return nil, fmt.Errorf("missing stub response")
	}
	resp := *s.response
	return &resp, nil
}

// DoWithTLS delegates compatible response recording and template selection to
// Do.
func (s *geminiCompatHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, providerID, providerConcurrency)
}

// newGeminiTokenSourceForTest builds default credential options including the
// Vertex test adapter.
func newGeminiTokenSourceForTest() *providercore.GeminiTokenSource {
	return &providercore.GeminiTokenSource{Options: providercore.GeminiTokenOptions{
		Debug: slog.Debug,
		Warn:  slog.Warn,

		Vertex: func(ctx context.Context, value *providercore.Record) (string, error) {
			return provideradapter.VertexServiceAccountAccessToken(ctx, nil, value)
		},
	}}
}

// newAntigravityTokenSourceForTest constructs credential options using the
// supplied token cache.
func newAntigravityTokenSourceForTest(cache providercore.AccessTokenCache) *providercore.AntigravityTokenSource {
	return &providercore.AntigravityTokenSource{Options: providercore.AntigravityTokenOptions{
		Cache: cache, Debug: slog.Debug, Warn: slog.Warn,
	}}
}

// newUpstreamHealthForTest binds borrowed store, cache, options, and runtime
// readers into a health observer. The supplied Google options parameter is
// unused, leaving health behavior controlled by the explicit fixture inputs.
func newUpstreamHealthForTest(store gatewayprovider.ExecutionProviderStore, _ *googleforward.Options, cache providercore.TempUnschedCache, options providercore.HealthOptions, readers *gatewayprovider.RuntimeReaders) *provideradapter.UpstreamHealth {
	return gatewaytestkit.NewHealthObserver(gatewaytestkit.HealthInput{Store: store, Cache: cache, Options: options, Readers: readers})
}
