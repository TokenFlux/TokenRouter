package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// Provider creation tests can submit several records concurrently, while list
// and credential-maintenance scenarios read and mutate their captures serially.
// Creation callbacks record borrowed request pointers for later assertions;
// catalogue and query callbacks retain configured record data, and unused
// embedded operations remain unimplemented.
//
// Creation input appends and Grok ID allocation use their owning mutexes, but
// record maps, list populations and mutation captures remain borrowed or
// serial. The refresh fixture invokes its callback directly and provides no
// production locking. Expected-credential comparison in the mutation fixture
// likewise models one serial decision, so handler assertions cannot establish
// cross-request store coordination through these helpers.

// deprecatedLongContextBillingExtraKey identifies the retired Extra field
// supplied by handler compatibility scenarios to test discard and fingerprint behavior.
const deprecatedLongContextBillingExtraKey = "openai_long_context_billing_enabled"

// grokImportAdminService allocates unique fixture IDs under a mutex for
// concurrent SSO creation. Returned records borrow input maps and pointers,
// while shared embedded mutation observations retain their separate serial
// usage rules.
type grokImportAdminService struct {
	*managementMutationFixture // borrowed shared serial mutation captures
	// mu protects nextID reservation for concurrent creation. Each accepted call
	// increments before returning its record; embedded mutation captures retain
	// their independent serial-use rule.
	mu     sync.Mutex // fixture capture synchronization mutex
	nextID int64      // next unique fixture provider identifier
}

// managementCreateFixture records borrowed creation requests under mu and
// supplies a configurable error. Tests set the error before concurrent creation
// and read captured pointers after their batch has finished.
type managementCreateFixture struct {
	ProviderManagement // unused embedded administrator operations
	// mu protects append operations on createdProviders. Those inputs remain
	// borrowed until tests inspect completed requests; createProviderErr stays
	// fixed while concurrent callbacks read it after unlocking.
	mu sync.Mutex // fixture capture synchronization mutex
	// borrowed captured provider creation inputs
	createdProviders  []*providercore.CreateProviderInput
	createProviderErr error // configured creation callback failure
}

// managementListFixture supplies serial list and score populations and captures
// request predicates. Returned record slices and nested values remain borrowed;
// configure populations before invoking a list handler and inspect observations
// afterward.
type managementListFixture struct {
	ProviderManagement // unused embedded administrator operations
	// configured serial provider records
	providers []providercore.Record
	// providerSchedulerScoreFilterProviders overrides providers when nonnil. An
	// explicitly empty slice keeps the score population empty, while nil selects
	// the ordinary configured rows.
	// optional serial score-filter population
	providerSchedulerScoreFilterProviders []providercore.Record
	// openAISchedulerScorePoolProviders overrides providers when nonnil. Nil
	// selects the ordinary rows; a supplied empty slice admits no group-scoring
	// candidates through this serial fixture.
	// optional serial group-score population
	openAISchedulerScorePoolProviders []providercore.Record
	// schedulerScoreFilterCalls: serial filter-query invocation count;
	// openAISchedulerScorePoolCalls: serial score-pool invocation count;
	// getGroupCalls: serial group-read observation count
	schedulerScoreFilterCalls, openAISchedulerScorePoolCalls, getGroupCalls int
	// captured queried routing-group identifiers
	openAISchedulerScorePoolGroupIDs []int64
	// captured serial list-query predicates
	lastListProviders struct {
		// platform: supplied upstream platform filter; providerType: supplied
		// authentication kind filter; status: supplied provider state filter; search:
		// supplied provider search text; privacyMode: supplied provider privacy
		// filter; sortBy: supplied sorting column selector; sortOrder: supplied
		// sorting direction selector
		platform, providerType, status, search, privacyMode, sortBy, sortOrder string
		// supplied routing group filter
		groupID int64
		calls   int // serial list-query invocation count
	}
}

// availableModelsAdminService provides one selected catalogue record before
// delegating other lookups. The selected record is shallowly copied, so its
// configuration maps remain borrowed during synchronous catalogue preparation.
type availableModelsAdminService struct {
	*managementMutationFixture // borrowed shared serial mutation captures
	// borrowed configured catalogue record
	provider providercore.Record
}

