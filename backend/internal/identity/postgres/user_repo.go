package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/lib/pq"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/apikey"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/authidentitychannel"
	dbgroup "github.com/TokenFlux/TokenRouter/ent/group"
	"github.com/TokenFlux/TokenRouter/ent/identityadoptiondecision"
	"github.com/TokenFlux/TokenRouter/ent/predicate"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/ent/userallowedgroup"
	"github.com/TokenFlux/TokenRouter/ent/userdisabledpublicgroup"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/team"
	usagequery "github.com/TokenFlux/TokenRouter/internal/usage/postgres/query"
)

// user_repo.go persists accounts and their group permissions, with guarded
// email changes and filtered account queries.
// Account persistence keeps email uniqueness, provider email identity, and
// per-user group permissions beside the user row. Guarded creation and updates
// join or own Ent transactions so permission and email-identity writes can
// follow the selected account mutation before completion.
//
// Email lookup normalization folds case and spacing, while registration
// normalization and mailbox alias probes have their separate rules and locks.
// Alias probes load a bounded candidate set and recheck it in Go, preferring
// another owner over the caller. List filtering counts the same selected
// account query before ordering and paging, then separately loads grants and
// optional subscriptions. Raw SQL and Ent client selection differ by method;
// callers supply matching storage dependencies and keep their pools available
// through those operations.

// Normalized email expressions match registration policy and the published
// index, while lock namespace and alias candidate bounds govern guarded
// account email operations.
const (
	// Keep these expressions consistent with identitycore.NormalizeRegistrationEmailAddress
	// and the index in 220_users_registration_email_normalized_index_notx.sql.
	IdentityNormalizedUserEmailValueSQL     = `lower(btrim(email))`
	IdentityNormalizedUserEmailLocalSQL     = `split_part(` + IdentityNormalizedUserEmailValueSQL + `, '@', 1)`
	IdentityNormalizedUserEmailDomainSQL    = `rtrim(split_part(` + IdentityNormalizedUserEmailValueSQL + `, '@', 2), '.')`
	IdentityNormalizedUserEmailBaseLocalSQL = `CASE WHEN strpos(` + IdentityNormalizedUserEmailLocalSQL + `, '+') > 1 ` +
		`THEN left(` + IdentityNormalizedUserEmailLocalSQL + `, strpos(` + IdentityNormalizedUserEmailLocalSQL + `, '+') - 1) ` +
		`ELSE ` + IdentityNormalizedUserEmailLocalSQL + ` END`
	IdentityNormalizedUserEmailSQL = `CASE WHEN ` + IdentityNormalizedUserEmailDomainSQL + ` IN ('gmail.com', 'googlemail.com') ` +
		`THEN coalesce(nullif(replace(` + IdentityNormalizedUserEmailBaseLocalSQL + `, '.', ''), ''), ` + IdentityNormalizedUserEmailBaseLocalSQL + `) || '@gmail.com' ` +
		`ELSE ` + IdentityNormalizedUserEmailBaseLocalSQL + ` || '@' || ` + IdentityNormalizedUserEmailDomainSQL + ` END`

	IdentityRegistrationEmailLockNamespace = 148623451

	// IdentityEmailAliasCandidateLimit bounds alias candidates loaded into memory.
	// Candidates are rechecked using the Go email normalization rules.
	IdentityEmailAliasCandidateLimit = 50
)

// LIKE escaping values quote literal mailbox and domain text before wildcard
// probes, while their callers add the intended substring or plus-suffix
// pattern components.
var (
	// IdentityLikeWildcardEscaper quotes %, _, and backslashes in email local parts for LIKE.
	IdentityLikeWildcardEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

	likePatternReplacer = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
)

// UserStore persists accounts, email identity, and group permissions with Ent
// and raw SQL.
type UserStore struct {
	// client supplies entity operations, and sql supplies raw activity,
	// attribute, and email queries. Context-aware methods can select a
	// transaction client; other methods use these retained defaults, so callers
	// provide matching database dependencies.
	client *dbent.Client // default account entity client
	sql    SQLExecutor   // default raw account query executor
}

// NewUserStore retains the supplied Ent client and SQL pool through the
// account store constructor. Callers provide matching database dependencies
// and retain ownership of their lifetime while account operations run.
func NewUserStore(client *dbent.Client, sqlDB *sql.DB) *UserStore {
	return NewUserStoreWithSQL(client, sqlDB)
}

// NewUserStoreWithSQL retains the Ent client and caller-supplied raw SQL
// executor. Query paths requiring raw SQL need a usable executor, while
// context-aware operations may select their injected transaction client.
func NewUserStoreWithSQL(client *dbent.Client, sqlq SQLExecutor) *UserStore {
	return &UserStore{client: client, sql: sqlq}
}

// Create delegates guarded account creation with exact normalized-email
// uniqueness and account permissions. Nil user input succeeds; the helper owns
// or joins the transaction and refreshes stored account metadata after its
// writes succeed.
func (r *UserStore) Create(ctx context.Context, userIn *identitycore.User) error {
	return r.IdentityCreateWithNormalizationGuard(ctx, userIn, "", "")
}

// CreateWithNormalizedEmailGuard delegates account creation with the supplied
// registration-normalized email guard. The shared helper also checks trimmed
// case-folded email uniqueness and keeps permission and email-identity writes
// in its selected persistence scope.
func (r *UserStore) CreateWithNormalizedEmailGuard(ctx context.Context, userIn *identitycore.User, normalizedEmail string) error {
	return r.IdentityCreateWithNormalizationGuard(ctx, userIn, normalizedEmail, "")
}

// CountUsersByEmailDomain delegates current account counting for the
// normalized registrable domain and subdomains. It selects the context Ent
// client when present, with blank domain or missing client producing zero.
func (r *UserStore) CountUsersByEmailDomain(ctx context.Context, domain string) (int, error) {
	return IdentityCountUsersByEmailDomainWithClient(ctx, clientFromContext(ctx, r.client), domain)
}

// CreateWithRegistrationEmailGuards delegates account creation with email
// uniqueness and normalized registrable-domain quota. The helper serializes
// destination email and domain keys before rechecking current account count
// and storing permissions.
func (r *UserStore) CreateWithRegistrationEmailGuards(ctx context.Context, userIn *identitycore.User, normalizedEmail, domain string) error {
	return r.IdentityCreateWithNormalizationGuard(ctx, userIn, normalizedEmail, IdentityNormalizeEmailDomain(domain))
}

// IdentityCreateWithNormalizationGuard stores an account, group permissions,
// and its email identity in the selected transaction. Nil user input is a
// successful no-op.
//
// An injected transaction is preferred; otherwise a new transaction is owned,
// or an already-bound configured client retains its external owner. Sorted
// scoped email/domain locks precede current-row uniqueness and quota checks.
// Account creation, permission synchronization, and email identity follow
// those checks before an owned commit. The supplied user receives stored
// identifiers and timestamps afterward; external transaction callers still
// complete their persistence scope.
func (r *UserStore) IdentityCreateWithNormalizationGuard(ctx context.Context, userIn *identitycore.User, normalizedEmail, domainLimit string) error {
	if userIn == nil {
		return nil
	}

	// Prefer the injected transaction so account, permission, and email-identity
	// writes share the caller's completion with invitation consumption. Opening
	// another transaction here would give account creation a separate commit.
	var txClient *dbent.Client
	txCtx := ctx
	var ownedTx *dbent.Tx
	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		txClient = existingTx.Client()
	} else {
		tx, err := r.client.Tx(ctx)
		switch {
		case err == nil:
			ownedTx = tx
			// Rollback the owned account, grant, and email-identity writes when a later
			// creation stage fails. An injected or already-bound transaction instead
			// keeps that recovery with its external owner.
			defer func() { _ = ownedTx.Rollback() }()
			txClient = tx.Client()
			txCtx = dbent.NewTxContext(ctx, tx)
		case errors.Is(err, dbent.ErrTxStarted):
			// The client may already be bound to a transaction, as in integration fixtures.
			// Its owner is responsible for committing or rolling back.
			txClient = r.client
		default:
			return err
		}
	}

	lockKeys := []string{IdentityNormalizedEmailUniquenessLockKey(userIn.Email)}
	if domainLimit != "" {
		lockKeys = append(lockKeys, IdentityRegistrationEmailDomainLockKey(domainLimit))
	}
	releaseEmailLock, err := IdentityLockRepositoryScopedKeys(
		txCtx,
		txClient,
		IdentityTxAwareSQLExecutor(txCtx, r.sql, r.client),
		lockKeys...,
	)
	if err != nil {
		return err
	}
	defer releaseEmailLock()

	if domainLimit != "" {
		count, err := IdentityCountUsersByEmailDomainWithClient(txCtx, txClient, domainLimit)
		if err != nil {
			return err
		}
		if count > 0 {
			return identitycore.ErrEmailDomainRegistrationLimit
		}
	}

	if err := IdentityEnsureNormalizedEmailAvailableWithClient(txCtx, txClient, 0, userIn.Email); err != nil {
		return err
	}

	if normalizedEmail != "" {
		if err := r.LockRegistrationEmail(txCtx, normalizedEmail); err != nil {
			return err
		}
		exists, err := r.IdentityExistsByNormalizedEmail(txCtx, normalizedEmail, 0)
		if err != nil {
			return err
		}
		if exists {
			return identitycore.ErrEmailExists
		}
	}

	created, err := r.IdentityCreateWithClient(txCtx, txClient, userIn)
	if err != nil {
		return translatePersistenceError(err, nil, identitycore.ErrEmailExists)
	}

	if err := r.IdentitySyncUserAllowedGroupsWithClient(txCtx, txClient, created.ID, userIn.AllowedGroups); err != nil {
		return err
	}
	if err := r.IdentitySyncUserDisabledPublicGroupsWithClient(txCtx, txClient, created.ID, userIn.DisabledPublicGroups); err != nil {
		return err
	}
	if err := IdentityEnsureEmailAuthIdentityWithClient(txCtx, txClient, created.ID, created.Email, "user_repo_create"); err != nil {
		return err
	}

	if ownedTx != nil {
		// Commit guarded account creation with its grant, denial, and email-identity
		// rows before publishing stored identifiers and timestamps onto the caller
		// account value.
		if err := ownedTx.Commit(); err != nil {
			return err
		}
	}

	IdentityApplyUserEntityToService(userIn, created)
	return nil
}

