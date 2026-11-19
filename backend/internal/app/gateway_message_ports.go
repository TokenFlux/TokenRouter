package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
)

// gateway_message_ports.go adapts retry cooldown, session isolation, and
// digest-session storage for Messages attempts. These adapters translate
// failure details for provider cooldown decisions and connect request group
// and session identity to the shared stores, including the one-hour isolation
// owner lifetime.

// provideRetryCooldown constructs provider cooldown decisions with diagnostics
// and optional storage. Nil store leaves the storage capability unset, and the
// provider service owns the retry-exhaustion rule applied to each input.
func provideRetryCooldown(store *providerpostgres.ProviderStore) *provider.RetryCooldown {
	var source provider.RetryCooldownStore
	if store != nil {
		source = store
	}
	return provider.NewRetryCooldown(source, provider.RetryCooldownOptions{
		Logf: func(format string, args ...any) { logging.LegacyPrintf("service.gateway", format, args...) },
		LookupError: func(id int64, err error) {
			logging.LegacyPrintf("service.gateway", "Failed to look up provider after retry exhaustion: provider=%d error=%v", id, err)
		},
	})
}

// messageRetryCooldown adapts upstream failure details into provider cooldown
// input. Nil failure supplies the provider ID with zero failure flags, leaving
// command.Apply to decide whether that observation warrants containment.
func messageRetryCooldown(command *provider.RetryCooldown) func(context.Context, int64, *forward.UpstreamFailoverError) {
	return func(ctx context.Context, id int64, failure *forward.UpstreamFailoverError) {
		input := provider.RetryCooldownInput{ProviderID: id}
		if failure != nil {
			input.Status = failure.StatusCode
			input.Retryable = failure.RetryableOnSameProvider
			input.RequestScopedTransient = failure.RequestScopedTransient
		}
		command.Apply(ctx, input)
	}
}

// messageSessionIsolation returns a one-hour session ownership check for the
// supplied cache. A nil Key succeeds immediately; absent GroupID selects zero,
// and the current group controls whether isolation is enabled.
func messageSessionIsolation(cache session.GatewayCache) func(context.Context, *apikey.APIKey, int64, string, string) error {
	return func(ctx context.Context, key *apikey.APIKey, userID int64, source, hash string) error {
		if key == nil {
			return nil
		}
		groupID := int64(0)
		if key.GroupID != nil {
			groupID = *key.GroupID
		}
		return session.EnsureIsolation(ctx, cache, session.IsolationInput{UserID: userID, GroupID: groupID, Source: source, Hash: hash, TTL: time.Hour, Enabled: key.Group != nil && key.Group.SessionIsolationEnabled})
	}
}

// messageDigestFind binds in-memory digest lookup to the shared store. The
// returned callback ignores request cancellation because Find performs its
// local lookup using the supplied ID, prefix, and chain.
func messageDigestFind(store *session.DigestSessionStore) func(context.Context, int64, string, string) (string, int64, string, bool) {
	return func(_ context.Context, id int64, prefix, chain string) (string, int64, string, bool) {
		return store.Find(id, prefix, chain)
	}
}

// messageDigestSave binds in-memory session saving to the shared digest store.
// The callback ignores its context, passes the session identity and prior
// digest to Save, and returns nil after that local operation.
func messageDigestSave(store *session.DigestSessionStore) func(context.Context, int64, string, string, string, int64, string) error {
	return func(_ context.Context, id int64, prefix, chain, uuid string, providerID int64, old string) error {
		store.Save(id, prefix, chain, uuid, providerID, old)
		return nil
	}
}