// managementMutationFixture captures serial configuration and credential
// operations for HTTP tests. The embedded creation capture uses its own mutex,
// while update inputs, provider rows, Extra observations and bulk fields need
// serial fixture access.
type managementMutationFixture struct {
	managementCreateFixture // creation captures with their own mutex
	// providers, updateProviderInput, updateExtraCalls, clearProviderErrorIDs and
	// lastBulkUpdateProviderInput retain serial rows or captured inputs. Nested
	// maps and captured pointers remain borrowed; tests inspect them after each
	// synchronous request and configure both error fields before use.
	providers []providercore.Record // configured serial provider records
	// borrowed last credential/configuration input
	updateProviderInput *providercore.UpdateProviderInput
	updateProviderErr   error // configured provider update failure
	// borrowed serial Extra patch observations
	updateExtraCalls      []map[string]any
	clearProviderErrorIDs []int64 // captured serial error-clear identifiers
	// borrowed last bulk editing input
	lastBulkUpdateProviderInput *providercore.BulkUpdateProvidersInput
	bulkUpdateProviderErr       error // configured bulk editing failure
}

// newGrokImportAdminService initializes concurrent SSO creation with the shared
// mutation fixture and an ID counter starting at five hundred. Its
// CreateProvider method owns counter synchronization, while embedded mutation
// methods remain serial fixtures.
func newGrokImportAdminService() *grokImportAdminService {
	return &grokImportAdminService{
		managementMutationFixture: newManagementMutationFixture(),
		nextID:                    500,
	}
}

// CreateProvider reserves a unique fixture identifier under mu and returns an
// active record from the supplied creation values. Input credential maps, Extra
// and proxy pointers remain borrowed; this substitute does not append shared
// creation captures.
func (s *grokImportAdminService) CreateProvider(_ context.Context, input *providercore.CreateProviderInput) (*providercore.Record, error) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.mu.Unlock()
	return &providercore.Record{
		ID:          id,
		Name:        input.Name,
		Platform:    input.Platform,
		Type:        input.Type,
		Credentials: input.Credentials,
		Extra:       input.Extra,
		ProxyID:     input.ProxyID,
		Concurrency: input.Concurrency,
		Status:      billing.StatusActive,
		Schedulable: true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}, nil
}

// newManagedRefreshFixture connects the supplied store and privacy fixture to
// platform exchange helpers. Its Coordinate callback invokes apply directly on
// the selected record; it provides no production refresh lock or locked reread,
// so concurrency guarantees belong to separate coordinator tests.
func newManagedRefreshFixture(source interface {
	providercore.ManagedCredentialStore   // borrowed fixture credential operations
	providercore.ManagedCredentialPrivacy // borrowed fixture privacy observations
}, exchange *providercore.ManualCredentialExchange,
) *providercore.ManagedRefreshService {
	return providercore.NewManagedRefreshService(providercore.ManagedRefreshOptions{
		Store: source, Privacy: source, CacheKey: provideradapter.ManagedRefreshCacheKey,
		Coordinate: func(ctx context.Context, v *providercore.Record, _ string, apply func(context.Context, *providercore.Record) (*providercore.Record, string, error)) (*providercore.Record, string, error) {
			return apply(ctx, v)
		},
		Exchange: func(ctx context.Context, v *providercore.Record) (providercore.ManagedRefreshObservation, error) {
			credentials, missing, err := exchange.Refresh(ctx, v)
			return providercore.ManagedRefreshObservation{Credentials: credentials, ProjectIDMissing: missing}, err
		},
	})
}

// CreateProvider records the borrowed creation input under mu before returning
// its configured error or a synthetic active result. The error field is read
// after unlocking and is configured before requests start; tests inspect
// captures after joining their batch.
func (s *managementCreateFixture) CreateProvider(_ context.Context, input *providercore.CreateProviderInput) (*providercore.Record, error) {
	s.mu.Lock()
	s.createdProviders = append(s.createdProviders, input)
	s.mu.Unlock()
	if s.createProviderErr != nil {
		return nil, s.createProviderErr
	}
	return &providercore.Record{ID: 300, Name: input.Name, Status: providercore.StatusActive}, nil
}

// ForceOpenAIPrivacy returns the empty observation for the creation fixture's
// OpenAI path.
func (s *managementCreateFixture) ForceOpenAIPrivacy(context.Context, *providercore.Record) string {
	return ""
}

// ForceAntigravityPrivacy returns the empty observation for the creation
// fixture's Antigravity path.
func (s *managementCreateFixture) ForceAntigravityPrivacy(context.Context, *providercore.Record) string {
	return ""
}

