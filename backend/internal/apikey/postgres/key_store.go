package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/apikey"
	"github.com/TokenFlux/TokenRouter/ent/apikeycompositegroup"
	"github.com/TokenFlux/TokenRouter/ent/group"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/ent/user"
	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	usagequery "github.com/TokenFlux/TokenRouter/internal/usage/postgres/query"
)

// key_store.go persists Key configuration and composite bindings and loads
// credential views for authentication and administration. Selected
// configuration columns are written to currently nondeleted rows so unrelated
// edits retain billing-owned counters. Composite bindings require a
// transaction with their owning Key record, while ordinary reads choose only
// relationships and fields required by their caller. Authentication conversion
// receives those selected entity values and independently constructs its
// cached view.
//
// Creation locks the PostgreSQL user row before checking the Key count and
// inserting, keeping concurrent same-user creation under its configured limit.
// Existing transaction clients leave completion to the caller. Deletion
// tombstones the unique credential while retaining its row ID, and usage
// sorting loads all filtered candidates before choosing the page. The store
// shares its SQL and Ent dependencies and delegates billing counter operations
// to the separate KeyUsageStore. Console ungrouped filters include ordinary
// Keys whose referenced group is soft-deleted, matching their missing group
// view while retaining the stored binding used by authentication.

// KeyStore persists credential configuration, bindings and selected console or
// authentication views.
type KeyStore struct {
	// client and sql are shared dependencies selected by assembly or a transaction
	// owner. Keep both usable through reads and writes; transaction-bound stores
	// leave completion to that owner, while newly opened transactions are closed here.
	client *dbent.Client // shared Ent credential client
	sql    SQLExecutor   // shared SQL operation executor
	// usageTotals is retained for complete-candidate usage ordering. A nil
	// callback reports an error on that sort path before a page can be selected.
	usageTotals UsageTotalsReader // configured total-spending observation reader
}

// UsageTotalsReader loads total spending observations used to sort filtered
// Keys before pagination.
type UsageTotalsReader func(context.Context, []int64) (map[int64]float64, error)

// NewKeyStore constructs the Key adapter from shared Ent, SQL and usage-total
// dependencies. The caller owns their lifetimes; the supplied usage reader is
// required when listing sorts by usage instead of a stored column.
func NewKeyStore(client *dbent.Client, sqlDB *sql.DB, usage UsageTotalsReader) *KeyStore {
	return NewKeyStoreWithSQL(client, sqlDB, usage)
}

// NewKeyStoreWithSQL retains caller-supplied storage interfaces and the
// usage-total callback. The caller can supply a transaction-bound client and
// executor, and owns commit, rollback and final resource closure for those
// dependencies.
func NewKeyStoreWithSQL(client *dbent.Client, sqlq SQLExecutor, usage UsageTotalsReader) *KeyStore {
	return &KeyStore{client: client, sql: sqlq, usageTotals: usage}
}

// KeyActiveQuery returns a new query excluding soft-deleted credential records.
func (r *KeyStore) KeyActiveQuery() *dbent.APIKeyQuery {
	return r.client.APIKey.Query().Where(apikey.DeletedAtIsNil())
}

// KeyApiKeyFastModePolicyForPersistence normalizes an accepted Key Fast choice
// before storage, defaulting empty input through the shared normalizer.
// Unsupported text is returned as supplied, leaving validation or persistence
// rejection to its caller.
func KeyApiKeyFastModePolicyForPersistence(value string) string {
	policy, ok := keycore.NormalizeAPIKeyFastModePolicy(value)
	if ok {
		return policy
	}
	return value
}

// KeyApiKeyBillingModeForPersistence returns the normalized funding mode when
// the shared validator accepts it. Empty input selects auto; unsupported text
// passes through unchanged so this helper supplies no independent rejection.
func KeyApiKeyBillingModeForPersistence(value string) string {
	mode, ok := keycore.NormalizeAPIKeyBillingMode(value)
	if ok {
		return mode
	}
	return value
}

// Create persists the supplied Key and its complete composite bindings, filling
// stored identity and timestamps after success. Nil input is rejected before a
// transaction begins.
//
// The PostgreSQL owner row is locked before counting all nondeleted
// credentials, including managed Keys, and checking a positive user limit. Key
// creation and binding replacement use the same client. Owned transaction
// failure triggers rollback; a caller-owned transaction receives no-op
// completion callbacks and still needs its final commit. Persistence uniqueness
// failures map to the existing-Key error, and the source record is updated only
// after the completion callback succeeds.
func (r *KeyStore) Create(ctx context.Context, key *keycore.APIKey) error {
	if key == nil {
		return fmt.Errorf("api key is required")
	}

	// Count and insert in one transaction; the PostgreSQL user-row lock serializes creation for that user.
	txClient, sqlq, commit, rollback, err := r.KeyBeginAPIKeyCreateTransaction(ctx)
	if err != nil {
		return err
	}
	defer rollback()

	current, limit, err := r.KeyLockAPIKeyOwnerAndCount(ctx, sqlq, key.UserID)
	if err != nil {
		return err
	}
	if limit > 0 && current >= int64(limit) {
		return keycore.NewAPIKeyLimitReachedError(current, limit)
	}

	created, err := KeyCreateAPIKeyRecord(ctx, txClient, key)
	if err != nil {
		return translatePersistenceError(err, nil, keycore.ErrAPIKeyExists)
	}
	// Publish the stored identity after completion succeeds. Caller-owned
	// transactions use a no-op here and remain responsible for their eventual commit.
	if err := commit(); err != nil {
		return err
	}

	key.ID = created.ID
	key.LastUsedAt = created.LastUsedAt
	key.CreatedAt = created.CreatedAt
	key.UpdatedAt = created.UpdatedAt
	return nil
}

// KeyBeginAPIKeyCreateTransaction returns the client, executor and completion
// callbacks for Key insertion. A context Ent transaction supplies its own
// client with no-op commit and rollback callbacks.
//
// Otherwise the store attempts to start a transaction and owns the returned
// completion callbacks on success. ErrTxStarted identifies a store already
// bound to a caller transaction; that path requires a configured SQL executor
// and leaves completion to the caller. Other begin failures are returned, and
// successful callers defer rollback while using the same returned client and
// executor for count and insertion.
func (r *KeyStore) KeyBeginAPIKeyCreateTransaction(ctx context.Context) (*dbent.Client, SQLExecutor, func() error, func(), error) {
	if existing := dbent.TxFromContext(ctx); existing != nil {
		client := existing.Client()
		return client, client, func() error { return nil }, func() {}, nil
	}

	tx, err := r.client.Tx(ctx)
	if err == nil {
		client := tx.Client()
		return client, client, tx.Commit, func() { _ = tx.Rollback() }, nil
	}
	if !errors.Is(err, dbent.ErrTxStarted) {
		return nil, nil, nil, nil, err
	}

	// ErrTxStarted means this store already uses a transaction-bound client.
	// Reuse its executor with no-op cleanup so this helper cannot finish caller-owned work.
	if r.sql == nil {
		return nil, nil, nil, nil, fmt.Errorf("sql executor is not configured")
	}
	return r.client, r.sql, func() error { return nil }, func() {}, nil
}

