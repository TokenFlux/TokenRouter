package selection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// gemini.go selects Gemini-compatible execution targets and ranks credentials
// for AI Studio auxiliary endpoints.
// Gemini selection reads providers from the admitted group and may reuse a
// compatible cross-platform session binding. Model scheduling, model support,
// forced-platform input, and local Gemini quota observations filter candidates
// before basic ordering or advanced scoring. The slot-free entry captures the
// requested group and model for later full-record validation.
//
// AI Studio auxiliary endpoints use a credential preference before scheduler
// scores: usable API keys, projectless OAuth, AI Studio OAuth, then other
// credentials. Basic ties also consider provider priority and use time;
// advanced scoring is confined to the best supported rank. Hydration can reject
// changed group membership or model rules after a snapshot candidate was
// ranked, keeping the target associated with its verified record.

// geminiStickySessionTTL sets the lifetime used when refreshing usable Gemini
// session bindings in the shared cache.
const geminiStickySessionTTL = time.Hour

// SelectProviderForModelWithExclusions chooses a slot-free target outside
// excludedIDs through the Gemini core. It captures the supplied group and model
// for hydration, and the final admitted group's scheduler mode controls
// candidate ordering.
func (s *Gemini) SelectProviderForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.ExecutionProvider, error) {
	ctx = withSelectionRequest(ctx, groupID, requestedModel)
	core, scope := s.geminiSelector()
	selected, err := core.SelectOnly(ctx, schedulercore.SelectionInput{
		GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel,
		ExcludedIDs: excludedIDs,
	})
	return scope.oldProvider(selected), err
}

// resolvePlatformAndSchedulingMode validates the admitted group using context
// or the configured lightweight reader. A nonempty forced platform narrows that
// group's pool; the mixed-scheduling flag remains false and otherwise platform
// is empty.
func (s *Gemini) resolvePlatformAndSchedulingMode(ctx context.Context, groupID *int64) (platform string, useMixedScheduling bool, hasForcePlatform bool, group *routing.Group, err error) {
	var read func(context.Context, int64) (*routing.Group, error)
	if s.groupRepo != nil {
		read = s.groupRepo.GetByIDLite
	}
	group, err = currentSelectionGroup(ctx, groupID, read)
	if err != nil {
		return "", false, false, nil, err
	}
	platform, forced := apikey.ForcePlatformFromContext(ctx)
	if forced && platform != "" {
		return platform, false, true, group, nil
	}
	return "", false, false, group, nil
}

// tryStickySessionHit returns a usable group member already bound to cacheKey,
// refreshing its session lifetime. Callers supply the Gemini cache when
// invoking this sticky path; missing sessions, failed reads, and excluded IDs
// return nil.
//
// Unschedulability or model cooldown triggers best-effort binding deletion.
// Other group, platform, model, or quota failures leave the binding available
// for another request that can use it. A usable provider remains selectable
// even if expiration refresh fails, because the cache write is auxiliary to
// current target eligibility.
func (s *Gemini) tryStickySessionHit(
	ctx context.Context,
	groupID *int64,
	sessionHash, cacheKey, requestedModel string,
	excludedIDs map[int64]struct{},
	platform string,
	useMixedScheduling bool,
) *gatewayprovider.ExecutionProvider {
	if sessionHash == "" {
		return nil
	}

	// Sticky affinity is optional. Cache lookup failure leaves this request free
	// to choose another eligible group member through the scheduler's ordinary pool.
	providerID, err := s.cache.GetSessionProviderID(ctx, derefGroupID(groupID), cacheKey)
	if err != nil || providerID <= 0 {
		return nil
	}

	if _, excluded := excludedIDs[providerID]; excluded {
		return nil
	}

	// A binding alone cannot establish current target eligibility. Failed provider
	// reads leave the sticky path empty so the core can inspect other candidates.
	provider, err := s.getSchedulableProvider(ctx, providerID)
	if err != nil {
		return nil
	}

	if shouldClearStickySession(ctx, provider, requestedModel) {
		// An unusable provider or active model cooldown invalidates this affinity.
		// Delete it best-effort so cache failure does not prevent ordinary pool selection.
		_ = s.cache.DeleteSessionProviderID(ctx, derefGroupID(groupID), cacheKey)
		return nil
	}

	if !openAIStickyProviderMatchesGroup(provider, groupID) || !s.isProviderUsableForRequest(ctx, provider, requestedModel, platform, useMixedScheduling) {
		return nil
	}

	// Refresh is auxiliary after target eligibility passed. A cache failure leaves
	// the selected provider usable now, with its stored affinity expiring as before.
	_ = s.cache.RefreshSessionTTL(ctx, derefGroupID(groupID), cacheKey, geminiStickySessionTTL)
	return provider
}

