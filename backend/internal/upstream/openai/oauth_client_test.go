package openai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
)

// oauthTestListenerCheck caches the first local-listener probe so restricted
// environments skip endpoint fixtures with one consistent failure reason.
var oauthTestListenerCheck struct {
	// once publishes the initial listener-probe err after closing its temporary
	// socket. Later fixtures reuse that observation for a consistent environment skip.
	once sync.Once // cached listener probe guard
	err  error     // cached listener probe failure
}

// OpenAIOAuthServiceSuite checks OAuth grants using local endpoints and
// transport recorders.
type OpenAIOAuthServiceSuite struct {
	suite.Suite                  // testify suite assertion support
	ctx         context.Context  // suite request context fixture
	srv         *httptest.Server // owned local endpoint server
	svc         *OAuthClient     // OAuth client under test
	received    chan url.Values  // buffered observed form channel
}

// openAIOAuthHTTPUpstreamRecorder captures TLS-pool request settings and
// returns configured token fixtures.
type openAIOAuthHTTPUpstreamRecorder struct {
	calledDo            bool                    // unexpected ordinary transport flag
	calledDoWithTLS     bool                    // selected TLS transport flag
	req                 *http.Request           // captured token HTTP request
	proxyURL            string                  // captured proxy selection value
	providerID          int64                   // captured transport provider identifier
	providerConcurrency int                     // captured provider capacity value
	profile             *tlsfingerprint.Profile // captured TLS fingerprint profile
	form                url.Values              // captured token grant form
	// statusCode overrides the fixture response status when nonzero; zero selects
	// HTTP 200 for successful token-exchange tests using this recorder.
	statusCode int
	// responseBody overrides fixture bytes when nonempty; empty text selects the
	// successful access-token and refresh-token JSON used by transport-selection
	// assertions.
	responseBody string
}

// Do records an unexpected ordinary-pool call and returns a fixture error.
func (r *openAIOAuthHTTPUpstreamRecorder) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	r.calledDo = true
	return nil, errors.New("Do should not be called")
}

// DoWithTLS captures the request, identity, proxy, provider, profile, and form
// for assertions. A zero status and empty body select the successful token
// fixture; the response reader owns the returned in-memory body.
func (r *openAIOAuthHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	r.calledDoWithTLS = true
	r.req = req
	r.proxyURL = proxyURL
	r.providerID = providerID
	r.providerConcurrency = providerConcurrency
	r.profile = profile
	// This recorder captures caller-generated form bytes for assertions; parsing
	// failures leave incomplete capture values that the grant-field checks will reject.
	body, _ := io.ReadAll(req.Body)
	form, _ := url.ParseQuery(string(body))
	r.form = form

	statusCode := r.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	responseBody := r.responseBody
	if responseBody == "" {
		responseBody = `{"access_token":"tls-at","refresh_token":"tls-rt","token_type":"bearer","expires_in":3600}`
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}, nil
}

// SetupTest initializes the suite context and one-slot form-observation channel
// before each case. The channel lets local handlers publish a request without
// blocking before the synchronous caller finishes reading its response.
func (s *OpenAIOAuthServiceSuite) SetupTest() {
	s.ctx = context.Background()
	s.received = make(chan url.Values, 1)
}

// TearDownTest closes any local fixture server and clears its pointer after
// each case. Cleanup runs for failure scenarios too, so the next test starts
// with no owned listener from this suite.
func (s *OpenAIOAuthServiceSuite) TearDownTest() {
	if s.srv != nil {
		s.srv.Close()
		s.srv = nil
	}
}

// setupServer starts a local endpoint and directs a fresh OAuthClient to its
// URL. Listener support is checked by newOAuthLocalTestServer; TearDownTest
// owns closure of the resulting server.
func (s *OpenAIOAuthServiceSuite) setupServer(handler http.HandlerFunc) {
	s.srv = newOAuthLocalTestServer(s.T(), handler)
	s.svc = &OAuthClient{tokenURL: s.srv.URL}
}

