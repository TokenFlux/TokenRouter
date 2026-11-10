package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// mcp_oauth_clients_test.go checks registration validation, SSRF exclusions, and
// metadata cache behavior. An injected HTTP transport supplies document bytes;
// address classification tests cover the production dialer's rejection decisions.

// metadataRepository supplies only the client persistence used by metadata resolution tests.
type metadataRepository struct {
	identity.MCPOAuthRepository                          // unused OAuth persistence operations
	client                      *identity.MCPOAuthClient // current cached client record
	err                         error                    // injected storage read failure
}

// metadataTransport returns controlled HTTP responses while counting metadata retrieval attempts.
type metadataTransport struct {
	body  string // fixture metadata JSON response
	calls int    // observed document request count
}

// GetClient returns a matching unexpired record or the repository's not-found error.
func (r *metadataRepository) GetClient(_ context.Context, id string) (*identity.MCPOAuthClient, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.client != nil && r.client.ID == id && r.client.ExpiresAt != nil && time.Now().Before(*r.client.ExpiresAt) {
		return r.client, nil
	}
	return nil, identity.ErrMCPTokenNotFound
}

// PutClient records validated metadata for subsequent exact-identifier cache lookups.
func (r *metadataRepository) PutClient(_ context.Context, client *identity.MCPOAuthClient) error {
	r.client = client
	return nil
}

// RoundTrip serves the fixture document with an explicit short cache lifetime.
func (t *metadataTransport) RoundTrip(*http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{"Cache-Control": []string{"max-age=60"}}, Body: io.NopCloser(strings.NewReader(t.body))}, nil
}

// TestPublicCIMDAddress rejects private, loopback, shared, and transition destinations.
// Public IPv4 and IPv6 addresses remain usable, while mapped IPv4 addresses are
// classified after unmapping so they cannot bypass the IPv4 network policy.
func TestPublicCIMDAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.1.1", "192.0.2.1", "198.18.0.1", "240.0.0.1", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "64:ff9b::a00:1"} {
		require.False(t, publicCIMDAddress(netip.MustParseAddr(address)), address)
	}
	for _, address := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		require.True(t, publicCIMDAddress(netip.MustParseAddr(address)), address)
	}
}

// TestOAuthClientMetadataValidation accepts the supported PKCE client shape and
// rejects insecure or ambiguous callbacks. Unsupported authentication methods
// cannot become usable registrations through either document or DCR parsing.
func TestOAuthClientMetadataValidation(t *testing.T) {
	for _, callback := range []string{"https://client.example/callback", "http://127.0.0.1:1234/callback", "http://[::1]:1234/callback"} {
		client := oauthClientMetadata{MCPOAuthClient: identity.MCPOAuthClient{Name: "Client", AuthMethod: "none", RedirectURIs: []string{callback}}}
		require.NoError(t, validateOAuthClient(&client))
	}
	for _, callback := range []string{"http://client.example/callback", "https://user:pass@client.example/callback", "https://*.example/callback", "https://client.example/callback#fragment", "javascript:alert(1)"} {
		client := oauthClientMetadata{MCPOAuthClient: identity.MCPOAuthClient{Name: "Client", AuthMethod: "none", RedirectURIs: []string{callback}}}
		require.Error(t, validateOAuthClient(&client), callback)
	}
}

// TestCIMDExactIdentityCacheAndStorageFailure resolves an HTTPS client document and
// reads it again from its unexpired cache. A mismatched identity or database
// failure rejects resolution without accepting the fetched application metadata.
func TestCIMDExactIdentityCacheAndStorageFailure(t *testing.T) {
	repo := &metadataRepository{}
	transport := &metadataTransport{body: `{"client_id":"https://client.example/client.json","client_name":"Client","redirect_uris":["https://client.example/callback"],"token_endpoint_auth_method":"none"}`}
	server := &MCPOAuthServer{repo: repo, clientHTTP: &http.Client{Transport: transport}}
	client, err := server.client(t.Context(), "https://client.example/client.json")
	require.NoError(t, err)
	require.Equal(t, "Client", client.Name)
	_, err = server.client(t.Context(), client.ID)
	require.NoError(t, err)
	require.Equal(t, 1, transport.calls)
	_, err = server.client(t.Context(), "https://client.example/different.json")
	require.Error(t, err)
	require.Equal(t, 2, transport.calls)
	repo.err = errors.New("database unavailable")
	_, err = server.client(t.Context(), "https://another.example/client.json")
	require.ErrorContains(t, err, "database unavailable")
	require.Equal(t, 2, transport.calls)
}

