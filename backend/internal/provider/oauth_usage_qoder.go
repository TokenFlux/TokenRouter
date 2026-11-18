package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// oauth_usage_qoder.go coordinates Qoder usage fetching, conditional snapshot
// persistence, and scheduling signals.
// Qoder display queries share work by provider row while associating cached
// and completed observations with credential identity. Force skips the
// caller’s cache checks but can still join an existing flight, whose captured
// identity is validated before returning independently copied display values.
//
// New queries use detached thirty-second activity so caller cancellation does
// not end shared fetching and service shutdown still owns completion. Fetch
// failures may become cached diagnostics with nil returned error. Successful
// quota results attempt conditional snapshot persistence before scheduling
// signals; a comparison miss aborts, while a persistence error still permits
// later scheduling/cache work. JSON conversion failure and absent quota/store
// are treated as skipped persistence success. Callers retain stable records
// and reached callbacks through the shared query.

// Qoder usage keys identify the stored JSON quota snapshot and observation
// timestamp used by later provider display and quota recovery consumers.
const (
	QoderUsageQuotaSnapshotExtraKey  = "qoder_quota_snapshot"
	QoderUsageQuotaUpdatedAtExtraKey = "qoder_quota_updated_at"

	// qoderQuotaAccountKey binds saved quota to the same account scope as the model catalog.
	qoderQuotaAccountKey = "account_identity"
)

// qoderUsageFlightResult ties shared display data to the credential identity
// that fetched it.
type qoderUsageFlightResult struct {
	// Usage is shared by row-key flight waiters. Each identity-matching caller
	// copies its mutable fields before independently editing returned display
	// data.
	Usage    *UsageInfo
	Identity string // queried credential identity digest
}

// QoderUsageOptions binds quota fetching, diagnostic conversion, and account
// display enrichment.
type QoderUsageOptions struct {
	// Fetch is required for cache misses and supplies nonnil successful display
	// data under detached work. Its record and clock remain stable through
	// enrichment.
	Fetch func(context.Context, *Record, func() time.Time) (*UsageInfo, error)
	// Degrade is required after fetch failure and supplies nonnil diagnostic
	// display data. The enriched result is cached with nil API error.
	Degrade func(error, *Record, time.Time) *UsageInfo
	// Enrich is required after fetch or degradation and mutates the owned view.
	// It completes before conditional persistence and copied cache publication
	// begin.
	Enrich func(*UsageInfo, *Record)
}

// GetQoderUsage returns copied cached or queried display data for a stable
// record. Nil record returns a dated empty view, while constructor-initialized
// cache and reached Fetch/Enrich/Degrade callbacks are required for shared
// work.
//
// Force bypasses local cache reuse but still joins same-row flights. Detached
// cancellation returns an error; other fetch failures become enriched cached
// diagnostics. Snapshot comparison miss stops scheduling and cache
// publication, but persistence error allows both to proceed. Final nonnil
// flight data must match the captured credential identity before copying. The
// nil-cache repair is unsynchronized, so manually constructed services
// initialize it before concurrent use. Supply a nonnil context when a cache
// miss reaches detached activity registration.
func (s *OAuthUsageService) GetQoderUsage(ctx context.Context, provider *Record, force bool) (*UsageInfo, error) {
	now := s.options.Now()
	if provider == nil {
		return &UsageInfo{UpdatedAt: &now}, nil
	}
	if s.cache == nil {
		s.cache = NewOAuthUsageCache()
	}

	if !force {
		if cache, ok := s.cache.LoadQoder(provider.ID); ok && QoderUsageCacheUsable(provider, cache, s.options.Now()) {
			return cache.UsageInfo, nil
		}
	}

	identity := UsageCacheIdentity(provider)
	flightKey := fmt.Sprintf("qoder-usage:%d", provider.ID)
	result, flightErr, _ := s.cache.DoQoder(flightKey, func() (any, error) {
		if !force {
			if cache, ok := s.cache.LoadQoder(provider.ID); ok && QoderUsageCacheUsable(provider, cache, s.options.Now()) {
				return qoderUsageFlightResult{cache.UsageInfo, cache.Identity}, nil
			}
		}

		// Shared work keeps context values while ignoring caller cancellation.
		// Service shutdown still cancels this registered fetch and joins its actual
		// completion under the independent thirty-second budget.
		fetchCtx, cancel, beginErr := s.BeginDetached(ctx, 30*time.Second)
		if beginErr != nil {
			return nil, beginErr
		}
		defer cancel()

		usage, err := s.options.Qoder.Fetch(fetchCtx, provider, s.options.Now)
		if fetchCtx.Err() == context.Canceled {
			return nil, fetchCtx.Err()
		}
		// A non-shutdown fetch error becomes cacheable display diagnostics.
		// Returning nil flight error lets matching waiters receive that enriched
		// view, with error fields controlling its shorter cache lifetime.
		if err != nil {
			degraded := s.options.Qoder.Degrade(err, provider, s.options.Now())
			s.options.Qoder.Enrich(degraded, provider)
			s.cache.StoreQoder(provider.ID, &OAuthQoderUsageCache{Identity: identity, UsageInfo: CloneUsageInfo(degraded), Timestamp: s.options.Now()})
			return qoderUsageFlightResult{CloneUsageInfo(degraded), identity}, nil
		}

		s.options.Qoder.Enrich(usage, provider)
		// Persist the identity-associated snapshot before scheduling or caching.
		// A clean comparison miss rejects this observation; an operational error
		// retains the existing best-effort follow-up path.
		applied, persistErr := s.persistQoderQuotaSnapshot(fetchCtx, provider, usage.QoderQuota)
		if persistErr == nil && !applied {
			return nil, ErrUsageObservationChanged
		}
		s.applyQoderQuotaSchedulingSignal(fetchCtx, provider, usage.QoderQuota)
		s.cache.StoreQoder(provider.ID, &OAuthQoderUsageCache{Identity: identity, UsageInfo: CloneUsageInfo(usage), Timestamp: s.options.Now()})
		return qoderUsageFlightResult{CloneUsageInfo(usage), identity}, nil
	})
	if flightErr != nil {
		return nil, flightErr
	}
	completed, ok := result.(qoderUsageFlightResult)
	if !ok || completed.Usage == nil {
		return &UsageInfo{UpdatedAt: &now}, nil
	}
	if completed.Identity != identity {
		return nil, ErrUsageObservationChanged
	}
	return CloneUsageInfo(completed.Usage), nil
}

