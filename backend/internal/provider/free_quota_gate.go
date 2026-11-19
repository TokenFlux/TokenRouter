package provider

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// free_quota_gate.go filters Grok Free OAuth candidates using cached local
// token-budget observations.
// Explicit Grok Free OAuth providers can reach a local token budget before
// upstream requests report exhaustion. Selection uses cached usage to remove
// only eligible providers whose known count reaches the configured fraction
// of that budget. A missing, expired, or failed observation admits the
// current request while a background load supplies later decisions.
//
// Each gate keeps provider-keyed observations and in-flight markers
// separately from the application's task tracker. Refresh admission reserves
// IDs before scheduling work and releases them when the task finishes or is
// rejected. Settings are sampled per check, while cached counts come from the
// window used by their earlier load; changing options does not invalidate
// those counts. A zero cache lifetime makes checks reuse observations of any
// age and makes refreshes started with that setting skip eviction.

// grokFreeQuotaGateCacheMinSweepAge keeps eviction from discarding recent
// observations when the configured cache lifetime is very short.
const grokFreeQuotaGateCacheMinSweepAge = 5 * time.Minute

// FreeQuotaOptions supplies the process-configured token budget and
// observation timing used by a Grok Free OAuth selection gate.
type FreeQuotaOptions struct {
	Enabled     bool  // soft gate enabled flag
	TokenLimit  int64 // tokens
	Percent     int   // whole-token threshold percentage
	WindowHours int   // hours
	// CacheSeconds governs expiry during checks and is captured by each refresh
	// for eviction. Zero reuses entries of any age and skips that refresh's
	// sweep; an earlier refresh can still sweep with its captured nonzero value.
	CacheSeconds int // seconds
}

// FreeQuotaCandidate identifies a selection candidate and its caller-verified
// Free OAuth eligibility.
type FreeQuotaCandidate struct {
	ID       int64 // provider database identifier
	Eligible bool  // caller-verified Free OAuth eligibility
}

// FreeQuotaGate holds provider usage observations and refresh reservations
// for one selection scope. Construct it before concurrent use and retain
// callbacks and dependencies until accepted background tasks finish. Options,
// load, clock, and observer callbacks can run concurrently across checks and
// refreshes, so their captured state needs corresponding synchronization. Keep
// this gate at its original address after first use, as required by its
// sync.Map state.
type FreeQuotaGate struct {
	// cache retains observations across checks. Values are
	// grokFreeQuotaGateCacheEntry copies, and changing options does not remove
	// entries gathered under an earlier usage window.
	cache sync.Map // provider-keyed usage observations
	// inFlight reserves each queried ID before background admission and releases
	// it after completion or rejection, coalescing overlapping batches within
	// this gate.
	inFlight sync.Map // provider-keyed refresh reservations
	// options is called on every Blocked check. Its captured configuration must
	// remain valid and safe to read concurrently with selection.
	options func() FreeQuotaOptions // current process option reader
	// load reads provider counts since the supplied start. Background tasks
	// retain this callback and its dependencies after Blocked has returned; its
	// result map stays stable while refresh reads the returned counts.
	load func(context.Context, []int64, time.Time) (map[int64]int64, error)
	// background owns accepted task execution and completion tracking. A false
	// result means the callback was rejected and will not run.
	background func(string, func()) bool  // application task admission function
	now        func() time.Time           // observation clock callback
	observe    func(bool, string, ...any) // query and budget event callback
	metrics    *FreeQuotaMetrics          // shared atomic observation counters
}

// grokFreeQuotaGateSettings contains validated budget values and duration
// units for one gate check.
type grokFreeQuotaGateSettings struct {
	limitTokens int64         // configured token budget
	gateTokens  int64         // whole-token blocking threshold
	window      time.Duration // usage query lookback
	cacheTTL    time.Duration // observation freshness duration
}

// grokFreeQuotaGateCacheEntry records one successful or unknown provider
// usage observation at its query start.
type grokFreeQuotaGateCacheEntry struct {
	tokens    int64     // nonnegative observed token count
	checkedAt time.Time // background query start instant
	// known distinguishes a successful count, including zero, from a cached read
	// failure. Fresh failures admit providers and suppress additional reads.
	known bool // successful usage-read flag
}

