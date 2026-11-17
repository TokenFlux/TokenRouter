package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
)

// qoder_catalog.go maintains account-scoped Qoder model observations and
// publishes replacements through conditional provider writes. A successful
// empty catalog removes all automatic candidates, while a failed refresh
// retains the last catalog explicitly synchronized for the same identity.
// Account and organization changes invalidate observations before the next fetch.
//
// Concurrent manual refreshes share one keyed probe runtime. The
// provider is reread before dispatch, and the storage writer compares that
// captured identity before publishing. This prevents a late response from
// restoring another account's routes. Published snapshots contain model data
// and an identity digest, never credentials. The existing provider outbox
// invalidates scheduling and catalog consumers after each accepted write.

// Manual catalog synchronization limits time and concurrency for upstream calls.
const (
	QoderCatalogExtraKey    = "qoder_model_catalog"
	QoderCatalogTimeout     = 15 * time.Second
	qoderCatalogConcurrency = 4
)

// ErrQoderCatalogUnavailable identifies a missing, invalid, or replaced account
// observation. Callers must not substitute the retired site-wide model table.
var ErrQoderCatalogUnavailable = errors.New("qoder model catalog is unavailable; synchronize the provider models")

// QoderCatalogSnapshot records one successful manual synchronization.
// @project-doc docs/interfaces/qoder_upstream.md#qoder_account_catalog
type QoderCatalogSnapshot struct {
	Site           string    `json:"site"`                      // observed deployment region
	UID            string    `json:"uid,omitempty"`             // authenticated user identifier when supplied
	AID            string    `json:"aid,omitempty"`             // authenticated account identifier when supplied
	OrganizationID string    `json:"organization_id,omitempty"` // selected organization identity
	Identity       string    `json:"identity"`                  // credential identity digest
	Version        string    `json:"version"`                   // normalized model content digest
	FetchedAt      time.Time `json:"fetched_at"`                // last successful observation
	// Models is nil before the first successful query and nonnil after success.
	// An empty nonnil collection intentionally represents an account without routes.
	Models []qodermodel.Model `json:"models"`
}

// QoderCatalogOptions binds discovery to provider persistence and transport.
type QoderCatalogOptions struct {
	Read   func(context.Context, int64) (*Record, error)              // current provider lookup
	Fetch  func(context.Context, *Record) ([]qodermodel.Model, error) // authenticated metadata query
	Writer UsageExtraWriter                                           // identity-checked snapshot persistence
	Now    func() time.Time                                           // injectable observation clock
}

// QoderCatalogService owns concurrent administrator-requested model queries.
type QoderCatalogService struct {
	// stopping closes transport admission before per-query cancellation begins.
	// A released slot can otherwise wake another account during the shutdown sweep.
	stopping atomic.Bool         // permanent transport admission barrier
	options  QoderCatalogOptions // immutable synchronization dependencies
	probes   ProbeRuntime        // shared query and shutdown owner
	slots    chan struct{}       // bounded upstream query admission
}

// preparedQoderCatalog holds the persisted snapshot and its immutable lookup index.
// Embedding the snapshot preserves its JSON form across Redis and database writes.
type preparedQoderCatalog struct {
	*QoderCatalogSnapshot
	index *qodermodel.Catalog // immutable account route index
}

// NewQoderCatalogService binds model discovery without starting network work.
// A nil clock uses time.Now. Application assembly supplies storage and fetch
// callbacks before an administrator can request a manual refresh.
func NewQoderCatalogService(options QoderCatalogOptions) *QoderCatalogService {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &QoderCatalogService{options: options, slots: make(chan struct{}, qoderCatalogConcurrency)}
}

// StopContext closes upstream admission and cancels active manual queries.
// Shutdown waits for the shared transports before their dependencies close.
func (s *QoderCatalogService) StopContext(ctx context.Context) error {
	s.stopping.Store(true)
	return s.probes.StopContext(ctx)
}

