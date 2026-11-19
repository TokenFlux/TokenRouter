package scheduler

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// diagnostic_service.go calculates administrative provider scores and explains
// eligibility, ranking, and affinity decisions.
// Diagnostic probabilities describe the candidate pool for an administrator's
// supplied model and provider IDs. The service reads an unpaged provider list
// for exclusion counts and a schedulable list for scoring, then applies group
// and platform eligibility before ranking. These observations cannot predict
// whether a later request obtains a physical slot, and request capabilities
// absent from the diagnostic input are reported as unevaluated.
//
// Hard affinity can override the displayed probability while leaving a
// provider's numeric rank and Top-K membership intact. Subscription preference
// narrows the ranked pool unless hard affinity has forced a provider first.
// Load, feedback, reset, and quota availability are reported alongside the
// factors already used by the shared scorer, so an unavailable observation can
// be distinguished from an eligible provider with a measured value.

// AdvancedSchedulerScoreCalculationVersion labels the shared scoring formula
// used to build these diagnostic responses and their displayed metric
// explanations.
const AdvancedSchedulerScoreCalculationVersion = "v1"

// AdvancedSchedulerScoreDiagnosticRequest contains the administrator's group,
// model, and simulated affinity inputs.
type AdvancedSchedulerScoreDiagnosticRequest = policy.AdvancedSchedulerScoreDiagnosticRequest

// AdvancedSchedulerScoreDiagnosticProvider identifies the target provider
// using credential-free display and status fields.
type AdvancedSchedulerScoreDiagnosticProvider = policy.AdvancedSchedulerScoreDiagnosticProvider

// AdvancedSchedulerScoreDiagnosticGroup identifies the scheduling group
// represented in a diagnostic response.
type AdvancedSchedulerScoreDiagnosticGroup = policy.AdvancedSchedulerScoreDiagnosticGroup

// AdvancedSchedulerScoreDiagnosticGroupSummary reports target eligibility and
// an optional final score for one group tab.
type AdvancedSchedulerScoreDiagnosticGroupSummary = policy.AdvancedSchedulerScoreDiagnosticGroupSummary

// AdvancedSchedulerScoreDiagnosticResponse contains provider identity,
// generation time, group summaries, and optional detail.
type AdvancedSchedulerScoreDiagnosticResponse = policy.AdvancedSchedulerScoreDiagnosticResponse

// AdvancedSchedulerScoreDiagnosticContext describes the baseline or model and
// affinity simulation used for scoring.
type AdvancedSchedulerScoreDiagnosticContext = policy.AdvancedSchedulerScoreDiagnosticContext

// AdvancedSchedulerScoreDiagnosticDetail contains one group's target
// explanation, ranked pool, settings, and policy signals.
type AdvancedSchedulerScoreDiagnosticDetail = policy.AdvancedSchedulerScoreDiagnosticDetail

// AdvancedSchedulerScoreDiagnosticCandidatePool reports ranked eligibility,
// exclusion counts, normalization ranges, and Top-K probabilities.
type AdvancedSchedulerScoreDiagnosticCandidatePool = policy.AdvancedSchedulerScoreDiagnosticCandidatePool

// AdvancedSchedulerScoreDiagnosticRanges contains the observed extrema used by
// the shared score normalizations.
type AdvancedSchedulerScoreDiagnosticRanges = policy.AdvancedSchedulerScoreDiagnosticRanges

// AdvancedSchedulerScoreDiagnosticCandidate contains one provider's displayed
// rank, score, and optional sampling probability.
type AdvancedSchedulerScoreDiagnosticCandidate = policy.AdvancedSchedulerScoreDiagnosticCandidate

// AdvancedSchedulerScoreDiagnosticScore explains the target provider's score,
// rank, and effective selection mode.
type AdvancedSchedulerScoreDiagnosticScore = policy.AdvancedSchedulerScoreDiagnosticScore

// AdvancedSchedulerScoreDiagnosticMetric describes a score factor's
// observation, availability, weight, and contribution.
type AdvancedSchedulerScoreDiagnosticMetric = policy.AdvancedSchedulerScoreDiagnosticMetric

// AdvancedSchedulerScoreDiagnosticSetting pairs an effective policy value with
// its inferred configuration source.
type AdvancedSchedulerScoreDiagnosticSetting = policy.AdvancedSchedulerScoreDiagnosticSetting

// AdvancedSchedulerScoreDiagnosticPolicySignal reports simulated affinity
// policy or a request check left unevaluated.
type AdvancedSchedulerScoreDiagnosticPolicySignal = policy.AdvancedSchedulerScoreDiagnosticPolicySignal

// DiagnosticProvider carries credential-free observations and an invocation
// association for filtering and scoring.
type DiagnosticProvider struct {
	// ProjectionID identifies the source record for Filter and Quota callbacks;
	// ID is the persisted provider identity, so separate same-ID reads keep
	// separate associations.
	// per-invocation target association
	ProjectionID uint64
	// persisted provider identity
	ID int64
	// provider display name, provider platform identity, provider authentication
	// type, and persisted provider status
	Name, Platform, Type, Status string
	// Priority compares lower values first; LoadFactor supplies the load
	// percentage denominator. These observations belong to the same diagnostic
	// record, while physical concurrency admission is evaluated elsewhere.
	// global sort priority and observed-load percentage denominator
	Priority, LoadFactor int
	// Schedulable rejects manually disabled providers; AutoPauseOnExpired
	// activates the ExpiresAt gate. IsOAuth and PrivacySet are checked when the
	// group requires them, and SubscriptionPriority identifies candidates
	// considered for the preferred subscription pool.
	// manual scheduling admission, automatic expiry enforcement, OAuth
	// authentication qualification, provider privacy qualification, and preferred
	// subscription membership
	Schedulable, AutoPauseOnExpired, IsOAuth, PrivacySet, SubscriptionPriority bool
	// ExpiresAt, OverloadUntil, RateLimitResetAt, TempUnschedulableUntil, and
	// SessionWindowEnd are optional observations. The production adapter retains
	// source time pointers, so diagnostics treats all pointed-to values as
	// read-only.
	// ExpiresAt rejects at or after its deadline when AutoPauseOnExpired enables
	// automatic expiry enforcement for this provider.
	// OverloadUntil, RateLimitResetAt, and TempUnschedulableUntil reject before
	// their deadlines; SessionWindowEnd supplies an optional future-reset scoring
	// signal.
	// expiry admission deadline, overload recovery deadline, rate-limit recovery
	// endpoint, temporary block recovery deadline, and session-window reset
	// endpoint
	ExpiresAt, OverloadUntil, RateLimitResetAt, TempUnschedulableUntil, SessionWindowEnd *time.Time
	// GroupIDs, Groups, and ProviderGroups describe membership in alternate
	// source representations. Overview collection takes advanced ProviderGroups
	// first, then lets Groups replace the same ID, and resolves uncovered
	// positive GroupIDs from the source.
	// persisted group membership IDs
	GroupIDs []int64
	// hydrated group membership pointers
	Groups []*DiagnosticGroup
	// hydrated membership record values
	ProviderGroups []DiagnosticProviderGroup
}
type (
	// DiagnosticProviderGroup carries an optional hydrated group from the
	// provider's membership records.
	DiagnosticProviderGroup struct {
		// Group supplies hydrated membership policy. A nil value is skipped by
		// advanced-group collection and by the ProviderGroups membership branch,
		// while the provider's other membership representations remain available.
		Group *DiagnosticGroup // hydrated membership group pointer
	}
	// DiagnosticGroup contains the scheduling policy and invocation association
	// for one diagnostic group.
	DiagnosticGroup struct {
		// ProjectionID recovers this group's source policy through the current
		// adapter lookup; passing metadata from another invocation can associate the
		// wrong group.
		ProjectionID                                  uint64 // per-invocation policy association
		ID                                            int64  // persisted scheduling group identity
		Name                                          string // group display name
		SortOrder                                     int    // group tab sort order
		Advanced, RequireOAuthOnly, RequirePrivacySet bool   // advanced scheduler mode, OAuth-only admission, and required privacy qualification
		// AdvancedSchedulerOverrides carries per-field optional overrides; Effective
		// resolves their values, while diagnosticEffectiveSettings infers display
		// source labels from pointer presence.
		// group advanced setting overrides
		AdvancedSchedulerOverrides policy.GroupAdvancedSchedulerOverrides
	}
)

