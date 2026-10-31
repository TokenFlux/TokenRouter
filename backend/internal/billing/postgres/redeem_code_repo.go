package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbpredicate "github.com/TokenFlux/TokenRouter/ent/predicate"
	"github.com/TokenFlux/TokenRouter/ent/redeemcode"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"
	"github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// redeem_code_repo.go persists redemption credentials and reconstructs
// per-user usage history.
// A multi-use redemption credential has a current usage summary and separate
// per-user history rows. Administrator reads evaluate expiry and capacity for
// display, while history reconstruction replaces the summary's latest user and
// time with each actual usage. Locked identity reads deliberately leave status
// eligibility to their service caller.
//
// Standalone Use performs its own CanUse check, inserts usage and updates the
// code summary inside a selected transaction. Batch administration checks
// target completeness and sensitive-field rules before updating common fields,
// but that preliminary read is unlocked. Context transactions remain
// caller-owned; owned operations attempt rollback on failure before returning. Derived
// expired filtering can include a disabled record even though sorting and
// display retain disabled precedence.

// RedeemStore persists redemption credentials, usage records and
// administration queries through Ent. Context transactions select its
// operation client, while service callers own entitlement grants and any lock
// lifetime needed across reads and writes.
type RedeemStore struct {
	// client is retained for later operations. A context Ent transaction
	// overrides it, and callers provide usable persistence before any repository
	// method dereferences the selected client.
	client *dbent.Client // base Ent redemption client
}

// NewRedeemCodeRepository retains an Ent client for later redemption
// persistence. It starts no transaction or credential work; methods require
// this client or an Ent transaction carried by context.
func NewRedeemCodeRepository(client *dbent.Client) *RedeemStore {
	return &RedeemStore{client: client}
}

// Create inserts a supplied nonnil redemption record using its credential and
// entitlement fields, then copies generated ID and creation time back. The
// context client wins, and uniqueness failures translate to
// ErrRedeemCodeExists.
func (r *RedeemStore) Create(ctx context.Context, code *billing.RedeemCode) error {
	client := clientFromContext(ctx, r.client)
	builder := client.RedeemCode.Create().
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetRequiresPayment(code.RequiresPayment).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes).
		SetNillableUsedBy(code.UsedBy).
		SetNillableUsedAt(code.UsedAt).
		SetNillablePlanID(code.PlanID).
		SetNillableValidityDays(code.ValidityDays)

	if code.ExpiresAt != nil {
		builder.SetExpiresAt(*code.ExpiresAt)
	}

	created, err := builder.Save(ctx)
	if err == nil {
		code.ID = created.ID
		code.CreatedAt = created.CreatedAt
	}
	return translatePersistenceError(err, nil, billing.ErrRedeemCodeExists)
}

// CreateBatch delegates bulk insertion of the supplied redemption values,
// returning immediately for empty input. It does not copy generated IDs or
// timestamps back to the slice; context client selection and uniqueness
// translation match single creation.
func (r *RedeemStore) CreateBatch(ctx context.Context, codes []billing.RedeemCode) error {
	if len(codes) == 0 {
		return nil
	}

	client := clientFromContext(ctx, r.client)
	builders := make([]*dbent.RedeemCodeCreate, 0, len(codes))
	for i := range codes {
		c := &codes[i]
		builder := client.RedeemCode.Create().
			SetCode(c.Code).
			SetType(c.Type).
			SetValue(c.Value).
			SetStatus(c.Status).
			SetRequiresPayment(c.RequiresPayment).
			SetMaxUses(c.MaxUses).
			SetUsedCount(c.UsedCount).
			SetNotes(c.Notes).
			SetNillableUsedBy(c.UsedBy).
			SetNillableUsedAt(c.UsedAt).
			SetNillablePlanID(c.PlanID).
			SetNillableValidityDays(c.ValidityDays)
		if c.ExpiresAt != nil {
			builder.SetExpiresAt(*c.ExpiresAt)
		}
		builders = append(builders, builder)
	}

	return translatePersistenceError(client.RedeemCode.CreateBulk(builders...).Exec(ctx), nil, billing.ErrRedeemCodeExists)
}

