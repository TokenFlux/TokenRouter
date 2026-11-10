//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// oauth_integration_test.go exercises the mounted protocol with real PostgreSQL
// transactions and the official MCP OAuth client. Browser JWT admission uses
// the existing verified-session fixture; account role checks, OAuth storage,
// signatures, PKCE, consent consumption, and credential rotation are production code.

// oauthFixture retains one migrated OAuth server and its manual-token MCP fixture.
type oauthFixture struct {
	repo        *postgres.MCPOAuthStore  // migrated OAuth fixture repository
	*mcpFixture                          // existing tool and browser fixtures
	db          *sql.DB                  // migrated PostgreSQL database connection
	oauth       *provider.MCPOAuthServer // initial protocol instance for fixture assertions
	config      *oauthFrontendFixture    // Mutable single-server site settings.
	http        *httptest.Server         // canonical OAuth and MCP server
	userID      int64                    // persisted administrator account identifier
}

// oauthLimiterFixture isolates protocol persistence tests from Redis admission limits.
type oauthLimiterFixture struct{}

// oauthFrontendFixture holds the single test server's editable site configuration.
type oauthFrontendFixture struct {
	mu  sync.RWMutex // Protects HTTP and test configuration access.
	url string       // Current public frontend address.
	err error        // Injected configuration read failure.
}

// pausedOAuthRepository holds an exchange immediately before its replacement access row.
// The transaction has already consumed the old refresh row, allowing the test to
// observe cleanup waiting on the family locks while the credential deadline passes.
type pausedOAuthRepository struct {
	identity.MCPOAuthRepository               // production OAuth persistence implementation
	entered                     chan struct{} // replacement insertion reached signal
	resume                      chan struct{} // permission to finish insertion
}

// oauthRefreshResult carries an HTTP refresh response between the test goroutines.
type oauthRefreshResult struct {
	status int            // HTTP token response status
	pair   map[string]any // decoded OAuth response fields
}

// failingRevocationRepository injects failures around production PostgreSQL revocation writes.
type failingRevocationRepository struct {
	identity.MCPOAuthRepository        // production OAuth persistence implementation
	stage                       string // selected failing transaction stage
}

// newOAuthFixture mounts OAuth beside the existing production MCP handler.
// Each test owns an isolated migrated database. OAuth and manual grants both
// begin at numeric identifier one so cross-source ownership checks are exercised.
func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	base := newMCPFixture(t)
	db := postgrescontainer.New(t)
	var id int64
	require.NoError(t, db.QueryRow(`INSERT INTO users(email,password_hash,role,status) VALUES('oauth@example.invalid','unused','admin','active') RETURNING id`).Scan(&id))
	router, ok := base.handler.router.(*gin.Engine)
	require.True(t, ok)
	server := httptest.NewUnstartedServer(router)
	issuer := "http://" + server.Listener.Addr().String()
	repo := postgres.NewMCPOAuthStore(db)
	config := &oauthFrontendFixture{url: issuer}
	oauth := provider.NewMCPOAuthServer(repo, base.accounts, config, bytes.Repeat([]byte{7}, 32))
	base.handler.options.OAuth = oauth
	base.handler.options.Limiter = &oauthLimiterFixture{}
	base.handler.tokens.SetOAuth(oauth)
	// Public protocol routes are added before starting this listener. Existing
	// browser routes remain owned by the original fixture's authenticated middleware.
	base.handler.mountOAuth(router, router.Group("/api/v1/admin/mcp", func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer browser" {
			c.AbortWithStatus(401)
			return
		}
		authctx.SetPrincipal(c, identity.Principal{UserID: id, Role: identity.RoleAdmin, CredentialKind: "jwt", SessionID: "fixture-session"}, 1, "oauth@example.invalid")
		c.Next()
	}))
	server.Start()
	t.Cleanup(server.Close)
	current, err := oauth.Current(t.Context())
	require.NoError(t, err)
	return &oauthFixture{config: config, mcpFixture: base, repo: repo, db: db, oauth: current, http: server, userID: id}
}

// register creates a public OAuth client through the actual dynamic registration endpoint.
func (f *oauthFixture) register(t *testing.T) string {
	t.Helper()
	body := `{"client_name":"Fixture MCP","redirect_uris":["http://127.0.0.1:43111/callback"],"grant_types":["authorization_code","refresh_token"],"token_endpoint_auth_method":"none"}`
	response, err := http.Post(f.http.URL+"/mcp/oauth/register", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	var data map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&data))
	require.Equal(t, 201, response.StatusCode, data)
	return oauthString(t, data, "client_id")
}

// consent opens the real authorization endpoint and completes the bound browser decision.
// The fixture supplies a verified JWT session to the real review and decision
// HTTP handlers, then preserves callback state and issuer for the SDK to validate.
func (f *oauthFixture) consent(t *testing.T, target string) *url.URL {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(target)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, 302, response.StatusCode, string(raw))
	redirect, err := url.Parse(response.Header.Get("Location"))
	require.NoError(t, err)
	reviewResponse := f.browser(t, http.MethodGet, "/api/v1/admin/mcp/oauth/requests/"+redirect.Query().Get("request"), "browser")
	require.Equal(t, http.StatusOK, reviewResponse.StatusCode)
	var review struct {
		Data provider.MCPOAuthReview `json:"data"` // authenticated browser review data
	}
	require.NoError(t, json.NewDecoder(reviewResponse.Body).Decode(&review))
	require.NoError(t, reviewResponse.Body.Close())
	decision, err := json.Marshal(map[string]any{"nonce": review.Data.Nonce, "approve": true, "permissions": []string{"fixture.get", "fixture.protected", "mcp.confirmations.get", "mcp.files.upload"}})
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.http.URL+"/api/v1/admin/mcp/oauth/requests/"+review.Data.Request.ID+"/decision", bytes.NewReader(decision))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer browser")
	request.Header.Set("Content-Type", "application/json")
	decisionResponse, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	var outcome struct {
		Data struct {
			RedirectURI string `json:"redirect_uri"` // validated client callback address
		} `json:"data"` // committed browser decision result
	}
	require.NoError(t, json.NewDecoder(decisionResponse.Body).Decode(&outcome))
	require.NoError(t, decisionResponse.Body.Close())
	require.Equal(t, http.StatusOK, decisionResponse.StatusCode)
	callback := outcome.Data.RedirectURI
	parsed, err := url.Parse(callback)
	require.NoError(t, err)
	require.Equal(t, f.http.URL, parsed.Query().Get("iss"))
	return parsed
}

