package provider

import (
	"context"
	"slices"
	"sort"

	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// scheduler_score_projection.go builds administrative provider scores from
// full candidate pools and shared load observations.
// An administration page contains only some providers, but their relative
// scores depend on a larger candidate pool. Base scores use the unpaginated
// filter pool when available. An empty filter pool, including a missing reader
// or failed query, uses the current page instead. Advanced group scores use
// each group's independently read schedulable pool. The view returns group
// results only for IDs on the requested page.
//
// Pools are collected before one deduplicated load read over their union, so
// each scoring call receives the same load observations while retaining its
// own group settings. Failed group reads omit that group's scores; failed
// load reads continue with an empty observation map. Base scores remain
// visible only for providers with an advanced group result, and the returned
// group slices are ordered by group ID.

// SchedulerScoreProviders reads full candidate pools for page-independent
// administrative score calculations.
type SchedulerScoreProviders interface {
	// ListProvidersForSchedulerScoreFilter reads the entire pool matching the
	// administration filters. Page size does not restrict these records, so base
	// scores can reflect providers outside the current result page.
	ListProvidersForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]Record, error)
	// ListSchedulableProvidersForAdvancedSchedulerScore reads the schedulable
	// pool for a group and optional platform. Build requests an empty platform
	// to score each advanced group's full candidate set.
	ListSchedulableProvidersForAdvancedSchedulerScore(context.Context, *int64, string) ([]Record, error)
}

// SchedulerLoadRequest identifies a provider and the load denominator used by
// the batch reader.
type SchedulerLoadRequest struct {
	ID             int64 // provider database identifier
	MaxConcurrency int   // effective load denominator
}

// SchedulerLoad contains provider concurrency, waiting, and normalized load
// observations for scoring.
type SchedulerLoad struct {
	ProviderID int64 // observed provider database identifier
	// CurrentConcurrency, WaitingCount, LoadRate: occupied provider slots,
	// queued provider requests, and observed load percentage
	CurrentConcurrency, WaitingCount, LoadRate int
}

// SchedulerScoreOptions supplies synchronous administrative scoring, optional
// load reads, and warning callbacks.
type SchedulerScoreOptions struct {
	// Load reads candidate observations when configured. Nil supplies an empty
	// map to Score; successful maps and their pointers stay stable through all
	// scoring calls in the page build.
	Load func(context.Context, []SchedulerLoadRequest) (map[int64]*SchedulerLoad, error)
	// Score computes pool results from pointers into supplied record slices
	// and the shared batch-load map. Keep their data stable while this required
	// synchronous callback calculates its provider-keyed scores. The callback
	// leaves those inputs unchanged so later pools use the same observations.
	Score func(context.Context, *accessview.GroupConfig, []*Record, map[int64]*SchedulerLoad) map[int64]ProviderSchedulerScore
	// Warn reports failed pool or load reads. NewSchedulerScoreView supplies a
	// no-op callback when this optional warning function is nil.
	Warn func(string, ...any) // failed observation warning callback
}

// SchedulerScoreView retains candidate-pool readers and scoring callbacks for
// administrative result pages.
type SchedulerScoreView struct {
	adminService SchedulerScoreProviders // retained full-pool reader
	options      SchedulerScoreOptions   // retained load and score callbacks
}

// ProviderSchedulerScore holds the numeric and sticky-admission score fields
// displayed for one provider.
type ProviderSchedulerScore struct {
	BaseScore float64 `json:"base_score"` // base weighted score
	// StickyScore, StickyScoreInfinity, and StickyWeightedEnabled describe a
	// hypothetical existing-affinity view. The production scoring adapter
	// supplies a numeric adjusted score when weighted affinity is enabled, and
	// zero plus the infinity flag when it uses hard affinity.
	StickyScore float64 `json:"sticky_score"` // hypothetical affinity score
	// StickyScoreInfinity: unbounded hard-sticky display flag
	StickyScoreInfinity bool `json:"sticky_score_infinity"`
	// StickyWeightedEnabled: weighted sticky scoring flag
	StickyWeightedEnabled bool `json:"sticky_weighted_enabled"`
}