// GetByID reads the exact redemption ID with its latest-user and plan
// relations. Missing rows become ErrRedeemCodeNotFound; the result uses
// current effective display status, leaving redemption eligibility checks to
// the service.
func (r *RedeemStore) GetByID(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.IDEQ(id)).
		WithUser().
		WithPlan().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, billing.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return RedeemFromEntity(model), nil
}

// GetByIDForUpdate reads and locks the exact code ID without eager relation
// loading. The caller supplies a transaction so FOR UPDATE survives subsequent
// validation and writes; the query applies no enabled-status or expiry
// predicate.
func (r *RedeemStore) GetByIDForUpdate(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, billing.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return RedeemFromEntity(model), nil
}

// GetByCode reads the exact credential text with latest-user and plan
// relations. Input is used verbatim without trimming or enabled-state
// filtering; missing rows become ErrRedeemCodeNotFound and conversion
// evaluates current display status.
func (r *RedeemStore) GetByCode(ctx context.Context, code string) (*billing.RedeemCode, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.CodeEQ(code)).
		WithUser().
		WithPlan().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, billing.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return RedeemFromEntity(model), nil
}

// GetByCodeForUpdate locks the exact credential row without eager relation
// loading or status predicates. The caller supplies transaction lifetime and
// checks eligibility after the read; missing rows become
// ErrRedeemCodeNotFound.
func (r *RedeemStore) GetByCodeForUpdate(ctx context.Context, code string) (*billing.RedeemCode, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.CodeEQ(code)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, billing.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return RedeemFromEntity(model), nil
}

// Update saves the supplied nonnil snapshot, clearing optional recipient,
// usage-time, plan and expiry columns when their pointers are nil. Success
// refreshes CreatedAt; missing records become ErrRedeemCodeNotFound, and the
// caller owns transaction completion.
func (r *RedeemStore) Update(ctx context.Context, code *billing.RedeemCode) error {
	client := clientFromContext(ctx, r.client)
	update := redeemUpdateBuilder(client, code)

	updated, err := update.Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return billing.ErrRedeemCodeNotFound
		}
		return err
	}
	code.CreatedAt = updated.CreatedAt
	return nil
}

// BatchUpdate deduplicates requested IDs and applies common status, notes or
// expiry changes in one selected transaction. Empty identities return zero. A
// context transaction is joined directly; otherwise the adapter owns begin,
// callback, commit and deferred rollback, with an already-started client
// delegating joined work.
//
// The inner operation requires every requested record before checking
// used-sensitive changes, then requires the affected count to match. Joined
// failures leave rollback decisions with the transaction owner. Its
// preliminary record read is not FOR UPDATE, so callers needing serialization
// against concurrent redemption must coordinate that separately; successful
// row-count validation alone does not establish those locks.
func (r *RedeemStore) BatchUpdate(ctx context.Context, ids []int64, fields billing.RedeemCodeBatchUpdateFields) (int64, error) {
	uniqueIDs := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return 0, nil
	}

	// Join the caller's transaction directly, so this operation neither commits
	// successful work nor rolls back earlier joined writes when validation fails.
	if dbent.TxFromContext(ctx) != nil {
		return r.batchUpdate(ctx, clientFromContext(ctx, r.client), uniqueIDs, fields)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		// An already-started base client can supply joined work even without a
		// transaction marker in context. Delegate through its selected client and
		// retain external commit ownership.
		if errors.Is(err, dbent.ErrTxStarted) {
			return r.batchUpdate(ctx, clientFromContext(ctx, r.client), uniqueIDs, fields)
		}
		return 0, err
	}
	// Keep rollback as owned cleanup for validation, write or commit failure.
	// Discard its secondary error so the operation's primary financial result
	// remains visible.
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	updated, err := r.batchUpdate(txCtx, tx.Client(), uniqueIDs, fields)
	if err != nil {
		return 0, err
	}
	// Commit after target validation and the affected-row check succeed. Joined
	// transaction paths return earlier, leaving completion and recovery to their
	// owner.
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

