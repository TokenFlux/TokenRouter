// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	context "context"
	fmt "fmt"
	strings "strings"
	time "time"

	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// RedeemAdminTransactions 固定管理变更与调整记录的事务边界，核心不接触 ORM。
type RedeemAdminTransactions interface {
	Mutation(context.Context, func(context.Context) error) error
	Adjustment(context.Context, func(context.Context) error) error
	PlanExists(context.Context, int64) error
}

// RedeemAdmin 拥有兑换码管理和调整记录，用户资料管理仍留旧用例。
type RedeemAdmin struct {
	redeemCodeRepo RedeemCodeRepository
	transactions   RedeemAdminTransactions
	now            func() time.Time
}

func NewRedeemAdmin(repo RedeemCodeRepository, transactions RedeemAdminTransactions, now func() time.Time) *RedeemAdmin {
	return &RedeemAdmin{redeemCodeRepo: repo, transactions: transactions, now: now}
}

type GenerateRedeemCodesInput struct {
	Code      string
	Count     int
	Type      string
	Value     float64
	MaxUses   *int
	ExpiresAt *time.Time
	PlanID    *int64 // 订阅类型专用：关联的套餐ID
}

type UpdateRedeemCodeInput struct {
	Value        *float64
	MaxUses      *int
	ExpiresAt    *time.Time
	ExpiresAtSet bool
	PlanID       *int64 // 订阅类型专用：关联的套餐ID
}

// Redeem code management implementations
func (s *RedeemAdmin) ListRedeemCodes(ctx context.Context, page, pageSize int, codeType, status, search string, sortBy, sortOrder string) ([]RedeemCode, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize, SortBy: sortBy, SortOrder: sortOrder}
	codes, result, err := s.redeemCodeRepo.ListWithFilters(ctx, params, codeType, status, search)
	if err != nil {
		return nil, 0, err
	}
	return codes, result.Total, nil
}

func (s *RedeemAdmin) GetRedeemCode(ctx context.Context, id int64) (*RedeemCode, error) {
	return s.redeemCodeRepo.GetByID(ctx, id)
}

