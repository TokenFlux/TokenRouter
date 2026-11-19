package routing

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// pricing_config_service.go manages shared pricing persistence and publishes
// generation-fenced snapshots for group-model lookups.
// Shared prices use a group association and complete normalized model identity,
// while group routing policy is read separately. Published snapshots contain
// cloned configuration graphs, exact indexes, and configuration-ordered
// wildcard entries; public reads clone selected values before exposing editable
// storage, while routing and billing metadata lookups borrow read-only snapshot
// pointers instead of cloning full card tables.
//
// A cache rebuild detaches request cancellation, installs a database timeout,
// and captures cacheGen under cacheMu before reading storage. Invalidation
// advances cacheGen, clears the shared pointer, forgets the singleflight key,
// and triggers a fresh load so an older in-flight builder started before
// invalidation cannot overwrite newer snapshots or publish stale error caches.
//
// Management writes validate working settings and cards through
// pricing_validation.go before repository persistence, then refresh prices and
// invalidate affected group authentication snapshots. Reload and refresh
// failures occur after persistent writes and do not imply rollback.

// Pricing snapshot lifetimes bound normal cache reuse, empty recovery snapshots
// after read errors, and the detached database work shared by concurrent
// callers.
const (
	pricingConfigCacheTTL       = 10 * time.Minute
	pricingConfigErrorTTL       = 5 * time.Second // Short cache lifetime after a database error.
	pricingConfigCacheDBTimeout = 10 * time.Second
)

// Shared-pricing errors distinguish missing configurations, duplicate names,
// and groups already assigned elsewhere so administrative callers can report
// the conflicting resource.
var (
	ErrPricingConfigNotFound       = infraerrors.NotFound("PRICING_CONFIG_NOT_FOUND", "price configuration not found")
	ErrPricingConfigExists         = infraerrors.Conflict("PRICING_CONFIG_EXISTS", "price configuration name already exists")
	ErrGroupAlreadyInPricingConfig = infraerrors.Conflict(
		"GROUP_ALREADY_IN_PRICING_CONFIG",
		"one or more groups already belong to another price configuration",
	)
)

type (
	// GroupAuthInvalidator expires Key authentication snapshots affected by
	// committed group configuration.
	GroupAuthInvalidator interface {
		// InvalidateAuthCacheByGroupID expires Key authentication snapshots affected
		// by a saved group's configuration. Management callers invoke it after
		// persistence succeeds, and the invalidator owns any distributed notification
		// behavior.
		InvalidateAuthCacheByGroupID(context.Context, int64)
	}
	// PricingConfigOptions supplies group policy reads, validation dependencies,
	// and cache diagnostics.
	PricingConfigOptions struct {
		// ReadGroup supplies group policy independently of shared price rows. Nil
		// returns no policy view; read errors propagate to callers and the service's
		// restriction check rejects the model.
		ReadGroup func(context.Context, int64) (*Group, error) // optional current group policy reader
		// Warn receives storage and lookup diagnostics synchronously on the
		// generating operation. Nil suppresses reporting through the service's warn
		// helper. During rebuild warnings, callbacks return without reentering cache
		// loads or rebuilds on this service; a singleflight builder still needs to
		// finish before its waiters can proceed.
		Warn func(string, ...any) // optional synchronous pricing diagnostic callback
		// Now supplies rebuild timestamps and expiration decisions.
		// NewPricingConfigService installs time.Now when this callback is omitted
		// from the selected options value.
		Now func() time.Time // cache age time source
		// LoadLocation is required when validating nonempty time-pricing periods. The
		// constructor leaves it nil when absent, and that validation returns a
		// missing-loader error.
		LoadLocation func(string) (*time.Location, error) // time-pricing timezone validation loader
	}
)