// TestExchangeCode_DefaultRedirectURI exchanges a code with blank redirect and
// client ID. The local handler checks all grant fields and canonical identity
// headers, and the result must contain the returned access and refresh tokens.
func (s *OpenAIOAuthServiceSuite) TestExchangeCode_DefaultRedirectURI() {
	errCh := make(chan string, 1)
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			errCh <- "method mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			errCh <- "ParseForm failed"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "authorization_code" {
			errCh <- "grant_type mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("client_id"); got != ClientID {
			errCh <- "client_id mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("code"); got != "code" {
			errCh <- "code mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("redirect_uri"); got != DefaultRedirectURI {
			errCh <- "redirect_uri mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("code_verifier"); got != "ver" {
			errCh <- "code_verifier mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		wantUA, wantOriginator := CodexCanonicalAuthIdentity()
		if got := r.Header.Get("User-Agent"); got != wantUA {
			errCh <- "user-agent mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("originator"); got != wantOriginator {
			errCh <- "originator mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","refresh_token":"rt","token_type":"bearer","expires_in":3600}`)
	}))

	resp, err := s.svc.ExchangeCode(s.ctx, "code", "ver", "", "", "")
	require.NoError(s.T(), err, "ExchangeCode")
	select {
	case msg := <-errCh:
		require.Fail(s.T(), msg)
	default:
	}
	require.Equal(s.T(), "at", resp.AccessToken)
	require.Equal(s.T(), "rt", resp.RefreshToken)
}

// TestRefreshToken_FormFields serves a refresh response from a local endpoint.
// It verifies refresh grant fields, default client ID, refresh scopes,
// canonical authentication headers, and decoded returned credentials.
func (s *OpenAIOAuthServiceSuite) TestRefreshToken_FormFields() {
	errCh := make(chan string, 1)
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			errCh <- "ParseForm failed"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			errCh <- "grant_type mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("refresh_token"); got != "rt" {
			errCh <- "refresh_token mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("client_id"); got != ClientID {
			errCh <- "client_id mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("scope"); got != RefreshScopes {
			errCh <- "scope mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		wantUA, wantOriginator := CodexCanonicalAuthIdentity()
		if got := r.Header.Get("User-Agent"); got != wantUA {
			errCh <- "user-agent mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("originator"); got != wantOriginator {
			errCh <- "originator mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at2","refresh_token":"rt2","token_type":"bearer","expires_in":3600}`)
	}))

	resp, err := s.svc.RefreshToken(s.ctx, "rt", "")
	require.NoError(s.T(), err, "RefreshToken")
	select {
	case msg := <-errCh:
		require.Fail(s.T(), msg)
	default:
	}
	require.Equal(s.T(), "at2", resp.AccessToken)
	require.Equal(s.T(), "rt2", resp.RefreshToken)
}

// TestRefreshToken_DefaultsToOpenAIClientID refreshes credentials without a
// supplied client ID. It expects one request containing ClientID and a decoded
// access token, protecting the default-client selection path.
func (s *OpenAIOAuthServiceSuite) TestRefreshToken_DefaultsToOpenAIClientID() {
	var seenClientIDs []string
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		clientID := r.PostForm.Get("client_id")
		seenClientIDs = append(seenClientIDs, clientID)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","refresh_token":"rt","token_type":"bearer","expires_in":3600}`)
	}))

	resp, err := s.svc.RefreshToken(s.ctx, "rt", "")
	require.NoError(s.T(), err, "RefreshToken")
	require.Equal(s.T(), "at", resp.AccessToken)
	// The default client ID requires a single request.
	require.Equal(s.T(), []string{ClientID}, seenClientIDs)
}

// TestRefreshToken_UseProvidedClientID refreshes with a custom client
// identifier accepted by the fixture. Exactly that ID must be observed once and
// the custom access and refresh tokens must return.
func (s *OpenAIOAuthServiceSuite) TestRefreshToken_UseProvidedClientID() {
	const customClientID = "custom-client-id"
	var seenClientIDs []string
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		clientID := r.PostForm.Get("client_id")
		seenClientIDs = append(seenClientIDs, clientID)
		if clientID != customClientID {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at-custom","refresh_token":"rt-custom","token_type":"bearer","expires_in":3600}`)
	}))

	resp, err := s.svc.RefreshTokenWithClientID(s.ctx, "rt", "", customClientID)
	require.NoError(s.T(), err, "RefreshTokenWithClientID")
	require.Equal(s.T(), "at-custom", resp.AccessToken)
	require.Equal(s.T(), "rt-custom", resp.RefreshToken)
	require.Equal(s.T(), []string{customClientID}, seenClientIDs)
}

