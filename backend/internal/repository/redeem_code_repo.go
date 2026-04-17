package repository

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
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/service"

	entsql "entgo.io/ent/dialect/sql"
)

type redeemCodeRepository struct {
	client *dbent.Client
}

func NewRedeemCodeRepository(client *dbent.Client) service.RedeemCodeRepository {
	return &redeemCodeRepository{client: client}
}

func (r *redeemCodeRepository) Create(ctx context.Context, code *service.RedeemCode) error {
	client := clientFromContext(ctx, r.client)
	builder := client.RedeemCode.Create().
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes).
		SetValidityDays(code.ValidityDays).
		SetNillableUsedBy(code.UsedBy).
		SetNillableUsedAt(code.UsedAt).
		SetNillableGroupID(code.GroupID)

	if code.ExpiresAt != nil {
		builder.SetExpiresAt(*code.ExpiresAt)
	}

	created, err := builder.Save(ctx)
	if err == nil {
		code.ID = created.ID
		code.CreatedAt = created.CreatedAt
	}
	return translatePersistenceError(err, nil, service.ErrRedeemCodeExists)
}

func (r *redeemCodeRepository) CreateBatch(ctx context.Context, codes []service.RedeemCode) error {
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
			SetValidityDays(c.ValidityDays).
			SetNillableUsedBy(c.UsedBy).
			SetNillableUsedAt(c.UsedAt).
			SetNillableGroupID(c.GroupID)
		if c.ExpiresAt != nil {
			builder.SetExpiresAt(*c.ExpiresAt)
		}
		builders = append(builders, builder)
	}

	return translatePersistenceError(client.RedeemCode.CreateBulk(builders...).Exec(ctx), nil, service.ErrRedeemCodeExists)
}

func (r *redeemCodeRepository) GetByID(ctx context.Context, id int64) (*service.RedeemCode, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.IDEQ(id)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return redeemCodeEntityToService(model), nil
}

func (r *redeemCodeRepository) GetByCode(ctx context.Context, code string) (*service.RedeemCode, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.CodeEQ(code)).
		WithUser().
		WithGroup().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return redeemCodeEntityToService(model), nil
}

func (r *redeemCodeRepository) GetByCodeForUpdate(ctx context.Context, code string) (*service.RedeemCode, error) {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.CodeEQ(code)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return nil, service.ErrRedeemCodeNotFound
		}
		return nil, err
	}
	return redeemCodeEntityToService(model), nil
}

func (r *redeemCodeRepository) Update(ctx context.Context, code *service.RedeemCode) error {
	client := clientFromContext(ctx, r.client)
	update := client.RedeemCode.UpdateOneID(code.ID).
		SetCode(code.Code).
		SetType(code.Type).
		SetValue(code.Value).
		SetStatus(code.Status).
		SetMaxUses(code.MaxUses).
		SetUsedCount(code.UsedCount).
		SetNotes(code.Notes).
		SetValidityDays(code.ValidityDays)

	if code.UsedBy != nil {
		update.SetUsedBy(*code.UsedBy)
	} else {
		update.ClearUsedBy()
	}
	if code.UsedAt != nil {
		update.SetUsedAt(*code.UsedAt)
	} else {
		update.ClearUsedAt()
	}
	if code.GroupID != nil {
		update.SetGroupID(*code.GroupID)
	} else {
		update.ClearGroupID()
	}
	if code.ExpiresAt != nil {
		update.SetExpiresAt(*code.ExpiresAt)
	} else {
		update.ClearExpiresAt()
	}

	updated, err := update.Save(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return service.ErrRedeemCodeNotFound
		}
		return err
	}
	code.CreatedAt = updated.CreatedAt
	return nil
}

func (r *redeemCodeRepository) Delete(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.RedeemCode.Delete().Where(redeemcode.IDEQ(id)).Exec(ctx)
	return err
}