// GetByID loads the account and separately reads sorted grant and denial lists
// through the configured client. Missing accounts map to ErrUserNotFound;
// successful restriction reads mark GroupRestrictionsLoaded even when their
// maps contain no entries.
func (r *UserStore) GetByID(ctx context.Context, id int64) (*identitycore.User, error) {
	m, err := r.client.User.Query().Where(dbuser.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}

	out := UserFromEntity(m)
	groups, err := r.IdentityLoadAllowedGroups(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if v, ok := groups[id]; ok {
		out.AllowedGroups = v
	}
	disabledPublicGroups, err := r.IdentityLoadDisabledPublicGroups(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if v, ok := disabledPublicGroups[id]; ok {
		out.DisabledPublicGroups = v
	}
	out.GroupRestrictionsLoaded = true
	return out, nil
}

// GetByIDIncludeDeleted skips account soft-delete filtering before loading the
// row and its group restrictions. Missing accounts map to ErrUserNotFound, and
// successful restriction reads set the complete-restrictions flag on the
// returned value.
func (r *UserStore) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identitycore.User, error) {
	ctx = mixins.SkipSoftDelete(ctx)
	m, err := r.client.User.Query().Where(dbuser.IDEQ(id)).Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	out := UserFromEntity(m)
	groups, err := r.IdentityLoadAllowedGroups(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if v, ok := groups[id]; ok {
		out.AllowedGroups = v
	}
	disabledPublicGroups, err := r.IdentityLoadDisabledPublicGroups(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	if v, ok := disabledPublicGroups[id]; ok {
		out.DisabledPublicGroups = v
	}
	out.GroupRestrictionsLoaded = true
	return out, nil
}

// GetByEmail selects one trimmed, case-folded email match through the
// configured client and loads group restrictions. No match returns
// ErrUserNotFound, multiple matches return an error, and the stored email text
// remains unchanged.
func (r *UserStore) GetByEmail(ctx context.Context, email string) (*identitycore.User, error) {
	matches, err := r.client.User.Query().
		Where(IdentityUserEmailLookupPredicate(email)).
		Order(dbent.Asc(dbuser.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		return nil, identitycore.ErrUserNotFound
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("normalized email lookup matched multiple users for %q", strings.TrimSpace(email))
	}
	m := matches[0]

	out := UserFromEntity(m)
	groups, err := r.IdentityLoadAllowedGroups(ctx, []int64{m.ID})
	if err != nil {
		return nil, err
	}
	if v, ok := groups[m.ID]; ok {
		out.AllowedGroups = v
	}
	disabledPublicGroups, err := r.IdentityLoadDisabledPublicGroups(ctx, []int64{m.ID})
	if err != nil {
		return nil, err
	}
	if v, ok := disabledPublicGroups[m.ID]; ok {
		out.DisabledPublicGroups = v
	}
	out.GroupRestrictionsLoaded = true
	return out, nil
}

// Update delegates flagged account edits and related group/email identity
// writes to the shared guarded updater. Nil user or empty flags succeeds
// without persistence; the helper selects transaction completion
// responsibility.
func (r *UserStore) Update(ctx context.Context, userIn *identitycore.User, fields identitycore.UserUpdateFields) error {
	return r.IdentityUpdateWithNormalizationGuard(ctx, userIn, "", fields)
}

// UpdateWithNormalizedEmailGuard delegates flagged account changes with
// supplied registration-normalized email validation. Destination email writes
// receive scoped uniqueness checks, while the shared helper owns or joins the
// related permission and identity updates.
func (r *UserStore) UpdateWithNormalizedEmailGuard(ctx context.Context, userIn *identitycore.User, normalizedEmail string, fields identitycore.UserUpdateFields) error {
	return r.IdentityUpdateWithNormalizationGuard(ctx, userIn, normalizedEmail, fields)
}

// IdentityUpdateWithNormalizationGuard writes flagged account fields, selected
// group relations, and stored email identity together. Nil user or empty flags
// is a no-op.
//
// The configured client is asked to start a transaction first. A regular
// client therefore opens an owned transaction even when context carries one;
// only ErrTxStarted selects an injected or retained transaction client. Email
// edits lock and recheck the destination before loading the preceding stored
// address. Permission flags choose relation synchronization, and the email
// identity is ensured before old-subject removal. An owned transaction commits
// before UpdatedAt is refreshed; external callers retain rollback
// responsibility.
func (r *UserStore) IdentityUpdateWithNormalizationGuard(ctx context.Context, userIn *identitycore.User, normalizedEmail string, fields identitycore.UserUpdateFields) error {
	if userIn == nil || fields.IsEmpty() {
		return nil
	}

	// Start an owned transaction for selected account fields, permission
	// relations, and email identity writes. An already-active client follows the
	// external-transaction path below instead of creating another owner.
	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}

	var txClient *dbent.Client
	txCtx := ctx
	if err == nil {
		// Rollback selected account fields and preceding relation writes when a
		// later guarded update fails. Cleanup errors are discarded; an external
		// transaction retains its own completion responsibility.
		defer func() { _ = tx.Rollback() }()
		txClient = tx.Client()
		txCtx = dbent.NewTxContext(ctx, tx)
	} else {
		// An already-active client uses the injected transaction when available,
		// otherwise its retained client. Those paths leave commit and rollback with
		// the caller spanning the related account writes.
		if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
			txClient = existingTx.Client()
		} else {
			txClient = r.client
		}
	}

	// Lock the requested destination before uniqueness checks and account
	// mutation. Other email writers using the same scoped key cannot interleave
	// that check with their own destination update.
	if fields.Email {
		releaseEmailLock, err := IdentityLockRepositoryScopedKeys(
			txCtx,
			txClient,
			IdentityTxAwareSQLExecutor(txCtx, r.sql, r.client),
			IdentityNormalizedEmailUniquenessLockKey(userIn.Email),
		)
		if err != nil {
			return err
		}
		defer releaseEmailLock()

		if err := IdentityEnsureNormalizedEmailAvailableWithClient(txCtx, txClient, userIn.ID, userIn.Email); err != nil {
			return err
		}
		if normalizedEmail != "" {
			if err := r.LockRegistrationEmail(txCtx, normalizedEmail); err != nil {
				return err
			}
			exists, err := r.IdentityExistsByNormalizedEmail(txCtx, normalizedEmail, userIn.ID)
			if err != nil {
				return err
			}
			if exists {
				return identitycore.ErrEmailExists
			}
		}
	}

	existing, err := clientFromContext(txCtx, txClient).User.Get(txCtx, userIn.ID)
	if err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	oldEmail := existing.Email

	updated, err := r.IdentityUpdateWithClient(txCtx, txClient, userIn, fields)
	if err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, identitycore.ErrEmailExists)
	}

	if fields.AllowedGroups {
		if err := r.IdentitySyncUserAllowedGroupsWithClient(txCtx, txClient, updated.ID, userIn.AllowedGroups); err != nil {
			return err
		}
	}
	if fields.DisabledPublicGroups {
		if err := r.IdentitySyncUserDisabledPublicGroupsWithClient(txCtx, txClient, updated.ID, userIn.DisabledPublicGroups); err != nil {
			return err
		}
	}
	// Ensure identity from the stored updated email before removing the preceding
	// subject, keeping the selected account credential and sign-in identity in
	// the same persistence scope.
	if err := IdentityReplaceEmailAuthIdentityWithClient(txCtx, txClient, updated.ID, oldEmail, updated.Email, "user_repo_update"); err != nil {
		return err
	}

	if tx != nil {
		// Commit flagged account fields, selected permission changes, and
		// email-identity replacement before refreshing UpdatedAt, keeping the
		// response observation after the owned database completion.
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	userIn.UpdatedAt = updated.UpdatedAt
	return nil
}

// IdentityEnsureEmailAuthIdentityWithClient attempts a real-email identity
// insert and checks the reloaded owner when present. An injected transaction
// client takes precedence over the supplied client, while missing storage,
// nonpositive account ID, or a blank or synthetic normalized subject skips
// work.
//
// The insert records current UTC time in VerifiedAt and uses do-nothing
// conflict handling for the email tuple. Recognized no-row insert results
// continue to readback. Another owner is rejected, but an absent readback
// succeeds, so callers cannot infer row existence solely from nil error or use
// this helper as email verification proof.
func IdentityEnsureEmailAuthIdentityWithClient(ctx context.Context, client *dbent.Client, userID int64, email string, source string) error {
	client = clientFromContext(ctx, client)
	if client == nil || userID <= 0 {
		return nil
	}

	subject := IdentityNormalizeEmailAuthIdentitySubject(email)
	if subject == "" {
		return nil
	}

	if err := client.AuthIdentity.Create().
		SetUserID(userID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject(subject).
		SetVerifiedAt(time.Now().UTC()).
		SetMetadata(map[string]any{"source": source}).
		OnConflictColumns(
			authidentity.FieldProviderType,
			authidentity.FieldProviderKey,
			authidentity.FieldProviderSubject,
		).
		DoNothing().
		Exec(ctx); err != nil {
		// Do-nothing insertion can report no returned SQL row. Continue to ownership
		// readback for that result, allowing a preexisting row to determine whether
		// this email binding conflicts.
		if !isSQLNoRowsError(err) {
			return err
		}
	}

	identity, err := client.AuthIdentity.Query().
		Where(
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(subject),
		).
		Only(ctx)
	if err != nil {
		// Readback absence is accepted after this conflict-tolerant insert. Callers
		// therefore cannot infer that an email identity row exists solely from this
		// helper returning no error.
		if dbent.IsNotFound(err) {
			return nil
		}
		return err
	}
	if identity.UserID != userID {
		return ErrAuthIdentityOwnershipConflict
	}
	return nil
}

// IdentityReplaceEmailAuthIdentityWithClient attempts the new owned email
// subject before removing the preceding subject. Callers supply the
// transaction spanning account credentials and identity replacement; the
// ensure helper prefers a transaction injected into context.
//
// Synthetic or blank new email can skip insertion, and missing readback can
// also succeed. A different nonempty old subject is then deleted for this
// account, even after that no-op result. Equal or empty old subjects skip
// deletion. A failed later delete returns before completion and needs rollback
// by the enclosing account operation.
func IdentityReplaceEmailAuthIdentityWithClient(ctx context.Context, client *dbent.Client, userID int64, oldEmail, newEmail string, source string) error {
	newSubject := IdentityNormalizeEmailAuthIdentitySubject(newEmail)
	if err := IdentityEnsureEmailAuthIdentityWithClient(ctx, client, userID, newEmail, source); err != nil {
		return err
	}

	oldSubject := IdentityNormalizeEmailAuthIdentitySubject(oldEmail)
	if oldSubject == "" || oldSubject == newSubject {
		return nil
	}

	_, err := clientFromContext(ctx, client).AuthIdentity.Delete().
		Where(
			authidentity.UserIDEQ(userID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ(oldSubject),
		).
		Exec(ctx)
	return err
}

// IdentityNormalizeEmailAuthIdentitySubject returns trimmed lowercase
// real-email text for account identity. Blank input and the four configured
// provider synthetic-email suffixes return empty, so those addresses do not
// create local email identities.
func IdentityNormalizeEmailAuthIdentitySubject(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return ""
	}
	if strings.HasSuffix(normalized, identitycore.LinuxDoConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, identitycore.OIDCConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, identitycore.WeChatConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, identitycore.DingTalkConnectSyntheticEmailDomain) {
		return ""
	}
	return normalized
}

// Delete removes provider identity references and deletes the account using
// an injected, owned, or already-bound Ent transaction. Ordinary context
// soft-deletes the user, while SkipSoftDelete selects physical removal.
//
// Identity deletion clears adoption references and channel rows before
// removing the user identity and account. Any stage failure returns before an
// owned commit, with rollback attempted to restore earlier writes. Missing
// account deletion maps to ErrUserNotFound, and the PostgreSQL team-owner
// trigger error becomes the team transfer requirement. The external
// transaction path leaves completion to its caller.
func (r *UserStore) Delete(ctx context.Context, id int64) error {
	// Reuse an injected transaction so administrator key deletion and
	// account-reference removal can share one commit. This path leaves earlier
	// writes and any rollback responsibility with that transaction owner.
	if existingTx := dbent.TxFromContext(ctx); existingTx != nil {
		return r.IdentityDeleteUser(ctx, existingTx.Client(), id)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	exec := r.client
	if err == nil {
		// Rollback earlier adoption-reference and channel/identity removals if the
		// account delete fails. Cleanup errors are discarded while the account or
		// team-owner failure remains the operation result.
		defer func() { _ = tx.Rollback() }()
		exec = tx.Client()
	}

	if err := r.IdentityDeleteUser(ctx, exec, id); err != nil {
		return err
	}

	if tx != nil {
		// Commit account deletion after its provider references are removed, so an
		// owned removal finishes those linked writes before reporting successful
		// account deletion.
		if err := tx.Commit(); err != nil {
			return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
		}
	}
	return nil
}

// IdentityDeleteUser clears provider adoption references and channels before
// deleting identities and the account through Ent. Ordinary context
// soft-deletes the user; SkipSoftDelete removes it physically. The supplied
// client belongs to the caller persistence scope.
//
// Identity IDs are loaded before those reference writes, and each failure
// stops later deletion. A later account failure can follow completed reference
// deletion, so transaction owners roll back the combined sequence. Zero
// affected accounts return ErrUserNotFound. The recognized PostgreSQL
// team-owner trigger refusal is returned as the ownership-transfer domain
// error instead of a raw storage failure.
func (r *UserStore) IdentityDeleteUser(ctx context.Context, exec *dbent.Client, id int64) error {
	identityIDs, err := exec.AuthIdentity.Query().
		Where(authidentity.UserIDEQ(id)).
		IDs(ctx)
	if err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	if len(identityIDs) > 0 {
		// Remove decision references and channel rows before their identities.
		// The account delete follows in the same caller scope, so its failure needs
		// rollback to restore these earlier relationship removals.
		if _, err := exec.IdentityAdoptionDecision.Update().
			Where(identityadoptiondecision.IdentityIDIn(identityIDs...)).
			ClearIdentityID().
			Save(ctx); err != nil {
			return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
		}
		if _, err := exec.AuthIdentityChannel.Delete().
			Where(authidentitychannel.IdentityIDIn(identityIDs...)).
			Exec(ctx); err != nil {
			return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
		}
		if _, err := exec.AuthIdentity.Delete().
			Where(authidentity.UserIDEQ(id)).
			Exec(ctx); err != nil {
			return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
		}
	}

	affected, err := exec.User.Delete().Where(dbuser.IDEQ(id)).Exec(ctx)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && strings.Contains(pqErr.Message, "TEAM_OWNER_TRANSFER_REQUIRED") {
			return team.ErrTeamOwnerTransferRequired
		}
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	if affected == 0 {
		return identitycore.ErrUserNotFound
	}
	return nil
}

// List delegates account paging with an empty filter set to ListWithFilters.
// The shared query uses its default related-data selection and ordering, while
// errors remain visible to account readers.
func (r *UserStore) List(ctx context.Context, params pagination.PaginationParams) ([]identitycore.User, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, identitycore.UserListFilters{})
}

// ListWithFilters counts and pages accounts using the same selected filters
// before loading requested related data. IncludeDeleted affects account
// queries, while the remaining relation queries use the ordinary supplied
// context.
//
// Attribute matching resolves an intersection of account IDs before count and
// page; an empty intersection returns an empty result immediately. Ordered
// paging selects the requested account rows, then active subscriptions with
// plans load unless explicitly disabled. Grant and denial reads populate
// complete restriction state. These are separate observations through retained
// dependencies, so this method does not create an encompassing read
// transaction.
func (r *UserStore) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters identitycore.UserListFilters) ([]identitycore.User, *pagination.PaginationResult, error) {
	// Use include-deleted context for user-table observations only. Subscription
	// and group queries retain their normal context, so those related collections
	// continue to omit rows hidden by soft-delete filtering.
	userCtx := ctx
	if filters.IncludeDeleted {
		userCtx = mixins.SkipSoftDelete(ctx)
	}

	q := r.client.User.Query()

	if filters.Status != "" {
		q = q.Where(dbuser.StatusEQ(filters.Status))
	}
	if filters.Role != "" {
		q = q.Where(dbuser.RoleEQ(filters.Role))
	}
	if filters.Search != "" {
		q = q.Where(identityUserSearch(filters.Search))
	}

	if filters.GroupName != "" {
		q = q.Where(dbuser.HasAllowedGroupsWith(
			dbgroup.NameContainsFold(filters.GroupName),
		))
	}

	if filters.APIKeyGroupID > 0 {
		// Match users with an undeleted API key in the requested group.
		// The HasAPIKeysWith subquery needs its own DeletedAtIsNil predicate.
		q = q.Where(dbuser.HasAPIKeysWith(
			apikey.GroupIDEQ(filters.APIKeyGroupID),
			apikey.DeletedAtIsNil(),
		))
	}

	// Resolve the complete attribute intersection before count and page
	// selection. Filtering after pagination would omit qualifying accounts and
	// give the panel a total for a different query.
	var allowedUserIDs []int64
	if len(filters.Attributes) > 0 {
		var attrErr error
		allowedUserIDs, attrErr = r.IdentityFilterUsersByAttributes(ctx, filters.Attributes)
		if attrErr != nil {
			return nil, nil, attrErr
		}
		if len(allowedUserIDs) == 0 {
			return []identitycore.User{}, pagination.ResultFromTotal(0, params), nil
		}
		q = q.Where(dbuser.IDIn(allowedUserIDs...))
	}

	total, err := q.Clone().Count(userCtx)
	if err != nil {
		return nil, nil, err
	}

	usersQuery := q.
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range IdentityUserListOrder(params) {
		usersQuery = usersQuery.Order(order)
	}

	users, err := usersQuery.All(userCtx)
	if err != nil {
		return nil, nil, err
	}

	outUsers := make([]identitycore.User, 0, len(users))
	if len(users) == 0 {
		return outUsers, pagination.ResultFromTotal(int64(total), params), nil
	}

	userIDs := make([]int64, 0, len(users))
	userMap := make(map[int64]*identitycore.User, len(users))
	for i := range users {
		userIDs = append(userIDs, users[i].ID)
		u := UserFromEntity(users[i])
		outUsers = append(outUsers, *u)
		userMap[u.ID] = &outUsers[len(outUsers)-1]
	}

	shouldLoadSubscriptions := filters.IncludeSubscriptions == nil || *filters.IncludeSubscriptions
	if shouldLoadSubscriptions {
		// Load active subscriptions and their plans in a batch.
		subs, err := r.client.UserSubscription.Query().
			Where(
				usersubscription.UserIDIn(userIDs...),
				usersubscription.StatusEQ(billing.SubscriptionStatusActive),
			).
			WithPlan().
			All(ctx)
		if err != nil {
			return nil, nil, err
		}

		for i := range subs {
			if u, ok := userMap[subs[i].UserID]; ok {
				u.Subscriptions = append(u.Subscriptions, *billingpostgres.SubscriptionFromEntity(subs[i]))
			}
		}
	}

	allowedGroupsByUser, err := r.IdentityLoadAllowedGroups(ctx, userIDs)
	if err != nil {
		return nil, nil, err
	}
	for id, u := range userMap {
		if groups, ok := allowedGroupsByUser[id]; ok {
			u.AllowedGroups = groups
		}
	}
	disabledPublicGroupsByUser, err := r.IdentityLoadDisabledPublicGroups(ctx, userIDs)
	if err != nil {
		return nil, nil, err
	}
	for id, u := range userMap {
		if groups, ok := disabledPublicGroupsByUser[id]; ok {
			u.DisabledPublicGroups = groups
		}
		u.GroupRestrictionsLoaded = true
	}

	return outUsers, pagination.ResultFromTotal(int64(total), params), nil
}

// identityUserSearch builds a UNION of case-insensitive email, username, note,
// and API Key candidate account IDs. The outer user query controls account
// soft deletion; key candidate search has no independent deleted-key
// predicate.
func identityUserSearch(search string) predicate.User {
	return func(s *entsql.Selector) {
		builder := entsql.Dialect(s.Dialect())
		users := builder.Table(dbuser.Table).As("search_users")
		keys := builder.Table(apikey.Table).As("search_keys")
		matches := builder.Select(users.C(dbuser.FieldID)).From(users).
			Where(entsql.ContainsFold(users.C(dbuser.FieldEmail), search))
		for _, field := range []string{dbuser.FieldUsername, dbuser.FieldNotes} {
			matches.Union(builder.Select(users.C(dbuser.FieldID)).From(users).
				Where(entsql.ContainsFold(users.C(field), search)))
		}
		keyMatches := builder.Select(keys.C(apikey.FieldUserID)).From(keys).
			Where(entsql.ContainsFold(keys.C(apikey.FieldKey), search))
		s.Where(entsql.In(s.C(dbuser.FieldID), matches.Union(keyMatches)))
	}
}

// IdentityUserListOrder returns the selected field order with an ID tie break
// in the same direction. Unknown fields select ID; last_active_at and personal
// recharge rates place nulls last in either direction. Last_used_at delegates
// its activity-specific ordering.
func IdentityUserListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)

	if sortBy == "last_used_at" {
		return IdentityUserLastUsedAtOrder(sortOrder)
	}

	var field string
	defaultField := true
	nullsLastField := false
	switch sortBy {
	case "email":
		field = dbuser.FieldEmail
		defaultField = false
	case "username":
		field = dbuser.FieldUsername
		defaultField = false
	case "role":
		field = dbuser.FieldRole
		defaultField = false
	case "balance_recharge_multiplier":
		field = dbuser.FieldBalanceRechargeMultiplier
		defaultField = false
		nullsLastField = true
	case "balance":
		field = dbuser.FieldBalance
		defaultField = false
	case "concurrency":
		field = dbuser.FieldConcurrency
		defaultField = false
	case "status":
		field = dbuser.FieldStatus
		defaultField = false
	case "created_at":
		field = dbuser.FieldCreatedAt
		defaultField = false
	case "last_active_at":
		field = dbuser.FieldLastActiveAt
		defaultField = false
		nullsLastField = true
	default:
		field = dbuser.FieldID
	}

	if sortOrder == pagination.SortOrderAsc {
		if defaultField && field == dbuser.FieldID {
			return []func(*entsql.Selector){dbent.Asc(dbuser.FieldID)}
		}
		if nullsLastField {
			return []func(*entsql.Selector){
				entsql.OrderByField(field, entsql.OrderNullsLast()).ToFunc(),
				dbent.Asc(dbuser.FieldID),
			}
		}
		return []func(*entsql.Selector){dbent.Asc(field), dbent.Asc(dbuser.FieldID)}
	}
	if defaultField && field == dbuser.FieldID {
		return []func(*entsql.Selector){dbent.Desc(dbuser.FieldID)}
	}
	if nullsLastField {
		return []func(*entsql.Selector){
			entsql.OrderByField(field, entsql.OrderDesc(), entsql.OrderNullsLast()).ToFunc(),
			dbent.Desc(dbuser.FieldID),
		}
	}
	return []func(*entsql.Selector){dbent.Desc(field), dbent.Desc(dbuser.FieldID)}
}