// TestNonSuccessStatus_IncludesBody makes token exchange receive an HTTP 400
// with a plain body. The returned error must include both status and captured
// body for diagnostic handling.
func (s *OpenAIOAuthServiceSuite) TestNonSuccessStatus_IncludesBody() {
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad")
	}))

	_, err := s.svc.ExchangeCode(s.ctx, "code", "ver", DefaultRedirectURI, "", "")
	require.Error(s.T(), err)
	require.ErrorContains(s.T(), err, "status 400")
	require.ErrorContains(s.T(), err, "bad")
}

// TestRequestError_ClosedServer stops a local token endpoint before code
// exchange. The exchange must return an error containing request-failure
// context for the unavailable direct transport.
func (s *OpenAIOAuthServiceSuite) TestRequestError_ClosedServer() {
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	// Stop the fixture before sending so the exchange exercises direct connection
	// failure while its caller context is still live and no proxy is configured.
	s.srv.Close()

	_, err := s.svc.ExchangeCode(s.ctx, "code", "ver", DefaultRedirectURI, "", "")
	require.Error(s.T(), err)
	require.ErrorContains(s.T(), err, "request failed")
}

// TestExchangeCode_RequestErrorWithoutProxyReturnsProxyHint calls a closed
// local endpoint with no proxy configured. It asserts the proxy-required
// application reason and message when the caller context remains live.
func (s *OpenAIOAuthServiceSuite) TestExchangeCode_RequestErrorWithoutProxyReturnsProxyHint() {
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	// Stop the fixture before sending so the exchange exercises direct connection
	// failure while its caller context is still live and no proxy is configured.
	s.srv.Close()

	_, err := s.svc.ExchangeCode(s.ctx, "code", "ver", DefaultRedirectURI, "", "")

	require.Error(s.T(), err)
	require.Equal(s.T(), "OPENAI_OAUTH_PROXY_REQUIRED", apperror.Reason(err))
	require.Contains(s.T(), apperror.Message(err), "no proxy is configured")
}

// TestContextCancel blocks a local token handler until exchange has begun, then
// cancels its context. The asynchronous exchange must return an error after
// cancellation and the fixture handler is released.
func (s *OpenAIOAuthServiceSuite) TestContextCancel() {
	started := make(chan struct{})
	block := make(chan struct{})
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-block
	}))

	ctx, cancel := context.WithCancel(s.ctx)

	done := make(chan error, 1)
	// Run exchange independently while the handler signals arrival and blocks;
	// the parent cancels only after the request has reached this fixture endpoint.
	go func() {
		_, err := s.svc.ExchangeCode(ctx, "code", "ver", DefaultRedirectURI, "", "")
		done <- err
	}()

	// Wait for handler arrival before cancelling, then release the handler so
	// server teardown cannot remain blocked after the exchange returns its error.
	<-started
	cancel()
	close(block)

	err := <-done
	require.Error(s.T(), err)
}

// TestExchangeCode_UsesProvidedRedirectURI supplies a custom callback URL
// during code exchange. The handler rejects other redirect values, and success
// confirms the custom value reached the token form.
func (s *OpenAIOAuthServiceSuite) TestExchangeCode_UsesProvidedRedirectURI() {
	want := "http://localhost:9999/cb"
	errCh := make(chan string, 1)
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.PostForm.Get("redirect_uri"); got != want {
			errCh <- "redirect_uri mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"bearer","expires_in":1}`)
	}))

	_, err := s.svc.ExchangeCode(s.ctx, "code", "ver", want, "", "")
	require.NoError(s.T(), err, "ExchangeCode")
	select {
	case msg := <-errCh:
		require.Fail(s.T(), msg)
	default:
	}
}

// TestExchangeCode_UseProvidedClientID supplies a custom OAuth client
// identifier during code exchange. The handler accepts that identifier and the
// exchange must complete successfully with the fixture response.
func (s *OpenAIOAuthServiceSuite) TestExchangeCode_UseProvidedClientID() {
	wantClientID := "custom-exchange-client-id"
	errCh := make(chan string, 1)
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got := r.PostForm.Get("client_id"); got != wantClientID {
			errCh <- "client_id mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"bearer","expires_in":1}`)
	}))

	_, err := s.svc.ExchangeCode(s.ctx, "code", "ver", DefaultRedirectURI, "", wantClientID)
	require.NoError(s.T(), err, "ExchangeCode")
	select {
	case msg := <-errCh:
		require.Fail(s.T(), msg)
	default:
	}
}

