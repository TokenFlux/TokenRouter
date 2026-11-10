package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	control "github.com/TokenFlux/TokenRouter/internal/mcp"
	mcpredis "github.com/TokenFlux/TokenRouter/internal/mcp/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// transport_test.go exercises the real MCP SDK over HTTP with existing Gin
// administrator admission, Redis confirmation transitions, and AES-encrypted
// artifacts. A memory token repository supplies live grant changes while a
// deterministic browser-auth fixture represents already verified JWT sessions.
// These tests cover MCP authorization and dispatch; JWT cryptographic validation
// remains covered by the identity HTTP tests.

// tokenFixture holds one synchronized credential and its digest for transport tests.
type tokenFixture struct {
	// Unused persistence methods deliberately remain unavailable; transport cases
	// exercise issuance and authorization without pretending to cover PostgreSQL mutations.
	identity.MCPTokenRepository
	mu     sync.Mutex        // token snapshot mutation lock
	token  identity.MCPToken // current credential metadata
	digest string            // stored credential digest
	sealed string            // encrypted fixture credential
}

// accountFixture represents the current active administrator consulted by token admission.
type accountFixture struct {
	role atomic.Value // current global role string
}

// grantFixture represents the current browser TOTP authorization state.
type grantFixture struct {
	enabled bool        // configured step-up policy
	granted atomic.Bool // current browser grant availability
}

// bearerTransport adds one dedicated credential to the SDK's HTTP requests.
type bearerTransport struct {
	credential string // fixture-issued MCP credential
}

// mcpFixture retains the real HTTP server and its observable test operation state.
type mcpFixture struct {
	handler    *Handler           // configured control plane
	repo       *tokenFixture      // mutable persisted grants
	accounts   *accountFixture    // current user role reader
	grants     *grantFixture      // mutable TOTP grant reader
	server     *httptest.Server   // local protocol listener
	session    *mcp.ClientSession // official SDK client session
	token      *identity.MCPToken // initially issued public metadata
	credential string             // fixture credential for negative admission tests
	writes     atomic.Int64       // executed business mutations
}

// conditionalUsers implements the account writes reached by actual user HTTP handlers.
type conditionalUsers struct {
	identityhttp.UserAdministration               // unused account administration operations
	writes                          *atomic.Int64 // borrowed successful mutation counter
}

// Create stores the issued digest and fills the fixture's generated identity.
func (f *tokenFixture) Create(_ context.Context, token *identity.MCPToken, digest, sealed string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	token.ID = 1
	token.CreatedAt = time.Now().UTC()
	token.UpdatedAt = token.CreatedAt
	f.token = *token
	f.digest = digest
	f.sealed = sealed
	return nil
}

// Credential returns ciphertext only for the fixture's owning administrator.
// Revoked records use the same not-found outcome as mismatched identities,
// allowing HTTP tests to exercise the separate authenticated disclosure path.
func (f *tokenFixture) Credential(_ context.Context, userID, id int64) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.token.ID || userID != f.token.UserID || f.token.RevokedAt != nil {
		return "", "", identity.ErrMCPTokenNotFound
	}
	return f.sealed, f.digest, nil
}

// Get returns an independent permission snapshot for the fixture's stored credential.
func (f *tokenFixture) Get(_ context.Context, id int64) (*identity.MCPToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id != f.token.ID {
		return nil, identity.ErrMCPTokenNotFound
	}
	token := f.token
	token.Permissions = slices.Clone(token.Permissions)
	return &token, nil
}

// FindHash matches the presented digest before returning fresh authorization metadata.
func (f *tokenFixture) FindHash(ctx context.Context, digest string) (*identity.MCPToken, error) {
	f.mu.Lock()
	matched := digest == f.digest
	f.mu.Unlock()
	if !matched {
		return nil, identity.ErrMCPTokenInvalid
	}
	return f.Get(ctx, 1)
}

// Touch accepts usage observation independently of the fixture's token settings version.
func (*tokenFixture) Touch(context.Context, int64) error { return nil }

// GetByID returns current role state for the fixture's owning administrator.
func (f *accountFixture) GetByID(_ context.Context, id int64) (*identity.User, error) {
	if id != 1 && id != 2 {
		return nil, errors.New("unknown fixture account")
	}
	role, _ := f.role.Load().(string)
	return &identity.User{ID: id, Role: role, Status: identity.StatusActive, TotpEnabled: true}, nil
}

// IsStepUpEnabled supplies the configured strong-authentication switch for these test requests.
func (f *grantFixture) IsStepUpEnabled(context.Context) bool { return f.enabled }

// HasStepUpGrant checks the verified browser session and its current grant availability.
func (f *grantFixture) HasStepUpGrant(_ context.Context, userID int64, sessionID string) (bool, error) {
	return userID == 1 && sessionID == "fixture-session" && f.granted.Load(), nil
}

// RoundTrip clones requests before installing the fixture's Bearer authentication header.
func (b bearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header.Set("Authorization", "Bearer "+b.credential)
	return http.DefaultTransport.RoundTrip(cloned)
}

