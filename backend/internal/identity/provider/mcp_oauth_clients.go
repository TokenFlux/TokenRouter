package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// mcp_oauth_clients.go validates registrations and HTTPS client metadata documents.
// Client names are untrusted display text; the exact client identifier and
// callback list determine which application receives an authorization code.
// Confidential registration secrets are issued once and stored as password
// verifiers, separate from the metadata returned to a consenting administrator.
// A document can advertise capabilities for several authorization servers.
// The cache stores only grants usable at this server. Exchanges that read the
// cached record use the same grant list as the request that populated it.
//
// Document retrieval validates resolved addresses inside the dial operation and
// connects directly to the checked address. Environment proxies and redirects
// are disabled to prevent routing public document URLs to private services.
// Response size, request duration, and cache freshness are bounded independently;
// stale documents and failed storage reads never authorize a client.

// These ranges exclude shared, reserved, documentation, benchmark, and transition
// networks that the standard library's private-address predicate does not reject.
var cimdBlockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("64:ff9b::/96"), netip.MustParsePrefix("64:ff9b:1::/48"),
}

// oauthClientMetadata contains supported public client registration and response metadata.
type oauthClientMetadata struct {
	// AuthMethods lists the methods a CIMD client can use. When present,
	// this list governs negotiation instead of the legacy singular preference.
	AuthMethods []string `json:"token_endpoint_auth_methods_supported"`

	identity.MCPOAuthClient          // application registration metadata
	ResponseTypes           []string `json:"response_types"` // permitted authorization response types
}

// newCIMDHTTPClient constructs bounded retrieval with DNS-rebinding protection.
// TLS verifies the original hostname. Every resolved address must be public,
// including when a hostname returns several addresses of different families.
//
// Dialing uses a checked address directly, so a second DNS lookup cannot substitute
// an internal service between validation and connection. The transport has no
// proxy callback, and the client does not follow redirects. Request, header, and
// TLS deadlines bound failed metadata retrieval independently from the caller's
// own cancellation scope.
func newCIMDHTTPClient() *http.Client {
	transport := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: time.Minute, MaxIdleConns: 16, MaxIdleConnsPerHost: 2}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("client metadata host unavailable")
		}
		for _, ip := range ips {
			if !publicCIMDAddress(ip) {
				return nil, errors.New("client metadata host is not public")
			}
		}
		// Connect to the validated IP directly so a second DNS lookup cannot
		// substitute a private service while TLS still verifies the requested hostname.
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// publicCIMDAddress accepts public unicast addresses outside reserved and private networks.
func publicCIMDAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range cimdBlockedPrefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

// client resolves a registered client or a currently valid HTTPS metadata document.
// Missing cached metadata can trigger retrieval. Storage errors are propagated,
// and discovered applications use public-client PKCE authentication.
// A nonempty grant list must include the authorization code flow.
//
// The document's client identifier must match its requested URL exactly before
// callbacks become eligible for authorization. Retrieval rejects redirects,
// non-success status, oversized bodies, and invalid metadata. Cache freshness is
// bounded by five minutes and shorter response directives; stale entries cannot
// supply authorization when their replacement document cannot be validated.
func (s *MCPOAuthServer) client(ctx context.Context, id string) (*identity.MCPOAuthClient, error) {
	cached, err := s.repo.GetClient(ctx, id)
	if err == nil {
		return cached, nil
	}
	if !errors.Is(err, identity.ErrMCPTokenNotFound) {
		return nil, err
	}
	target, err := url.Parse(id)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.Path == "" || target.Path == "/" || target.User != nil || target.Fragment != "" || len(id) > 2048 {
		return nil, identity.ErrMCPTokenNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	response, err := s.clientHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("client metadata is unavailable")
	}
	// Read one extra byte to detect oversized documents before JSON parsing can
	// accept an incomplete metadata response within the advertised size limit.
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("client metadata exceeds the response limit")
	}
	var metadata oauthClientMetadata
	if json.Unmarshal(raw, &metadata) != nil || metadata.ID != id {
		return nil, errors.New("client metadata identifier does not match")
	}
	if metadata.AuthMethods != nil {
		// CIMD clients can advertise several methods with a legacy preference.
		// Accept the public method only when it belongs to the advertised intersection.
		if !slices.Contains(metadata.AuthMethods, "none") {
			return nil, errors.New("unsupported client metadata authentication methods")
		}
		metadata.AuthMethod = "none"
	} else if metadata.AuthMethod == "" {
		metadata.AuthMethod = "none"
	}
	if metadata.AuthMethod != "none" {
		return nil, errors.New("unsupported client metadata authentication method")
	}
	// Select the local subset from a shared CIMD document before validation and
	// caching. Leave a nonempty list without authorization_code for validation
	// to reject, since an empty list would default to authorization_code.
	if slices.Contains(metadata.GrantTypes, "authorization_code") {
		grants := []string{"authorization_code"}
		if slices.Contains(metadata.GrantTypes, "refresh_token") {
			grants = append(grants, "refresh_token")
		}
		metadata.GrantTypes = grants
	}
	if err := validateOAuthClient(&metadata); err != nil {
		return nil, err
	}
	// Keep callback metadata fresh while avoiding a document fetch for every request.
	ttl := 5 * time.Minute
	for directive := range strings.SplitSeq(response.Header.Get("Cache-Control"), ",") {
		directive = strings.TrimSpace(strings.ToLower(directive))
		if directive == "no-store" || directive == "no-cache" {
			ttl = 0
		}
		if value, ok := strings.CutPrefix(directive, "max-age="); ok {
			if seconds, err := strconv.Atoi(strings.Trim(value, "\"")); err == nil {
				ttl = min(ttl, time.Duration(max(0, min(seconds, 300)))*time.Second)
			}
		}
	}
	deadline := time.Now().UTC().Add(ttl)
	metadata.ExpiresAt = &deadline
	if ttl > 0 {
		if err := s.repo.PutClient(ctx, &metadata.MCPOAuthClient); err != nil {
			return nil, err
		}
	}
	return &metadata.MCPOAuthClient, nil
}