// FreeQuotaMetrics counts completed load failures and over-budget
// observations across gates sharing this instance. Keep the shared counters
// at their original address after first use, as required by their atomic
// state.
type FreeQuotaMetrics struct {
	QueryFailureTotal atomic.Int64 // failed usage batch count
	BlockedTotal      atomic.Int64 // over-threshold provider observation count
}

// NewFreeQuotaGate constructs an empty gate with caller-supplied usage
// loading and background admission. Nil options disable checks, nil load
// prevents refreshes, and nil clock, observer, or metrics receive local
// defaults. Supply background when enabled eligible checks can reach refresh;
// it must run accepted callbacks and return false when rejected. The gate
// retains all callbacks for later checks and tasks.
func NewFreeQuotaGate(options func() FreeQuotaOptions, load func(context.Context, []int64, time.Time) (map[int64]int64, error), background func(string, func()) bool, now func() time.Time, observe func(bool, string, ...any), metrics *FreeQuotaMetrics) *FreeQuotaGate {
	if now == nil {
		now = time.Now
	}
	if metrics == nil {
		metrics = &FreeQuotaMetrics{}
	}
	if observe == nil {
		observe = func(bool, string, ...any) {}
	}
	if options == nil {
		options = func() FreeQuotaOptions { return FreeQuotaOptions{} }
	}
	return &FreeQuotaGate{options: options, load: load, background: background, now: now, observe: observe, metrics: metrics}
}

// resolveGrokFreeQuotaGateSettings validates enabled options and converts
// their time units for evaluation. Invalid budgets, percentages outside 1
// through 100, nonpositive windows, negative cache seconds, or a rounded gate
// of zero disable the check; zero cache seconds keep entries indefinitely.
// Callers supply hours and seconds whose direct time.Duration conversions
// fit.
func resolveGrokFreeQuotaGateSettings(cfg FreeQuotaOptions) (grokFreeQuotaGateSettings, bool) {
	if !cfg.Enabled {
		return grokFreeQuotaGateSettings{}, false
	}
	limit := cfg.TokenLimit
	percent := cfg.Percent
	windowHours := cfg.WindowHours
	cacheSeconds := cfg.CacheSeconds
	if limit <= 0 || percent < 1 || percent > 100 || windowHours <= 0 || cacheSeconds < 0 {
		return grokFreeQuotaGateSettings{}, false
	}
	// Whole-token rounding can erase a small positive percentage. Disable that
	// zero threshold because every successful zero-usage read would meet it.
	gate := calculateGrokFreeQuotaSoftGateTokens(limit, percent)
	if gate <= 0 {
		return grokFreeQuotaGateSettings{}, false
	}
	return grokFreeQuotaGateSettings{
		limitTokens: limit,
		gateTokens:  gate,
		// The lookback uses configured hours in the token-window reader.
		window: time.Duration(windowHours) * time.Hour,
		// Cache freshness compares elapsed duration with configured seconds.
		cacheTTL: time.Duration(cacheSeconds) * time.Second,
	}, true
}

// calculateGrokFreeQuotaSoftGateTokens returns the budget fraction rounded
// down to whole tokens. Nonpositive inputs return zero; the gate's validated
// caller supplies a percentage no greater than 100, keeping the split
// multiplication within the budget.
func calculateGrokFreeQuotaSoftGateTokens(limit int64, percent int) int64 {
	if limit <= 0 || percent <= 0 {
		return 0
	}
	// Split the budget before multiplication so a valid percentage cannot
	// overflow the direct limit-times-percent intermediate, while retaining the
	// remainder's contribution to the whole-token result.
	return (limit/100)*int64(percent) + (limit%100)*int64(percent)/100
}

// IsExplicitGrokFreeOAuthProvider reports whether any credential or Extra
// tier marker names free for a Grok OAuth record. Comparison trims spaces and
// ignores case; nil records, other authentication types, missing markers, and
// inferred tiers return false.
func IsExplicitGrokFreeOAuthProvider(provider *Record) bool {
	if provider == nil || !provider.IsGrokOAuth() {
		return false
	}
	for _, tier := range []string{
		provider.GetCredential("subscription_tier"),
		provider.GetCredential("plan_type"),
		provider.GetExtraString("subscription_tier"),
		provider.GetExtraString("plan_type"),
	} {
		if strings.EqualFold(strings.TrimSpace(tier), "free") {
			return true
		}
	}
	return false
}

