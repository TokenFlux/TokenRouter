package postgres

import (
	"context"
	"database/sql"
	"errors"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbgroup "github.com/TokenFlux/TokenRouter/ent/group"
	dbprovider "github.com/TokenFlux/TokenRouter/ent/provider"
	dbprovidergroup "github.com/TokenFlux/TokenRouter/ent/providergroup"
	dbproxy "github.com/TokenFlux/TokenRouter/ent/proxy"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// provider_data.go creates provider records and hydrates database results with
// proxies and group memberships.
// Provider reads combine database rows with proxy details and group
// memberships before returning records to management and scheduling callers.
// Batched lookups keep the caller's first-occurrence ID order and omit
// missing rows, while conversion copies supported JSON containers and
// hydration adds the remaining associations.
//
// Creation has two persistence paths. A bare create saves the provider and
// attempts its event separately; creation with groups saves the row,
// memberships, and outbox event through one Ent transaction. Input records
// receive generated identity and timestamps before commit, so callers must
// account for those mutations if a later step rolls back.

// Create saves the supplied provider row and then attempts a provider-change
// event through r.sql. Creation mutates the input's ID and timestamps; group
// IDs describe event payload only, and an event error is observed after the
// row save.
func (r *ProviderStore) Create(ctx context.Context, record *provider.Record) error {
	if err := CreateRecord(ctx, r.client, record); err != nil {
		return err
	}
	// A bare row save has completed through the supplied client; event failure is
	// reported separately because group-aware creation owns its own transactional path.
	if err := r.publish(ctx, r.sql, record.ID, record.GroupIDs); err != nil {
		r.observe("[SchedulerOutbox] enqueue provider create failed: provider=%d err=%v", record.ID, err)
	}
	return nil
}

// CreateRecord saves one provider through the supplied Ent client and fills
// its generated ID and timestamps into record. Nil input returns
// ErrProviderNilInput; deprecated Extra keys are removed from the supplied
// map before saving, including when a later database step fails.
//
// Optional scalar pointers select fields to set, while the schema supplies
// defaults for omitted values and quota dimension is normalized. This helper
// uses the supplied client's transaction ownership and performs no group or
// event write. The caller therefore coordinates associations and publication
// around this mutable record result.
func CreateRecord(ctx context.Context, client *dbent.Client, record *provider.Record) error {
	if record == nil {
		return provider.ErrProviderNilInput
	}
	// Remove retired keys from the submitted map before persistence, matching the
	// dedicated patch paths even when later database validation rejects the row.
	provider.DiscardDeprecatedExtra(record.Extra)

	builder := client.Provider.Create().
		SetName(record.Name).
		SetNillableNotes(record.Notes).
		SetPlatform(record.Platform).
		SetType(record.Type).
		SetCredentials(normalizeJSONMap(record.Credentials)).
		SetExtra(normalizeJSONMap(record.Extra)).
		SetConcurrency(record.Concurrency).
		SetPriority(record.Priority).
		SetStatus(record.Status).
		SetErrorMessage(record.ErrorMessage).
		SetSchedulable(record.Schedulable).
		SetAutoPauseOnExpired(record.AutoPauseOnExpired)

	if record.RateMultiplier != nil {
		builder.SetRateMultiplier(*record.RateMultiplier)
	}
	if record.LoadFactor != nil {
		builder.SetLoadFactor(*record.LoadFactor)
	}

	if record.ProxyID != nil {
		builder.SetProxyID(*record.ProxyID)
	}
	if record.LastUsedAt != nil {
		builder.SetLastUsedAt(*record.LastUsedAt)
	}
	if record.ExpiresAt != nil {
		builder.SetExpiresAt(*record.ExpiresAt)
	}
	if record.RateLimitedAt != nil {
		builder.SetRateLimitedAt(*record.RateLimitedAt)
	}
	if record.RateLimitResetAt != nil {
		builder.SetRateLimitResetAt(*record.RateLimitResetAt)
	}
	if record.OverloadUntil != nil {
		builder.SetOverloadUntil(*record.OverloadUntil)
	}
	if record.SessionWindowStart != nil {
		builder.SetSessionWindowStart(*record.SessionWindowStart)
	}
	if record.SessionWindowEnd != nil {
		builder.SetSessionWindowEnd(*record.SessionWindowEnd)
	}
	if record.SessionWindowStatus != "" {
		builder.SetSessionWindowStatus(record.SessionWindowStatus)
	}

	builder.SetQuotaDimension(dbprovider.QuotaDimension(record.QuotaDimensionOrDefault()))
	if record.ParentProviderID != nil {
		builder.SetParentProviderID(*record.ParentProviderID)
	}

	created, err := builder.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, provider.ErrProviderNotFound, nil)
	}

	record.ID = created.ID
	record.CreatedAt = created.CreatedAt
	record.UpdatedAt = created.UpdatedAt
	return nil
}