// KeyLockAPIKeyOwnerAndCount reads the nondeleted creator's Key allowance and
// current nondeleted credential count. PostgreSQL adds a user-row update lock,
// so callers keep the transaction open through insertion; other dialects use
// the same reads without that lock. Missing creator rows become
// ErrUserNotFound.
//
// Use the insertion transaction's executor for these reads. Retaining its owner
// lock through the later insert makes the next cooperating creator count again
// after the preceding transaction completes, including rollback of failed insertion.
func (r *KeyStore) KeyLockAPIKeyOwnerAndCount(ctx context.Context, sqlq SQLExecutor, userID int64) (int64, int, error) {
	lockQuery := `SELECT api_key_limit FROM users WHERE id = $1 AND deleted_at IS NULL`
	if r.client.Driver().Dialect() == dialect.Postgres {
		lockQuery += ` FOR UPDATE`
	}

	var limit int
	if err := scanSingleRow(ctx, sqlq, lockQuery, []any{userID}, &limit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, keycore.ErrUserNotFound
		}
		return 0, 0, err
	}

	var current int64
	const countQuery = `SELECT COUNT(*) FROM api_keys WHERE user_id = $1 AND deleted_at IS NULL`
	if err := scanSingleRow(ctx, sqlq, countQuery, []any{userID}, &current); err != nil {
		return 0, 0, err
	}
	return current, limit, nil
}

// KeyCreateAPIKeyRecord inserts the Key record and replaces its composite
// mappings using the supplied client. The caller supplies an active transaction
// when both writes need rollback together; a binding failure can follow record
// insertion before this helper returns its error.
//
// Binding rows use the ID returned by record insertion, so those steps cannot
// be reversed. Success means insertion and complete binding replacement both
// finished in the supplied transaction. The returned entity is the owning Key
// row; after failure, its transaction owner rolls back completed writes before
// deciding how to retry.
func KeyCreateAPIKeyRecord(ctx context.Context, client *dbent.Client, key *keycore.APIKey) (*dbent.APIKey, error) {
	builder := client.APIKey.Create().
		SetUserID(key.UserID).
		SetNillableTeamID(key.TeamID).
		SetKey(key.Key).
		SetName(key.Name).
		SetStatus(key.Status).
		SetIsComposite(key.IsComposite).
		SetFastModePolicy(KeyApiKeyFastModePolicyForPersistence(key.FastModePolicy)).
		SetBillingMode(KeyApiKeyBillingModeForPersistence(key.BillingMode)).
		SetNillablePreferredSubscriptionID(key.PreferredSubscriptionID).
		SetModelMapping(keycore.CloneModelMapping(key.ModelMapping)).
		SetNillableGroupID(key.GroupID).
		SetNillableLastUsedAt(key.LastUsedAt).
		SetNillableManagedBy(key.ManagedBy).
		SetQuota(key.Quota).
		SetQuotaUsed(key.QuotaUsed).
		SetNillableExpiresAt(key.ExpiresAt).
		SetMaxTokenRateMultiplier(key.MaxTokenRateMultiplier).
		SetConcurrencyLimit(key.ConcurrencyLimit).
		SetRpmLimit(key.RPMLimit).
		SetRateLimit5h(key.RateLimit5h).
		SetRateLimit1d(key.RateLimit1d).
		SetRateLimit7d(key.RateLimit7d).
		SetFallbackWhenGroupUnavailable(key.FallbackWhenGroupUnavailable)

	if len(key.IPWhitelist) > 0 {
		builder.SetIPWhitelist(key.IPWhitelist)
	}
	if len(key.IPBlacklist) > 0 {
		builder.SetIPBlacklist(key.IPBlacklist)
	}

	// Insert the owning record before its mappings so their foreign key uses
	// the assigned ID; a later binding failure leaves rollback to the caller.
	created, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := KeyReplaceCompositeGroupRecords(ctx, client, created.ID, key.CompositeGroups); err != nil {
		return nil, err
	}
	return created, nil
}

// KeyReplaceCompositeGroupRecords deletes the Key's saved mappings and
// bulk-inserts the supplied complete replacement. Empty input clears mappings,
// and IDs or request overrides on the supplied binding records are not
// persisted.
//
// The caller owns transaction consistency across deletion and insertion. A
// failed bulk insert must roll back the preceding deletion when old mappings
// need to remain available. The provided Prefix, NormalizedPrefix, GroupID and
// SortOrder values become storage input; service validation establishes their
// uniqueness and ordering rules before this helper runs.
func KeyReplaceCompositeGroupRecords(ctx context.Context, client *dbent.Client, apiKeyID int64, bindings []keycore.APIKeyCompositeGroup) error {
	// Replace all rows within the caller's transaction; a later bulk-insert
	// failure requires rollback so the preceding deletion cannot discard saved prefixes.
	if _, err := client.APIKeyCompositeGroup.Delete().
		Where(apikeycompositegroup.APIKeyIDEQ(apiKeyID)).
		Exec(ctx); err != nil {
		return err
	}
	if len(bindings) == 0 {
		return nil
	}
	builders := make([]*dbent.APIKeyCompositeGroupCreate, 0, len(bindings))
	for _, binding := range bindings {
		builders = append(builders, client.APIKeyCompositeGroup.Create().
			SetAPIKeyID(apiKeyID).
			SetGroupID(binding.GroupID).
			SetPrefix(binding.Prefix).
			SetNormalizedPrefix(binding.NormalizedPrefix).
			SetSortOrder(binding.SortOrder))
	}
	return client.APIKeyCompositeGroup.CreateBulk(builders...).Exec(ctx)
}

// KeyWithAPIKeyCompositeGroups adds binding and group relationship loading to
// query. Mappings are ordered by configured SortOrder and then row ID; the
// supplied query remains the returned query instance for further filtering.
func KeyWithAPIKeyCompositeGroups(query *dbent.APIKeyQuery) *dbent.APIKeyQuery {
	return query.WithCompositeGroups(func(q *dbent.APIKeyCompositeGroupQuery) {
		q.Order(dbent.Asc(apikeycompositegroup.FieldSortOrder), dbent.Asc(apikeycompositegroup.FieldID)).
			WithGroup()
	})
}