// isProviderUsableForRequest checks model scheduling, support, platform, and
// local quota using individual observations. It delegates to the precheck-aware
// helper with no batch map, so quota admission is read for the target when
// configured.
func (s *Gemini) isProviderUsableForRequest(
	ctx context.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestedModel, platform string,
	useMixedScheduling bool,
) bool {
	return s.isProviderUsableForRequestWithPrecheck(ctx, provider, requestedModel, platform, useMixedScheduling, nil)
}

// isProviderUsableForRequestWithPrecheck applies scheduling and support before
// platform and quota admission. A batch precheck entry supplies that provider's
// observed result; missing entries fall through to the individual Gemini quota
// reader.
func (s *Gemini) isProviderUsableForRequestWithPrecheck(
	ctx context.Context,
	provider *gatewayprovider.ExecutionProvider,
	requestedModel, platform string,
	useMixedScheduling bool,
	precheckResult map[int64]bool,
) bool {
	if !gatewayprovider.ExecutionModelPolicy(provider).Schedulable(ctx, requestedModel) {
		return false
	}

	if requestedModel != "" && !s.isModelSupportedByProvider(ctx, provider, requestedModel) {
		return false
	}

	if !s.isProviderValidForPlatform(provider, platform, useMixedScheduling) {
		return false
	}

	if !s.passesRateLimitPreCheckWithCache(ctx, provider, requestedModel, precheckResult) {
		return false
	}

	return true
}

// isProviderValidForPlatform accepts nonnil targets matching an optional forced
// platform. Empty platform permits any group platform, and useMixedScheduling
// is a compatibility input that does not alter this predicate.
func (s *Gemini) isProviderValidForPlatform(provider *gatewayprovider.ExecutionProvider, platform string, useMixedScheduling bool) bool {
	return provider != nil && (platform == "" || provider.Record.Platform == platform)
}

// passesRateLimitPreCheckWithCache returns a supplied batch quota result or
// delegates an individual Gemini usage check. Absent precheck services or empty
// models pass; read errors are logged while the precheck's returned admission
// decision is retained.
func (s *Gemini) passesRateLimitPreCheckWithCache(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string, precheckResult map[int64]bool) bool {
	if s.quotaPrecheck == nil || requestedModel == "" {
		return true
	}

	if precheckResult != nil {
		if ok, exists := precheckResult[provider.Record.ID]; exists {
			return ok
		}
	}

	// Local usage prediction can fail while the precheck permits dispatch. Retain
	// that admission result and log its paired error for quota-read diagnosis.
	ok, err := s.quotaPrecheck.PreCheckUsage(ctx, gatewayprovider.ExecutionRecord(provider), requestedModel)
	if err != nil {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheck] Provider %d precheck error: %v", provider.Record.ID, err)
	}
	return ok
}

// eligibleGeminiProviders returns pointers to candidates passing exclusions,
// model policy, platform, and quota prechecks. One batch quota observation is
// reused across the pass, and pointers refer to the incoming candidate slice's
// elements.
func (s *Gemini) eligibleGeminiProviders(
	ctx context.Context,
	providers []gatewayprovider.ExecutionProvider,
	requestedModel string,
	excludedIDs map[int64]struct{},
	platform string,
	useMixedScheduling bool,
) []*gatewayprovider.ExecutionProvider {
	precheckResult := s.buildPreCheckUsageResultMap(ctx, providers, requestedModel)
	eligible := make([]*gatewayprovider.ExecutionProvider, 0, len(providers))

	for i := range providers {
		acc := &providers[i]

		if _, excluded := excludedIDs[acc.Record.ID]; excluded {
			continue
		}

		if !s.isProviderUsableForRequestWithPrecheck(ctx, acc, requestedModel, platform, useMixedScheduling, precheckResult) {
			continue
		}
		eligible = append(eligible, acc)
	}

	return eligible
}