// Refresh performs a forced account query and returns its successful snapshot.
// Matching provider identities share one in-flight query. Returned snapshots
// are decoded copies, so callers cannot mutate another waiter's result.
//
// The record is read again after concurrency admission to reject replacement
// credentials before dispatch. A waiter can cancel independently of shared
// fetching, while service shutdown cancels that shared work. Publication uses
// the reread identity and its conditional writer, so an account change during
// the network query cannot install the old account's model capabilities.
func (s *QoderCatalogService) Refresh(ctx context.Context, id int64) (*QoderCatalogSnapshot, error) {
	if s == nil || s.options.Read == nil || s.options.Fetch == nil || s.options.Writer == nil {
		return nil, errors.New("qoder model synchronization is not configured")
	}
	record, err := s.options.Read(ctx, id)
	if err != nil {
		return nil, err
	}
	if record == nil || !record.IsQoderCosy() {
		return nil, errors.New("provider is not a Qoder account")
	}
	identity := QoderCatalogIdentity(record)
	if identity == "" {
		return nil, errors.New("qoder credential identity cannot be encoded")
	}
	key := "qoder-catalog:" + strconv.FormatInt(id, 10) + ":" + identity
	result, err := s.probes.Run(ctx, key, QoderCatalogTimeout+5*time.Second, func(ctx context.Context) (any, error) {
		// Admission shares the query deadline. Cancellation while queued must not
		// start a later upstream request or leave a semaphore slot occupied.
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		current, err := s.options.Read(ctx, id)
		if err != nil {
			return nil, err
		}
		if current == nil || QoderCatalogIdentity(current) != identity {
			return nil, ErrUsageObservationChanged
		}
		// A cancelled transport can release its slot before the runtime reaches
		// every queued waiter's cancel function. Recheck shutdown after admission.
		if s.stopping.Load() {
			return nil, ErrProbeStopped
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return s.refresh(ctx, current)
	})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var snapshot QoderCatalogSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// refresh publishes a complete catalog only after a successful manual query.
// Capturing the provider identity before dispatch prevents a concurrent account
// replacement from accepting an old result. Fetch and persistence failures leave
// the previously saved catalog available to requests for the same identity.
func (s *QoderCatalogService) refresh(ctx context.Context, record *Record) (*QoderCatalogSnapshot, error) {
	version := ObserveUsageVersion(record)
	queryCtx, cancel := context.WithTimeout(ctx, QoderCatalogTimeout)
	defer cancel()
	models, err := s.options.Fetch(queryCtx, record)
	if err != nil {
		return nil, err
	}
	if err := queryCtx.Err(); err != nil {
		return nil, err
	}
	if models == nil {
		models = []qodermodel.Model{}
	}
	encoded, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	snapshot := &QoderCatalogSnapshot{
		Site:           record.GetCredential("site"),
		UID:            record.GetCredential("uid"),
		AID:            record.GetCredential("aid"),
		OrganizationID: record.GetCredential("organization_id"),
		Identity:       QoderCatalogIdentity(record),
		Version:        hex.EncodeToString(digest[:]),
		FetchedAt:      s.options.Now(),
		Models:         models,
	}
	if snapshot.Site == "" {
		snapshot.Site = "global"
	}
	// The conditional writer publishes the metadata and provider outbox together.
	// A late result cannot restore routes belonging to credentials replaced mid-query.
	applied, err := s.options.Writer.UpdateUsageExtraIfUnchanged(ctx, version, map[string]any{QoderCatalogExtraKey: snapshot})
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, ErrUsageObservationChanged
	}
	return snapshot, nil
}

// PrepareQoderCatalog decodes a freshly hydrated provider's account observation once.
// Database and Redis readers call it before publishing a record. Replacing the
// Extra entry ties the lifetime of the index to the data it represents.
func (r *Record) PrepareQoderCatalog() {
	if r == nil || r.Platform != PlatformQoder {
		return
	}
	if catalog := parseQoderCatalog(r); catalog != nil {
		r.Extra[QoderCatalogExtraKey] = catalog
	}
}

// parseQoderCatalog shares prepared publications and decodes raw storage values.
// Callers reading a shared record never write to it. Malformed or missing
// snapshots return nil so admission cannot discover models from partial data.
func parseQoderCatalog(record *Record) *preparedQoderCatalog {
	if record == nil {
		return nil
	}
	value := record.Extra[QoderCatalogExtraKey]
	if prepared, ok := value.(*preparedQoderCatalog); ok {
		return prepared
	}
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var snapshot QoderCatalogSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil
	}
	qodermodel.NormalizeModels(snapshot.Models)
	version := fmt.Sprintf("%d:%s:%s", record.ID, snapshot.Identity, snapshot.Version)
	return &preparedQoderCatalog{QoderCatalogSnapshot: &snapshot, index: qodermodel.NewCatalog(version, snapshot.Models)}
}

// QoderCatalogIdentity identifies authentication inputs without routing policy.
// Editing a mapping or whitelist does not change the upstream account catalog.
// Known accounts retain their observation across an access-token refresh. A
// site, organization, account, or PAT replacement gets a new identity. When no
// account identifiers are available, use the credential digest conservatively.
// Storage publication separately compares the complete captured configuration.
//
// Lightweight scheduler records carry a digest computed before credential
// filtering. Their private hint preserves the full record's identity decision
// without retaining authentication tokens; ordinary database records compute
// the digest from a copied credential map instead.
func QoderCatalogIdentity(record *Record) string {
	if record == nil {
		return ""
	}
	if record.QoderCatalogIdentityHint != "" {
		return record.QoderCatalogIdentityHint
	}
	var credentials map[string]any
	if record.GetCredential("uid") != "" || record.GetCredential("aid") != "" {
		site := record.GetCredential("site")
		if site == "" {
			site = "global"
		}
		credentials = map[string]any{
			"site":            site,
			"uid":             record.GetCredential("uid"),
			"aid":             record.GetCredential("aid"),
			"organization_id": record.GetCredential("organization_id"),
			"pat":             record.GetCredential("pat"),
		}
	} else {
		credentials = maps.Clone(record.Credentials)
		delete(credentials, "model_mapping")
		delete(credentials, "model_whitelist")
	}
	return RefreshCredentialIdentity(&Record{Platform: record.Platform, Type: record.Type, Credentials: credentials})
}