// DiagnosticSource reads diagnostic metadata and unpaged provider sets for
// group explanations.
type DiagnosticSource interface {
	// GetProvider returns diagnostic metadata for a persisted provider ID. A
	// missing record may be nil; returned observations and invocation
	// associations remain valid until this diagnostic call finishes.
	GetProvider(context.Context, int64) (*DiagnosticProvider, error)
	// GetGroup resolves an uncovered group membership ID to diagnostic policy.
	// Missing groups may return nil, and read failures stop collection of the
	// provider's advanced group summaries.
	GetGroup(context.Context, int64) (*DiagnosticGroup, error)
	// ListProvidersForSchedulerScoreFilter returns an unpaged provider set under
	// the supplied filters. Detail uses the full requested group, including
	// unschedulable records, to explain exclusions absent from the scoring pool.
	ListProvidersForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]DiagnosticProvider, error)
	// ListSchedulableProvidersForAdvancedSchedulerScore returns group-scoped
	// candidate metadata for scoring and platform prefetch. An empty platform
	// requests all platforms; hard filters and subscription policy may further
	// narrow this returned set.
	ListSchedulableProvidersForAdvancedSchedulerScore(context.Context, *int64, string) ([]DiagnosticProvider, error)
}

// DiagnosticPorts supplies invocation-bound policy checks and app-owned
// scoring observations for diagnostic reads.
type DiagnosticPorts struct {
	// Effective is required and resolves settings against the invocation's
	// original group reference; runtime presence also supplies the diagnostic
	// display source labels.
	// effective and runtime settings resolver
	Effective func(context.Context, *DiagnosticGroup) (policy.EffectiveSettings, policy.RuntimeSettings)
	// Prepare is optional; when supplied, it can prefetch read-only platform
	// observations for the schedulable list before candidate filtering.
	// candidate eligibility context preparation
	Prepare func(context.Context, *DiagnosticGroup, []DiagnosticProvider) context.Context
	// Filter is optional and runs after base hard filters. Diagnostic callers
	// supply a read-only implementation that returns an empty reason for an
	// eligible candidate.
	// additional platform rejection reason
	Filter func(context.Context, *DiagnosticProvider, *DiagnosticGroup, AdvancedSchedulerScoreDiagnosticRequest, time.Time) string
	// Quota receives a per-invocation provider association and the scoring time.
	// A missing callback supplies 0.5; the metric also uses 0.5 as its
	// unavailable-observation sentinel.
	Quota func(uint64, time.Time) float64 // platform quota headroom factor
	// Stats is optional; missing feedback still permits scoring with the shared
	// scorer's zero error baseline and unknown latency signal.
	Stats func() *RuntimeStats // shared runtime feedback reader
	// Now is required by response generation and scoring. NewDiagnosticService
	// retains it as supplied and does not provide a default clock.
	Now func() time.Time // diagnostic observation clock
}

// DiagnosticService builds group summaries and simulated score explanations
// from shared production observations.
type DiagnosticService struct {
	// source may be nil at construction; overview and detail reject an
	// unavailable source before reading the target provider.
	source DiagnosticSource // invocation-bound diagnostic metadata reader
	// concurrencyService is optional. Missing service or failed batch
	// observations produce an empty load map rather than rejecting otherwise
	// eligible providers.
	concurrencyService *ConcurrencyService // shared provider load service
	ports              DiagnosticPorts     // policy and observation callbacks
	now                func() time.Time    // bound diagnostic observation clock
}

// diagnosticPolicyOutcome records simulated hard affinity, escape, and
// subscription policy decisions.
type diagnosticPolicyOutcome struct {
	// previousResponseState and sessionStickyState explain attempted affinity,
	// with stickyEscapeReason accompanying an escaped session. forcedProviderID
	// supplies the resulting hard choice; subscriptionPoolActive records whether
	// the remaining pool was narrowed after that choice was considered.
	//
	// forcedProviderID is positive when hard affinity takes precedence.
	// Probability display then selects that ID with probability one while
	// retaining numeric score ranks.
	forcedProviderID       int64  // hard-affinity selected provider ID
	previousResponseState  string // previous-response policy display state
	sessionStickyState     string // session affinity display state
	stickyEscapeReason     string // feedback-triggered escape reason
	subscriptionPoolActive bool   // subscription-ranked pool currently active
}

// diagnosticTopKSelection holds numeric sampling weights and effective
// probabilities for one ranked pool.
type diagnosticTopKSelection struct {
	// minimumScore, weightSum, weights, and probabilities describe one Top-K
	// pool. weights maps persisted provider IDs to shifted scores; probabilities
	// applies hard-affinity override after numeric normalization.
	minimumScore     float64           // lowest Top-K numeric score
	weightSum        float64           // summed positive sampling weights
	weights          map[int64]float64 // provider sampling weight map
	probabilities    map[int64]float64 // effective provider probability map
	forcedProviderID int64             // hard-affinity selected provider ID
}

// UsesAdvancedScheduler reports whether a nonnil diagnostic group enables
// advanced selection.
func (g *DiagnosticGroup) UsesAdvancedScheduler() bool { return g != nil && g.Advanced }

// NewDiagnosticService retains the source, optional load service, and
// diagnostic callbacks. Callers supply Effective and Now plus read-only source
// and eligibility implementations; construction performs no validation or
// clock substitution.
func NewDiagnosticService(source DiagnosticSource, concurrency *ConcurrencyService, ports DiagnosticPorts) *DiagnosticService {
	return &DiagnosticService{source: source, concurrencyService: concurrency, ports: ports, now: ports.Now}
}

// effectiveSettings delegates effective policy and runtime provenance reads to
// the bound callback.
func (s *DiagnosticService) effectiveSettings(ctx context.Context, g *DiagnosticGroup) (policy.EffectiveSettings, policy.RuntimeSettings) {
	return s.ports.Effective(ctx, g)
}

// prepareEligibilityContext applies the optional candidate observation
// prefetch callback for this group. With no Prepare callback, the supplied
// context is returned unchanged and base eligibility checks remain available.
func (s *DiagnosticService) prepareEligibilityContext(ctx context.Context, g *DiagnosticGroup, a []DiagnosticProvider) context.Context {
	if s.ports.Prepare == nil {
		return ctx
	}
	return s.ports.Prepare(ctx, g, a)
}

// HardFilterReason returns the first base eligibility rejection, then an
// optional platform reason. Empty output means the available checks accept
// this metadata; absent request capabilities still require evaluation during
// actual selection.
func (s *DiagnosticService) HardFilterReason(ctx context.Context, a *DiagnosticProvider, g *DiagnosticGroup, request AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
	if reason := diagnosticBaseHardFilterReason(a, g, now); reason != "" {
		return reason
	}
	if s.ports.Filter == nil {
		return ""
	}
	return s.ports.Filter(ctx, a, g, request, now)
}

// diagnosticHardFilterReason delegates candidate eligibility to the service's
// ordered hard-filter checks.
func (s *DiagnosticService) diagnosticHardFilterReason(ctx context.Context, a *DiagnosticProvider, g *DiagnosticGroup, request AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
	return s.HardFilterReason(ctx, a, g, request, now)
}

// partitionDiagnosticSubscriptionProviders preserves provider order in
// subscription and regular pointer slices. Nil entries join the regular slice,
// and the underlying provider observations remain borrowed from the diagnostic
// input.
func partitionDiagnosticSubscriptionProviders(providers []*DiagnosticProvider) ([]*DiagnosticProvider, []*DiagnosticProvider) {
	var subscribed, regular []*DiagnosticProvider
	for _, a := range providers {
		if a != nil && a.SubscriptionPriority {
			subscribed = append(subscribed, a)
		} else {
			regular = append(regular, a)
		}
	}
	return subscribed, regular
}

// quotaHeadroom reads platform headroom using the provider's invocation
// association and scoring time. With no Quota callback it returns the shared
// neutral factor 0.5, so missing data does not reject the provider.
func (s *DiagnosticService) quotaHeadroom(a *ScoreProvider, now time.Time) float64 {
	if s.ports.Quota == nil {
		return 0.5
	}
	return s.ports.Quota(a.ProjectionID, now)
}

// scoreCandidates converts diagnostic observations to the metadata consumed by
// the shared scorer. It retains each ProjectionID alongside the persisted ID
// so the quota callback can recover the record that produced that candidate,
// including separate same-ID reads. Nil entries stay nil for scorer filtering.
//
// Time observations remain shared and read-only, and load records are looked
// up by persisted ID. The scorer returns factor ranges and load dispersion for
// this supplied pool; it does not acquire capacity or validate request
// capabilities missing from the diagnostic request.
func (s *DiagnosticService) scoreCandidates(providers []*DiagnosticProvider, loads map[int64]*ProviderLoadInfo, stats *RuntimeStats, weights policy.ScoreWeights, input ScoreInput, now time.Time) ([]CandidateScore, float64, ScoreRanges) {
	projected := make([]*ScoreProvider, len(providers))
	for i, a := range providers {
		if a != nil {
			projected[i] = &ScoreProvider{ProjectionID: a.ProjectionID, ID: a.ID, Name: a.Name, Platform: a.Platform, Priority: a.Priority, SessionWindowEnd: a.SessionWindowEnd}
		}
	}
	return ScoreCandidatesWithRanges(projected, loads, stats, weights, input, now)
}