// PricingConfigRepository persists shared settings, group memberships, model
// cards, and cost rules.
type PricingConfigRepository interface {
	// Create saves shared settings, group associations, model cards, and
	// provider-cost rules together. The supplied configuration receives its
	// persistent identity as rows are inserted.
	//
	// The PostgreSQL repository uses one transaction for the aggregate save, so a
	// later child or association failure rolls back preceding writes. Generated
	// identities assigned to the in-memory value remain after a failed
	// transaction; callers discard or reload that working state before retrying an
	// uncertain result.
	Create(ctx context.Context, pricingConfig *PricingConfig) error
	// GetByID reads a disposable configuration value with memberships, cards, and
	// provider-cost rules. Missing parent rows return ErrPricingConfigNotFound,
	// and a child read failure returns an error before the assembled configuration
	// is exposed. PricingConfigService.Update merges into this object before final
	// validation, so implementations detach retained snapshots before returning
	// it.
	GetByID(ctx context.Context, id int64) (*PricingConfig, error)
	// Update saves shared settings and supplied child collections for the
	// configuration ID. Nil collections retain stored data, while non-nil empty
	// collections clear it.
	//
	// The PostgreSQL aggregate write uses one transaction for scalar fields, group
	// links, user-price cards, and provider-cost rules. Child replacement can
	// assign new identities into the input before commit, and rollback does not
	// reverse those assignments. Callers keep the working value disposable until
	// the persistent result is known.
	Update(ctx context.Context, pricingConfig *PricingConfig) error
	// Delete removes a shared configuration and its linked child records. Missing
	// parent rows return ErrPricingConfigNotFound; service callers capture group
	// IDs first and request pricing and authentication refresh after successful
	// removal.
	Delete(ctx context.Context, id int64) error
	// List returns a filtered configuration page with its pagination metadata.
	List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]PricingConfig, *pagination.PaginationResult, error)
	// ListAll returns stored configurations with memberships and nested prices for
	// cache publication. Disabled rows remain included for later active filtering;
	// implementations honor the supplied context deadline so shared database reads
	// can finish within their budget.
	ListAll(ctx context.Context) ([]PricingConfig, error)
	// ExistsByName reports whether a stored configuration uses the supplied name.
	ExistsByName(ctx context.Context, name string) (bool, error)
	// ExistsByNameExcluding checks name uniqueness while ignoring the current
	// configuration ID.
	ExistsByNameExcluding(ctx context.Context, name string, excludeID int64) (bool, error)

	// GetGroupIDs returns persistent group IDs associated with the specified
	// configuration.
	GetGroupIDs(ctx context.Context, pricingConfigID int64) ([]int64, error)
	// SetGroupIDs replaces the configuration's complete group-association set,
	// clearing it for empty input. Its PostgreSQL implementation uses the
	// connection pool for deletion followed by insertion.
	//
	// Those statements can commit separately, so an insertion error may leave
	// prior links removed. Aggregate Create and Update perform corresponding
	// replacement within their own transaction. Callers requiring a coordinated
	// settings and membership change use that aggregate path rather than
	// interpreting a SetGroupIDs error as evidence of rollback.
	SetGroupIDs(ctx context.Context, pricingConfigID int64, groupIDs []int64) error
	// GetPricingConfigIDByGroupID reads the associated configuration ID regardless
	// of its activity status. An unassociated group returns zero without an error,
	// leaving active filtering and default-price selection to the consuming
	// service.
	GetPricingConfigIDByGroupID(ctx context.Context, groupID int64) (int64, error)
	// GetGroupsInOtherPricingConfigs returns supplied groups linked to a different
	// configuration ID. Empty input skips the read; result ordering is unspecified
	// and callers use presence to reject association conflicts.
	GetGroupsInOtherPricingConfigs(ctx context.Context, pricingConfigID int64, groupIDs []int64) ([]int64, error)

	// ListModelPricing returns a configuration's cards with their stored
	// intervals. PostgreSQL turns malformed model-name JSON into an empty match
	// list, but invalid time-pricing JSON or interval-loading errors discard the
	// assembled read result.
	ListModelPricing(ctx context.Context, pricingConfigID int64) ([]ModelPricingEntry, error)
	// CreateModelPricing inserts one card and then its intervals, assigning
	// generated identity and timestamps to the supplied working value. The
	// PostgreSQL implementation executes those statements through the connection
	// pool.
	//
	// A child failure can leave its parent and earlier intervals committed, so
	// callers reload the known persistent identity before deciding how to retry.
	// Aggregate configuration creation and whole-list replacement provide
	// transactional paths when the full price tree needs one commit scope.
	CreateModelPricing(ctx context.Context, pricing *ModelPricingEntry) error
	// UpdateModelPricing replaces one card's names and price columns using its
	// persistent ID. PostgreSQL leaves existing intervals unchanged, defaults an
	// empty billing mode to token, and reports a missing card when no row is
	// updated.
	UpdateModelPricing(ctx context.Context, pricing *ModelPricingEntry) error
	// DeleteModelPricing removes one card and its foreign-key-owned intervals.
	// PostgreSQL treats an absent ID as success because it ignores the
	// affected-row count, while statement failures are returned.
	DeleteModelPricing(ctx context.Context, id int64) error
	// ReplaceModelPricing replaces the configuration's whole card list, clearing
	// it when the supplied list is empty. The PostgreSQL implementation deletes
	// old parent cards and inserts replacements in one transaction.
	//
	// Foreign-key cascades remove old intervals before new parent and interval
	// rows are inserted. A failed replacement rolls back persistent deletion and
	// insertion, while generated identities already assigned to the input slice
	// remain in memory. Callers discard or reload that working slice after a
	// failed transaction.
	ReplaceModelPricing(ctx context.Context, pricingConfigID int64, pricingList []ModelPricingEntry) error
}

// pricingModelKey identifies a normalized model card within one saved group.
type pricingModelKey struct {
	// groupID and model form the exact-card lookup identity. model uses shared
	// price-name normalization, and provider platform is absent from this key so
	// associated groups share the same card namespace.
	groupID int64  // saved routing group ID
	model   string // normalized complete model identity
}

// wildcardPricingEntry associates a normalized model prefix with its cached
// configuration card.
type wildcardPricingEntry struct {
	prefix string // normalized model-name match prefix
	// pricing points into the cloned configuration graph indexed by the snapshot.
	// Readers keep the card immutable and clone it before exposing editable
	// values.
	pricing *ModelPricingEntry // borrowed cached model card
}

// pricingConfigCache indexes one cloned configuration graph for immutable
// published lookup.
type pricingConfigCache struct {
	// pricingByGroupModel, wildcardByGroup, pricingConfigByGroupID, and byID point
	// into the same cloned configuration graph. Builders finish all indexes before
	// publication, and readers keep both current and replaced graphs immutable
	// because another lookup may still retain either one.
	pricingByGroupModel map[pricingModelKey]*ModelPricingEntry // exact group-model card index
	// wildcardByGroup preserves configuration order for prefix matching.
	// lookupPricing uses the first eligible match, allowing legacy overlapping
	// cards to retain that order.
	wildcardByGroup map[int64][]*wildcardPricingEntry // ordered group wildcard-card lists

	pricingConfigByGroupID map[int64]*PricingConfig // group configuration association index

	byID map[int64]*PricingConfig // configuration persistent-ID lookup index
	// loadedAt controls the whole snapshot's lifetime. Error snapshots backdate
	// this origin so the normal expiration comparison gives their empty result a
	// shorter reuse window.
	loadedAt time.Time // snapshot cache age origin
}

// GroupMappingResult contains group model decisions with independent billing
// and display metadata.
type GroupMappingResult struct {
	RestrictionModelSource string // request-stage model restriction choice
	RestrictModels         bool   // enable model allowlist enforcement
	// MappedModel begins as the supplied request name. ResolveGroupMapping
	// replaces it from group policy, and Mapped records whether the result differs
	// from that original input.
	MappedModel     string // model after group mapping
	PricingConfigID int64  // associated configuration ID; zero means absent
	// Mapped records whether ResolveGroupMapping changed the supplied request
	// name. ToUsageFields and the chain builder use this flag to decide whether to
	// consume MappedModel.
	Mapped bool // group mapping changed name
	// BillingModelSource comes from active pricing configuration.
	// ResolveGroupMapping defaults blank configured values to group_mapped, while
	// absent pricing metadata leaves this field empty.
	BillingModelSource string // requested, upstream, or group_mapped price stage
	// ClientModel and APIKeyRedirected control inclusion of the pre-redirection
	// name in the display chain. Billing and OriginalModel still use the caller's
	// redirected request name.
	ClientModel      string // original client display model
	APIKeyRedirected bool   // include client alias in chain
}