// GetByID loads a nondeleted credential with its user, ordinary group and
// ordered composite binding relationships. Missing rows return
// ErrAPIKeyNotFound; managed-Key visibility and creator ownership are checked
// by the user service or handler after loading.
func (r *KeyStore) GetByID(ctx context.Context, id int64) (*keycore.APIKey, error) {
	m, err := KeyWithAPIKeyCompositeGroups(r.KeyActiveQuery()).
		Where(apikey.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

// GetManagedKeyByUserAndGroup loads a nondeleted credential matching creator,
// group and manager, with its ordinary group view. Missing rows return
// ErrAPIKeyNotFound; Only requires one matching record, so multiple matches
// remain a persistence error.
func (r *KeyStore) GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*keycore.APIKey, error) {
	m, err := r.KeyActiveQuery().
		Where(
			apikey.UserIDEQ(userID),
			apikey.GroupIDEQ(groupID),
			apikey.ManagedByEQ(managedBy),
		).
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

// CreateManagedKey delegates managed credential insertion to the normal Key
// creation path. Callers set ManagedBy before the call; the creator's
// nondeleted Key count limit includes this hidden execution credential.
func (r *KeyStore) CreateManagedKey(ctx context.Context, key *keycore.APIKey) error {
	return r.Create(ctx, key)
}

// GetKeyAndOwnerID selects the stored credential and creator ID for a
// nondeleted record. It loads no related records, and a missing match reports
// ErrAPIKeyNotFound for callers preparing deletion or invalidation.
func (r *KeyStore) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	m, err := r.KeyActiveQuery().
		Where(apikey.IDEQ(id)).
		Select(apikey.FieldKey, apikey.FieldUserID).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return "", 0, keycore.ErrAPIKeyNotFound
		}
		return "", 0, err
	}
	return m.Key, m.UserID, nil
}

// GetByKey loads a nondeleted credential with current user group restrictions,
// its ordinary group and composite binding relationships. Credential text is
// passed to storage as given, and an absent record returns ErrAPIKeyNotFound.
func (r *KeyStore) GetByKey(ctx context.Context, key string) (*keycore.APIKey, error) {
	m, err := KeyWithAPIKeyCompositeGroups(r.KeyActiveQuery()).
		Where(apikey.KeyEQ(key)).
		WithUser(func(q *dbent.UserQuery) {
			q.WithAllowedGroups(func(gq *dbent.GroupQuery) {
				gq.Select(group.FieldID)
			})
			q.WithUserDisabledPublicGroups()
		}).
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

// GetByKeyForAuth loads the selected Key, creator-user and ordinary-group columns
// used for authentication, plus ordered composite groups. Missing records
// return ErrAPIKeyNotFound. Ordinary-group fields outside its select list
// retain zero values during view conversion, while composite group
// relationships use their full load path. In particular, ordinary queries leave
// UnavailableFallbackGroupID unset because it is outside this select list.
func (r *KeyStore) GetByKeyForAuth(ctx context.Context, key string) (*keycore.APIKey, error) {
	m, err := r.KeyActiveQuery().
		Where(apikey.KeyEQ(key)).
		Select(
			apikey.FieldID,
			apikey.FieldUserID,
			apikey.FieldTeamID,
			apikey.FieldTeamOwnerDisabled,
			apikey.FieldCreatedAt,
			apikey.FieldGroupID,
			apikey.FieldIsComposite,
			apikey.FieldName,
			apikey.FieldStatus,
			apikey.FieldFastModePolicy,
			apikey.FieldBillingMode,
			apikey.FieldPreferredSubscriptionID,
			apikey.FieldModelMapping,
			apikey.FieldIPWhitelist,
			apikey.FieldIPBlacklist,
			apikey.FieldQuota,
			apikey.FieldQuotaUsed,
			apikey.FieldExpiresAt,
			apikey.FieldMaxTokenRateMultiplier,
			apikey.FieldConcurrencyLimit,
			apikey.FieldRpmLimit,
			apikey.FieldRateLimit5h,
			apikey.FieldRateLimit1d,
			apikey.FieldRateLimit7d,
			apikey.FieldFallbackWhenGroupUnavailable,
		).
		WithUser(func(q *dbent.UserQuery) {
			q.Select(
				user.FieldID,
				user.FieldEmail,
				user.FieldUsername,
				user.FieldStatus,
				user.FieldRole,
				user.FieldBalance,
				user.FieldConcurrency,
				user.FieldBalanceNotifyEnabled,
				user.FieldBalanceNotifyThresholdType,
				user.FieldBalanceNotifyThreshold,
				user.FieldBalanceNotifyExtraEmails,
				user.FieldTotalRecharged,
				user.FieldSignupSource,
				user.FieldLastLoginAt,
				user.FieldLastActiveAt,
				user.FieldRpmLimit,
			)
			q.WithUserDisabledPublicGroups()
			q.WithAllowedGroups(func(gq *dbent.GroupQuery) {
				gq.Select(group.FieldID)
			})
		}).
		WithGroup(func(q *dbent.GroupQuery) {
			q.Select(
				group.FieldID,
				group.FieldName,

				group.FieldSchedulerType,
				group.FieldAdvancedSchedulerOverrides,
				group.FieldIsExclusive,
				group.FieldStatus,
				group.FieldRateMultiplier,
				group.FieldAllowImageGeneration,
				group.FieldAllowBatchImageGeneration,
				group.FieldRoutingPolicy,
				group.FieldClaudeCodeOnly,
				group.FieldFallbackGroupID,
				group.FieldFallbackGroupIDOnInvalidRequest,
				group.FieldModelRoutingEnabled,
				group.FieldModelRouting,
				group.FieldMcpXMLInject,
				group.FieldSupportedModelScopes,
				group.FieldAllowedProtocols,
				group.FieldProtocolFallbacks,
				group.FieldResponsesImagePolicy,
				group.FieldAllowLive,
				group.FieldForceOpenaiFast,
				group.FieldOpenaiFastPolicy,
				group.FieldDefaultMappedModel,
				group.FieldModelsListConfig,
				group.FieldRpmLimit,
				group.FieldMaxReasoningEffort,
				group.FieldMaxReasoningEffortOverLimit,
				group.FieldReasoningEffortMappings,
				group.FieldSessionIsolationEnabled,
			)
		}).
		WithCompositeGroups(func(q *dbent.APIKeyCompositeGroupQuery) {
			q.Order(dbent.Asc(apikeycompositegroup.FieldSortOrder), dbent.Asc(apikeycompositegroup.FieldID)).
				WithGroup()
		}).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, keycore.ErrAPIKeyNotFound
		}
		return nil, err
	}
	return KeyApiKeyEntityToService(m), nil
}