// batchUpdate checks the complete target set before writing supported common
// fields. Missing requested IDs reject the operation. Status or expiry changes
// additionally reject any queried row with positive used count or stored
// used/active status; notes-only changes bypass that guard.
//
// Expiry Set distinguishes omission, clearing and replacement, while other
// attempted entitlement fields are ignored by this adapter and rejected by
// administration validation. The preliminary read does not lock rows, and an
// affected-count mismatch follows the update; the calling transaction must
// undo writes when this result fails. Callers provide deduplicated IDs and the
// transaction consistency they require.
func (r *RedeemStore) batchUpdate(ctx context.Context, client *dbent.Client, ids []int64, fields billing.RedeemCodeBatchUpdateFields) (int64, error) {
	existing, err := client.RedeemCode.Query().
		Where(redeemcode.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return 0, err
	}
	if len(existing) != len(ids) {
		return 0, billing.ErrRedeemCodeNotFound
	}
	// Status or expiry changes require these queried records to remain unused by
	// the captured guard. The read has no row locks, so concurrent redemption
	// needs caller coordination.
	if fields.TouchesUsedSensitiveFields() {
		for _, code := range existing {
			if code.UsedCount > 0 || code.Status == billing.StatusUsed || code.Status == billing.StatusActive {
				return 0, billing.ErrRedeemCodeUsed
			}
		}
	}

	update := client.RedeemCode.Update().Where(redeemcode.IDIn(ids...))
	if fields.Status != nil {
		update.SetStatus(*fields.Status)
	}
	if fields.Notes != nil {
		update.SetNotes(*fields.Notes)
	}
	// Expiry presence distinguishes leaving the column unchanged from clearing
	// it. A present nil Value requests SQL null while a timestamp requests
	// replacement.
	if fields.ExpiresAt.Set {
		if fields.ExpiresAt.Value != nil {
			update.SetExpiresAt(*fields.ExpiresAt.Value)
		} else {
			update.ClearExpiresAt()
		}
	}

	affected, err := update.Save(ctx)
	if err != nil {
		return 0, err
	}
	if affected != len(ids) {
		return 0, billing.ErrRedeemCodeNotFound
	}
	return int64(affected), nil
}

// Delete physically removes the exact redemption row and returns any execution
// failure. A missing row succeeds because affected count is discarded; service
// callers own used-code deletion restrictions and transaction recovery.
func (r *RedeemStore) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.RedeemCode.Delete().Where(redeemcode.IDEQ(id)).Exec(ctx)
	return err
}

// Use records one code use in a selected transaction and propagates its
// persistence result. A context transaction is joined without inner commit or
// rollback; otherwise the adapter creates owned work and commits after useInTx
// succeeds. An already-started base client delegates that joined operation.
//
// Owned failure or commit error triggers deferred rollback, whose cleanup
// result is discarded. useInTx locks and validates the code before inserting
// the per-user usage and updating its summary. This method records use
// metadata; user-facing RedeemService applies entitlement grants through its
// separate transactional workflow.
func (r *RedeemStore) Use(ctx context.Context, id, userID int64) error {
	// Join the caller's transaction directly, so this operation neither commits
	// successful work nor rolls back earlier joined writes when validation fails.
	if dbent.TxFromContext(ctx) != nil {
		return r.useInTx(ctx, id, userID)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		// An already-started base client can supply joined work even without a
		// transaction marker in context. Delegate through its selected client and
		// retain external commit ownership.
		if errors.Is(err, dbent.ErrTxStarted) {
			return r.useInTx(ctx, id, userID)
		}
		return err
	}
	// Keep rollback as owned cleanup for validation, write or commit failure.
	// Discard its secondary error so the operation's primary financial result
	// remains visible.
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := r.useInTx(txCtx, id, userID); err != nil {
		return err
	}
	// Commit only after the usage insert and summary update both succeed. Any
	// commit failure still reaches owned rollback cleanup before this method
	// returns.
	return tx.Commit()
}