// newManagementCreateFixtureHandler binds shared creation captures to
// ManagementBatch, privacy callbacks and runtime presentation. Only creation
// routes are configured for this fixture, whose empty privacy observations keep
// follow-up effects controlled.
func newManagementCreateFixtureHandler(source *managementCreateFixture) *ManagementHandler {
	presenter := NewRuntimePresenter(providercore.NewRuntimeStatusReader(providercore.RuntimeStatusOptions{}), source, nil)
	batch := providercore.NewManagementBatch(source, nil, providercore.ManagementCreationOptions{Privacy: source})
	return NewManagementHandler(source, ManagementOptions{Presenter: presenter, Privacy: source, Batch: batch})
}

// ListProviders captures list predicates and slices the configured serial
// record population by page. Filters and sorting are recorded rather than
// applied. Invalid page selects one, nonpositive page size uses the population
// length and out-of-range pages return an empty slice.
func (s *managementListFixture) ListProviders(ctx context.Context, page, pageSize int, platform, providerType, status, search string, groupID int64, privacyMode string, sortBy, sortOrder string) ([]providercore.Record, int64, error) {
	s.lastListProviders.platform = platform
	s.lastListProviders.providerType = providerType
	s.lastListProviders.status = status
	s.lastListProviders.search = search
	s.lastListProviders.groupID = groupID
	s.lastListProviders.privacyMode = privacyMode
	s.lastListProviders.sortBy = sortBy
	s.lastListProviders.sortOrder = sortOrder
	s.lastListProviders.calls++
	providers := s.providers
	total := len(providers)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = total
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []providercore.Record{}, int64(total), nil
	}
	// Bound the borrowed page by the available fixture row count.
	end := min(start+pageSize, total)
	return providers[start:end], int64(total), nil
}

// ListProvidersForSchedulerScoreFilter counts the serial candidate query and
// prefers its configured nonnil score population. Nil falls back to providers,
// while an explicitly empty nonnil slice remains an empty candidate population.
func (s *managementListFixture) ListProvidersForSchedulerScoreFilter(_ context.Context, platform, providerType, status, search string, groupID int64, privacyMode string) ([]providercore.Record, error) {
	s.schedulerScoreFilterCalls++
	// A configured empty candidate slice remains an intentional empty population.
	// Nil alone selects the broader provider fixture for score-filter queries.
	if s.providerSchedulerScoreFilterProviders != nil {
		return s.providerSchedulerScoreFilterProviders, nil
	}
	return s.providers, nil
}

// ListSchedulableProvidersForAdvancedSchedulerScore records group-pool queries
// and selects configured schedulable fixture providers. Callers configure rows
// before invocation and inspect the serial counters afterward; returned record
// values retain shared nested maps and pointers.
//
// A nil dedicated score population reuses providers, while a nonnil empty one
// stays empty. Platform and schedulability filtering precede group selection.
// Nil group admits records with neither ProviderGroups nor GroupIDs; a supplied
// group matches loaded ProviderGroups alone, so bare GroupIDs do not establish
// membership for this substitute.
func (s *managementListFixture) ListSchedulableProvidersForAdvancedSchedulerScore(_ context.Context, groupID *int64, platform string) ([]providercore.Record, error) {
	s.openAISchedulerScorePoolCalls++
	if groupID != nil {
		s.openAISchedulerScorePoolGroupIDs = append(s.openAISchedulerScorePoolGroupIDs, *groupID)
	}
	providers := s.openAISchedulerScorePoolProviders
	// Nil score-pool input reuses the configured provider population, while an
	// explicit empty slice keeps the pool empty for this group-query scenario.
	if providers == nil {
		providers = s.providers
	}
	out := make([]providercore.Record, 0, len(providers))
	for _, provider := range providers {
		if (platform != "" && provider.Platform != platform) || !provider.IsSchedulable() {
			continue
		}
		if groupID == nil {
			if len(provider.ProviderGroups) == 0 && len(provider.GroupIDs) == 0 {
				out = append(out, provider)
			}
			continue
		}
		for _, providerGroup := range provider.ProviderGroups {
			if providerGroup.GroupID == *groupID {
				out = append(out, provider)
				break
			}
		}
	}
	return out, nil
}

// newManagementListFixture initializes one active Anthropic OAuth provider with
// UTC fixture timestamps. The caller can replace its serial population before
// HTTP requests, while the initial record supplies the standard list response.
func newManagementListFixture() *managementListFixture {
	now := time.Now().UTC()
	return &managementListFixture{providers: []providercore.Record{{ID: 3, Name: "provider", Platform: providercore.PlatformAnthropic, Type: providercore.ProviderTypeOAuth, Status: providercore.StatusActive, CreatedAt: now, UpdatedAt: now}}}
}