// Update writes the configuration and reset columns selected by fields on a
// currently nondeleted Key. An empty mask returns before any write, and a
// missing matching row reports ErrAPIKeyNotFound.
//
// CompositeConfiguration opens an owned transaction when possible so the Key
// row and replacement mappings commit together; existing transaction contexts
// or transaction-bound stores leave completion to their owner. Unselected
// billing counters retain current database values. The update uses one
// eligibility predicate against concurrent deletion, and the caller record
// receives the chosen timestamp after selected writes finish. Failed multi-step
// changes require the owning transaction to roll back.
func (r *KeyStore) Update(ctx context.Context, key *keycore.APIKey, fields keycore.APIKeyUpdateFields) error {
	// An empty field mask means the caller requested no writes.
	if fields.IsEmpty() {
		return nil
	}

	// The mode flag and saved prefix rows describe one configuration. Own
	// their transaction together so replacement failure cannot commit only the Key row.
	if fields.CompositeConfiguration && dbent.TxFromContext(ctx) == nil {
		tx, err := r.client.Tx(ctx)
		if err == nil {
			// Roll back this owned transaction after a failed Key-row or mapping
			// replacement write so those components cannot commit as a partial configuration.
			defer func() { _ = tx.Rollback() }()
			opCtx := dbent.NewTxContext(ctx, tx)
			if err := r.Update(opCtx, key, fields); err != nil {
				return err
			}
			return tx.Commit()
		}
		if !errors.Is(err, dbent.ErrTxStarted) {
			return err
		}
	}
	// Include the soft-delete predicate in the update to prevent writing a concurrently deleted key.
	// A separate existence check would race with deletion.
	// Update().Where() applies the predicate and mutation in one statement.
	// Zero affected rows report a missing or deleted key.
	// Set updated_at here so the returned timestamp needs no second query.
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	builder := client.APIKey.Update().
		Where(apikey.IDEQ(key.ID), apikey.DeletedAtIsNil()).
		SetUpdatedAt(now)
	if fields.Name {
		builder.SetName(key.Name)
	}
	if fields.Status {
		builder.SetStatus(key.Status)
	}
	if fields.CompositeConfiguration {
		builder.SetIsComposite(key.IsComposite)
	}
	if fields.FastModePolicy {
		builder.SetFastModePolicy(KeyApiKeyFastModePolicyForPersistence(key.FastModePolicy))
	}
	if fields.BillingConfiguration {
		builder.SetBillingMode(KeyApiKeyBillingModeForPersistence(key.BillingMode))
		if key.PreferredSubscriptionID == nil {
			builder.ClearPreferredSubscriptionID()
		} else {
			builder.SetPreferredSubscriptionID(*key.PreferredSubscriptionID)
		}
	}
	if fields.ModelMapping {
		builder.SetModelMapping(keycore.CloneModelMapping(key.ModelMapping))
	}
	if fields.FallbackWhenGroupUnavailable {
		builder.SetFallbackWhenGroupUnavailable(key.FallbackWhenGroupUnavailable)
	}
	if fields.Quota {
		builder.SetQuota(key.Quota)
	}
	if fields.MaxTokenRateMultiplier {
		builder.SetMaxTokenRateMultiplier(key.MaxTokenRateMultiplier)
	}
	if fields.ConcurrencyLimit {
		builder.SetConcurrencyLimit(key.ConcurrencyLimit)
	}
	if fields.RPMLimit {
		builder.SetRpmLimit(key.RPMLimit)
	}
	if fields.RateLimits {
		builder.
			SetRateLimit5h(key.RateLimit5h).
			SetRateLimit1d(key.RateLimit1d).
			SetRateLimit7d(key.RateLimit7d)
	}
	if fields.GroupID {
		if key.GroupID != nil {
			builder.SetGroupID(*key.GroupID)
		} else {
			builder.ClearGroupID()
		}
	}

	if fields.ExpiresAt {
		if key.ExpiresAt != nil {
			builder.SetExpiresAt(*key.ExpiresAt)
		} else {
			builder.ClearExpiresAt()
		}
	}

	// An empty supplied IP rule set clears the corresponding stored field.
	if fields.IPRules {
		if len(key.IPWhitelist) > 0 {
			builder.SetIPWhitelist(key.IPWhitelist)
		} else {
			builder.ClearIPWhitelist()
		}
		if len(key.IPBlacklist) > 0 {
			builder.SetIPBlacklist(key.IPBlacklist)
		} else {
			builder.ClearIPBlacklist()
		}
	}

	billingpostgres.ApplyKeyUsageReset(builder, billing.KeyUsageReset{ResetQuota: fields.QuotaUsed, ResetWindows: fields.RateLimitUsage, QuotaUsed: key.QuotaUsed, Windows: billing.APIKeyRateLimitData{Usage5h: key.Usage5h, Usage1d: key.Usage1d, Usage7d: key.Usage7d, Window5hStart: key.Window5hStart, Window1dStart: key.Window1dStart, Window7dStart: key.Window7dStart}})
	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// The update predicate found no current nondeleted credential record.
		return keycore.ErrAPIKeyNotFound
	}
	if fields.CompositeConfiguration {
		if err := KeyReplaceCompositeGroupRecords(ctx, client, key.ID, key.CompositeGroups); err != nil {
			return err
		}
	}

	// Return the timestamp selected for this write after all selected changes
	// finish; an extra record read could instead observe a concurrent deletion.
	key.UpdatedAt = now
	return nil
}

// RotateCredential replaces the secret and timestamp when ID, creator, old
// secret, nondeleted state and unmanaged state match. It returns rotation
// conflict when no row matches, and leaves configuration and usage columns
// outside the update so a stale view cannot overwrite them.
func (r *KeyStore) RotateCredential(ctx context.Context, key *keycore.APIKey, oldKey string) error {
	now := time.Now()
	affected, err := clientFromContext(ctx, r.client).APIKey.Update().
		Where(
			apikey.IDEQ(key.ID),
			apikey.UserIDEQ(key.UserID),
			apikey.KeyEQ(oldKey),
			apikey.DeletedAtIsNil(),
			apikey.ManagedByIsNil(),
		).
		SetKey(key.Key).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// Rotation, deletion, or ownership changes make the supplied old credential ineligible.
		return keycore.ErrAPIKeyRotationConflict
	}
	key.UpdatedAt = now
	return nil
}

