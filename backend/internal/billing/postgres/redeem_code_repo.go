package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbpredicate "github.com/TokenFlux/TokenRouter/ent/predicate"
	"github.com/TokenFlux/TokenRouter/ent/redeemcode"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"
	"github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	entsql "entgo.io/ent/dialect/sql"
)

type RedeemStore struct {
	client *dbent.Client
}

func NewRedeemCodeRepository(client *dbent.Client) *RedeemStore {
	return &RedeemStore{client: client}
}

func (r *RedeemStore) Create(ctx context.Context, code *billing.RedeemCode) error {
	client := clientFromContext(ctx, r.client)
	builder := client.RedeemCode.Create().
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes).
		SetNillableUsedBy(code.UsedBy).
		SetNillableUsedAt(code.UsedAt).
		SetNillablePlanID(code.PlanID)

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
			SetMaxUses(c.MaxUses).
			SetUsedCount(c.UsedCount).
			SetNotes(c.Notes).
			SetNillableUsedBy(c.UsedBy).
			SetNillableUsedAt(c.UsedAt).
			SetNillablePlanID(c.PlanID)
		if c.ExpiresAt != nil {
			builder.SetExpiresAt(*c.ExpiresAt)
		}
		builders = append(builders, builder)
	}

	return translatePersistenceError(client.RedeemCode.CreateBulk(builders...).Exec(ctx), nil, billing.ErrRedeemCodeExists)
}

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

	if dbent.TxFromContext(ctx) != nil {
		return r.batchUpdate(ctx, clientFromContext(ctx, r.client), uniqueIDs, fields)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		if errors.Is(err, dbent.ErrTxStarted) {
			return r.batchUpdate(ctx, clientFromContext(ctx, r.client), uniqueIDs, fields)
		}
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	updated, err := r.batchUpdate(txCtx, tx.Client(), uniqueIDs, fields)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

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

func (r *RedeemStore) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.RedeemCode.Delete().Where(redeemcode.IDEQ(id)).Exec(ctx)
	return err
}

func (r *RedeemStore) Use(ctx context.Context, id, userID int64) error {
	if dbent.TxFromContext(ctx) != nil {
		return r.useInTx(ctx, id, userID)
	}

	tx, err := r.client.Tx(ctx)
	if err != nil {
		if errors.Is(err, dbent.ErrTxStarted) {
			return r.useInTx(ctx, id, userID)
		}
		return err
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := r.useInTx(txCtx, id, userID); err != nil {
		return err
	}
	return tx.Commit()
}

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

	usageTime := time.Now()
	if err := r.CreateUsage(ctx, &billing.RedeemCodeUsage{
		RedeemCodeID: id,
		UserID:       userID,
		UsedAt:       usageTime,
	}); err != nil {
		if isUniqueConstraintViolation(err) {
			return billing.ErrRedeemCodeUsed
		}
		return err
	}

	code.UsedCount++
	code.UsedBy = &userID
	code.UsedAt = &usageTime
	code.Status = code.PersistedStatus()

	return r.Update(ctx, code)
}

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

func (r *RedeemStore) List(ctx context.Context, params pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, "", "", "")
}

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

func redeemCodeEffectiveAvailablePredicate(s *entsql.Selector) *entsql.Predicate {
	return entsql.And(
		entsql.Not(redeemCodeEffectiveExpiredPredicate(s)),
		entsql.NEQ(s.C(redeemcode.FieldStatus), billing.StatusDisabled),
	)
}

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

func (r *RedeemStore) ListByUser(ctx context.Context, userID int64, limit int) ([]billing.RedeemCode, error) {
	if limit <= 0 {
		limit = 10
	}

	client := clientFromContext(ctx, r.client)
	usages, err := client.RedeemCodeUsage.Query().
		Where(redeemcodeusage.UserIDEQ(userID)).
		WithRedeemCode(func(q *dbent.RedeemCodeQuery) {
			q.WithPlan()
		}).
		Order(dbent.Desc(redeemcodeusage.FieldUsedAt), dbent.Desc(redeemcodeusage.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}

	return redeemCodeHistoryFromUsageEntities(usages), nil
}

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

func (r *RedeemStore) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	client := clientFromContext(ctx, r.client)
	var result []struct {
		Sum float64 `json:"sum"`
	}

	err := client.RedeemCodeUsage.Query().
		Where(
			redeemcodeusage.UserIDEQ(userID),
			redeemcodeusage.HasRedeemCodeWith(
				redeemcode.ValueGT(0),
				redeemcode.TypeIn(billing.RedeemTypeBalance, billing.AdjustmentTypeAdminBalance),
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

func RedeemFromEntity(model *dbent.RedeemCode) *billing.RedeemCode {
	if model == nil {
		return nil
	}
	out := &billing.RedeemCode{
		ID:        model.ID,
		Code:      model.Code,
		Type:      model.Type,
		Value:     model.Value,
		Status:    model.Status,
		MaxUses:   model.MaxUses,
		UsedCount: model.UsedCount,
		ExpiresAt: model.ExpiresAt,
		UsedBy:    model.UsedBy,
		UsedAt:    model.UsedAt,
		Notes:     derefString(model.Notes),
		CreatedAt: model.CreatedAt,
		PlanID:    model.PlanID,
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
	out.Status = out.EffectiveStatus()
	return out
}

func redeemCodeEntitiesToService(models []*dbent.RedeemCode) []billing.RedeemCode {
	out := make([]billing.RedeemCode, 0, len(models))
	for i := range models {
		if code := RedeemFromEntity(models[i]); code != nil {
			out = append(out, *code)
		}
	}
	return out
}

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

func redeemCodeUsageEntitiesToService(models []*dbent.RedeemCodeUsage) []billing.RedeemCodeUsage {
	out := make([]billing.RedeemCodeUsage, 0, len(models))
	for i := range models {
		if usage := redeemCodeUsageEntityToService(models[i]); usage != nil {
			out = append(out, *usage)
		}
	}
	return out
}

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
		usedBy := usage.UserID
		usedAt := usage.UsedAt
		code.UsedBy = &usedBy
		code.UsedAt = &usedAt
		out = append(out, *code)
	}
	return out
}