// GetLatestUsedAtByUserIDs delegates activity-summary lookup through the
// retained raw executor. Empty input returns an allocated empty map, while
// activity-query errors remain errors for administrator account readers.
func (r *UserStore) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	if len(userIDs) == 0 {
		return map[int64]*time.Time{}, nil
	}
	return usagequery.GetLatestUsedAtByUserIDs(ctx, r.sql, userIDs)
}

// GetLatestUsedAtByUserID returns the selected account latest usage timestamp
// from the batch summary reader. Missing map entries yield nil; lookup
// failures stop the result before a timestamp is selected.
func (r *UserStore) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	latestByUserID, err := r.GetLatestUsedAtByUserIDs(ctx, []int64{userID})
	if err != nil {
		return nil, err
	}
	return latestByUserID[userID], nil
}

// IdentityUserLastUsedAtOrder delegates activity timestamp and account ID
// ordering to usage queries. The supplied sort direction selects their null
// placement and tie-break behavior before account paging.
func IdentityUserLastUsedAtOrder(sortOrder string) []func(*entsql.Selector) {
	return usagequery.IdentityUserLastUsedAtOrder(sortOrder)
}

// IdentityFilterUsersByAttributes returns account IDs matching every supplied
// attribute substring filter. Empty filters return nil with no error; the
// retained raw executor is required for nonempty selections.
//
// Each attribute contributes an ID/value alternative, and grouped account rows
// must match the distinct attribute count before they qualify. Input text
// enters ILIKE patterns with wildcard behavior retained. SQL rows close on all
// exits, with close errors discarded while scan or iteration failure
// determines the returned result.
func (r *UserStore) IdentityFilterUsersByAttributes(ctx context.Context, attrs map[int64]string) ([]int64, error) {
	if len(attrs) == 0 {
		return nil, nil
	}

	if r.sql == nil {
		return nil, fmt.Errorf("sql executor is not configured")
	}

	clauses := make([]string, 0, len(attrs))
	args := make([]any, 0, len(attrs)*2+1)
	argIndex := 1
	for attrID, value := range attrs {
		clauses = append(clauses, fmt.Sprintf("(attribute_id = $%d AND value ILIKE $%d)", argIndex, argIndex+1))
		args = append(args, attrID, "%"+value+"%")
		argIndex += 2
	}

	query := fmt.Sprintf(
		`SELECT user_id
		 FROM user_attribute_values
		 WHERE %s
		 GROUP BY user_id
		 HAVING COUNT(DISTINCT attribute_id) = $%d`,
		strings.Join(clauses, " OR "),
		argIndex,
	)
	args = append(args, len(attrs))

	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	// Close attribute query rows on early or completed exits. Cleanup failures
	// are discarded while scan and iterator failures remain the result used by
	// the account list caller.
	defer func() { _ = rows.Close() }()

	result := make([]int64, 0)
	for rows.Next() {
		var userID int64
		if scanErr := rows.Scan(&userID); scanErr != nil {
			return nil, scanErr
		}
		result = append(result, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// UpdateBalance delegates a balance delta and positive recharge-base increment
// to billing storage. The injected transaction is selected when present, and a
// missing matched account remains the billing operation error.
func (r *UserStore) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	return r.IdentityBillingBalance(ctx).UpdateBalance(ctx, id, amount)
}

// AddBalance delegates the supplied balance delta to billing storage while
// retaining the cumulative recharge amount. Zero succeeds before persistence,
// and context transactions take precedence over the retained billing client.
func (r *UserStore) AddBalance(ctx context.Context, id int64, amount float64) error {
	return r.IdentityBillingBalance(ctx).AddBalance(ctx, id, amount)
}

// ApplyRedeemBalanceAdjustment delegates the signed redemption delta and zero
// floor to billing storage. It filters deleted accounts, uses an injected
// transaction when present, and leaves cumulative recharges unchanged.
func (r *UserStore) ApplyRedeemBalanceAdjustment(ctx context.Context, id int64, delta float64) error {
	return r.IdentityBillingBalance(ctx).ApplyRedeemBalanceAdjustment(ctx, id, delta)
}

// DeductBalance delegates capped positive available-balance deduction using
// the selected raw executor. Existing nonpositive balances yield zero
// deduction; missing executor and billing-query failures remain errors before
// the amount is reported.
func (r *UserStore) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	sqlq := r.IdentitySqlExecutorFromContext(ctx)
	if sqlq == nil {
		return 0, fmt.Errorf("sql executor is not configured")
	}

	_, deductedAmount, err := IdentityDeductUserBalance(ctx, sqlq, id, amount)
	if err != nil {
		return 0, err
	}
	return deductedAmount, nil
}

// AdjustBalance delegates a delta and old/new reporting to billing storage.
// The selected transaction retains the update scope, and insufficient or
// missing funds remain the billing domain results for the caller.
func (r *UserStore) AdjustBalance(ctx context.Context, id int64, delta float64) (identitycore.BalanceChange, error) {
	return r.IdentityBillingBalance(ctx).AdjustBalance(ctx, id, delta)
}

// SetBalance delegates balance replacement and old/new reporting to billing
// storage. The selected transaction retains update scope, while negative
// replacement and missing account cases remain the billing domain results.
func (r *UserStore) SetBalance(ctx context.Context, id int64, value float64) (identitycore.BalanceChange, error) {
	return r.IdentityBillingBalance(ctx).SetBalance(ctx, id, value)
}

// UpdateConcurrency delegates a signed allowance delta to ConcurrencyStore
// using the configured Ent client. Context transactions take precedence in
// that store, and a missing matched account returns ErrUserNotFound.
func (r *UserStore) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	return NewConcurrencyStore(r.client).UpdateConcurrency(ctx, id, amount)
}

