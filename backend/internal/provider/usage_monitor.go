package provider

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// usage_monitor.go polls Kimi, Zhipu, and DeepSeek provider usage and writes
// identity-checked balance decisions and observation snapshots. upstream usage
// monitoring records failed attempts alongside the last successful balance or
// Coding Plan observation. Each result belongs to a fingerprint of the
// provider's query identity, and snapshot writes compare the reread row version
// so delayed polls do not overwrite later configuration changes.
//
// Periodic polling waits for the first full interval and requests singleton
// ownership through the configured lease backends. Each round bounds concurrent
// upstream queries and separates balance decisions from snapshot persistence,
// rereading after health changes. Shutdown cancels tracked loops and rounds,
// then waits for actual completion; a dependency that ignores cancellation can
// leave a retained timeout result. Manual RunOnce calls share this completion
// tracking.

// UsageMonitorSnapshotVersion, usageMonitorLeaderLockKey, and
// usageMonitorReasonPrefix identify stored observations, shared round
// ownership, and identity-labelled balance pauses. The lock key and reason
// prefix retain their stored spelling so mixed-version instances coordinate the
// same rounds and recognize existing pauses.
const (
	UsageMonitorSnapshotVersion = 1                         // accepted monitor snapshot format version
	usageMonitorLeaderLockKey   = "cn:usage:monitor:leader" // shared periodic round lease key
	usageMonitorReasonPrefix    = "cn_usage_monitor:"       // prefix for fingerprint balance reasons
)

// ErrUsageMonitorStopped rejects newly registered rounds after the monitor
// activity has stopped. StartContext also keeps a stopped monitor from
// launching its loop.
var ErrUsageMonitorStopped = errors.New("upstream usage monitor is stopped")

// UsageMonitorStore supplies current provider records and conditional monitor
// snapshot and health writes.
type UsageMonitorStore interface {
	// GetByID reads the current provider version used for identity
	// rechecks.
	GetByID(context.Context, int64) (*Record, error)
	// ListByPlatform returns records for one Kimi, Zhipu, or DeepSeek platform
	// before monitor eligibility filtering.
	ListByPlatform(context.Context, string) ([]Record, error)
	// UpdateUsageMonitorSnapshotCAS writes a snapshot when UpdatedAt matches
	// the supplied version. Its final string optionally names an Extra key to
	// clear; false identifies a stale or missing row.
	UpdateUsageMonitorSnapshotCAS(context.Context, int64, time.Time, *UsageMonitorSnapshot, string) (bool, error)
	// SetUsageMonitorDecisionCAS pauses or clears a matching UpdatedAt version.
	// A clear requires the stored reason to match, and an extension requires a
	// later deadline; false reports no matched write.
	SetUsageMonitorDecisionCAS(context.Context, int64, time.Time, time.Time, string, bool) (bool, error)
}

// UsageMonitorQueries supplies normalized upstream observations for one
// persisted provider identity.
type UsageMonitorQueries interface {
	// QueryProvider queries normalized upstream usage by provider ID. The
	// monitor supplies a bounded context and rereads the provider
	// afterward, so
	// observations are conditionally persisted against the identity that
	// was
	// queried.
	QueryProvider(context.Context, int64) (*UpstreamUsageQueryResult, error)
}

// UsageMonitorOptions configures probe budgets, host admission, round
// ownership, and diagnostic callbacks.
type UsageMonitorOptions struct {
	Enabled bool // permits periodic loop startup
	// Interval, ProbeTimeout, and RoundTimeout are replaced when nonpositive by
	// NewUsageMonitor. They independently bound tick spacing, each upstream
	// query, and the complete leased round.
	Interval, ProbeTimeout, RoundTimeout time.Duration // Interval tick spacing; ProbeTimeout query budget; RoundTimeout round budget
	Concurrency                          int           // maximum concurrent round probes
	BalanceThreshold                     float64       // minimum usable reported balance
	// HostPolicy admits official and configured custom query hosts; the
	// constructor clones it before background work can read its lists.
	HostPolicy egress.MonitorHostPolicy
	Now        func() time.Time // snapshot and cooldown clock
	InstanceID string           // shared leader lease owner
	// Leader optionally supplies the first lease backend; contention skips
	// the
	// round and errors permit the Advisory backend to run.
	Leader LeaderLocker
	// Advisory optionally acquires database ownership after absent or
	// failed
	// Leader acquisition; its release function is called when the admitted
	// round finishes.
	Advisory func(context.Context, string) (func(), bool)
	// Warn and Debug receive monitor diagnostics and default to no-op
	// callbacks
	// at construction. Warn records failures; Debug reports a snapshot
	// comparison miss.
	// Warn receives failures; Debug receives snapshot comparison misses.
	Warn, Debug func(string, ...any)
}