// newMCPFixture mounts real SDK transport and route admission around bounded test handlers.
// Browser authentication is a fixture, while token checks, schema validation,
// approval storage, and SDK serialization use the production implementations.
func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	fixture := &mcpFixture{repo: &tokenFixture{}, accounts: &accountFixture{}, grants: &grantFixture{enabled: true}}
	fixture.accounts.role.Store(identity.RoleAdmin)
	fixture.grants.granted.Store(true)
	cache := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: cache.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	cipher, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	tokens := identity.NewMCPTokenService(fixture.repo, fixture.accounts, cipher)
	fixture.handler = NewHandler(tokens, mcpredis.NewState(redisClient, cipher), fixture.grants, fixture.accounts, fixture.grants, Options{Origins: func(context.Context) []string { return []string{"https://management.example"} }})
	router := gin.New()
	normal := func(c *gin.Context) {
		id := int64(1)
		if c.GetHeader("Authorization") == "Bearer other-browser" {
			id = 2
		} else if c.GetHeader("Authorization") != "Bearer browser" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		authctx.SetPrincipal(c, identity.Principal{UserID: id, Role: identity.RoleAdmin, CredentialKind: "jwt", SessionID: "fixture-session"}, 1, "fixture@example.invalid")
		c.Next()
	}
	admin := identityhttp.MCPAdminAuth(tokens, normal)
	group := router.Group("/api/v1/admin", admin)
	group.GET("/fixture/:id", func(c *gin.Context) {
		c.JSON(200, gin.H{"code": 0, "data": gin.H{"id": c.Param("id"), "integration_token": "fixture-routing-label"}})
	})
	group.POST("/fixture", func(c *gin.Context) {
		fixture.writes.Add(1)
		c.JSON(200, gin.H{"code": 0, "data": gin.H{"created": true}})
	})
	group.POST("/protected", identityhttp.StepUpAuth(fixture.grants, fixture.accounts, fixture.grants), func(c *gin.Context) {
		fixture.writes.Add(1)
		c.JSON(200, gin.H{"code": 0, "data": gin.H{"saved": true}})
	})
	fixture.handler.registerManagement(router.Group("/api/v1/admin/mcp", admin))
	operations := []*adminapi.Operation{
		{ID: "fixture.get", Method: "GET", Path: "/api/v1/admin/fixture/:id", ReadOnly: true, Input: adminapi.Arguments("/api/v1/admin/fixture/:id", nil, nil)},
		{ID: "fixture.create", Method: "POST", Path: "/api/v1/admin/fixture", Input: adminapi.Arguments("/api/v1/admin/fixture", nil, map[string]any{})},
		{ID: "fixture.protected", Method: "POST", Path: "/api/v1/admin/protected", Sensitive: true, StepUp: true, Input: adminapi.Arguments("/api/v1/admin/protected", nil, map[string]any{})},
	}
	for _, operation := range managementOperations {
		operations = append(operations, operation)
	}
	fixture.handler.catalog, err = adminapi.NewCatalog(operations)
	require.NoError(t, err)
	fixture.handler.router = router
	router.Any("/mcp", gin.WrapH(http.HandlerFunc(fixture.handler.serveMCP)))
	fixture.token, fixture.credential, err = tokens.Create(t.Context(), 1, identity.MCPTokenInput{Name: "SDK fixture", Enabled: true, Permissions: []string{"fixture.get", "fixture.create", "fixture.protected", "mcp.confirmations.approve"}})
	require.NoError(t, err)
	fixture.server = httptest.NewServer(router)
	t.Cleanup(fixture.server.Close)
	client := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	fixture.session, err = client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: fixture.server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{fixture.credential}}, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fixture.session.Close()) })
	return fixture
}

// invoke calls a tool through the official client and decodes its structured result.
func (f *mcpFixture) invoke(t *testing.T, tool string, arguments map[string]any) (*mcp.CallToolResult, map[string]any) {
	t.Helper()
	result, err := f.session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: arguments})
	require.NoError(t, err)
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return result, decoded
}

// browser sends a workflow request with the fixture's already-verified browser identity.
func (f *mcpFixture) browser(t *testing.T, method, path, credential string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+credential)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
	return response
}