// setupProviderMutationContractRouter mounts creation, editing and bulk editing
// with the supplied shared mutation fixture. Each test controls captured inputs
// after synchronous requests, and response presentation uses empty
// runtime-observation defaults.
func setupProviderMutationContractRouter(adminSvc *managementMutationFixture) *gin.Engine {
	router := gin.New()
	providerHandler := newMutationHandler(adminSvc, nil)
	router.POST("/api/v1/admin/providers", providerHandler.Create)
	router.PUT("/api/v1/admin/providers/:id", providerHandler.Update)
	router.POST("/api/v1/admin/providers/bulk-update", providerHandler.BulkUpdate)
	return router
}

// GetProvider returns a shallow copy of the configured matching catalogue
// provider. Other IDs delegate to the shared synthetic-record fixture using a
// background context, leaving this adapter's observed catalogue configuration
// unchanged.
func (s *availableModelsAdminService) GetProvider(_ context.Context, id int64) (*providercore.Record, error) {
	if s.provider.ID == id {
		acc := s.provider
		return &acc, nil
	}
	return s.managementMutationFixture.GetProvider(context.Background(), id)
}

// GetProvider returns a shallow copy of a matching serial fixture record or a
// synthetic active Anthropic OAuth record. Nested maps and pointers remain
// borrowed, and missing configured IDs therefore do not model production
// not-found behavior.
func (s *managementMutationFixture) GetProvider(ctx context.Context, id int64) (*providercore.Record, error) {
	for i := range s.providers {
		if s.providers[i].ID == id {
			provider := s.providers[i]
			return &provider, nil
		}
	}
	provider := providercore.Record{ID: id, Name: "provider", Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive}
	return &provider, nil
}

// GetProvidersByIDs preserves requested order and duplicates while shallowly
// copying matching fixture records. Missing configured IDs receive synthetic
// active records, allowing management tests to observe selection behavior
// without real storage existence checks. Nested maps and pointers in copied
// records remain borrowed from the configured population.
func (s *managementMutationFixture) GetProvidersByIDs(ctx context.Context, ids []int64) ([]*providercore.Record, error) {
	out := make([]*providercore.Record, 0, len(ids))
	for _, id := range ids {
		found := false
		for i := range s.providers {
			if s.providers[i].ID == id {
				provider := s.providers[i]
				out = append(out, &provider)
				found = true
				break
			}
		}
		if found {
			continue
		}
		provider := providercore.Record{ID: id, Name: "provider", Status: billing.StatusActive}
		out = append(out, &provider)
	}
	return out, nil
}

// UpdateProvider models serial credential comparison and selected map mutation
// for managed refresh tests. A supplied ExpectedCredentials is compared with
// each matching stored fixture record before configured write failure is
// checked; omitted expectations permit ordinary edits. The comparison acquires
// no mutex and establishes no concurrent store guarantee.
//
// Accepted calls capture the borrowed input and replace or patch credentials on
// a matching row. PatchCredentials uses the core merge helper so selected
// credential fields can retain neighboring values. Other configuration fields
// are captured rather than comprehensively persisted, and missing rows return a
// synthetic record. Returned record copies can share nested containers with
// this serial fixture.
func (s *managementMutationFixture) UpdateProvider(ctx context.Context, id int64, input *providercore.UpdateProviderInput) (*providercore.Record, error) {
	// This serial substitute compares the observed credential version before any
	// fixture mutation. No mutex covers this check, so it cannot prove production concurrency.
	if input.ExpectedCredentials != nil {
		for i := range s.providers {
			if s.providers[i].ID == id && !providercore.MatchesCredentialVersion(&s.providers[i], *input.ExpectedCredentials) {
				return nil, providercore.ErrRefreshProviderStateChanged
			}
		}
	}

	if s.updateProviderErr != nil {
		return nil, s.updateProviderErr
	}
	s.updateProviderInput = input
	for i := range s.providers {
		if s.providers[i].ID == id {
			if input.Credentials != nil {
				if input.PatchCredentials {
					s.providers[i].Credentials = providercore.MergeCredentials(s.providers[i].Credentials, input.Credentials)
				} else {
					s.providers[i].Credentials = input.Credentials
				}
			}
			provider := s.providers[i]
			return &provider, nil
		}
	}
	provider := providercore.Record{ID: id, Name: input.Name, Platform: capability.PlatformAnthropic, Type: input.Type, Status: billing.StatusActive, Credentials: input.Credentials}
	return &provider, nil
}