// ObserveBlocked reports whether a fresh cached usage entry currently blocks a
// positive provider ID under the active Grok Free OAuth soft-gate settings.
// Missing, stale, or failed entries return false without starting background
// refreshes or incrementing metrics, keeping diagnostic reads side-effect-free.
func (g *FreeQuotaGate) ObserveBlocked(id int64) bool {
	if g == nil || id <= 0 {
		return false
	}
	settings, enabled := resolveGrokFreeQuotaGateSettings(g.options())
	if !enabled || g.load == nil {
		return false
	}
	cached, ok := g.cache.Load(id)
	if !ok {
		return false
	}
	entry, valid := cached.(grokFreeQuotaGateCacheEntry)
	if !valid || !entry.known {
		return false
	}
	age := g.now().UTC().Sub(entry.checkedAt)
	// Reuse the same clock-rollback and TTL freshness rule as Blocked while
	// leaving expired entries untouched for live selection to refresh later.
	fresh := settings.cacheTTL <= 0 || (age >= 0 && age < settings.cacheTTL)
	return fresh && entry.tokens >= settings.gateTokens
}

// Blocked returns an ID map for eligible positive provider IDs whose fresh
// known usage reaches the current token threshold. Nil gates, disabled
// settings, missing loaders, and empty inputs return nil; valid checks return
// a map that may be empty.
//
// Missing and stale entries schedule deduplicated background reads but admit
// this call immediately. Fresh unknown entries also admit requests while
// suppressing another load until expiry. Cached usage is keyed by provider
// ID, so the caller keeps option changes consistent with observations
// collected under earlier windows.
func (g *FreeQuotaGate) Blocked(providers []FreeQuotaCandidate) map[int64]bool {
	if g == nil {
		return nil
	}
	settings, enabled := resolveGrokFreeQuotaGateSettings(g.options())
	if !enabled || len(providers) == 0 || g.load == nil {
		return nil
	}
	now := g.now().UTC()
	tokensByID := make(map[int64]int64)
	missingIDs := make([]int64, 0, len(providers))
	seenMissing := make(map[int64]struct{})
	for i := range providers {
		provider := &providers[i]
		if !provider.Eligible || provider.ID <= 0 {
			continue
		}
		if cached, ok := g.cache.Load(provider.ID); ok {
			entry, valid := cached.(grokFreeQuotaGateCacheEntry)
			if valid {
				age := now.Sub(entry.checkedAt)
				// Zero lifetime intentionally reuses even an unknown failure entry. Timed
				// entries require a nonnegative age so clock rollback triggers another
				// observation rather than trusting a future timestamp.
				fresh := settings.cacheTTL <= 0 || (age >= 0 && age < settings.cacheTTL)
				if fresh {
					if entry.known {
						tokensByID[provider.ID] = entry.tokens
					}
					continue
				}
			}
		}
		// Refresh this ID once per batch while admitting the current request;
		// selection can continue when its local usage observation is unavailable.
		if _, exists := seenMissing[provider.ID]; !exists {
			seenMissing[provider.ID] = struct{}{}
			missingIDs = append(missingIDs, provider.ID)
		}
	}

	if len(missingIDs) > 0 {
		g.refresh(settings, missingIDs)
	}

	blocked := make(map[int64]bool)
	for id, tokens := range tokensByID {
		// Equality consumes the available soft budget too.
		if tokens >= settings.gateTokens {
			blocked[id] = true
		}
	}
	return blocked
}