// PricingConfigService manages shared configuration and publishes immutable
// group-model price indexes.
type PricingConfigService struct {
	options    PricingConfigOptions    // group reader and cache callbacks
	validation PricingConfigValidation // shared model-card validation dependencies
	repo       PricingConfigRepository // shared configuration persistence reader
	// authCacheInvalidator is invoked after successful management writes for
	// affected groups. Nil skips old-membership reads needed solely for that
	// invalidation phase.
	authCacheInvalidator GroupAuthInvalidator // optional group authentication invalidator

	// cache, cacheMu, cacheGen, and cacheSF coordinate shared rebuilds and
	// invalidations. cacheMu protects cacheGen advances and snapshot publication
	// checks so builders started in an older generation cannot overwrite a newer
	// snapshot or clear a concurrent invalidation; lock-free readers load
	// immutable *pricingConfigCache pointers from cache directly.
	cache    atomic.Value       // published pricing snapshot pointer
	cacheMu  sync.Mutex         // snapshot publication and generation mutex
	cacheGen uint64             // monotonic invalidation generation counter
	cacheSF  singleflight.Group // cache rebuild coalescing group
}

// pricingConfigLookup keeps a group configuration attached to its selected
// cache snapshot.
type pricingConfigLookup struct {
	// cache and pricingConfig belong to the same published graph for this lookup.
	// Keeping both together prevents a card query from accidentally using indexes
	// from another snapshot.
	cache         *pricingConfigCache // borrowed immutable lookup snapshot
	pricingConfig *PricingConfig      // borrowed associated configuration value
}

// CreatePricingConfigInput contains shared settings, memberships, and prices
// for a new configuration.
type CreatePricingConfigInput struct {
	BillingSettingsPatch        // optional shared billing setting updates
	Name                 string // new configuration display name
	Description          string // new configuration description text
	// GroupIDs is attached to the creation working configuration. Group conflicts
	// are checked before persistence, and repository constraints decide concurrent
	// association races.
	GroupIDs []int64 // initial associated group IDs
	// ModelPricing is attached to the working configuration and validated in
	// place. Empty time objects can be cleared, and repository creation can assign
	// card identities into this slice.
	ModelPricing []ModelPricingEntry // initial shared model-price cards

	// BillingModelSource defaults to group_mapped when empty during creation.
	// Supplied nonempty values are carried into storage and interpreted by runtime
	// price-stage selection.
	BillingModelSource string // configured price model stage

	// ProviderStatsPricingRules is attached to the working value and each rule's
	// cards are validated. Callers keep the collection stable while repository
	// writes can assign child identities.
	ProviderStatsPricingRules []ProviderStatsPricingRule // initial provider-cost pricing rules
}

// UpdatePricingConfigInput contains optional shared-configuration settings and
// child collection replacements. Supplied GroupIDs, ModelPricing, and
// ProviderStatsPricingRules collections are attached to the working
// configuration; callers keep them stable while validation and storage can
// mutate card elements.
type UpdatePricingConfigInput struct {
	BillingSettingsPatch // optional shared billing setting updates
	// Name retains the saved value when empty. A different nonempty name is
	// checked against other configurations before assignment to the working value.
	Name string // replacement configuration display name
	// Description retains saved text when nil. A pointer to an empty string clears
	// the description during input merging.
	Description *string // optional replacement description text
	// Status retains the saved value when empty. A nonempty value replaces it and
	// active filtering continues to use the literal active status.
	Status string // replacement configuration activity status
	// GroupIDs retains saved memberships when nil. A supplied empty list clears
	// them, and nonempty replacement IDs are checked for association conflicts.
	GroupIDs *[]int64 // optional associated-group collection replacement
	// ModelPricing retains saved cards when nil. Supplied empty or nonempty slices
	// replace the collection, and validation may normalize their time-pricing
	// objects.
	ModelPricing *[]ModelPricingEntry // optional shared-card collection replacement

	// BillingModelSource retains the saved stage when empty. A nonempty value is
	// assigned to the working configuration before runtime consumers select the
	// corresponding model.
	BillingModelSource string // replacement price model stage

	// ProviderStatsPricingRules retains stored rules when nil. Supplied empty or
	// nonempty collections replace them, and each rule's cards are validated
	// before persistence.
	ProviderStatsPricingRules *[]ProviderStatsPricingRule // optional provider-cost rule replacement
}

// normalizePriceModelName trims outer whitespace and lowercases the complete
// price identity.
func normalizePriceModelName(model string) string {
	return pricing.NormalizePriceModelName(model)
}

// BuildModelMappingChain builds an audit display from client, redirected
// request, group-mapped, and execution stages. Callers supply the redirected
// request and actual upstream names; the client name participates only when
// APIKeyRedirected is true.
//
// Mapped controls inclusion of the group stage, then the shared chain builder
// trims values and keeps each distinct spelling once in first-occurrence order.
// This can omit a later return to an earlier name, so the chain is a compact
// display of distinct identities rather than a complete event trace. Fewer than
// two distinct names produces an empty string.
func (r GroupMappingResult) BuildModelMappingChain(reqModel, upstreamModel string) string {
	stages := make([]string, 0, 4)
	if r.APIKeyRedirected {
		stages = append(stages, r.ClientModel)
	}
	stages = append(stages, reqModel)
	if r.Mapped {
		stages = append(stages, r.MappedModel)
	}
	stages = append(stages, upstreamModel)
	return BuildModelMappingChain(stages...)
}