// groupUsesAdvancedScheduler resolves the final group's mode from valid
// context, a full snapshot-bound read, or a lightweight reader. Missing
// selectors, invalid IDs, or unavailable reads return false; forcing a platform
// does not change the group's mode.
func (s *Gemini) groupUsesAdvancedScheduler(ctx context.Context, groupID *int64, hasForcePlatform bool) bool {
	if s == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
		return group.UsesAdvancedScheduler()
	}
	if s.schedulerSnapshot != nil {
		// A snapshot-bound full group read can fail independently of admission.
		// Continue to the lightweight reader before deciding that advanced mode is unavailable.
		if group, err := s.readSchedulingGroup(ctx, *groupID); err == nil && group != nil {
			return group.UsesAdvancedScheduler()
		}
	}
	if s.groupRepo == nil {
		return false
	}
	// A failed lightweight read cannot establish the group's advanced mode. The
	// false result leaves this request on the auxiliary entry's basic ordering.
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

// advancedSchedulerStats returns the bound feedback store or creates one when
// absent. Nil selectors return nil; callers using lazy initialization arrange
// the first call before concurrent access because this store assignment is
// unsynchronized.
func (s *Gemini) advancedSchedulerStats() *schedulercore.RuntimeStats {
	if s == nil {
		return nil
	}
	if s.advancedProviderStats == nil {
		s.advancedProviderStats = schedulercore.NewRuntimeStats(time.Now)
	}
	return s.advancedProviderStats
}

// advancedSchedulerEffectiveSettingsForRequest delegates global settings and
// final-group overrides to Parameters. Nil context uses Background, and
// unavailable or basic groups contribute empty overrides so inherited process
// and runtime settings remain effective.
func (s *Gemini) advancedSchedulerEffectiveSettingsForRequest(ctx context.Context, id *int64) policy.EffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	group := schedulerRequestGroup(ctx, id, s.schedulerSnapshot != nil, s.readSchedulingGroup)
	return s.schedulerParameters.Effective(ctx, schedulerGroupOverrides(group))
}

// selectAdvancedGeminiProvider returns the first weighted Top-K target among
// the supplied eligible providers. Empty eligible input or an empty selection
// order returns nil, leaving the caller's basic or no-provider handling
// available.
//
// The score input combines the group's Top-K and sticky-weight option with an
// optional session binding and shared performance observations. A separate
// pointer map associates each scored record with its source target. This path
// supplies no load map and acquires no slot; hard eligibility is the caller's
// responsibility before scoring.
func (s *Gemini) selectAdvancedGeminiProvider(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	cacheKey string,
	eligible []*gatewayprovider.ExecutionProvider,
	settings policy.EffectiveSettings,
) *gatewayprovider.ExecutionProvider {
	if len(eligible) == 0 {
		return nil
	}
	var stickyProviderID int64
	if sessionHash != "" && s.cache != nil {
		// Affinity is optional for scoring. Retain the cache's returned identifier
		// even when its read reports an error, so scoring uses that supplied
		// binding value while continuing the request.
		stickyProviderID, _ = s.cache.GetSessionProviderID(ctx, derefGroupID(groupID), cacheKey)
	}
	input := schedulercore.ScoreInput{
		GroupID:          groupID,
		SessionHash:      cacheKey,
		StickyProviderID: stickyProviderID,
		StickyWeighted:   settings.StickyWeightedEnabled,
		TopK:             settings.TopK,
	}
	// Scored metadata needs its associated execution target after ordering. Use
	// each created score object's pointer as the lookup key for this selection.
	values := make([]*schedulercore.ScoreProvider, len(eligible))
	source := make(map[*schedulercore.ScoreProvider]*gatewayprovider.ExecutionProvider, len(eligible))
	for i, value := range eligible {
		if value == nil {
			continue
		}
		values[i] = &schedulercore.ScoreProvider{ID: value.Record.ID, Platform: value.Record.Platform, Priority: value.Record.Priority, SessionWindowEnd: value.Record.SessionWindowEnd}
		source[values[i]] = value
	}
	candidates, _ := schedulercore.ScoreCandidates(values, nil, s.advancedSchedulerStats(), settings.Weights, input, time.Now())
	selectionOrder := schedulercore.BuildSelectionOrder(candidates, input)
	if len(selectionOrder) == 0 {
		return nil
	}
	return source[selectionOrder[0].Provider]
}

