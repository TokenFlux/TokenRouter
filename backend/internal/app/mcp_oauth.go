package app

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// mcp_oauth.go assembles the OAuth protocol service and expired-state cleanup.
// Bootstrap supplies an independent persisted signing secret so backend
// restarts can verify existing opaque credentials. The existing
// frontend URL determines the public issuer and protocol availability. Grant
// management remains available while the address is missing or invalid.
//
// Cleanup starts under the application lifecycle and stops before database
// shutdown. Each pass has a bounded context, and shutdown cancels an active
// pass before waiting for its worker. Family cleanup retains consumed refresh
// records while another family member remains unexpired, preserving replay
// detection across access-token renewal and process restarts.

// mcpOAuthFrontend reads the existing site address for OAuth protocol requests.
// The deployment frontend URL supplies the existing default when no nonblank
// database address exists. Read errors remain failures rather than changing origins.
type mcpOAuthFrontend struct {
	store       *settings.Store // Authoritative site settings store.
	frontendURL string          // Deployment frontend address default.
}

// provideMCPOAuth constructs OAuth services and registers bounded expired-state cleanup.
// Missing or malformed persistent signing material fails service assembly.
// The repository and user reader share the application's existing database resources.
//
// The lifecycle starts one worker, which owns its ticker and pass-specific timeout.
// Shutdown cancels the worker context before waiting for its completion channel,
// so an active SQL cleanup can stop before the database pool closes. Failed passes
// are logged and retried on the next hourly tick; they do not authorize requests
// or remove still-live replay detection records.
func provideMCPOAuth(db *sql.DB, users *identity.UserService, cfg *config.Config, store *settings.Store, manager *lifecycle.Manager) (*identityprovider.MCPOAuthServer, error) {
	repo := identitypostgres.NewMCPOAuthStore(db)
	secret, err := hex.DecodeString(cfg.MCPOAuthSigningSecret)
	if err != nil || len(secret) != 32 {
		return nil, fmt.Errorf("MCP OAuth signing secret is unavailable")
	}
	server := identityprovider.NewMCPOAuthServer(repo, users, &mcpOAuthFrontend{store: store, frontendURL: cfg.Site.FrontendURL}, secret)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	manager.Register(lifecycle.Hook{
		Name: "MCPOAuthCleanup", StartOrder: 100, StopOrder: 100,
		Start: func(context.Context) error {
			// The lifecycle starts this worker before serving traffic. Cancellation reaches
			// the active SQL pass, and the completion channel joins it before database shutdown.
			go func() {
				defer close(done)
				ticker := time.NewTicker(time.Hour)
				defer ticker.Stop()
				for {
					pass, stop := context.WithTimeout(ctx, 30*time.Second)
					err := repo.Cleanup(pass)
					stop()
					if err != nil && ctx.Err() == nil {
						slog.Warn("MCP OAuth cleanup failed", "error", err)
					}
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}()
			return nil
		},
		Stop: func(ctx context.Context) error {
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	return server, nil
}

// FrontendURL resolves the stored site address or the existing deployment default.
// A missing or blank setting uses the deployment frontend URL. Storage failures
// remain errors so OAuth cannot switch origins during a transient database failure.
func (s *mcpOAuthFrontend) FrontendURL(ctx context.Context) (string, error) {
	value, err := s.store.GetValue(ctx, "frontend_url")
	if err != nil && !errors.Is(err, settings.ErrSettingNotFound) {
		return "", err
	}
	if value = strings.TrimSpace(value); value != "" {
		return value, nil
	}
	return s.frontendURL, nil
}