// ToUsageFields records the request and group-mapped identities together with
// billing metadata. Callers pass the request model after Key redirection and
// the actual executed upstream model.
//
// Mapped selects whether GroupMappedModel comes from the mapping result or
// remains the request model. The original client alias participates only in the
// display chain when APIKeyRedirected is set, keeping that alias separate from
// OriginalModel used by billing records. Configuration identity and
// billing-source metadata are copied from the supplied mapping result.
func (r GroupMappingResult) ToUsageFields(reqModel, upstreamModel string) PricingUsageFields {
	groupMappedModel := reqModel
	if r.Mapped {
		groupMappedModel = r.MappedModel
	}
	return PricingUsageFields{
		PricingConfigID:    r.PricingConfigID,
		OriginalModel:      reqModel,
		GroupMappedModel:   groupMappedModel,
		BillingModelSource: r.BillingModelSource,
		ModelMappingChain:  r.BuildModelMappingChain(reqModel, upstreamModel),
	}
}

// NewPricingConfigService retains repositories, invalidation, and the first
// optional options value. Later options are ignored, a nil clock becomes
// time.Now, and configured time-pricing periods require an explicitly supplied
// LoadLocation callback for validation.
func NewPricingConfigService(repo PricingConfigRepository, authCacheInvalidator GroupAuthInvalidator, options ...PricingConfigOptions) *PricingConfigService {
	optionsValue := PricingConfigOptions{Now: time.Now}
	if len(options) > 0 {
		optionsValue = options[0]
		if optionsValue.Now == nil {
			optionsValue.Now = time.Now
		}
	}
	s := &PricingConfigService{
		options: optionsValue, validation: PricingConfigValidation{LoadLocation: optionsValue.LoadLocation},
		repo:                 repo,
		authCacheInvalidator: authCacheInvalidator,
	}
	return s
}

// loadCache returns an unexpired shared snapshot or joins one cache rebuild
// through singleflight. Internal callers treat its maps and values as immutable
// and clone selected configuration or card data before exposing it.
//
// The closure checks expiry again after joining so a snapshot published by
// another caller can be reused. Database work receives a detached bounded
// context in buildCache, so caller cancellation does not release a singleflight
// waiter. A failed rebuild returns its error to joined callers and publishes a
// short-lived empty snapshot for subsequent lookups.
func (s *PricingConfigService) loadCache(ctx context.Context) (*pricingConfigCache, error) {
	if cached, ok := s.cache.Load().(*pricingConfigCache); ok && cached != nil {
		if s.options.Now().Sub(cached.loadedAt) < pricingConfigCacheTTL {
			return cached, nil
		}
	}

	result, err, _ := s.cacheSF.Do("pricing_config_cache", func() (any, error) {
		// A caller can publish after the outer expiry check but before this generation
		// starts, so recheck the pointer before creating another database-backed
		// snapshot.
		if cached, ok := s.cache.Load().(*pricingConfigCache); ok && cached != nil {
			if s.options.Now().Sub(cached.loadedAt) < pricingConfigCacheTTL {
				return cached, nil
			}
		}
		return s.buildCache(ctx)
	})
	if err != nil {
		return nil, err
	}
	cache, ok := result.(*pricingConfigCache)
	if !ok {
		return nil, fmt.Errorf("unexpected cache type")
	}
	return cache, nil
}

// newEmptyPricingConfigCache creates initialized indexes for a pricing snapshot
// before publication.
func newEmptyPricingConfigCache() *pricingConfigCache {
	return &pricingConfigCache{
		pricingByGroupModel: make(map[pricingModelKey]*ModelPricingEntry),
		wildcardByGroup:     make(map[int64][]*wildcardPricingEntry),

		pricingConfigByGroupID: make(map[int64]*PricingConfig),
		byID:                   make(map[int64]*PricingConfig),
	}
}

// expandPricingToCache indexes a configuration's model identities for one
// associated group against their shared card values. Callers supply an
// unpublished cache and cloned configuration, keeping its indexed pointers
// immutable after publication.
//
// Asterisk detection uses the supplied suffix before trimming and lowercasing,
// matching validation's classification of the same spelling. Exact normalized
// keys replace earlier cards, while wildcard prefixes append in configuration
// order so lookupPricing can select the first eligible legacy overlapping card.
func expandPricingToCache(cache *pricingConfigCache, config *PricingConfig, groupID int64) {
	for i := range config.ModelPricing {
		entry := &config.ModelPricing[i]
		for _, model := range entry.Models {
			// Check the supplied suffix before normalization so validation and cache
			// construction agree when whitespace after an asterisk makes the name an
			// exact identity.
			if before, ok := strings.CutSuffix(model, "*"); ok {
				cache.wildcardByGroup[groupID] = append(cache.wildcardByGroup[groupID], &wildcardPricingEntry{prefix: normalizePriceModelName(before), pricing: entry})
			} else {
				cache.pricingByGroupModel[pricingModelKey{groupID: groupID, model: normalizePriceModelName(model)}] = entry
			}
		}
	}
}

// storeErrorCache publishes an empty snapshot after a failed configuration
// read when cacheGen still matches gen. All lookup maps are initialized so
// later requests within the same generation treat the outage as absent
// configuration during the short error lifetime without overwriting an
// invalidation that occurred while the failed read was in flight.
func (s *PricingConfigService) storeErrorCache(gen uint64) {
	errorCache := newEmptyPricingConfigCache()
	// Reuse the normal expiry comparison with a backdated origin, allowing the
	// empty error snapshot to suppress immediate repeat database reads for the
	// shorter error lifetime.
	errorCache.loadedAt = s.options.Now().Add(-(pricingConfigCacheTTL - pricingConfigErrorTTL))
	s.cacheMu.Lock()
	if s.cacheGen == gen {
		s.cache.Store(errorCache)
	}
	s.cacheMu.Unlock()
}

