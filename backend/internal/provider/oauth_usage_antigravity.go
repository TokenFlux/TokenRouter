package provider

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// oauth_usage_antigravity.go fetches and caches Antigravity usage while
// checking the credential identity of shared results and attaching local
// model-family window statistics.
// Antigravity usage queries associate cached results with credential identity
// while sharing in-flight work by provider row ID. A waiter with changed
// credentials can join that work, then rejects its result identity before
// returning a copied display observation. Forced refreshes bypass cached reads
// and populate fresh upstream observations back into the shared cache.
//
// Fresh cache reads copy display values, so countdown recalculation and local
// WindowStats attachment cannot mutate stored data. Direct cache hits use an
// unclamped integer countdown, while the flight-cache path uses
// RecalcAntigravityRemainingSeconds and clamps expired countdowns. New fetches
// run under detached thirty-second activity canceled by service shutdown. Query
// errors become enriched display failures and can be cached with nil returned
// error; context cancellation from shutdown stops that conversion. Callers keep
// the record stable and provide reached fetch, degradation, and enrichment
// callbacks.

// AntigravityModelUsage holds per-model request, token, provider-cost,
// standard-cost, and user-charge totals for one local observation window.
type AntigravityModelUsage struct {
	Model        string  // observed upstream or requested model name
	Requests     int64   // matching usage record count
	TotalTokens  int64   // combined token count
	ProviderCost float64 // provider expense in USD
	StandardCost float64 // standard catalog cost in USD
	UserCost     float64 // user balance charge
}

// AntigravityUsageOptions supplies quota qualification and account-specific
// fetch, degradation, enrichment, and optional local model statistics.
type AntigravityUsageOptions struct {
	// CanFetch optionally qualifies records for quota reads. Missing or false
	// qualification returns an empty dated display before cache or protocol work.
	CanFetch func(*Record) bool
	// Fetch is required for qualified cache misses and receives detached work.
	// Success supplies nonnil display data for future cached reads; keep the
	// borrowed record stable while this callback consumes its credentials.
	Fetch func(context.Context, *Record) (*UsageInfo, error)
	// Degrade is required after fetch failure and supplies nonnil diagnostic
	// display data for cached reads. That enriched result returns with nil error.
	Degrade func(error, time.Time) *UsageInfo
	// Enrich is required after fetch or degradation and may mutate its display.
	// Query identity remains the credential observation captured before shared
	// work starts.
	Enrich func(*UsageInfo, *Record)
	// ModelTotals optionally reads local per-model usage totals over [start, end)
	// to populate Gemini and Claude/GPT WindowStats on summary quota buckets.
	ModelTotals func(context.Context, int64, time.Time, time.Time) ([]AntigravityModelUsage, error)
}

// antigravityUsageFlightResult associates shared quota display data with
// queried credential identity.
type antigravityUsageFlightResult struct {
	// Usage is shared across flight waiters. Each matching consumer copies its
	// current mutable fields before returning independently editable display data.
	Usage    *UsageInfo
	Identity string // queried credential identity digest
}

// antigravityFamilyStats pairs Gemini and non-Gemini local usage totals for one
// queried window start.
type antigravityFamilyStats struct {
	gemini    WindowStats // aggregated totals for Gemini models
	claudeGPT WindowStats // aggregated totals for Claude and GPT models
}