// useInTx locks the exact code ID and records one user use on the selected
// transaction client. Missing rows return ErrRedeemCodeNotFound. A nonnil
// converted code must pass CanUse, and any existing record for this code/user
// rejects reuse with ErrRedeemCodeUsed.
//
// The usage row is inserted before incrementing count and replacing the latest
// user/time summary. A recognized unique-constraint failure becomes the same
// used error; other insert failures stop before summary update.
// PersistedStatus selects the updated capacity label, and Update failure
// leaves prior writes for the transaction owner to roll back. Callers
// establish this transaction's lifetime and any entitlement grant ordering.
func (r *RedeemStore) useInTx(ctx context.Context, id, userID int64) error {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return billing.ErrRedeemCodeNotFound
		}
		return err
	}

	code := RedeemFromEntity(model)
	if code == nil || !code.CanUse() {
		return billing.ErrRedeemCodeUsed
	}

	existingUsage, err := r.GetUsageByRedeemCodeAndUser(ctx, id, userID)
	if err != nil {
		return err
	}
	if existingUsage != nil {
		return billing.ErrRedeemCodeUsed
	}

	// Record the per-user usage before changing the aggregate count and
	// latest-user/time summary. Their selected transaction coordinates rollback
	// if either persistence step fails.
	usageTime := time.Now()
	if err := r.CreateUsage(ctx, &billing.RedeemCodeUsage{
		RedeemCodeID: id,
		UserID:       userID,
		UsedAt:       usageTime,
	}); err != nil {
		// A concurrent or repeated per-user usage insert can violate uniqueness.
		// Convert that recognized persistence class into the same used-code result
		// as the earlier read.
		if isUniqueConstraintViolation(err) {
			return billing.ErrRedeemCodeUsed
		}
		return err
	}

	code.UsedCount++
	code.UsedBy = &userID
	code.UsedAt = &usageTime
	// Persist capacity state from the updated count while natural expiry remains
	// a separate read-time calculation. A reusable code stays active until its
	// finite capacity is consumed.
	code.Status = code.PersistedStatus()

	return r.Update(ctx, code)
}

// CreateUsage inserts the supplied nonnil code/user/time record and copies its
// generated ID back on success. It uses the selected context client and
// propagates persistence errors without used-code or entitlement validation.
func (r *RedeemStore) CreateUsage(ctx context.Context, usage *billing.RedeemCodeUsage) error {
	client := clientFromContext(ctx, r.client)
	created, err := client.RedeemCodeUsage.Create().
		SetRedeemCodeID(usage.RedeemCodeID).
		SetUserID(usage.UserID).
		SetUsedAt(usage.UsedAt).
		Save(ctx)
	if err != nil {
		return err
	}
	usage.ID = created.ID
	return nil
}

// GetUsageByRedeemCodeAndUser reads the exact code/user usage pair. Missing
// records return nil with no error for reuse checks; other read failures
// propagate, and loaded optional edges follow the usage conversion rules.
func (r *RedeemStore) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*billing.RedeemCodeUsage, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCodeUsage.Query().
		Where(
			redeemcodeusage.RedeemCodeIDEQ(redeemCodeID),
			redeemcodeusage.UserIDEQ(userID),
		).
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return redeemCodeUsageEntityToService(model), nil
}

// List delegates an unfiltered administrator code page to ListWithFilters
// using the supplied pagination choices. Sorting, eager relations, total
// counting and effective-status conversion follow that method's rules.
func (r *RedeemStore) List(ctx context.Context, params pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, "", "", "")
}