// buildCache reads all pricing configurations and publishes one newly indexed
// snapshot when its starting generation remains current. Request cancellation
// and deadlines are detached before installing a bounded database timeout.
//
// Publication checks cacheGen under cacheMu after cloned configurations and
// lookup maps are prepared. If invalidateCache advanced cacheGen while ListAll
// was running, buildCache leaves s.cache untouched and returns any newer
// published snapshot so stale pre-invalidation rows never overwrite fresh data.
func (s *PricingConfigService) buildCache(ctx context.Context) (*pricingConfigCache, error) {
	s.cacheMu.Lock()
	gen := s.cacheGen
	s.cacheMu.Unlock()

	// Detach the initiating request's cancellation because other singleflight
	// callers share this read, then impose a separate database deadline so
	// abandoned callers do not leave unbounded work.
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), pricingConfigCacheDBTimeout)
	defer cancel()

	pricingConfigs, err := s.fetchPricingConfigData(dbCtx, gen)
	if err != nil {
		return nil, err
	}

	cache := populatePricingConfigCache(pricingConfigs)
	cache.loadedAt = s.options.Now()

	var published *pricingConfigCache
	// Verify generation parity under cacheMu before storing so an invalidation
	// during ListAll fences this older snapshot out of s.cache.
	s.cacheMu.Lock()
	if s.cacheGen == gen {
		s.cache.Store(cache)
		published = cache
	} else if current, ok := s.cache.Load().(*pricingConfigCache); ok && current != nil {
		published = current
	}
	s.cacheMu.Unlock()
	if published != nil {
		return published, nil
	}
	return cache, nil
}

// fetchPricingConfigData loads configurations and their children through
// ListAll. A read failure is observed, publishes a generation-fenced temporary
// empty error cache, and returns a wrapped storage error to the initiating
// rebuild caller.
func (s *PricingConfigService) fetchPricingConfigData(ctx context.Context, gen uint64) ([]PricingConfig, error) {
	configs, err := s.repo.ListAll(ctx)
	if err != nil {
		s.warn("failed to build pricing configuration cache", "error", err)
		s.storeErrorCache(gen)
		// The initiating read keeps its error while later requests can reuse the
		// empty recovery snapshot, reducing repeated storage calls during a transient
		// outage.
		return nil, fmt.Errorf("list all price configurations: %w", err)
	}
	return configs, nil
}

// populatePricingConfigCache clones supplied configurations and indexes their
// cards by configuration, group, and model identity. The indexes borrow pointers
// into those clones, so management inputs can later change while a published
// snapshot remains readable.
//
// Every index is prepared before the caller publishes this graph, which readers
// then keep immutable. Inactive configurations remain indexed for filtering at
// group lookup. Supply unique configuration IDs and one association per group;
// repeated keys overwrite associations while wildcard lists keep appended
// entries.
func populatePricingConfigCache(configs []PricingConfig) *pricingConfigCache {
	cache := newEmptyPricingConfigCache()
	cache.loadedAt = time.Now()
	for i := range configs {
		config := configs[i].Clone()
		cache.byID[config.ID] = config
		for _, id := range config.GroupIDs {
			cache.pricingConfigByGroupID[id] = config
			expandPricingToCache(cache, config, id)
		}
	}
	return cache
}

// InvalidateCache discards the published snapshot and synchronously attempts
// another bounded read. Call it after external price or association changes;
// rebuild failures are observed and converted to the short empty-cache state.
func (s *PricingConfigService) InvalidateCache() {
	s.invalidateCache()
}

// invalidateCache advances cacheGen, clears the shared pointer, forgets its
// singleflight key, and synchronously loads a fresh snapshot. Advancing
// cacheGen under cacheMu fences any in-flight builder started before
// invalidation from publishing stale rows or error snapshots afterward.
func (s *PricingConfigService) invalidateCache() {
	// Advance the generation, store a typed nil to keep atomic.Value's concrete
	// type stable, and forget singleflight together so new readers join the
	// post-invalidation rebuild while older builders are fenced from s.cache.
	s.cacheMu.Lock()
	s.cacheGen++
	s.cache.Store((*pricingConfigCache)(nil))
	s.cacheSF.Forget("pricing_config_cache")
	s.cacheMu.Unlock()

	if _, err := s.loadCache(context.Background()); err != nil {
		s.warn("failed to rebuild pricing configuration cache after invalidation", "error", err)
	}
}

// lookupPricing selects one cached card for a normalized group and model
// identity. The returned pointer belongs to the snapshot and stays read-only;
// exposing it for editing requires ModelPricingEntry.Clone.
//
// An eligible exact match precedes the ordered wildcard list. With
// effective=false, even a configured blank card can match; with effective=true,
// references remain selectable for target lookup. Other cards use
// HasEffectivePricing to decide eligibility, including supported token
// modifiers and interval fields. Skipped exact or wildcard entries let a later
// eligible wildcard provide the result.
func lookupPricing(cache *pricingConfigCache, groupID int64, model string, effective bool) *ModelPricingEntry {
	model = normalizePriceModelName(model)
	matches := func(entry *ModelPricingEntry) bool {
		return entry != nil && (!effective || entry.ReferenceModel != "" || entry.HasEffectivePricing())
	}
	// Apply eligibility before returning exact or ordered prefix entries, so an
	// ineffective configured card cannot mask a later usable wildcard during
	// effective-price lookup.
	if entry := cache.pricingByGroupModel[pricingModelKey{groupID: groupID, model: model}]; matches(entry) {
		return entry
	}
	for _, wildcard := range cache.wildcardByGroup[groupID] {
		if strings.HasPrefix(model, wildcard.prefix) && matches(wildcard.pricing) {
			return wildcard.pricing
		}
	}
	return nil
}

// GetPricingConfigForGroup returns a clone of the group's active shared
// configuration. Missing associations and inactive configurations yield nil
// without an error, while a failed rebuild returns its load error to that
// caller.
func (s *PricingConfigService) GetPricingConfigForGroup(ctx context.Context, groupID int64) (*PricingConfig, error) {
	cache, err := s.loadCache(ctx)
	if err != nil {
		return nil, err
	}

	ch, ok := cache.pricingConfigByGroupID[groupID]
	if !ok || !ch.IsActive() {
		return nil, nil
	}

	return ch.Clone(), nil
}