// groupModelUnsupportedErrorIfApplicable returns a typed model rejection when
// the relevant configured pool cannot support the requested name. Empty input,
// blank models, or any supporting relevant provider produces nil for ordinary
// capacity handling.
//
// The relevant pool excludes requested IDs and records failing schedulability,
// platform, or local quota precheck. Batch quota observations are reused for
// the pass so model diagnosis follows the same quota input as eligibility. If
// every relevant record rejects the model, routing builds the advertised model
// list from the supplied candidates.
func (s *Gemini) groupModelUnsupportedErrorIfApplicable(ctx context.Context, providers []gatewayprovider.ExecutionProvider, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixedScheduling bool) error {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(providers) == 0 {
		return nil
	}
	precheckResult := s.buildPreCheckUsageResultMap(ctx, providers, requestedModel)
	hasRelevantProvider := false
	for i := range providers {
		acc := &providers[i]
		if excludedIDs != nil {
			if _, excluded := excludedIDs[acc.Record.ID]; excluded {
				continue
			}
		}
		if !acc.View().IsSchedulable() || !s.isProviderValidForPlatform(acc, platform, useMixedScheduling) || !s.passesRateLimitPreCheckWithCache(ctx, acc, requestedModel, precheckResult) {
			continue
		}
		hasRelevantProvider = true
		if s.isModelSupportedByProvider(ctx, acc, requestedModel) {
			return nil
		}
	}
	if !hasRelevantProvider {
		return nil
	}
	return routing.NewGroupModelRejection(platform, requestedModel, modelRejectionSources(providers))
}

// buildPreCheckUsageResultMap delegates batch Gemini quota observation for this
// candidate slice and requested model. Missing service, empty model, or empty
// input returns nil; errors are logged and any available result map is
// retained.
func (s *Gemini) buildPreCheckUsageResultMap(ctx context.Context, providers []gatewayprovider.ExecutionProvider, requestedModel string) map[int64]bool {
	if s.quotaPrecheck == nil || requestedModel == "" || len(providers) == 0 {
		return nil
	}

	candidates := make([]*gatewayprovider.ExecutionProvider, 0, len(providers))
	for i := range providers {
		candidates = append(candidates, &providers[i])
	}

	// Batch quota readers can return usable observations with an error. Keep that
	// result for per-candidate checks; missing entries permit an individual precheck.
	result, err := s.quotaPrecheck.PreCheckUsageBatch(ctx, gatewayprovider.ExecutionRecordPointers(candidates), requestedModel)
	if err != nil {
		logging.LegacyPrintf("service.gemini_messages_compat", "[Gemini PreCheckBatch] failed: %v", err)
	}
	return result
}

// isModelSupportedByProvider applies execution model support rules for a nonnil
// target using ctx so effort_model_mapping and Antigravity thinking state match
// post-selection hydration.
func (s *Gemini) isModelSupportedByProvider(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string) bool {
	if provider == nil {
		return false
	}
	return gatewayprovider.ExecutionModelPolicy(provider).Supports(ctx, requestedModel)
}

// getSchedulableProvider reads the provider by ID through snapshots when
// configured, otherwise through the repository. Callers apply request
// eligibility afterward; the full-read result and error pass through this
// helper.
func (s *Gemini) getSchedulableProvider(ctx context.Context, providerID int64) (*gatewayprovider.ExecutionProvider, error) {
	if s.schedulerSnapshot != nil {
		return readSnapshotProvider(ctx, s.schedulerSnapshot, providerID)
	}
	return s.providerRepo.GetByID(ctx, providerID)
}