// GetAntigravityUsage returns an independently mutable quota display for a
// stable provider record. Missing or false CanFetch yields an
// observation-time-only result; a qualifying input must be nonnil and requires
// the service cache and reached callbacks. Passing a true force flag bypasses
// cached upstream quota reads while still updating the shared cache on success.
//
// Cache age and identity decide reuse when force is false. Row-keyed flights
// can be joined by different credentials, so their captured identity is checked
// before copying nonnil results. Detached fetch cancellation returns an error;
// other failures may become enriched cached diagnostics with nil error,
// including deadline outcomes. Direct cache countdowns can be negative, while
// flight-cache countdowns clamp at zero. Wrong or nil flight values become an
// observation-time-only view. Supply a nonnil context when a cache miss reaches
// detached activity registration.
func (s *OAuthUsageService) GetAntigravityUsage(ctx context.Context, provider *Record, force ...bool) (*UsageInfo, error) {
	if s.options.Antigravity.CanFetch == nil || !s.options.Antigravity.CanFetch(provider) {
		now := s.options.Now()
		return &UsageInfo{UpdatedAt: &now}, nil
	}

	forceRefresh := len(force) > 0 && force[0]
	identity := UsageCacheIdentity(provider)
	// Identity prevents a cached account observation from crossing a credential
	// change. LoadAntigravity already copied the view before this countdown
	// update; explicit force queries bypass cached upstream observations.
	if !forceRefresh {
		if cache, ok := s.cache.LoadAntigravity(provider.ID); ok && cache.Identity == identity {
			ttl := AntigravityCacheTTL(cache.UsageInfo)
			if s.options.Now().Sub(cache.Timestamp) < ttl {
				usage := cache.UsageInfo
				if usage.FiveHour != nil && usage.FiveHour.ResetsAt != nil {
					// Keep signed truncated seconds here, allowing
					// already elapsed resets to remain visible
					// through this direct cache-hit countdown.
					usage.FiveHour.RemainingSeconds = int(usage.FiveHour.ResetsAt.Sub(s.options.Now()).Seconds())
				}
				s.attachAntigravityWindowStats(ctx, provider.ID, usage, s.options.Now())
				return usage, nil
			}
		}
	}

	// Row-key sharing lets changed credentials join an existing flight. Its
	// completed identity check below rejects that mismatched observation before
	// returning data; forced refreshes use a separate key so they never reuse an
	// unforced flight's cached short-circuit.
	flightKey := fmt.Sprintf("ag-usage:%d", provider.ID)
	if forceRefresh {
		flightKey = fmt.Sprintf("ag-usage-force:%d", provider.ID)
	}
	result, flightErr, _ := s.cache.DoAntigravity(flightKey, func() (any, error) {
		// A completed peer may have filled the cache before this flight begins.
		// Recheck age and identity on unforced requests so the new leader can reuse
		// that copied observation.
		if !forceRefresh {
			if cache, ok := s.cache.LoadAntigravity(provider.ID); ok && cache.Identity == identity {
				ttl := AntigravityCacheTTL(cache.UsageInfo)
				if s.options.Now().Sub(cache.Timestamp) < ttl {
					usage := cache.UsageInfo
					// This flight-cache path clamps expired
					// countdowns through the helper; the direct
					// cache-hit path above instead retains its
					// signed duration conversion.
					RecalcAntigravityRemainingSeconds(usage, s.options.Now)
					return antigravityUsageFlightResult{usage, identity}, nil
				}
			}
		}

		// Detach the shared fetch from caller cancellation while preserving context
		// values. Service shutdown still cancels this registered thirty-second
		// budgeted work.
		fetchCtx, fetchCancel, beginErr := s.BeginDetached(ctx, 30*time.Second)
		if beginErr != nil {
			return nil, beginErr
		}
		defer fetchCancel()

		fetchedUsage, err := s.options.Antigravity.Fetch(fetchCtx, provider)
		if fetchCtx.Err() == context.Canceled {
			return nil, fetchCtx.Err()
		}
		// Fetch failure other than shutdown cancellation becomes display data.
		// Enrichment and copied cache publication retain that diagnostic for later
		// reads while the shared result itself reports nil error.
		if err != nil {
			degraded := s.options.Antigravity.Degrade(err, s.options.Now())
			s.options.Antigravity.Enrich(degraded, provider)
			s.cache.StoreAntigravity(provider.ID, &OAuthAntigravityUsageCache{
				Identity:  identity,
				UsageInfo: CloneUsageInfo(degraded),
				Timestamp: s.options.Now(),
			})
			return antigravityUsageFlightResult{CloneUsageInfo(degraded), identity}, nil
		}

		s.options.Antigravity.Enrich(fetchedUsage, provider)
		s.cache.StoreAntigravity(provider.ID, &OAuthAntigravityUsageCache{
			Identity:  identity,
			UsageInfo: CloneUsageInfo(fetchedUsage),
			Timestamp: s.options.Now(),
		})
		return antigravityUsageFlightResult{CloneUsageInfo(fetchedUsage), identity}, nil
	})

	if flightErr != nil {
		return nil, flightErr
	}
	completed, ok := result.(antigravityUsageFlightResult)
	if !ok || completed.Usage == nil {
		now := s.options.Now()
		return &UsageInfo{UpdatedAt: &now}, nil
	}
	if completed.Identity != identity {
		return nil, ErrUsageObservationChanged
	}
	out := CloneUsageInfo(completed.Usage)
	s.attachAntigravityWindowStats(ctx, provider.ID, out, s.options.Now())
	return out, nil
}