// lookupGroupPricingConfig returns one snapshot with its group's active
// configuration pointer. Both objects stay borrowed and read-only; nil with no
// error means absent or inactive configuration, while a load error remains
// distinct.
func (s *PricingConfigService) lookupGroupPricingConfig(ctx context.Context, groupID int64) (*pricingConfigLookup, error) {
	cache, err := s.loadCache(ctx)
	if err != nil {
		return nil, err
	}
	ch, ok := cache.pricingConfigByGroupID[groupID]
	if !ok || !ch.IsActive() {
		return nil, nil
	}
	return &pricingConfigLookup{
		cache:         cache,
		pricingConfig: ch,
	}, nil
}

// GetConfigModelPricing returns a cloned configured card after exact and
// ordered-wildcard lookup. Missing or inactive configuration, a lookup miss, or
// a load failure yields nil; load failures are observed through the optional
// warning callback.
func (s *PricingConfigService) GetConfigModelPricing(ctx context.Context, groupID int64, model string) *ModelPricingEntry {
	return s.getConfigModelPricing(ctx, groupID, model, false)
}

// GetEffectiveConfigModelPricing returns a cloned reference or effective price
// card for the group and model. Blank custom cards are skipped so a later
// wildcard may apply. References retain their target for ResolveModelPricing;
// absent configuration and observed load failures yield nil.
func (s *PricingConfigService) GetEffectiveConfigModelPricing(ctx context.Context, groupID int64, model string) *ModelPricingEntry {
	return s.getConfigModelPricing(ctx, groupID, model, true)
}

// getConfigModelPricing reads the group's active configuration once and returns
// a cloned card selected by lookupPricing. A true effective value filters cards
// through HasEffectivePricing while keeping references eligible for target lookup.
// Missing configurations and cards yield nil; load errors are reported through
// the warning callback before yielding nil.
func (s *PricingConfigService) getConfigModelPricing(ctx context.Context, groupID int64, model string, effective bool) *ModelPricingEntry {
	lk, err := s.lookupGroupPricingConfig(ctx, groupID)
	if err != nil {
		// Treat a failed configuration read as an absent price override so billing
		// consumers can continue through their catalog or default pricing source.
		s.warn("failed to load pricing configuration cache", "group_id", groupID, "error", err)
		return nil
	}
	if lk == nil {
		return nil
	}

	modelLower := strings.ToLower(model)
	pricing := lookupPricing(lk.cache, groupID, modelLower, effective)
	if pricing == nil {
		return nil
	}

	cp := pricing.Clone()
	return &cp
}

// ResolveGroupMapping combines group model rules with independent
// shared-pricing metadata. Its initial result retains the request model and
// uses the group-mapped restriction stage before any optional policy is read.
//
// A policy-read error enables restriction in the result and is observed; a
// valid view supplies the mapped name and allowlist stage. Active price
// configuration then supplies its ID and billing source, defaulting a blank
// source to group_mapped. A price-cache error leaves that metadata absent
// rather than undoing the group policy. These separate reads do not form one
// atomic cross-resource snapshot.
func (s *PricingConfigService) ResolveGroupMapping(ctx context.Context, groupID int64, model string) GroupMappingResult {
	result := GroupMappingResult{MappedModel: model, RestrictionModelSource: BillingModelSourceGroupMapped}
	policy, err := s.GetGroupPolicy(ctx, groupID)
	if err != nil {
		s.warn("failed to read group routing policy", "group_id", groupID, "error", err)
		// Preserve the request identity but mark restriction when policy loading
		// fails, so this mapping result cannot imply unrestricted model admission
		// after losing its rules.
		result.RestrictModels = true
	}
	if policy != nil {
		result.MappedModel = policy.ResolveModel(model)
		result.Mapped = result.MappedModel != model
		result.RestrictionModelSource = policy.RestrictionSource()
		result.RestrictModels = policy.RestrictModels
	}
	lk, err := s.lookupGroupPricingConfig(ctx, groupID)
	// Read scalar pricing metadata from the borrowed snapshot without cloning
	// model cards; a price-read failure leaves mapping and restriction intact.
	if err == nil && lk != nil {
		result.PricingConfigID = lk.pricingConfig.ID
		result.BillingModelSource = lk.pricingConfig.BillingModelSource
		if result.BillingModelSource == "" {
			result.BillingModelSource = BillingModelSourceGroupMapped
		}
	}
	return result
}

// IsModelRestricted checks the supplied model against the group's current
// allowlist view. A group-policy read error rejects the model, while a missing
// view follows GroupPolicyView's unrestricted nil behavior; pricing
// configuration contents do not determine admission.
func (s *PricingConfigService) IsModelRestricted(ctx context.Context, groupID int64, model string) bool {
	policy, err := s.GetGroupPolicy(ctx, groupID)
	if err != nil {
		// Treat an unreadable policy as restricted so a repository outage cannot
		// turn a configured model allowlist into unrestricted admission.
		return true
	}
	return policy.IsModelRestricted(model)
}

