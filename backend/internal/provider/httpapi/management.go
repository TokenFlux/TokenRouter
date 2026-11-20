package httpapi

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	idempotencyhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// management.go constructs provider management handlers and serves individual
// provider reads and deletions.
// Provider administration separates stored configuration from live scheduler
// and usage observations. Individual reads can resolve shared Ollama usage
// before presentation, while route handlers delegate mutations to the
// administrator and return the use case's error mapping. Dependencies are
// borrowed during construction and need to remain available for mounted
// endpoints.

// ProviderManagement supplies administrative reads and mutations used by the
// mounted provider handlers.
type ProviderManagement interface {
	// BulkUpdateProviders applies selected configuration fields to the requested
	// IDs or filter selection. Per-item failures are returned with successes;
	// validation failures can reject the batch before any write.
	BulkUpdateProviders(context.Context, *providercore.BulkUpdateProvidersInput) (*providercore.BulkUpdateProvidersResult, error)
	// SetProviderSchedulable writes the administrator scheduling switch and
	// returns the updated provider. Runtime health and rate-limit state remain
	// separate inputs to scheduling eligibility, so true alone does not imply
	// availability.
	SetProviderSchedulable(context.Context, int64, bool) (*providercore.Record, error)
	// ResetProviderQuota clears the provider quota accounting selected by the
	// administration implementation. Callers read the record afterward for
	// presentation; a later read error does not reverse the completed reset.
	ResetProviderQuota(context.Context, int64) error
	// RevertProviderProxyFallback restores a recorded proxy fallback origin
	// through administration. The handler forwards storage failures and returns
	// confirmation after success; proxy selection validation and durable changes
	// belong to the implementation.
	RevertProviderProxyFallback(context.Context, int64) error
	// GetProvidersByIDs reads the requested records for batch operations and
	// parent presentation. Callers handle incomplete results according to their
	// operation; the returned record ownership follows the store implementation.
	GetProvidersByIDs(context.Context, []int64) ([]*providercore.Record, error)
	// CreateProvider validates supplied creation settings and persists a new
	// provider. The returned record is passed to privacy and presentation
	// callbacks, which may update its observed fields after creation.
	CreateProvider(context.Context, *providercore.CreateProviderInput) (*providercore.Record, error)
	// UpdateProvider applies the supplied configuration fields to a provider.
	// Omitted pointer fields retain stored values; credentials and Extra follow
	// the administration rules for sensitive-value retention and selected
	// configuration writes.
	UpdateProvider(context.Context, int64, *providercore.UpdateProviderInput) (*providercore.Record, error)
	// DuplicateProvider creates a provider using the source configuration and
	// administrator operation identity. The actor scope and idempotency key
	// identify the durable duplicate for recovery after uncertain response-cache
	// outcomes.
	DuplicateProvider(context.Context, int64, string, string) (*providercore.Record, error)
	// RecoverDuplicateProvider looks up a duplicate for the source, actor scope
	// and operation key. A nil record reports no recoverable duplicate; lookup
	// errors leave the original idempotency failure available to the handler.
	RecoverDuplicateProvider(context.Context, int64, string, string) (*providercore.Record, error)
	// GetProvider reads the record selected by its numeric provider identifier.
	// The handler maps failures according to its endpoint and may use the returned
	// record for runtime enrichment or credential maintenance.
	GetProvider(context.Context, int64) (*providercore.Record, error)
	// DeleteProvider deletes a provider and its store-managed dependent records.
	// Batch deletion arranges selected parent relationships before calling this
	// operation; successful handler responses follow the store return.
	DeleteProvider(context.Context, int64) error
}

// ProviderRuntimePresenter builds administrator response fields from a loaded
// provider record.
type ProviderRuntimePresenter interface {
	// Present builds the management response from a loaded provider under the
	// supplied request context. Its implementation chooses runtime reads and
	// redaction, and the handler sends the returned view directly.
	Present(context.Context, *providercore.Record) ProviderWithConcurrency
}

// ProviderRuntimePresenterFunc adapts a presentation callback to the management
// handler dependency.
type ProviderRuntimePresenterFunc func(context.Context, *providercore.Record) ProviderWithConcurrency

// ManagementHandler binds HTTP provider requests to administrative use cases
// and response presentation. Its service and callback references are borrowed;
// configure the dependencies reached by each mounted endpoint and keep them
// available until that endpoint's requests finish.
type ManagementHandler struct {
	idempotencyhttp.Executor // administrator write idempotency binding

	models           *providercore.ModelSyncService                    // live upstream model queries
	reports          ProviderReportOptions                             // local usage report callbacks
	catalog          *routing.AdminCatalog                             // administrator model eligibility catalogue
	modelDefaults    providercore.ModelMappingDefaults                 // platform mapping default callbacks
	modelRules       func(*providercore.Record) routing.CatalogueRules // per-record catalogue rules callback
	listing          *providercore.ManagementList                      // paginated runtime list use case
	runtimePresenter *RuntimePresenter                                 // loaded list response presentation
	recovery         *providercore.RecoveryService                     // recoverable runtime state maintenance
	batch            *providercore.ManagementBatch                     // bounded administrative batch use case
	managed          *providercore.ManagedRefreshService               // credential refresh and reauthorization
	adminService     ProviderManagement                                // provider administrative storage operations
	presenter        ProviderRuntimePresenter                          // individual record response presenter
	ollamaCloudUsage *providercore.OllamaCloudUsageService             // optional shared Ollama usage resolution
	privacy          ProviderCreationPrivacy                           // creation privacy observation callbacks
	// afterCreate receives the captured creation record after a successful
	// response operation. Idempotent replay supplies nil, so callbacks handle that
	// value before reading fields.
	afterCreate func(*providercore.Record) // optional successful response follow-up
}