// ProviderSchedulerGroupScore attaches a group's identity to that group's
// provider scoring result.
type ProviderSchedulerGroupScore struct {
	// GroupID is shared by results from one scored group. Build supplies a
	// nonnil pointer for those entries and uses it when ordering each provider's
	// group slice. Treat the pointed-to identifier as immutable while these
	// results are in use because other provider entries retain that same
	// pointer.
	GroupID                *int64 `json:"group_id"`             // scored group identifier
	GroupName              string `json:"group_name,omitempty"` // scored group display name
	ProviderSchedulerScore        // group's scalar score values
}

// NewSchedulerScoreView retains pool and scoring dependencies, defaulting
// Warn to a no-op. Load and the pool reader can be absent; supply Score
// whenever a nonempty schedulable pool can reach calculation.
func NewSchedulerScoreView(reader SchedulerScoreProviders, options SchedulerScoreOptions) *SchedulerScoreView {
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	return &SchedulerScoreView{reader, options}
}

// scoreAdvancedSchedulerPool scores records passing IsSchedulable and group
// OAuth, privacy, and protocol gates, then copies callback values into a new
// map. Empty usable pools return nil; nil loadMap triggers a batch read, while
// supplied maps are passed directly. Keep provider data and supplied loads
// stable through the synchronous Score callback.
func (h *SchedulerScoreView) scoreAdvancedSchedulerPool(ctx context.Context, group *accessview.GroupConfig, providers []Record, loadMap map[int64]*SchedulerLoad) map[int64]ProviderSchedulerScore {
	if len(providers) == 0 {
		return nil
	}

	schedulableProviders := make([]*Record, 0, len(providers))
	for i := range providers {
		provider := &providers[i]
		if !provider.IsSchedulable() || !providerEligibleForSchedulerScoreGroup(provider, group) {
			continue
		}
		schedulableProviders = append(schedulableProviders, provider)
	}
	if len(schedulableProviders) == 0 {
		return nil
	}

	if loadMap == nil {
		loadMap = h.fetchAdvancedSchedulerLoadMap(ctx, schedulableProviders)
	}

	scores := h.options.Score(ctx, group, schedulableProviders, loadMap)
	result := make(map[int64]ProviderSchedulerScore, len(scores))
	for providerID, score := range scores {
		result[providerID] = ProviderSchedulerScore{
			BaseScore:             score.BaseScore,
			StickyScore:           score.StickyScore,
			StickyScoreInfinity:   score.StickyScoreInfinity,
			StickyWeightedEnabled: score.StickyWeightedEnabled,
		}
	}
	return result
}

// providerEligibleForSchedulerScoreGroup checks static group authentication,
// privacy, and client-protocol gates for administrative score pools. Nil groups
// admit all candidates; unhydrated groups with nil AllowedProtocols skip the
// protocol check while explicit empty selections reject all candidates.
func providerEligibleForSchedulerScoreGroup(provider *Record, group *accessview.GroupConfig) bool {
	if provider == nil {
		return false
	}
	if group == nil {
		return true
	}
	if group.RequireOAuthOnly && !provider.IsOAuth() {
		return false
	}
	if group.RequirePrivacySet && !provider.IsPrivacySet() {
		return false
	}
	if group.AllowedProtocols == nil {
		return true
	}
	if len(group.AllowedProtocols) == 0 {
		return false
	}
	protocols := capability.ProviderProtocols{
		Platform: provider.Platform,
		Type:     provider.Type,
		AuthMode: ProtocolAuthMode(provider),
		Enabled:  provider.UpstreamProtocolsForLegacy((ProtocolTarget{Record: provider}).GetAPIProtocol()),
	}
	for _, entry := range group.AllowedProtocols {
		if _, ok := capability.ResolveRoute(protocols, entry, group.ProtocolFallbacks); ok {
			return true
		}
	}
	return false
}