// TestSDKDiscoveryDispatchAndRevocation connects the official MCP client to the mounted
// management transport. It verifies schema rejection, exact permissions,
// default and opt-in result masking, query isolation, and live revocation.
func TestSDKDiscoveryDispatchAndRevocation(t *testing.T) {
	f := newMCPFixture(t)
	tools, err := f.session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 4)
	result, data := f.invoke(t, "search_operations", map[string]any{})
	require.False(t, result.IsError)
	require.Equal(t, float64(4), data["total"])
	arguments := map[string]any{"operation_id": "fixture.get", "path": map[string]string{"id": "7"}}
	result, _ = f.invoke(t, "read_operation", arguments)
	require.False(t, result.IsError)
	require.Contains(t, jsonTestValue[*mcp.TextContent](t, result.Content[0]).Text, `"integration_token":"fixture-routing-label"`)
	// Change the stored preference while keeping the session open to exercise
	// the live credential read used for the next tool invocation.
	f.repo.mu.Lock()
	f.repo.token.RedactToolResults = true
	f.repo.mu.Unlock()
	result, data = f.invoke(t, "read_operation", arguments)
	require.False(t, result.IsError)
	require.Equal(t, "***", jsonTestValue[map[string]any](t, jsonTestValue[map[string]any](t, data["data"])["data"])["integration_token"])
	for _, arguments := range []map[string]any{
		{"operation_id": "fixture.create"},
		{"operation_id": "fixture.get", "path": map[string]string{"id": "../protected"}},
		{"operation_id": "fixture.get", "path": map[string]string{"id": "%2fprotected"}},
		{"operation_id": "fixture.get", "path": map[string]string{"id": "7"}, "query": map[string]string{"unknown": "x"}},
		{"operation_id": "mcp.tokens.create"},
	} {
		result, _ = f.invoke(t, "read_operation", arguments)
		require.True(t, result.IsError)
	}
	require.Zero(t, f.writes.Load())
	result, _ = f.invoke(t, "execute_operation", map[string]any{"operation_id": "fixture.create", "body": map[string]any{"name": "example"}})
	require.False(t, result.IsError)
	require.Equal(t, int64(1), f.writes.Load())
	f.repo.mu.Lock()
	f.repo.token.Permissions = []string{}
	f.repo.mu.Unlock()
	result, _ = f.invoke(t, "describe_operation", map[string]any{"operation_id": "fixture.get"})
	require.True(t, result.IsError)
	f.accounts.role.Store(identity.RoleUser)
	response := f.browser(t, "POST", "/mcp", f.credential)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
}

// TestBrowserApprovalBindsOwnerPayloadAndSingleExecution requests a sensitive operation
// through the SDK. Different owners, changed arguments, expired TOTP grants, and
// reused approvals must fail without adding another business mutation.
func TestBrowserApprovalBindsOwnerPayloadAndSingleExecution(t *testing.T) {
	f := newMCPFixture(t)
	arguments := map[string]any{"operation_id": "fixture.protected", "body": map[string]any{"password": "hidden", "name": "reviewed"}}
	result, pending := f.invoke(t, "execute_operation", arguments)
	require.False(t, result.IsError)
	require.Equal(t, "confirmation_required", pending["status"])
	id := jsonTestValue[string](t, pending["confirmation_id"])
	approval, err := f.handler.state.GetApproval(t.Context(), id)
	require.NoError(t, err)
	require.NotContains(t, approval.Preview, "hidden")
	response := f.browser(t, "POST", "/api/v1/admin/mcp/confirmations/"+id+"/approve", "other-browser")
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	response = f.browser(t, "POST", "/api/v1/admin/mcp/confirmations/"+id+"/approve", "browser")
	require.Equal(t, http.StatusOK, response.StatusCode)
	arguments["confirmation_id"] = id
	arguments["body"] = map[string]any{"password": "hidden", "name": "changed"}
	result, _ = f.invoke(t, "execute_operation", arguments)
	require.True(t, result.IsError)
	arguments["body"] = map[string]any{"password": "hidden", "name": "reviewed"}
	f.grants.granted.Store(false)
	result, _ = f.invoke(t, "execute_operation", arguments)
	require.True(t, result.IsError)
	require.Zero(t, f.writes.Load())
	f.grants.granted.Store(true)
	result, _ = f.invoke(t, "execute_operation", arguments)
	require.False(t, result.IsError)
	require.Equal(t, int64(1), f.writes.Load())
	result, _ = f.invoke(t, "execute_operation", arguments)
	require.True(t, result.IsError)
	require.Equal(t, int64(1), f.writes.Load())
}

// TestMCPPublicHostOverLoopback reproduces a reverse proxy retaining the public Host
// while connecting through loopback. Initialization accepts authenticated native and
// trusted browser clients, while credential and Origin checks still reject invalid requests.
func TestMCPPublicHostOverLoopback(t *testing.T) {
	f := newMCPFixture(t)
	address, ok := f.server.Listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.True(t, address.IP.IsLoopback())

	tests := []struct {
		name       string // descriptive admission scenario
		host       string // forwarded public authority
		origin     string // optional browser origin
		credential string // presented Bearer credential
		wantStatus int    // expected HTTP status
		wantError  string // expected admission failure
	}{
		{
			name:       "native client",
			host:       "api.example.com",
			credential: f.credential,
			wantStatus: http.StatusOK,
		},
		{
			name:       "public authority with port",
			host:       "api.example.com:8443",
			credential: f.credential,
			wantStatus: http.StatusOK,
		},
		{
			name:       "trusted browser origin",
			host:       "api.example.com",
			origin:     "https://management.example",
			credential: f.credential,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing credential",
			host:       "api.example.com",
			wantStatus: http.StatusUnauthorized,
			wantError:  "MCP access token required",
		},
		{
			name: "invalid credential",
			host: "api.example.com",
			// Match the credential format so authentication rejects an unknown digest.
			credential: "tr_mcp_" + strings.Repeat("x", 43),
			wantStatus: http.StatusUnauthorized,
			wantError:  "MCP access token is invalid or expired",
		},
		{
			name:       "Host does not authorize Origin",
			host:       "api.example.com",
			origin:     "https://api.example.com",
			credential: f.credential,
			wantStatus: http.StatusForbidden,
			wantError:  "Origin is not permitted",
		},
		{
			name:       "lookalike browser origin",
			host:       "api.example.com",
			origin:     "https://management.example.attacker.invalid",
			credential: f.credential,
			wantStatus: http.StatusForbidden,
			wantError:  "Origin is not permitted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"reverse-proxy-regression","version":"1"}}}`
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+"/mcp", strings.NewReader(body))
			require.NoError(t, err)
			// Keep the TCP destination on loopback while forwarding the public Host,
			// matching a same-host reverse proxy without replacing the server context.
			request.Host = tt.host
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept", "application/json, text/event-stream")
			if tt.origin != "" {
				request.Header.Set("Origin", tt.origin)
			}
			if tt.credential != "" {
				request.Header.Set("Authorization", "Bearer "+tt.credential)
			}
			response, err := f.server.Client().Do(request)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
			data, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, tt.wantStatus, response.StatusCode, string(data))
			if tt.wantError != "" {
				require.Equal(t, tt.wantError+"\n", string(data))
				return
			}
			var envelope map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &envelope))
			require.JSONEq(t, `"2.0"`, string(envelope["jsonrpc"]))
			require.JSONEq(t, `1`, string(envelope["id"]))
			require.NotContains(t, envelope, "error")
			var initialized mcp.InitializeResult
			require.NoError(t, json.Unmarshal(envelope["result"], &initialized))
			require.Equal(t, "2025-11-25", initialized.ProtocolVersion)
			require.NotNil(t, initialized.ServerInfo)
			require.Equal(t, "TokenRouter", initialized.ServerInfo.Name)
		})
	}
}