// Delete replaces a nondeleted record's credential with a short tombstone and
// sets its deletion time in one update. An already deleted existing row returns
// success; an absent ID returns ErrAPIKeyNotFound after an unrestricted
// existence check.
func (r *KeyStore) Delete(ctx context.Context, id int64) error {
	// A short tombstone frees the unique credential while fitting the 128-character column.
	tombstoneKey := fmt.Sprintf("__deleted__%d__%d", id, time.Now().UnixNano())
	// Replace the unique secret and deletion marker in the same mutation,
	// making later ordinary lookup reject the record while its retained ID remains available.
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetKey(tombstoneKey).
		SetDeletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return keycore.ErrAPIKeyNotFound
		}
		return err
	}
	if affected == 0 {
		exists, err := r.client.APIKey.Query().
			Where(apikey.IDEQ(id)).
			Exist(mixins.SkipSoftDelete(ctx))
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		return keycore.ErrAPIKeyNotFound
	}
	return nil
}

// DeleteWithAudit retains the historical method name while tombstoning and
// soft-deleting id. It supplies no separate audit insert.
//
// A context transaction uses its existing client; otherwise the store attempts
// an owned transaction, tolerating ErrTxStarted for a transaction-bound store.
// The owned path rolls back on failure and commits after mutation. The helper
// treats an existing tombstone as repeated successful deletion, while an
// unknown ID reports missing. Transaction-bound callers own final completion
// and post-commit invalidation.
func (r *KeyStore) DeleteWithAudit(ctx context.Context, id int64) error {
	tombstoneKey := fmt.Sprintf("__deleted__%d__%d", id, time.Now().UnixNano())

	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		return r.KeyDeleteWithTombstone(ctx, existingTx.Client(), id, tombstoneKey)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}
	exec := r.client
	if err == nil {
		// Roll back an owned transaction on any later failure so preceding
		// configuration or tombstone work is not committed as a partial operation.
		defer func() { _ = tx.Rollback() }()
		exec = tx.Client()
	}

	if err := r.KeyDeleteWithTombstone(ctx, exec, id, tombstoneKey); err != nil {
		return err
	}

	if tx != nil {
		return tx.Commit()
	}
	return nil
}

// KeyDeleteWithTombstone updates credential, deletion time and modification
// time together through exec. The caller owns any surrounding transaction; an
// already deleted row succeeds when the store's unrestricted existence query
// still finds its ID, while an absent ID reports ErrAPIKeyNotFound.
func (r *KeyStore) KeyDeleteWithTombstone(ctx context.Context, exec *dbent.Client, id int64, tombstoneKey string) error {
	res, err := exec.ExecContext(ctx, `
		UPDATE api_keys
		SET key = $1, deleted_at = NOW(), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`, tombstoneKey, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		// Repeated deletion succeeds if the record still exists as a tombstone; an unknown ID returns NotFound.
		exists, existErr := r.client.APIKey.Query().
			Where(apikey.IDEQ(id)).
			Exist(mixins.SkipSoftDelete(ctx))
		if existErr != nil {
			return existErr
		}
		if exists {
			return nil
		}
		return keycore.ErrAPIKeyNotFound
	}
	return nil
}

// KeyApiKeyListByUserIDQuery builds a creator-scoped nondeleted query excluding
// managed credentials. Text matches name or secret without case, zero group
// selects ordinary Keys without an undeleted group, and nonzero group selects
// ordinary or composite bindings. Personal and team scope narrow membership;
// other scope text leaves both eligible.
//
// @project-doc docs/domains/routing_and_billing.md#api_key_group_filter
func (r *KeyStore) KeyApiKeyListByUserIDQuery(userID int64, filters keycore.APIKeyListFilters) *dbent.APIKeyQuery {
	q := r.KeyActiveQuery().Where(apikey.UserIDEQ(userID))
	// Hide server-managed keys, including creative execution keys, from ordinary key lists.
	q = q.Where(apikey.ManagedByIsNil())

	if filters.Search != "" {
		q = q.Where(apikey.Or(
			apikey.NameContainsFold(filters.Search),
			apikey.KeyContainsFold(filters.Search),
		))
	}
	if filters.Status != "" {
		q = q.Where(apikey.StatusEQ(filters.Status))
	}
	if filters.GroupID != nil {
		if *filters.GroupID == 0 {
			// Soft-deleted groups disappear from loaded views but leave stored IDs,
			// so match the missing live relationship shown as ungrouped in the console.
			q = q.Where(
				apikey.Not(apikey.HasGroupWith(group.DeletedAtIsNil())),
				apikey.IsCompositeEQ(false),
			)
		} else {
			q = q.Where(apikey.Or(
				apikey.GroupIDEQ(*filters.GroupID),
				apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(*filters.GroupID)),
			))
		}
	}
	// Personal and team are the supported scope filters; empty includes both.
	switch filters.Scope {
	case "personal":
		q = q.Where(apikey.TeamIDIsNil())
	case "team":
		q = q.Where(apikey.TeamIDNotNil())
	}

	return q
}

// ListByUserID counts the filtered creator's visible credentials and returns
// their requested page with group and binding views. Stored-column ordering
// uses ID as its tie-breaker; usage ordering delegates to a full-candidate
// sort. Last nonempty usage IP lookup failures reject the page, while a missing
// SQL dependency skips those observations.
func (r *KeyStore) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters keycore.APIKeyListFilters) ([]keycore.APIKey, *pagination.PaginationResult, error) {
	q := r.KeyApiKeyListByUserIDQuery(userID, filters)

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	if strings.EqualFold(strings.TrimSpace(params.SortBy), "usage") {
		return r.KeyListByUserIDWithUsageSort(ctx, q, params, total)
	}

	keysQuery := KeyWithAPIKeyCompositeGroups(q.WithGroup())
	keysQuery = keysQuery.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range KeyApiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *KeyApiKeyEntityToService(keys[i]))
	}
	if err := r.KeyAttachLastUsedIPs(ctx, outKeys); err != nil {
		return nil, nil, err
	}

	return outKeys, pagination.ResultFromTotal(int64(total), params), nil
}

// KeyAttachLastUsedIPs fills each supplied Key with its latest nonempty usage
// address using a batch query. Empty input or absent SQL skips the lookup; a
// successful result mutates the supplied slice and gives each observed address
// its own pointer.
func (r *KeyStore) KeyAttachLastUsedIPs(ctx context.Context, keys []keycore.APIKey) error {
	if len(keys) == 0 || r.sql == nil {
		return nil
	}

	apiKeyIDs := make([]int64, 0, len(keys))
	for i := range keys {
		apiKeyIDs = append(apiKeyIDs, keys[i].ID)
	}

	lastUsedIPs, err := r.KeyLatestUsageLogIPs(ctx, apiKeyIDs)
	if err != nil {
		return err
	}
	for i := range keys {
		if ipAddress, ok := lastUsedIPs[keys[i].ID]; ok {
			keys[i].LastUsedIP = &ipAddress
		}
	}
	return nil
}