// ApplyRedeemConcurrencyAdjustment delegates redemption allowance adjustment
// with a zero floor to ConcurrencyStore. It selects the context transaction
// when present and reports missing current accounts through the delegated
// result.
func (r *UserStore) ApplyRedeemConcurrencyAdjustment(ctx context.Context, id int64, delta int) error {
	return NewConcurrencyStore(r.client).ApplyRedeemConcurrencyAdjustment(ctx, id, delta)
}

// BatchSetConcurrency replaces current-account allowances through the selected
// raw executor, flooring negative input to zero. Empty IDs return zero before
// persistence; affected-count read errors are discarded after a successful
// statement.
func (r *UserStore) BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	// Account concurrency replacement cannot store a negative allowance; clamp
	// the requested value before building the current-account update.
	if value < 0 {
		value = 0
	}
	sqlq := r.IdentitySqlExecutorFromContext(ctx)
	if sqlq == nil {
		return 0, fmt.Errorf("sql executor is not configured")
	}
	res, err := sqlq.ExecContext(ctx,
		"UPDATE users SET concurrency = $1, updated_at = NOW() WHERE id = ANY($2) AND deleted_at IS NULL",
		value, pq.Array(userIDs))
	if err != nil {
		return 0, fmt.Errorf("batch set concurrency: %w", err)
	}
	// The SQL mutation already succeeded. Discard affected-count errors and
	// return the observed count, so an unavailable count can appear as zero after
	// a completed write.
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