// Create validates and persists an active shared pricing configuration, then
// reloads its saved representation. Name and association conflict checks
// precede card validation, and omitted billing source selects group_mapped.
//
// Supplied card collections are attached to the working configuration and can
// be normalized or receive child identities during repository writes. After
// repository success, the service synchronously rebuilds prices and requests
// group authentication invalidation. Rebuild errors are observed without
// undoing persistence, and a final reload error can be returned after the
// configuration has already been saved.
func (s *PricingConfigService) Create(ctx context.Context, input *CreatePricingConfigInput) (*PricingConfig, error) {
	exists, err := s.repo.ExistsByName(ctx, input.Name)
	if err != nil {
		return nil, fmt.Errorf("check price configuration exists: %w", err)
	}
	if exists {
		return nil, ErrPricingConfigExists
	}

	if err := s.checkGroupConflicts(ctx, 0, input.GroupIDs); err != nil {
		return nil, err
	}

	pricingConfig := &PricingConfig{
		BillingSettings:    pricing.DefaultBillingSettings(),
		Name:               input.Name,
		Description:        input.Description,
		Status:             StatusActive,
		BillingModelSource: input.BillingModelSource,

		GroupIDs:     input.GroupIDs,
		ModelPricing: input.ModelPricing,

		ProviderStatsPricingRules: input.ProviderStatsPricingRules,
	}
	if err := input.Apply(&pricingConfig.BillingSettings); err != nil {
		return nil, err
	}
	if pricingConfig.BillingModelSource == "" {
		pricingConfig.BillingModelSource = BillingModelSourceGroupMapped
	}

	if err := s.validation.PricingEntries(pricingConfig.ModelPricing); err != nil {
		return nil, err
	}
	for i, rule := range pricingConfig.ProviderStatsPricingRules {
		if err := s.validation.ProviderStatsPricing(rule.Pricing); err != nil {
			return nil, fmt.Errorf("provider stats pricing rule #%d: %w", i+1, err)
		}
	}

	if err := s.repo.Create(ctx, pricingConfig); err != nil {
		return nil, fmt.Errorf("create price configuration: %w", err)
	}

	// Refresh readers after repository persistence succeeds; a rebuild failure
	// remains diagnostic and does not reverse the already saved configuration or
	// its associations.
	s.invalidateCache()
	s.invalidateAuthCacheForGroups(ctx, pricingConfig.GroupIDs)
	return s.repo.GetByID(ctx, pricingConfig.ID)
}

// GetByID loads the shared configuration directly through its repository
// reader.
func (s *PricingConfigService) GetByID(ctx context.Context, id int64) (*PricingConfig, error) {
	return s.repo.GetByID(ctx, id)
}

// Update applies supplied settings to a repository-loaded working
// configuration, validates cards, and persists the result. Omitted collection
// pointers retain their loaded values, while supplied empty collections clear
// their contents.
//
// Old group IDs are captured before persistence when authentication
// invalidation is configured. After a successful write, prices are rebuilt and
// each distinct old or current group receives an invalidation request. A failed
// old-ID read can leave incomplete invalidation coverage, and a reload error
// returns after the write and refresh phase have completed.
func (s *PricingConfigService) Update(ctx context.Context, id int64, input *UpdatePricingConfigInput) (*PricingConfig, error) {
	pricingConfig, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get price configuration: %w", err)
	}

	if err := s.applyUpdateInput(ctx, pricingConfig, input); err != nil {
		return nil, err
	}

	if err := s.validation.PricingEntries(pricingConfig.ModelPricing); err != nil {
		return nil, err
	}
	for i, rule := range pricingConfig.ProviderStatsPricingRules {
		if err := s.validation.ProviderStatsPricing(rule.Pricing); err != nil {
			return nil, fmt.Errorf("provider stats pricing rule #%d: %w", i+1, err)
		}
	}

	oldGroupIDs := s.getOldGroupIDs(ctx, id)

	if err := s.repo.Update(ctx, pricingConfig); err != nil {
		return nil, fmt.Errorf("update price configuration: %w", err)
	}

	// Refresh after the update commits, then include both departed and current
	// memberships so authentication snapshots can shed old prices and load newly
	// associated settings.
	s.invalidateCache()
	s.invalidateAuthCacheForGroups(ctx, oldGroupIDs, pricingConfig.GroupIDs)

	return s.repo.GetByID(ctx, id)
}

// applyUpdateInput merges supplied fields and checks name and group-association
// conflicts on the working configuration. It can mutate billing settings before
// a later conflict or validation error, so callers discard the working value
// when this helper fails.
func (s *PricingConfigService) applyUpdateInput(ctx context.Context, pricingConfig *PricingConfig, input *UpdatePricingConfigInput) error {
	if err := input.Apply(&pricingConfig.BillingSettings); err != nil {
		return err
	}
	if input.Name != "" && input.Name != pricingConfig.Name {
		exists, err := s.repo.ExistsByNameExcluding(ctx, input.Name, pricingConfig.ID)
		if err != nil {
			return fmt.Errorf("check price configuration exists: %w", err)
		}
		if exists {
			return ErrPricingConfigExists
		}
		pricingConfig.Name = input.Name
	}
	if input.Description != nil {
		pricingConfig.Description = *input.Description
	}
	if input.Status != "" {
		pricingConfig.Status = input.Status
	}

	if input.GroupIDs != nil {
		if err := s.checkGroupConflicts(ctx, pricingConfig.ID, *input.GroupIDs); err != nil {
			return err
		}
		pricingConfig.GroupIDs = *input.GroupIDs
	}
	if input.ModelPricing != nil {
		pricingConfig.ModelPricing = *input.ModelPricing
	}

	if input.BillingModelSource != "" {
		pricingConfig.BillingModelSource = input.BillingModelSource
	}

	if input.ProviderStatsPricingRules != nil {
		pricingConfig.ProviderStatsPricingRules = *input.ProviderStatsPricingRules
	}
	return nil
}

// checkGroupConflicts rejects requested groups already associated with another
// configuration. Pass zero for creation and the current configuration ID for
// updates; an empty ID list returns before making a repository query.
func (s *PricingConfigService) checkGroupConflicts(ctx context.Context, pricingConfigID int64, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		return nil
	}
	conflicting, err := s.repo.GetGroupsInOtherPricingConfigs(ctx, pricingConfigID, groupIDs)
	if err != nil {
		return fmt.Errorf("check group conflicts: %w", err)
	}
	if len(conflicting) > 0 {
		return ErrGroupAlreadyInPricingConfig
	}
	return nil
}

// getOldGroupIDs reads previous memberships when an authentication invalidator
// is installed. Missing invalidation disables the read; a repository error is
// observed, and any IDs returned alongside that error remain available for
// subsequent invalidation.
func (s *PricingConfigService) getOldGroupIDs(ctx context.Context, pricingConfigID int64) []int64 {
	if s.authCacheInvalidator == nil {
		return nil
	}
	oldGroupIDs, err := s.repo.GetGroupIDs(ctx, pricingConfigID)
	if err != nil {
		// Old membership diagnostics do not prevent the update; any returned IDs
		// still participate, but missing old IDs can leave authentication
		// invalidation incomplete.
		s.warn("failed to get old group IDs for cache invalidation", "pricing_config_id", pricingConfigID, "error", err)
	}
	return oldGroupIDs
}