// GetOverview returns baseline eligibility and scores for the provider's
// advanced groups in display order. Source and group reads propagate errors;
// model and simulated affinity inputs are absent from every summary.
func (s *DiagnosticService) GetOverview(ctx context.Context, providerID int64) (*AdvancedSchedulerScoreDiagnosticResponse, error) {
	provider, groups, err := s.loadProviderAndAdvancedGroups(ctx, providerID)
	if err != nil {
		return nil, err
	}
	response := s.newResponse(provider)
	for _, group := range groups {
		summary, err := s.buildGroupSummary(ctx, provider, group)
		if err != nil {
			return nil, err
		}
		response.Groups = append(response.Groups, summary)
	}
	return response, nil
}

// GetDetail explains one advanced group using a trimmed model and nonnegative
// simulated affinity IDs. The selected positive group must belong to the
// target's advanced memberships; model input is limited to 512 bytes. Other
// group tabs keep baseline summaries.
//
// Detail combines the full-group exclusion query with the schedulable scoring
// query, then reports numeric rank and effective policy selection separately.
// The IDs simulate affinity against currently eligible candidates rather than
// reading a user's session. Read failures stop the response, and unavailable
// request capability inputs remain unevaluated in the policy signals.
func (s *DiagnosticService) GetDetail(ctx context.Context, providerID int64, request AdvancedSchedulerScoreDiagnosticRequest) (*AdvancedSchedulerScoreDiagnosticResponse, error) {
	provider, groups, err := s.loadProviderAndAdvancedGroups(ctx, providerID)
	if err != nil {
		return nil, err
	}
	if request.GroupID <= 0 {
		return nil, fmt.Errorf("group_id is required")
	}
	request.RequestedModel = strings.TrimSpace(request.RequestedModel)
	// The request limit counts bytes after trimming, including multibyte model
	// names.
	if len(request.RequestedModel) > 512 {
		return nil, fmt.Errorf("requested_model is too long")
	}
	if request.StickyProviderID < 0 || request.PreviousResponseProviderID < 0 {
		return nil, fmt.Errorf("sticky provider id must not be negative")
	}

	var selected *DiagnosticGroup
	for _, group := range groups {
		if group.ID == request.GroupID {
			selected = group
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("provider is not in an advanced scheduler group")
	}

	response := s.newResponse(provider)
	for _, group := range groups {
		if group.ID == selected.ID {
			detail, buildErr := s.buildDetail(ctx, provider, selected, request)
			if buildErr != nil {
				return nil, buildErr
			}
			response.Detail = detail
			response.Groups = append(response.Groups, summaryFromDiagnosticDetail(detail))
			continue
		}
		summary, buildErr := s.buildGroupSummary(ctx, provider, group)
		if buildErr != nil {
			return nil, buildErr
		}
		response.Groups = append(response.Groups, summary)
	}
	return response, nil
}

// loadProviderAndAdvancedGroups reads the target and deduplicates its advanced
// memberships by persisted group ID. An unavailable service/source or missing
// provider returns an error. ProviderGroups contribute first, Groups replace
// matching IDs, and uncovered positive GroupIDs trigger source reads.
//
// Only advanced groups enter the result; a non-advanced hydrated membership
// can consequently cause its ID to be read again through GroupIDs. Failed
// group reads stop collection. Successful results sort by SortOrder and then
// ID, while their invocation associations retain the source record selected
// for each group.
func (s *DiagnosticService) loadProviderAndAdvancedGroups(ctx context.Context, providerID int64) (*DiagnosticProvider, []*DiagnosticGroup, error) {
	if s == nil || s.source == nil {
		return nil, nil, fmt.Errorf("advanced scheduler diagnostics is unavailable")
	}
	provider, err := s.source.GetProvider(ctx, providerID)
	if err != nil {
		return nil, nil, err
	}
	if provider == nil {
		return nil, nil, fmt.Errorf("provider not found")
	}
	groupsByID := make(map[int64]*DiagnosticGroup)
	for _, providerGroup := range provider.ProviderGroups {
		if providerGroup.Group != nil && providerGroup.Group.UsesAdvancedScheduler() {
			groupsByID[providerGroup.Group.ID] = providerGroup.Group
		}
	}
	for _, group := range provider.Groups {
		if group != nil && group.UsesAdvancedScheduler() {
			groupsByID[group.ID] = group
		}
	}
	for _, groupID := range provider.GroupIDs {
		if groupID <= 0 {
			continue
		}
		if _, exists := groupsByID[groupID]; exists {
			continue
		}
		group, groupErr := s.source.GetGroup(ctx, groupID)
		if groupErr != nil {
			return nil, nil, groupErr
		}
		if group != nil && group.UsesAdvancedScheduler() {
			groupsByID[group.ID] = group
		}
	}

	groups := make([]*DiagnosticGroup, 0, len(groupsByID))
	for _, group := range groupsByID {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].SortOrder != groups[j].SortOrder {
			return groups[i].SortOrder < groups[j].SortOrder
		}
		return groups[i].ID < groups[j].ID
	})
	return provider, groups, nil
}

// newResponse builds provider identity and an allocated empty group list with
// the bound clock's UTC timestamp. A nil provider produces an empty identity
// summary; callers append overview or detail results.
func (s *DiagnosticService) newResponse(provider *DiagnosticProvider) *AdvancedSchedulerScoreDiagnosticResponse {
	return &AdvancedSchedulerScoreDiagnosticResponse{
		Provider:           diagnosticProviderSummary(provider),
		GeneratedAt:        s.now().UTC(),
		CalculationVersion: AdvancedSchedulerScoreCalculationVersion,
		Groups:             make([]AdvancedSchedulerScoreDiagnosticGroupSummary, 0),
	}
}

// diagnosticProviderSummary copies provider identity and display fields into a
// response value. Nil metadata returns a zero summary, allowing unavailable
// identity to be represented without dereferencing a provider.
func diagnosticProviderSummary(provider *DiagnosticProvider) AdvancedSchedulerScoreDiagnosticProvider {
	if provider == nil {
		return AdvancedSchedulerScoreDiagnosticProvider{}
	}
	return AdvancedSchedulerScoreDiagnosticProvider{
		ID:       provider.ID,
		Name:     provider.Name,
		Platform: provider.Platform,
		Type:     provider.Type,
		Status:   provider.Status,
	}
}

// diagnosticGroupSummary copies a group's ID and display name for response
// output. Nil metadata returns a zero summary; the scheduling and invocation
// association fields stay outside this display value.
func diagnosticGroupSummary(group *DiagnosticGroup) AdvancedSchedulerScoreDiagnosticGroup {
	if group == nil {
		return AdvancedSchedulerScoreDiagnosticGroup{}
	}
	return AdvancedSchedulerScoreDiagnosticGroup{ID: group.ID, Name: group.Name}
}

// summaryFromDiagnosticDetail builds a group tab from a nonnil detail
// response. Filtered targets have no displayed score, while eligible targets
// copy the optional FinalScore into an independent scalar pointer.
func summaryFromDiagnosticDetail(detail *AdvancedSchedulerScoreDiagnosticDetail) AdvancedSchedulerScoreDiagnosticGroupSummary {
	summary := AdvancedSchedulerScoreDiagnosticGroupSummary{
		AdvancedSchedulerScoreDiagnosticGroup: detail.Group,
		Eligible:                              detail.Eligible,
		Status:                                "eligible",
	}
	if !detail.Eligible {
		summary.Status = "filtered"
		return summary
	}
	if detail.Score != nil {
		score := detail.Score.FinalScore
		summary.FinalScore = &score
	}
	return summary
}