// UpdateProviderExtra records the borrowed update map in a serial capture
// slice. It returns success without applying Extra to fixture rows, allowing
// reauthorization tests to inspect patch dispatch separately from persistence.
func (s *managementMutationFixture) UpdateProviderExtra(ctx context.Context, id int64, updates map[string]any) error {
	s.updateExtraCalls = append(s.updateExtraCalls, updates)
	return nil
}

// ClearProviderError records the selected identifier and returns a synthetic
// active Anthropic OAuth record. Existing fixture rows are unchanged, so this
// method captures recovery dispatch rather than a durable row-state transition.
func (s *managementMutationFixture) ClearProviderError(ctx context.Context, id int64) (*providercore.Record, error) {
	s.clearProviderErrorIDs = append(s.clearProviderErrorIDs, id)
	provider := providercore.Record{ID: id, Name: "provider", Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth, Status: billing.StatusActive}
	return &provider, nil
}

// BulkUpdateProviders captures the borrowed bulk input and returns one success
// per supplied ID unless a configured error wins. It does not resolve filters,
// and returned SuccessIDs shares the input selection slice.
func (s *managementMutationFixture) BulkUpdateProviders(ctx context.Context, input *providercore.BulkUpdateProvidersInput) (*providercore.BulkUpdateProvidersResult, error) {
	s.lastBulkUpdateProviderInput = input
	if s.bulkUpdateProviderErr != nil {
		return nil, s.bulkUpdateProviderErr
	}
	return &providercore.BulkUpdateProvidersResult{Success: len(input.ProviderIDs), Failed: 0, SuccessIDs: input.ProviderIDs}, nil
}

// EnsureOpenAIPrivacy returns an empty observation for the shared fixture's
// OpenAI maintenance.
func (s *managementMutationFixture) EnsureOpenAIPrivacy(ctx context.Context, provider *providercore.Record) string {
	return ""
}

// EnsureAntigravityPrivacy returns an empty observation for the shared
// fixture's Antigravity maintenance.
func (s *managementMutationFixture) EnsureAntigravityPrivacy(ctx context.Context, provider *providercore.Record) string {
	return ""
}

// newManagementMutationFixture constructs empty serial provider mutation and
// creation observation storage.
func newManagementMutationFixture() *managementMutationFixture { return &managementMutationFixture{} }

// newMutationHandler connects serial mutation captures to managed refresh,
// batch operations and runtime presentation. An optional invalidator is bound
// by callback, while empty privacy results keep credential-maintenance
// assertions focused on requested store operations.
func newMutationHandler(source *managementMutationFixture, invalidator providercore.TokenCacheInvalidator) *ManagementHandler {
	options := providercore.ManagedRefreshOptions{Store: source, Privacy: source}
	if invalidator != nil {
		options.Invalidate = invalidator.InvalidateToken
	}
	managed := providercore.NewManagedRefreshService(options)
	presenter := NewRuntimePresenter(providercore.NewRuntimeStatusReader(providercore.RuntimeStatusOptions{}), source, nil)
	batch := providercore.NewManagementBatch(source, managed, providercore.ManagementCreationOptions{Privacy: source})
	return NewManagementHandler(source, ManagementOptions{Managed: managed, Presenter: presenter, RuntimePresenter: presenter, Privacy: source, Batch: batch})
}

// attachQoderHTTPFixtureCatalog supplies serialized account models to HTTP tests.
// The fixture uses the same persisted representation as production observations,
// while authentication and transport remain controlled by each test's own doubles.
func attachQoderHTTPFixtureCatalog(value *providercore.Record) {
	if value.Extra == nil {
		value.Extra = map[string]any{}
	}
	value.Extra[providercore.QoderCatalogExtraKey] = map[string]any{
		"identity": providercore.QoderCatalogIdentity(value), "version": "fixture", "fetched_at": time.Now(),
		"models": []map[string]any{
			{"key": "auto", "alias": "auto", "enabled": true, "input_limit": 1000000},
			{"key": "ultimate", "alias": "ultimate", "enabled": true, "input_limit": 1000000},
			{"key": "qmodel_38max", "alias": "qwen3.8-max", "enabled": true, "input_limit": 1000000},
		},
	}
}