// CreateWithProviderGroups saves a provider, its memberships, and its change
// event through one Ent transaction client. A locally opened transaction is
// committed here; ErrTxStarted reuses a transaction-bound store client and
// leaves completion to its caller.
//
// Generated ID and timestamps are installed before memberships and the event
// finish. Each supplied membership is assigned that provider ID, and the
// record receives copied membership rows and group IDs before commit. A later
// error can leave those input values populated even when a locally owned
// transaction rolls back.
func (r *ProviderStore) CreateWithProviderGroups(ctx context.Context, record *provider.Record, groups []provider.GroupMembership) error {
	if record == nil {
		return provider.ErrProviderNilInput
	}
	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}

	var txClient *dbent.Client
	if err == nil {
		// Finish only the transaction opened here; rollback also covers later link
		// or outbox failure after generated IDs have already changed the input record.
		defer func() { _ = tx.Rollback() }()
		txClient = tx.Client()
	} else {
		// ErrTxStarted identifies a client already held by the outer owner. Use
		// that connection for the row, links, and event, leaving completion
		// to its caller.
		txClient = r.client
	}

	if err := CreateRecord(ctx, txClient, record); err != nil {
		return err
	}
	groupIDs := make([]int64, 0, len(groups))
	if len(groups) > 0 {
		builders := make([]*dbent.ProviderGroupCreate, 0, len(groups))
		for i := range groups {
			groups[i].ProviderID = record.ID
			groupIDs = append(groupIDs, groups[i].GroupID)
			builders = append(builders, txClient.ProviderGroup.Create().
				SetProviderID(record.ID).
				SetGroupID(groups[i].GroupID),
			)
		}
		if _, err := txClient.ProviderGroup.CreateBulk(builders...).Save(ctx); err != nil {
			return err
		}
	}
	record.GroupIDs = groupIDs
	record.ProviderGroups = append([]provider.GroupMembership(nil), groups...)
	// Enlist the event after the membership writes on the same connection so a
	// failed event cannot commit a locally owned creation that lacks
	// its group notification.
	if err := r.publish(ctx, txClient, record.ID, groupIDs); err != nil {
		return err
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// GetByID loads one provider and hydrates its proxy and group associations.
// Missing rows return ErrProviderNotFound; normal Ent queries hide retained
// deletions, and RecordsFromEntities clones supported nested data and scalar
// pointers before the record is returned.
func (r *ProviderStore) GetByID(ctx context.Context, id int64) (*provider.Record, error) {
	m, err := r.client.Provider.Query().Where(dbprovider.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, provider.ErrProviderNotFound, nil)
	}

	providers, err := r.RecordsFromEntities(ctx, []*dbent.Provider{m})
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, provider.ErrProviderNotFound
	}
	return &providers[0], nil
}

