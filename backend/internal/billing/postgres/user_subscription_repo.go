package postgres

import (
	"context"
	"maps"
	"time"

	"github.com/lib/pq"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// user_subscription_repo.go persists subscription grants and queries their
// effective status and group coverage.
// Grant rows retain issued lifetime and quota snapshots, while plan mappings
// determine which current groups those grants cover. Ordinary Ent reads filter
// soft deletion; restoration and administrator revocation history deliberately
// read through that filter. Display conversion can map a deleted row to
// revoked while a restoration read retains its stored status.
//
// Service transactions choose the context client and own locks before
// mutations. Conditional quota resets compare the expected anchor and can
// succeed without changing a stale row, requiring admission to reread usage.
// Unconditional metadata and counter updates rely on the caller's eligibility
// checks. Plan conversion detaches group collections but retains optional
// pointers, and history relation attachment shares converted objects across
// records with the same relation identity.

// The repository assignment checks SubscriptionStore's complete grant
// persistence method set against billing.UserSubscriptionRepository during
// compilation, keeping service repository requirements visible to this
// adapter.
var _ billing.UserSubscriptionRepository = (*SubscriptionStore)(nil)

// SubscriptionStore persists grant lifetimes and quota counters through Ent
// and selected raw SQL. Each call selects a context transaction before the
// retained base client; service callers own transaction completion and locks.
type SubscriptionStore struct {
	// client is retained for later calls. A context-carried Ent transaction takes
	// precedence; callers provide a usable binding before methods dereference its
	// repositories.
	client *dbent.Client // base Ent grant client
}

// NewUserSubscriptionRepository retains the supplied Ent client for later
// grant operations. Construction opens no transaction; subsequent calls need
// that client or a usable Ent transaction carried by context.
func NewUserSubscriptionRepository(client *dbent.Client) *SubscriptionStore {
	return &SubscriptionStore{client: client}
}

// Create inserts the supplied nonnil grant and copies its generated ID and
// timestamps back on success. Zero StartsAt uses current time, while empty
// status and zero assignment time use schema defaults; optional nil fields
// remain unset.
func (r *SubscriptionStore) Create(ctx context.Context, sub *billing.UserSubscription) error {
	if sub == nil {
		return billing.ErrSubscriptionNilInput
	}

	client := clientFromContext(ctx, r.client)
	builder := client.UserSubscription.Create().
		SetUserID(sub.UserID).
		SetPlanID(sub.PlanID).
		SetExpiresAt(sub.ExpiresAt).
		SetNillableDailyWindowStart(sub.DailyWindowStart).
		SetNillableWeeklyWindowStart(sub.WeeklyWindowStart).
		SetNillableMonthlyWindowStart(sub.MonthlyWindowStart).
		SetNillableDailyLimitUsd(sub.DailyLimitUSD).
		SetNillableWeeklyLimitUsd(sub.WeeklyLimitUSD).
		SetNillableMonthlyLimitUsd(sub.MonthlyLimitUSD).
		SetDailyUsageUsd(sub.DailyUsageUSD).
		SetWeeklyUsageUsd(sub.WeeklyUsageUSD).
		SetMonthlyUsageUsd(sub.MonthlyUsageUSD).
		SetNillableAssignedBy(sub.AssignedBy).
		SetNillableSourceOrderID(sub.SourceOrderID)

	if sub.StartsAt.IsZero() {
		builder.SetStartsAt(time.Now())
	} else {
		builder.SetStartsAt(sub.StartsAt)
	}
	if sub.Status != "" {
		builder.SetStatus(sub.Status)
	}
	if !sub.AssignedAt.IsZero() {
		builder.SetAssignedAt(sub.AssignedAt)
	}
	builder.SetNotes(sub.Notes)

	created, err := builder.Save(ctx)
	if err == nil {
		applyUserSubscriptionEntityToService(sub, created)
	}
	return translatePersistenceError(err, nil, billing.ErrSubscriptionAlreadyExists)
}

// GetByID reads one grant with its user, plan and assigning-user relations.
// Ordinary soft-delete filtering applies unless the caller bypasses it;
// missing rows translate to ErrSubscriptionNotFound and loaded entity data is
// converted to a service record.
func (r *SubscriptionStore) GetByID(ctx context.Context, id int64) (*billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		WithUser().
		WithPlan().
		WithAssignedByUser().
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	return SubscriptionFromEntity(m), nil
}

// GetByIDIncludeDeleted reads one grant and its display relations while
// bypassing subscription soft deletion. Stored status remains available for
// restoration, and missing rows translate to ErrSubscriptionNotFound; the
// caller owns restoration eligibility checks.
func (r *SubscriptionStore) GetByIDIncludeDeleted(ctx context.Context, id int64) (*billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	// Bypass deletion visibility for this restoration read or write while
	// retaining the context's transaction. Stored status must remain available
	// until the caller selects its restored value.
	queryCtx := mixins.SkipSoftDelete(ctx)
	m, err := client.UserSubscription.Query().
		Where(usersubscription.IDEQ(id)).
		WithUser().
		WithPlan().
		WithAssignedByUser().
		Only(queryCtx)
	if err != nil {
		return nil, translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	return SubscriptionFromEntityPreserveStatus(m), nil
}

// GetLatestByUserIDAndPlanID returns the first visible matching grant by
// expiry descending and creation time descending. It loads the plan but
// performs no active-status or quota filtering; exact ordering ties have no
// added ID comparison.
func (r *SubscriptionStore) GetLatestByUserIDAndPlanID(ctx context.Context, userID, planID int64) (*billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	m, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.PlanIDEQ(planID),
		).
		WithPlan().
		Order(
			dbent.Desc(usersubscription.FieldExpiresAt),
			dbent.Desc(usersubscription.FieldCreatedAt),
		).
		First(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	return SubscriptionFromEntity(m), nil
}

// Update writes the supplied nonnil grant's owner, plan, lifetime, status and
// scalar counters, then refreshes ID and timestamps. Nil optional anchors,
// limits and identities leave their stored columns unchanged because nillable
// setters skip nil; callers handle nullable clearing separately.
func (r *SubscriptionStore) Update(ctx context.Context, sub *billing.UserSubscription) error {
	if sub == nil {
		return billing.ErrSubscriptionNilInput
	}

	client := clientFromContext(ctx, r.client)
	builder := client.UserSubscription.UpdateOneID(sub.ID).
		SetUserID(sub.UserID).
		SetPlanID(sub.PlanID).
		SetStartsAt(sub.StartsAt).
		SetExpiresAt(sub.ExpiresAt).
		SetStatus(sub.Status).
		SetNillableDailyWindowStart(sub.DailyWindowStart).
		SetNillableWeeklyWindowStart(sub.WeeklyWindowStart).
		SetNillableMonthlyWindowStart(sub.MonthlyWindowStart).
		SetNillableDailyLimitUsd(sub.DailyLimitUSD).
		SetNillableWeeklyLimitUsd(sub.WeeklyLimitUSD).
		SetNillableMonthlyLimitUsd(sub.MonthlyLimitUSD).
		SetDailyUsageUsd(sub.DailyUsageUSD).
		SetWeeklyUsageUsd(sub.WeeklyUsageUSD).
		SetMonthlyUsageUsd(sub.MonthlyUsageUSD).
		SetNillableAssignedBy(sub.AssignedBy).
		SetAssignedAt(sub.AssignedAt).
		SetNillableSourceOrderID(sub.SourceOrderID).
		SetNotes(sub.Notes)

	updated, err := builder.Save(ctx)
	if err == nil {
		applyUserSubscriptionEntityToService(sub, updated)
		return nil
	}
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, billing.ErrSubscriptionAlreadyExists)
}

// Delete normally marks a visible grant deleted through its schema hook,
// preserving an existing deletion timestamp. A SkipSoftDelete context
// physically removes the matching row instead; missing IDs succeed because
// affected count is discarded.
func (r *SubscriptionStore) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.Delete().Where(usersubscription.IDEQ(id)).Exec(ctx)
	return err
}