// ListWithFilters counts and fetches an administrator page with optional exact
// type, derived status and case-insensitive search. Nonempty search matches
// credential text or any usage user email; the supplied search is not trimmed
// here. Unknown nonempty status selects an always-false predicate.
//
// Count and page reads are separate operations, and page results load
// latest-user and plan relations. Ordering uses normalized allowed fields or a
// derived status CASE with an ID tie-breaker. SQL filter time and service
// conversion time can differ; notably the expired filter can include disabled
// rows whose displayed status remains disabled. Callers own permission and
// search-size checks.
func (r *RedeemStore) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	query := client.RedeemCode.Query()

	if codeType != "" {
		query = query.Where(redeemcode.TypeEQ(codeType))
	}
	if status != "" {
		query = query.Where(redeemCodeEffectiveStatusPredicate(status))
	}
	if search != "" {
		query = query.Where(
			redeemcode.Or(
				redeemcode.CodeContainsFold(search),
				redeemcode.HasUsageRecordsWith(redeemcodeusage.HasUserWith(user.EmailContainsFold(search))),
			),
		)
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	codesQuery := query.
		WithUser().
		WithPlan().
		Offset(params.Offset()).
		Limit(params.Limit())
	for _, order := range redeemCodeListOrder(params) {
		codesQuery = codesQuery.Order(order)
	}

	models, err := codesQuery.All(ctx)
	if err != nil {
		return nil, nil, err
	}

	return redeemCodeEntitiesToService(models), paginationResultFromTotal(int64(total), params), nil
}

// redeemCodeEffectiveStatusPredicate builds a selector predicate for one
// requested derived listing state. Disabled matches stored disabled directly,
// expired accepts stored expiry or an elapsed deadline, and unknown labels
// select no rows. The expired branch does not exclude disabled records.
//
// Used, active and unused predicates first require availability, excluding
// expiry and disabled. Positive finite capacity exhausted by used count
// selects used; positive count with remaining or zero-capacity unlimited use
// selects active; zero count selects unused. These filters use SQL NOW and
// field comparisons, independently of the later display-status conversion and
// its disabled-first precedence.
//
// SQL uses stored MaxUses directly; negative maxima stay unchanged here, while
// RedeemCode.MaxUsesOrDefault would select a single-use capacity.
func redeemCodeEffectiveStatusPredicate(status string) dbpredicate.RedeemCode {
	return dbpredicate.RedeemCode(func(s *entsql.Selector) {
		usedCountCol := s.C(redeemcode.FieldUsedCount)
		maxUsesCol := s.C(redeemcode.FieldMaxUses)
		switch status {
		case billing.StatusExpired:
			s.Where(redeemCodeEffectiveExpiredPredicate(s))
		case billing.StatusDisabled:
			s.Where(entsql.EQ(s.C(redeemcode.FieldStatus), billing.StatusDisabled))
		case billing.StatusUsed:
			s.Where(entsql.And(
				redeemCodeEffectiveAvailablePredicate(s),
				entsql.GT(maxUsesCol, 0),
				entsql.ColumnsGTE(usedCountCol, maxUsesCol),
			))
		case billing.StatusActive:
			s.Where(entsql.And(
				redeemCodeEffectiveAvailablePredicate(s),
				entsql.GT(usedCountCol, 0),
				entsql.Or(
					entsql.EQ(maxUsesCol, 0),
					entsql.ColumnsLT(usedCountCol, maxUsesCol),
				),
			))
		case billing.StatusUnused:
			s.Where(entsql.And(
				redeemCodeEffectiveAvailablePredicate(s),
				entsql.EQ(usedCountCol, 0),
			))
		default:
			s.Where(entsql.False())
		}
	})
}

