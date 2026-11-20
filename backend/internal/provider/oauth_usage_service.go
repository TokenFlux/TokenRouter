package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// oauth_usage_service.go dispatches OAuth usage queries and combines their
// observations with local statistics and recovery.
// OAuth usage combines active upstream observations, stored passive samples,
// local statistics, and conditional error recovery. Anthropic active results
// and failures share a row flight but retain credential identity, while batch
// reads select passive Anthropic data and continue collecting independent
// provider errors.
//
// Tracked entry points each register their own lifecycle; nested dispatch does
// not reuse an outer registration. Anthropic shared work follows its leader
// context, whereas platform helpers can own detached work that survives caller
// cancellation until service shutdown. Active-to-passive persistence writes
// Extra before session end, stopping on a clean comparison miss but continuing
// after a storage error, so partial persistence can remain. Recovery admission
// differs by platform display/error rules. Callers provide reached callbacks
// and Anthropic statistics dependencies, keep borrowed records stable, and
// inspect partial results rather than infer every side effect from nil query
// error.

// ErrOAuthUsageStopped reports newly requested tracked work rejected after
// service shutdown, keeping admission failure distinct from canceled derived
// work.
var ErrOAuthUsageStopped = errors.New("oauth provider usage is stopped")

// OAuthUsageReader loads mutable provider observations for individual and
// batch queries.
type OAuthUsageReader interface {
	// GetByID returns the requested borrowed record or a lookup error.
	// The dispatcher handles a nil successful result as unsupported input, while
	// callers keep mutable fields stable through display and writes.
	GetByID(context.Context, int64) (*Record, error)
	// GetByIDs returns available borrowed rows for the supplied positive IDs.
	// Batch dispatch diagnoses missing IDs, skips nil rows, and lets the last
	// duplicate returned ID replace earlier observations.
	GetByIDs(context.Context, []int64) ([]*Record, error)
}

// OAuthUsageOptions supplies platform callbacks, timing, diagnostics, and
// OpenAI pause observation.
type OAuthUsageOptions struct {
	// Now supplies observation time and countdown decisions. Constructor defaults
	// it to time.Now; callbacks remain stable across synchronous and detached
	// work.
	Now func() time.Time
	// Jitter receives the query-delay bound in nanoseconds and returns
	// nanoseconds. Constructor defaults to zero; supplied results are used without
	// clamping before Anthropic flight admission.
	Jitter func(int64) int64
	// Log reports parse/statistics/recovery diagnostics on reached paths.
	// Constructor defaults to a no-op, allowing those failures to retain available
	// display observations.
	Log func(string, ...any)
	// Warn diagnoses omitted or failed conditional writes and platform failures.
	// Constructor defaults to a no-op; diagnostics do not imply rollback of
	// earlier accepted persistence.
	Warn func(string, ...any)
	// OpenAI retains optional regular/shadow probe callbacks. Their own methods
	// define cancellation, local merge, admission age, and detached
	// conditional-write behavior.
	OpenAI OpenAIUsageOptions
	// Gemini retains local quota dependencies required by reached Gemini display.
	// Configure those callbacks before querying and keep returned
	// settings/observations stable during use.
	Gemini GeminiUsageOptions
	// Antigravity retains model/subscription query dependencies for its platform
	// path. Keep configured callbacks and their borrowed response values stable
	// during shared observation work.
	Antigravity AntigravityUsageOptions
	// Grok retains stored/active billing and quota dependencies for its platform
	// path. Its methods decide authoritative observations, probe timing, and
	// partial statistic behavior.
	Grok GrokUsageOptions
	// Qoder retains fetch and conditional quota dependencies for its shared
	// platform path. Configure reached operations before use and retain them
	// through detached completion.
	Qoder QoderUsageOptions
	// Anthropic is required on active cache misses and receives the leader
	// context. Return nonnil typed data on success and keep its nested observation
	// stable through copying.
	Anthropic func(context.Context, *Record) (*ClaudeUsageResponse, error)
	// OpenAIQuotaPause is required after nil-error OpenAI OAuth display. It
	// observes or mutates quota pause display before conditional credential-error
	// recovery, including when probes were suppressed.
	OpenAIQuotaPause func(context.Context, *Record, *UsageInfo)
}