// TestTokenURL_CanBeOverriddenWithQuery directs exchange to a fixture token URL
// containing a query string. It asserts successful exchange and form arrival,
// protecting support for that endpoint shape.
func (s *OpenAIOAuthServiceSuite) TestTokenURL_CanBeOverriddenWithQuery() {
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.received <- r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"bearer","expires_in":1}`)
	}))
	s.svc.tokenURL = s.srv.URL + "?x=1"

	_, err := s.svc.ExchangeCode(s.ctx, "code", "ver", DefaultRedirectURI, "", "")
	require.NoError(s.T(), err, "ExchangeCode")
	select {
	case <-s.received:
	default:
		require.Fail(s.T(), "expected server to receive request")
	}
}

// TestExchangeCode_SuccessButInvalidJSON returns HTTP 200 with a JSON content
// type and malformed content. Token exchange must return a decoding error
// instead of usable credentials.
func (s *OpenAIOAuthServiceSuite) TestExchangeCode_SuccessButInvalidJSON() {
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "not-valid-json")
	}))

	_, err := s.svc.ExchangeCode(s.ctx, "code", "ver", DefaultRedirectURI, "", "")
	require.Error(s.T(), err, "expected error for invalid JSON response")
}

// TestRefreshToken_NonSuccessStatus returns unauthorized status for a refresh
// grant. The refresh must fail and include status 401, keeping upstream status
// evidence available to its caller.
func (s *OpenAIOAuthServiceSuite) TestRefreshToken_NonSuccessStatus() {
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "unauthorized")
	}))

	_, err := s.svc.RefreshToken(s.ctx, "rt", "")
	require.Error(s.T(), err, "expected error for non-2xx status")
	require.ErrorContains(s.T(), err, "status 401")
}

// TestExchangeCode_WithTLSProfileUsesHTTPUpstream supplies a TLS profile and
// provider transport settings. It asserts pool selection, profile identity,
// proxy and capacity arguments, request profile and headers, grant fields, and
// returned credentials.
func (s *OpenAIOAuthServiceSuite) TestExchangeCode_WithTLSProfileUsesHTTPUpstream() {
	upstream := &openAIOAuthHTTPUpstreamRecorder{}
	profile := &tlsfingerprint.Profile{Name: "token-profile"}
	s.svc = &OAuthClient{tokenURL: "https://auth.com/oauth/token", httpUpstream: upstream}

	resp, err := s.svc.ExchangeCode(s.ctx, "code", "verifier", "", "http://proxy.local:8080", "client-id", OAuthTokenRequestOptions{
		UserAgent:           "Token UA",
		TLSProfile:          profile,
		ProviderID:          123,
		ProviderConcurrency: 2,
	})

	require.NoError(s.T(), err)
	require.Equal(s.T(), "tls-at", resp.AccessToken)
	require.False(s.T(), upstream.calledDo)
	require.True(s.T(), upstream.calledDoWithTLS)
	require.Same(s.T(), profile, upstream.profile)
	require.Equal(s.T(), "http://proxy.local:8080", upstream.proxyURL)
	require.Equal(s.T(), int64(123), upstream.providerID)
	require.Equal(s.T(), 2, upstream.providerConcurrency)
	require.Equal(s.T(), upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.req.Context()))
	require.Equal(s.T(), "application/x-www-form-urlencoded", upstream.req.Header.Get("Content-Type"))
	require.Equal(s.T(), "application/json", upstream.req.Header.Get("Accept"))
	require.Equal(s.T(), "Token UA", upstream.req.Header.Get("User-Agent"))
	require.Equal(s.T(), "authorization_code", upstream.form.Get("grant_type"))
	require.Equal(s.T(), "client-id", upstream.form.Get("client_id"))
	require.Equal(s.T(), "code", upstream.form.Get("code"))
	require.Equal(s.T(), DefaultRedirectURI, upstream.form.Get("redirect_uri"))
	require.Equal(s.T(), "verifier", upstream.form.Get("code_verifier"))
}

// TestRefreshToken_WithTLSProfileUsesHTTPUpstream supplies a TLS profile and
// custom identity to refresh. The recorder checks selected transport/profile,
// request identity and profile context, refresh grant fields, and returned
// access token.
func (s *OpenAIOAuthServiceSuite) TestRefreshToken_WithTLSProfileUsesHTTPUpstream() {
	upstream := &openAIOAuthHTTPUpstreamRecorder{}
	profile := &tlsfingerprint.Profile{Name: "refresh-profile"}
	s.svc = &OAuthClient{tokenURL: "https://auth.com/oauth/token", httpUpstream: upstream}

	resp, err := s.svc.RefreshTokenWithClientID(s.ctx, "refresh-token", "", "client-id", OAuthTokenRequestOptions{
		UserAgent:  "Refresh UA",
		TLSProfile: profile,
	})

	require.NoError(s.T(), err)
	require.Equal(s.T(), "tls-at", resp.AccessToken)
	require.True(s.T(), upstream.calledDoWithTLS)
	require.Same(s.T(), profile, upstream.profile)
	require.Equal(s.T(), "Refresh UA", upstream.req.Header.Get("User-Agent"))
	require.Equal(s.T(), upstreamcore.HTTPUpstreamProfileOpenAI, upstreamcore.HTTPUpstreamProfileFromContext(upstream.req.Context()))
	require.Equal(s.T(), "refresh_token", upstream.form.Get("grant_type"))
	require.Equal(s.T(), "refresh-token", upstream.form.Get("refresh_token"))
	require.Equal(s.T(), "client-id", upstream.form.Get("client_id"))
	require.Equal(s.T(), RefreshScopes, upstream.form.Get("scope"))
}

// TestRefreshToken_WithOnlyUserAgentKeepsReqPath supplies a custom User-Agent
// without a TLS profile. The local req-client endpoint must receive that header
// and return the access token successfully.
func (s *OpenAIOAuthServiceSuite) TestRefreshToken_WithOnlyUserAgentKeepsReqPath() {
	errCh := make(chan string, 1)
	s.setupServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "UA Only" {
			errCh <- "user-agent mismatch"
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","refresh_token":"rt","token_type":"bearer","expires_in":3600}`)
	}))

	resp, err := s.svc.RefreshTokenWithClientID(s.ctx, "rt", "", "client-id", OAuthTokenRequestOptions{UserAgent: "UA Only"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "at", resp.AccessToken)
	select {
	case msg := <-errCh:
		require.Fail(s.T(), msg)
	default:
	}
}