// BatchAddConcurrency adds the supplied allowance delta with an atomic zero
// floor for current accounts. Empty IDs return zero before persistence, while
// affected-count read errors are discarded after successful SQL execution.
func (r *UserStore) BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error) {
	if len(userIDs) == 0 {
		return 0, nil
	}
	sqlq := r.IdentitySqlExecutorFromContext(ctx)
	if sqlq == nil {
		return 0, fmt.Errorf("sql executor is not configured")
	}
	res, err := sqlq.ExecContext(ctx,
		"UPDATE users SET concurrency = GREATEST(concurrency + $1, 0), updated_at = NOW() WHERE id = ANY($2) AND deleted_at IS NULL",
		delta, pq.Array(userIDs))
	if err != nil {
		return 0, fmt.Errorf("batch add concurrency: %w", err)
	}
	// The SQL mutation already succeeded. Discard affected-count errors and
	// return the observed count, so an unavailable count can appear as zero after
	// a completed write.
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

// BatchUpdateLimits replaces supplied concurrency and RPM fields in one
// current-account SQL update. Nil limit pointers leave their columns
// unchanged, negative supplied limits floor to zero, and absent IDs or both
// limits skip persistence.
func (r *UserStore) BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	if len(userIDs) == 0 || (concurrency == nil && rpmLimit == nil) {
		return 0, nil
	}
	sqlq := r.IdentitySqlExecutorFromContext(ctx)
	if sqlq == nil {
		return 0, fmt.Errorf("sql executor is not configured")
	}

	setClauses := make([]string, 0, 3)
	args := make([]any, 0, 3)
	if concurrency != nil {
		value := max(*concurrency, 0)
		args = append(args, value)
		setClauses = append(setClauses, fmt.Sprintf("concurrency = $%d", len(args)))
	}
	if rpmLimit != nil {
		value := max(*rpmLimit, 0)
		args = append(args, value)
		setClauses = append(setClauses, fmt.Sprintf("rpm_limit = $%d", len(args)))
	}
	setClauses = append(setClauses, "updated_at = NOW()")
	args = append(args, pq.Array(userIDs))

	query := fmt.Sprintf(
		"UPDATE users SET %s WHERE id = ANY($%d) AND deleted_at IS NULL",
		strings.Join(setClauses, ", "),
		len(args),
	)
	res, err := sqlq.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("batch update user limits: %w", err)
	}
	// The SQL mutation already succeeded. Discard affected-count errors and
	// return the observed count, so an unavailable count can appear as zero after
	// a completed write.
	affected, _ := res.RowsAffected()
	return int(affected), nil
}

// ExistsByEmail checks trimmed, case-folded email presence using the selected
// Ent client. Empty normalized input uses the original exact text predicate;
// ordinary user soft-delete filtering remains with the query context.
func (r *UserStore) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	client := clientFromContext(ctx, r.client)
	return client.User.Query().Where(IdentityUserEmailLookupPredicate(email)).Exist(ctx)
}

// ExistsByEmailAlias delegates normalized mailbox ownership lookup with no
// current account preference. The bounded candidate probe and Go recheck
// determine its result, and ordinary user soft-delete filtering remains
// context dependent.
func (r *UserStore) ExistsByEmailAlias(ctx context.Context, email string) (bool, error) {
	_, exists, err := r.EmailAliasOwnerID(ctx, email, 0)
	return exists, err
}

// EmailAliasOwnerID delegates bounded mailbox ownership lookup using the
// selected Ent client. Another matching owner takes precedence over the
// supplied currentUserID, while absent or unsupported mailbox input reports no
// owner.
func (r *UserStore) EmailAliasOwnerID(ctx context.Context, email string, currentUserID int64) (int64, bool, error) {
	return IdentityEmailAliasOwnerIDWithClient(ctx, clientFromContext(ctx, r.client), email, currentUserID)
}

// IdentityEmailAliasOwnerIDWithClient probes at most fifty possible mailbox
// aliases and rechecks them with Go normalization. Missing client or unusable
// email probes return no owner.
//
// SQL dot-stripped equality and plus-suffix patterns produce a broad candidate
// set, with wildcard escaping retaining literal input characters. Go recheck
// restores domain-specific alias rules before selection. Another matching
// account wins over the current user; otherwise its matching row can be
// returned. The candidate cap limits this observed search and does not
// establish completeness across an arbitrarily large ambiguous candidate set.
func IdentityEmailAliasOwnerIDWithClient(ctx context.Context, client *dbent.Client, email string, currentUserID int64) (int64, bool, error) {
	if client == nil {
		return 0, false, nil
	}
	probes := identitycore.EmailAliasDedupProbes(email)
	if len(probes) == 0 {
		return 0, false, nil
	}

	preds := make([]predicate.User, 0, 2*len(probes))
	for _, probe := range probes {
		probeEmail := probe.Local + "@" + probe.Domain
		preds = append(preds,
			IdentityDotStrippedEmailEQ(probeEmail),
			// The stored plus suffix is unknown, so use a wildcard after the escaped
			// base local part. Go recheck then decides whether the candidate belongs to
			// the same mailbox.
			IdentityDotStrippedEmailLike(IdentityEscapeLikeWildcards(probe.Local)+"+%@"+IdentityEscapeLikeWildcards(probe.Domain)),
		)
	}

	candidates, err := client.User.Query().
		Where(dbuser.Or(preds...)).
		Limit(IdentityEmailAliasCandidateLimit).
		Select(dbuser.FieldID, dbuser.FieldEmail).
		All(ctx)
	if err != nil {
		return 0, false, err
	}

	identity := identitycore.NormalizeEmailForAliasDedup(email)
	var selfID int64
	selfExists := false
	for _, candidate := range candidates {
		if identitycore.NormalizeEmailForAliasDedup(candidate.Email) != identity {
			continue
		}
		if candidate.ID != 0 && candidate.ID != currentUserID {
			return candidate.ID, true, nil
		}
		if candidate.ID == currentUserID {
			selfID = candidate.ID
			selfExists = true
		}
	}
	return selfID, selfExists, nil
}

// IdentityDotStrippedEmailExpr writes a broad lowercase trimmed dot-removal
// expression for alias candidate SQL. The later Go mailbox recheck restores
// distinctions that this candidate expression deliberately does not retain.
func IdentityDotStrippedEmailExpr(b *entsql.Builder, s *entsql.Selector) *entsql.Builder {
	return b.WriteString("REPLACE(LOWER(TRIM(").
		Ident(s.C(dbuser.FieldEmail)).
		WriteString(")), '.', '')")
}

// IdentityDotStrippedEmailEQ returns exact SQL matching against the
// dot-stripped candidate expression. Callers supply their normalized probe
// value and recheck resulting accounts with the full Go mailbox alias rules.
func IdentityDotStrippedEmailEQ(value string) predicate.User {
	return predicate.User(func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			IdentityDotStrippedEmailExpr(b, s).WriteString(" = ").Arg(value)
		}))
	})
}

// IdentityDotStrippedEmailLike returns LIKE matching against the dot-stripped
// candidate expression with backslash escaping. Callers escape literal
// components before adding their intended wildcards, then recheck selected
// account mailboxes in Go.
func IdentityDotStrippedEmailLike(pattern string) predicate.User {
	return predicate.User(func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			IdentityDotStrippedEmailExpr(b, s).WriteString(" LIKE ").Arg(pattern).WriteString(` ESCAPE '\'`)
		}))
	})
}