// fetchAdvancedSchedulerLoadMap reads one deduplicated batch using each
// provider's effective load factor. The first occurrence of an ID supplies
// its denominator. Nil loaders and empty inputs return an empty map; errors
// warn and discard the returned batch, while a successful nonnil map is
// passed through.
func (h *SchedulerScoreView) fetchAdvancedSchedulerLoadMap(ctx context.Context, providers []*Record) map[int64]*SchedulerLoad {
	loadMap := map[int64]*SchedulerLoad{}
	if h.options.Load == nil || len(providers) == 0 {
		return loadMap
	}
	seen := make(map[int64]struct{}, len(providers))
	loadReq := make([]SchedulerLoadRequest, 0, len(providers))
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		if _, ok := seen[provider.ID]; ok {
			continue
		}
		seen[provider.ID] = struct{}{}
		loadReq = append(loadReq, SchedulerLoadRequest{
			ID:             provider.ID,
			MaxConcurrency: provider.EffectiveLoadFactor(),
		})
	}
	// Use the empty map on failure so optional load storage does not prevent
	// Score from calculating results for otherwise schedulable candidate pools.
	if batchLoad, err := h.options.Load(ctx, loadReq); err != nil {
		h.options.Warn("advanced_scheduler_score_load_batch_failed", "error", err)
	} else if batchLoad != nil {
		loadMap = batchLoad
	}
	return loadMap
}

// buildAdvancedProviderSchedulerScores calculates base and advanced group
// views for the requested page's provider IDs. An empty page returns nil
// maps, and an absent filter pool uses the page records as its base pool.
//
// Advanced group IDs come from page memberships, then full group pools are
// collected before one load read over all involved records. Each pool is
// scored with that shared observation map; failed group reads omit their
// result, and group scores are restricted to page IDs. Base scores without an
// advanced group result are removed, and remaining group slices are sorted by
// ID for stable display.
func (h *SchedulerScoreView) buildAdvancedProviderSchedulerScores(
	ctx context.Context,
	providers []Record,
	filterPool []Record,
) (map[int64]*ProviderSchedulerScore, map[int64][]ProviderSchedulerGroupScore) {
	if len(providers) == 0 {
		return nil, nil
	}
	if len(filterPool) == 0 {
		filterPool = providers
	}

	pageProviderIDs := make(map[int64]struct{})
	advancedGroups := make(map[int64]*accessview.GroupConfig)
	for i := range providers {
		provider := &providers[i]
		pageProviderIDs[provider.ID] = struct{}{}
		if len(provider.ProviderGroups) == 0 {
			continue
		}
		for _, providerGroup := range provider.ProviderGroups {
			if providerGroup.GroupID > 0 && providerGroup.Group != nil && providerGroup.Group.SchedulerType == "advanced" {
				advancedGroups[providerGroup.GroupID] = providerGroup.Group
			}
		}
	}
	if len(pageProviderIDs) == 0 {
		return nil, nil
	}

	// Collect full group pools first so the single load batch covers base and
	// group candidates together; later scoring calls share that observation map.
	groupIDList := make([]int64, 0, len(advancedGroups))
	for groupID := range advancedGroups {
		groupIDList = append(groupIDList, groupID)
	}
	slices.Sort(groupIDList)

	groupPools := make(map[int64][]Record, len(groupIDList))
	if h.adminService != nil {
		for _, groupID := range groupIDList {
			gid := groupID
			group := advancedGroups[gid]
			if group == nil {
				continue
			}
			pool, err := h.adminService.ListSchedulableProvidersForAdvancedSchedulerScore(ctx, &gid, "")
			if err != nil {
				// Omit this group's scores when its pool cannot be read; scoring a
				// partial result would change the candidate comparison for that group.
				h.options.Warn("advanced_scheduler_group_score_pool_failed", "group_id", gid, "error", err)
				continue
			}
			groupPools[gid] = pool
		}
	}

	loadUnion := make([]*Record, 0, len(filterPool))
	collectAdvancedProviders := func(pool []Record) {
		for i := range pool {
			loadUnion = append(loadUnion, &pool[i])
		}
	}
	collectAdvancedProviders(filterPool)
	for _, pool := range groupPools {
		collectAdvancedProviders(pool)
	}
	loadMap := h.fetchAdvancedSchedulerLoadMap(ctx, loadUnion)

	baseScores := make(map[int64]*ProviderSchedulerScore)
	for providerID, score := range h.scoreAdvancedSchedulerPool(ctx, nil, filterPool, loadMap) {
		copiedScore := score
		baseScores[providerID] = &copiedScore
	}

	groupScoresByProvider := make(map[int64][]ProviderSchedulerGroupScore)
	scoreGroupPool := func(groupID *int64, group *accessview.GroupConfig, groupNameByID map[int64]string, pool []Record) {
		if len(pool) == 0 {
			return
		}
		scores := h.scoreAdvancedSchedulerPool(ctx, group, pool, loadMap)
		for providerID, schedulerScore := range scores {
			if _, ok := pageProviderIDs[providerID]; !ok {
				continue
			}
			groupScore := ProviderSchedulerGroupScore{
				GroupID:                groupID,
				ProviderSchedulerScore: schedulerScore,
			}
			if groupID != nil {
				groupScore.GroupName = groupNameByID[*groupID]
			}
			groupScoresByProvider[providerID] = append(groupScoresByProvider[providerID], groupScore)
		}
	}

	for _, groupID := range groupIDList {
		gid := groupID
		pool, ok := groupPools[gid]
		if !ok {
			continue
		}
		groupNameByID := make(map[int64]string)
		for i := range pool {
			provider := &pool[i]
			for _, providerGroup := range provider.ProviderGroups {
				if providerGroup.GroupID != gid {
					continue
				}
				if providerGroup.Group != nil {
					groupNameByID[gid] = providerGroup.Group.Name
				}
			}
		}
		scoreGroupPool(&gid, advancedGroups[gid], groupNameByID, pool)
	}
	// A base score is displayed alongside an advanced group result. Remove
	// entries whose page memberships produced no scored group, including failed
	// pool reads, so their administration view does not imply group scoring.
	for providerID := range baseScores {
		if _, ok := groupScoresByProvider[providerID]; !ok {
			delete(baseScores, providerID)
		}
	}

	for providerID := range groupScoresByProvider {
		sort.SliceStable(groupScoresByProvider[providerID], func(i, j int) bool {
			left := groupScoresByProvider[providerID][i]
			right := groupScoresByProvider[providerID][j]
			return *left.GroupID < *right.GroupID
		})
	}
	return baseScores, groupScoresByProvider
}