// TestMCPOriginAndCredentialIsolation supplies an attacker-controlled Host and Origin
// plus a valid token. Origin admission rejects it, and the same credential never
// becomes a public REST administrator credential.
func TestMCPOriginAndCredentialIsolation(t *testing.T) {
	f := newMCPFixture(t)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+"/mcp", nil)
	require.NoError(t, err)
	request.Host = "attacker.example"
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Authorization", "Bearer "+f.credential)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	response = f.browser(t, "POST", "/api/v1/admin/fixture", f.credential)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.Zero(t, f.writes.Load())
}

// TestConfirmationConcurrentConsumptionAndGrantChange races shared approval claims
// and then changes token permissions after a separate approval. Exactly one claim
// succeeds, and the stale grant never reaches the protected business handler.
func TestConfirmationConcurrentConsumptionAndGrantChange(t *testing.T) {
	f := newMCPFixture(t)
	args := map[string]any{"operation_id": "fixture.protected", "body": map[string]any{"name": "reviewed"}}
	_, pending := f.invoke(t, "execute_operation", args)
	id := jsonTestValue[string](t, pending["confirmation_id"])
	require.Equal(t, 200, f.browser(t, "POST", "/api/v1/admin/mcp/confirmations/"+id+"/approve", "browser").StatusCode)
	approval, err := f.handler.state.GetApproval(t.Context(), id)
	require.NoError(t, err)
	var accepted atomic.Int64
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if f.handler.state.Consume(t.Context(), approval) == nil {
				accepted.Add(1)
			}
		})
	}
	workers.Wait()
	require.Equal(t, int64(1), accepted.Load())
	_, pending = f.invoke(t, "execute_operation", args)
	id = jsonTestValue[string](t, pending["confirmation_id"])
	require.Equal(t, 200, f.browser(t, "POST", "/api/v1/admin/mcp/confirmations/"+id+"/approve", "browser").StatusCode)
	f.repo.mu.Lock()
	f.repo.token.UpdatedAt = time.Now().Add(time.Second)
	f.repo.mu.Unlock()
	args["confirmation_id"] = id
	result, _ := f.invoke(t, "execute_operation", args)
	require.True(t, result.IsError)
	require.Zero(t, f.writes.Load())
}

// TestProtectedFileOwnershipAndRevocation stores an encrypted result for one token.
// Another administrator cannot download it, and revocation removes access even
// from its original owner before the temporary file expires.
func TestProtectedFileOwnershipAndRevocation(t *testing.T) {
	f := newMCPFixture(t)
	file := &control.Artifact{ID: "owned-result", UserID: 1, TokenID: f.token.ID, OperationID: "fixture.get", Name: "result.json", ContentType: "application/json", Data: []byte(`{"secret":"browser-only"}`), ExpiresAt: time.Now().Add(time.Minute)}
	require.NoError(t, f.handler.state.SaveArtifact(t.Context(), file))
	response := f.browser(t, "GET", "/api/v1/admin/mcp/files/owned-result", "other-browser")
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	response = f.browser(t, "GET", "/api/v1/admin/mcp/files/owned-result", "browser")
	require.Equal(t, http.StatusOK, response.StatusCode)
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, file.Data, data)
	f.repo.mu.Lock()
	f.repo.token.Enabled = false
	f.repo.mu.Unlock()
	response = f.browser(t, "GET", "/api/v1/admin/mcp/files/owned-result", "browser")
	require.Equal(t, http.StatusForbidden, response.StatusCode)
}

