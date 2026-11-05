package app

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	redisinfra "github.com/TokenFlux/TokenRouter/internal/infra/redis"
	mcphttp "github.com/TokenFlux/TokenRouter/internal/mcp/httpapi"
	mcpredis "github.com/TokenFlux/TokenRouter/internal/mcp/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// mcp.go assembles dedicated administrator credentials, shared temporary state,
// and the MCP transport. SQL grants are read directly for authorization; Redis
// holds expiring confirmations and encrypted file results. Browser origins come
// from explicit CORS entries and configured public site URLs, preventing an
// arbitrary Host header from establishing a trusted browser origin.

// provideMCPTokens binds token storage to live account reads and durable encryption.
// An unconfigured startup key is ephemeral, so issuance and credential copying
// remain unavailable until the deployment supplies its fixed TOTP encryption key.
func provideMCPTokens(db *sql.DB, users *identity.UserService, cipher identity.SecretEncryptor, cfg *config.Config, oauth *identityprovider.MCPOAuthServer) *identity.MCPTokenService {
	if cfg == nil || !cfg.Security.SecretEncryptionKeyConfigured {
		cipher = nil
	}
	service := identity.NewMCPTokenService(identitypostgres.NewMCPTokenStore(db), users, cipher)
	service.SetOAuth(oauth)
	return service
}

// provideMCPHTTP constructs the management transport using shared identity resources.
// Origin reads fail closed for unavailable dynamic URLs; explicit configured
// origins remain usable. Wildcard CORS entries never authorize MCP browser origins.
func provideMCPHTTP(oauth *identityprovider.MCPOAuthServer, tokens *identity.MCPTokenService, client *redis.Client, cipher identity.SecretEncryptor, totp *identity.TotpService, users *identity.UserService, identitySettings *identity.RuntimeSettings, store *settings.Store, cfg *config.Config) (*mcphttp.Handler, error) {
	var err error
	cipher, err = mcpArtifactCipher(cipher, cfg)
	if err != nil {
		return nil, err
	}
	origins := func(ctx context.Context) []string {
		allowed := slices.Clone(cfg.Security.CORS.AllowedOrigins)
		if status, err := oauth.Status(ctx); err == nil && status.Issuer != "" {
			allowed = append(allowed, status.Issuer)
		}
		for _, key := range []string{"api_base_url", "frontend_url"} {
			value, err := store.GetValue(ctx, key)
			if err != nil {
				// Missing public URL configuration grants no extra browser origin;
				// native MCP clients without Origin still authenticate normally with their dedicated token.
				continue
			}
			parsed, err := url.Parse(strings.TrimSpace(value))
			if err == nil && parsed.Host != "" && parsed.User == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
				allowed = append(allowed, parsed.Scheme+"://"+parsed.Host)
			}
		}
		return allowed
	}
	return mcphttp.NewHandler(tokens, mcpredis.NewState(client, cipher), totp, users, identitySettings, mcphttp.Options{OAuth: oauth, Origins: origins, BrowserBaseURL: func(ctx context.Context) string {
		return site.ReadFrontendURL(ctx, store, func() string { return cfg.Site.FrontendURL })
	}, Limiter: redisinfra.NewFixedWindowLimiter(client, "mcp:rate:")}), nil
}

// mcpArtifactCipher selects a stable encryption key for shared temporary MCP files.
// Configured TOTP encryption retains the existing ciphertext format and key.
// Deployments with ephemeral TOTP configuration derive a separate artifact
// key from their independently persisted signing secret using a fixed HKDF context.
//
// This choice lets backend restarts decrypt surviving Redis file records while
// keeping the HMAC signing key distinct from the AES encryption key. An invalid
// persisted secret prevents transport construction; changing deployed key material
// can make earlier temporary files unavailable until their ordinary TTL expires.
func mcpArtifactCipher(existing identity.SecretEncryptor, cfg *config.Config) (identity.SecretEncryptor, error) {
	if cfg == nil || cfg.Security.SecretEncryptionKeyConfigured {
		return existing, nil
	}
	secret, err := hex.DecodeString(cfg.MCPOAuthSigningSecret)
	if err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("MCP OAuth artifact secret is unavailable")
	}
	key, err := hkdf.Key(sha256.New, secret, nil, "TokenRouter MCP artifact encryption v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive MCP artifact key: %w", err)
	}
	return crypto.NewAESEncryptor(key)
}