// persistQoderQuotaSnapshot conditionally stores a quota JSON object and UTC
// timestamp for its observed account. The same record supplies the conditional
// write version and the account digest checked when reading. A missing store or
// quota, or a JSON conversion failure, skips persistence with a nil error.
//
// Usable data requires UsageObservationWriter, whose absence returns the
// missing-writer error. A false successful comparison reports identity change
// to the query caller and blocks later scheduling/cache work. Storage errors
// are logged and returned, while the surrounding query still permits those
// later best-effort actions. Keep the quota stable during JSON conversion and
// provide reached Warn.
func (s *OAuthUsageService) persistQoderQuotaSnapshot(ctx context.Context, provider *Record, quota *QoderQuotaInfo) (bool, error) {
	if s == nil || s.providerRepo == nil || quota == nil {
		return true, nil
	}
	// Convert the typed quota to the stored generic JSON shape. Encoding or
	// decoding failure skips persistence without failing the query, so a true
	// result here does not prove that storage accepted an observation.
	raw, err := json.Marshal(quota)
	if err != nil {
		return true, nil
	}
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return true, nil
	}
	snapshot[qoderQuotaAccountKey] = QoderCatalogIdentity(provider)
	version := ObserveUsageVersion(provider)
	writer, ok := s.providerRepo.(UsageObservationWriter)
	if !ok {
		return false, ErrUsageObservationWriterMissing
	}
	applied, err := writer.UpdateUsageExtraIfUnchanged(ctx, version, map[string]any{
		QoderUsageQuotaSnapshotExtraKey:  snapshot,
		QoderUsageQuotaUpdatedAtExtraKey: s.options.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.options.Warn("failed to persist qoder quota snapshot", "provider_id", version.ID, "error", err)
	}
	return applied, err
}

// SavedQoderQuota returns an independently owned display of this account's last
// successful query. An unbound legacy snapshot may belong to a previous account
// on the same provider row, so only a successful query can make it available again.
// The stable identity permits token rotation for a known account and organization.
//
// Persisted Extra contains ordinary JSON objects. Malformed data leaves the quota
// absent. A separate observation timestamp fills an omitted last-update field,
// and the source marker distinguishes these values from a live query.
func (r *Record) SavedQoderQuota() *QoderQuotaInfo {
	if r == nil {
		return nil
	}
	snapshot, _ := r.Extra[QoderUsageQuotaSnapshotExtraKey].(map[string]any)
	identity, _ := snapshot[qoderQuotaAccountKey].(string)
	if identity == "" || identity != QoderCatalogIdentity(r) {
		return nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil
	}
	var quota QoderQuotaInfo
	if err := json.Unmarshal(raw, &quota); err != nil {
		return nil
	}
	if quota.LastUpdatedAt == nil {
		updated, _ := r.Extra[QoderUsageQuotaUpdatedAtExtraKey].(string)
		if parsed, err := time.Parse(time.RFC3339, updated); err == nil {
			quota.LastUpdatedAt = &parsed
		}
	}
	quota.SnapshotFromProvider = true
	return &quota
}

// applyQoderQuotaSchedulingSignal best-effort sets or clears the rate limit
// for a stable record observation. Missing service/store/record skips work,
// and a missing conditional writer is diagnosed.
//
// Future exhausted quota selects its expiry for a rate limit. Otherwise
// clearing requires a live matching quota window without active overload and
// no remaining exhaustion signal. Conditional writer errors are logged,
// comparison results are ignored, and the local record is not updated.
// Snapshot persistence is a separate earlier operation, so these scheduling
// effects can fail independently while display caching continues.
func (s *OAuthUsageService) applyQoderQuotaSchedulingSignal(ctx context.Context, provider *Record, quota *QoderQuotaInfo) {
	if s == nil || s.providerRepo == nil || provider == nil {
		return
	}
	writer, ok := s.providerRepo.(UsageObservationWriter)
	if !ok {
		s.options.Warn("qoder usage conditional writer is missing")
		return
	}
	version := ObserveUsageVersion(provider)
	now := s.options.Now()
	resetAt, ok := QoderQuotaRateLimitResetAt(quota, now)
	if !ok {
		if QoderQuotaShouldClearRateLimit(provider, quota, now) {
			if _, err := writer.ClearUsageRateLimitIfUnchanged(ctx, version); err != nil {
				s.options.Warn("failed to clear qoder quota rate limit", "provider_id", provider.ID, "error", err)
			}
		}
		return
	}
	if _, err := writer.SetUsageRateLimitIfUnchanged(ctx, version, resetAt); err != nil {
		s.options.Warn("failed to apply qoder quota rate limit", "provider_id", provider.ID, "error", err)
	}
}