// Restore clears the deletion marker and writes the caller-selected stored
// status, then rereads the grant with ordinary relation loading. The caller
// establishes that restoration is eligible and supplies a transaction when its
// write and reread need rollback together.
//
// The write bypasses soft-delete filtering and stamps current update time.
// Persistence errors retain their translated missing/conflict classification;
// a later reread error can follow an already applied write when no caller
// transaction coordinates recovery. No overlap or time-chain check is
// performed inside this adapter method.
func (r *SubscriptionStore) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	// Carry the deletion-bypass marker into restoration while retaining
	// transaction binding. This update writes the supplied restored status and
	// clears deletion before the follow-up relation read.
	queryCtx := mixins.SkipSoftDelete(ctx)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetStatus(restoredStatus).
		ClearDeletedAt().
		SetUpdatedAt(time.Now()).
		Save(queryCtx)
	if err != nil {
		return nil, translatePersistenceError(err, billing.ErrSubscriptionNotFound, billing.ErrSubscriptionAlreadyExists)
	}
	return r.GetByID(ctx, subscriptionID)
}

// ListByUserID returns visible user grants with loaded plans, ordered by
// expiry then creation time descending. It does not filter stored status or
// available quota; returned records follow entity conversion's nested-sharing
// rules.
func (r *SubscriptionStore) ListByUserID(ctx context.Context, userID int64) ([]billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(usersubscription.UserIDEQ(userID)).
		WithPlan().
		Order(
			dbent.Desc(usersubscription.FieldExpiresAt),
			dbent.Desc(usersubscription.FieldCreatedAt),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

// ListByUserIDAndPlanID returns the visible user-plan timeline with loaded
// plans. StartsAt and creation time sort ascending without an ID tie-breaker;
// the caller selects the portion to mutate under its transaction locks.
func (r *SubscriptionStore) ListByUserIDAndPlanID(ctx context.Context, userID, planID int64) ([]billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.PlanIDEQ(planID),
		).
		WithPlan().
		Order(
			dbent.Asc(usersubscription.FieldStartsAt),
			dbent.Asc(usersubscription.FieldCreatedAt),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

// ListActiveByUserID returns visible, unheld grants whose lifetime contains one
// current-time observation and whose stored status is active or pending.
// Expiry and start sort ascending; available quota and final-group coverage
// are checked by service consumers.
func (r *SubscriptionStore) ListActiveByUserID(ctx context.Context, userID int64) ([]billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.StartsAtLTE(now),
			usersubscription.ExpiresAtGT(now),
			usersubscription.StatusIn(billing.SubscriptionStatusActive, billing.SubscriptionStatusPending),
			usersubscription.RefundHoldOperationIDIsNil(),
		).
		WithPlan().
		Order(
			dbent.Asc(usersubscription.FieldExpiresAt),
			dbent.Asc(usersubscription.FieldStartsAt),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

// FilterByGroup keeps input grants whose current plan mapping admits the
// positive group, preserving supplied grant order. Nonpositive group IDs or
// empty inputs return the input slice directly. Positive filtering uses
// relational plan-group rows, accepting plans with no restrictions or the
// requested mapping.
//
// Hydrated Plan.GroupIDs are not consulted, and missing plan rows remain
// absent from the allowed set. Successful filtering returns fresh outer slice
// storage with nested grant values still shared. Scan and iteration errors
// close the result best-effort; successful reads propagate a Close failure
// before publishing a filtered result. Eligibility and transaction locking
// remain with the caller.
func (r *SubscriptionStore) FilterByGroup(ctx context.Context, subs []billing.UserSubscription, groupID int64) ([]billing.UserSubscription, error) {
	if groupID <= 0 || len(subs) == 0 {
		return subs, nil
	}
	planIDs := make([]int64, 0, len(subs))
	for i := range subs {
		planIDs = append(planIDs, subs[i].PlanID)
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
		SELECT sp.id
		FROM subscription_plans sp
		WHERE sp.id = ANY($1)
			AND (
				NOT EXISTS (
					SELECT 1
					FROM subscription_plan_groups spg
					WHERE spg.plan_id = sp.id
				)
				OR EXISTS (
					SELECT 1
					FROM subscription_plan_groups spg
					WHERE spg.plan_id = sp.id
						AND spg.group_id = $2
				)
			)
	`, pq.Array(planIDs), groupID)
	if err != nil {
		return nil, err
	}

	// Collect allowed plan identities before constructing the returned slice.
	// Scan or iteration failure closes this result best-effort and keeps its
	// primary error ahead of cleanup.
	allowed := make(map[int64]struct{}, len(planIDs))
	for rows.Next() {
		var planID int64
		if err := rows.Scan(&planID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		allowed[planID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// Finish reading and close the SQL result before publishing filtered grants.
	// After a successful scan, a Close error remains an operation failure for the
	// caller.
	if err := rows.Close(); err != nil {
		return nil, err
	}

	out := make([]billing.UserSubscription, 0, len(subs))
	for i := range subs {
		if _, ok := allowed[subs[i].PlanID]; ok {
			out = append(out, subs[i])
		}
	}
	return out, nil
}

// ListByPlanID counts visible plan grants, then separately fetches the
// requested page with users and plans. Creation time sorts descending; no
// snapshot or additional ID order is added, and page helpers normalize the
// supplied limits.
func (r *SubscriptionStore) ListByPlanID(ctx context.Context, planID int64, params pagination.PaginationParams) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	q := client.UserSubscription.Query().Where(usersubscription.PlanIDEQ(planID))

	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	subs, err := q.
		WithUser().
		WithPlan().
		Order(dbent.Desc(usersubscription.FieldCreatedAt)).
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(ctx)
	if err != nil {
		return nil, nil, err
	}

	return userSubscriptionEntitiesToService(subs), paginationResultFromTotal(int64(total), params), nil
}

// List returns a counted page of grants selected by optional owner, plan and
// stored/time-derived status filters. Empty status and revoked history bypass
// soft deletion; other filters use ordinary queries. The expired filter
// accepts stored expired status or an elapsed deadline, while revoked means a present
// deletion marker.
//
// Count and page reads are separate operations. History pages load
// subscription rows first, then attach user, plan and assigning-user data
// through ordinary relation queries; an attachment error rejects the page.
// Sorting accepts selected field names, with ascending used only for literal
// asc and a nonempty sort field. Platform is ignored, and service consumers
// separately normalize display status and windows.
func (r *SubscriptionStore) List(ctx context.Context, params pagination.PaginationParams, userID, planID *int64, status, _platform, sortBy, sortOrder string) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	queryCtx := ctx
	if status == "" || status == billing.SubscriptionStatusRevoked {
		// Include deleted grant rows when the administrator asks for all history or
		// revoked records. Relations are loaded later through ordinary queries so
		// their visibility stays independent.
		queryCtx = mixins.SkipSoftDelete(ctx)
	}
	q := client.UserSubscription.Query()
	if userID != nil {
		q = q.Where(usersubscription.UserIDEQ(*userID))
	}
	if planID != nil {
		q = q.Where(usersubscription.PlanIDEQ(*planID))
	}

	now := time.Now()
	switch status {
	case billing.SubscriptionStatusActive:
		q = q.Where(
			usersubscription.StartsAtLTE(now),
			usersubscription.ExpiresAtGT(now),
			usersubscription.StatusIn(billing.SubscriptionStatusActive, billing.SubscriptionStatusPending),
			usersubscription.RefundHoldOperationIDIsNil(),
		)
	case billing.SubscriptionStatusPending:
		q = q.Where(
			usersubscription.StatusEQ(billing.SubscriptionStatusPending),
			usersubscription.RefundHoldOperationIDIsNil(),
			usersubscription.StartsAtGT(now),
			usersubscription.ExpiresAtGT(now),
		)
	case billing.SubscriptionStatusExpired:
		q = q.Where(
			usersubscription.Or(
				usersubscription.StatusEQ(billing.SubscriptionStatusExpired),
				usersubscription.ExpiresAtLTE(now),
			),
		)
	case billing.SubscriptionStatusSuspended:
		q = q.Where(usersubscription.Or(
			usersubscription.StatusEQ(billing.SubscriptionStatusSuspended),
			usersubscription.And(usersubscription.RefundHoldOperationIDNotNil(), usersubscription.ExpiresAtGT(now)),
		))
	case billing.SubscriptionStatusRevoked:
		q = q.Where(usersubscription.DeletedAtNotNil())
	case "":
	default:
		q = q.Where(usersubscription.StatusEQ(status))
	}

	total, err := q.Clone().Count(queryCtx)
	if err != nil {
		return nil, nil, err
	}

	if status != "" && status != billing.SubscriptionStatusRevoked {
		q = q.WithUser().WithPlan().WithAssignedByUser()
	}

	var field string
	switch sortBy {
	case "expires_at":
		field = usersubscription.FieldExpiresAt
	case "starts_at":
		field = usersubscription.FieldStartsAt
	case "status":
		field = usersubscription.FieldStatus
	default:
		field = usersubscription.FieldCreatedAt
	}

	if sortOrder == "asc" && sortBy != "" {
		q = q.Order(dbent.Asc(field))
	} else {
		q = q.Order(dbent.Desc(field))
	}

	subs, err := q.
		Offset(params.Offset()).
		Limit(params.Limit()).
		All(queryCtx)
	if err != nil {
		return nil, nil, err
	}

	result := userSubscriptionEntitiesToService(subs)
	if status == "" || status == billing.SubscriptionStatusRevoked {
		if err := r.attachUserSubscriptionRelations(ctx, result); err != nil {
			return nil, nil, err
		}
	}

	return result, paginationResultFromTotal(int64(total), params), nil
}

// ListBySourceOrderID returns exact source-order matches with plans, ordered
// by start and creation time ascending. It applies no owner or plan
// restriction; assignment replay uses the first record before template
// validation.
func (r *SubscriptionStore) ListBySourceOrderID(ctx context.Context, sourceOrderID int64) ([]billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(usersubscription.SourceOrderIDEQ(sourceOrderID)).
		WithPlan().
		Order(
			dbent.Asc(usersubscription.FieldStartsAt),
			dbent.Asc(usersubscription.FieldCreatedAt),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

// ExtendExpiry writes the supplied expiry for one grant ID through the
// selected client. Missing records translate to ErrSubscriptionNotFound; the
// caller owns validity checks, time-chain coordination and transaction
// recovery.
func (r *SubscriptionStore) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetExpiresAt(newExpiresAt).
		Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

// UpdateStatus writes the supplied stored status for one grant ID through the
// selected client. Missing records translate to ErrSubscriptionNotFound; this
// adapter performs no transition validation or effective-time calculation.
func (r *SubscriptionStore) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetStatus(status).
		Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

// UpdateNotes writes the supplied notes for one grant ID through the selected
// client. Missing records translate to ErrSubscriptionNotFound; transaction
// lifetime and any permission checks belong to the service caller.
func (r *SubscriptionStore) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetNotes(notes).
		Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

// ActivateWindows writes the same supplied start into selected quota anchors
// and leaves counters unchanged. No selected flag returns immediately; updates
// are unconditional for those anchors, so callers establish activation
// eligibility and synchronize concurrent maintenance.
func (r *SubscriptionStore) ActivateWindows(ctx context.Context, id int64, start time.Time, activation billing.SubscriptionWindowActivation) error {
	if !activation.Any() {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	update := client.UserSubscription.UpdateOneID(id)
	if activation.Daily {
		update.SetDailyWindowStart(start)
	}
	if activation.Weekly {
		update.SetWeeklyWindowStart(start)
	}
	if activation.Monthly {
		update.SetMonthlyWindowStart(start)
	}
	_, err := update.Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

// ResetUsageWindows writes selected anchors and clears their usage counters in
// one Ent update. All-false flags still execute the update builder;
// administrators validate selection first, and caller locks coordinate this
// unconditional reset with consumption.
func (r *SubscriptionStore) ResetUsageWindows(ctx context.Context, id int64, resetDaily, resetWeekly, resetMonthly bool, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	update := client.UserSubscription.UpdateOneID(id)
	if resetDaily {
		update.SetDailyUsageUsd(0).SetDailyWindowStart(newWindowStart)
	}
	if resetWeekly {
		update.SetWeeklyUsageUsd(0).SetWeeklyWindowStart(newWindowStart)
	}
	if resetMonthly {
		update.SetMonthlyUsageUsd(0).SetMonthlyWindowStart(newWindowStart)
	}
	_, err := update.Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

// ResetDailyUsage clears daily usage and advances its anchor when the stored
// anchor matches the expected pointer value. Nil expects a SQL-null anchor; a
// stale mismatch on an existing visible grant returns success, so admission
// callers reread storage.
func (r *SubscriptionStore) ResetDailyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.DailyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.DailyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetDailyUsageUsd(0).
		SetDailyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

// ResetWeeklyUsage clears weekly usage and advances its anchor when the stored
// anchor matches the expected pointer value. Nil expects a SQL-null anchor; a
// stale mismatch on an existing visible grant returns success, so admission
// callers reread storage.
func (r *SubscriptionStore) ResetWeeklyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.WeeklyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.WeeklyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetWeeklyUsageUsd(0).
		SetWeeklyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

// ResetMonthlyUsage clears monthly usage and advances its anchor when the
// stored anchor matches the expected pointer value. Nil expects a SQL-null
// anchor; a stale mismatch on an existing visible grant returns success, so
// admission callers reread storage.
func (r *SubscriptionStore) ResetMonthlyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	client := clientFromContext(ctx, r.client)
	query := client.UserSubscription.Update().Where(usersubscription.IDEQ(id))
	if expectedWindowStart == nil {
		query = query.Where(usersubscription.MonthlyWindowStartIsNil())
	} else {
		query = query.Where(usersubscription.MonthlyWindowStartEQ(*expectedWindowStart))
	}
	n, err := query.
		SetMonthlyUsageUsd(0).
		SetMonthlyWindowStart(newWindowStart).
		Save(ctx)
	return r.translateConditionalWindowReset(ctx, client, id, n, err)
}

// translateConditionalWindowReset interprets the affected count after a
// conditional quota reset. An execution error is translated first; any
// positive count succeeds. Zero affected rows trigger a visible-grant
// existence read to distinguish a stale expected anchor from a missing grant.
//
// A grant that still exists makes the no-op successful, even though this
// request did not clear stored counters. Service maintenance can locally clear
// its snapshot after that success, so EnsureWindowMaintenance rereads before
// admission. The existence read is a later observation, and caller transaction
// isolation and locking govern changes between the reset and that lookup.
func (r *SubscriptionStore) translateConditionalWindowReset(ctx context.Context, client *dbent.Client, id int64, affected int, err error) error {
	if err != nil {
		return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	if affected > 0 {
		return nil
	}

	// The expected anchor can lose to an earlier reset, making zero affected rows
	// successful while stored counters remain untouched. Distinguish that case
	// with a visible-row existence read.
	exists, err := client.UserSubscription.Query().Where(usersubscription.IDEQ(id)).Exist(ctx)
	if err != nil {
		return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	if !exists {
		return billing.ErrSubscriptionNotFound
	}
	return nil
}

// IncrementUsage adds the supplied amount to all three counters of an
// undeleted grant using one SQL statement. It does not test enabled limits or
// validate the amount's sign; missing rows and affected-count failures
// propagate to callers.
func (r *SubscriptionStore) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	const updateSQL = `
		UPDATE user_subscriptions
		SET
			daily_usage_usd = daily_usage_usd + $1,
			weekly_usage_usd = weekly_usage_usd + $1,
			monthly_usage_usd = monthly_usage_usd + $1,
			updated_at = NOW()
		WHERE id = $2
			AND deleted_at IS NULL
	`

	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(ctx, updateSQL, costUSD, id)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return billing.ErrSubscriptionNotFound
}

// BatchUpdateExpiredStatus marks stored active or pending grants expired when
// their deadline has reached a current-time observation. It returns affected
// count with the Ent execution result and adds no separate deleted-row
// predicate to this update.
func (r *SubscriptionStore) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	client := clientFromContext(ctx, r.client)
	n, err := client.UserSubscription.Update().
		Where(
			usersubscription.StatusIn(billing.SubscriptionStatusActive, billing.SubscriptionStatusPending),
			usersubscription.ExpiresAtLTE(time.Now()),
		).
		SetStatus(billing.SubscriptionStatusExpired).
		Save(ctx)
	return int64(n), err
}

// CountByGroupID returns the compatibility result zero with no error for every
// supplied group and context. It executes no database query, so callers cannot
// use this result to establish that a group has no grants.
func (r *SubscriptionStore) CountByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}

// attachUserSubscriptionRelations enriches the supplied grant slice after
// history queries that bypass subscription deletion filtering. Empty input
// returns immediately. It gathers positive user, plan and optional
// assigning-user identities, then reads those relation sets through the
// selected client and ordinary query contexts.
//
// Only after every required relation read succeeds does the method publish
// pointers into the input records. Missing relations become nil, and records
// sharing a relation ID receive the same converted pointer, so subsequent
// display mutation can be shared. The queries have separate observations
// unless a caller transaction provides stronger consistency; an earlier query
// failure leaves the supplied records unenriched.
func (r *SubscriptionStore) attachUserSubscriptionRelations(ctx context.Context, subs []billing.UserSubscription) error {
	if len(subs) == 0 {
		return nil
	}

	userIDs := make([]int64, 0, len(subs))
	planIDs := make([]int64, 0, len(subs))
	assignedByIDs := make([]int64, 0, len(subs))
	for i := range subs {
		userIDs = append(userIDs, subs[i].UserID)
		planIDs = append(planIDs, subs[i].PlanID)
		if subs[i].AssignedBy != nil {
			assignedByIDs = append(assignedByIDs, *subs[i].AssignedBy)
		}
	}

	client := clientFromContext(ctx, r.client)
	users, err := client.User.Query().Where(dbuser.IDIn(uniquePositiveInt64s(userIDs)...)).All(ctx)
	if err != nil {
		return err
	}
	userByID := make(map[int64]*billing.UserSummary, len(users))
	for _, u := range users {
		userByID[u.ID] = userSummaryFromEntity(u)
	}

	plans, err := client.SubscriptionPlan.Query().Where(subscriptionplan.IDIn(uniquePositiveInt64s(planIDs)...)).All(ctx)
	if err != nil {
		return err
	}
	planByID := make(map[int64]*billing.SubscriptionPlan, len(plans))
	for _, plan := range plans {
		planByID[plan.ID] = PlanFromEntity(plan)
	}

	assignedByID := map[int64]*billing.UserSummary{}
	if len(assignedByIDs) > 0 {
		assignedUsers, err := client.User.Query().Where(dbuser.IDIn(uniquePositiveInt64s(assignedByIDs)...)).All(ctx)
		if err != nil {
			return err
		}
		assignedByID = make(map[int64]*billing.UserSummary, len(assignedUsers))
		for _, u := range assignedUsers {
			assignedByID[u.ID] = userSummaryFromEntity(u)
		}
	}

	// Publish shared relation pointers after all required reads succeed. A
	// missing identity yields nil, and grants with matching IDs receive the same
	// converted relation object.
	for i := range subs {
		subs[i].User = userByID[subs[i].UserID]
		subs[i].Plan = planByID[subs[i].PlanID]
		if subs[i].AssignedBy != nil {
			subs[i].AssignedByUser = assignedByID[*subs[i].AssignedBy]
		}
	}
	return nil
}

// SubscriptionFromEntity converts a nullable Ent grant, mapping any deletion
// marker to displayed revoked status. Nil input yields nil; optional field
// pointers remain shared and loaded relations use their conversion helpers.
func SubscriptionFromEntity(m *dbent.UserSubscription) *billing.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, true)
}

// SubscriptionFromEntityPreserveStatus converts a nullable Ent grant while
// retaining stored status even when deleted. Restoration uses that status with
// the original lifetime; optional fields remain shared under the conversion
// helper's ownership rules.
func SubscriptionFromEntityPreserveStatus(m *dbent.UserSubscription) *billing.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, false)
}

// userSubscriptionEntityToServiceWithStatusMapping creates a service grant
// from a nullable Ent row and selected relation edges. Nil input yields nil.
// mapDeletedToRevoked selects a display status for deleted rows; false retains
// the stored status needed when clearing deletion during restoration.
//
// Lifetime and usage scalars copy by value, while optional anchors, limits,
// owner metadata and deletion time retain their pointers. Loaded user
// summaries are newly converted, and plan conversion copies group collections
// but keeps its own shared optional data. The caller treats those retained
// nested values as read-only or copies them before mutation.
func userSubscriptionEntityToServiceWithStatusMapping(m *dbent.UserSubscription, mapDeletedToRevoked bool) *billing.UserSubscription {
	if m == nil {
		return nil
	}
	// Select displayed deletion status before assembling the grant. Restoration
	// conversion retains stored status so clearing DeletedAt does not write the
	// display-only revoked label back.
	status := m.Status
	if mapDeletedToRevoked && m.DeletedAt != nil {
		status = billing.SubscriptionStatusRevoked
	}
	out := &billing.UserSubscription{
		ID:                    m.ID,
		UserID:                m.UserID,
		PlanID:                m.PlanID,
		StartsAt:              m.StartsAt,
		ExpiresAt:             m.ExpiresAt,
		Status:                status,
		DailyWindowStart:      m.DailyWindowStart,
		WeeklyWindowStart:     m.WeeklyWindowStart,
		MonthlyWindowStart:    m.MonthlyWindowStart,
		DailyLimitUSD:         m.DailyLimitUsd,
		WeeklyLimitUSD:        m.WeeklyLimitUsd,
		MonthlyLimitUSD:       m.MonthlyLimitUsd,
		DailyUsageUSD:         m.DailyUsageUsd,
		WeeklyUsageUSD:        m.WeeklyUsageUsd,
		MonthlyUsageUSD:       m.MonthlyUsageUsd,
		AssignedBy:            m.AssignedBy,
		AssignedAt:            m.AssignedAt,
		SourceOrderID:         m.SourceOrderID,
		RefundHoldOperationID: m.RefundHoldOperationID,
		Notes:                 derefString(m.Notes),
		CreatedAt:             m.CreatedAt,
		UpdatedAt:             m.UpdatedAt,
		DeletedAt:             m.DeletedAt,
	}
	if m.Edges.User != nil {
		out.User = userSummaryFromEntity(m.Edges.User)
	}
	if m.Edges.Plan != nil {
		out.Plan = PlanFromEntity(m.Edges.Plan)
	}
	if m.Edges.AssignedByUser != nil {
		out.AssignedByUser = userSummaryFromEntity(m.Edges.AssignedByUser)
	}
	return out
}

// userSubscriptionEntitiesToService returns a fresh outer slice of converted
// nonnil grant rows, skipping nil entities. Each record follows
// SubscriptionFromEntity's status mapping and retained nested pointers; the
// slice is allocated even for empty input.
func userSubscriptionEntitiesToService(models []*dbent.UserSubscription) []billing.UserSubscription {
	out := make([]billing.UserSubscription, 0, len(models))
	for i := range models {
		if s := SubscriptionFromEntity(models[i]); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

// applyUserSubscriptionEntityToService copies ID, CreatedAt and UpdatedAt from
// a successful persistence row into the supplied grant. Nil inputs are
// ignored; schema defaults and other saved fields remain available only
// through a subsequent reread.
func applyUserSubscriptionEntityToService(dst *billing.UserSubscription, src *dbent.UserSubscription) {
	if dst == nil || src == nil {
		return
	}
	dst.ID = src.ID
	dst.CreatedAt = src.CreatedAt
	dst.UpdatedAt = src.UpdatedAt
}

// PlanFromEntity converts a nullable Ent plan, copying GroupIDs and the
// group-rate map into new collections. Optional price/quota pointers and
// nested localization storage remain shared; nil input yields nil, so callers
// copy those retained values before mutable presentation work.
func PlanFromEntity(plan *dbent.SubscriptionPlan) *billing.SubscriptionPlan {
	if plan == nil {
		return nil
	}
	return &billing.SubscriptionPlan{
		Localization:         plan.Localization,
		ID:                   plan.ID,
		Name:                 plan.Name,
		Description:          plan.Description,
		Price:                plan.Price,
		OriginalPrice:        plan.OriginalPrice,
		Currency:             plan.Currency,
		ValidityDays:         plan.ValidityDays,
		ValidityUnit:         plan.ValidityUnit,
		GroupIDs:             append([]int64(nil), plan.GroupIds...),
		GroupRateMultipliers: cloneInt64Float64Map(plan.GroupRateMultipliers),
		DailyLimitUSD:        plan.DailyLimitUsd,
		WeeklyLimitUSD:       plan.WeeklyLimitUsd,
		MonthlyLimitUSD:      plan.MonthlyLimitUsd,
		Features:             plan.Features,
		ProductName:          plan.ProductName,
		ForSale:              plan.ForSale,
		SortOrder:            plan.SortOrder,
		CreatedAt:            plan.CreatedAt,
		UpdatedAt:            plan.UpdatedAt,
	}
}

// cloneInt64Float64Map returns separate scalar rate-map storage with the
// supplied entries. Nil or empty input produces an allocated empty map;
// callers can change returned entries independently of the input map.
func cloneInt64Float64Map(in map[int64]float64) map[int64]float64 {
	if len(in) == 0 {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(in))
	maps.Copy(out, in)
	return out
}
