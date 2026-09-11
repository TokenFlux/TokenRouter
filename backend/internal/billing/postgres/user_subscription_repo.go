package postgres

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	"github.com/TokenFlux/TokenRouter/ent/subscriptionplan"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/lib/pq"
)

type SubscriptionStore struct {
	client *dbent.Client
}

func NewUserSubscriptionRepository(client *dbent.Client) *SubscriptionStore {
	return &SubscriptionStore{client: client}
}

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

// GetByIDIncludeDeleted 绕过软删除过滤查询订阅，供恢复撤销订阅时读取原始记录。
func (r *SubscriptionStore) GetByIDIncludeDeleted(ctx context.Context, id int64) (*billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
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

func (r *SubscriptionStore) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.Delete().Where(usersubscription.IDEQ(id)).Exec(ctx)
	return err
}

// Restore 清除订阅软删除标记，并按当前时间窗口写回恢复后的状态。
func (r *SubscriptionStore) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
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

func (r *SubscriptionStore) ListActiveByUserID(ctx context.Context, userID int64) ([]billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	now := time.Now()
	subs, err := client.UserSubscription.Query().
		Where(
			usersubscription.UserIDEQ(userID),
			usersubscription.StartsAtLTE(now),
			usersubscription.ExpiresAtGT(now),
			usersubscription.StatusIn(billing.SubscriptionStatusActive, billing.SubscriptionStatusPending),
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

func (r *SubscriptionStore) List(ctx context.Context, params pagination.PaginationParams, userID, planID *int64, status, _platform, sortBy, sortOrder string) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	client := clientFromContext(ctx, r.client)
	queryCtx := ctx
	if status == "" || status == billing.SubscriptionStatusRevoked {
		// 管理端列表需要看到软删除订阅，以便撤销后仍能展示历史记录。
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
		)
	case billing.SubscriptionStatusPending:
		q = q.Where(
			usersubscription.StatusEQ(billing.SubscriptionStatusPending),
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

func (r *SubscriptionStore) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetExpiresAt(newExpiresAt).
		Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

func (r *SubscriptionStore) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetStatus(status).
		Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

func (r *SubscriptionStore) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.UserSubscription.UpdateOneID(subscriptionID).
		SetNotes(notes).
		Save(ctx)
	return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
}

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

func (r *SubscriptionStore) translateConditionalWindowReset(ctx context.Context, client *dbent.Client, id int64, affected int, err error) error {
	if err != nil {
		return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	if affected > 0 {
		return nil
	}

	// 旧快照触发的重置是预期的空操作，说明另一请求已推进窗口；但目标记录
	// 确实不存在时仍需保留 not-found 语义。
	exists, err := client.UserSubscription.Query().Where(usersubscription.IDEQ(id)).Exist(ctx)
	if err != nil {
		return translatePersistenceError(err, billing.ErrSubscriptionNotFound, nil)
	}
	if !exists {
		return billing.ErrSubscriptionNotFound
	}
	return nil
}

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

func (r *SubscriptionStore) ListExpired(ctx context.Context) ([]billing.UserSubscription, error) {
	client := clientFromContext(ctx, r.client)
	subs, err := client.UserSubscription.Query().
		Where(usersubscription.ExpiresAtLTE(time.Now())).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return userSubscriptionEntitiesToService(subs), nil
}

func (r *SubscriptionStore) CountByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}

func (r *SubscriptionStore) CountActiveByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}

func (r *SubscriptionStore) DeleteByGroupID(_ context.Context, _ int64) (int64, error) {
	return 0, nil
}

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

	for i := range subs {
		subs[i].User = userByID[subs[i].UserID]
		subs[i].Plan = planByID[subs[i].PlanID]
		if subs[i].AssignedBy != nil {
			subs[i].AssignedByUser = assignedByID[*subs[i].AssignedBy]
		}
	}
	return nil
}

func SubscriptionFromEntity(m *dbent.UserSubscription) *billing.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, true)
}

// SubscriptionFromEntityPreserveStatus 保留软删除记录的持久化状态，避免恢复逻辑误把 revoked 写回数据库。
func SubscriptionFromEntityPreserveStatus(m *dbent.UserSubscription) *billing.UserSubscription {
	return userSubscriptionEntityToServiceWithStatusMapping(m, false)
}

func userSubscriptionEntityToServiceWithStatusMapping(m *dbent.UserSubscription, mapDeletedToRevoked bool) *billing.UserSubscription {
	if m == nil {
		return nil
	}
	status := m.Status
	if mapDeletedToRevoked && m.DeletedAt != nil {
		status = billing.SubscriptionStatusRevoked
	}
	out := &billing.UserSubscription{
		ID:                 m.ID,
		UserID:             m.UserID,
		PlanID:             m.PlanID,
		StartsAt:           m.StartsAt,
		ExpiresAt:          m.ExpiresAt,
		Status:             status,
		DailyWindowStart:   m.DailyWindowStart,
		WeeklyWindowStart:  m.WeeklyWindowStart,
		MonthlyWindowStart: m.MonthlyWindowStart,
		DailyLimitUSD:      m.DailyLimitUsd,
		WeeklyLimitUSD:     m.WeeklyLimitUsd,
		MonthlyLimitUSD:    m.MonthlyLimitUsd,
		DailyUsageUSD:      m.DailyUsageUsd,
		WeeklyUsageUSD:     m.WeeklyUsageUsd,
		MonthlyUsageUSD:    m.MonthlyUsageUsd,
		AssignedBy:         m.AssignedBy,
		AssignedAt:         m.AssignedAt,
		SourceOrderID:      m.SourceOrderID,
		Notes:              derefString(m.Notes),
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
		DeletedAt:          m.DeletedAt,
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

func userSubscriptionEntitiesToService(models []*dbent.UserSubscription) []billing.UserSubscription {
	out := make([]billing.UserSubscription, 0, len(models))
	for i := range models {
		if s := SubscriptionFromEntity(models[i]); s != nil {
			out = append(out, *s)
		}
	}
	return out
}

func applyUserSubscriptionEntityToService(dst *billing.UserSubscription, src *dbent.UserSubscription) {
	if dst == nil || src == nil {
		return
	}
	dst.ID = src.ID
	dst.CreatedAt = src.CreatedAt
	dst.UpdatedAt = src.UpdatedAt
}

func PlanFromEntity(plan *dbent.SubscriptionPlan) *billing.SubscriptionPlan {
	if plan == nil {
		return nil
	}
	return &billing.SubscriptionPlan{
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

func cloneInt64Float64Map(in map[int64]float64) map[int64]float64 {
	if len(in) == 0 {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

var _ billing.UserSubscriptionRepository = (*SubscriptionStore)(nil)