// buildGroupSummary reports the target's baseline eligibility and final score
// for one advanced group. Missing target or group produces a filtered tab.
// Candidate reads use no model or affinity IDs, and source failures stop the
// summary.
//
// Platform prefetch and hard filters run before subscription preference
// chooses the scoring pool. Load failures supply unknown observations, and
// shared feedback and effective weights then determine scores for that pool.
// The target is eligible only if found among those scored candidates; a
// regular provider deferred by subscription preference therefore receives a
// filtered summary.
func (s *DiagnosticService) buildGroupSummary(
	ctx context.Context,
	target *DiagnosticProvider,
	group *DiagnosticGroup,
) (AdvancedSchedulerScoreDiagnosticGroupSummary, error) {
	summary := AdvancedSchedulerScoreDiagnosticGroupSummary{
		AdvancedSchedulerScoreDiagnosticGroup: diagnosticGroupSummary(group),
		Status:                                "eligible",
	}
	if target == nil || group == nil {
		summary.Status = "filtered"
		return summary, nil
	}
	now := s.now()
	effective, _ := s.effectiveSettings(ctx, group)
	poolProviders, err := s.source.ListSchedulableProvidersForAdvancedSchedulerScore(ctx, &group.ID, "")
	if err != nil {
		return summary, err
	}
	ctx = s.prepareEligibilityContext(ctx, group, poolProviders)
	filtered := make([]*DiagnosticProvider, 0, len(poolProviders))
	for index := range poolProviders {
		candidate := poolProviders[index]
		if s.diagnosticHardFilterReason(ctx, &candidate, group, AdvancedSchedulerScoreDiagnosticRequest{GroupID: group.ID}, now) == "" {
			filtered = append(filtered, &candidate)
		}
	}
	filtered, _, _ = diagnosticSubscriptionPriorityPool(filtered, group, effective)
	loadMap := s.LoadMap(ctx, filtered)
	var stats *RuntimeStats
	if s.ports.Stats != nil {
		stats = s.ports.Stats()
	}
	candidates, _, _ := s.scoreCandidates(
		filtered,
		loadMap,
		stats,
		effective.Weights,
		ScoreInput{
			GroupID:             &group.ID,
			StickyWeighted:      effective.StickyWeightedEnabled,
			TopK:                effective.TopK,
			QuotaHeadroomFactor: s.quotaHeadroom,
		},
		now,
	)
	targetCandidate, _ := findDiagnosticCandidate(candidates, target.ID)
	if targetCandidate == nil {
		summary.Status = "filtered"
		return summary, nil
	}
	summary.Eligible = true
	finalScore := targetCandidate.Score
	summary.FinalScore = &finalScore
	return summary, nil
}

// buildDetail ranks a group's simulated candidate pool and explains the target
// using current settings and observations. It requires nonnil target and group
// metadata. The full provider query accounts for exclusions, while only the
// schedulable query can contribute score candidates; source errors stop either
// phase.
//
// Escaped hard session affinity clears sticky eligibility for cost and RPM
// checks before filtering. A usable hard affinity can force a provider before
// subscription narrowing; otherwise regular providers are deferred when the
// subscription pool is active. The shared scorer supplies numeric rank and
// Top-K membership, and probability display applies the hard-affinity decision
// separately. Targets excluded or deferred receive reasons instead of a score.
func (s *DiagnosticService) buildDetail(
	ctx context.Context,
	target *DiagnosticProvider,
	group *DiagnosticGroup,
	request AdvancedSchedulerScoreDiagnosticRequest,
) (*AdvancedSchedulerScoreDiagnosticDetail, error) {
	now := s.now()
	effective, runtime := s.effectiveSettings(ctx, group)
	allProviders, err := s.source.ListProvidersForSchedulerScoreFilter(ctx, "", "", "", "", group.ID, "")
	if err != nil {
		return nil, err
	}
	poolProviders, err := s.source.ListSchedulableProvidersForAdvancedSchedulerScore(ctx, &group.ID, "")
	if err != nil {
		return nil, err
	}
	ctx = s.prepareEligibilityContext(ctx, group, poolProviders)
	stats := (*RuntimeStats)(nil)
	if s.ports.Stats != nil {
		stats = s.ports.Stats()
	}
	eligibilityRequest := request
	// Feedback escape removes the hard-sticky admission allowance for this
	// request. Clear the eligibility copy before platform cost and RPM checks,
	// retaining the supplied ID for policy explanation.
	if !effective.StickyWeightedEnabled && request.StickyProviderID > 0 {
		if _, _, _, escaped := ShouldEscapeSticky(stats, request.StickyProviderID, effective.StickyEscape); escaped {
			eligibilityRequest.StickyProviderID = 0
		}
	}

	exclusions := make(map[string]int)
	filtered := make([]*DiagnosticProvider, 0, len(poolProviders))
	seenPoolIDs := make(map[int64]struct{}, len(poolProviders))
	for i := range poolProviders {
		candidate := poolProviders[i]
		seenPoolIDs[candidate.ID] = struct{}{}
		if reason := s.diagnosticHardFilterReason(ctx, &candidate, group, eligibilityRequest, now); reason != "" {
			exclusions[reason]++
			continue
		}
		filtered = append(filtered, &candidate)
	}
	for i := range allProviders {
		provider := &allProviders[i]
		if _, found := seenPoolIDs[provider.ID]; found {
			continue
		}
		reason := s.diagnosticHardFilterReason(ctx, provider, group, eligibilityRequest, now)
		if reason == "" {
			// A full-group record can pass available hard checks yet be absent from the
			// candidate source. Count that source exclusion instead of inventing
			// another gate failure.
			reason = "not_in_schedulable_pool"
		}
		exclusions[reason]++
	}

	policyOutcome := diagnosticHardStickyPolicyOutcome(filtered, group, request, effective, stats, effective.StickyEscape)
	deferredProviderIDs := map[int64]struct{}{}
	// Usable hard affinity takes precedence over subscription deferral. Only an
	// unforced choice narrows the ranked pool and counts ordinary candidates as
	// deferred.
	if policyOutcome.forcedProviderID == 0 {
		var subscriptionPoolActive bool
		filtered, deferredProviderIDs, subscriptionPoolActive = diagnosticSubscriptionPriorityPool(filtered, group, effective)
		policyOutcome.subscriptionPoolActive = subscriptionPoolActive
		if len(deferredProviderIDs) > 0 {
			exclusions["subscription_priority_deferred"] += len(deferredProviderIDs)
		}
	}

	loadMap := s.LoadMap(ctx, filtered)
	previousResponseProviderID := int64(0)
	if diagnosticPreviousResponseSupported(filtered, request.PreviousResponseProviderID) {
		previousResponseProviderID = request.PreviousResponseProviderID
	}
	input := ScoreInput{
		GroupID:                  &group.ID,
		RequestedModel:           request.RequestedModel,
		StickyProviderID:         request.StickyProviderID,
		StickyPreviousProviderID: previousResponseProviderID,
		StickyWeighted:           effective.StickyWeightedEnabled,
		TopK:                     effective.TopK,
		QuotaHeadroomFactor:      s.quotaHeadroom,
	}
	candidates, _, ranges := s.scoreCandidates(filtered, loadMap, stats, effective.Weights, input, now)
	sort.SliceStable(candidates, func(i, j int) bool {
		return CandidateBetter(candidates[i], candidates[j])
	})
	topKCandidates := SelectTopK(candidates, effective.TopK)
	selection := diagnosticSelectionStats(topKCandidates, policyOutcome.forcedProviderID)

	detail := &AdvancedSchedulerScoreDiagnosticDetail{
		Group:             diagnosticGroupSummary(group),
		Context:           diagnosticContext(request),
		CandidatePool:     diagnosticCandidatePool(candidates, topKCandidates, ranges, exclusions, selection),
		EffectiveSettings: diagnosticEffectiveSettings(group, runtime, effective),
		PolicySignals:     diagnosticPolicySignals(group, request, effective, policyOutcome),
		Metrics:           make([]AdvancedSchedulerScoreDiagnosticMetric, 0),
	}
	targetReason := s.diagnosticHardFilterReason(ctx, target, group, eligibilityRequest, now)
	if target != nil {
		if _, deferred := deferredProviderIDs[target.ID]; deferred {
			targetReason = "subscription_priority_deferred"
		}
	}
	if targetReason != "" {
		detail.HardFilterReasons = append(detail.HardFilterReasons, targetReason)
	}
	targetCandidate, targetRank := findDiagnosticCandidate(candidates, target.ID)
	if targetCandidate == nil {
		if targetReason == "" {
			detail.HardFilterReasons = append(detail.HardFilterReasons, "not_in_schedulable_pool")
		}
		detail.Eligible = false
		return detail, nil
	}
	detail.Eligible = true
	detail.Score = diagnosticScore(targetCandidate, targetRank, topKCandidates, selection, effective, request)
	detail.Metrics = s.diagnosticMetrics(targetCandidate, ranges, effective.Weights, now)
	return detail, nil
}

// LoadMap returns observed loads using each provider's LoadFactor denominator,
// omitting nil candidates. Missing service, empty input, batch errors, or a
// nil result produce an allocated empty map so shared scoring can use
// unknown-load factors.
func (s *DiagnosticService) LoadMap(ctx context.Context, providers []*DiagnosticProvider) map[int64]*ProviderLoadInfo {
	if s == nil || s.concurrencyService == nil || len(providers) == 0 {
		return map[int64]*ProviderLoadInfo{}
	}
	loads := make([]ProviderWithConcurrency, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			loads = append(loads, ProviderWithConcurrency{ID: provider.ID, MaxConcurrency: provider.LoadFactor})
		}
	}
	loadMap, err := s.concurrencyService.GetProvidersLoadBatch(ctx, loads)
	// Observation failure leaves hard-eligible candidates available. Returning an
	// empty map makes the shared scorer use unknown load and queue factors.
	if err != nil || loadMap == nil {
		return map[int64]*ProviderLoadInfo{}
	}
	return loadMap
}