// hydrateSelectedProvider obtains a full target after snapshot selection,
// preferring the repository when available. Nil providers or an absent snapshot
// return the input; read errors propagate and missing targets return
// ErrNoAvailableProviders.
//
// Captured selection inputs constrain the later record's group membership,
// model scheduling, and model support. A final group already present in context
// takes precedence over the initially captured group pointer for that check.
// The supplied model is retained for provider policy, preventing changed
// full-record eligibility from being hidden by the lightweight candidate.
func (s *Gemini) hydrateSelectedProvider(ctx context.Context, provider *gatewayprovider.ExecutionProvider) (*gatewayprovider.ExecutionProvider, error) {
	if provider == nil || s.schedulerSnapshot == nil {
		return provider, nil
	}
	var hydrated *gatewayprovider.ExecutionProvider
	var err error
	if s.providerRepo != nil {
		hydrated, err = s.providerRepo.GetByID(ctx, provider.Record.ID)
	} else {
		hydrated, err = readSnapshotProvider(ctx, s.schedulerSnapshot, provider.Record.ID)
	}
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, schedulercore.ErrNoAvailableProviders
	}
	if input, ok := ctx.Value(selectionRequestKey{}).(selectionRequest); ok {
		groupID := input.groupID
		if group, ok := requeststate.GroupFromContext(ctx); ok && group != nil {
			groupID = &group.ID
		}
		model := input.model
		policy := gatewayprovider.ExecutionModelPolicy(hydrated)
		if !openAIStickyProviderMatchesGroup(hydrated, groupID) || !policy.Schedulable(ctx, model) || !policy.Supports(ctx, model) {
			return nil, schedulercore.ErrNoAvailableProviders
		}
	}
	return hydrated, nil
}

// listSchedulableProvidersOnce reads the positive group's candidates through
// snapshots or the configured repository. Empty platform queries the known
// platform set, while absent or nonpositive group IDs return nil without
// querying providers.
func (s *Gemini) listSchedulableProvidersOnce(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]gatewayprovider.ExecutionProvider, error) {
	if groupID == nil || *groupID <= 0 {
		return nil, nil
	}
	if s.schedulerSnapshot != nil {
		providers, _, err := readSnapshotProviders(ctx, s.schedulerSnapshot, groupID, platform, hasForcePlatform)
		return providers, err
	}
	if platform == "" {
		return s.providerRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, capability.ProviderPlatforms())
	}
	return s.providerRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
}

// HasAntigravityProviders reports whether the group has any schedulable
// Antigravity candidates in its configured source. Candidate read failures
// propagate, while absent groups or empty platform pools return false.
func (s *Gemini) HasAntigravityProviders(ctx context.Context, groupID *int64) (bool, error) {
	providers, err := s.listSchedulableProvidersOnce(ctx, groupID, capability.PlatformAntigravity, false)
	if err != nil {
		return false, err
	}
	return len(providers) > 0, nil
}