// redeemCodeEffectiveStatusExpr returns the SQL CASE used to order derived
// redemption status. Stored disabled wins, then stored or deadline expiry,
// then finite exhausted capacity, then positive use count, with unused as the
// remaining label.
//
// Column names come from the supplied selector so the expression fits its
// query aliases. SQL NOW supplies the time observation, while entity
// conversion later observes its own current time. The sorting expression
// excludes no records and differs from the expired filter's inclusion rule for
// disabled rows; list ordering adds the requested ID tie-breaker separately.
//
// SQL uses stored MaxUses directly; negative maxima stay unchanged here, while
// RedeemCode.MaxUsesOrDefault would select a single-use capacity.
func redeemCodeEffectiveStatusExpr(s *entsql.Selector) string {
	statusCol := s.C(redeemcode.FieldStatus)
	expiresAtCol := s.C(redeemcode.FieldExpiresAt)
	usedCountCol := s.C(redeemcode.FieldUsedCount)
	maxUsesCol := s.C(redeemcode.FieldMaxUses)

	return fmt.Sprintf(
		"CASE "+
			"WHEN %s = '%s' THEN '%s' "+
			"WHEN %s = '%s' OR (%s IS NOT NULL AND %s <= NOW()) THEN '%s' "+
			"WHEN %s > 0 AND %s >= %s THEN '%s' "+
			"WHEN %s > 0 THEN '%s' "+
			"ELSE '%s' END",
		statusCol,
		billing.StatusDisabled,
		billing.StatusDisabled,
		statusCol,
		billing.StatusExpired,
		expiresAtCol,
		expiresAtCol,
		billing.StatusExpired,
		maxUsesCol,
		usedCountCol,
		maxUsesCol,
		billing.StatusUsed,
		usedCountCol,
		billing.StatusActive,
		billing.StatusUnused,
	)
}

// redeemCodeEffectiveExpiredPredicate accepts stored expired status or a
// nonnull deadline at or before SQL NOW. It leaves disabled records eligible
// for this predicate, matching the expiry question rather than disabled-first
// display precedence.
func redeemCodeEffectiveExpiredPredicate(s *entsql.Selector) *entsql.Predicate {
	statusCol := s.C(redeemcode.FieldStatus)
	expiresAtCol := s.C(redeemcode.FieldExpiresAt)
	return entsql.Or(
		entsql.EQ(statusCol, billing.StatusExpired),
		entsql.And(
			entsql.Not(entsql.IsNull(expiresAtCol)),
			entsql.LTE(expiresAtCol, entsql.Expr("NOW()")),
		),
	)
}

// redeemCodeEffectiveAvailablePredicate excludes SQL expiry and stored
// disabled from candidate rows. Derived usage-state filters combine this
// availability predicate with their count/capacity checks; it performs no
// credential-type or per-user reuse validation.
func redeemCodeEffectiveAvailablePredicate(s *entsql.Selector) *entsql.Predicate {
	return entsql.And(
		entsql.Not(redeemCodeEffectiveExpiredPredicate(s)),
		entsql.NEQ(s.C(redeemcode.FieldStatus), billing.StatusDisabled),
	)
}

// redeemCodeListOrder returns allowed normalized field ordering or the
// derived-status CASE, with ID ordered in the same direction as a tie-breaker.
// Unknown fields default to ID and invalid directions to descending; input
// normalization trims and lowercases.
func redeemCodeListOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := params.NormalizedSortOrder(pagination.SortOrderDesc)

	if sortBy == "status" {
		if sortOrder == pagination.SortOrderAsc {
			return []func(*entsql.Selector){
				func(s *entsql.Selector) {
					s.OrderExpr(entsql.Expr(redeemCodeEffectiveStatusExpr(s)))
				},
				dbent.Asc(redeemcode.FieldID),
			}
		}
		return []func(*entsql.Selector){
			func(s *entsql.Selector) {
				s.OrderExpr(entsql.DescExpr(entsql.Expr(redeemCodeEffectiveStatusExpr(s))))
			},
			dbent.Desc(redeemcode.FieldID),
		}
	}

	var field string
	switch sortBy {
	case "type":
		field = redeemcode.FieldType
	case "value":
		field = redeemcode.FieldValue
	case "used_at":
		field = redeemcode.FieldUsedAt
	case "used_count":
		field = redeemcode.FieldUsedCount
	case "max_uses":
		field = redeemcode.FieldMaxUses
	case "expires_at":
		field = redeemcode.FieldExpiresAt
	case "created_at":
		field = redeemcode.FieldCreatedAt
	case "code":
		field = redeemcode.FieldCode
	default:
		field = redeemcode.FieldID
	}

	if sortOrder == pagination.SortOrderAsc {
		return []func(*entsql.Selector){dbent.Asc(field), dbent.Asc(redeemcode.FieldID)}
	}
	return []func(*entsql.Selector){dbent.Desc(field), dbent.Desc(redeemcode.FieldID)}
}