// QoderCatalog returns a usable observation for the record's exact identity.
// A successful manual sync remains authoritative until the next successful sync
// or an account change. Older snapshots can contain automatic retry timestamps;
// those fields have no effect on manual catalog availability.
func (r *Record) QoderCatalog(now time.Time) (*QoderCatalogSnapshot, error) {
	prepared, err := r.qoderPublication(now)
	if err != nil {
		return nil, err
	}
	snapshot := *prepared.QoderCatalogSnapshot
	snapshot.Models = make([]qodermodel.Model, len(prepared.Models))
	for i, model := range prepared.Models {
		snapshot.Models[i] = model.Clone()
	}
	return &snapshot, nil
}

// qoderPublication checks current credentials before returning the immutable model
// index. An account replacement invalidates the publication even when a caller
// copied the old Extra map onto a newly authorized record.
func (r *Record) qoderPublication(now time.Time) (*preparedQoderCatalog, error) {
	snapshot := parseQoderCatalog(r)
	if snapshot == nil || snapshot.Version == "" || snapshot.Identity == "" || snapshot.Identity != QoderCatalogIdentity(r) || snapshot.Models == nil ||
		snapshot.FetchedAt.IsZero() || snapshot.FetchedAt.After(now.Add(time.Minute)) {
		return nil, ErrQoderCatalogUnavailable
	}
	return snapshot, nil
}

// QoderModelIndex pins the validated account catalog for repeated route lookups.
// Callers retain it only while using the same provider record. An unavailable
// snapshot returns an error distinct from a successful empty publication.
func (r *Record) QoderModelIndex() (*qodermodel.Catalog, error) {
	snapshot, err := r.qoderPublication(time.Now())
	if err != nil {
		return nil, err
	}
	return snapshot.index, nil
}

// QoderCatalogStatus returns the saved catalog for the provider editor.
// An unusable catalog exposes neither model details nor account identifiers.
func (r *Record) QoderCatalogStatus(now time.Time) map[string]any {
	snapshot, err := r.QoderCatalog(now)
	if err != nil {
		return map[string]any{"state": "unavailable"}
	}
	return snapshot.adminStatus()
}

// adminStatus exposes model properties from the accepted account observation.
// Disabled models are included in the property list so administrators can
// inspect their restrictions. Only enabled routes supply whitelist candidates.
func (snapshot *QoderCatalogSnapshot) adminStatus() map[string]any {
	return map[string]any{
		"state":         "fresh",
		"fetched_at":    snapshot.FetchedAt,
		"version":       snapshot.Version,
		"models":        qoderCatalogModelIDs(snapshot),
		"model_details": snapshot.Models,
	}
}

// QoderRequestModelIDs returns one public request ID per enabled account route.
// An unavailable observation yields no automatic candidates; explicit mappings
// remain separately owned by the provider configuration.
func (r *Record) QoderRequestModelIDs() []string {
	snapshot, err := r.qoderPublication(time.Now())
	if err != nil {
		return nil
	}
	return qoderCatalogModelIDs(snapshot.QoderCatalogSnapshot)
}

// qoderCatalogModelIDs returns one preferred ID per enabled model from a validated snapshot.
// Callers validate the saved observation and identity before invoking it; the returned
// slice is independent and retains a nonnil empty list for empty catalogs.
func qoderCatalogModelIDs(snapshot *QoderCatalogSnapshot) []string {
	ids := []string{}
	for _, model := range snapshot.Models {
		if model.Enabled {
			ids = append(ids, model.RequestID())
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// NormalizeQoderModel resolves only aliases observed for this exact account.
// Unknown model names retain their trimmed spelling for explicit custom routes.
func (r *Record) NormalizeQoderModel(model string) string {
	index, err := r.QoderModelIndex()
	if err == nil {
		return index.RouteKey(model)
	}
	return strings.TrimSpace(model)
}

// QoderModelRules binds eligibility and alias resolution to the same publication.
// A successful empty observation removes automatic candidates. Explicit mapping
// targets allow custom routes, but a saved whitelist alone cannot reintroduce a
// retired model. Callers rebuild these rules when replacing the provider record.
//
// The callbacks retain the validated index and a private copy of the mapping.
// This keeps one candidate check from decoding metadata or picking up changes
// made after a prepared scheduling policy selected its provider version.
func (r *Record) QoderModelRules() ModelPlatformRules {
	// Missing catalogs still supply a normalizer for presentation. The eligibility
	// callback rejects every candidate until this account has synchronized successfully.
	index, _ := r.QoderModelIndex()
	mapping := ResolveModelMapping(r, ModelMappingDefaults{})
	return ModelPlatformRules{
		NormalizeQoder: index.RouteKey,
		QoderCompatible: func(model string) bool {
			if index == nil {
				return false
			}
			if enabled, found := index.Enabled(model); found {
				return enabled
			}
			for _, target := range mapping {
				if strings.EqualFold(strings.TrimSpace(target), strings.TrimSpace(model)) {
					return true
				}
			}
			return false
		},
	}
}