// GetByIDs returns hydrated records in the first-occurrence order of positive
// supplied IDs. Duplicates, nonpositive values, and missing rows are omitted;
// empty or wholly absent input produces an allocated empty slice.
//
// The provider query preloads proxy edges and group associations are read in
// batches. Records are indexed by ID before reconstructing input order, so
// database row order cannot misassociate a result with the caller's
// selection. CloneRecord copies supported collections and scalar pointers at
// the return point; arbitrary mutable values outside CloneValues's supported
// shapes can remain shared.
func (r *ProviderStore) GetByIDs(ctx context.Context, ids []int64) ([]*provider.Record, error) {
	if len(ids) == 0 {
		return []*provider.Record{}, nil
	}

	// Keep positive first occurrences as the reconstruction order; the database ID
	// query below can return rows in another order or omit records deleted meanwhile.
	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return []*provider.Record{}, nil
	}

	entProviders, err := r.client.Provider.
		Query().
		Where(dbprovider.IDIn(uniqueIDs...)).
		WithProxy().
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(entProviders) == 0 {
		return []*provider.Record{}, nil
	}

	providerIDs := make([]int64, 0, len(entProviders))
	entByID := make(map[int64]*dbent.Provider, len(entProviders))
	for _, acc := range entProviders {
		entByID[acc.ID] = acc
		providerIDs = append(providerIDs, acc.ID)
	}

	groupsByProvider, groupIDsByProvider, providerGroupsByProvider, err := r.LoadProviderGroups(ctx, providerIDs)
	if err != nil {
		return nil, err
	}

	outByID := make(map[int64]*provider.Record, len(entProviders))
	for _, entAcc := range entProviders {
		out := r.recordFromEntity(entAcc)
		if out == nil {
			continue
		}

		// The eager edge already uses this query's context and deletion filtering;
		// convert that observed proxy before the final record copy isolates its fields.
		if entAcc.Edges.Proxy != nil {
			out.Proxy = r.options.Proxy(entAcc.Edges.Proxy)
		}

		if groups, ok := groupsByProvider[entAcc.ID]; ok {
			out.Groups = groups
		}
		if groupIDs, ok := groupIDsByProvider[entAcc.ID]; ok {
			out.GroupIDs = groupIDs
		}
		if ags, ok := providerGroupsByProvider[entAcc.ID]; ok {
			out.ProviderGroups = ags
		}
		outByID[entAcc.ID] = out
	}

	// Reconstruct from IDs rather than database positions so associations stay with
	// their provider and missing rows leave a gap instead of shifting later results.
	out := make([]*provider.Record, 0, len(uniqueIDs))
	for _, id := range uniqueIDs {
		if _, ok := entByID[id]; !ok {
			continue
		}
		if acc, ok := outByID[id]; ok && acc != nil {
			out = append(out, provider.CloneRecord(acc))
		}
	}

	return out, nil
}

// RecordsFromEntities hydrates each supplied nonnil Ent row with current
// proxy and group data, retaining input row order. Empty input returns an
// allocated empty slice; callers supply rows rather than nil entries because
// ID collection dereferences every entry.
//
// Proxy lookup includes current and fallback-origin IDs, allowing the
// management view to name the original proxy while forwarding uses the
// selected one. Group IDs and membership rows are retained even when their
// group lookup is absent. Each assembled result passes through CloneRecord
// before return, isolating supported containers and scalar pointers from the
// temporary association maps.
func (r *ProviderStore) RecordsFromEntities(ctx context.Context, providers []*dbent.Provider) ([]provider.Record, error) {
	if len(providers) == 0 {
		return []provider.Record{}, nil
	}

	providerIDs := make([]int64, 0, len(providers))
	proxyIDs := make([]int64, 0, len(providers))
	for _, acc := range providers {
		providerIDs = append(providerIDs, acc.ID)
		if acc.ProxyID != nil {
			proxyIDs = append(proxyIDs, *acc.ProxyID)
		}
		if acc.ProxyFallbackOriginID != nil {
			proxyIDs = append(proxyIDs, *acc.ProxyFallbackOriginID)
		}
	}

	proxyMap, err := r.LoadProxies(ctx, proxyIDs)
	if err != nil {
		return nil, err
	}
	groupsByProvider, groupIDsByProvider, providerGroupsByProvider, err := r.LoadProviderGroups(ctx, providerIDs)
	if err != nil {
		return nil, err
	}

	outProviders := make([]provider.Record, 0, len(providers))
	for _, acc := range providers {
		out := r.recordFromEntity(acc)
		if out == nil {
			continue
		}
		if acc.ProxyID != nil {
			if proxy, ok := proxyMap[*acc.ProxyID]; ok {
				out.Proxy = proxy
			}
		}
		out.ProxyFallbackOriginID = acc.ProxyFallbackOriginID
		if acc.ProxyFallbackOriginID != nil {
			if op, ok := proxyMap[*acc.ProxyFallbackOriginID]; ok && op != nil {
				n := op.Name
				out.ProxyFallbackOriginName = &n
			}
		}
		if groups, ok := groupsByProvider[acc.ID]; ok {
			out.Groups = groups
		}
		if groupIDs, ok := groupIDsByProvider[acc.ID]; ok {
			out.GroupIDs = groupIDs
		}
		if ags, ok := providerGroupsByProvider[acc.ID]; ok {
			out.ProviderGroups = ags
		}
		outProviders = append(outProviders, *provider.CloneRecord(out))
	}

	return outProviders, nil
}