func (r *redeemCodeRepository) Use(ctx context.Context, id, userID int64) error {
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

func (r *redeemCodeRepository) useInTx(ctx context.Context, id, userID int64) error {
	client := clientFromContext(ctx, r.client)
	model, err := client.RedeemCode.Query().
		Where(redeemcode.IDEQ(id)).
		ForUpdate().
		Only(ctx)
	if err != nil {
		if dbent.IsNotFound(err) {
			return service.ErrRedeemCodeNotFound
		}
		return err
	}

	code := redeemCodeEntityToService(model)
	if code == nil || !code.CanUse() {
		return service.ErrRedeemCodeUsed
	}

	existingUsage, err := r.GetUsageByRedeemCodeAndUser(ctx, id, userID)
	if err != nil {
		return err
	}
	if existingUsage != nil {
		return service.ErrRedeemCodeUsed
	}

	usageTime := time.Now()
	if err := r.CreateUsage(ctx, &service.RedeemCodeUsage{
		RedeemCodeID: id,
		UserID:       userID,
		UsedAt:       usageTime,
	}); err != nil {
		if isUniqueConstraintViolation(err) {
			return service.ErrRedeemCodeUsed
		}
		return err
	}

	code.UsedCount++
	code.UsedBy = &userID
	code.UsedAt = &usageTime
	code.Status = code.PersistedStatus()

	return r.Update(ctx, code)
}

func (r *redeemCodeRepository) CreateUsage(ctx context.Context, usage *service.RedeemCodeUsage) error {
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

func (r *redeemCodeRepository) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*service.RedeemCodeUsage, error) {
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

func (r *redeemCodeRepository) List(ctx context.Context, params pagination.PaginationParams) ([]service.RedeemCode, *pagination.PaginationResult, error) {
	return r.ListWithFilters(ctx, params, "", "", "")
}

func (r *redeemCodeRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]service.RedeemCode, *pagination.PaginationResult, error) {
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
		WithGroup().
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
		case service.StatusExpired:
			s.Where(redeemCodeEffectiveExpiredPredicate(s))
		case service.StatusUsed:
			s.Where(entsql.And(
				redeemCodeEffectiveAvailablePredicate(s),
				entsql.GT(maxUsesCol, 0),
				entsql.ColumnsGTE(usedCountCol, maxUsesCol),
			))
		case service.StatusActive:
			s.Where(entsql.And(
				redeemCodeEffectiveAvailablePredicate(s),
				entsql.GT(usedCountCol, 0),
				entsql.Or(
					entsql.EQ(maxUsesCol, 0),
					entsql.ColumnsLT(usedCountCol, maxUsesCol),
				),
			))
		case service.StatusUnused:
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
	typeCol := s.C(redeemcode.FieldType)
	expiresAtCol := s.C(redeemcode.FieldExpiresAt)
	usedCountCol := s.C(redeemcode.FieldUsedCount)
	maxUsesCol := s.C(redeemcode.FieldMaxUses)

	return fmt.Sprintf(
		"CASE "+
			"WHEN %s = '%s' OR (%s <> '%s' AND %s IS NOT NULL AND %s <= NOW()) THEN '%s' "+
			"WHEN %s > 0 AND %s >= %s THEN '%s' "+
			"WHEN %s > 0 THEN '%s' "+
			"ELSE '%s' END",
		statusCol,
		service.StatusExpired,
		typeCol,
		service.RedeemTypeInvitation,
		expiresAtCol,
		expiresAtCol,
		service.StatusExpired,
		maxUsesCol,
		usedCountCol,
		maxUsesCol,
		service.StatusUsed,
		usedCountCol,
		service.StatusActive,
		service.StatusUnused,
	)
}

func redeemCodeEffectiveExpiredPredicate(s *entsql.Selector) *entsql.Predicate {
	statusCol := s.C(redeemcode.FieldStatus)
	typeCol := s.C(redeemcode.FieldType)
	expiresAtCol := s.C(redeemcode.FieldExpiresAt)
	return entsql.Or(
		entsql.EQ(statusCol, service.StatusExpired),
		entsql.And(
			entsql.Not(entsql.EQ(typeCol, service.RedeemTypeInvitation)),
			entsql.Not(entsql.IsNull(expiresAtCol)),
			entsql.LTE(expiresAtCol, entsql.Expr("NOW()")),
		),
	)
}

func redeemCodeEffectiveAvailablePredicate(s *entsql.Selector) *entsql.Predicate {
	return entsql.Not(redeemCodeEffectiveExpiredPredicate(s))
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

func (r *redeemCodeRepository) ListByUser(ctx context.Context, userID int64, limit int) ([]service.RedeemCode, error) {
	if limit <= 0 {
		limit = 10
	}

	client := clientFromContext(ctx, r.client)
	usages, err := client.RedeemCodeUsage.Query().
		Where(redeemcodeusage.UserIDEQ(userID)).
		WithRedeemCode(func(q *dbent.RedeemCodeQuery) {
			q.WithGroup()
		}).
		Order(dbent.Desc(redeemcodeusage.FieldUsedAt), dbent.Desc(redeemcodeusage.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}

	return redeemCodeHistoryFromUsageEntities(usages), nil
}

func (r *redeemCodeRepository) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]service.RedeemCode, *pagination.PaginationResult, error) {
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
			q.WithGroup()
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

func (r *redeemCodeRepository) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	client := clientFromContext(ctx, r.client)
	var result []struct {
		Sum float64 `json:"sum"`
	}

	err := client.RedeemCodeUsage.Query().
		Where(
			redeemcodeusage.UserIDEQ(userID),
			redeemcodeusage.HasRedeemCodeWith(
				redeemcode.ValueGT(0),
				redeemcode.TypeIn(service.RedeemTypeBalance, service.AdjustmentTypeAdminBalance),
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

func redeemCodeEntityToService(model *dbent.RedeemCode) *service.RedeemCode {
	if model == nil {
		return nil
	}
	out := &service.RedeemCode{
		ID:           model.ID,
		Code:         model.Code,
		Type:         model.Type,
		Value:        model.Value,
		Status:       model.Status,
		MaxUses:      model.MaxUses,
		UsedCount:    model.UsedCount,
		ExpiresAt:    model.ExpiresAt,
		UsedBy:       model.UsedBy,
		UsedAt:       model.UsedAt,
		Notes:        derefString(model.Notes),
		CreatedAt:    model.CreatedAt,
		GroupID:      model.GroupID,
		ValidityDays: model.ValidityDays,
	}
	if model.Edges.User != nil {
		out.User = userEntityToService(model.Edges.User)
	}
	if model.Edges.Group != nil {
		out.Group = groupEntityToService(model.Edges.Group)
	}
	if len(model.Edges.UsageRecords) > 0 {
		out.UsageRecords = redeemCodeUsageEntitiesToService(model.Edges.UsageRecords)
	}
	out.Status = out.EffectiveStatus()
	return out
}

func redeemCodeEntitiesToService(models []*dbent.RedeemCode) []service.RedeemCode {
	out := make([]service.RedeemCode, 0, len(models))
	for i := range models {
		if code := redeemCodeEntityToService(models[i]); code != nil {
			out = append(out, *code)
		}
	}
	return out
}

func redeemCodeUsageEntityToService(model *dbent.RedeemCodeUsage) *service.RedeemCodeUsage {
	if model == nil {
		return nil
	}
	out := &service.RedeemCodeUsage{
		ID:           model.ID,
		RedeemCodeID: model.RedeemCodeID,
		UserID:       model.UserID,
		UsedAt:       model.UsedAt,
	}
	if model.Edges.User != nil {
		out.User = userEntityToService(model.Edges.User)
	}
	if model.Edges.RedeemCode != nil {
		out.RedeemCode = redeemCodeEntityToService(model.Edges.RedeemCode)
	}
	return out
}

func redeemCodeUsageEntitiesToService(models []*dbent.RedeemCodeUsage) []service.RedeemCodeUsage {
	out := make([]service.RedeemCodeUsage, 0, len(models))
	for i := range models {
		if usage := redeemCodeUsageEntityToService(models[i]); usage != nil {
			out = append(out, *usage)
		}
	}
	return out
}

func redeemCodeHistoryFromUsageEntities(models []*dbent.RedeemCodeUsage) []service.RedeemCode {
	out := make([]service.RedeemCode, 0, len(models))
	for i := range models {
		usage := models[i]
		if usage == nil || usage.Edges.RedeemCode == nil {
			continue
		}
		code := redeemCodeEntityToService(usage.Edges.RedeemCode)
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