// code creates a fresh PKCE-bound authorization code for the supplied client.
func (f *oauthFixture) code(t *testing.T, clientID string) (string, string) {
	t.Helper()
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	values := url.Values{"client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "response_type": {"code"}, "scope": {"mcp offline_access"}, "state": {"fixture-state-123456"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "resource": {f.oauth.Resource()}}
	return f.consent(t, f.http.URL+"/mcp/oauth/authorize?"+values.Encode()).Query().Get("code"), verifier
}

// exchange submits token parameters and returns the raw protocol status and decoded body.
func (f *oauthFixture) exchange(t *testing.T, values url.Values) (int, map[string]any) {
	t.Helper()
	response, err := http.PostForm(f.http.URL+"/mcp/oauth/token", values)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	var data map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&data))
	return response.StatusCode, data
}

// TestOAuthSDKDiscoveryAndInvocation follows discovery, dynamic registration, browser
// approval, PKCE exchange, and a management query with the official SDK. The
// same OAuth connection reflects later changes to its result masking preference.
func TestOAuthSDKDiscoveryAndInvocation(t *testing.T) {
	f := newOAuthFixture(t)
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{ClientName: "Official SDK", RedirectURIs: []string{"http://127.0.0.1:43111/callback"}, GrantTypes: []string{"authorization_code", "refresh_token"}, TokenEndpointAuthMethod: "none"}}, RequestRefreshToken: true, NewTokenSource: func(ctx context.Context, config *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
		// Force the SDK's standard refresh path before its first MCP request;
		// golang.org/x/oauth2 omits resource and inherits the stored family audience.
		token.Expiry = time.Now().Add(-time.Minute)
		return config.TokenSource(ctx, token), nil
	}, AuthorizationCodeFetcher: func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		callback := f.consent(t, args.URL)
		return &auth.AuthorizationResult{Code: callback.Query().Get("code"), State: callback.Query().Get("state"), Iss: callback.Query().Get("iss")}, nil
	}})
	require.NoError(t, err)
	// The SDK OAuth transport owns discovery and token exchange before MCP initialization.
	client := mcp.NewClient(&mcp.Implementation{Name: "OAuth acceptance", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", OAuthHandler: handler, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()
	params := &mcp.CallToolParams{Name: "read_operation", Arguments: map[string]any{"operation_id": "fixture.get", "path": map[string]string{"id": "42"}}}
	result, err := session.CallTool(t.Context(), params)
	require.NoError(t, err)
	require.False(t, result.IsError, result)
	require.Contains(t, jsonTestValue[*mcp.TextContent](t, result.Content[0]).Text, `"integration_token":"fixture-routing-label"`)

	grants, err := f.repo.ListGrants(t.Context(), f.userID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	grant := grants[0]
	require.False(t, grant.RedactToolResults)
	input := identity.MCPTokenInput{
		Name: grant.Name, Enabled: true, Permissions: grant.Permissions,
		RedactToolResults: new(true), ExpectedUpdatedAt: &grant.UpdatedAt,
	}
	updated, err := f.oauth.UpdateGrant(t.Context(), f.userID, grant.ID, input)
	require.NoError(t, err)
	require.True(t, updated.RedactToolResults)
	input.ExpectedUpdatedAt = &updated.UpdatedAt
	input.RedactToolResults = nil
	updated, err = f.oauth.UpdateGrant(t.Context(), f.userID, grant.ID, input)
	require.NoError(t, err)
	require.True(t, updated.RedactToolResults)

	result, err = session.CallTool(t.Context(), params)
	require.NoError(t, err)
	require.False(t, result.IsError, result)
	require.Contains(t, jsonTestValue[*mcp.TextContent](t, result.Content[0]).Text, `"integration_token":"***"`)
}

// TestOAuthRotationReplayAndLiveGrants exchanges real credentials and rotates the
// refresh token. Replaying the previous refresh revokes the entire family,
// while a failed PKCE attempt leaves the unconsumed code available for correction.
func TestOAuthRotationReplayAndLiveGrants(t *testing.T) {
	f := newOAuthFixture(t)
	clientID := f.register(t)
	code, verifier := f.code(t, clientID)
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {"incorrect"}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}}
	status, _ := f.exchange(t, values)
	require.Equal(t, 400, status)
	values.Set("code_verifier", verifier)
	exchangeStarted := time.Now()
	status, data := f.exchange(t, values)
	require.Equal(t, 200, status, data)
	access := oauthString(t, data, "access_token")
	refresh := oauthString(t, data, "refresh_token")
	grant, err := f.oauth.Authenticate(t.Context(), access)
	require.NoError(t, err)
	require.Equal(t, "oauth", grant.Source)
	// Protocol TTL rounds down and excludes time spent processing the exchange.
	require.InDelta(t, 900, data["expires_in"], 1+time.Since(exchangeStarted).Seconds())
	values = url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}, "resource": {f.oauth.Resource()}}
	status, next := f.exchange(t, values)
	require.Equal(t, 200, status, next)
	current, err := f.oauth.Authenticate(t.Context(), oauthString(t, next, "access_token"))
	require.NoError(t, err)
	require.Equal(t, grant.ID, current.ID)
	status, _ = f.exchange(t, values)
	require.Equal(t, 400, status)
	_, err = f.oauth.Authenticate(t.Context(), oauthString(t, next, "access_token"))
	require.Error(t, err)
	_, err = f.oauth.Authenticate(t.Context(), access)
	require.Error(t, err)
	var leaked int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM mcp_oauth_sessions WHERE payload::text LIKE $1 OR payload::text LIKE $2`, "%"+access+"%", "%"+refresh+"%").Scan(&leaked))
	require.Zero(t, leaked)
}

// TestOAuthConcurrentCodeConsumption submits one authorization code simultaneously.
// PostgreSQL locking permits one successful exchange; replay detection rejects the
// duplicate and revokes any credentials that the first exchange already created.
func TestOAuthConcurrentCodeConsumption(t *testing.T) {
	f := newOAuthFixture(t)
	clientID := f.register(t)
	code, verifier := f.code(t, clientID)
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}}
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wg.Go(func() { status, _ := f.exchange(t, values); statuses <- status })
	}
	wg.Wait()
	close(statuses)
	successes := 0
	for status := range statuses {
		if status == 200 {
			successes++
		} else {
			require.Equal(t, 400, status)
		}
	}
	require.Equal(t, 1, successes)
}

// Allow admits fixture protocol requests while real rate-limit behavior has separate tests.
func (*oauthLimiterFixture) Allow(context.Context, string, int, time.Duration) (bool, int64, time.Duration, error) {
	return true, 0, 0, nil
}

// TestOAuthApprovalSurvivesRefreshAndRejectsNarrowing binds a sensitive operation
// to the stable application grant, rotates its credential, and resumes approval.
// A later permission update invalidates another pending confirmation immediately.
func TestOAuthApprovalSurvivesRefreshAndRejectsNarrowing(t *testing.T) {
	f := newOAuthFixture(t)
	clientID := f.register(t)
	code, verifier := f.code(t, clientID)
	status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}})
	require.Equal(t, 200, status, pair)
	first := oauthString(t, pair, "access_token")
	client := mcp.NewClient(&mcp.Implementation{Name: "OAuth confirmation", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{first}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()
	arguments := map[string]any{"operation_id": "fixture.protected", "body": map[string]any{"value": "fixture"}}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "execute_operation", Arguments: arguments})
	require.NoError(t, err)
	require.False(t, result.IsError, result)
	data := oauthResultMap(t, result)
	require.Equal(t, "confirmation_required", data["status"])
	confirmation := oauthString(t, data, "confirmation_id")
	status, next := f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {oauthString(t, pair, "refresh_token")}})
	require.Equal(t, 200, status, next)
	response := f.browser(t, http.MethodPost, "/api/v1/admin/mcp/confirmations/"+confirmation+"/approve", "browser")
	require.Equal(t, 200, response.StatusCode)
	require.NoError(t, response.Body.Close())
	refreshed, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{oauthString(t, next, "access_token")}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, refreshed.Close()) }()
	arguments["confirmation_id"] = confirmation
	result, err = refreshed.CallTool(t.Context(), &mcp.CallToolParams{Name: "execute_operation", Arguments: arguments})
	require.NoError(t, err)
	require.False(t, result.IsError, result)
	require.Equal(t, int64(1), f.writes.Load())
	delete(arguments, "confirmation_id")
	result, err = refreshed.CallTool(t.Context(), &mcp.CallToolParams{Name: "execute_operation", Arguments: arguments})
	require.NoError(t, err)
	pending := oauthString(t, oauthResultMap(t, result), "confirmation_id")
	grants, err := f.repo.ListGrants(t.Context(), f.userID)
	require.NoError(t, err)
	grant := grants[0]
	_, err = f.repo.UpdateGrant(t.Context(), f.userID, grant.ID, identity.MCPTokenInput{Name: grant.Name, Enabled: true, Permissions: []string{"fixture.get"}, ExpectedUpdatedAt: &grant.UpdatedAt})
	require.NoError(t, err)
	response = f.browser(t, http.MethodPost, "/api/v1/admin/mcp/confirmations/"+pending+"/approve", "browser")
	require.Equal(t, 403, response.StatusCode)
	require.NoError(t, response.Body.Close())
	arguments["confirmation_id"] = pending
	result, err = refreshed.CallTool(t.Context(), &mcp.CallToolParams{Name: "execute_operation", Arguments: arguments})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Equal(t, int64(1), f.writes.Load())
	// OAuth grants and manual credentials intentionally share numeric identifiers;
	// the existing manual credential remains valid after the application is revoked.
	require.NoError(t, f.repo.RevokeGrant(t.Context(), f.userID, grant.ID))
	_, _, err = f.handler.tokens.Authenticate(t.Context(), f.credential)
	require.NoError(t, err)
	_, err = f.oauth.Authenticate(t.Context(), oauthString(t, next, "access_token"))
	require.Error(t, err)
}

// TestOAuthClientResourceBindingAndRevocation checks that an intercepted code
// cannot move between clients or resource audiences. Correct revocation rejects
// later access, while another client's revocation request cannot affect the family.
func TestOAuthClientResourceBindingAndRevocation(t *testing.T) {
	f := newOAuthFixture(t)
	clientID := f.register(t)
	other := f.register(t)
	code, verifier := f.code(t, clientID)
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {other}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}}
	status, _ := f.exchange(t, values)
	require.Equal(t, 400, status)
	values.Set("client_id", clientID)
	values.Set("resource", "https://another.example/mcp")
	status, _ = f.exchange(t, values)
	require.Equal(t, 400, status)
	values.Set("resource", f.oauth.Resource())
	status, pair := f.exchange(t, values)
	require.Equal(t, 200, status, pair)
	access := oauthString(t, pair, "access_token")
	refresh := oauthString(t, pair, "refresh_token")
	response, err := http.PostForm(f.http.URL+"/mcp/oauth/revoke", url.Values{"client_id": {other}, "token": {refresh}, "token_type_hint": {"refresh_token"}})
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	_, err = f.oauth.Authenticate(t.Context(), access)
	require.NoError(t, err)
	response, err = http.PostForm(f.http.URL+"/mcp/oauth/revoke", url.Values{"client_id": {clientID}, "token": {refresh}, "token_type_hint": {"refresh_token"}})
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	require.NoError(t, response.Body.Close())
	_, err = f.oauth.Authenticate(t.Context(), access)
	require.Error(t, err)
}

// TestOAuthRevocationFailureRollsBack uses real credentials and PostgreSQL transactions
// with injected revocation failures. HTTP 503 must leave the family usable until
// a later successful retry commits revocation of both access and refresh credentials.
func TestOAuthRevocationFailureRollsBack(t *testing.T) {
	f := newOAuthFixture(t)
	for _, stage := range []string{"begin", "read", "write", "commit"} {
		t.Run(stage, func(t *testing.T) {
			clientID := f.register(t)
			code, verifier := f.code(t, clientID)
			status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}})
			require.Equal(t, http.StatusOK, status, pair)
			access := oauthString(t, pair, "access_token")
			refresh := oauthString(t, pair, "refresh_token")
			grant, err := f.oauth.Authenticate(t.Context(), access)
			require.NoError(t, err)
			failing := &failingRevocationRepository{MCPOAuthRepository: f.repo, stage: stage}
			root := f.handler.options.OAuth
			f.handler.options.OAuth = provider.NewMCPOAuthServer(failing, f.accounts, f.config, bytes.Repeat([]byte{7}, 32))
			t.Cleanup(func() { f.handler.options.OAuth = root })
			response, err := http.PostForm(f.http.URL+"/mcp/oauth/revoke", url.Values{"client_id": {clientID}, "token": {refresh}})
			require.NoError(t, err)
			var body map[string]any
			require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusServiceUnavailable, response.StatusCode, body)
			require.Equal(t, "temporarily_unavailable", body["error"])
			_, err = f.oauth.Authenticate(t.Context(), access)
			require.NoError(t, err)
			var activeRefresh int
			require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM mcp_oauth_sessions WHERE grant_id=$1 AND kind='refresh' AND active`, grant.ID).Scan(&activeRefresh))
			require.Equal(t, 1, activeRefresh)
			// The failed HTTP request has completed before restoring the unwrapped repository.
			// A retry must commit revocation rather than inheriting request-local failure state.
			f.handler.options.OAuth = root
			response, err = http.PostForm(f.http.URL+"/mcp/oauth/revoke", url.Values{"client_id": {clientID}, "token": {refresh}})
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			require.Equal(t, http.StatusOK, response.StatusCode)
			_, err = f.oauth.Authenticate(t.Context(), access)
			require.Error(t, err)
			status, _ = f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
			require.Equal(t, http.StatusBadRequest, status)
		})
	}
}