// UsageMonitor owns periodic polling and tracked manual rounds for Kimi, Zhipu,
// and DeepSeek provider usage.
type UsageMonitor struct {
	providerRepo                         UsageMonitorStore   // provider and conditional-write storage
	usageService                         UsageMonitorQueries // normalized upstream query dependency
	options                              UsageMonitorOptions // normalized callbacks and admission options
	interval, probeTimeout, roundTimeout time.Duration       // interval tick spacing; probeTimeout query budget; roundTimeout round budget
	concurrency                          int                 // active probe worker limit
	mu                                   sync.Mutex          // start and stop state lock
	// started and stopped are accessed under mu. Startup sets started once;
	// StopContext sets stopped permanently before activity cancellation so
	// later startup cannot launch another loop.
	// started records loop admission; stopped rejects later loop startup.
	started, stopped bool
	// cancel records the successfully started loop context for startup
	// inspection; StopContext cancels registered work through activity
	// rather
	// than invoking this field.
	cancel context.CancelFunc
	// activity tracks the loop and each RunOnce call through actual
	// completion,
	// rejecting new rounds and cancelling existing contexts after shutdown
	// starts.
	activity operationActivity
}

// NewUsageMonitor constructs an idle monitor with a cloned host policy.
// Nonpositive timing and concurrency select ten-minute intervals, twenty-second
// probes, five-minute rounds, and four workers; missing clock and log callbacks
// receive defaults.
func NewUsageMonitor(store UsageMonitorStore, queries UsageMonitorQueries, options UsageMonitorOptions) *UsageMonitor {
	if options.Interval <= 0 {
		options.Interval = 10 * time.Minute
	}
	if options.ProbeTimeout <= 0 {
		options.ProbeTimeout = 20 * time.Second
	}
	if options.RoundTimeout <= 0 {
		options.RoundTimeout = 5 * time.Minute
	}
	if options.Concurrency <= 0 {
		options.Concurrency = 4
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	if options.Debug == nil {
		options.Debug = func(string, ...any) {}
	}
	options.HostPolicy = options.HostPolicy.Clone()
	return &UsageMonitor{providerRepo: store, usageService: queries, options: options, interval: options.Interval, probeTimeout: options.ProbeTimeout, roundTimeout: options.RoundTimeout, concurrency: options.Concurrency}
}

// StartContext launches one polling loop when the monitor is enabled and both
// store and query dependencies exist. Nil, disabled, already-started, or
// stopped monitors return nil without launching work. Callers supply a nonnil
// parent context.
//
// Startup flags are serialized with StopContext and the loop registers with
// activity before publication. The first round waits for a full interval.
// Parent or shutdown cancellation ends the loop, while its deferred finish
// keeps shutdown waiting until the loop has actually exited.
func (s *UsageMonitor) StartContext(parent context.Context) error {
	if s == nil || s.providerRepo == nil || s.usageService == nil || !s.options.Enabled {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return nil
	}
	loopParent, cancel := context.WithCancel(parent)
	ctx, finish, err := s.activity.begin(loopParent, ErrUsageMonitorStopped)
	if err != nil {
		cancel()
		return err
	}
	s.started = true
	s.cancel = cancel
	// Register the loop before launching it so StopContext can cancel and
	// wait
	// for its actual exit, including in-progress rounds.
	go func() {
		defer cancel()
		defer finish()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.RunOnce(ctx)
			}
		}
	}()
	return nil
}

// Stop cancels tracked monitor work and waits with a background context. It
// discards the delegated shutdown result; callers requiring a bounded wait or
// failure reporting use StopContext.
func (s *UsageMonitor) Stop() { _ = s.StopContext(context.Background()) }