// TestStreamCompletionAndOutputBounds supplies complete, failed, and truncated event
// streams. A transport success cannot mask a stream error or missing terminal,
// and oversized output cancels capture without returning truncated success data.
func TestStreamCompletionAndOutputBounds(t *testing.T) {
	f := newMCPFixture(t)
	op := &adminapi.Operation{ID: "fixture.test", TerminalEvent: "test_complete"}
	for _, test := range []struct {
		name   string // scenario identification label
		body   string // finite SSE wire data
		status string // expected operation state
	}{
		{"complete", "data: {\"type\":\"content\",\"text\":\"ok\"}\n\ndata: {\"type\":\"test_complete\",\"success\":true}\n\n", "succeeded"},
		{"error", "data: {\"type\":\"error\",\"error\":\"upstream failed\"}\n\ndata: {\"type\":\"test_complete\",\"success\":true}\n\n", "failed"},
		{"truncated", "data: {\"type\":\"content\",\"text\":\"partial\"}\n\n", "outcome_unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := f.handler.streamResult(f.token, op, []byte(test.body), "request-fixture")
			require.Equal(t, test.status, jsonTestValue[map[string]any](t, result.StructuredContent)["status"])
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := newCapture(ctx, cancel)
	defer writer.close()
	_, err := writer.Write(make([]byte, maxResultBytes+1))
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

// TestConfirmationRetainsExactLargeNumbers approves an integer beyond floating-point
// precision and then substitutes its adjacent value. The changed payload must
// be rejected even though both integers round to the same float64 value.
func TestConfirmationRetainsExactLargeNumbers(t *testing.T) {
	f := newMCPFixture(t)
	args := map[string]any{"operation_id": "fixture.protected", "body": map[string]any{"limit": int64(9007199254740993)}}
	_, pending := f.invoke(t, "execute_operation", args)
	id := jsonTestValue[string](t, pending["confirmation_id"])
	require.Equal(t, 200, f.browser(t, "POST", "/api/v1/admin/mcp/confirmations/"+id+"/approve", "browser").StatusCode)
	args["confirmation_id"] = id
	args["body"] = map[string]any{"limit": int64(9007199254740992)}
	result, _ := f.invoke(t, "execute_operation", args)
	require.True(t, result.IsError)
	require.Zero(t, f.writes.Load())
}

// jsonTestValue extracts a decoded result value for a typed assertion. Unexpected
// JSON shapes fail at the test caller, avoiding an unrelated type-assertion panic
// that would hide which response field changed.
func jsonTestValue[T any](t *testing.T, value any) T {
	t.Helper()
	result, ok := value.(T)
	require.True(t, ok, "unexpected JSON value type %T", value)
	return result
}

// TestTokenConfirmationPreferenceRetainsTOTPAndLiveRevocation opts one credential
// into direct sensitive execution. Ordinary confirmations disappear, existing TOTP
// still requires a browser session, and disabling the preference takes effect immediately.
func TestTokenConfirmationPreferenceRetainsTOTPAndLiveRevocation(t *testing.T) {
	f := newMCPFixture(t)
	operations := f.handler.catalog.Operations()
	var sensitive *adminapi.Operation
	for i, operation := range operations {
		if operation.ID == "fixture.create" {
			clone := *operation
			clone.Sensitive = true
			sensitive = &clone
			operations[i] = sensitive
		}
	}
	var err error
	f.handler.catalog, err = adminapi.NewCatalog(operations)
	require.NoError(t, err)
	args := map[string]any{"operation_id": "fixture.create", "body": map[string]any{"name": "direct"}}
	_, pending := f.invoke(t, "execute_operation", args)
	require.Equal(t, "confirmation_required", pending["status"])
	f.repo.mu.Lock()
	f.repo.token.SkipBrowserConfirmation = true
	f.repo.token.UpdatedAt = time.Now().Add(time.Second)
	f.repo.mu.Unlock()
	result, done := f.invoke(t, "execute_operation", args)
	require.False(t, result.IsError)
	require.Equal(t, "succeeded", done["status"])
	require.Equal(t, int64(1), f.writes.Load())
	_, description := f.invoke(t, "describe_operation", map[string]any{"operation_id": "fixture.create"})
	require.Equal(t, false, description["requires_confirmation"])
	_, protected := f.invoke(t, "execute_operation", map[string]any{"operation_id": "fixture.protected", "body": map[string]any{}})
	require.Equal(t, "confirmation_required", protected["status"])
	require.Equal(t, int64(1), f.writes.Load())

	token, err := f.repo.Get(t.Context(), 1)
	require.NoError(t, err)
	request, err := f.handler.request(t.Context(), token, sensitive, sensitive, control.Call{Body: json.RawMessage(`{}`)}, nil)
	require.NoError(t, err)
	dispatch, ok := authctx.GetMCPDispatch(request.Context())
	require.True(t, ok)
	require.True(t, dispatch.ConfirmationSkipped)
	f.repo.mu.Lock()
	f.repo.token.SkipBrowserConfirmation = false
	f.repo.token.UpdatedAt = f.repo.token.UpdatedAt.Add(time.Second)
	f.repo.mu.Unlock()
	response := httptest.NewRecorder()
	f.handler.router.ServeHTTP(response, request)
	require.Equal(t, 403, response.Code)
	require.Equal(t, int64(1), f.writes.Load())
	_, pending = f.invoke(t, "execute_operation", args)
	require.Equal(t, "confirmation_required", pending["status"])

	f.repo.mu.Lock()
	f.repo.token.SkipBrowserConfirmation = true
	f.repo.mu.Unlock()
	f.grants.enabled = false
	result, done = f.invoke(t, "execute_operation", map[string]any{"operation_id": "fixture.protected", "body": map[string]any{}})
	require.False(t, result.IsError)
	require.Equal(t, "succeeded", done["status"])
}

// TestMutationServerErrorRetainsUnknownOutcome models a write whose handler returns
// an error after dispatch. Server errors cannot establish rollback, while rejected
// client arguments and failed queries remain ordinary failures.
func TestMutationServerErrorRetainsUnknownOutcome(t *testing.T) {
	f := newMCPFixture(t)
	for _, test := range []struct {
		readOnly bool   // operation side effect classification
		code     int    // returned HTTP error status
		status   string // expected client outcome state
	}{{false, 500, "outcome_unknown"}, {false, 400, "failed"}, {true, 500, "failed"}} {
		writer := newCapture(t.Context(), func() {})
		writer.WriteHeader(test.code)
		_, err := writer.Write([]byte(`{"message":"storage unavailable"}`))
		require.NoError(t, err)
		result := f.handler.operationResult(t.Context(), f.token, &adminapi.Operation{ReadOnly: test.readOnly}, writer)
		writer.close()
		require.True(t, result.IsError)
		require.Equal(t, test.status, jsonTestValue[map[string]any](t, result.StructuredContent)["status"])
	}
}

// TestConfirmationStatusBelongsToOriginatingToken reads a pending review through
// the SDK. The originating token sees its state and redacted parameters, while
// another token identifier cannot reuse that confirmation reference.
func TestConfirmationStatusBelongsToOriginatingToken(t *testing.T) {
	f := newMCPFixture(t)
	f.repo.mu.Lock()
	f.repo.token.Permissions = append(f.repo.token.Permissions, "mcp.confirmations.get")
	f.repo.mu.Unlock()
	_, pending := f.invoke(t, "execute_operation", map[string]any{"operation_id": "fixture.protected", "body": map[string]any{"password": "private"}})
	id := jsonTestValue[string](t, pending["confirmation_id"])
	args := map[string]any{"operation_id": "mcp.confirmations.get", "path": map[string]string{"id": id}}
	result, data := f.invoke(t, "read_operation", args)
	require.False(t, result.IsError)
	review := jsonTestValue[map[string]any](t, jsonTestValue[map[string]any](t, data["data"])["data"])
	require.Equal(t, "pending", review["state"])
	require.NotContains(t, review["preview"], "private")
	approval, err := f.handler.state.GetApproval(t.Context(), id)
	require.NoError(t, err)
	approval.ID = "other-token-review"
	approval.TokenID = 2
	require.NoError(t, f.handler.state.CreateApproval(t.Context(), approval))
	args["path"] = map[string]string{"id": approval.ID}
	result, _ = f.invoke(t, "read_operation", args)
	require.True(t, result.IsError)
}

// TestArchiveDownloadUsesCurrentBrowserGrant exercises a protected streaming file.
// The current owner's browser session satisfies TOTP; removing its grant blocks
// the same link before the archive handler can deliver any bytes.
func TestArchiveDownloadUsesCurrentBrowserGrant(t *testing.T) {
	f := newMCPFixture(t)
	op := &adminapi.Operation{ID: "fixture.archive", Method: http.MethodGet, Path: "/api/v1/admin/fixture-archive/download", ReadOnly: true, Sensitive: true, StepUp: true, Response: adminapi.ResponseFile, Input: adminapi.Arguments("/api/v1/admin/fixture-archive/download", nil, nil)}
	catalog, err := adminapi.NewCatalog(append(f.handler.catalog.Operations(), op))
	require.NoError(t, err)
	f.handler.catalog = catalog
	router := jsonTestValue[*gin.Engine](t, f.handler.router)
	router.GET(op.Path, identityhttp.MCPAdminAuth(f.handler.tokens, func(c *gin.Context) { c.AbortWithStatus(401) }), identityhttp.StepUpAuth(f.grants, f.accounts, f.grants), func(c *gin.Context) { c.Data(200, "application/gzip", []byte("archive-content")) })
	f.repo.mu.Lock()
	f.repo.token.Permissions = append(f.repo.token.Permissions, op.ID)
	f.repo.mu.Unlock()
	file := f.handler.newArtifact(f.token, op.ID)
	file.Download = &control.Call{OperationID: op.ID}
	file.TokenVersion = f.token.UpdatedAt.UTC().Format(time.RFC3339Nano)
	require.NoError(t, f.handler.state.SaveArtifact(t.Context(), file))
	response := f.browser(t, "GET", "/api/v1/admin/mcp/files/"+file.ID, "browser")
	require.Equal(t, 200, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "archive-content", string(body))
	f.grants.granted.Store(false)
	response = f.browser(t, "GET", "/api/v1/admin/mcp/files/"+file.ID, "browser")
	require.Equal(t, 403, response.StatusCode)
}

// CreateUser records a successful fixture write after the handler's verification checks.
func (u conditionalUsers) CreateUser(_ context.Context, input *identity.CreateUserInput) (*identity.User, error) {
	u.writes.Add(1)
	return &identity.User{ID: 9, Role: input.Role, Status: identity.StatusActive}, nil
}

// UpdateUser records a successful fixture replacement after the handler's verification checks.
func (u conditionalUsers) UpdateUser(_ context.Context, id int64, input *identity.UpdateUserInput) (*identity.User, error) {
	u.writes.Add(1)
	return &identity.User{ID: id, Role: input.Role, Status: identity.StatusActive}, nil
}

// registerFixtureOperation binds a production description to its selected test handler.
// It grants that operation to the fixture and replaces the catalogue before any
// scenario request starts; shared production descriptions remain unchanged.
func registerFixtureOperation(t *testing.T, f *mcpFixture, op *adminapi.Operation, handler gin.HandlerFunc) {
	t.Helper()
	router, ok := f.handler.router.(*gin.Engine)
	require.True(t, ok)
	router.Handle(op.Method, op.Path, identityhttp.MCPAdminAuth(f.handler.tokens, func(c *gin.Context) { c.AbortWithStatus(401) }), handler)
	catalogue, err := adminapi.NewCatalog(append(f.handler.catalog.Operations(), op))
	require.NoError(t, err)
	f.handler.catalog = catalogue
	f.repo.mu.Lock()
	f.repo.token.Permissions = append(f.repo.token.Permissions, op.ID)
	f.repo.mu.Unlock()
}

// TestConditionalAdministratorVerificationCompletesMCPFlow exercises real create/update handlers for both requested roles.
// Conditional verification happens before approval, while ordinary user changes and disabled step-up retain their original calling rules.
func TestConditionalAdministratorVerificationCompletesMCPFlow(t *testing.T) {
	for _, update := range []bool{false, true} {
		for _, role := range []string{"admin", "user"} {
			for _, skip := range []bool{false, true} {
				for _, enabled := range []bool{false, true} {
					t.Run(fmt.Sprintf("update=%t/role=%s/skip=%t/policy=%t", update, role, skip, enabled), func(t *testing.T) {
						f := newMCPFixture(t)
						f.grants.enabled = enabled
						f.grants.granted.Store(false)
						f.repo.mu.Lock()
						f.repo.token.SkipBrowserConfirmation = skip
						f.repo.mu.Unlock()
						handler := identityhttp.NewAdminUserHandler[struct{}](conditionalUsers{writes: &f.writes}, nil, nil, func(c *gin.Context) bool { return identityhttp.EnforceStepUp(c, f.grants, f.accounts, f.grants) })
						op := identityhttp.ManagementOperations["users.create"]
						action := handler.Create
						args := map[string]any{"body": map[string]any{"email": "case@example.invalid", "password": "local-test-password", "role": role}}
						if update {
							op = identityhttp.ManagementOperations["users.update"]
							action = handler.Update
							args["path"] = map[string]string{"id": "9"}
						}
						args["operation_id"] = op.ID
						registerFixtureOperation(t, f, op, action)
						result, data := f.invoke(t, "execute_operation", args)
						needsStepUp := role == "admin" && enabled
						if !skip || needsStepUp {
							require.Equal(t, "confirmation_required", data["status"])
							require.Zero(t, f.writes.Load())
							id := jsonTestValue[string](t, data["confirmation_id"])
							if needsStepUp {
								require.Equal(t, 403, f.browser(t, "POST", "/api/v1/admin/mcp/confirmations/"+id+"/approve", "browser").StatusCode)
								approval, err := f.handler.state.GetApproval(t.Context(), id)
								require.NoError(t, err)
								require.Equal(t, "pending", approval.State)
								f.grants.granted.Store(true)
							}
							require.Equal(t, 200, f.browser(t, "POST", "/api/v1/admin/mcp/confirmations/"+id+"/approve", "browser").StatusCode)
							args["confirmation_id"] = id
							result, data = f.invoke(t, "execute_operation", args)
						}
						require.False(t, result.IsError, data)
						require.Equal(t, "succeeded", data["status"])
						require.Equal(t, int64(1), f.writes.Load())
					})
				}
			}
		}
	}
}

// TestMCPRequestSizeMatchesDeclaredUploadLimit sends ordinary JSON writes on both sides of the documented limit.
// A five MiB request reaches the handler, while an oversized request is rejected before its business write.
func TestMCPRequestSizeMatchesDeclaredUploadLimit(t *testing.T) {
	f := newMCPFixture(t)
	for _, size := range []int{5 << 20, 17 << 20} {
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "execute_operation", "arguments": map[string]any{"operation_id": "fixture.create", "body": map[string]any{"data": strings.Repeat("x", size)}}}})
		require.NoError(t, err)
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+"/mcp", bytes.NewReader(payload))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+f.credential)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		_, readErr := io.Copy(io.Discard, response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, readErr)
		if size < maxRequestBytes {
			require.Equal(t, 200, response.StatusCode)
		} else {
			require.Equal(t, 413, response.StatusCode)
		}
	}
	require.Equal(t, int64(1), f.writes.Load())
}