// TestOAuthRevocationDuringAdmissionDenial revokes credentials while application or
// account admission is unavailable. Restoring the grant or administrator role must
// never restore the revoked access and refresh credentials for either token hint.
func TestOAuthRevocationDuringAdmissionDenial(t *testing.T) {
	f := newOAuthFixture(t)
	for _, denial := range []string{"disabled", "expired", "demoted"} {
		for _, kind := range []string{"access_token", "refresh_token"} {
			t.Run(denial+"/"+kind, func(t *testing.T) {
				clientID := f.register(t)
				code, verifier := f.code(t, clientID)
				status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}})
				require.Equal(t, http.StatusOK, status, pair)
				access := oauthString(t, pair, "access_token")
				refresh := oauthString(t, pair, "refresh_token")
				grant, err := f.oauth.Authenticate(t.Context(), access)
				require.NoError(t, err)
				input := identity.MCPTokenInput{Name: grant.Name, Enabled: denial != "disabled", Permissions: grant.Permissions, ExpectedUpdatedAt: &grant.UpdatedAt}
				if denial == "expired" {
					expired := time.Now().Add(-time.Hour)
					input.ExpiresAt = &expired
				}
				var updated *identity.MCPOAuthGrant
				if denial == "demoted" {
					f.accounts.role.Store(identity.RoleUser)
					_, err = f.db.ExecContext(t.Context(), `UPDATE users SET role='user' WHERE id=$1`, f.userID)
					require.NoError(t, err)
					t.Cleanup(func() {
						f.accounts.role.Store(identity.RoleAdmin)
						_, restoreErr := f.db.Exec(`UPDATE users SET role='admin' WHERE id=$1`, f.userID)
						require.NoError(t, restoreErr)
					})
				} else {
					if denial == "expired" {
						// Backdate persisted expiry to exercise elapsed authorization without a timed wait.
						// The public update API correctly rejects dates that have already passed.
						updated, err = f.repo.UpdateGrant(t.Context(), f.userID, grant.ID, input)
					} else {
						updated, err = f.oauth.UpdateGrant(t.Context(), f.userID, grant.ID, input)
					}
					require.NoError(t, err)
				}
				_, err = f.oauth.Authenticate(t.Context(), access)
				require.Error(t, err)
				status, _ = f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
				require.Equal(t, http.StatusBadRequest, status)
				// Denied admission must still permit client-owned removal before the administrator
				// restores the application, otherwise both credential types become usable again.
				response, err := http.PostForm(f.http.URL+"/mcp/oauth/revoke", url.Values{"client_id": {clientID}, "token": {oauthString(t, pair, kind)}, "token_type_hint": {kind}})
				require.NoError(t, err)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusOK, response.StatusCode)
				if denial == "demoted" {
					f.accounts.role.Store(identity.RoleAdmin)
					_, err = f.db.ExecContext(t.Context(), `UPDATE users SET role='admin' WHERE id=$1`, f.userID)
					require.NoError(t, err)
				} else {
					input.Enabled = true
					input.ExpiresAt = nil
					input.ExpectedUpdatedAt = &updated.UpdatedAt
					_, err = f.oauth.UpdateGrant(t.Context(), f.userID, grant.ID, input)
					require.NoError(t, err)
				}
				_, err = f.oauth.Authenticate(t.Context(), access)
				require.Error(t, err)
				status, _ = f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {refresh}})
				require.Equal(t, http.StatusBadRequest, status)
			})
		}
	}
}