// diagnosticContext trims the requested model and records the two simulated
// affinity IDs. Baseline is true only when that model and both IDs are absent,
// describing the context used for display.
func diagnosticContext(request AdvancedSchedulerScoreDiagnosticRequest) AdvancedSchedulerScoreDiagnosticContext {
	return AdvancedSchedulerScoreDiagnosticContext{
		RequestedModel:             strings.TrimSpace(request.RequestedModel),
		StickyProviderID:           request.StickyProviderID,
		PreviousResponseProviderID: request.PreviousResponseProviderID,
		Baseline: strings.TrimSpace(request.RequestedModel) == "" &&
			request.StickyProviderID == 0 && request.PreviousResponseProviderID == 0,
	}
}

// diagnosticBaseHardFilterReason returns the first metadata rejection in the
// diagnostic order. Provider presence and group membership precede active
// status and the manual scheduling flag; automatic expiry, overload, rate
// reset, temporary blocking, OAuth-only admission, and required privacy follow.
// Empty output means these base observations accept the candidate.
//
// Expiry is enforced only when AutoPauseOnExpired is enabled and its deadline
// has arrived. Temporary recovery timestamps reject while they remain in the
// future. Model, quota, proxy, and other platform checks belong to the later
// Filter callback, so passing this function alone does not establish request
// eligibility.
func diagnosticBaseHardFilterReason(provider *DiagnosticProvider, group *DiagnosticGroup, now time.Time) string {
	if provider == nil {
		return "provider_missing"
	}
	if !diagnosticPlatformMatchesGroup(provider, group) {
		return "group_mismatch"
	}
	if provider.Status != "active" {
		return "provider_inactive"
	}
	if !provider.Schedulable {
		return "provider_disabled"
	}
	if provider.AutoPauseOnExpired && provider.ExpiresAt != nil && !now.Before(*provider.ExpiresAt) {
		return "provider_expired"
	}
	if provider.OverloadUntil != nil && now.Before(*provider.OverloadUntil) {
		return "provider_overloaded"
	}
	if provider.RateLimitResetAt != nil && now.Before(*provider.RateLimitResetAt) {
		return "provider_rate_limited"
	}
	if provider.TempUnschedulableUntil != nil && now.Before(*provider.TempUnschedulableUntil) {
		return "provider_temporarily_unschedulable"
	}
	if group != nil && group.RequireOAuthOnly && !provider.IsOAuth {
		return "oauth_required"
	}
	if group != nil && group.RequirePrivacySet && !provider.PrivacySet {
		return "privacy_not_set"
	}
	return ""
}

// diagnosticPlatformMatchesGroup reports membership in a nonnil group through
// any of the provider's three membership representations. Despite its name,
// the function compares group IDs; nil provider or group returns false.
func diagnosticPlatformMatchesGroup(provider *DiagnosticProvider, group *DiagnosticGroup) bool {
	if provider == nil || group == nil {
		return false
	}
	if slices.Contains(provider.GroupIDs, group.ID) {
		return true
	}
	for _, item := range provider.ProviderGroups {
		if item.Group != nil && item.Group.ID == group.ID {
			return true
		}
	}
	for _, item := range provider.Groups {
		if item != nil && item.ID == group.ID {
			return true
		}
	}
	return false
}

// diagnosticSubscriptionPriorityPool chooses the providers ranked under
// subscription preference and identifies deferred regular IDs. With no group,
// a disabled policy, or no subscription candidates, it returns the input slice
// and an allocated empty deferred map. Active preference returns the
// subscription slice in input order.
//
// This decision uses eligibility established by the caller, not physical slot
// availability. Regular candidates are represented as deferred so detail can
// explain why they lack ranks. A hard-affinity decision is handled before this
// helper and can bypass narrowing of the ranked pool.
func diagnosticSubscriptionPriorityPool(
	providers []*DiagnosticProvider,
	group *DiagnosticGroup,
	effective policy.EffectiveSettings,
) ([]*DiagnosticProvider, map[int64]struct{}, bool) {
	deferred := make(map[int64]struct{})
	if group == nil || !effective.SubscriptionPriorityEnabled {
		return providers, deferred, false
	}
	subscriptionProviders, regularProviders := partitionDiagnosticSubscriptionProviders(providers)
	if len(subscriptionProviders) == 0 {
		return providers, deferred, false
	}
	for _, provider := range regularProviders {
		if provider != nil {
			deferred[provider.ID] = struct{}{}
		}
	}
	return subscriptionProviders, deferred, true
}

// diagnosticHardStickyPolicyOutcome resolves simulated affinity against an
// already eligible provider list. Weighted mode reports affinity signals
// without forcing a provider, with previous-response support limited to OpenAI
// records found in that list. Hard mode gives supported previous-response
// affinity precedence over session affinity.
//
// A forced previous-response provider leaves session affinity unreached.
// Otherwise shared feedback can escape the session binding before its
// eligibility is considered; a remaining eligible binding is forced first.
// Unsupported or absent previous-response records are reported as ignored.
// This simulated decision supplies display probabilities and uses no session
// hash or stored response binding.
func diagnosticHardStickyPolicyOutcome(
	providers []*DiagnosticProvider,
	group *DiagnosticGroup,
	request AdvancedSchedulerScoreDiagnosticRequest,
	effective policy.EffectiveSettings,
	stats *RuntimeStats,
	escapeConfig policy.StickyEscapeConfig,
) diagnosticPolicyOutcome {
	outcome := diagnosticPolicyOutcome{}
	eligibleIDs := make(map[int64]struct{}, len(providers))
	for _, provider := range providers {
		if provider != nil {
			eligibleIDs[provider.ID] = struct{}{}
		}
	}
	if effective.StickyWeightedEnabled {
		if request.PreviousResponseProviderID > 0 {
			if !diagnosticPreviousResponseSupported(providers, request.PreviousResponseProviderID) {
				outcome.previousResponseState = "ignored"
			} else {
				outcome.previousResponseState = "weighted"
			}
		}
		if request.StickyProviderID > 0 {
			outcome.sessionStickyState = "weighted"
		}
		return outcome
	}

	if request.PreviousResponseProviderID > 0 {
		if !diagnosticPreviousResponseSupported(providers, request.PreviousResponseProviderID) {
			outcome.previousResponseState = "ignored"
		} else if _, eligible := eligibleIDs[request.PreviousResponseProviderID]; eligible {
			outcome.previousResponseState = "forced_first"
			outcome.forcedProviderID = request.PreviousResponseProviderID
		} else {
			outcome.previousResponseState = "unavailable"
		}
	}
	if request.StickyProviderID <= 0 {
		return outcome
	}
	if outcome.forcedProviderID > 0 {
		outcome.sessionStickyState = "not_reached"
		return outcome
	}
	if reason, _, _, escape := ShouldEscapeSticky(stats, request.StickyProviderID, escapeConfig); escape {
		outcome.sessionStickyState = "escaped"
		outcome.stickyEscapeReason = reason
		return outcome
	}
	if _, eligible := eligibleIDs[request.StickyProviderID]; !eligible {
		outcome.sessionStickyState = "unavailable"
		return outcome
	}
	outcome.sessionStickyState = "forced_first"
	outcome.forcedProviderID = request.StickyProviderID
	return outcome
}

// diagnosticSelectionStats computes shifted positive Top-K weights and
// effective display probabilities by persisted provider ID. Empty Top-K
// returns allocated maps and no forced choice. Invalid individual shifted
// weights become one, then the accumulated sum normalizes the numeric
// distribution.
//
// A positive forced provider overrides that distribution to probability one,
// even if its ID lies outside Top-K; the numeric weights and minimum remain
// unchanged. Callers retain numeric rank separately. The accumulated sum has
// no finite-value repair here, so valid individual weights do not establish a
// finite total for extreme score magnitudes.
func diagnosticSelectionStats(topK []CandidateScore, forcedProviderID int64) diagnosticTopKSelection {
	selection := diagnosticTopKSelection{
		weights:       make(map[int64]float64, len(topK)),
		probabilities: make(map[int64]float64, len(topK)),
	}
	if len(topK) == 0 {
		return selection
	}
	selection.minimumScore = topK[0].Score
	for _, candidate := range topK[1:] {
		if candidate.Score < selection.minimumScore {
			selection.minimumScore = candidate.Score
		}
	}
	for _, candidate := range topK {
		if candidate.Provider == nil {
			continue
		}
		weight := candidate.Score - selection.minimumScore + 1
		// An invalid shifted weight cannot support sampling. Use a positive unit
		// weight for that candidate; this repair does not validate the accumulated
		// total.
		if math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
			weight = 1
		}
		selection.weights[candidate.Provider.ID] = weight
		selection.weightSum += weight
	}
	if selection.weightSum > 0 {
		for providerID, weight := range selection.weights {
			selection.probabilities[providerID] = weight / selection.weightSum
		}
	}
	// Hard affinity supplies an effective choice independent of numeric rank.
	// Replace display probabilities after numeric normalization while keeping
	// Top-K weights for explanation.
	if forcedProviderID > 0 {
		selection.forcedProviderID = forcedProviderID
		for providerID := range selection.probabilities {
			selection.probabilities[providerID] = 0
		}
		selection.probabilities[forcedProviderID] = 1
	}
	return selection
}