// invalidateAuthCacheForGroups requests one authentication invalidation per
// distinct supplied group ID. A nil invalidator is ignored; IDs are
// deduplicated across old and new sets without adding an independent positivity
// filter.
func (s *PricingConfigService) invalidateAuthCacheForGroups(ctx context.Context, groupIDSets ...[]int64) {
	if s.authCacheInvalidator == nil {
		return
	}
	seen := make(map[int64]struct{})
	for _, ids := range groupIDSets {
		for _, gid := range ids {
			if _, ok := seen[gid]; ok {
				continue
			}
			// Old and replacement sets may overlap. Record each group before invoking
			// invalidation so a membership transition produces one request per distinct
			// ID.
			seen[gid] = struct{}{}
			s.authCacheInvalidator.InvalidateAuthCacheByGroupID(ctx, gid)
		}
	}
}

// Delete removes a saved shared configuration and refreshes prices plus the
// groups' authentication snapshots. A failed membership read is observed but
// does not block deletion.
//
// Memberships are captured before repository removal because their saved links
// may disappear with the configuration. A deletion error returns before
// refresh; successful removal triggers a synchronous rebuild and invalidation
// for captured IDs. Rebuild failure stays diagnostic, so persisted deletion
// remains the result even when price-cache recovery is deferred.
func (s *PricingConfigService) Delete(ctx context.Context, id int64) error {
	groupIDs, err := s.repo.GetGroupIDs(ctx, id)
	if err != nil {
		// Continue removal despite a membership-read failure; only captured group IDs
		// remain available for authentication invalidation after their association
		// rows disappear.
		s.warn("failed to get group IDs before delete", "pricing_config_id", id, "error", err)
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete price configuration: %w", err)
	}

	// Rebuild prices and invalidate captured groups only after repository removal
	// succeeds, keeping cache refresh from publishing a deletion that failed to
	// persist.
	s.invalidateCache()
	s.invalidateAuthCacheForGroups(ctx, groupIDs)

	return nil
}

// List returns the repository's filtered page of shared pricing configurations.
func (s *PricingConfigService) List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]PricingConfig, *pagination.PaginationResult, error) {
	return s.repo.List(ctx, params, status, search)
}

// BuildModelMappingChain joins distinct nonblank model spellings in
// first-occurrence order for display. Inputs are trimmed, equality remains
// case-sensitive, and fewer than two retained names returns an empty string.
//
// Deduplication covers the entire stage list, so a,b,a produces a→b instead of
// repeating the final a. Callers keep actual request and execution identities
// in their separate usage fields when that round trip matters. This display
// helper does not apply model mapping or price-name aliases to its inputs.
func BuildModelMappingChain(models ...string) string {
	stages := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		stages = append(stages, model)
	}
	// One retained identity has no model transition to display.
	if len(stages) < 2 {
		return ""
	}
	return strings.Join(stages, "→")
}

// warn reports a pricing diagnostic through the optional warning callback. Nil
// callbacks suppress reporting, while installed callbacks run synchronously
// with the load, mutation, or lookup operation that generated the message.
func (s *PricingConfigService) warn(message string, args ...any) {
	if s.options.Warn != nil {
		s.options.Warn(message, args...)
	}
}

// ResolveRoutingModel applies the group's routing policy directly without
// consulting or cloning shared pricing configuration. A missing service or
// group ID, or a blank request model, returns the input unchanged; policy read
// errors are reported through warn while retaining the requested identity.
func (s *PricingConfigService) ResolveRoutingModel(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil || groupID == nil || strings.TrimSpace(requestedModel) == "" {
		return requestedModel
	}
	policy, err := s.GetGroupPolicy(ctx, *groupID)
	if err != nil {
		s.warn("failed to read group routing policy", "group_id", *groupID, "error", err)
	}
	if policy != nil {
		if mapped := strings.TrimSpace(policy.ResolveModel(requestedModel)); mapped != "" {
			return mapped
		}
	}
	// An empty mapped result cannot identify an execution target. Retain the
	// requested name so downstream model eligibility can still inspect this
	// request's identity.
	return requestedModel
}

// GetEffectiveBillingSettings clones shared billing settings from the group's
// borrowed active configuration without copying its model-card slices. Missing
// configuration or a loading error selects shared default settings, so billing
// consumers can continue with the default policy value.
func (s *PricingConfigService) GetEffectiveBillingSettings(ctx context.Context, groupID int64) pricing.BillingSettings {
	lk, err := s.lookupGroupPricingConfig(ctx, groupID)
	// DefaultBillingSettings keeps long-context pricing enabled and batch image
	// discount and hold defaults usable when a cache error hides the group's
	// configured settings.
	if err != nil || lk == nil {
		return pricing.DefaultBillingSettings()
	}
	return lk.pricingConfig.BillingSettings.Clone()
}

// BillingModelForPrice selects and trims the configured model stage for price
// lookup. requestedModel must be the name after Key redirection; absent or
// unsupported sources use groupMappedModel, and an empty selected stage stays
// empty.
func BillingModelForPrice(mapping GroupMappingResult, requestedModel, groupMappedModel, upstreamModel string) string {
	switch mapping.BillingModelSource {
	case BillingModelSourceRequested:
		return strings.TrimSpace(requestedModel)
	case BillingModelSourceUpstream:
		return strings.TrimSpace(upstreamModel)
	case BillingModelSourceGroupMapped:
		return strings.TrimSpace(groupMappedModel)
	default:
		// Unconfigured and unrecognized sources use the group stage, matching the
		// default identity that ResolveGroupMapping attaches to active configuration
		// metadata.
		return strings.TrimSpace(groupMappedModel)
	}
}