// ListByUserPaginated counts and pages one user's usage records, optionally
// restricting their code type. Newest use/time and usage ID sort descending;
// returned code-shaped history uses each usage's user/time and loads
// associated plan data.
func (r *RedeemStore) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	preds := []dbpredicate.RedeemCodeUsage{
		redeemcodeusage.UserIDEQ(userID),
	}
	if codeType != "" {
		preds = append(preds, redeemcodeusage.HasRedeemCodeWith(redeemcode.TypeEQ(codeType)))
	}

	total, err := client.RedeemCodeUsage.Query().
		Where(preds...).
		Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	usages, err := client.RedeemCodeUsage.Query().
		Where(preds...).
		WithRedeemCode(func(q *dbent.RedeemCodeQuery) {
			q.WithPlan()
		}).
		Offset(params.Offset()).
		Limit(params.Limit()).
		Order(dbent.Desc(redeemcodeusage.FieldUsedAt), dbent.Desc(redeemcodeusage.FieldID)).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	return redeemCodeHistoryFromUsageEntities(usages), paginationResultFromTotal(int64(total), params), nil
}

// SumPositiveBalanceByUser sums current positive balance or
// administrator-balance code values reached through the user's usage records.
// Query errors propagate, an absent scanned row yields zero, and neither
// disabled status nor later expiry is filtered.
func (r *RedeemStore) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	client := clientFromContext(ctx, r.client)
	var result []struct {
		Sum float64 `json:"sum"` // positive recorded code values USD
	}

	err := client.RedeemCodeUsage.Query().
		Where(
			redeemcodeusage.UserIDEQ(userID),
			redeemcodeusage.HasRedeemCodeWith(
				redeemcode.ValueGT(0),
				redeemcode.TypeIn(billing.RedeemTypeBalance, billing.AdjustmentTypeAdminBalance, billing.AdjustmentTypePluginBalance),
			),
		).
		QueryRedeemCode().
		Aggregate(dbent.As(dbent.Sum(redeemcode.FieldValue), "sum")).
		Scan(ctx, &result)
	if err != nil {
		return 0, err
	}
	if len(result) == 0 {
		return 0, nil
	}
	return result[0].Sum, nil
}

// RedeemFromEntity converts a nullable Ent code and loaded relations into a
// service record. Nil input returns nil. Optional expiry, latest-user/time and
// plan-ID pointers are retained, while loaded user summaries, plans and usage
// records use their conversion helpers.
//
// After copying fields, EffectiveStatus replaces stored status using current
// expiry and capacity with disabled precedence. The converted record has
// already lost that raw stored label; useInTx checks CanUse before choosing
// its updated capacity label. Relation graphs remain caller-controlled and
// conversion follows loaded edges recursively. Retained optional data requires
// copying before independent mutation.
func RedeemFromEntity(model *dbent.RedeemCode) *billing.RedeemCode {
	if model == nil {
		return nil
	}
	out := &billing.RedeemCode{
		ID:              model.ID,
		Code:            model.Code,
		Type:            model.Type,
		Value:           model.Value,
		Status:          model.Status,
		RequiresPayment: model.RequiresPayment,
		MaxUses:         model.MaxUses,
		UsedCount:       model.UsedCount,
		ExpiresAt:       model.ExpiresAt,
		UsedBy:          model.UsedBy,
		UsedAt:          model.UsedAt,
		Notes:           derefString(model.Notes),
		CreatedAt:       model.CreatedAt,
		PlanID:          model.PlanID,
		ValidityDays:    model.ValidityDays,
	}
	if model.Edges.User != nil {
		out.User = userSummaryFromEntity(model.Edges.User)
	}
	if model.Edges.Plan != nil {
		out.Plan = PlanFromEntity(model.Edges.Plan)
	}
	if len(model.Edges.UsageRecords) > 0 {
		out.UsageRecords = redeemCodeUsageEntitiesToService(model.Edges.UsageRecords)
	}
	// Derive current display status after all copied usage and expiry fields are
	// available. Persistent usage updates separately recompute the stored
	// capacity label.
	out.Status = out.EffectiveStatus()
	return out
}