// Register persists validated metadata and returns the newly generated client credentials.
// Confidential secrets are disclosed once, with bcrypt verifiers stored separately.
// Existing registrations remain immutable and each accepted request gets a new identifier.
func (s *MCPOAuthServer) Register(w http.ResponseWriter, r *http.Request) {
	var metadata oauthClientMetadata
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&metadata) != nil || decoder.Decode(new(any)) != io.EOF {
		WriteOAuthJSON(w, 400, map[string]string{"error": "invalid_client_metadata"})
		return
	}
	if metadata.AuthMethod == "" {
		metadata.AuthMethod = "client_secret_basic"
	}
	// DCR permits an omitted display name. The generated client identifier and
	// validated callback remain visible on consent, so this label implies no trusted identity.
	if strings.TrimSpace(metadata.Name) == "" {
		metadata.Name = "MCP application"
	}

	if err := validateOAuthClient(&metadata); err != nil {
		WriteOAuthJSON(w, 400, map[string]string{"error": "invalid_client_metadata", "error_description": err.Error()})
		return
	}
	id, err := oauthRandom()
	if err != nil {
		WriteOAuthJSON(w, 500, map[string]string{"error": "server_error"})
		return
	}
	metadata.ID = "mcp_" + id
	var secret string
	if metadata.AuthMethod != "none" {
		secret, err = oauthRandom()
		if err == nil {
			metadata.SecretHash, err = bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
		}
		if err != nil {
			WriteOAuthJSON(w, 500, map[string]string{"error": "server_error"})
			return
		}
	}
	if err := s.repo.PutClient(r.Context(), &metadata.MCPOAuthClient); err != nil {
		WriteOAuthJSON(w, 500, map[string]string{"error": "server_error"})
		return
	}
	result := map[string]any{
		"client_id":                  metadata.ID,
		"client_name":                metadata.Name,
		"redirect_uris":              metadata.RedirectURIs,
		"grant_types":                metadata.GrantTypes,
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": metadata.AuthMethod,
		"client_id_issued_at":        time.Now().Unix(),
	}
	if secret != "" {
		result["client_secret"] = secret
		result["client_secret_expires_at"] = 0
	}
	WriteOAuthJSON(w, http.StatusCreated, result)
}

// validateOAuthClient normalizes supported grants and checks bounded registration metadata.
// HTTPS callbacks and loopback HTTP callbacks are allowed. Credentials, fragments,
// wildcard hosts, unsupported authentication methods, and other grant types are rejected.
func validateOAuthClient(metadata *oauthClientMetadata) error {
	metadata.Name = strings.TrimSpace(metadata.Name)
	if metadata.Name == "" || utf8.RuneCountInString(metadata.Name) > 120 {
		return errors.New("client_name must contain 1 to 120 characters")
	}
	if !slices.Contains([]string{"none", "client_secret_basic", "client_secret_post"}, metadata.AuthMethod) {
		return errors.New("unsupported token_endpoint_auth_method")
	}
	if len(metadata.RedirectURIs) == 0 || len(metadata.RedirectURIs) > 10 {
		return errors.New("provide 1 to 10 redirect_uris")
	}
	for _, raw := range metadata.RedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(u.Host, "*") || len(raw) > 2048 {
			return errors.New("invalid redirect_uri")
		}
		ip := net.ParseIP(u.Hostname())
		loopback := u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()
		secure := u.Scheme == "https" || u.Scheme == "http" && loopback
		if !secure {
			return errors.New("redirect_uri requires HTTPS or loopback HTTP")
		}
	}
	if len(metadata.GrantTypes) == 0 {
		metadata.GrantTypes = []string{"authorization_code"}
	}
	if !slices.Contains(metadata.GrantTypes, "authorization_code") || len(metadata.GrantTypes) > 2 {
		return errors.New("authorization_code grant is required")
	}
	for _, grant := range metadata.GrantTypes {
		if grant != "authorization_code" && grant != "refresh_token" {
			return errors.New("unsupported grant_type")
		}
	}
	if len(metadata.ResponseTypes) > 0 && (len(metadata.ResponseTypes) != 1 || metadata.ResponseTypes[0] != "code") {
		return errors.New("only code responses are supported")
	}
	return nil
}