// diagnosticCandidatePool builds ranked display candidates and aggregates
// exclusion reason counts. TotalCandidates is ranked candidates plus counted
// exclusions, rather than a separate database row count. The exclusions map is
// retained in the returned response.
//
// Numeric Top-K membership comes from the supplied Top-K records and stays
// independent of forced affinity. A forced candidate outside Top-K can
// therefore receive probability one while retaining its original rank and
// absent numeric weight. Ranking assumes CandidateBetter order from the
// caller, and nil candidate metadata is omitted from the displayed rows.
func diagnosticCandidatePool(
	candidates []CandidateScore,
	topK []CandidateScore,
	ranges ScoreRanges,
	exclusions map[string]int,
	selection diagnosticTopKSelection,
) AdvancedSchedulerScoreDiagnosticCandidatePool {
	pool := AdvancedSchedulerScoreDiagnosticCandidatePool{
		EligibleCandidates:  len(candidates),
		ExcludedCandidates:  0,
		ExclusionReasons:    exclusions,
		TopK:                len(topK),
		NormalizationRanges: diagnosticRanges(ranges),
		Candidates:          make([]AdvancedSchedulerScoreDiagnosticCandidate, 0, len(candidates)),
	}
	for _, count := range exclusions {
		pool.ExcludedCandidates += count
	}
	pool.TotalCandidates = pool.EligibleCandidates + pool.ExcludedCandidates
	if len(topK) > 0 {
		minimum := selection.minimumScore
		weightSum := selection.weightSum
		pool.TopKMinimumScore = &minimum
		pool.TopKWeightSum = &weightSum
	}
	topKIDs := make(map[int64]struct{}, len(topK))
	for _, candidate := range topK {
		if candidate.Provider != nil {
			topKIDs[candidate.Provider.ID] = struct{}{}
		}
	}
	for index, candidate := range candidates {
		if candidate.Provider == nil {
			continue
		}
		_, inTopK := topKIDs[candidate.Provider.ID]
		item := AdvancedSchedulerScoreDiagnosticCandidate{
			ID:         candidate.Provider.ID,
			Name:       candidate.Provider.Name,
			Platform:   candidate.Provider.Platform,
			Priority:   candidate.Provider.Priority,
			FinalScore: candidate.Score,
			Rank:       index + 1,
			InTopK:     inTopK,
		}
		if inTopK || selection.forcedProviderID == candidate.Provider.ID {
			if weight, found := selection.weights[candidate.Provider.ID]; found {
				item.SelectionWeight = &weight
			}
			if probability, found := selection.probabilities[candidate.Provider.ID]; found {
				item.SelectionProbability = &probability
			}
		}
		pool.Candidates = append(pool.Candidates, item)
	}
	return pool
}

// diagnosticRanges copies score extrema into response ranges and allocates
// optional latency and reset scalar pointers. Those pointers are present only
// when the scorer observed the corresponding signal in this pool.
func diagnosticRanges(ranges ScoreRanges) AdvancedSchedulerScoreDiagnosticRanges {
	result := AdvancedSchedulerScoreDiagnosticRanges{
		PriorityMin:     ranges.MinPriority,
		PriorityMax:     ranges.MaxPriority,
		MaxWaitingCount: ranges.MaxWaiting,
	}
	if ranges.HasTTFTSample {
		minValue, maxValue := ranges.MinTTFT, ranges.MaxTTFT
		result.TTFTMinMs = &minValue
		result.TTFTMaxMs = &maxValue
	}
	if ranges.HasResetSample {
		minValue, maxValue := ranges.MinResetRemaining, ranges.MaxResetRemaining
		result.ResetMinSeconds = &minValue
		result.ResetMaxSeconds = &maxValue
	}
	return result
}

// findDiagnosticCandidate returns a pointer into the supplied score slice and
// its one-based rank for a matching nonnil provider. Absence returns nil and
// zero; callers retain the slice while using the pointer.
func findDiagnosticCandidate(candidates []CandidateScore, providerID int64) (*CandidateScore, int) {
	for index := range candidates {
		if candidates[index].Provider != nil && candidates[index].Provider.ID == providerID {
			return &candidates[index], index + 1
		}
	}
	return nil, 0
}

// diagnosticScore builds the target's numeric score and effective selection
// display from a nonnil candidate. Nil candidate input returns nil. The
// provider metadata of a present candidate is expected to be nonnil.
//
// Top-K membership and rank remain numeric, while a forced affinity changes
// SelectionMode and any available probability. A forced provider outside Top-K
// can consequently have probability one with InTopK=false and no numeric
// selection weight. The effective settings supply formula weights; the request
// parameter does not change this already computed score.
func diagnosticScore(
	candidate *CandidateScore,
	rank int,
	topK []CandidateScore,
	selection diagnosticTopKSelection,
	effective policy.EffectiveSettings,
	request AdvancedSchedulerScoreDiagnosticRequest,
) *AdvancedSchedulerScoreDiagnosticScore {
	if candidate == nil {
		return nil
	}
	score := &AdvancedSchedulerScoreDiagnosticScore{
		BaseScore:     candidate.BaseScore,
		StickyBonus:   candidate.StickyBonus,
		FinalScore:    candidate.Score,
		Rank:          rank,
		Formula:       diagnosticFormula(candidate, effective.Weights),
		SelectionMode: "top_k_weighted",
	}
	for _, item := range topK {
		if item.Provider == nil || item.Provider.ID != candidate.Provider.ID {
			continue
		}
		score.InTopK = true
		weight := selection.weights[candidate.Provider.ID]
		probability := selection.probabilities[candidate.Provider.ID]
		score.SelectionWeight = &weight
		score.SelectionProbability = &probability
		break
	}
	if selection.forcedProviderID > 0 {
		score.SelectionMode = "sticky_forced_first"
		if probability, found := selection.probabilities[candidate.Provider.ID]; found {
			score.SelectionProbability = &probability
		}
		if candidate.Provider != nil && candidate.Provider.ID == selection.forcedProviderID {
			if weight, found := selection.weights[candidate.Provider.ID]; found {
				score.SelectionWeight = &weight
			}
		}
	}
	return score
}

// diagnosticFormula formats base factor contributions and any present sticky
// bonuses for the displayed candidate. Nil input returns an empty string, and
// all numeric terms use diagnosticFloat's four fractional digits.
func diagnosticFormula(candidate *CandidateScore, weights policy.ScoreWeights) string {
	if candidate == nil {
		return ""
	}
	terms := []string{
		diagnosticFormulaTerm(weights.Priority, candidate.Factors.Priority),
		diagnosticFormulaTerm(weights.Load, candidate.Factors.Load),
		diagnosticFormulaTerm(weights.Queue, candidate.Factors.Queue),
		diagnosticFormulaTerm(weights.ErrorRate, candidate.Factors.ErrorRate),
		diagnosticFormulaTerm(weights.TTFT, candidate.Factors.TTFT),
		diagnosticFormulaTerm(weights.Reset, candidate.Factors.Reset),
		diagnosticFormulaTerm(weights.QuotaHeadroom, candidate.Factors.QuotaHeadroom),
	}
	base := strings.Join(terms, " + ") + " = " + diagnosticFloat(candidate.BaseScore)
	if candidate.StickyBonus == 0 {
		return base
	}
	bonus := make([]string, 0, 2)
	if candidate.PreviousBonus != 0 {
		bonus = append(bonus, diagnosticFloat(candidate.PreviousBonus))
	}
	if candidate.SessionStickyBonus != 0 {
		bonus = append(bonus, diagnosticFloat(candidate.SessionStickyBonus))
	}
	return base + "；粘性加成 " + strings.Join(bonus, " + ") + "；最终 = " + diagnosticFloat(candidate.Score)
}

// diagnosticFormulaTerm formats one weight and factor multiplication for
// formula display.
func diagnosticFormulaTerm(weight, factor float64) string {
	return diagnosticFloat(weight) + "×" + diagnosticFloat(factor)
}