// StopContext prevents later startup, cancels tracked work, and waits within
// ctx for completion. Nil receivers succeed; a dependency that ignores
// cancellation can remain active after the caller budget expires.
//
// The stopped flag is installed under the startup mutex before activity
// cancellation begins, so a racing StartContext cannot admit another loop.
// operationActivity keeps every accepted round registered until it finishes
// and retains the first stop result, including timeout, for subsequent calls.
func (s *UsageMonitor) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	// Close startup admission before activity cancellation, then release
	// the
	// startup lock so the possibly lengthy drain does not hold that lock.
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	return s.activity.stop(ctx, "upstream usage monitor")
}

// acquireLease requests singleton round ownership and returns the release
// function when admitted. Contention skips the round, leader errors try
// advisory locking, and absent backends admit work; callers release the lease
// after their round finishes.
func (s *UsageMonitor) acquireLease(ctx context.Context) (func(), bool) {
	// Keep the lease thirty seconds beyond the round budget so ordinary
	// timeout
	// cleanup can complete before lease expiry.
	return AcquireSingletonLease(ctx, s.options.Leader, s.options.Advisory, usageMonitorLeaderLockKey, s.options.InstanceID, s.roundTimeout+30*time.Second)
}

// RunOnce performs a tracked round with its configured timeout context, using
// the first context argument when nonnil, otherwise a background context. It
// skips absent
// dependencies and rejected activity registration. Manual rounds can run even
// when periodic monitoring is disabled.
//
// Lease release is deferred until admitted probes return; backend expiry can
// precede completion when dependencies ignore cancellation. Candidate queries
// use the round timeout context and configured worker limit. Each probe handles
// its own failures so one provider does not cancel the others.
// Shutdown cancellation reaches the round, and activity completion follows the
// worker wait and lease release.
func (s *UsageMonitor) RunOnce(parents ...context.Context) {
	parent := context.Background()
	if len(parents) > 0 && parents[0] != nil {
		parent = parents[0]
	}
	if s == nil {
		return
	}
	if s.providerRepo == nil || s.usageService == nil {
		return
	}
	// A cancelled parent or stopped activity rejects this round before a
	// lease
	// or probe is admitted; RunOnce returns silently because it has no
	// result.
	parent, finish, err := s.activity.begin(parent, ErrUsageMonitorStopped)
	if err != nil {
		return
	}
	defer finish()
	roundCtx, cancel := context.WithTimeout(parent, s.roundTimeout)
	defer cancel()
	release, acquired := s.acquireLease(roundCtx)
	if !acquired {
		return
	}
	defer release()

	providers := s.monitorCandidates(roundCtx)
	group, groupCtx := errgroup.WithContext(roundCtx)
	// Bound admitted probe goroutines and wait for them before lease
	// release;
	// individual provider failures are handled inside each probe.
	group.SetLimit(s.concurrency)
	for i := range providers {
		providerID := providers[i].ID
		group.Go(func() error {
			s.probeOne(groupCtx, providerID)
			return nil
		})
	}
	_ = group.Wait()
}

// monitorCandidates loads active API-key records from platform-declared
// monitoring sources. Unsupported or disabled queries are skipped.
// Cancellation retains candidates already collected, while a failed platform
// listing is logged and other declared platforms are still examined.
func (s *UsageMonitor) monitorCandidates(ctx context.Context) []Record {
	result := make([]Record, 0)
	for _, platform := range usageMonitorPlatforms() {
		if ctx.Err() != nil {
			return result
		}
		providers, err := s.providerRepo.ListByPlatform(ctx, platform)
		if err != nil {
			// A failed platform list must not discard already collected
			// candidates or prevent the remaining Kimi, Zhipu, and DeepSeek
			// platforms from being considered.
			s.options.Warn("cn_usage_monitor_list_failed", "platform", platform, "error", err)
			continue
		}
		for i := range providers {
			provider := providers[i]
			if provider.Type != ProviderTypeAPIKey || provider.Status != StatusActive {
				continue
			}
			queryConfig, err := EffectiveUpstreamUsageConfig(&provider)
			if err != nil || !queryConfig.Enabled {
				continue
			}
			result = append(result, provider)
		}
	}
	return result
}