// attachAntigravityWindowStats populates best-effort local WindowStats onto
// Gemini and Claude/GPT weekly and 5-hour summary slots, deduplicating queries
// that share the same reset-aligned window start. Nil summaries, absent
// ModelTotals callbacks, and query errors leave existing quota bars intact.
func (s *OAuthUsageService) attachAntigravityWindowStats(ctx context.Context, providerID int64, usage *UsageInfo, now time.Time) {
	if s == nil || usage == nil || usage.AntigravityQuotaSummary == nil || s.options.Antigravity.ModelTotals == nil || providerID <= 0 {
		return
	}

	byStart := make(map[time.Time]*antigravityFamilyStats, 2)
	loadForStart := func(start time.Time) *antigravityFamilyStats {
		if cached, exists := byStart[start]; exists {
			return cached
		}
		// Local usage query failures leave upstream quota bars intact without
		// aborting the usage display response.
		rows, err := s.options.Antigravity.ModelTotals(ctx, providerID, start, now)
		if err != nil {
			if s.options.Warn != nil {
				s.options.Warn("antigravity_window_stats_query_failed", "provider_id", providerID, "error", err)
			}
			byStart[start] = nil
			return nil
		}
		grouped := &antigravityFamilyStats{}
		for _, row := range rows {
			target := &grouped.claudeGPT
			if strings.Contains(strings.ToLower(row.Model), "gemini") {
				target = &grouped.gemini
			}
			target.Requests += row.Requests
			target.Tokens += row.TotalTokens
			target.Cost += row.ProviderCost
			target.StandardCost += row.StandardCost
			target.UserCost += row.UserCost
		}
		byStart[start] = grouped
		return grouped
	}

	assignSlot := func(slot *AntigravityModelQuota, window time.Duration, geminiFamily bool) {
		if slot == nil {
			return
		}
		start := antigravityWindowStatsStart(slot, window, now)
		grouped := loadForStart(start)
		if grouped == nil {
			return
		}
		if geminiFamily {
			stats := grouped.gemini
			slot.WindowStats = &stats
		} else {
			stats := grouped.claudeGPT
			slot.WindowStats = &stats
		}
	}

	summary := usage.AntigravityQuotaSummary
	if summary.Gemini != nil {
		assignSlot(summary.Gemini.Weekly, 7*24*time.Hour, true)
		assignSlot(summary.Gemini.FiveHour, 5*time.Hour, true)
	}
	if summary.ClaudeGPT != nil {
		assignSlot(summary.ClaudeGPT.Weekly, 7*24*time.Hour, false)
		assignSlot(summary.ClaudeGPT.FiveHour, 5*time.Hour, false)
	}
}

// antigravityWindowStatsStart anchors local usage lookup at reset minus window
// when a future reset timestamp parses cleanly. Missing, malformed, or elapsed
// reset timestamps fall back to now minus window.
func antigravityWindowStatsStart(slot *AntigravityModelQuota, window time.Duration, now time.Time) time.Time {
	if slot != nil {
		if trimmed := strings.TrimSpace(slot.ResetTime); trimmed != "" {
			if resetAt, err := ParseUsageTime(trimmed); err == nil && now.Before(resetAt) {
				return resetAt.Add(-window)
			}
		}
	}
	return now.Add(-window)
}