// TestConfirmationPreviewRetainsLargeUserIDs compares exact request identifiers with the browser-visible approval text.
// Schema validation may normalize numeric types, but the persisted preview and retry digest retain the submitted decimal value.
func TestConfirmationPreviewRetainsLargeUserIDs(t *testing.T) {
	f := newMCPFixture(t)
	op := identityhttp.ManagementOperations["users.batch_limits.create"]
	input := control.Call{OperationID: op.ID, Body: json.RawMessage(`{"user_ids":[9007199254740993],"concurrency":1}`)}
	args, err := callArguments(input)
	require.NoError(t, err)
	require.NoError(t, op.Validate(args))
	approval, err := f.handler.confirmation(t.Context(), f.token, op, input, args)
	require.NoError(t, err)
	require.Contains(t, approval.Preview, "9007199254740993")
	require.NotContains(t, approval.Preview, "9007199254740992")
	require.Contains(t, approval.Preview, "\n")
}

// TestGeneratedCodeFilePreservesJSONAndFilename exercises the common delivery mode used by generated promotion codes.
// MCP returns a file reference, and the administrator download contains the original code with its JSON filename.
func TestGeneratedCodeFilePreservesJSONAndFilename(t *testing.T) {
	f := newMCPFixture(t)
	op := &adminapi.Operation{ID: "fixture.promo", Method: "POST", Path: "/api/v1/admin/fixture-promo", Response: adminapi.ResponseFile, Input: adminapi.Arguments("/api/v1/admin/fixture-promo", nil, nil)}
	registerFixtureOperation(t, f, op, func(c *gin.Context) { c.JSON(200, gin.H{"code": 0, "data": gin.H{"code": "GENERATED-FIXTURE"}}) })
	result, data := f.invoke(t, "execute_operation", map[string]any{"operation_id": op.ID})
	require.False(t, result.IsError)
	require.Equal(t, "file_ready", data["status"])
	response := f.browser(t, "GET", "/api/v1/admin/mcp/files/"+jsonTestValue[string](t, data["file_id"]), "browser")
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "GENERATED-FIXTURE")
	require.Contains(t, response.Header.Get("Content-Disposition"), "fixture.promo.json")
	file, err := f.handler.state.GetArtifact(t.Context(), jsonTestValue[string](t, data["file_id"]))
	require.NoError(t, err)
	file.Name = "优惠码.json"
	require.NoError(t, f.handler.state.SaveArtifact(t.Context(), file))
	response = f.browser(t, "GET", "/api/v1/admin/mcp/files/"+file.ID, "browser")
	_, parameters, err := mime.ParseMediaType(response.Header.Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, file.Name, parameters["filename"])
	require.Contains(t, response.Header.Get("Content-Disposition"), "filename*=")
	require.Equal(t, "chosen.sql.gz", artifactFilename(op.ID, http.Header{"Content-Disposition": []string{`attachment; filename="chosen.sql.gz"`}}))
}