// diagnosticFloat formats a float with four fractional digits for diagnostic
// output.
func diagnosticFloat(value float64) string {
	// Four fractional digits keep displayed factors and formula terms comparable.
	return strconv.FormatFloat(value, 'f', 4, 64)
}

// diagnosticMetrics returns the seven base factor explanations already
// computed for a candidate. Missing candidate or provider returns an allocated
// empty slice; present metrics distinguish observation availability from their
// normalized score factors.
func (s *DiagnosticService) diagnosticMetrics(
	candidate *CandidateScore,
	ranges ScoreRanges,
	weights policy.ScoreWeights,
	now time.Time,
) []AdvancedSchedulerScoreDiagnosticMetric {
	if candidate == nil || candidate.Provider == nil {
		return []AdvancedSchedulerScoreDiagnosticMetric{}
	}
	metrics := []AdvancedSchedulerScoreDiagnosticMetric{
		{
			Key:                  "priority",
			RawValue:             strconv.Itoa(candidate.Provider.Priority),
			Normalization:        diagnosticPriorityNormalization(candidate.Provider.Priority, ranges),
			NormalizedValue:      candidate.Factors.Priority,
			Weight:               weights.Priority,
			WeightedContribution: weights.Priority * candidate.Factors.Priority,
			Available:            true,
			Source:               "provider.Priority",
		},
		diagnosticLoadMetric(candidate, weights),
		diagnosticQueueMetric(candidate, ranges, weights),
		diagnosticErrorRateMetric(candidate, weights),
		diagnosticTTFTMetric(candidate, ranges, weights),
		diagnosticResetMetric(candidate, ranges, weights, now),
		s.diagnosticQuotaMetric(candidate, weights, now),
	}
	return metrics
}

// diagnosticPriorityNormalization formats the pool-relative priority formula
// for display. When the pool has no positive priority range, it explains the
// shared scorer's equal-priority value of one.
func diagnosticPriorityNormalization(priority int, ranges ScoreRanges) string {
	if ranges.MaxPriority <= ranges.MinPriority {
		return "候选优先级相同，使用 1.0000"
	}
	return "1 - (" + strconv.Itoa(priority) + " - " + strconv.Itoa(ranges.MinPriority) + ") / (" + strconv.Itoa(ranges.MaxPriority) + " - " + strconv.Itoa(ranges.MinPriority) + ")"
}

// diagnosticLoadMetric explains the candidate's already computed load factor
// and contribution. Unknown load is marked neutral and unavailable; a known
// sample displays its percentage using the observed-load denominator.
func diagnosticLoadMetric(candidate *CandidateScore, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "load",
		NormalizedValue:      candidate.Factors.Load,
		Weight:               weights.Load,
		WeightedContribution: weights.Load * candidate.Factors.Load,
		Source:               "concurrency.load_rate",
	}
	if !candidate.LoadKnown {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = strconv.Itoa(candidate.LoadInfo.LoadRate) + "%"
	metric.Normalization = "1 - clamp(" + strconv.Itoa(candidate.LoadInfo.LoadRate) + " / 100)"
	return metric
}

// diagnosticQueueMetric explains the candidate's already computed queue factor
// relative to the pool's maximum waiting count. An unknown load sample also
// makes queue observation unavailable and neutral for display.
func diagnosticQueueMetric(candidate *CandidateScore, ranges ScoreRanges, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "queue",
		NormalizedValue:      candidate.Factors.Queue,
		Weight:               weights.Queue,
		WeightedContribution: weights.Queue * candidate.Factors.Queue,
		Source:               "concurrency.waiting_count",
	}
	if !candidate.LoadKnown {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = strconv.Itoa(candidate.LoadInfo.WaitingCount)
	metric.Normalization = "1 - clamp(" + strconv.Itoa(candidate.LoadInfo.WaitingCount) + " / " + strconv.Itoa(ranges.MaxWaiting) + ")"
	return metric
}

// diagnosticErrorRateMetric explains error health from the shared feedback
// observation. Missing feedback displays an unobserved zero error baseline
// with Neutral=true; observed feedback includes its sample count and last
// observation time.
func diagnosticErrorRateMetric(candidate *CandidateScore, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "error_rate",
		NormalizedValue:      candidate.Factors.ErrorRate,
		Weight:               weights.ErrorRate,
		WeightedContribution: weights.ErrorRate * candidate.Factors.ErrorRate,
		Source:               "advanced_scheduler.error_rate_ewma",
	}
	if !candidate.Feedback.HasFeedback {
		metric.RawValue = "0%（未观测）"
		metric.Normalization = "未观测，错误率按 0% 计算：1 - 0 = 1.0000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = "EWMA " + diagnosticFloat(candidate.Feedback.ErrorRate) + "（" + strconv.FormatInt(candidate.Feedback.ErrorSamples, 10) + " 样本）"
	metric.Normalization = "1 - clamp(" + diagnosticFloat(candidate.Feedback.ErrorRate) + ")"
	metric.ObservedAt = candidate.Feedback.LastObservedAt
	return metric
}

// diagnosticTTFTMetric explains observed latency and its pool-relative factor.
// Missing latency is unavailable and neutral; measured equal-range latency
// remains available while its normalized value is marked neutral.
func diagnosticTTFTMetric(candidate *CandidateScore, ranges ScoreRanges, weights policy.ScoreWeights) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "ttft",
		NormalizedValue:      candidate.Factors.TTFT,
		Weight:               weights.TTFT,
		WeightedContribution: weights.TTFT * candidate.Factors.TTFT,
		Source:               "advanced_scheduler.ttft_ewma",
	}
	if !candidate.Feedback.HasTTFT {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	metric.Available = true
	metric.RawValue = diagnosticFloat(candidate.Feedback.TTFT) + " ms（" + strconv.FormatInt(candidate.Feedback.TTFTSamples, 10) + " 样本）"
	if !ranges.HasTTFTSample || ranges.MaxTTFT <= ranges.MinTTFT {
		metric.Normalization = "候选 TTFT 相同，使用中性值 0.5000"
		metric.Neutral = true
	} else {
		metric.Normalization = "1 - clamp((" + diagnosticFloat(candidate.Feedback.TTFT) + " - " + diagnosticFloat(ranges.MinTTFT) + ") / (" + diagnosticFloat(ranges.MaxTTFT) + " - " + diagnosticFloat(ranges.MinTTFT) + "))"
	}
	metric.ObservedAt = candidate.Feedback.LastTTFTAt
	return metric
}

// diagnosticResetMetric explains the reset factor using a future session
// window endpoint. Disabled weight or missing/expired windows are neutral and
// unavailable; equal observed reset ranges use one as their factor.
func diagnosticResetMetric(candidate *CandidateScore, ranges ScoreRanges, weights policy.ScoreWeights, now time.Time) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "reset",
		NormalizedValue:      candidate.Factors.Reset,
		Weight:               weights.Reset,
		WeightedContribution: weights.Reset * candidate.Factors.Reset,
		Source:               "provider.session_window_end",
	}
	if weights.Reset <= 0 {
		metric.RawValue = "权重为 0，未参与评分"
		metric.Normalization = "不适用，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	if candidate.Provider.SessionWindowEnd == nil || !now.Before(*candidate.Provider.SessionWindowEnd) {
		metric.RawValue = "未观测"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	// Express window delay in seconds to match scorer reset ranges.
	remaining := candidate.Provider.SessionWindowEnd.Sub(now).Seconds()
	metric.Available = true
	metric.RawValue = diagnosticFloat(remaining) + " 秒"
	if !ranges.HasResetSample || ranges.MaxResetRemaining <= ranges.MinResetRemaining {
		metric.Normalization = "候选窗口重置时间相同，使用 1.0000"
	} else {
		metric.Normalization = "1 - clamp((" + diagnosticFloat(remaining) + " - " + diagnosticFloat(ranges.MinResetRemaining) + ") / (" + diagnosticFloat(ranges.MaxResetRemaining) + " - " + diagnosticFloat(ranges.MinResetRemaining) + "))"
	}
	return metric
}