// refresh reserves currently unclaimed provider IDs and schedules one batch
// usage read for this gate. It captures the check's settings and queries
// counts since the background task's sampled time minus the configured
// window, using a context independent of any selecting request.
//
// Reservation precedes application admission so overlapping calls cannot
// start another load for an already reserved ID. Rejected admission releases
// those markers immediately; accepted work releases them on return. A read
// failure publishes unknown entries, while success publishes nonnegative
// counts and cumulative observations before sweeping older cache entries.
func (g *FreeQuotaGate) refresh(settings grokFreeQuotaGateSettings, providerIDs []int64) {
	// Reserve IDs before handing work to the application so another selecting
	// caller cannot queue the same provider while task admission is pending.
	toFetch := make([]int64, 0, len(providerIDs))
	for _, id := range providerIDs {
		if _, loaded := g.inFlight.LoadOrStore(id, struct{}{}); !loaded {
			toFetch = append(toFetch, id)
		}
	}
	if len(toFetch) == 0 {
		return
	}

	window := settings.window
	gateTokens := settings.gateTokens
	limitTokens := settings.limitTokens
	cacheTTL := settings.cacheTTL
	// Reopen every reserved ID after completion or admission rejection so later
	// stale checks can request a new observation through this gate.
	release := func() {
		for _, id := range toFetch {
			g.inFlight.Delete(id)
		}
	}
	// Accepted tasks finish through the application tracker. The independent
	// context lets a selecting request return without cancelling this shared
	// observation, and deferred release reopens its IDs after the task returns.
	if !g.background("provider/free_quota_gate.go:stats_refresh", func() {
		defer release()
		now := g.now().UTC()
		statsByID, err := g.load(context.Background(), toFetch, now.Add(-window))
		if err != nil {
			g.metrics.QueryFailureTotal.Add(1)
			// Publish unknown observations for the attempted IDs so later selection
			// calls admit them without repeatedly querying a failing usage store until
			// the configured observation lifetime expires.
			for _, providerID := range toFetch {
				g.cache.Store(providerID, grokFreeQuotaGateCacheEntry{checkedAt: now})
			}
			g.observe(true, "grok_free_quota_soft_gate_stats_failed",
				"provider_count", len(toFetch),
				"window_hours", window.Hours(),
				"error", err)
			sweepGrokFreeQuotaGateCache(&g.cache, now, cacheTTL)
			return
		}
		for _, providerID := range toFetch {
			tokens := int64(0)
			// Successful missing or negative counts represent zero local usage.
			if stats := statsByID[providerID]; stats > 0 {
				tokens = stats
			}
			// Publish the query-start time with its count before metrics and sweeping.
			// Storage delay contributes to age, so a slow read can already be expired
			// when later selection inspects this entry.
			g.cache.Store(providerID, grokFreeQuotaGateCacheEntry{tokens: tokens, checkedAt: now, known: true})
			if tokens >= gateTokens {
				g.metrics.BlockedTotal.Add(1)
				g.observe(false, "grok_free_quota_soft_gate_blocked",
					"provider_id", providerID,
					"tokens", tokens,
					"gate_tokens", gateTokens,
					"limit_tokens", limitTokens,
					"window_hours", window.Hours())
			}
		}
		sweepGrokFreeQuotaGateCache(&g.cache, now, cacheTTL)
	}) {
		// Rejected tasks never reach deferred cleanup. Reopen their IDs here so a
		// later selection can retry admission through the application tracker.
		release()
	}
}

// sweepGrokFreeQuotaGateCache removes malformed entries and old observations
// using the larger of cacheTTL times twenty and five minutes. Supply a
// lifetime whose twentyfold duration fits. Nil caches and nonpositive
// lifetimes skip eviction; later eligible checks repopulate removed IDs.
func sweepGrokFreeQuotaGateCache(cache *sync.Map, now time.Time, cacheTTL time.Duration) {
	if cache == nil || cacheTTL <= 0 {
		return
	}
	// Expired observations remain untrusted by Blocked even when cleanup
	// retains their cache entries beyond the configured freshness lifetime.
	maxAge := max(cacheTTL*20, grokFreeQuotaGateCacheMinSweepAge)
	cache.Range(func(key, value any) bool {
		entry, ok := value.(grokFreeQuotaGateCacheEntry)
		if !ok || now.Sub(entry.checkedAt) > maxAge {
			// Age-based eviction also drops departed providers because this cache
			// receives no membership-removal notification from the selection pool.
			cache.Delete(key)
		}
		return true
	})
}