// listProviderSchedulerScoreFilterPool reads the full administration filter
// pool for base scores. Missing readers and failed reads return nil, with
// failures warning; Build then lets its calculator use page records when no
// pool is available.
func (h *SchedulerScoreView) listProviderSchedulerScoreFilterPool(
	ctx context.Context,
	platform, providerType, status, search string,
	groupID int64,
	privacyMode string,
) []Record {
	if h.adminService == nil {
		return nil
	}
	providers, err := h.adminService.ListProvidersForSchedulerScoreFilter(ctx, platform, providerType, status, search, groupID, privacyMode)
	if err != nil {
		// Return no filter pool on failure so the delegated calculator can use the
		// supplied page records while still completing their display view.
		h.options.Warn("advanced_scheduler_filter_score_pool_failed", "error", err)
		return nil
	}
	return providers
}

// Build reads an unpaginated filter pool and returns base plus ordered group
// score views for the supplied page. Failed or absent filter reads let the
// delegated calculator use page records; callbacks run synchronously and
// require stable input data.
func (h *SchedulerScoreView) Build(ctx context.Context, values []Record, platform, kind, status, search string, groupID int64, privacy string) (map[int64]*ProviderSchedulerScore, map[int64][]ProviderSchedulerGroupScore) {
	pool := h.listProviderSchedulerScoreFilterPool(ctx, platform, kind, status, search, groupID, privacy)
	return h.buildAdvancedProviderSchedulerScores(ctx, values, pool)
}