// ProviderCreationPrivacy requests platform privacy observations during
// creation and explicit administrator maintenance.
type ProviderCreationPrivacy interface {
	// ForceAntigravityPrivacy requests an eligible Antigravity OAuth privacy
	// observation using the borrowed record. Empty means no applicable result; a
	// nonempty mode can survive a persistence error and does not prove storage
	// success. Callers keep record identity stable during the observation and
	// synchronize any local Extra mutation performed after an accepted write.
	ForceAntigravityPrivacy(context.Context, *providercore.Record) string
	// ForceOpenAIPrivacy requests an eligible OpenAI OAuth privacy observation
	// using the borrowed record. Empty means no applicable result; successful
	// conditional persistence may mutate local Extra, while a write error can
	// still return the mode. Callers keep record identity stable and synchronize
	// local Extra mutation throughout this observation.
	ForceOpenAIPrivacy(context.Context, *providercore.Record) string
}

// ManagementOptions supplies the use cases and callbacks selected for mounted
// management endpoints. Construction retains these service and callback
// references; their owners manage shutdown after the handlers finish using them.
type ManagementOptions struct {
	Models           *providercore.ModelSyncService                    // live upstream model queries
	Reports          ProviderReportOptions                             // local usage report callbacks
	Catalog          *routing.AdminCatalog                             // administrator model eligibility catalogue
	ModelDefaults    providercore.ModelMappingDefaults                 // platform mapping default callbacks
	ModelRules       func(*providercore.Record) routing.CatalogueRules // per-record catalogue rules callback
	List             *providercore.ManagementList                      // paginated runtime list use case
	RuntimePresenter *RuntimePresenter                                 // loaded list response presentation
	Recovery         *providercore.RecoveryService                     // recoverable runtime state maintenance
	Batch            *providercore.ManagementBatch                     // bounded administrative batch use case
	Managed          *providercore.ManagedRefreshService               // credential refresh and reauthorization
	Presenter        ProviderRuntimePresenter                          // individual record response presenter
	Ollama           *providercore.OllamaCloudUsageService             // optional shared Ollama usage resolution
	Privacy          ProviderCreationPrivacy                           // creation privacy observation callbacks
	// AfterCreate receives the captured creation record after a successful
	// response operation. Idempotent replay supplies nil, so callbacks handle that
	// value before reading fields.
	AfterCreate func(*providercore.Record) // optional successful response follow-up
}

// Present invokes the stored function to build a provider response. The
// function and record follow the implementation supplied by the constructor; a
// nil function panics when invoked.
func (f ProviderRuntimePresenterFunc) Present(ctx context.Context, v *providercore.Record) ProviderWithConcurrency {
	return f(ctx, v)
}

// NewManagementHandler borrows the administrator and supplied use cases for
// route handling. Configure each dependency before mounting its endpoint; the
// constructor supplies no substitutes or background lifecycle and retains
// callback references.
func NewManagementHandler(admin ProviderManagement, options ManagementOptions) *ManagementHandler {
	return &ManagementHandler{models: options.Models, reports: options.Reports, catalog: options.Catalog, modelDefaults: options.ModelDefaults, modelRules: options.ModelRules, listing: options.List, runtimePresenter: options.RuntimePresenter, recovery: options.Recovery, batch: options.Batch, managed: options.Managed, adminService: admin, presenter: options.Presenter, ollamaCloudUsage: options.Ollama, privacy: options.Privacy, afterCreate: options.AfterCreate}
}

// GetByID reads a provider and returns its runtime management view. An optional
// Ollama service resolves shared usage first, and resolution errors prevent
// response presentation even after a successful record lookup.
// GET /api/v1/admin/providers/:id
func (h *ManagementHandler) GetByID(c *gin.Context) {
	providerID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid provider ID")
		return
	}

	provider, err := h.adminService.GetProvider(c.Request.Context(), providerID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.ollamaCloudUsage != nil {
		if err := h.ollamaCloudUsage.ResolveProviders(c.Request.Context(), []*providercore.Record{provider}); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	response.Success(c, h.presenter.Present(c.Request.Context(), provider))
}

// Delete removes the provider selected by the decimal path identifier.
// Malformed identifiers return 400; store failures use the common error
// mapping, and successful deletion returns the confirmation message.
// DELETE /api/v1/admin/providers/:id
func (h *ManagementHandler) Delete(c *gin.Context) {
	providerID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid provider ID")
		return
	}

	err = h.adminService.DeleteProvider(c.Request.Context(), providerID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"message": "Provider deleted successfully"})
}