// OAuthUsageService owns platform dispatch, shared queries, partial batches,
// and shutdown tracking.
type OAuthUsageService struct {
	// providerRepo is required for ID lookup and optionally implements conditional
	// writers. Returned records remain borrowed, and absent reached writers
	// produce synchronization/recovery diagnostics.
	providerRepo OAuthUsageReader
	// cache retains copied observations and row-keyed flights/admission times.
	// Constructor allocates when nil; callers keep it usable until all tracked
	// work finishes.
	cache *OAuthUsageCache
	// stats is required by active Anthropic, remaining setup-token fallback, and
	// passive display. Supply its cache/repository dependencies; other platform
	// getters can separately permit missing statistics.
	stats *LocalUsageStatistics
	// options retains configured callbacks and shared dependencies across admitted
	// work. Keep it stable throughout query callbacks, conditional persistence,
	// and service shutdown.
	options OAuthUsageOptions
	// activity tracks every public/nested admission separately and retains
	// shutdown outcome. Keep this instance stable and do not copy service state
	// after use.
	activity operationActivity
}

// oauthAPIFlightResult binds shared Anthropic data or failure to credential
// identity.
type oauthAPIFlightResult struct {
	// Response, Err, and Identity describe one shared Anthropic outcome. Identity
	// is compared before either successful data or stored failure reaches the
	// caller, while the enclosing flight can itself return nil error.
	// Response retains the shared successful typed observation for display
	// copying. A nil successful upstream callback is unusable on the active
	// display path.
	Response *ClaudeUsageResponse // successful upstream quota observation
	// Err remains the same diagnostic object in the flight and negative cache.
	// Callers treat it as immutable while queries share or retain that failure.
	Err      error  // shared active-query failure result
	Identity string // observed credential cache identity
}

// UsageSessionWindowWriter conditionally persists the session reset after
// Extra observation writes.
type UsageSessionWindowWriter interface {
	// UpdateUsageSessionWindowEndIfUnchanged compares observed identity and prior
	// session end before persisting the supplied reset. False means a comparison
	// miss; SyncActiveToPassive ignores that result while reporting errors after
	// its preceding Extra attempt.
	UpdateUsageSessionWindowEndIfUnchanged(context.Context, UsageObservationVersion, *time.Time, time.Time) (bool, error)
}