// probeOne queries a provider after checking its current usage configuration
// and allowed monitor host. Read or configuration failures skip the attempt;
// host rejection is saved as an attempt error. The upstream query receives its
// own timeout within the parent round.
//
// A successful balance decision runs before the final snapshot write and can
// update the provider version. The provider is reread afterward, and
// persistAttempt rechecks the original query identity against that current row
// before a conditional snapshot write. Failed queries retain previous
// successful display data through that same persistence path.
func (s *UsageMonitor) probeOne(parent context.Context, providerID int64) {
	if parent.Err() != nil {
		return
	}
	// A usable row and supported settings are needed to capture query
	// identity;
	// failed preparation skips this provider while other probes continue.
	provider, err := s.providerRepo.GetByID(parent, providerID)
	if err != nil || provider == nil {
		return
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(provider)
	if err != nil || !queryConfig.Enabled {
		return
	}
	identityHash := usageMonitorFingerprint(provider, queryConfig)
	if err := s.validateMonitorHost(provider, queryConfig); err != nil {
		s.persistAttempt(parent, provider, queryConfig, identityHash, nil, err)
		return
	}

	probeCtx, cancel := context.WithTimeout(parent, s.probeTimeout)
	result, queryErr := s.usageService.QueryProvider(probeCtx, providerID)
	cancel()
	if queryErr == nil && result != nil {
		// The health decision can advance UpdatedAt; reread afterward
		// so the
		// observation write compares the version remaining after that
		// decision.
		s.applyBalanceDecision(parent, providerID, identityHash, result)
	}
	// The final snapshot needs the post-decision row version; an unreadable
	// row
	// ends this attempt before conditional publication can start.
	current, err := s.providerRepo.GetByID(parent, providerID)
	if err != nil || current == nil {
		return
	}
	s.persistAttempt(parent, current, queryConfig, identityHash, result, queryErr)
}

// persistAttempt conditionally saves an attempt for the supplied row version
// when its query configuration and fingerprint still match. Cancellation or a
// changed identity stops persistence. Existing observations with another
// version or identity are replaced with an empty snapshot for this identity.
//
// Failure updates attempt time and a sanitized error code while retaining
// matching successful data. Success installs the observation and clears the
// error. Snapshot and scheduler-event consistency belongs to
// UpdateUsageMonitorSnapshotCAS; write errors and clean version misses are
// logged through separate callbacks, with no retry here.
func (s *UsageMonitor) persistAttempt(
	ctx context.Context,
	provider *Record,
	queryConfig UpstreamUsageQueryConfig,
	identityHash string,
	result *UpstreamUsageQueryResult,
	queryErr error,
) {
	if provider == nil || ctx.Err() != nil {
		return
	}
	// Unreadable current query settings prevent an identity comparison, so
	// this
	// attempt leaves the stored observation unchanged.
	currentConfig, err := EffectiveUpstreamUsageConfig(provider)
	if err != nil {
		return
	}
	if currentConfig != queryConfig || usageMonitorFingerprint(provider, currentConfig) != identityHash {
		return
	}
	now := s.options.Now().UTC()
	snapshot := UsageMonitorSnapshotFromExtra(provider.Extra)
	if snapshot == nil || snapshot.Version != UsageMonitorSnapshotVersion || snapshot.IdentityHash != identityHash {
		snapshot = &UsageMonitorSnapshot{
			Version:      UsageMonitorSnapshotVersion,
			Adapter:      queryConfig.Adapter,
			IdentityHash: identityHash,
		}
	}
	snapshot.LastAttemptAt = now
	if queryErr != nil || result == nil {
		code := apperror.Reason(queryErr)
		if code == "" {
			code = "CN_USAGE_MONITOR_QUERY_FAILED"
		}
		// Retain matching successful balance and limits on failure;
		// only the
		// attempt timestamp and sanitized error describe this failed
		// poll.
		snapshot.LastError = &UsageMonitorError{Code: code, ObservedAt: now}
	} else {
		observedAt := result.ObservedAt.UTC()
		snapshot.Adapter = result.Adapter
		snapshot.Provider = result.Provider
		snapshot.Mode = result.Mode
		snapshot.Unit = result.Unit
		snapshot.Balance = result.Balance
		snapshot.Balances = result.Balances
		snapshot.Available = result.Available
		snapshot.Limits = result.Limits
		snapshot.Subscription = result.Subscription
		snapshot.ExpiresAt = result.ExpiresAt
		snapshot.ObservedAt = &observedAt
		snapshot.LastError = nil
	}
	// Compare the current row version at storage, after identity checks, so
	// a
	// concurrent configuration or health update suppresses stale
	// publication.
	written, err := s.providerRepo.UpdateUsageMonitorSnapshotCAS(
		ctx,
		provider.ID,
		provider.UpdatedAt,
		snapshot,
		"",
	)
	if err != nil {
		s.options.Warn("cn_usage_monitor_snapshot_failed", "provider_id", provider.ID, "error", err)
	} else if !written {
		s.options.Debug("cn_usage_monitor_snapshot_stale", "provider_id", provider.ID)
	}
}

// applyBalanceDecision attempts an identity-matched pause or recovery from a
// balance-mode result. It rereads the row, verifies the query fingerprint, and
// leaves scheduling untouched when balance availability is unknown or the
// context is cancelled.
//
// Low balance pauses a currently schedulable record for two monitor intervals.
// Recovered balance clears only a present pause with the exact monitor reason
// for this identity. Both writes compare the reread UpdatedAt, so a later row
// change can suppress the decision; errors are logged and clean comparison
// misses are ignored.
func (s *UsageMonitor) applyBalanceDecision(
	ctx context.Context,
	providerID int64,
	identityHash string,
	result *UpstreamUsageQueryResult,
) {
	if result == nil || result.Mode != "balance" || ctx.Err() != nil {
		return
	}
	// Balance decisions require a current comparable query identity; failed
	// reads or invalid settings leave any existing scheduling pause
	// untouched.
	provider, err := s.providerRepo.GetByID(ctx, providerID)
	if err != nil || provider == nil {
		return
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(provider)
	if err != nil {
		return
	}
	if usageMonitorFingerprint(provider, queryConfig) != identityHash {
		return
	}
	threshold := s.options.BalanceThreshold
	low, known := UsageBalanceBelowThreshold(result, threshold)
	if !known {
		return
	}
	reason := UsageMonitorReason(identityHash)
	if low {
		if !provider.IsSchedulable() {
			return
		}
		// Allow two complete polling intervals before the low-balance
		// pause
		// expires, leaving the next observation a chance to recover it.
		until := s.options.Now().Add(2 * s.interval)
		if _, err := s.providerRepo.SetUsageMonitorDecisionCAS(ctx, provider.ID, provider.UpdatedAt, until, reason, false); err != nil {
			s.options.Warn("cn_usage_monitor_pause_failed", "provider_id", provider.ID, "error", err)
		}
		return
	}
	// Clear this identity's exact monitor reason so a healthy balance
	// cannot
	// undo a pause recorded for a different cause.
	if provider.TempUnschedulableUntil != nil && provider.TempUnschedulableReason == reason {
		if _, err := s.providerRepo.SetUsageMonitorDecisionCAS(ctx, provider.ID, provider.UpdatedAt, time.Time{}, reason, true); err != nil {
			s.options.Warn("cn_usage_monitor_resume_failed", "provider_id", provider.ID, "error", err)
		}
	}
}

// UsageBalanceBelowThreshold returns whether a balance is low and whether
// that conclusion is known. Nil and other modes are unknown. An explicit
// unavailable account is low regardless of the balances recorded alongside it.
//
// For multiple currencies, any remaining balance at or above threshold keeps
// the account usable, even if another currency is low. The single-balance path
// requires a remaining value. This comparison uses supplied units and threshold
// directly; it performs no currency conversion or numeric validation.
func UsageBalanceBelowThreshold(result *UpstreamUsageQueryResult, threshold float64) (bool, bool) {
	if result == nil || result.Mode != "balance" {
		return false, false
	}
	if result.Available != nil && !*result.Available {
		return true, true
	}
	if len(result.Balances) > 0 {
		// A usable currency keeps mixed-currency accounts schedulable.
		for _, balance := range result.Balances {
			if balance.Remaining >= threshold {
				return false, true
			}
		}
		return true, true
	}
	if result.Balance == nil || result.Balance.Remaining == nil {
		return false, false
	}
	return *result.Balance.Remaining < threshold, true
}

// UsageMonitorReason embeds the query fingerprint in the persisted low-balance
// reason.
func UsageMonitorReason(identityHash string) string {
	return usageMonitorReasonPrefix + identityHash + ": balance is below the monitoring threshold"
}

// UsageMonitorSnapshotFromExtra decodes the stored monitor value through a JSON
// round trip. Missing values and serialization or decoding errors return nil;
// snapshot version and current query identity are checked separately by
// ValidUsageMonitorSnapshot.
func UsageMonitorSnapshotFromExtra(extra map[string]any) *UsageMonitorSnapshot {
	if len(extra) == 0 || extra[UsageMonitorSnapshotExtraKey] == nil {
		return nil
	}
	payload, err := json.Marshal(extra[UsageMonitorSnapshotExtraKey])
	if err != nil {
		// An unencodable Extra value is unusable display data; treat it
		// as
		// absent so consumers can obtain a fresh observation.
		return nil
	}
	var snapshot UsageMonitorSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		// Malformed stored snapshot data is treated as absent; callers
		// then
		// avoid applying a balance or reset from it.
		return nil
	}
	return &snapshot
}

// ValidUsageMonitorSnapshot reads a snapshot whose format version, adapter,
// and query identity match a monitored platform. Nil records, unavailable
// adapters, and stale identities return nil so consumers cannot apply
// observations from an earlier configuration.
func ValidUsageMonitorSnapshot(provider *Record) *UsageMonitorSnapshot {
	if provider == nil || !provider.SupportsUsageMonitoring() {
		return nil
	}
	// A malformed query configuration supplies no trusted current identity,
	// so
	// consumers receive no snapshot even if Extra contains one.
	queryConfig, err := EffectiveUpstreamUsageConfig(provider)
	if err != nil {
		return nil
	}
	if queryConfig.Adapter == "" {
		return nil
	}
	snapshot := UsageMonitorSnapshotFromExtra(provider.Extra)
	if snapshot == nil || snapshot.Version != UsageMonitorSnapshotVersion ||
		snapshot.Adapter != queryConfig.Adapter ||
		snapshot.IdentityHash != usageMonitorFingerprint(provider, queryConfig) {
		return nil
	}
	return snapshot
}

// UsageMonitorOfficialHost matches a host against the selected platform's
// automatic-query allowlist. Matching trims whitespace and ignores case; an
// undeclared platform or host returns false for custom-host admission checks.
func UsageMonitorOfficialHost(platform, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return slices.Contains(platformRuleRegistry[platform].usage.officialHosts, host)
}

// usageMonitorFingerprint hashes the provider query context and its selected
// protocol base URL, reusing UsageMonitorIdentityHint when present on a
// lightweight scheduler metadata record.
func usageMonitorFingerprint(value *Record, config UpstreamUsageQueryConfig) string {
	if value != nil && value.UsageMonitorIdentityHint != "" {
		return value.UsageMonitorIdentityHint
	}
	return UpstreamUsageContextFingerprint(value, config, value.OpenAIBaseURL(value.ConfiguredAPIProtocol() == APIProtocolAdaptive))
}

// validateMonitorHost checks the configured query base URL or the provider's
// selected endpoint against the cloned monitor host policy. Invalid URL errors
// become ErrUpstreamUsageConfigInvalid; other host-policy failures retain their
// original error.
func (s *UsageMonitor) validateMonitorHost(value *Record, config UpstreamUsageQueryConfig) error {
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		base = value.OpenAIBaseURL(value.ConfiguredAPIProtocol() == APIProtocolAdaptive)
	}
	err := s.options.HostPolicy.Validate(base, func(host string) bool { return UsageMonitorOfficialHost(value.Platform, host) })
	// Use the administrator-facing configuration error for malformed
	// monitor
	// URLs while retaining distinct policy errors for denied hosts.
	if errors.Is(err, egress.ErrMonitorURLInvalid) {
		return ErrUpstreamUsageConfigInvalid
	}
	return err
}