// KeyLatestUsageLogIPs delegates the latest nonempty IP batch query using the
// client dialect. Empty IDs or absent SQL return an allocated empty map before
// reading the dialect; query failures are returned for caller presentation
// handling.
func (r *KeyStore) KeyLatestUsageLogIPs(ctx context.Context, apiKeyIDs []int64) (result map[int64]string, err error) {
	// Return before reading the dialect when there is no work or executor.
	if len(apiKeyIDs) == 0 || r.sql == nil {
		return map[int64]string{}, nil
	}
	return usagequery.KeyLatestUsageLogIPs(ctx, r.sql, r.client.Driver().Dialect(), apiKeyIDs)
}

// KeyListByUserIDWithUsageSort loads all filtered credentials before sorting
// and selecting a page. It requires the bound usage-total reader and returns
// lookup failure rather than presenting an arbitrary page.
//
// Missing totals use zero map values. The normalized direction applies to total
// spending and to Key ID ties, giving deterministic selection before
// pagination. Only the selected page receives latest-IP lookup; that failure
// rejects the result. The supplied total comes from the preceding query count
// and can differ from later reads under concurrent database mutation.
func (r *KeyStore) KeyListByUserIDWithUsageSort(ctx context.Context, q *dbent.APIKeyQuery, params pagination.PaginationParams, total int) ([]keycore.APIKey, *pagination.PaginationResult, error) {
	keys, err := KeyWithAPIKeyCompositeGroups(q.WithGroup()).
		Order(dbent.Desc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	keyIDs := make([]int64, 0, len(keys))
	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		key := KeyApiKeyEntityToService(keys[i])
		outKeys = append(outKeys, *key)
		keyIDs = append(keyIDs, key.ID)
	}

	usageTotals, err := r.KeyLoadAPIKeyUsageTotals(ctx, keyIDs)
	if err != nil {
		return nil, nil, err
	}

	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)
	// Spending is read before page selection, with Key IDs breaking equal
	// totals in the same direction so missing or tied observations still select a stable page.
	sort.SliceStable(outKeys, func(i, j int) bool {
		left := usageTotals[outKeys[i].ID]
		right := usageTotals[outKeys[j].ID]
		if left == right {
			if sortOrder == pagination.SortOrderAsc {
				return outKeys[i].ID < outKeys[j].ID
			}
			return outKeys[i].ID > outKeys[j].ID
		}
		if sortOrder == pagination.SortOrderAsc {
			return left < right
		}
		return left > right
	})

	pageKeys := pagination.Slice(outKeys, params)
	if err := r.KeyAttachLastUsedIPs(ctx, pageKeys); err != nil {
		return nil, nil, err
	}
	return pageKeys, pagination.ResultFromTotal(int64(total), params), nil
}

// VerifyOwnership returns nondeleted requested IDs created by userID, including
// hidden managed records. Empty input returns an allocated empty result;
// callers needing presentation visibility apply that independent policy before
// exposing any credential.
func (r *KeyStore) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}

	ids, err := r.client.APIKey.Query().
		Where(apikey.UserIDEQ(userID), apikey.IDIn(apiKeyIDs...), apikey.DeletedAtIsNil()).
		IDs(ctx)
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// CountByUserID returns the creator's count of all nondeleted credential
// records.
func (r *KeyStore) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	count, err := r.KeyActiveQuery().Where(apikey.UserIDEQ(userID)).Count(ctx)
	return int64(count), err
}

// ExistsByKey reports whether a nondeleted record uses the supplied credential
// text.
func (r *KeyStore) ExistsByKey(ctx context.Context, key string) (bool, error) {
	count, err := r.KeyActiveQuery().Where(apikey.KeyEQ(key)).Count(ctx)
	return count > 0, err
}

// ListByGroupID returns paginated nondeleted Keys bound ordinarily or through a
// composite mapping to groupID. It loads user and group relationships, uses
// stored-column ordering, and includes managed records for administrative
// consumers.
func (r *KeyStore) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]keycore.APIKey, *pagination.PaginationResult, error) {
	q := r.KeyActiveQuery().Where(apikey.Or(
		apikey.GroupIDEQ(groupID),
		apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
	))

	total, err := q.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	keysQuery := KeyWithAPIKeyCompositeGroups(q.WithUser().WithGroup())
	keysQuery = keysQuery.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range KeyApiKeyListOrder(params) {
		keysQuery = keysQuery.Order(order)
	}

	keys, err := keysQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *KeyApiKeyEntityToService(keys[i]))
	}

	return outKeys, pagination.ResultFromTotal(int64(total), params), nil
}

// KeyApiKeyListOrder returns supported stored-column ordering with a normalized
// direction and same-direction ID tie-breaker. Unknown fields select ID; usage
// sorting uses a separate full-candidate path in ListByUserID. Other callers
// receive the ID default when they supply usage to this helper.
func KeyApiKeyListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)

	var field string
	switch sortBy {
	case "name":
		field = apikey.FieldName
	case "status":
		field = apikey.FieldStatus
	case "expires_at":
		field = apikey.FieldExpiresAt
	case "last_used_at":
		field = apikey.FieldLastUsedAt
	case "created_at":
		field = apikey.FieldCreatedAt
	case "id":
		field = apikey.FieldID
	default:
		// Use stable ID ordering for unrecognized names so a caller still
		// receives an ordered page using supported stored columns.
		field = apikey.FieldID
	}

	if sortOrder == pagination.SortOrderAsc {
		orders := []func(*entsql.Selector){dbent.Asc(field)}
		if field != apikey.FieldID {
			orders = append(orders, dbent.Asc(apikey.FieldID))
		}
		return orders
	}
	orders := []func(*entsql.Selector){dbent.Desc(field)}
	if field != apikey.FieldID {
		orders = append(orders, dbent.Desc(apikey.FieldID))
	}
	return orders
}