// TestNewOAuthClient_DefaultTokenURL constructs a client with no provider-aware
// transport. It checks the compiled token endpoint, protecting default
// construction for requests using the ordinary req client.
func TestNewOAuthClient_DefaultTokenURL(t *testing.T) {
	client := NewOAuthClient(nil)
	svc := client
	require.Equal(t, TokenURL, svc.tokenURL)
}

// TestOpenAIOAuthServiceSuite runs the local endpoint and TLS-recorder suite
// through testify. Per-case setup and teardown provide isolated clients and
// listener cleanup for its grant and failure assertions.
func TestOpenAIOAuthServiceSuite(t *testing.T) {
	suite.Run(t, new(OpenAIOAuthServiceSuite))
}

// newOAuthLocalTestServer checks local listener support and starts the supplied
// fixture handler. Callers own closure of the returned server. The first probe
// runs through a process-wide sync.Once and closes its temporary listener
// immediately.
//
// Later tests reuse that probe result rather than repeatedly attempting a
// forbidden listener. A failed first probe skips callers with its diagnostic,
// while a successful probe allows httptest to create each independent fixture
// server. This cached capability result reflects the initial environment for
// the remainder of the test process.
func newOAuthLocalTestServer(t testing.TB, handler http.Handler) *httptest.Server {
	t.Helper()
	oauthTestListenerCheck.once.Do(func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		oauthTestListenerCheck.err = err
		if listener != nil {
			_ = listener.Close()
		}
	})
	if oauthTestListenerCheck.err != nil {
		t.Skipf("local listeners are not permitted in this environment: %v", oauthTestListenerCheck.err)
	}
	return httptest.NewServer(handler)
}

// TestCreateOpenAIHTTPClient_Timeout120Seconds builds the shared OAuth HTTP
// client with a proxy URL. It checks the concrete client timeout is two
// minutes, protecting the token-exchange transport budget.
func TestCreateOpenAIHTTPClient_Timeout120Seconds(t *testing.T) {
	client, err := CreateOAuthHTTPClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Equal(t, 120*time.Second, client.Timeout)
}