// TestOperationResultsRetainExactNumbers checks ordinary JSON and finite SSE results with large identifiers.
// Result conversion and text rendering must preserve the HTTP value just as confirmation previews preserve submitted parameters.
func TestOperationResultsRetainExactNumbers(t *testing.T) {
	f := newMCPFixture(t)
	capture := newCapture(t.Context(), func() {})
	defer capture.close()
	_, err := capture.Write([]byte(`{"code":0,"data":{"id":9007199254740993,"total_tokens":9007199254740993}}`))
	require.NoError(t, err)
	result := f.handler.operationResult(t.Context(), f.token, &adminapi.Operation{ID: "fixture.get", ReadOnly: true}, capture)
	encoded, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"id":9007199254740993`)
	require.Contains(t, string(encoded), `"total_tokens":9007199254740993`)
	result = f.handler.streamResult(f.token, &adminapi.Operation{TerminalEvent: "done"}, []byte("data: {\"type\":\"done\",\"success\":true,\"id\":9007199254740993}\n\n"), "fixture")
	encoded, err = json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"id":9007199254740993`)
}

// TestResultParsingRequiresOneCompleteJSONValue rejects treating a valid prefix as the whole handler result.
// Non-JSON bodies retain file delivery, while malformed finite events cannot claim a successful terminal response.
func TestResultParsingRequiresOneCompleteJSONValue(t *testing.T) {
	f := newMCPFixture(t)
	capture := newCapture(t.Context(), func() {})
	defer capture.close()
	_, err := capture.Write([]byte(`{"code":0}{"unexpected":true}`))
	require.NoError(t, err)
	result := f.handler.operationResult(t.Context(), f.token, &adminapi.Operation{ID: "fixture.get", ReadOnly: true}, capture)
	metadata := jsonTestValue[map[string]any](t, result.StructuredContent)
	require.Equal(t, "file_ready", metadata["status"])
	result = f.handler.streamResult(f.token, &adminapi.Operation{TerminalEvent: "done"}, []byte("data: {\"type\":\"done\",\"success\":true}{}\n\n"), "fixture")
	require.True(t, result.IsError)
	require.Equal(t, "outcome_unknown", jsonTestValue[map[string]any](t, result.StructuredContent)["status"])
}