// SearchAPIKeys returns newest nondeleted records matching optional positive
// creator ID and case-insensitive name text. It includes managed Keys and
// passes limit directly to Ent; ordinary group and ordered composite binding
// relationships accompany the result.
func (r *KeyStore) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]keycore.APIKey, error) {
	q := r.KeyActiveQuery()
	if userID > 0 {
		q = q.Where(apikey.UserIDEQ(userID))
	}

	if keyword != "" {
		q = q.Where(apikey.NameContainsFold(keyword))
	}

	keys, err := KeyWithAPIKeyCompositeGroups(q.WithGroup()).
		Limit(limit).
		Order(dbent.Desc(apikey.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	outKeys := make([]keycore.APIKey, 0, len(keys))
	for i := range keys {
		outKeys = append(outKeys, *KeyApiKeyEntityToService(keys[i]))
	}
	return outKeys, nil
}

// ClearGroupIDByGroupID clears ordinary bindings and removes composite mappings
// targeting groupID. It uses the context transaction when supplied, and returns
// ordinary-row plus removed-mapping count.
//
// The two writes need a caller-owned transaction when consistency across both
// steps is required. A mapping deletion failure can follow a completed ordinary
// clear; without that transaction the first write remains. Composite deletion
// covers every saved mapping for the group, including mappings whose owning Key
// is already deleted.
func (r *KeyStore) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	// Clear ordinary references before removing mappings. A later deletion error
	// requires caller rollback when both binding forms must change together.
	n, err := client.APIKey.Update().
		Where(apikey.GroupIDEQ(groupID), apikey.DeletedAtIsNil()).
		ClearGroupID().
		Save(ctx)
	if err != nil {
		return 0, err
	}
	deleted, err := client.APIKeyCompositeGroup.Delete().
		Where(apikeycompositegroup.GroupIDEQ(groupID)).
		Exec(ctx)
	return int64(n + deleted), err
}

// UpdateGroupIDByUserAndGroup moves a creator's nondeleted ordinary and
// composite bindings from oldGroupID to newGroupID. The returned count measures
// ordinary rows plus processed mapping rows.
//
// Each old composite mapping keeps its prefix when moved; if a target mapping
// already exists for that Key, the old mapping is deleted and the target prefix
// retained. The method uses a context transaction when supplied but opens none
// itself, so callers requiring rollback across all writes provide one. Failure
// can follow earlier completed changes, and the transaction owner decides final
// completion.
func (r *KeyStore) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	client := clientFromContext(ctx, r.client)
	// Move ordinary bindings first; later mapping failure can follow this write.
	// A caller-owned transaction provides rollback across both forms of the move.
	n, err := client.APIKey.Update().
		Where(apikey.UserIDEQ(userID), apikey.GroupIDEQ(oldGroupID), apikey.DeletedAtIsNil()).
		SetGroupID(newGroupID).
		Save(ctx)
	if err != nil {
		return 0, err
	}
	// If the target mapping exists, retain its prefix and remove the old mapping to avoid a uniqueness conflict.
	bindings, err := client.APIKeyCompositeGroup.Query().
		Where(apikeycompositegroup.GroupIDEQ(oldGroupID), apikeycompositegroup.HasAPIKeyWith(apikey.UserIDEQ(userID), apikey.DeletedAtIsNil())).
		All(ctx)
	if err != nil {
		return 0, err
	}
	for _, binding := range bindings {
		exists, err := client.APIKeyCompositeGroup.Query().
			Where(apikeycompositegroup.APIKeyIDEQ(binding.APIKeyID), apikeycompositegroup.GroupIDEQ(newGroupID)).
			Exist(ctx)
		if err != nil {
			return 0, err
		}
		if exists {
			if err := client.APIKeyCompositeGroup.DeleteOneID(binding.ID).Exec(ctx); err != nil {
				return 0, err
			}
			continue
		}
		if _, err := client.APIKeyCompositeGroup.UpdateOneID(binding.ID).SetGroupID(newGroupID).Save(ctx); err != nil {
			return 0, err
		}
	}
	return int64(n + len(bindings)), nil
}

// CountByGroupID counts nondeleted Keys with ordinary or composite binding to
// groupID.
func (r *KeyStore) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	count, err := r.KeyActiveQuery().Where(apikey.Or(
		apikey.GroupIDEQ(groupID),
		apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
	)).Count(ctx)
	return int64(count), err
}

// ListKeysByUserID returns every nondeleted credential created by userID for
// invalidation.
func (r *KeyStore) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	keys, err := r.KeyActiveQuery().
		Where(apikey.UserIDEQ(userID)).
		Select(apikey.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// ListKeysByGroupID returns nondeleted secrets ordinarily or compositely bound
// to groupID for invalidation.
func (r *KeyStore) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	keys, err := r.KeyActiveQuery().
		Where(apikey.Or(
			apikey.GroupIDEQ(groupID),
			apikey.HasCompositeGroupsWith(apikeycompositegroup.GroupIDEQ(groupID)),
		)).
		Select(apikey.FieldKey).
		Strings(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// UpdateLastUsed writes supplied usage and modification timestamps on a
// nondeleted credential. A missing row reports ErrAPIKeyNotFound; the service
// owns write throttling, and this storage method accepts the caller's time
// directly.
func (r *KeyStore) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	affected, err := r.client.APIKey.Update().
		Where(apikey.IDEQ(id), apikey.DeletedAtIsNil()).
		SetLastUsedAt(usedAt).
		SetUpdatedAt(usedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return keycore.ErrAPIKeyNotFound
	}
	return nil
}

// KeyApiKeyEntityToService maps selected entity fields and loaded relationships
// into a Key view, retaining nil input. ModelMapping is copied, while many
// scalar pointers and IP lists remain shared with the entity.
//
// Loaded allowed and denied group rows become sorted user ID lists; only a
// loaded disabled-public edge marks GroupRestrictionsLoaded. The user view
// initially acts as both creator and actor until team hydration resolves the
// payer. Ordinary and composite group conversions use their selected entity
// data. Missing or unloaded composite edges leave the default slice, while
// loaded empty edges produce an allocated empty binding list.
func KeyApiKeyEntityToService(m *dbent.APIKey) *keycore.APIKey {
	if m == nil {
		return nil
	}
	out := &keycore.APIKey{
		ID:                           m.ID,
		UserID:                       m.UserID,
		TeamID:                       m.TeamID,
		TeamOwnerDisabled:            m.TeamOwnerDisabled,
		Key:                          m.Key,
		Name:                         m.Name,
		Status:                       m.Status,
		FastModePolicy:               m.FastModePolicy,
		BillingMode:                  m.BillingMode,
		PreferredSubscriptionID:      m.PreferredSubscriptionID,
		ModelMapping:                 keycore.CloneModelMapping(m.ModelMapping),
		IPWhitelist:                  m.IPWhitelist,
		IPBlacklist:                  m.IPBlacklist,
		LastUsedAt:                   m.LastUsedAt,
		CreatedAt:                    m.CreatedAt,
		UpdatedAt:                    m.UpdatedAt,
		GroupID:                      m.GroupID,
		IsComposite:                  m.IsComposite,
		Quota:                        m.Quota,
		QuotaUsed:                    m.QuotaUsed,
		ExpiresAt:                    m.ExpiresAt,
		MaxTokenRateMultiplier:       m.MaxTokenRateMultiplier,
		ConcurrencyLimit:             m.ConcurrencyLimit,
		RPMLimit:                     m.RpmLimit,
		RateLimit5h:                  m.RateLimit5h,
		RateLimit1d:                  m.RateLimit1d,
		RateLimit7d:                  m.RateLimit7d,
		Usage5h:                      m.Usage5h,
		Usage1d:                      m.Usage1d,
		Usage7d:                      m.Usage7d,
		Window5hStart:                m.Window5hStart,
		Window1dStart:                m.Window1dStart,
		Window7dStart:                m.Window7dStart,
		FallbackWhenGroupUnavailable: m.FallbackWhenGroupUnavailable,
		ManagedBy:                    m.ManagedBy,
	}
	if m.Edges.User != nil {
		out.User = userEntityToKeyView(m.Edges.User)
		out.ActorUser = out.User
		if allowed := m.Edges.User.Edges.AllowedGroups; len(allowed) > 0 {
			out.User.AllowedGroups = make([]int64, 0, len(allowed))
			for _, g := range allowed {
				if g != nil {
					out.User.AllowedGroups = append(out.User.AllowedGroups, g.ID)
				}
			}
			slices.Sort(out.User.AllowedGroups)
		}
		// Mark restrictions loaded only after this relationship was selected,
		// including an empty result; unloaded edges leave later permission refresh eligible.
		if disabledPublicRows, err := m.Edges.User.Edges.UserDisabledPublicGroupsOrErr(); err == nil {
			out.User.DisabledPublicGroups = make([]int64, 0, len(disabledPublicRows))
			for _, row := range disabledPublicRows {
				out.User.DisabledPublicGroups = append(out.User.DisabledPublicGroups, row.GroupID)
			}
			slices.Sort(out.User.DisabledPublicGroups)
			out.User.GroupRestrictionsLoaded = true
		}
	}
	if m.Edges.Group != nil {
		out.Group = groupEntityToKeyView(m.Edges.Group)
	}
	if rows, err := m.Edges.CompositeGroupsOrErr(); err == nil {
		out.CompositeGroups = make([]keycore.APIKeyCompositeGroup, 0, len(rows))
		for _, row := range rows {
			if row == nil {
				continue
			}
			binding := keycore.APIKeyCompositeGroup{
				ID:               row.ID,
				APIKeyID:         row.APIKeyID,
				GroupID:          row.GroupID,
				Prefix:           row.Prefix,
				NormalizedPrefix: row.NormalizedPrefix,
				SortOrder:        row.SortOrder,
			}
			if row.Edges.Group != nil {
				binding.Group = groupEntityToKeyView(row.Edges.Group)
			}
			out.CompositeGroups = append(out.CompositeGroups, binding)
		}
	}
	return out
}

// KeyDerefString returns the supplied string value or empty text when nil.
// Callers use it for nullable persisted group labels; no trimming or
// interpretation is performed on a present value.
func KeyDerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// IncrementQuotaUsed delegates a quota delta for a nondeleted record to
// KeyUsageStore. The returned value is its updated usage, and missing records
// report the billing Key-not-found identity; service callers validate any
// spending-specific amount rule.
func (r *KeyStore) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).IncrementQuotaUsed(ctx, id, amount)
}