// diagnosticQuotaMetric explains platform headroom for supported OpenAI or
// Grok candidates when its weight is positive. A factor exactly 0.5 is
// displayed as unavailable and neutral, including a measured half-headroom
// value.
func (s *DiagnosticService) diagnosticQuotaMetric(candidate *CandidateScore, weights policy.ScoreWeights, now time.Time) AdvancedSchedulerScoreDiagnosticMetric {
	metric := AdvancedSchedulerScoreDiagnosticMetric{
		Key:                  "quota_headroom",
		NormalizedValue:      candidate.Factors.QuotaHeadroom,
		Weight:               weights.QuotaHeadroom,
		WeightedContribution: weights.QuotaHeadroom * candidate.Factors.QuotaHeadroom,
		Source:               "platform.quota_headroom",
	}
	if weights.QuotaHeadroom <= 0 {
		metric.RawValue = "权重为 0，未参与评分"
		metric.Normalization = "不适用，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	if candidate.Provider.Platform != capability.PlatformOpenAI && candidate.Provider.Platform != capability.PlatformGrok {
		metric.RawValue = "当前平台未提供配额余量信号"
		metric.Normalization = "未观测，使用中性值 0.5000"
		metric.Neutral = true
		return metric
	}
	factor := s.quotaHeadroom(candidate.Provider, now)
	// The headroom callback has no separate presence bit. Treating 0.5 as
	// unavailable also labels an actual half-headroom sample neutral in this
	// display.
	metric.Available = factor != 0.5
	metric.Neutral = !metric.Available
	metric.RawValue = diagnosticFloat(factor)
	if metric.Neutral {
		metric.RawValue = "未观测或过期，0.5000"
		metric.Normalization = "未观测，使用中性值 0.5000"
	} else {
		metric.Normalization = "平台适配器提供的 0..1 quota headroom"
	}
	return metric
}

// diagnosticEffectiveSettings reports effective policy values with source
// labels inferred from group pointers and runtime presence. These labels
// describe supplied configuration presence, while Effective has already
// resolved validation and fallback. A historical invalid override can
// therefore have a group label even when its effective weight came from a
// validated global value.
//
// Sticky weighting and subscription switches are labeled runtime when no group
// override is present. Top-K runtime attribution requires a positive runtime
// override, and individual weight labels use map membership. The returned
// display rows carry formatted values rather than the mutable source
// configuration.
func diagnosticEffectiveSettings(group *DiagnosticGroup, runtime policy.RuntimeSettings, effective policy.EffectiveSettings) []AdvancedSchedulerScoreDiagnosticSetting {
	overrides := policy.GroupAdvancedSchedulerOverrides{}
	if group != nil {
		overrides = group.AdvancedSchedulerOverrides
	}
	settingSource := func(groupOverride bool, runtimeOverride bool) string {
		if groupOverride {
			return "group_override"
		}
		if runtimeOverride {
			return "global_runtime"
		}
		return "process_default"
	}
	settings := []AdvancedSchedulerScoreDiagnosticSetting{
		{Key: "sticky_weighted_enabled", Value: strconv.FormatBool(effective.StickyWeightedEnabled), Source: settingSource(overrides.StickyWeightedEnabled != nil, true)},
		{Key: "subscription_priority_enabled", Value: strconv.FormatBool(effective.SubscriptionPriorityEnabled), Source: settingSource(overrides.SubscriptionPriorityEnabled != nil, true)},
		{Key: "ewma_error_rate_alpha", Value: diagnosticFloat(effective.Feedback.ErrorRateAlpha), Source: settingSource(overrides.EWMAErrorRateAlpha != nil, runtime.EwmaErrorRateAlphaSet)},
		{Key: "ewma_ttft_alpha", Value: diagnosticFloat(effective.Feedback.TtftAlpha), Source: settingSource(overrides.EWMATTFTAlpha != nil, runtime.EwmaTTFTAlphaSet)},
		{Key: "sticky_escape_enabled", Value: strconv.FormatBool(effective.StickyEscape.Enabled), Source: settingSource(overrides.StickyEscapeEnabled != nil, runtime.StickyEscapeEnabledSet)},
		{Key: "sticky_escape_ttft_ms", Value: diagnosticFloat(effective.StickyEscape.TtftMs), Source: settingSource(overrides.StickyEscapeTTFTMs != nil, runtime.StickyEscapeTTFTMsSet)},
		{Key: "sticky_escape_error_rate", Value: diagnosticFloat(effective.StickyEscape.ErrorRate), Source: settingSource(overrides.StickyEscapeErrorRate != nil, runtime.StickyEscapeErrorRateSet)},
		{Key: "lb_top_k", Value: strconv.Itoa(effective.TopK), Source: settingSource(overrides.LBTopK != nil, runtime.LbTopKOverride > 0)},
	}
	for _, item := range []struct {
		key   string  // diagnostic setting display key
		value float64 // resolved effective numeric value
		// groupOverride controls the row's group source label through pointer
		// presence, including when validation selected a different effective value.
		groupOverride *float64 // optional group override pointer
		runtimeKey    string   // runtime weight map key
	}{
		{"weight_priority", effective.Weights.Priority, overrides.WeightPriority, "priority"},
		{"weight_load", effective.Weights.Load, overrides.WeightLoad, "load"},
		{"weight_queue", effective.Weights.Queue, overrides.WeightQueue, "queue"},
		{"weight_error_rate", effective.Weights.ErrorRate, overrides.WeightErrorRate, "error_rate"},
		{"weight_ttft", effective.Weights.TTFT, overrides.WeightTTFT, "ttft"},
		{"weight_reset", effective.Weights.Reset, overrides.WeightReset, "reset"},
		{"weight_quota_headroom", effective.Weights.QuotaHeadroom, overrides.WeightQuotaHeadroom, "quota_headroom"},
		{"weight_previous_response", effective.Weights.Previous, overrides.WeightPreviousResponse, "previous_response"},
		{"weight_session_sticky", effective.Weights.SessionSticky, overrides.WeightSessionSticky, "session_sticky"},
	} {
		_, hasRuntimeOverride := runtime.WeightOverrides[item.runtimeKey]
		settings = append(settings, AdvancedSchedulerScoreDiagnosticSetting{
			Key: item.key, Value: diagnosticFloat(item.value), Source: settingSource(item.groupOverride != nil, hasRuntimeOverride),
		})
	}
	return settings
}

// diagnosticPolicySignals reports supplied simulated affinity and enabled
// subscription policy alongside missing request capability context. Every
// result includes request_capabilities as not_evaluated because this input
// lacks endpoint, transport, compact, and session-registration data. Empty
// simulated state becomes ignored.
//
// Affinity details describe administrator-supplied provider IDs, not a
// retrieved session or response binding. A hard-affinity decision may bypass
// subscription narrowing, so an enabled subscription signal can still report
// no active subscription pool. Escape feedback is included when the simulated
// session policy supplies a reason.
func diagnosticPolicySignals(
	group *DiagnosticGroup,
	request AdvancedSchedulerScoreDiagnosticRequest,
	effective policy.EffectiveSettings,
	outcome diagnosticPolicyOutcome,
) []AdvancedSchedulerScoreDiagnosticPolicySignal {
	signals := make([]AdvancedSchedulerScoreDiagnosticPolicySignal, 0, 4)
	if request.PreviousResponseProviderID > 0 {
		state := outcome.previousResponseState
		if state == "" {
			state = "ignored"
		}
		signals = append(signals, AdvancedSchedulerScoreDiagnosticPolicySignal{
			Key: "previous_response_binding", State: state,
			Detail: "仅使用管理员输入的提供商 ID 模拟上一响应粘性；不会读取响应正文或响应标识。",
		})
	}
	if request.StickyProviderID > 0 {
		state := outcome.sessionStickyState
		if state == "" {
			state = "ignored"
		}
		detail := "仅使用管理员输入的提供商 ID 模拟会话粘性；不会读取或写入 session hash。"
		if outcome.stickyEscapeReason != "" {
			detail += " 当前运行时反馈触发粘性逃逸：" + outcome.stickyEscapeReason + "。"
		}
		signals = append(signals, AdvancedSchedulerScoreDiagnosticPolicySignal{
			Key: "session_sticky", State: state,
			Detail: detail,
		})
	}
	if group != nil && effective.SubscriptionPriorityEnabled {
		state := "enabled"
		detail := "当前没有可用订阅提供商，使用完整候选池。"
		if outcome.subscriptionPoolActive {
			state = "active_pool"
			detail = "当前存在可用订阅提供商，排名、Top-K 与概率仅基于订阅池计算。"
		}
		signals = append(signals,
			AdvancedSchedulerScoreDiagnosticPolicySignal{
				Key: "subscription_priority", State: state,
				Detail: detail,
			},
		)
	}
	signals = append(signals, AdvancedSchedulerScoreDiagnosticPolicySignal{
		Key:    "request_capabilities",
		State:  "not_evaluated",
		Detail: "诊断请求未提供端点、传输协议、compact 与会话注册上下文，这些请求级门禁不参与本次结果。",
	})
	return signals
}

// diagnosticPreviousResponseSupported reports whether the first matching
// nonnil record in the supplied list is OpenAI. An absent ID or a first match
// on another platform returns false. Callers choose that list before numeric
// Top-K ranking, so this check alone imposes no Top-K requirement.
func diagnosticPreviousResponseSupported(providers []*DiagnosticProvider, id int64) bool {
	for _, provider := range providers {
		if provider != nil && provider.ID == id {
			return provider.Platform == capability.PlatformOpenAI
		}
	}
	return false
}