// LoadProxies returns converted proxies indexed by their positive unique IDs.
// Invalid and duplicate IDs are omitted before bounded query batches; missing
// rows simply lack a map entry, and conversion requires the configured Proxy
// callback for every loaded row.
func (r *ProviderStore) LoadProxies(ctx context.Context, proxyIDs []int64) (map[int64]*egress.Proxy, error) {
	proxyMap := make(map[int64]*egress.Proxy)
	proxyIDs = UniquePositiveInt64s(proxyIDs)
	if len(proxyIDs) == 0 {
		return proxyMap, nil
	}

	// Bound the IN parameters before proxy conversion and record hydration.
	for start := 0; start < len(proxyIDs); start += postgresParameterBatchSize {
		end := min(start+postgresParameterBatchSize, len(proxyIDs))
		proxies, err := r.client.Proxy.Query().Where(dbproxy.IDIn(proxyIDs[start:end]...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range proxies {
			proxyMap[p.ID] = r.options.Proxy(p)
		}
	}
	return proxyMap, nil
}

// LoadProviderGroups returns parallel maps of group records, group IDs, and
// membership rows for the requested providers. Positive unique provider IDs
// are queried in bounded batches and membership ordering follows provider ID
// then group ID.
//
// A missing group record leaves its membership and ID visible while the
// full-group list omits that record. Those three outputs therefore describe
// related but different views of the same stored links. Group pointers within
// the outputs are shared with the converted lookup map; hydrated callers
// apply CloneRecord when returning their assembled provider records.
func (r *ProviderStore) LoadProviderGroups(ctx context.Context, providerIDs []int64) (map[int64][]*accessview.GroupConfig, map[int64][]int64, map[int64][]provider.GroupMembership, error) {
	groupsByProvider := make(map[int64][]*accessview.GroupConfig)
	groupIDsByProvider := make(map[int64][]int64)
	providerGroupsByProvider := make(map[int64][]provider.GroupMembership)

	providerIDs = UniquePositiveInt64s(providerIDs)
	if len(providerIDs) == 0 {
		return groupsByProvider, groupIDsByProvider, providerGroupsByProvider, nil
	}

	// Keep provider-ID batches below the parameter limit before loading memberships.
	for start := 0; start < len(providerIDs); start += postgresParameterBatchSize {
		end := min(start+postgresParameterBatchSize, len(providerIDs))
		entries, err := r.client.ProviderGroup.Query().
			Where(dbprovidergroup.ProviderIDIn(providerIDs[start:end]...)).
			Order(dbprovidergroup.ByProviderID(), dbprovidergroup.ByGroupID()).
			All(ctx)
		if err != nil {
			return nil, nil, nil, err
		}

		groupIDs := make([]int64, 0, len(entries))
		for _, ag := range entries {
			groupIDs = append(groupIDs, ag.GroupID)
		}
		groupMap, err := r.LoadGroups(ctx, groupIDs)
		if err != nil {
			return nil, nil, nil, err
		}

		for _, ag := range entries {
			groupSvc := groupMap[ag.GroupID]
			agSvc := provider.GroupMembership{
				ProviderID: ag.ProviderID,
				GroupID:    ag.GroupID,
				CreatedAt:  ag.CreatedAt,
				Group:      groupSvc,
			}
			providerGroupsByProvider[ag.ProviderID] = append(providerGroupsByProvider[ag.ProviderID], agSvc)
			groupIDsByProvider[ag.ProviderID] = append(groupIDsByProvider[ag.ProviderID], ag.GroupID)
			if groupSvc != nil {
				groupsByProvider[ag.ProviderID] = append(groupsByProvider[ag.ProviderID], groupSvc)
			}
		}
	}

	return groupsByProvider, groupIDsByProvider, providerGroupsByProvider, nil
}

// LoadGroups converts existing groups and indexes them by positive unique ID.
// Empty input returns an empty map; each bounded Ent batch uses the
// configured Group callback, and missing group IDs are omitted.
func (r *ProviderStore) LoadGroups(ctx context.Context, groupIDs []int64) (map[int64]*accessview.GroupConfig, error) {
	groupMap := make(map[int64]*accessview.GroupConfig)
	groupIDs = UniquePositiveInt64s(groupIDs)
	if len(groupIDs) == 0 {
		return groupMap, nil
	}

	// Bound group-ID parameters while retaining all loaded groups in one map.
	for start := 0; start < len(groupIDs); start += postgresParameterBatchSize {
		end := min(start+postgresParameterBatchSize, len(groupIDs))
		groups, err := r.client.Group.Query().Where(dbgroup.IDIn(groupIDs[start:end]...)).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			groupMap[g.ID] = r.options.Group(g)
		}
	}
	return groupMap, nil
}

// UniquePositiveInt64s returns positive IDs in their first-occurrence order
// using new slice storage. Empty input returns nil; a nonempty input
// containing no positive values returns an allocated empty slice, and the
// supplied slice is retained unchanged.
func UniquePositiveInt64s(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// RecordFromEntity converts a nonnil Ent row into the provider
// representation, copying supported credential and Extra containers. Nil
// returns nil; RateMultiplier receives fresh storage, while other nullable
// scalar pointers remain borrowed from the Ent row until a caller performs
// CloneRecord.
func RecordFromEntity(m *dbent.Provider) *provider.Record {
	if m == nil {
		return nil
	}

	rateMultiplier := m.RateMultiplier

	record := &provider.Record{
		ID:                      m.ID,
		Name:                    m.Name,
		Notes:                   m.Notes,
		Platform:                m.Platform,
		Type:                    m.Type,
		Credentials:             provider.CloneValues(m.Credentials),
		Extra:                   provider.CloneValues(m.Extra),
		ProxyID:                 m.ProxyID,
		ProxyFallbackOriginID:   m.ProxyFallbackOriginID,
		Concurrency:             m.Concurrency,
		Priority:                m.Priority,
		RateMultiplier:          &rateMultiplier,
		LoadFactor:              m.LoadFactor,
		Status:                  m.Status,
		ErrorMessage:            derefString(m.ErrorMessage),
		LastUsedAt:              m.LastUsedAt,
		ExpiresAt:               m.ExpiresAt,
		AutoPauseOnExpired:      m.AutoPauseOnExpired,
		CreatedAt:               m.CreatedAt,
		UpdatedAt:               m.UpdatedAt,
		Schedulable:             m.Schedulable,
		RateLimitedAt:           m.RateLimitedAt,
		RateLimitResetAt:        m.RateLimitResetAt,
		OverloadUntil:           m.OverloadUntil,
		TempUnschedulableUntil:  m.TempUnschedulableUntil,
		TempUnschedulableReason: derefString(m.TempUnschedulableReason),
		SessionWindowStart:      m.SessionWindowStart,
		SessionWindowEnd:        m.SessionWindowEnd,
		SessionWindowStatus:     derefString(m.SessionWindowStatus),
		ParentProviderID:        m.ParentProviderID,
		QuotaDimension:          string(m.QuotaDimension),
	}
	record.PrepareQoderCatalog()
	return record
}

// translatePersistenceError attaches matching database failures to the
// supplied application error while retaining their cause. Nil targets disable
// their mapping; not-found covers sql.ErrNoRows and Ent NotFound, unique
// violations use the PostgreSQL classifier, and unmatched failures pass
// through.
func translatePersistenceError(err error, notFound, conflict *infraerrors.ApplicationError) error {
	if err == nil {
		return nil
	}

	if notFound != nil && (errors.Is(err, sql.ErrNoRows) || dbent.IsNotFound(err)) {
		return notFound.WithCause(err)
	}

	if conflict != nil && postgresinfra.IsUniqueConstraintViolation(err) {
		return conflict.WithCause(err)
	}

	return err
}