// TestOAuthConsentSessionIsolationAndExpiry binds a ticket to one browser session.
// A different session, wrong nonce, repeat decision, or expired request cannot
// create credentials; denial consumes the request without creating an application grant.
func TestOAuthConsentSessionIsolationAndExpiry(t *testing.T) {
	f := newOAuthFixture(t)
	clientID := f.register(t)
	client, err := f.repo.GetClient(t.Context(), clientID)
	require.NoError(t, err)
	verifier := strings.Repeat("z", 64)
	sum := sha256.Sum256([]byte(verifier))
	pending := &identity.MCPOAuthPending{ID: "consent-ticket", RedirectURI: "http://127.0.0.1:43111/callback", Client: *client, ExpiresAt: time.Now().Add(time.Minute), Form: url.Values{"client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "response_type": {"code"}, "scope": {"mcp"}, "state": {"state-with-enough-entropy"}, "resource": {f.oauth.Resource()}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}}
	require.NoError(t, f.repo.PutPending(t.Context(), pending))
	review, err := f.oauth.Review(t.Context(), pending.ID, f.userID, "browser-a")
	require.NoError(t, err)
	_, err = f.oauth.Review(t.Context(), pending.ID, f.userID, "browser-b")
	require.Error(t, err)
	_, err = f.oauth.Decide(t.Context(), pending.ID, f.userID, "browser-a", "wrong", true, []string{"fixture.get"})
	require.Error(t, err)
	callback, err := f.oauth.Decide(t.Context(), pending.ID, f.userID, "browser-a", review.Nonce, false, nil)
	require.NoError(t, err)
	parsed, err := url.Parse(callback)
	require.NoError(t, err)
	require.Equal(t, "access_denied", parsed.Query().Get("error"))
	require.Equal(t, f.http.URL, parsed.Query().Get("iss"))
	_, err = f.oauth.Decide(t.Context(), pending.ID, f.userID, "browser-a", review.Nonce, true, []string{"fixture.get"})
	require.Error(t, err)
	grants, err := f.repo.ListGrants(t.Context(), f.userID)
	require.NoError(t, err)
	require.Empty(t, grants)
	pending.ID = "expired-ticket"
	pending.ExpiresAt = time.Now().Add(-time.Minute)
	require.NoError(t, f.repo.PutPending(t.Context(), pending))
	_, err = f.oauth.Review(t.Context(), pending.ID, f.userID, "browser-a")
	require.Error(t, err)
	require.NoError(t, f.repo.Cleanup(t.Context()))
	var count int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM mcp_oauth_pending WHERE id='expired-ticket'`).Scan(&count))
	require.Zero(t, count)
}

// TestOAuthConfidentialClientAuthentication covers both advertised secret authentication
// methods. A missing secret cannot exchange the code, and successful retries
// use the registered method while persistence contains a verifier rather than plaintext.
func TestOAuthConfidentialClientAuthentication(t *testing.T) {
	f := newOAuthFixture(t)
	for _, method := range []string{"client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			metadata := map[string]any{"client_name": "Confidential client", "redirect_uris": []string{"http://127.0.0.1:43111/callback"}, "grant_types": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_method": method}
			raw, err := json.Marshal(metadata)
			require.NoError(t, err)
			response, err := http.Post(f.http.URL+"/mcp/oauth/register", "application/json", bytes.NewReader(raw))
			require.NoError(t, err)
			var registration map[string]any
			require.NoError(t, json.NewDecoder(response.Body).Decode(&registration))
			require.NoError(t, response.Body.Close())
			require.Equal(t, 201, response.StatusCode, registration)
			clientID := oauthString(t, registration, "client_id")
			secret := oauthString(t, registration, "client_secret")
			code, verifier := f.code(t, clientID)
			values := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}}
			status, _ := f.exchange(t, values)
			require.NotEqual(t, 200, status)
			if method == "client_secret_post" {
				values.Set("client_secret", secret)
			}
			request, err := http.NewRequest(http.MethodPost, f.http.URL+"/mcp/oauth/token", strings.NewReader(values.Encode()))
			require.NoError(t, err)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if method == "client_secret_basic" {
				request.SetBasicAuth(clientID, secret)
			}
			response, err = http.DefaultClient.Do(request)
			require.NoError(t, err)
			var pair map[string]any
			require.NoError(t, json.NewDecoder(response.Body).Decode(&pair))
			require.NoError(t, response.Body.Close())
			require.Equal(t, 200, response.StatusCode, pair)
			stored, err := f.repo.GetClient(t.Context(), clientID)
			require.NoError(t, err)
			require.NotEqual(t, secret, string(stored.SecretHash))
			_, err = f.oauth.Authenticate(t.Context(), oauthString(t, pair, "access_token"))
			require.NoError(t, err)
		})
	}
}

// oauthString requires a string-valued protocol field before returning its content.
func oauthString(t *testing.T, data map[string]any, key string) string {
	t.Helper()
	value, ok := data[key].(string)
	require.True(t, ok, "expected string field %s in %v", key, data)
	return value
}

// oauthResultMap requires the SDK result's decoded structured management response.
func oauthResultMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	value, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	return value
}

// TestOAuthCIMDClientUsesStableIdentity connects a URL-identified application with
// the official SDK. The verified document cache supplies ChatGPT-shaped identity
// and callbacks; no dynamic registration or external network fetch is required.
func TestOAuthCIMDClientUsesStableIdentity(t *testing.T) {
	f := newOAuthFixture(t)
	expires := time.Now().Add(5 * time.Minute)
	const clientID = "https://chatgpt.com/oauth/client.json"
	const callbackURI = "https://chatgpt.com/connector_platform_oauth_redirect"
	require.NoError(t, f.repo.PutClient(t.Context(), &identity.MCPOAuthClient{ID: clientID, Name: "ChatGPT", RedirectURIs: []string{callbackURI}, GrantTypes: []string{"authorization_code", "refresh_token"}, AuthMethod: "none", ExpiresAt: &expires}))
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{ClientIDMetadataDocumentConfig: &auth.ClientIDMetadataDocumentConfig{URL: clientID}, RedirectURL: callbackURI, RequestRefreshToken: true, AuthorizationCodeFetcher: func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		callback := f.consent(t, args.URL)
		require.Equal(t, "chatgpt.com", callback.Host)
		return &auth.AuthorizationResult{Code: callback.Query().Get("code"), State: callback.Query().Get("state"), Iss: callback.Query().Get("iss")}, nil
	}})
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "CIMD acceptance", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", OAuthHandler: handler, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "search_operations", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	grants, err := f.repo.ListGrants(t.Context(), f.userID)
	require.NoError(t, err)
	require.Len(t, grants, 1)
	require.Equal(t, clientID, grants[0].ClientID)
}

// TestOAuthUploadUsesCurrentAuthorization uploads JSON without exposing the opaque
// grant's internal identifier to its client. The file belongs to the OAuth
// source even when a manual token has the same ID, and revocation blocks download.
func TestOAuthUploadUsesCurrentAuthorization(t *testing.T) {
	f := newOAuthFixture(t)
	clientID := f.register(t)
	code, verifier := f.code(t, clientID)
	status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}})
	require.Equal(t, 200, status, pair)
	client := mcp.NewClient(&mcp.Implementation{Name: "OAuth files", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.http.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{oauthString(t, pair, "access_token")}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, session.Close()) }()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "execute_operation", Arguments: map[string]any{"operation_id": "mcp.files.upload", "body": map[string]any{"operation_id": "fixture.get", "name": "args.json", "data": base64.StdEncoding.EncodeToString([]byte(`{"id":"from-file"}`))}}})
	require.NoError(t, err)
	require.False(t, result.IsError, result)
	envelope := jsonTestValue[map[string]any](t, oauthResultMap(t, result)["data"])
	data := jsonTestValue[map[string]any](t, envelope["data"])
	fileID := oauthString(t, data, "file_id")
	file, err := f.handler.state.GetArtifact(t.Context(), fileID)
	require.NoError(t, err)
	require.Equal(t, "oauth", file.Source)
	require.Equal(t, f.token.ID, file.TokenID)
	response := f.browser(t, http.MethodGet, "/api/v1/admin/mcp/files/"+fileID, "browser")
	require.Equal(t, 200, response.StatusCode)
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.JSONEq(t, `{"id":"from-file"}`, string(raw))
	require.NoError(t, f.repo.RevokeGrant(t.Context(), f.userID, file.TokenID))
	response = f.browser(t, http.MethodGet, "/api/v1/admin/mcp/files/"+fileID, "browser")
	require.Equal(t, 403, response.StatusCode)
	require.NoError(t, response.Body.Close())
}

// FrontendURL copies the site address while excluding concurrent fixture updates.
func (s *oauthFrontendFixture) FrontendURL(context.Context) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.url, s.err
}

// setFrontend replaces the fixture address before subsequent OAuth requests arrive.
func (s *oauthFrontendFixture) setFrontend(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.url = value
}

// TestOAuthFrontendRuntimeChanges uses the production protocol before and after site edits.
// Old access tokens, refresh credentials, codes, and browser requests must fail
// under the new origin, while discovery publishes only the configured address.
func TestOAuthFrontendRuntimeChanges(t *testing.T) {
	f := newOAuthFixture(t)
	clientID := f.register(t)
	code, verifier := f.code(t, clientID)
	status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}})
	require.Equal(t, http.StatusOK, status)
	access := oauthString(t, pair, "access_token")
	root := f.handler.options.OAuth
	grant, err := root.Authenticate(t.Context(), access)
	require.NoError(t, err)
	otherClient := f.register(t)
	oldCode, oldVerifier := f.code(t, otherClient)
	metadata, err := f.repo.GetClient(t.Context(), clientID)
	require.NoError(t, err)
	pending := &identity.MCPOAuthPending{ID: "site-change-review", Client: *metadata, Form: url.Values{"resource": {f.oauth.Resource()}}, ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, f.repo.PutPending(t.Context(), pending))
	review, err := f.oauth.Review(t.Context(), pending.ID, f.userID, "fixture-session")
	require.NoError(t, err)
	f.config.setFrontend("https://new-site.example")
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.http.URL+"/.well-known/oauth-authorization-server", nil)
	require.NoError(t, err)
	request.Host = "attacker.example"
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	var discovery map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&discovery))
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "https://new-site.example", discovery["issuer"])
	_, err = root.Authenticate(t.Context(), access)
	require.Error(t, err)
	_, err = root.AuthorizeGrant(t.Context(), grant.ID)
	require.Error(t, err)
	status, _ = f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {oauthString(t, pair, "refresh_token")}})
	require.Equal(t, http.StatusBadRequest, status)
	status, _ = f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {otherClient}, "code": {oldCode}, "code_verifier": {oldVerifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {"https://new-site.example/mcp"}})
	require.Equal(t, http.StatusBadRequest, status)
	current, err := root.Current(t.Context())
	require.NoError(t, err)
	_, err = current.Review(t.Context(), pending.ID, f.userID, "fixture-session")
	require.Error(t, err)
	_, err = current.Decide(t.Context(), pending.ID, f.userID, "fixture-session", review.Nonce, true, []string{"fixture.get"})
	require.Error(t, err)
	// A fresh request at the new site must show the existing application grants
	// for review, while the old credentials remain bound to their earlier resource.
	fresh := &identity.MCPOAuthPending{ID: "new-site-review", Client: *metadata, Form: url.Values{"resource": {current.Resource()}}, ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, f.repo.PutPending(t.Context(), fresh))
	renewedReview, err := current.Review(t.Context(), fresh.ID, f.userID, "fixture-session")
	require.NoError(t, err)
	require.NotNil(t, renewedReview.Grant)
	require.Equal(t, grant.ID, renewedReview.Grant.ID)
	require.Equal(t, grant.Permissions, renewedReview.Grant.Permissions)
}

// TestOAuthAvailabilityChangesWithoutRestart edits the site address after mounting routes.
// Protocol discovery becomes available as soon as a valid address exists;
// missing addresses and read failures reject subsequent credential and grant checks.
func TestOAuthAvailabilityChangesWithoutRestart(t *testing.T) {
	f := newOAuthFixture(t)
	f.config.setFrontend("")
	response, err := http.Get(f.http.URL + "/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	response = f.browser(t, http.MethodGet, "/api/v1/admin/mcp/oauth/status", "browser")
	var state struct {
		Data provider.MCPOAuthStatus `json:"data"` /* Browser status response. */
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&state))
	require.NoError(t, response.Body.Close())
	require.False(t, state.Data.Enabled)
	require.Equal(t, "frontend_url_missing", state.Data.Reason)
	f.config.setFrontend(f.http.URL)
	clientID := f.register(t)
	code, verifier := f.code(t, clientID)
	status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}})
	require.Equal(t, http.StatusOK, status)
	access := oauthString(t, pair, "access_token")
	root := f.handler.options.OAuth
	grant, err := root.Authenticate(t.Context(), access)
	require.NoError(t, err)
	response = f.browser(t, http.MethodPut, "/api/v1/admin/mcp/oauth/status", "browser")
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	f.config.setFrontend("")
	_, err = root.Authenticate(t.Context(), access)
	require.Error(t, err)
	_, err = root.AuthorizeGrant(t.Context(), grant.ID)
	require.Error(t, err)
	f.config.setFrontend(f.http.URL)
	f.config.mu.Lock()
	f.config.err = errors.New("settings unavailable")
	f.config.mu.Unlock()
	response, err = http.Get(f.http.URL + "/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	_, err = root.Authenticate(t.Context(), access)
	require.Error(t, err)
}

// TestOAuthCleanupPreservesConcurrentRefreshReplay overlaps expiration cleanup with an admitted refresh.
// Cleanup must retain replay history so resubmitting the consumed refresh token
// invalidates both newly issued credentials after the waiting cleanup finishes.
func TestOAuthCleanupPreservesConcurrentRefreshReplay(t *testing.T) {
	f := newOAuthFixture(t)
	client := f.register(t)
	code, verifier := f.code(t, client)
	status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {f.oauth.Resource()}})
	require.Equal(t, 200, status, pair)
	oldRefresh := oauthString(t, pair, "refresh_token")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	// Shorten SQL and Fosite deadlines together so an admitted refresh crosses expiration
	// while the production transaction still holds its grant and family locks.
	deadline := time.Now().Add(2 * time.Second)
	_, err := f.db.Exec(`UPDATE mcp_oauth_sessions SET expires_at=$1, payload=jsonb_set(payload,'{session,expires_at,refresh_token}',to_jsonb($1::timestamptz))`, deadline)
	require.NoError(t, err)
	paused := &pausedOAuthRepository{MCPOAuthRepository: f.repo, entered: make(chan struct{}), resume: make(chan struct{})}
	f.handler.options.OAuth = provider.NewMCPOAuthServer(paused, f.accounts, f.config, bytes.Repeat([]byte{7}, 32))

	var release sync.Once
	defer release.Do(func() { close(paused.resume) })
	done := make(chan oauthRefreshResult, 1)
	go func() {
		status, pair := f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {oldRefresh}})
		done <- oauthRefreshResult{status: status, pair: pair}
	}()
	select {
	case <-paused.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("refresh did not reach insertion")
	}
	// Time advances expiration only; channels and PostgreSQL wait state establish the
	// refresh and cleanup ordering before either transaction is permitted to finish.
	<-time.After(time.Until(deadline) + 20*time.Millisecond)
	cleaned := make(chan error, 1)
	go func() { cleaned <- f.repo.Cleanup(ctx) }()
	require.Eventually(t, func() bool {
		var n int
		err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND wait_event='advisory'`).Scan(&n)
		return err == nil && n > 0
	}, 5*time.Second, 10*time.Millisecond)
	release.Do(func() { close(paused.resume) })
	var next oauthRefreshResult
	select {
	case next = <-done:
	case <-ctx.Done():
		t.Fatal("refresh did not complete: ", ctx.Err())
	}
	require.Equal(t, 200, next.status, next.pair)
	select {
	case err := <-cleaned:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("cleanup did not complete: ", ctx.Err())
	}
	var oldRows int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM mcp_oauth_sessions WHERE kind='refresh' AND NOT active`).Scan(&oldRows))
	t.Logf("consumed refresh replay records after concurrent cleanup: %d", oldRows)
	require.Equal(t, 1, oldRows)
	access := oauthString(t, next.pair, "access_token")
	_, err = f.oauth.Authenticate(t.Context(), access)
	require.NoError(t, err)
	status, _ = f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {oldRefresh}})
	require.Equal(t, 400, status)
	_, err = f.oauth.Authenticate(t.Context(), access)
	require.Error(t, err)
	status, _ = f.exchange(t, url.Values{"grant_type": {"refresh_token"}, "client_id": {client}, "refresh_token": {oauthString(t, next.pair, "refresh_token")}})
	require.Equal(t, 400, status)
}

// PutSession pauses replacement access insertion until the test releases the exchange.
// The old refresh token is already consumed inside the transaction, while neither
// replacement credential is published and the production family locks remain held.
func (r *pausedOAuthRepository) PutSession(ctx context.Context, s *identity.MCPOAuthSession) error {
	if s.Kind == "access" {
		close(r.entered)
		select {
		case <-r.resume:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.MCPOAuthRepository.PutSession(ctx, s)
}

// Transaction injects begin failures or rolls back successful callbacks before commit.
// The commit-stage injection runs inside the production transaction callback so
// real writes are rolled back while the caller receives a persistence failure.
func (r *failingRevocationRepository) Transaction(ctx context.Context, fn func(context.Context) error) error {
	if r.stage == "begin" {
		return errors.New("revocation begin unavailable")
	}
	return r.MCPOAuthRepository.Transaction(ctx, func(ctx context.Context) error {
		if err := fn(ctx); err != nil {
			return err
		}
		if r.stage == "commit" {
			return errors.New("revocation commit unavailable")
		}
		return nil
	})
}

// GetSession injects discovery failures before returning the persisted credential ownership.
func (r *failingRevocationRepository) GetSession(ctx context.Context, kind, signature string) (*identity.MCPOAuthSession, error) {
	if r.stage == "read" {
		return nil, errors.New("revocation read unavailable")
	}
	return r.MCPOAuthRepository.GetSession(ctx, kind, signature)
}

// RevokeFamily fails after real invalidation to exercise the production rollback path.
// Returning the injected error after the write proves the HTTP error response
// preserves credentials through transaction rollback rather than merely skipping mutation.
func (r *failingRevocationRepository) RevokeFamily(ctx context.Context, id string) error {
	if err := r.MCPOAuthRepository.RevokeFamily(ctx, id); err != nil {
		return err
	}
	if r.stage == "write" {
		return errors.New("revocation write unavailable")
	}
	return nil
}

// TestOAuthOmittedRedirectURI retains the selected sole callback through browser
// review, approval, denial, and code redemption. Replacing CIMD metadata after
// review must reject both decisions locally instead of selecting the new callback.
func TestOAuthOmittedRedirectURI(t *testing.T) {
	f := newOAuthFixture(t)
	const callbackURI = "https://client.example/callback?existing=value"
	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	for _, scenario := range []struct {
		name    string // distinct metadata document path
		approve bool   // requested browser consent decision
		replace bool   // remove the initially selected callback
	}{
		{name: "approve", approve: true},
		{name: "deny"},
		{name: "changed_approve", approve: true, replace: true},
		{name: "changed_deny", replace: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			expires := time.Now().Add(5 * time.Minute)
			clientID := "https://client.example/" + scenario.name + ".json"
			client := &identity.MCPOAuthClient{ID: clientID, Name: "Default callback", RedirectURIs: []string{callbackURI}, GrantTypes: []string{"authorization_code", "refresh_token"}, AuthMethod: "none", ExpiresAt: &expires}
			require.NoError(t, f.repo.PutClient(t.Context(), client))
			form := url.Values{"client_id": {clientID}, "response_type": {"code"}, "scope": {"mcp offline_access"}, "state": {"default-callback-state"}, "resource": {f.oauth.Resource()}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}}
			response := httptest.NewRecorder()
			f.oauth.Start(response, httptest.NewRequest(http.MethodGet, f.http.URL+"/mcp/oauth/authorize?"+form.Encode(), nil))
			require.Equal(t, http.StatusFound, response.Code, response.Body.String())
			redirect, err := url.Parse(response.Header().Get("Location"))
			require.NoError(t, err)
			requestID := redirect.Query().Get("request")
			reviewResponse := f.browser(t, http.MethodGet, "/api/v1/admin/mcp/oauth/requests/"+requestID, "browser")
			var review struct {
				Data struct {
					Nonce       string `json:"nonce"`        // browser-bound decision proof
					RedirectURI string `json:"redirect_uri"` // displayed effective callback address
				} `json:"data"` // authenticated review response fields
			}
			require.NoError(t, json.NewDecoder(reviewResponse.Body).Decode(&review))
			require.NoError(t, reviewResponse.Body.Close())
			require.Equal(t, http.StatusOK, reviewResponse.StatusCode)
			require.Equal(t, callbackURI, review.Data.RedirectURI)
			pending, err := f.repo.BindPending(t.Context(), requestID, f.userID, "fixture-session")
			require.NoError(t, err)
			require.Equal(t, callbackURI, pending.RedirectURI)
			require.NotContains(t, pending.Form, "redirect_uri")
			if scenario.replace {
				client.RedirectURIs = []string{"https://replacement.example/callback"}
				require.NoError(t, f.repo.PutClient(t.Context(), client))
			}
			body, err := json.Marshal(map[string]any{"nonce": review.Data.Nonce, "approve": scenario.approve, "permissions": []string{"fixture.get"}})
			require.NoError(t, err)
			decision, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.http.URL+"/api/v1/admin/mcp/oauth/requests/"+requestID+"/decision", bytes.NewReader(body))
			require.NoError(t, err)
			decision.Header.Set("Authorization", "Bearer browser")
			decision.Header.Set("Content-Type", "application/json")
			result, err := http.DefaultClient.Do(decision)
			require.NoError(t, err)
			var outcome struct {
				Data struct {
					RedirectURI string `json:"redirect_uri"` // committed client callback address
				} `json:"data"` // browser decision response fields
			}
			require.NoError(t, json.NewDecoder(result.Body).Decode(&outcome))
			require.NoError(t, result.Body.Close())
			if scenario.replace {
				require.Equal(t, http.StatusBadRequest, result.StatusCode)
				require.Empty(t, outcome.Data.RedirectURI)
				_, err = f.repo.BindPending(t.Context(), requestID, f.userID, "fixture-session")
				require.NoError(t, err)
				return
			}
			require.Equal(t, http.StatusOK, result.StatusCode)
			callback, err := url.Parse(outcome.Data.RedirectURI)
			require.NoError(t, err)
			require.Equal(t, "client.example", callback.Host)
			require.Equal(t, "value", callback.Query().Get("existing"))
			require.Equal(t, "default-callback-state", callback.Query().Get("state"))
			require.Equal(t, f.http.URL, callback.Query().Get("iss"))
			if !scenario.approve {
				require.Equal(t, "access_denied", callback.Query().Get("error"))
				require.Empty(t, callback.Query().Get("code"))
				return
			}
			status, pair := f.exchange(t, url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {callback.Query().Get("code")}, "code_verifier": {verifier}, "resource": {f.oauth.Resource()}})
			require.Equal(t, http.StatusOK, status, pair)
			require.NotEmpty(t, oauthString(t, pair, "access_token"))
		})
	}
}

// TestOAuthExchangesWithOneDatabaseConnection uses the production UserStore and
// UserService alongside the OAuth repository. Code and refresh exchanges must
// reuse their transaction connection for administrator admission and finish before cancellation.
func TestOAuthExchangesWithOneDatabaseConnection(t *testing.T) {
	f := newOAuthFixture(t)
	f.db.SetMaxOpenConns(1)
	f.db.SetMaxIdleConns(1)
	entClient := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
	users := identity.NewUserService(postgres.NewUserStore(entClient, f.db), nil, nil, nil, nil)
	root := provider.NewMCPOAuthServer(f.repo, users, f.config, bytes.Repeat([]byte{7}, 32))
	current, err := root.Current(t.Context())
	require.NoError(t, err)
	_, err = users.GetByID(t.Context(), f.userID)
	require.NoError(t, err)
	clientID := f.register(t)
	code, verifier := f.code(t, clientID)
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {"http://127.0.0.1:43111/callback"}, "resource": {current.Resource()}}
	for _, grantType := range []string{"authorization_code", "refresh_token"} {
		t.Run(grantType, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			waits := f.db.Stats().WaitCount
			request := httptest.NewRequest(http.MethodPost, "/mcp/oauth/token", strings.NewReader(values.Encode())).WithContext(ctx)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			current.Token(response, request)
			require.NoError(t, ctx.Err())
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, waits, f.db.Stats().WaitCount)
			var pair map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &pair))
			require.NotEmpty(t, oauthString(t, pair, "access_token"))
			values = url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {oauthString(t, pair, "refresh_token")}}
		})
	}
}