// IdentityEscapeLikeWildcards escapes backslash, percent, and underscore
// before alias wildcard construction.
func IdentityEscapeLikeWildcards(value string) string {
	return IdentityLikeWildcardEscaper.Replace(value)
}

// UpdateEmailWithAliasGuard writes account email and password after a locked
// mailbox ownership recheck in the caller Ent transaction. Positive account
// ID, nonblank email, and password hash are required.
//
// Sorted exact-email and alias lock keys serialize writes to the destination
// mailbox. The owner probe prefers another account over the current one,
// returning ErrEmailExists when that conflict is observed. The caller
// transaction covers this credential write and later identity or grant stages.
// Missing transaction is an error, and storage failures use the configured
// account error mapping.
func (r *UserStore) UpdateEmailWithAliasGuard(
	ctx context.Context,
	userID int64,
	email string,
	passwordHash string,
) error {
	if userID <= 0 {
		return identitycore.ErrUserNotFound
	}
	if strings.TrimSpace(email) == "" || passwordHash == "" {
		return fmt.Errorf("email identity update requires email and password hash")
	}
	tx := dbent.TxFromContext(ctx)
	if tx == nil {
		return fmt.Errorf("email identity update requires a transaction")
	}
	client := tx.Client()

	releaseEmailLock, err := IdentityLockRepositoryScopedKeys(
		ctx,
		client,
		IdentityTxAwareSQLExecutor(ctx, r.sql, r.client),
		IdentityNormalizedEmailUniquenessLockKey(email),
		IdentityEmailAliasUniquenessLockKey(email),
	)
	if err != nil {
		return err
	}
	defer releaseEmailLock()

	ownerID, exists, err := IdentityEmailAliasOwnerIDWithClient(ctx, client, email, userID)
	if err != nil {
		return err
	}
	if exists && ownerID != userID {
		return identitycore.ErrEmailExists
	}

	if _, err := client.User.UpdateOneID(userID).
		SetEmail(email).
		SetPasswordHash(passwordHash).
		Save(ctx); err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, identitycore.ErrEmailExists)
	}
	return nil
}

// ExistsByNormalizedEmail delegates current-row lookup with no account
// excluded. Callers supply already registration-normalized text; selected raw
// executor or query failures remain errors rather than an absent-email
// observation.
func (r *UserStore) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	return r.IdentityExistsByNormalizedEmail(ctx, normalizedEmail, 0)
}

// ExistsByNormalizedEmailExcluding delegates registration-normalized email
// presence while excluding the supplied account ID. Zero exclusion retains all
// current rows, and raw executor/query errors remain errors for registration
// guards.
func (r *UserStore) ExistsByNormalizedEmailExcluding(ctx context.Context, normalizedEmail string, excludedUserID int64) (bool, error) {
	return r.IdentityExistsByNormalizedEmail(ctx, normalizedEmail, excludedUserID)
}

// IdentityExistsByNormalizedEmail runs the shared registration-normalized SQL
// expression against current user rows. A nonzero excluded ID removes that
// account, and a missing selected raw executor returns an error before
// querying.
func (r *UserStore) IdentityExistsByNormalizedEmail(ctx context.Context, normalizedEmail string, excludedUserID int64) (bool, error) {
	sqlq := r.IdentitySqlExecutorFromContext(ctx)
	if sqlq == nil {
		return false, fmt.Errorf("sql executor is not configured")
	}

	var exists bool
	query := fmt.Sprintf(`
		SELECT EXISTS(
			SELECT 1
			FROM users
			WHERE deleted_at IS NULL
			  AND %s = $1
			  AND ($2 = 0 OR id <> $2)
		)
	`, IdentityNormalizedUserEmailSQL)
	if err := scanSingleRow(ctx, sqlq, query, []any{normalizedEmail, excludedUserID}, &exists); err != nil {
		return false, err
	}
	return exists, nil
}

// LockRegistrationEmail takes a transaction-scoped PostgreSQL lock for the
// supplied normalized address. Blank input and configured non-PostgreSQL
// dialects skip the lock; the caller keeps subsequent guarded writes in the
// same database transaction.
func (r *UserStore) LockRegistrationEmail(ctx context.Context, normalizedEmail string) error {
	if normalizedEmail == "" {
		return nil
	}
	if r.client != nil && r.client.Driver().Dialect() != dialect.Postgres {
		return nil
	}

	sqlq := r.IdentitySqlExecutorFromContext(ctx)
	if sqlq == nil {
		return fmt.Errorf("sql executor is not configured")
	}
	_, err := sqlq.ExecContext(
		ctx,
		"SELECT pg_advisory_xact_lock($1, hashtext($2))",
		IdentityRegistrationEmailLockNamespace,
		normalizedEmail,
	)
	return err
}

// IdentityEnsureNormalizedEmailAvailableWithClient rejects a trimmed
// case-folded email owned by another selected account row. Missing client
// succeeds, and the supplied userID permits its own row while ordinary
// soft-delete context controls matches.
func IdentityEnsureNormalizedEmailAvailableWithClient(ctx context.Context, client *dbent.Client, userID int64, email string) error {
	client = clientFromContext(ctx, client)
	if client == nil {
		return nil
	}

	matches, err := client.User.Query().
		Where(IdentityUserEmailLookupPredicate(email)).
		All(ctx)
	if err != nil {
		return err
	}
	for _, match := range matches {
		if match.ID != userID {
			return identitycore.ErrEmailExists
		}
	}
	return nil
}

// IdentityUserEmailLookupPredicate matches trimmed lowercase email text
// without rewriting the stored column. Empty normalized input uses exact
// original text; query callers separately apply lifecycle and transaction
// selection rules.
func IdentityUserEmailLookupPredicate(email string) predicate.User {
	normalized := IdentityNormalizeEmailLookupValue(email)
	if normalized == "" {
		return dbuser.EmailEQ(email)
	}
	return predicate.User(func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			b.WriteString("LOWER(TRIM(").
				Ident(s.C(dbuser.FieldEmail)).
				WriteString(")) = ").
				Arg(normalized)
		}))
	})
}

// IdentityNormalizeEmailLookupValue returns trimmed lowercase email text for
// exact account lookup.
func IdentityNormalizeEmailLookupValue(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IdentityNormalizedEmailUniquenessLockKey returns the scoped lock key for
// trimmed lowercase email text. Empty normalized input returns an empty key,
// which scoped lock normalization omits before acquisition.
func IdentityNormalizedEmailUniquenessLockKey(email string) string {
	normalized := IdentityNormalizeEmailLookupValue(email)
	if normalized == "" {
		return ""
	}
	return "users:normalized-email:" + normalized
}

// IdentityEmailAliasUniquenessLockKey returns the shared lock key for aliases
// of one normalized mailbox. Unsupported or empty mailbox input yields no key,
// and caller transaction participation protects the subsequent ownership
// write.
func IdentityEmailAliasUniquenessLockKey(email string) string {
	identity := identitycore.NormalizeEmailForAliasDedup(email)
	if identity == "" {
		return ""
	}
	return "users:email-alias-identity:" + identity
}

// IdentityRegistrationEmailDomainLockKey returns a scoped key for the
// normalized registrable email domain. Empty normalization yields no key,
// letting guarded creation omit unusable domain observations before locking.
func IdentityRegistrationEmailDomainLockKey(domain string) string {
	domain = IdentityNormalizeEmailDomain(domain)
	if domain == "" {
		return ""
	}
	return "users:registration-email-domain:" + domain
}

// IdentityNormalizeEmailDomain delegates registrable-domain normalization used
// by account quota keys and predicates.
func IdentityNormalizeEmailDomain(domain string) string {
	return identitycore.NormalizeRegistrationEmailDomain(domain)
}

// IdentityCountUsersByEmailDomainWithClient counts selected user rows in a
// normalized registrable domain and its subdomains. Missing client or blank
// domain returns zero, while ordinary query context retains soft-delete
// filtering.
func IdentityCountUsersByEmailDomainWithClient(ctx context.Context, client *dbent.Client, domain string) (int, error) {
	client = clientFromContext(ctx, client)
	domain = IdentityNormalizeEmailDomain(domain)
	if client == nil || domain == "" {
		return 0, nil
	}
	return client.User.Query().Where(IdentityUserEmailDomainPredicate(domain)).Count(ctx)
}

// IdentityUserEmailDomainPredicate matches normalized root-domain and
// subdomain suffixes using escaped literal domain components. Stored case,
// outer spacing, and root dots are normalized in the expression before the
// caller user query filters lifecycle.
func IdentityUserEmailDomainPredicate(domain string) predicate.User {
	domain = IdentityNormalizeEmailDomain(domain)
	escapedDomain := escapeLikePattern(domain)
	exactPattern := "%@" + escapedDomain
	subdomainPattern := "%@%." + escapedDomain
	return predicate.User(func(s *entsql.Selector) {
		s.Where(entsql.P(func(b *entsql.Builder) {
			b.WriteString("(RTRIM(LOWER(TRIM(").
				Ident(s.C(dbuser.FieldEmail)).
				WriteString(")), '.') LIKE ").
				Arg(exactPattern).
				WriteString(` ESCAPE '\' OR RTRIM(LOWER(TRIM(`).
				Ident(s.C(dbuser.FieldEmail)).
				WriteString(")), '.') LIKE ").
				Arg(subdomainPattern).
				WriteString(` ESCAPE '\'`).
				WriteString(")")
		}))
	})
}

// AddGroupToAllowedGroups stores one account/group grant with do-nothing
// conflict handling in the selected client. Recognized SQL no-row conflict
// results succeed, and the caller owns any surrounding transaction completion.
func (r *UserStore) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	client := clientFromContext(ctx, r.client)
	err := client.UserAllowedGroup.Create().
		SetUserID(userID).
		SetGroupID(groupID).
		OnConflictColumns(userallowedgroup.FieldUserID, userallowedgroup.FieldGroupID).
		DoNothing().
		Exec(ctx)
	// Conflict-tolerant grant insertion can report no returned SQL row. Accept
	// that result so repeated requests for an existing account/group grant do not
	// become operation failures.
	if isSQLNoRowsError(err) {
		return nil
	}
	return err
}