// IncrementQuotaUsedAndGetState delegates the nondeleted Key quota update and
// conditional exhaustion state read in one SQL operation. It returns current
// quota, usage, secret and state; this path differs from settlement of
// already-admitted soft-deleted Keys.
func (r *KeyStore) IncrementQuotaUsedAndGetState(ctx context.Context, id int64, amount float64) (*keycore.APIKeyQuotaUsageState, error) {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).IncrementQuotaUsedAndGetState(ctx, id, amount)
}

// IncrementRateLimitUsage delegates spending-window increments for a nondeleted
// Key to KeyUsageStore. It passes cost unchanged, and the SQL helper can report
// success when no row matched; settlement uses its own admitted-request write
// path.
func (r *KeyStore) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).IncrementRateLimitUsage(ctx, id, cost)
}

// ResetRateLimitWindows delegates elapsed-window resets to KeyUsageStore using
// transaction time. It targets nondeleted Keys, retains unelapsed counters, and
// leaves missing starts unchanged according to that helper's saved-window reset
// rules.
func (r *KeyStore) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).ResetRateLimitWindows(ctx, id)
}

// GetRateLimitData delegates nondeleted spending counters and saved
// window-start reads to KeyUsageStore. Missing records return the billing
// Key-not-found error; the helper owns result-row scanning and closing before
// this call returns.
func (r *KeyStore) GetRateLimitData(ctx context.Context, id int64) (result *keycore.APIKeyRateLimitData, err error) {
	return billingpostgres.NewKeyUsageStore(r.client, r.sql).GetRateLimitData(ctx, id)
}

// KeyLoadAPIKeyUsageTotals invokes the bound spending reader for supplied Key
// IDs. Missing callback returns a configuration error; its returned map belongs
// to the reader, and sorting treats missing entries as zero.
func (r *KeyStore) KeyLoadAPIKeyUsageTotals(ctx context.Context, ids []int64) (map[int64]float64, error) {
	if r.usageTotals == nil {
		return nil, fmt.Errorf("API key usage totals reader is not configured")
	}
	return r.usageTotals(ctx, ids)
}

// clientFromContext returns the Ent client stored in the context transaction
// when present. Otherwise it returns defaultClient unchanged; callers own
// transaction completion and keep the selected client usable through their
// operation.
func clientFromContext(ctx context.Context, defaultClient *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return defaultClient
}

// translatePersistenceError maps recognized missing rows or unique conflicts to
// the supplied application errors with the persistence cause retained. Nil
// input returns nil; absent mappings and unrelated failures return the original
// error for caller handling.
func translatePersistenceError(err error, notFound, conflict *infraerrors.ApplicationError) error {
	if err == nil {
		return nil
	}

	// Ent and database/sql report missing rows with different error types.
	// Handle both dbent.NotFoundError and sql.ErrNoRows.
	// The application error retains the persistence error as its cause.
	if notFound != nil && (errors.Is(err, sql.ErrNoRows) || dbent.IsNotFound(err)) {
		return notFound.WithCause(err)
	}

	// Use the caller-supplied conflict identity for recognized unique failures,
	// retaining their cause so higher-level error matching and diagnostics remain usable.
	if conflict != nil && isUniqueConstraintViolation(err) {
		return conflict.WithCause(err)
	}

	return err
}

// isUniqueConstraintViolation delegates PostgreSQL unique-constraint error
// classification to the shared storage helper.
func isUniqueConstraintViolation(err error) bool {
	return postgresinfra.IsUniqueConstraintViolation(err)
}

// scanSingleRow delegates first-row scanning and result closure to the shared
// SQL helper. Callers supply destination pointers and arguments; a failed query
// returns directly before row cleanup is registered. After query success,
// deferred Close errors are joined with any scan, no-row or iteration error.
func scanSingleRow(ctx context.Context, q SQLExecutor, query string, args []any, dest ...any) error {
	return postgresinfra.ScanSingleRow(ctx, q, query, args, dest...)
}