// redeemCodeEntitiesToService returns a fresh outer slice of converted nonnil
// code entities, skipping nil entries. Nested optional data follows
// RedeemFromEntity's sharing rules, and empty input still produces an
// allocated empty slice.
func redeemCodeEntitiesToService(models []*dbent.RedeemCode) []billing.RedeemCode {
	out := make([]billing.RedeemCode, 0, len(models))
	for i := range models {
		if code := RedeemFromEntity(models[i]); code != nil {
			out = append(out, *code)
		}
	}
	return out
}

// redeemCodeUsageEntityToService converts a nullable usage row with any loaded
// user or code relations. Nil input yields nil; nested code conversion follows
// its loaded edges and retains optional pointers under RedeemFromEntity's
// rules.
func redeemCodeUsageEntityToService(model *dbent.RedeemCodeUsage) *billing.RedeemCodeUsage {
	if model == nil {
		return nil
	}
	out := &billing.RedeemCodeUsage{
		ID:           model.ID,
		RedeemCodeID: model.RedeemCodeID,
		UserID:       model.UserID,
		UsedAt:       model.UsedAt,
	}
	if model.Edges.User != nil {
		out.User = userSummaryFromEntity(model.Edges.User)
	}
	if model.Edges.RedeemCode != nil {
		out.RedeemCode = RedeemFromEntity(model.Edges.RedeemCode)
	}
	return out
}

// redeemCodeUsageEntitiesToService returns a fresh outer slice of converted
// nonnil usage entities, skipping nil rows. Loaded relations follow the
// individual converter's ownership rules, and empty input still yields an
// allocated empty slice.
func redeemCodeUsageEntitiesToService(models []*dbent.RedeemCodeUsage) []billing.RedeemCodeUsage {
	out := make([]billing.RedeemCodeUsage, 0, len(models))
	for i := range models {
		if usage := redeemCodeUsageEntityToService(models[i]); usage != nil {
			out = append(out, *usage)
		}
	}
	return out
}

// redeemCodeHistoryFromUsageEntities returns code-shaped history tied to each
// usage's actor and time. Nil usage rows and missing code edges are skipped.
// Each code is converted first, then its latest-user/time summary pointers are
// replaced with separately addressed values from that usage record.
//
// This keeps two users or times using the same multi-use credential from being
// displayed as its most recent redemption. Other optional code fields and
// loaded relations retain their conversion sharing, and output follows input
// usage order. A fresh outer slice is returned even when every record is
// skipped; status continues to reflect current code state rather than its
// historical state at use.
func redeemCodeHistoryFromUsageEntities(models []*dbent.RedeemCodeUsage) []billing.RedeemCode {
	out := make([]billing.RedeemCode, 0, len(models))
	for i := range models {
		usage := models[i]
		if usage == nil || usage.Edges.RedeemCode == nil {
			continue
		}
		code := RedeemFromEntity(usage.Edges.RedeemCode)
		if code == nil {
			continue
		}
		// Replace the code summary with this usage's identity and timestamp. Each
		// loop iteration gets separate pointer storage so multi-use history remains
		// tied to its record.
		usedBy := usage.UserID
		usedAt := usage.UsedAt
		code.UsedBy = &usedBy
		code.UsedAt = &usedAt
		out = append(out, *code)
	}
	return out
}