func (s *RedeemAdmin) GenerateRedeemCodes(ctx context.Context, input *GenerateRedeemCodesInput) ([]RedeemCode, error) {
	maxUses := 1
	if input.MaxUses != nil {
		if *input.MaxUses < 0 {
			return nil, apperror.BadRequest("REDEEM_CODE_MAX_USES_INVALID", "max_uses must be greater than or equal to 0")
		}
		maxUses = *input.MaxUses
	}

	customCode := strings.TrimSpace(input.Code)
	if customCode != "" {
		if input.Count != 1 {
			return nil, apperror.BadRequest("REDEEM_CODE_CUSTOM_COUNT_INVALID", "count must be 1 when code is provided")
		}
		if len(customCode) > 32 {
			return nil, apperror.BadRequest("REDEEM_CODE_TOO_LONG", "code must be at most 32 characters")
		}
	}

	// 如果是订阅类型，验证必须有 plan_id
	if input.Type == RedeemTypeSubscription {
		if input.PlanID == nil || *input.PlanID <= 0 {
			return nil, apperror.BadRequest("REDEEM_CODE_PLAN_REQUIRED", "plan_id is required for subscription type")
		}
		if err := s.transactions.PlanExists(ctx, *input.PlanID); err != nil {
			return nil, fmt.Errorf("plan not found: %w", err)
		}
	}
	if input.Type == RedeemTypeInvitation {
		maxUses = 1
	}

	codes := make([]RedeemCode, 0, input.Count)
	for i := 0; i < input.Count; i++ {
		codeValue := customCode
		if codeValue == "" {
			generatedCode, err := GenerateRedeemCode()
			if err != nil {
				return nil, err
			}
			codeValue = generatedCode
		}
		code := RedeemCode{
			Code:      codeValue,
			Type:      input.Type,
			Value:     input.Value,
			Status:    StatusUnused,
			MaxUses:   maxUses,
			ExpiresAt: input.ExpiresAt,
		}
		if input.Type == RedeemTypeSubscription {
			code.PlanID = input.PlanID
		}
		if err := s.redeemCodeRepo.Create(ctx, &code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func (s *RedeemAdmin) DeleteRedeemCode(ctx context.Context, id int64) error {
	code, err := s.redeemCodeRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if !code.CanDelete() {
		return apperror.Conflict("REDEEM_CODE_DELETE_USED", "cannot delete redeem code that has usage records")
	}
	return s.redeemCodeRepo.Delete(ctx, id)
}

func (s *RedeemAdmin) BatchDeleteRedeemCodes(ctx context.Context, ids []int64) (int64, error) {
	var deleted int64
	for _, id := range ids {
		code, err := s.redeemCodeRepo.GetByID(ctx, id)
		if err != nil || !code.CanDelete() {
			continue
		}
		if err := s.redeemCodeRepo.Delete(ctx, id); err == nil {
			deleted++
		}
	}
	return deleted, nil
}

func (s *RedeemAdmin) ExpireRedeemCode(ctx context.Context, id int64) (*RedeemCode, error) {
	code, err := s.redeemCodeRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	code.Status = StatusExpired
	if err := s.redeemCodeRepo.Update(ctx, code); err != nil {
		return nil, err
	}
	return code, nil
}

// GetUserBalanceHistory returns paginated balance/concurrency change records for a user.
func (s *RedeemAdmin) GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]RedeemCode, int64, float64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	codes, result, err := s.redeemCodeRepo.ListByUserPaginated(ctx, userID, params, codeType)
	if err != nil {
		return nil, 0, 0, err
	}
	// Aggregate total recharged amount (only once, regardless of type filter)
	totalRecharged, err := s.redeemCodeRepo.SumPositiveBalanceByUser(ctx, userID)
	if err != nil {
		return nil, 0, 0, err
	}
	return codes, result.Total, totalRecharged, nil
}

func (s *RedeemAdmin) UpdateRedeemCode(ctx context.Context, id int64, input *UpdateRedeemCodeInput) (*RedeemCode, error) {
	if input == nil {
		return nil, apperror.BadRequest("REDEEM_CODE_UPDATE_REQUIRED", "update payload is required")
	}
	err := s.transactions.Mutation(ctx, func(opCtx context.Context) error {
		code, err := s.redeemCodeRepo.GetByIDForUpdate(opCtx, id)
		if err != nil {
			return err
		}
		if !isEditableRedeemCodeType(code.Type) {
			return apperror.Conflict("REDEEM_CODE_SYSTEM_RECORD", "system redeem records cannot be updated")
		}

		if input.MaxUses != nil {
			if *input.MaxUses < 0 {
				return apperror.BadRequest("REDEEM_CODE_MAX_USES_INVALID", "max_uses must be greater than or equal to 0")
			}
			if *input.MaxUses > 0 && *input.MaxUses < code.UsedCount {
				return apperror.BadRequest("REDEEM_CODE_MAX_USES_BELOW_USED", "max_uses cannot be less than used_count")
			}
			code.MaxUses = *input.MaxUses
		}

		if input.ExpiresAtSet {
			code.ExpiresAt = input.ExpiresAt
		}

		if input.Value != nil || input.PlanID != nil {
			// 已兑换记录没有面值快照，修改面值或套餐会让历史展示与实际发放权益不一致。
			if code.UsedCount > 0 {
				return apperror.Conflict("REDEEM_CODE_VALUE_LOCKED", "value or plan cannot be updated after the code has been redeemed")
			}
			if err := s.applyRedeemCodeValueUpdate(opCtx, code, input); err != nil {
				return err
			}
		}

		if code.Type == RedeemTypeInvitation {
			code.MaxUses = 1
		}
		if code.Status == StatusExpired && !code.IsNaturallyExpired() {
			// 更新次数或过期时间后，允许管理员把手动过期的普通兑换码恢复为可兑换状态。
			code.Status = StatusUnused
		}
		code.Status = code.PersistedStatus()
		if err := s.redeemCodeRepo.Update(opCtx, code); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.redeemCodeRepo.GetByID(ctx, id)
}

func (s *RedeemAdmin) applyRedeemCodeValueUpdate(ctx context.Context, code *RedeemCode, input *UpdateRedeemCodeInput) error {
	switch code.Type {
	case RedeemTypeBalance:
		if input.PlanID != nil {
			return apperror.BadRequest("REDEEM_CODE_PLAN_UNSUPPORTED", "plan_id is only supported for subscription redeem codes")
		}
		if input.Value != nil {
			code.Value = *input.Value
		}
	case RedeemTypeConcurrency:
		if input.PlanID != nil {
			return apperror.BadRequest("REDEEM_CODE_PLAN_UNSUPPORTED", "plan_id is only supported for subscription redeem codes")
		}
		if input.Value != nil {
			if *input.Value == 0 || *input.Value != float64(int(*input.Value)) {
				return apperror.BadRequest("REDEEM_CODE_VALUE_INVALID", "concurrency value must be a non-zero integer")
			}
			code.Value = *input.Value
		}
	case RedeemTypeSubscription:
		if input.Value != nil {
			return apperror.BadRequest("REDEEM_CODE_VALUE_UNSUPPORTED", "value is not editable for subscription redeem codes")
		}
		if input.PlanID != nil {
			if *input.PlanID <= 0 {
				return apperror.BadRequest("REDEEM_CODE_PLAN_REQUIRED", "plan_id is required for subscription type")
			}
			if err := s.transactions.PlanExists(ctx, *input.PlanID); err != nil {
				return fmt.Errorf("plan not found: %w", err)
			}
			code.PlanID = input.PlanID
		}
	case RedeemTypeInvitation:
		if input.Value != nil || input.PlanID != nil {
			return apperror.BadRequest("REDEEM_CODE_VALUE_UNSUPPORTED", "invitation code value cannot be updated")
		}
	}
	return nil
}

func (s *RedeemAdmin) RecordAdjustment(ctx context.Context, userID int64, codeType string, value float64, notes string) error {
	code, err := GenerateRedeemCode()
	if err != nil {
		return fmt.Errorf("generate adjustment redeem code: %w", err)
	}

	usedAt := s.now()
	record := &RedeemCode{
		Code:      code,
		Type:      codeType,
		Value:     value,
		Status:    StatusUsed,
		MaxUses:   1,
		UsedCount: 1,
		UsedBy:    &userID,
		UsedAt:    &usedAt,
		Notes:     notes,
	}

	return s.transactions.Adjustment(ctx, func(txCtx context.Context) error {
		if err := s.redeemCodeRepo.Create(txCtx, record); err != nil {
			return fmt.Errorf("create adjustment redeem code: %w", err)
		}
		if err := s.redeemCodeRepo.CreateUsage(txCtx, &RedeemCodeUsage{
			RedeemCodeID: record.ID,
			UserID:       userID,
			UsedAt:       usedAt,
		}); err != nil {
			return fmt.Errorf("create adjustment redeem usage: %w", err)
		}
		return nil
	})
}

func isEditableRedeemCodeType(codeType string) bool {
	switch codeType {
	case RedeemTypeBalance, RedeemTypeConcurrency, RedeemTypeSubscription, RedeemTypeInvitation:
		return true
	default:
		return false
	}
}