// TestCIMDAuthenticationMethodNegotiation covers ChatGPT's published metadata shape:
// private_key_jwt is its legacy preference while none remains a supported method.
// A plural list without none must not inherit an incompatible singular preference.
func TestCIMDAuthenticationMethodNegotiation(t *testing.T) {
	for _, test := range []struct {
		name     string // named metadata compatibility scenario
		methods  string // JSON authentication method fields
		accepted bool   // expected public-client negotiation outcome
	}{
		{"ChatGPT transition", `"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["none","private_key_jwt"]`, true},
		{"legacy public", `"token_endpoint_auth_method":"none"`, true},
		{"private only", `"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["private_key_jwt"]`, false},
		{"plural overrides legacy", `"token_endpoint_auth_method":"none","token_endpoint_auth_methods_supported":["private_key_jwt"]`, false},
		{"empty capabilities", `"token_endpoint_auth_method":"none","token_endpoint_auth_methods_supported":[]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &metadataRepository{}
			transport := &metadataTransport{body: `{"client_id":"https://chatgpt.com/oauth/client.json","client_name":"ChatGPT","redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],"grant_types":["authorization_code","refresh_token"],` + test.methods + `}`}
			server := &MCPOAuthServer{repo: repo, clientHTTP: &http.Client{Transport: transport}}
			client, err := server.client(t.Context(), "https://chatgpt.com/oauth/client.json")
			if test.accepted {
				require.NoError(t, err)
				require.Equal(t, "none", client.AuthMethod)
				require.Equal(t, "https://chatgpt.com/oauth/client.json", client.ID)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestCIMDGrantTypeNegotiation resolves documents that advertise additional
// grants for other servers. The cached client keeps only its supported grants,
// and documents that omit the authorization code grant from a nonempty list
// cannot acquire it through the default.
func TestCIMDGrantTypeNegotiation(t *testing.T) {
	const clientID = "https://chatgpt.com/oauth/client.json"
	for _, test := range []struct {
		name   string   // named grant negotiation scenario
		grants string   // JSON array of advertised grants
		want   []string // effective grants, or nil for rejection
	}{
		{
			name:   "ChatGPT with JWT bearer grant",
			grants: `["authorization_code","refresh_token","urn:ietf:params:oauth:grant-type:jwt-bearer"]`,
			want:   []string{"authorization_code", "refresh_token"},
		},
		{
			name:   "code and extension without refresh",
			grants: `["authorization_code","urn:ietf:params:oauth:grant-type:jwt-bearer"]`,
			want:   []string{"authorization_code"},
		},
		{
			name:   "extension without code",
			grants: `["urn:ietf:params:oauth:grant-type:jwt-bearer"]`,
		},
		{
			name:   "refresh and extension without code",
			grants: `["refresh_token","urn:ietf:params:oauth:grant-type:jwt-bearer"]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &metadataRepository{}
			transport := &metadataTransport{body: `{
				"client_id":"https://chatgpt.com/oauth/client.json",
				"client_name":"ChatGPT",
				"redirect_uris":["https://chatgpt.com/connector_platform_oauth_redirect"],
				"token_endpoint_auth_method":"private_key_jwt",
				"token_endpoint_auth_methods_supported":["none","private_key_jwt"],
				"response_types":["code"],
				"grant_types":` + test.grants + `
			}`}
			server := &MCPOAuthServer{repo: repo, clientHTTP: &http.Client{Transport: transport}}
			client, err := server.client(t.Context(), clientID)
			if test.want == nil {
				require.ErrorContains(t, err, "authorization_code grant is required")
				require.Nil(t, repo.client)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.want, client.GrantTypes)
			cached, err := repo.GetClient(t.Context(), clientID)
			require.NoError(t, err)
			require.Equal(t, test.want, cached.GrantTypes)
		})
	}
}

// TestDCRAcceptsOptionalClientName registers the minimal public authorization client.
// Missing display text receives a neutral label while the issued identifier and
// callback remain the authoritative application details shown during browser consent.
func TestDCRAcceptsOptionalClientName(t *testing.T) {
	repo := &metadataRepository{}
	server := &MCPOAuthServer{repo: repo}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example/mcp/oauth/register", strings.NewReader(`{"redirect_uris":["https://client.example/callback"],"token_endpoint_auth_method":"none"}`))
	response := httptest.NewRecorder()
	server.Register(response, request)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	require.Equal(t, "MCP application", repo.client.Name)
	require.Equal(t, "none", repo.client.AuthMethod)
	require.NotEmpty(t, repo.client.ID)
}

// TestDCRRejectsUnsupportedGrant submits an extension grant alongside the
// authorization code grant. The registration endpoint rejects the request
// before writing a client record.
func TestDCRRejectsUnsupportedGrant(t *testing.T) {
	repo := &metadataRepository{}
	server := &MCPOAuthServer{repo: repo}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.example/mcp/oauth/register", strings.NewReader(`{
		"redirect_uris":["https://client.example/callback"],
		"token_endpoint_auth_method":"none",
		"grant_types":["authorization_code","urn:ietf:params:oauth:grant-type:jwt-bearer"]
	}`))
	response := httptest.NewRecorder()
	server.Register(response, request)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "invalid_client_metadata")
	require.Nil(t, repo.client)
}