// NewOAuthUsageService retains dependencies and creates a cache when none is
// supplied. Clock defaults to time.Now, jitter to zero, and Log/Warn to no-ops
// without invoking provider callbacks; configure other reached callbacks and
// statistics, then keep this instance/dependencies stable through tracked
// work.
func NewOAuthUsageService(reader OAuthUsageReader, cache *OAuthUsageCache, stats *LocalUsageStatistics, options OAuthUsageOptions) *OAuthUsageService {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Jitter == nil {
		options.Jitter = func(int64) int64 { return 0 }
	}
	if options.Log == nil {
		options.Log = func(string, ...any) {}
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	if cache == nil {
		cache = NewOAuthUsageCache()
	}
	return &OAuthUsageService{providerRepo: reader, cache: cache, stats: stats, options: options}
}

// StopContext rejects new tracked usage work, cancels admitted operations, and
// waits within ctx. Nil service succeeds; the first stop result, including
// unfinished-work timeout, remains retained, while untracked direct
// synchronization depends on its caller.
// Pass a nonnil context when registered work or shutdown waiting is reached.
func (s *OAuthUsageService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.activity.stop(ctx, "OAuth provider usage")
}

// BeginDetached registers budgeted work independently of caller cancellation
// while retaining parent context values. Supply a nonnil parent and invoke
// returned finish exactly once; nonpositive budget cancels immediately.
//
// Shutdown registration occurs before the timeout child is created, allowing
// stop to own admitted work until explicit finish cancels and unregisters it.
// If shutdown wins admission, ErrOAuthUsageStopped is translated to
// context.Canceled for derived work. The budget bounds cooperative
// cancellation rather than forcing callback return, so callers retain
// dependencies until actual completion and stop may still report unfinished
// ownership.
func (s *OAuthUsageService) BeginDetached(parent context.Context, budget time.Duration) (context.Context, func(), error) {
	// Register before creating the timeout child so shutdown owns admitted work
	// until explicit finish, even after caller cancellation is removed from the
	// retained parent values used by this derived operation.
	ctx, finish, err := s.activity.begin(context.WithoutCancel(parent), ErrOAuthUsageStopped)
	// Translate rejected derived-work admission into cancellation so platform
	// queries distinguish interrupted follow-up from a newly requested public
	// operation rejected after shutdown. No work is dispatched on this failure.
	if err == ErrOAuthUsageStopped {
		return nil, nil, context.Canceled
	}
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	return ctx, func() { cancel(); finish() }, nil
}

// SupportsAnthropicPassiveUsage reports whether a nonnil record is Anthropic
// OAuth or setup-token.
func SupportsAnthropicPassiveUsage(provider *Record) bool {
	return provider != nil && provider.IsAnthropicOAuthOrSetupToken()
}

// batchUsageErrorMessage returns an error's message or empty text for nil.
func batchUsageErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// GetUsageForProvider dispatches a stable nonnil caller-owned record under its
// own lifecycle registration. Reached platform callbacks and OpenAIQuotaPause
// must remain usable, while active Anthropic, remaining setup-token fallback,
// and passive display need valid local statistics.
//
// Anthropic active misses use cancelable supplied jitter then a row flight
// driven by its leader context. Cached and shared outcomes retain credential
// identity; a different completed identity is rejected before display or
// synchronization, while waiting callers cannot independently interrupt the
// synchronous flight wait. Successful API data updates passive storage
// best-effort and can use a passive Fable fallback.
//
// OpenAI, Gemini, and Antigravity nil-error observations request
// credential-error recovery. Grok additionally requires a nonnil view with
// empty Error; Qoder requires nil view or empty Error and ErrorCode. Those
// differing display gates do not establish a universal healthy-account
// guarantee. Setup-token fallback estimates session usage, and unsupported
// remaining types return an error. Returned display and mutable record side
// effects remain owned by the caller.
// Pass a nonnil context when registered work or shutdown waiting is reached.
func (s *OAuthUsageService) GetUsageForProvider(ctx context.Context, provider *Record, forceProbe bool) (*UsageInfo, error) {
	// Register before lookup or dispatch so shutdown owns this admitted call
	// through deferred completion. Nested helpers register separately and can be
	// rejected if stop begins before their own admission.
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	if provider == nil {
		return nil, fmt.Errorf("provider is required")
	}
	providerID := provider.ID
	identity := UsageCacheIdentity(provider)

	if provider.Platform == PlatformOpenAI && provider.Type == ProviderTypeOAuth {
		usage, err := s.GetOpenAIUsage(ctx, provider, forceProbe)
		if err == nil {
			s.options.OpenAIQuotaPause(ctx, provider, usage)
			s.recoverProviderError(ctx, provider)
		}
		return usage, err
	}

	if provider.Platform == PlatformGemini {
		usage, err := s.GetGeminiUsage(ctx, provider)
		if err == nil {
			s.recoverProviderError(ctx, provider)
		}
		return usage, err
	}

	if provider.Platform == PlatformAntigravity {
		usage, err := s.GetAntigravityUsage(ctx, provider, forceProbe)
		if err == nil {
			s.recoverProviderError(ctx, provider)
		}
		return usage, err
	}

	if provider.Platform == PlatformGrok {
		usage, err := s.GetGrokUsage(ctx, provider, forceProbe)
		if err == nil && usage != nil && usage.Error == "" {
			s.recoverProviderError(ctx, provider)
		}
		return usage, err
	}

	if provider.Platform == PlatformQoder {
		usage, err := s.GetQoderUsage(ctx, provider, forceProbe)
		if err == nil && (usage == nil || (usage.Error == "" && usage.ErrorCode == "")) {
			s.recoverProviderError(ctx, provider)
		}
		return usage, err
	}

	// CanGetUsage checks OAuth type only after the explicit platform branches.
	// Remaining OAuth rows reach the configured Anthropic callback without a
	// platform or profile-scope check here; callers supply applicable credentials.
	if provider.CanGetUsage() {
		var apiResp *ClaudeUsageResponse

		// Match cached identity before applying separate success/error lifetimes so a
		// stored failure from replaced credentials cannot become the new caller's
		// cached observation. Returned typed data is copied by the cache.
		if cache, ok := s.cache.LoadAPI(providerID); ok && cache.Identity == identity {
			age := s.options.Now().Sub(cache.Timestamp)
			if cache.Err != nil && age < OAuthUsageAPIErrorCacheTTL {
				return nil, cache.Err
			}
			if cache.Response != nil && age < OAuthUsageAPICacheTTL {
				apiResp = cache.Response
			}
		}

		// Share row-keyed active misses, retaining completed credential identity for
		// later verification. The leader's synchronous callback drives its shared
		// result, and waiters remain blocked until that flight completes.
		if apiResp == nil {
			// Apply supplied jitter before row-flight admission and
			// allow caller/service cancellation to interrupt this
			// delay. The callback receives a nanosecond bound; its
			// returned duration is used directly without range
			// clamping.
			jitter := time.Duration(s.options.Jitter(int64(OAuthUsageAPIQueryMaxJitter)))
			select {
			case <-time.After(jitter):
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			flightKey := fmt.Sprintf("usage:%d", providerID)
			result, flightErr, _ := s.cache.DoAPI(flightKey, func() (any, error) {
				// Recheck identity and age inside the new
				// flight because an earlier flight can populate
				// the cache during jitter/admission. This
				// avoids another fetch when a still-valid
				// observation already completed for these
				// credentials.
				if cache, ok := s.cache.LoadAPI(providerID); ok && cache.Identity == identity {
					age := s.options.Now().Sub(cache.Timestamp)
					if cache.Err != nil && age < OAuthUsageAPIErrorCacheTTL {
						return oauthAPIFlightResult{Err: cache.Err, Identity: identity}, nil
					}
					if cache.Response != nil && age < OAuthUsageAPICacheTTL {
						return oauthAPIFlightResult{Response: cache.Response, Identity: identity}, nil
					}
				}
				resp, fetchErr := s.options.Anthropic(ctx, provider)
				if fetchErr != nil {
					// Cache the failed attempt with its
					// observed identity, limiting
					// sequential retry pressure without
					// letting replaced credentials reuse
					// this failure. The shared result
					// carries that identity even while its
					// method error is nil.
					s.cache.StoreAPI(providerID, &OAuthAPIUsageCache{
						Identity:  identity,
						Err:       fetchErr,
						Timestamp: s.options.Now(),
					})
					return oauthAPIFlightResult{Err: fetchErr, Identity: identity}, nil
				}
				s.cache.StoreAPI(providerID, &OAuthAPIUsageCache{
					Identity:  identity,
					Response:  resp,
					Timestamp: s.options.Now(),
				})
				return oauthAPIFlightResult{Response: clonePointer(resp), Identity: identity}, nil
			})
			if flightErr != nil {
				return nil, flightErr
			}
			// Reject incompatible shared results before constructing display or writing
			// passive state, so a flight led by other credentials cannot update this
			// caller's observed identity with its successful or failed response.
			completed, ok := result.(oauthAPIFlightResult)
			if !ok || completed.Identity != identity {
				return nil, ErrUsageObservationChanged
			}
			if completed.Err != nil {
				return nil, completed.Err
			}
			apiResp = completed.Response
		}

		// Rebuild countdowns from copied API data at the current display time.
		// Invalid reset text can retain sampled utilization, while local statistics
		// load independently through their separate cache.
		now := s.options.Now()
		usage := BuildUsageInfo(apiResp, &now, s.options.Now, s.options.Log)

		// Local window statistics have a separate one-minute cache.
		s.stats.AddWindowStats(ctx, provider, usage)

		// Attempt passive writes before error recovery changes the local status used
		// in observed identity. Write diagnostics do not reject the active display,
		// and partial persistence remains possible between Extra and session end.
		s.SyncActiveToPassive(ctx, provider, usage)

		// Use the independently sampled 7d_oi window when active data omits Fable.
		// This fallback occurs after synchronization, so the new view of stored
		// header data is displayed without being written back in that attempt.
		if usage.SevenDayFable == nil {
			usage.SevenDayFable = BuildPassiveUsageWindow(provider.Extra, "passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset", s.options.Now)
		}

		s.recoverProviderError(ctx, provider)
		return usage, nil
	}

	// Estimate setup-token display from stored session data because this remaining
	// path cannot issue the active profile usage query. Local totals still use
	// the separate statistics dependency and cache.
	if provider.Type == ProviderTypeSetupToken {
		usage := EstimateSetupTokenUsage(provider, s.options.Now)
		s.stats.AddWindowStats(ctx, provider, usage)
		return usage, nil
	}

	// Remaining provider types do not support this usage query.
	return nil, fmt.Errorf("provider type %s does not support usage query", provider.Type)
}

// GetUsage loads one borrowed provider and dispatches its query under
// caller/shutdown registration. Reader is required; only the first optional
// force flag is used, with force effect delegated to supported platform paths
// and nested dispatch registered independently.
// Pass a nonnil context when registered work or shutdown waiting is reached.
func (s *OAuthUsageService) GetUsage(ctx context.Context, providerID int64, force ...bool) (*UsageInfo, error) {
	// Register before lookup or dispatch so shutdown owns this admitted call
	// through deferred completion. Nested helpers register separately and can be
	// rejected if stop begins before their own admission.
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	forceProbe := len(force) > 0 && force[0]

	provider, err := s.providerRepo.GetByID(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("get provider failed: %w", err)
	}

	return s.GetUsageForProvider(ctx, provider, forceProbe)
}

// GetUsageBatch queries unique positive IDs with at most six concurrent
// workers. Empty input returns empty result/error maps without a read;
// required reader supplies stable borrowed records, and missing rows receive
// per-ID errors.
//
// Anthropic OAuth/setup-token rows use passive display while other rows
// delegate platform queries with force. Each worker records its own failure
// and returns nil to the errgroup, so individual failures do not cancel peers
// or become a batch error. One mutex protects shared result/error maps,
// including missing-row writes during dispatch. Initial lookup or lifecycle
// failure remains a batch error; caller cancellation can instead be recorded
// in worker errors after lookup. Duplicate returned row IDs use the last
// record in the reader result.
// Pass a nonnil context when registered work or shutdown waiting is reached.
func (s *OAuthUsageService) GetUsageBatch(ctx context.Context, providerIDs []int64, force bool) (map[int64]*UsageInfo, map[int64]string, error) {
	// Register before lookup or dispatch so shutdown owns this admitted call
	// through deferred completion. Nested helpers register separately and can be
	// rejected if stop begins before their own admission.
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, nil, err
	}
	defer finish()

	uniqueIDs := make([]int64, 0, len(providerIDs))
	seen := make(map[int64]struct{}, len(providerIDs))
	for _, providerID := range providerIDs {
		if providerID <= 0 {
			continue
		}
		if _, ok := seen[providerID]; ok {
			continue
		}
		seen[providerID] = struct{}{}
		uniqueIDs = append(uniqueIDs, providerID)
	}

	usageByProvider := make(map[int64]*UsageInfo, len(uniqueIDs))
	errorsByProvider := make(map[int64]string)
	if len(uniqueIDs) == 0 {
		return usageByProvider, errorsByProvider, nil
	}

	providers, err := s.providerRepo.GetByIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("get providers failed: %w", err)
	}

	providersByID := make(map[int64]*Record, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		providersByID[provider.ID] = provider
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	// Bound simultaneous provider work at six while workers retain independent
	// result errors. They return nil to errgroup, so a failed provider does not
	// cancel another provider's query through the group error mechanism.
	g.SetLimit(6)

	// Record missing rows under the same mutex used by already dispatched
	// workers, preventing a serial-looking dispatch branch from racing concurrent
	// error publication while later providers are admitted.
	for _, providerID := range uniqueIDs {
		id := providerID
		provider := providersByID[id]
		if provider == nil {
			mu.Lock()
			errorsByProvider[id] = ErrProviderNotFound.Error()
			mu.Unlock()
			continue
		}

		// Publish each result under the shared map mutex; provider errors remain
		// keyed data rather than worker errors. This keeps missing-row writes from
		// racing already running workers during the same dispatch loop.
		g.Go(func() error {
			var usage *UsageInfo
			var usageErr error
			if SupportsAnthropicPassiveUsage(provider) {
				usage, usageErr = s.GetPassiveUsageForProvider(gctx, provider)
			} else {
				usage, usageErr = s.GetUsageForProvider(gctx, provider, force)
			}

			mu.Lock()
			defer mu.Unlock()
			if usageErr != nil {
				errorsByProvider[id] = batchUsageErrorMessage(usageErr)
				return nil
			}
			usageByProvider[id] = usage
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	return usageByProvider, errorsByProvider, nil
}

// GetPassiveUsage loads one row and delegates stored Anthropic display under
// caller/shutdown tracking. Reader and reached statistics are required;
// unsupported or nil records fail in the nested passive admission check, whose
// lifecycle registration remains separate.
// Pass a nonnil context when registered work or shutdown waiting is reached.
func (s *OAuthUsageService) GetPassiveUsage(ctx context.Context, providerID int64) (*UsageInfo, error) {
	// Register before lookup or dispatch so shutdown owns this admitted call
	// through deferred completion. Nested helpers register separately and can be
	// rejected if stop begins before their own admission.
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	provider, err := s.providerRepo.GetByID(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("get provider failed: %w", err)
	}

	return s.GetPassiveUsageForProvider(ctx, provider)
}

// GetPassiveUsageForProvider builds stored Anthropic OAuth/setup-token display
// under caller/shutdown registration. The borrowed record must remain stable,
// and usable local statistics are required when the resulting windows trigger
// totals.
//
// FiveHour comes from session state for both credential types. UpdatedAt
// exists only for a string sampled timestamp that parses as RFC 3339; missing
// or malformed text leaves it nil rather than supplying query time. Weekly and
// Fable use independent fractional/Unix-second samples, with expired
// countdowns clamped without clearing their utilization. Local totals attach
// afterward, and this passive path does not perform active credential-error
// recovery.
// Pass a nonnil context when registered work or shutdown waiting is reached.
func (s *OAuthUsageService) GetPassiveUsageForProvider(ctx context.Context, provider *Record) (*UsageInfo, error) {
	// Register before lookup or dispatch so shutdown owns this admitted call
	// through deferred completion. Nested helpers register separately and can be
	// rejected if stop begins before their own admission.
	ctx, finish, err := s.activity.begin(ctx, ErrOAuthUsageStopped)
	if err != nil {
		return nil, err
	}
	defer finish()

	if !SupportsAnthropicPassiveUsage(provider) {
		return nil, fmt.Errorf("passive usage only supported for Anthropic OAuth/SetupToken providers")
	}

	// Derive both credential types' short display from session state, preserving
	// its expiry-clearing rule independently from sampled weekly windows below.
	// No active query is issued to repair absent stored usage here.
	info := EstimateSetupTokenUsage(provider, s.options.Now)
	info.Source = "passive"

	// Retain only usable string sample timing; malformed or differently typed
	// metadata leaves UpdatedAt absent. Suppressing that parse failure keeps
	// otherwise usable session and weekly display available.
	if raw, ok := provider.Extra["passive_usage_sampled_at"]; ok {
		if str, ok := raw.(string); ok {
			if t, err := time.Parse(time.RFC3339, str); err == nil {
				info.UpdatedAt = &t
			}
		}
	}

	info.SevenDay = BuildPassiveUsageWindow(provider.Extra, "passive_usage_7d_utilization", "passive_usage_7d_reset", s.options.Now)

	// Read the separate 7d_oi sample into Fable so it remains distinguishable from
	// general weekly usage. Missing sample observations keep that specialized
	// window absent while the rest of passive display remains usable.
	info.SevenDayFable = BuildPassiveUsageWindow(provider.Extra, "passive_usage_7d_oi_utilization", "passive_usage_7d_oi_reset", s.options.Now)

	s.stats.AddWindowStats(ctx, provider, info)

	return info, nil
}

// SyncActiveToPassive synchronously writes sampled fractions and session reset
// for a stable observed record/view. Nil inputs skip work; reached conditional
// writers and Warn are required, and direct calls rely on caller
// context/lifetime rather than service registration.
//
// Extra stores percentages divided by one hundred and Unix-second resets
// before a separate session-end write. Missing Extra writer or clean
// conditional miss stops the sequence; storage error is diagnosed but still
// permits the later session write. That second write uses the same captured
// identity and borrowed previous end, ignoring its comparison result while
// diagnosing errors. No local record update or rollback reconciles partial
// writes, so callers synchronize mutable inputs and allow later reads to
// observe accepted persistence independently of this display.
func (s *OAuthUsageService) SyncActiveToPassive(ctx context.Context, observed *Record, usage *UsageInfo) {
	if observed == nil || usage == nil {
		return
	}
	providerID := observed.ID
	version := ObserveUsageVersion(observed)
	extraUpdates := make(map[string]any, 4)

	// Convert active percentages to passive fractions without clamping sampled
	// values. The scalar patch retains separate weekly/Fable Unix-second timing
	// before conditional Extra persistence and later session reset
	// synchronization.
	if usage.FiveHour != nil {
		extraUpdates["session_window_utilization"] = usage.FiveHour.Utilization / 100
	}
	if usage.SevenDay != nil {
		extraUpdates["passive_usage_7d_utilization"] = usage.SevenDay.Utilization / 100
		if usage.SevenDay.ResetsAt != nil {
			extraUpdates["passive_usage_7d_reset"] = usage.SevenDay.ResetsAt.Unix()
		}
	}
	if usage.SevenDayFable != nil {
		extraUpdates["passive_usage_7d_oi_utilization"] = usage.SevenDayFable.Utilization / 100
		if usage.SevenDayFable.ResetsAt != nil {
			extraUpdates["passive_usage_7d_oi_reset"] = usage.SevenDayFable.ResetsAt.Unix()
		}
	}

	if len(extraUpdates) > 0 {
		extraUpdates["passive_usage_sampled_at"] = s.options.Now().UTC().Format(time.RFC3339)
		writer, ok := s.providerRepo.(UsageExtraWriter)
		if !ok {
			s.options.Warn("sync_active_to_passive_failed", "provider_id", providerID, "error", ErrUsageObservationWriterMissing)
			return
		}
		// Attempt Extra first using captured identity. A clean miss stops session
		// persistence, while an error only emits diagnostics and permits the next
		// stage, leaving accepted partial state to later readers.
		applied, err := writer.UpdateUsageExtraIfUnchanged(ctx, version, extraUpdates)
		if err != nil {
			s.options.Warn("sync_active_to_passive_failed", "provider_id", providerID, "error", err)
		} else if !applied {
			return
		}
	}

	// Write session end separately because setup/passive FiveHour reads that field
	// instead of an Extra reset. Failure leaves any accepted fraction write in
	// place, and a comparison miss does not trigger retry or rollback here.
	if usage.FiveHour != nil && usage.FiveHour.ResetsAt != nil {
		writer, ok := s.providerRepo.(UsageSessionWindowWriter)
		if !ok {
			s.options.Warn("sync_active_to_passive_session_window_end_failed", "provider_id", providerID, "error", ErrUsageObservationWriterMissing)
			return
		}
		if _, err := writer.UpdateUsageSessionWindowEndIfUnchanged(ctx, version, observed.SessionWindowEnd, *usage.FiveHour.ResetsAt); err != nil {
			s.options.Warn("sync_active_to_passive_session_window_end_failed", "provider_id", providerID, "error", err)
		}
	}
}

// recoverProviderError delegates conditional recovery of a stable caller-owned
// credential error. Missing eligible writer or persistence failure is logged
// without rejecting display; only accepted recovery changes local
// status/error, and the caller selects its platform health gate and
// synchronizes shared mutation.
func (s *OAuthUsageService) recoverProviderError(ctx context.Context, value *Record) {
	writer, _ := s.providerRepo.(UsageRecoveryWriter)
	// Let conditional recovery decide whether the exact observed error still
	// belongs to these credentials. Diagnose failure without changing successful
	// display, and apply local active status only after accepted persistence.
	applied, err := RecoverUsageProviderError(ctx, value, writer)
	if err != nil {
		s.options.Log("[usage] failed to clear recoverable provider error for provider %d: %v", value.ID, err)
		return
	}
	if applied {
		value.Status = StatusActive
		value.ErrorMessage = ""
	}
}