// RemoveGroupFromAllowedGroups deletes the selected group grants through the
// configured client and returns its count. This method uses the relation table
// directly and leaves transaction selection to that retained client.
func (r *UserStore) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	affected, err := r.client.UserAllowedGroup.Delete().
		Where(userallowedgroup.GroupIDEQ(groupID)).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return int64(affected), nil
}

// RemoveGroupFromUserAllowedGroups removes one account/group grant through the
// selected Ent client. Absent grants succeed when Ent reports no error, and
// the caller owns any injected transaction completion.
func (r *UserStore) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserAllowedGroup.Delete().
		Where(userallowedgroup.UserIDEQ(userID), userallowedgroup.GroupIDEQ(groupID)).
		Exec(ctx)
	return err
}

// GetFirstAdmin returns the first active administrator by ascending account ID
// through the configured client. It loads sorted grant and denial data and
// marks complete restrictions; no matching account maps to ErrUserNotFound.
func (r *UserStore) GetFirstAdmin(ctx context.Context) (*identitycore.User, error) {
	m, err := r.client.User.Query().
		Where(
			dbuser.RoleEQ(identitycore.RoleAdmin),
			dbuser.StatusEQ(identitycore.StatusActive),
		).
		Order(dbent.Asc(dbuser.FieldID)).
		First(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}

	out := UserFromEntity(m)
	groups, err := r.IdentityLoadAllowedGroups(ctx, []int64{m.ID})
	if err != nil {
		return nil, err
	}
	if v, ok := groups[m.ID]; ok {
		out.AllowedGroups = v
	}
	disabledPublicGroups, err := r.IdentityLoadDisabledPublicGroups(ctx, []int64{m.ID})
	if err != nil {
		return nil, err
	}
	if v, ok := disabledPublicGroups[m.ID]; ok {
		out.DisabledPublicGroups = v
	}
	out.GroupRestrictionsLoaded = true
	return out, nil
}