// SelectProviderForAIStudioEndpoints chooses a group-bound Gemini target for
// auxiliary AI Studio endpoints. Admission is validated before reading
// candidates, and an empty pool returns an error rather than a target.
//
// Credential rank prefers a populated API key, projectless OAuth, AI Studio
// OAuth, then other OAuth. Equal ranks compare provider priority and last-use
// time, with never-used OAuth resolving a remaining type tie. Advanced mode
// scores only the best rank below the sentinel rank; when scoring has no
// result, the basic choice remains for hydration. Sentinel-rank records can
// still be the basic choice when the entire pool has that rank.
func (s *Gemini) SelectProviderForAIStudioEndpoints(ctx context.Context, groupID *int64) (*gatewayprovider.ExecutionProvider, error) {
	var read func(context.Context, int64) (*routing.Group, error)
	if s.groupRepo != nil {
		read = s.groupRepo.GetByIDLite
	}
	current, groupErr := currentSelectionGroup(ctx, groupID, read)
	if groupErr != nil {
		return nil, groupErr
	}
	if current != nil {
		ctx = requeststate.WithGroup(ctx, current)
	}
	if group, ok := s.resolveAdvancedSchedulerGroup(ctx, groupID); ok {
		ctx = requeststate.WithGroup(ctx, group)
	}
	providers, err := s.listSchedulableProvidersOnce(ctx, groupID, capability.PlatformGemini, true)
	if err != nil {
		return nil, fmt.Errorf("query providers failed: %w", err)
	}
	if len(providers) == 0 {
		return nil, errors.New("no available Gemini providers")
	}

	// Rank 999 excludes this class from advanced refinement, while basic ordering
	// can still return it when all candidates have that credential rank.
	rank := func(a *gatewayprovider.ExecutionProvider) int {
		if a == nil {
			return 999
		}
		switch a.Record.Type {
		case capability.ProviderTypeAPIKey:
			if strings.TrimSpace(a.View().GetCredential("api_key")) != "" {
				return 0
			}
			return 9
		case capability.ProviderTypeOAuth:
			if strings.TrimSpace(a.View().GetCredential("project_id")) == "" {
				return 1
			}
			if strings.TrimSpace(a.View().GetCredential("oauth_type")) == "ai_studio" {
				return 2
			}

			return 3
		case capability.ProviderTypeServiceAccount:

			return 999
		default:
			return 10
		}
	}

	var selected *gatewayprovider.ExecutionProvider
	for i := range providers {
		acc := &providers[i]
		if selected == nil {
			selected = acc
			continue
		}

		// AI Studio endpoint compatibility ranks credentials ahead of use history.
		// Compare priority and recency only within the same credential rank for this pool.
		r1, r2 := rank(acc), rank(selected)
		if r1 < r2 {
			selected = acc
			continue
		}
		if r1 > r2 {
			continue
		}

		if acc.Record.Priority < selected.Record.Priority {
			selected = acc
		} else if acc.Record.Priority == selected.Record.Priority {
			switch {
			case acc.Record.LastUsedAt == nil && selected.Record.LastUsedAt != nil:
				selected = acc
			case acc.Record.LastUsedAt != nil && selected.Record.LastUsedAt == nil:

			case acc.Record.LastUsedAt == nil && selected.Record.LastUsedAt == nil:
				if acc.Record.Type == capability.ProviderTypeOAuth && selected.Record.Type != capability.ProviderTypeOAuth {
					selected = acc
				}
			default:
				if acc.Record.LastUsedAt.Before(*selected.Record.LastUsedAt) {
					selected = acc
				}
			}
		}
	}

	if selected == nil {
		return nil, errors.New("no available Gemini providers")
	}

	// Advanced scores refine candidates in the best credential rank. Keep the
	// basic choice if scoring cannot supply a target, so hydration still has its candidate.
	if s.groupUsesAdvancedScheduler(ctx, groupID, false) {
		bestRank := rank(selected)
		if bestRank < 999 {
			eligible := make([]*gatewayprovider.ExecutionProvider, 0, len(providers))
			for i := range providers {
				provider := &providers[i]
				if rank(provider) == bestRank {
					eligible = append(eligible, provider)
				}
			}
			if advanced := s.selectAdvancedGeminiProvider(
				ctx,
				groupID,
				"",
				"",
				eligible,
				s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID),
			); advanced != nil {
				selected = advanced
			}
		}
	}
	return s.hydrateSelectedProvider(ctx, selected)
}

// resolveAdvancedSchedulerGroup obtains the final group for Gemini auxiliary
// entry points. Valid matching context wins, then full snapshot-bound reads and
// lightweight repository reads are tried; unavailable or failed lookups return
// false.
func (s *Gemini) resolveAdvancedSchedulerGroup(ctx context.Context, groupID *int64) (*routing.Group, bool) {
	if s == nil || groupID == nil || *groupID <= 0 {
		return nil, false
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
		return group, true
	}
	if s.schedulerSnapshot != nil {
		// A snapshot-bound full group read can fail independently of admission.
		// Continue to the lightweight reader before deciding that advanced mode is unavailable.
		if group, err := s.readSchedulingGroup(ctx, *groupID); err == nil && group != nil {
			return group, true
		}
	}
	if s.groupRepo == nil {
		return nil, false
	}
	// Auxiliary preparation accepts group data only after a successful read. A
	// false result keeps the caller from installing this returned record as its request group.
	group, err := s.groupRepo.GetByIDLite(ctx, *groupID)
	return group, err == nil && group != nil
}

// readSchedulingGroup reads the full group settings used by Gemini advanced
// mode. An absent group repository returns nil without error so optional
// group-setting resolution can use its missing-group behavior.
func (s *Gemini) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.groupRepo == nil {
		return nil, nil
	}
	return s.groupRepo.GetByID(ctx, id)
}
