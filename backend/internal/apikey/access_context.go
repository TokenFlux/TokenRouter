package apikey

import (
	"context"
)

// access_context.go stores and updates authenticated request identity,
// effective Key views, and per-request Key policy overrides in contexts.
// Context updates copy the access record and its TeamID so those scalar
// decisions stay local, while the request-owned key pointer remains available
// to later gateway processing.
//
// WithRuntimeAPIKey accepts a replacement only when its ID matches the saved
// access record. A replacement with a hydrated user refreshes the payer ID
// while retaining the owner and actor IDs already established for the request.
// This check keeps later group admission from attaching an unrelated
// credential to the authenticated identity.

// accessSnapshotContextKey identifies authenticated access values in standard
// request contexts.
type accessSnapshotContextKey struct{}

// WithAccessSnapshot stores an access record with a copied TeamID in ctx. The
// private key pointer remains shared with the supplied record, so callers keep
// that request view within its owning request.
func WithAccessSnapshot(ctx context.Context, access AccessSnapshot) context.Context {
	access.TeamID = clonePointer(access.TeamID)
	return context.WithValue(ctx, accessSnapshotContextKey{}, access)
}

// AccessSnapshotFromContext reads the access record and copies its TeamID
// before returning. A nil context, absent value, or different stored type
// returns the zero record with false; the private key pointer remains shared.
func AccessSnapshotFromContext(ctx context.Context) (AccessSnapshot, bool) {
	if ctx == nil {
		return AccessSnapshot{}, false
	}
	access, ok := ctx.Value(accessSnapshotContextKey{}).(AccessSnapshot)
	access.TeamID = clonePointer(access.TeamID)
	return access, ok
}

// WithFastModePolicy stores policy in a copied access record for later provider
// processing. An absent access record starts with zero identity fields; the
// supplied policy is accepted as given and requires caller normalization.
func WithFastModePolicy(ctx context.Context, policy string) context.Context {
	access, _ := AccessSnapshotFromContext(ctx)
	access.fastModePolicy = policy
	return WithAccessSnapshot(ctx, access)
}

// FastModePolicy returns the acceleration policy saved for this request.
func (a AccessSnapshot) FastModePolicy() string { return a.fastModePolicy }

// WithForceClaude1hCache stores enabled in a copied access record for later
// Claude cache-policy resolution. An absent access record starts with zero
// identity fields while preserving the supplied cache-lifetime choice.
func WithForceClaude1hCache(ctx context.Context, enabled bool) context.Context {
	access, _ := AccessSnapshotFromContext(ctx)
	access.forceClaude1hCache = enabled
	return WithAccessSnapshot(ctx, access)
}

// ForceClaude1hCache reports whether this request forces Claude ephemeral cache markers to 1h.
func (a AccessSnapshot) ForceClaude1hCache() bool { return a.forceClaude1hCache }

// WithRuntimeAPIKey publishes key as the effective view when its ID matches the
// stored access record. Missing access, nil keys, and mismatched IDs return ctx
// unchanged; a present User refreshes PayerUserID while the other identity,
// Fast policy, and Claude cache-lifetime override fields retain their values.
func WithRuntimeAPIKey(ctx context.Context, key *APIKey) context.Context {
	access, ok := AccessSnapshotFromContext(ctx)
	if !ok || key == nil || access.KeyID != key.ID {
		return ctx
	}
	access.key = key
	if key.User != nil {
		access.PayerUserID = key.User.ID
	}
	return WithAccessSnapshot(ctx, access)
}