// IdentityLoadAllowedGroups returns sorted grant IDs by supplied user ID
// through the configured client. Empty input returns an allocated map, and
// users with no grant rows have no map entry.
func (r *UserStore) IdentityLoadAllowedGroups(ctx context.Context, userIDs []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}

	rows, err := r.client.UserAllowedGroup.Query().
		Where(userallowedgroup.UserIDIn(userIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	for i := range rows {
		out[rows[i].UserID] = append(out[rows[i].UserID], rows[i].GroupID)
	}

	for userID := range out {
		slices.Sort(out[userID])
	}

	return out, nil
}

// IdentityLoadDisabledPublicGroups returns sorted public-denial IDs by
// supplied user ID through the configured client. Empty input returns an
// allocated map, while users without denial rows have no map entry.
func (r *UserStore) IdentityLoadDisabledPublicGroups(ctx context.Context, userIDs []int64) (map[int64][]int64, error) {
	out := make(map[int64][]int64, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}

	rows, err := r.client.UserDisabledPublicGroup.Query().
		Where(userdisabledpublicgroup.UserIDIn(userIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	for i := range rows {
		out[rows[i].UserID] = append(out[rows[i].UserID], rows[i].GroupID)
	}

	for userID := range out {
		slices.Sort(out[userID])
	}

	return out, nil
}

// IdentitySyncUserAllowedGroupsWithClient updates an account grant set through
// the caller supplied client. Positive unique requested IDs form the desired
// set; callers provide the transaction containing its removals and additions.
//
// Existing grants absent from the desired set are removed before missing
// requested grants are inserted in bulk. Do-nothing conflicts tolerate already
// stored pairs, with recognized no-row outcomes accepted. A later failure can
// follow completed removals, so transaction rollback belongs to the enclosing
// account operation. Nil client succeeds without work.
func (r *UserStore) IdentitySyncUserAllowedGroupsWithClient(ctx context.Context, client *dbent.Client, userID int64, groupIDs []int64) error {
	if client == nil {
		return nil
	}

	existingRows, err := client.UserAllowedGroup.Query().
		Where(userallowedgroup.UserIDEQ(userID)).
		All(ctx)
	if err != nil {
		return err
	}

	desired := make(map[int64]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		desired[id] = struct{}{}
	}

	existing := make(map[int64]struct{}, len(existingRows))
	removed := make([]int64, 0)
	for _, row := range existingRows {
		existing[row.GroupID] = struct{}{}
		if _, keep := desired[row.GroupID]; !keep {
			removed = append(removed, row.GroupID)
		}
	}
	// Remove obsolete grants before inserting the desired missing pairs. A later
	// insertion failure requires caller rollback, so this intermediate grant set
	// is not a completed synchronization result.
	if len(removed) > 0 {
		if _, err := client.UserAllowedGroup.Delete().
			Where(userallowedgroup.UserIDEQ(userID), userallowedgroup.GroupIDIn(removed...)).
			Exec(ctx); err != nil {
			return err
		}
	}

	creates := make([]*dbent.UserAllowedGroupCreate, 0, len(desired))
	for groupID := range desired {
		if _, present := existing[groupID]; !present {
			creates = append(creates, client.UserAllowedGroup.Create().SetUserID(userID).SetGroupID(groupID))
		}
	}
	if len(creates) > 0 {
		if err := client.UserAllowedGroup.
			CreateBulk(creates...).
			OnConflictColumns(userallowedgroup.FieldUserID, userallowedgroup.FieldGroupID).
			DoNothing().
			Exec(ctx); err != nil {
			// A recognized no-row result from do-nothing bulk insertion counts as
			// successful synchronization. Earlier grant removals have already run, so
			// callers use their surrounding transaction to recover other insert
			// failures.
			if isSQLNoRowsError(err) {
				return nil
			}
			return err
		}
	}

	return nil
}

// IdentitySyncUserDisabledPublicGroupsWithClient replaces the account denial
// set using verified current public groups. The caller supplies the
// transaction spanning deletion, group lookup, and bulk insertion.
//
// It removes preceding denials first, then deduplicates positive requested IDs
// and looks up groups whose exclusive flag is false. Only those selected
// public IDs are inserted in sorted candidate order. Empty or wholly invalid
// selections leave the set empty. Lookup or insertion failure relies on caller
// rollback of the preceding delete, and recognized do-nothing no-row outcomes
// succeed.
func (r *UserStore) IdentitySyncUserDisabledPublicGroupsWithClient(ctx context.Context, client *dbent.Client, userID int64, groupIDs []int64) error {
	if client == nil {
		return nil
	}

	// Removing the preceding set first lets empty or invalid requested IDs clear
	// all denials. A later public-group lookup or insert failure needs caller
	// rollback to restore that removed set.
	if _, err := client.UserDisabledPublicGroup.Delete().Where(userdisabledpublicgroup.UserIDEQ(userID)).Exec(ctx); err != nil {
		return err
	}

	unique := make(map[int64]struct{}, len(groupIDs))
	for _, id := range groupIDs {
		if id <= 0 {
			continue
		}
		unique[id] = struct{}{}
	}
	if len(unique) == 0 {
		return nil
	}

	candidateIDs := make([]int64, 0, len(unique))
	for groupID := range unique {
		candidateIDs = append(candidateIDs, groupID)
	}
	slices.Sort(candidateIDs)

	publicIDs, err := client.Group.Query().
		Where(
			dbgroup.IDIn(candidateIDs...),
			dbgroup.IsExclusiveEQ(false),
		).
		IDs(ctx)
	if err != nil {
		return err
	}
	publicSet := make(map[int64]struct{}, len(publicIDs))
	for _, groupID := range publicIDs {
		publicSet[groupID] = struct{}{}
	}

	creates := make([]*dbent.UserDisabledPublicGroupCreate, 0, len(publicIDs))
	for _, groupID := range candidateIDs {
		if _, ok := publicSet[groupID]; !ok {
			continue
		}
		creates = append(creates, client.UserDisabledPublicGroup.Create().SetUserID(userID).SetGroupID(groupID))
	}
	if len(creates) == 0 {
		return nil
	}
	if err := client.UserDisabledPublicGroup.
		CreateBulk(creates...).
		OnConflictColumns(userdisabledpublicgroup.FieldUserID, userdisabledpublicgroup.FieldGroupID).
		DoNothing().
		Exec(ctx); err != nil {
		// No-row conflict results are accepted after denial replacement. The
		// preceding deletion has already run, so callers own transactional recovery
		// when another insertion error is returned.
		if isSQLNoRowsError(err) {
			return nil
		}
		return err
	}

	return nil
}

// IdentityApplyUserEntityToService refreshes identifiers, signup source, and
// row activity/timestamps on the supplied account. Nil source or destination
// is ignored, and optional activity pointers remain shared with the entity.
func IdentityApplyUserEntityToService(dst *identitycore.User, src *dbent.User) {
	if dst == nil || src == nil {
		return
	}
	dst.ID = src.ID
	dst.SignupSource = src.SignupSource
	dst.LastLoginAt = src.LastLoginAt
	dst.LastActiveAt = src.LastActiveAt
	dst.CreatedAt = src.CreatedAt
	dst.UpdatedAt = src.UpdatedAt
}

// IdentitySqlExecutorFromContext returns the injected Ent transaction client
// or retained raw executor. A missing retained executor can return nil, which
// raw-query callers check before executing their operation.
func (r *UserStore) IdentitySqlExecutorFromContext(ctx context.Context) SQLExecutor {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return r.sql
}

// IdentityDeductUserBalance delegates available-funds deduction and
// prior-balance reporting to billing queries. The caller supplies the query
// executor and surrounding transaction, while billing owns capped deduction
// and missing-account results.
func IdentityDeductUserBalance(ctx context.Context, q sqlQueryer, userID int64, amount float64) (float64, float64, error) {
	return billingpostgres.DeductAvailableBalance(ctx, q, userID, amount)
}

// IdentityUpdateWithClient writes the selected scalar account flags through
// the supplied client. Nil locale, recharge override, activity timestamp, or
// notification threshold pointers clear their selected fields, while permission
// and identity synchronization remain with the enclosing updater.
func (r *UserStore) IdentityUpdateWithClient(ctx context.Context, client *dbent.Client, userIn *identitycore.User, fields identitycore.UserUpdateFields) (*dbent.User, error) {
	updateOp := client.User.UpdateOneID(userIn.ID)
	if fields.Email {
		updateOp = updateOp.SetEmail(userIn.Email)
	}
	if fields.PreferredLocale {
		if userIn.PreferredLocale == nil {
			updateOp = updateOp.ClearPreferredLocale()
		} else {
			updateOp = updateOp.SetPreferredLocale(*userIn.PreferredLocale)
		}
	}
	if fields.Username {
		updateOp = updateOp.SetUsername(userIn.Username)
	}
	if fields.Notes {
		updateOp = updateOp.SetNotes(userIn.Notes)
	}
	if fields.PasswordHash {
		updateOp = updateOp.SetPasswordHash(userIn.PasswordHash)
	}
	if fields.Role {
		updateOp = updateOp.SetRole(userIn.Role)
	}
	if fields.Concurrency {
		updateOp = updateOp.SetConcurrency(userIn.Concurrency)
	}
	if fields.Status {
		updateOp = updateOp.SetStatus(userIn.Status)
	}
	if fields.SignupSource {
		updateOp = updateOp.SetSignupSource(IdentityUserSignupSourceOrDefault(userIn.SignupSource))
	}
	if fields.LastLoginAt {
		if userIn.LastLoginAt != nil {
			updateOp = updateOp.SetLastLoginAt(*userIn.LastLoginAt)
		} else {
			updateOp = updateOp.ClearLastLoginAt()
		}
	}
	if fields.LastActiveAt {
		if userIn.LastActiveAt != nil {
			updateOp = updateOp.SetLastActiveAt(*userIn.LastActiveAt)
		} else {
			updateOp = updateOp.ClearLastActiveAt()
		}
	}
	if fields.RPMLimit {
		updateOp = updateOp.SetRpmLimit(userIn.RPMLimit)
	}
	if fields.APIKeyLimit {
		updateOp = updateOp.SetAPIKeyLimit(userIn.APIKeyLimit)
	}
	if fields.BalanceRechargeMultiplier {
		if userIn.BalanceRechargeMultiplier == nil {
			updateOp = updateOp.ClearBalanceRechargeMultiplier()
		} else {
			updateOp = updateOp.SetBalanceRechargeMultiplier(*userIn.BalanceRechargeMultiplier)
		}
	}
	if fields.BalanceNotifySettings {
		updateOp = updateOp.
			SetBalanceNotifyEnabled(userIn.BalanceNotifyEnabled).
			SetBalanceNotifyThresholdType(userIn.BalanceNotifyThresholdType)
		if userIn.BalanceNotifyThreshold != nil {
			updateOp = updateOp.SetBalanceNotifyThreshold(*userIn.BalanceNotifyThreshold)
		} else {
			updateOp = updateOp.ClearBalanceNotifyThreshold()
		}
	}
	if fields.BalanceNotifyExtraEmails {
		updateOp = updateOp.SetBalanceNotifyExtraEmails(IdentityMarshalExtraEmails(userIn.BalanceNotifyExtraEmails))
	}
	return updateOp.Save(ctx)
}

// IdentityCreateWithClient stores prepared account fields through the caller
// client and applies initial funds through billing. Signup source receives its
// supported default, and the enclosing creator owns permissions, email
// identity, and transaction completion.
func (r *UserStore) IdentityCreateWithClient(ctx context.Context, client *dbent.Client, userIn *identitycore.User) (*dbent.User, error) {
	createOp := client.User.Create().
		SetEmail(userIn.Email).
		SetUsername(userIn.Username).
		SetNotes(userIn.Notes).
		SetPasswordHash(userIn.PasswordHash).
		SetRole(userIn.Role).
		SetConcurrency(userIn.Concurrency).
		SetStatus(userIn.Status).
		SetSignupSource(IdentityUserSignupSourceOrDefault(userIn.SignupSource)).
		SetNillableLastLoginAt(userIn.LastLoginAt).
		SetNillableLastActiveAt(userIn.LastActiveAt).
		SetRpmLimit(userIn.RPMLimit).
		SetNillableBalanceRechargeMultiplier(userIn.BalanceRechargeMultiplier).
		SetAPIKeyLimit(userIn.APIKeyLimit)
	billingpostgres.ApplyInitialUserFunds(createOp, billing.InitialUserFunds{Balance: userIn.Balance})
	return createOp.Save(ctx)
}

// IdentityUserSignupSourceOrDefault returns a supported trimmed lowercase
// signup-source label. Blank, email, and unsupported values select email,
// while the configured external provider labels retain their normalized names.
func IdentityUserSignupSourceOrDefault(signupSource string) string {
	switch strings.TrimSpace(strings.ToLower(signupSource)) {
	case "", "email":
		return "email"
	case "linuxdo", "wechat", "oidc", "github", "google", "dingtalk":
		return strings.TrimSpace(strings.ToLower(signupSource))
	default:
		return "email"
	}
}

// IdentityMarshalExtraEmails delegates notification-recipient JSON encoding
// for the stored account field.
func IdentityMarshalExtraEmails(entries []identitycore.NotifyEmailEntry) string {
	return identitycore.MarshalNotifyEmails(entries)
}

// UpdateTotpSecret replaces the account encrypted authenticator secret or
// clears it for nil input. Context transaction client takes precedence, and
// missing accounts map to ErrUserNotFound through persistence error
// translation.
func (r *UserStore) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	client := clientFromContext(ctx, r.client)
	update := client.User.UpdateOneID(userID)
	if encryptedSecret == nil {
		update = update.ClearTotpSecretEncrypted()
	} else {
		update = update.SetTotpSecretEncrypted(*encryptedSecret)
	}
	_, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	return nil
}

// EnableTotp saves account authenticator enablement and its current activation
// time together. It selects the context transaction client when present and
// maps missing rows to ErrUserNotFound before returning.
func (r *UserStore) EnableTotp(ctx context.Context, userID int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.User.UpdateOneID(userID).
		SetTotpEnabled(true).
		SetTotpEnabledAt(time.Now()).
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	return nil
}

// DisableTotp clears account authenticator secret and activation time while
// saving disabled state. It selects the context transaction client when
// present and maps missing rows to ErrUserNotFound before returning.
func (r *UserStore) DisableTotp(ctx context.Context, userID int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.User.UpdateOneID(userID).
		SetTotpEnabled(false).
		ClearTotpEnabledAt().
		ClearTotpSecretEncrypted().
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	return nil
}

// IdentityBillingBalance returns billing storage participating in the injected
// Ent transaction when present. Otherwise it retains the configured client for
// ordinary account funds operations, with transaction completion left to
// callers.
func (r *UserStore) IdentityBillingBalance(ctx context.Context) *billingpostgres.BalanceStore {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return billingpostgres.BalanceInTx(tx)
	}
	return billingpostgres.NewBalanceStore(r.client)
}

// clientFromContext returns the injected Ent transaction client before the
// supplied default client. It creates no transaction, and callers configure
// the default needed when their context has no transaction.
func clientFromContext(ctx context.Context, defaultClient *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return defaultClient
}

// translatePersistenceError maps recognized missing rows or unique conflicts
// to supplied application errors with their causes. Nil mapping targets skip
// that category, nil input succeeds, and unmatched errors retain their
// original result.
func translatePersistenceError(err error, notFound, conflict *infraerrors.ApplicationError) error {
	if err == nil {
		return nil
	}

	// Both Ent NotFoundError and sql.ErrNoRows indicate a missing record.
	if notFound != nil && (errors.Is(err, sql.ErrNoRows) || dbent.IsNotFound(err)) {
		return notFound.WithCause(err)
	}

	if conflict != nil && isUniqueConstraintViolation(err) {
		return conflict.WithCause(err)
	}

	return err
}

// isUniqueConstraintViolation delegates recognition of supported PostgreSQL
// and message-based uniqueness failures.
func isUniqueConstraintViolation(err error) bool {
	return postgresinfra.IsUniqueConstraintViolation(err)
}

// isSQLNoRowsError delegates recognition of a SQL operation reporting no
// matching record.
func isSQLNoRowsError(err error) bool { return postgresinfra.IsNoRows(err) }

// scanSingleRow delegates one-row querying and destination decoding to
// infrastructure storage. The selected executor owns its transaction
// participation, while missing, scanning, iteration, and cleanup errors follow
// that helper.
func scanSingleRow(ctx context.Context, q sqlQueryer, query string, args []any, dest ...any) error {
	return postgresinfra.ScanSingleRow(ctx, q, query, args, dest...)
}

// escapeLikePattern escapes backslash, percent, and underscore in literal LIKE
// pattern components. Callers add the intended wildcard structure afterward,
// retaining literal domain and mailbox input during SQL probing.
func escapeLikePattern(s string) string {
	return likePatternReplacer.Replace(s)
}
