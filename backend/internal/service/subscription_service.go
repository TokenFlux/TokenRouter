package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	"github.com/TokenFlux/TokenRouter/internal/config"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

var MaxExpiresAt = time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC)

const MaxValidityDays = 36500

var (
	ErrSubscriptionNotFound      = infraerrors.NotFound("SUBSCRIPTION_NOT_FOUND", "subscription not found")
	ErrSubscriptionExpired       = infraerrors.Forbidden("SUBSCRIPTION_EXPIRED", "subscription has expired")
	ErrSubscriptionSuspended     = infraerrors.Forbidden("SUBSCRIPTION_SUSPENDED", "subscription is suspended")
	ErrSubscriptionAlreadyExists = infraerrors.Conflict("SUBSCRIPTION_ALREADY_EXISTS", "subscription already exists")
	ErrInvalidInput              = infraerrors.BadRequest("INVALID_INPUT", "at least one of resetDaily, resetWeekly, or resetMonthly must be true")
	ErrDailyLimitExceeded        = infraerrors.TooManyRequests("DAILY_LIMIT_EXCEEDED", "daily usage limit exceeded")
	ErrWeeklyLimitExceeded       = infraerrors.TooManyRequests("WEEKLY_LIMIT_EXCEEDED", "weekly usage limit exceeded")
	ErrMonthlyLimitExceeded      = infraerrors.TooManyRequests("MONTHLY_LIMIT_EXCEEDED", "monthly usage limit exceeded")
	ErrSubscriptionNilInput      = infraerrors.BadRequest("SUBSCRIPTION_NIL_INPUT", "subscription input cannot be nil")
	ErrAdjustWouldExpire         = infraerrors.BadRequest("ADJUST_WOULD_EXPIRE", "adjustment would result in invalid subscription window")
)

type SubscriptionService struct {
	userSubRepo         UserSubscriptionRepository
	billingCacheService *BillingCacheService
	entClient           *dbent.Client
}

func NewSubscriptionService(_ GroupRepository, userSubRepo UserSubscriptionRepository, billingCacheService *BillingCacheService, entClient *dbent.Client, _ *config.Config) *SubscriptionService {
	return &SubscriptionService{
		userSubRepo:         userSubRepo,
		billingCacheService: billingCacheService,
		entClient:           entClient,
	}
}

func (s *SubscriptionService) Stop() {}

func (s *SubscriptionService) InvalidateSubCache(_ int64, _ int64) {}

type AssignSubscriptionInput struct {
	UserID              int64
	PlanID              int64
	ValidityDays        int
	DailyLimitUSD       *float64
	WeeklyLimitUSD      *float64
	MonthlyLimitUSD     *float64
	UseProvidedTemplate bool
	SourceOrderID       *int64
	AssignedBy          int64
	Notes               string
}

type grantPlanTemplate struct {
	ValidityDays    int
	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64
}

func (s *SubscriptionService) resolveGrantPlanTemplate(ctx context.Context, input *AssignSubscriptionInput) (*grantPlanTemplate, error) {
	if input == nil || input.PlanID <= 0 {
		return nil, fmt.Errorf("assign subscription: invalid plan_id")
	}

	template := &grantPlanTemplate{}
	if input.UseProvidedTemplate {
		template.ValidityDays = normalizeAssignValidityDays(input.ValidityDays)
		template.DailyLimitUSD = input.DailyLimitUSD
		template.WeeklyLimitUSD = input.WeeklyLimitUSD
		template.MonthlyLimitUSD = input.MonthlyLimitUSD
		if err := validatePlanQuotas(template.DailyLimitUSD, template.WeeklyLimitUSD, template.MonthlyLimitUSD); err != nil {
			return nil, err
		}
		return template, nil
	}
	if dbent.TxFromContext(ctx) == nil && s.entClient == nil {
		template.ValidityDays = normalizeAssignValidityDays(input.ValidityDays)
		template.DailyLimitUSD = input.DailyLimitUSD
		template.WeeklyLimitUSD = input.WeeklyLimitUSD
		template.MonthlyLimitUSD = input.MonthlyLimitUSD
		if err := validatePlanQuotas(template.DailyLimitUSD, template.WeeklyLimitUSD, template.MonthlyLimitUSD); err != nil {
			return nil, err
		}
		return template, nil
	}

	plan, err := s.getPlanForGrant(ctx, input.PlanID)
	if err != nil {
		return nil, fmt.Errorf("assign subscription: get plan %d: %w", input.PlanID, err)
	}

	template.ValidityDays = normalizeAssignValidityDays(psComputeValidityDays(plan.ValidityDays, plan.ValidityUnit))
	template.DailyLimitUSD = plan.DailyLimitUsd
	template.WeeklyLimitUSD = plan.WeeklyLimitUsd
	template.MonthlyLimitUSD = plan.MonthlyLimitUsd
	if input.ValidityDays > 0 {
		template.ValidityDays = normalizeAssignValidityDays(input.ValidityDays)
	}
	if input.DailyLimitUSD != nil {
		template.DailyLimitUSD = input.DailyLimitUSD
	}
	if input.WeeklyLimitUSD != nil {
		template.WeeklyLimitUSD = input.WeeklyLimitUSD
	}
	if input.MonthlyLimitUSD != nil {
		template.MonthlyLimitUSD = input.MonthlyLimitUSD
	}
	if err := validatePlanQuotas(template.DailyLimitUSD, template.WeeklyLimitUSD, template.MonthlyLimitUSD); err != nil {
		return nil, err
	}
	return template, nil
}

func (s *SubscriptionService) AssignSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, error) {
	sub, _, err := s.AssignOrExtendSubscription(ctx, input)
	return sub, err
}

func (s *SubscriptionService) AssignOrExtendSubscription(ctx context.Context, input *AssignSubscriptionInput) (*UserSubscription, bool, error) {
	if input == nil {
		return nil, false, ErrSubscriptionNilInput
	}
	if existing, found, err := s.findSourceOrderSubscription(ctx, input.SourceOrderID); err != nil {
		return nil, false, err
	} else if found {
		return existing, existing.IsPending(), nil
	}

	template, err := s.resolveGrantPlanTemplate(ctx, input)
	if err != nil {
		return nil, false, err
	}

	if tx := dbent.TxFromContext(ctx); tx != nil {
		return s.assignOrExtendSubscriptionInTx(ctx, tx, input, template)
	}
	if s.entClient == nil {
		return s.assignOrExtendSubscriptionUnlocked(ctx, input, template)
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	created, queued, err := s.assignOrExtendSubscriptionInTx(txCtx, tx, input, template)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit transaction: %w", err)
	}
	return created, queued, nil
}

func (s *SubscriptionService) getPlanForGrant(ctx context.Context, planID int64) (*dbent.SubscriptionPlan, error) {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.SubscriptionPlan.Get(ctx, planID)
	}
	if s.entClient == nil {
		return nil, fmt.Errorf("ent client is nil")
	}
	return s.entClient.SubscriptionPlan.Get(ctx, planID)
}

func (s *SubscriptionService) findSourceOrderSubscription(ctx context.Context, sourceOrderID *int64) (*UserSubscription, bool, error) {
	if sourceOrderID == nil || *sourceOrderID <= 0 {
		return nil, false, nil
	}
	subs, err := s.userSubRepo.ListBySourceOrderID(ctx, *sourceOrderID)
	if err != nil {
		return nil, false, err
	}
	if len(subs) == 0 {
		return nil, false, nil
	}
	normalizeSubscriptionStatus(subs)
	return &subs[0], true, nil
}

func (s *SubscriptionService) assignOrExtendSubscriptionInTx(ctx context.Context, tx *dbent.Tx, input *AssignSubscriptionInput, template *grantPlanTemplate) (*UserSubscription, bool, error) {
	if _, err := tx.User.Query().Where(dbuser.IDEQ(input.UserID)).ForUpdate().Only(ctx); err != nil {
		return nil, false, fmt.Errorf("lock user %d: %w", input.UserID, err)
	}
	if existing, found, err := s.findSourceOrderSubscription(ctx, input.SourceOrderID); err != nil {
		return nil, false, err
	} else if found {
		return existing, existing.IsPending(), nil
	}
	return s.assignOrExtendSubscriptionUnlocked(ctx, input, template)
}

func (s *SubscriptionService) assignOrExtendSubscriptionUnlocked(ctx context.Context, input *AssignSubscriptionInput, template *grantPlanTemplate) (*UserSubscription, bool, error) {
	now := time.Now()
	latest, err := s.userSubRepo.GetLatestByUserIDAndPlanID(ctx, input.UserID, input.PlanID)
	if err != nil {
		latest = nil
	}

	startsAt := now
	queued := false
	if latest != nil && latest.ExpiresAt.After(now) {
		startsAt = latest.ExpiresAt
		queued = true
	}
	expiresAt := startsAt.AddDate(0, 0, template.ValidityDays)
	if expiresAt.After(MaxExpiresAt) {
		expiresAt = MaxExpiresAt
	}

	status := SubscriptionStatusActive
	if startsAt.After(now) {
		status = SubscriptionStatusPending
	}

	sub := &UserSubscription{
		UserID:          input.UserID,
		PlanID:          input.PlanID,
		StartsAt:        startsAt,
		ExpiresAt:       expiresAt,
		Status:          status,
		DailyLimitUSD:   template.DailyLimitUSD,
		WeeklyLimitUSD:  template.WeeklyLimitUSD,
		MonthlyLimitUSD: template.MonthlyLimitUSD,
		AssignedAt:      now,
		SourceOrderID:   input.SourceOrderID,
		Notes:           input.Notes,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if input.AssignedBy > 0 {
		sub.AssignedBy = &input.AssignedBy
	}

	if err := s.userSubRepo.Create(ctx, sub); err != nil {
		return nil, false, err
	}
	created, err := s.userSubRepo.GetByID(ctx, sub.ID)
	if err != nil {
		return nil, false, err
	}
	return created, queued, nil
}

type BulkAssignSubscriptionInput struct {
	UserIDs         []int64
	PlanID          int64
	ValidityDays    int
	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64
	AssignedBy      int64
	Notes           string
}

type BulkAssignResult struct {
	SuccessCount  int
	CreatedCount  int
	ReusedCount   int
	FailedCount   int
	Subscriptions []UserSubscription
	Errors        []string
	Statuses      map[int64]string
}

func (s *SubscriptionService) BulkAssignSubscription(ctx context.Context, input *BulkAssignSubscriptionInput) (*BulkAssignResult, error) {
	result := &BulkAssignResult{
		Subscriptions: make([]UserSubscription, 0),
		Errors:        make([]string, 0),
		Statuses:      make(map[int64]string),
	}

	for _, userID := range input.UserIDs {
		sub, queued, err := s.AssignOrExtendSubscription(ctx, &AssignSubscriptionInput{
			UserID:          userID,
			PlanID:          input.PlanID,
			ValidityDays:    input.ValidityDays,
			DailyLimitUSD:   input.DailyLimitUSD,
			WeeklyLimitUSD:  input.WeeklyLimitUSD,
			MonthlyLimitUSD: input.MonthlyLimitUSD,
			AssignedBy:      input.AssignedBy,
			Notes:           input.Notes,
		})
		if err != nil {
			result.FailedCount++
			result.Errors = append(result.Errors, fmt.Sprintf("user %d: %v", userID, err))
			result.Statuses[userID] = "failed"
			continue
		}
		result.SuccessCount++
		result.CreatedCount++
		result.Subscriptions = append(result.Subscriptions, *sub)
		if queued {
			result.Statuses[userID] = "queued"
		} else {
			result.Statuses[userID] = "active"
		}
	}

	return result, nil
}

func normalizeAssignValidityDays(days int) int {
	if days <= 0 {
		days = 30
	}
	if days > MaxValidityDays {
		days = MaxValidityDays
	}
	return days
}

func (s *SubscriptionService) RevokeSubscription(ctx context.Context, subscriptionID int64) error {
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return err
	}

	chain, err := s.userSubRepo.ListByUserIDAndPlanID(ctx, sub.UserID, sub.PlanID)
	if err != nil {
		return err
	}

	duration := sub.ExpiresAt.Sub(sub.StartsAt)
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)

	if err := s.userSubRepo.Delete(txCtx, sub.ID); err != nil {
		return err
	}
	if duration > 0 {
		if err := s.shiftLaterChain(txCtx, chain, sub, -duration); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *SubscriptionService) ExtendSubscription(ctx context.Context, subscriptionID int64, days int) (*UserSubscription, error) {
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	if days > MaxValidityDays {
		days = MaxValidityDays
	}
	if days < -MaxValidityDays {
		days = -MaxValidityDays
	}

	now := time.Now()
	oldExpiresAt := sub.ExpiresAt
	var newExpiresAt time.Time
	if !oldExpiresAt.After(now) {
		if days < 0 {
			return nil, ErrAdjustWouldExpire
		}
		newExpiresAt = now.AddDate(0, 0, days)
	} else {
		newExpiresAt = oldExpiresAt.AddDate(0, 0, days)
	}
	if newExpiresAt.After(MaxExpiresAt) {
		newExpiresAt = MaxExpiresAt
	}
	if !newExpiresAt.After(sub.StartsAt) {
		return nil, ErrAdjustWouldExpire
	}

	delta := newExpiresAt.Sub(oldExpiresAt)
	chain, err := s.userSubRepo.ListByUserIDAndPlanID(ctx, sub.UserID, sub.PlanID)
	if err != nil {
		return nil, err
	}

	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)

	if err := s.userSubRepo.ExtendExpiry(txCtx, subscriptionID, newExpiresAt); err != nil {
		return nil, err
	}
	if delta != 0 {
		if err := s.shiftLaterChain(txCtx, chain, sub, delta); err != nil {
			return nil, err
		}
	}

	var status string
	if !newExpiresAt.After(now) {
		status = SubscriptionStatusExpired
	} else if now.Before(sub.StartsAt) {
		status = SubscriptionStatusPending
	} else {
		status = SubscriptionStatusActive
	}
	if err := s.userSubRepo.UpdateStatus(txCtx, subscriptionID, status); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return s.userSubRepo.GetByID(ctx, subscriptionID)
}

func (s *SubscriptionService) shiftLaterChain(ctx context.Context, chain []UserSubscription, anchor *UserSubscription, delta time.Duration) error {
	if delta == 0 || anchor == nil {
		return nil
	}
	now := time.Now()
	for i := range chain {
		item := chain[i]
		if item.ID == anchor.ID {
			continue
		}
		if item.StartsAt.Before(anchor.ExpiresAt) {
			continue
		}
		item.StartsAt = item.StartsAt.Add(delta)
		item.ExpiresAt = item.ExpiresAt.Add(delta)
		if item.ExpiresAt.After(MaxExpiresAt) {
			item.ExpiresAt = MaxExpiresAt
		}
		switch {
		case !item.ExpiresAt.After(now):
			item.Status = SubscriptionStatusExpired
		case now.Before(item.StartsAt):
			item.Status = SubscriptionStatusPending
		default:
			item.Status = SubscriptionStatusActive
		}
		if err := s.userSubRepo.Update(ctx, &item); err != nil {
			return err
		}
	}
	return nil
}

func (s *SubscriptionService) GetByID(ctx context.Context, id int64) (*UserSubscription, error) {
	sub, err := s.userSubRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sub.Status = sub.EffectiveStatus(now)
	return sub, nil
}

func (s *SubscriptionService) GetActiveSubscription(ctx context.Context, userID, planID int64) (*UserSubscription, error) {
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range subs {
		if subs[i].PlanID == planID {
			return &subs[i], nil
		}
	}
	return nil, ErrSubscriptionNotFound
}

func (s *SubscriptionService) GetUsableSubscription(ctx context.Context, userID int64) (*UserSubscription, bool, error) {
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(subs, func(i, j int) bool {
		if subs[i].ExpiresAt.Equal(subs[j].ExpiresAt) {
			return subs[i].StartsAt.Before(subs[j].StartsAt)
		}
		return subs[i].ExpiresAt.Before(subs[j].ExpiresAt)
	})
	for i := range subs {
		sub := &subs[i]
		needsMaintenance, validateErr := s.ValidateAndCheckLimits(sub, nil)
		if validateErr == nil {
			return sub, needsMaintenance, nil
		}
		if errors.Is(validateErr, ErrDailyLimitExceeded) ||
			errors.Is(validateErr, ErrWeeklyLimitExceeded) ||
			errors.Is(validateErr, ErrMonthlyLimitExceeded) ||
			errors.Is(validateErr, ErrSubscriptionExpired) ||
			errors.Is(validateErr, ErrSubscriptionSuspended) ||
			errors.Is(validateErr, ErrSubscriptionInvalid) {
			continue
		}
		return nil, false, validateErr
	}
	return nil, false, ErrSubscriptionNotFound
}

func (s *SubscriptionService) ListUserSubscriptions(ctx context.Context, userID int64) ([]UserSubscription, error) {
	subs, err := s.userSubRepo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	normalizeExpiredWindows(subs)
	normalizeSubscriptionStatus(subs)
	return subs, nil
}

func (s *SubscriptionService) ListActiveUserSubscriptions(ctx context.Context, userID int64) ([]UserSubscription, error) {
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	normalizeExpiredWindows(subs)
	normalizeSubscriptionStatus(subs)
	return subs, nil
}

func (s *SubscriptionService) ListSubscriptionsBySourceOrderID(ctx context.Context, sourceOrderID int64) ([]UserSubscription, error) {
	subs, err := s.userSubRepo.ListBySourceOrderID(ctx, sourceOrderID)
	if err != nil {
		return nil, err
	}
	normalizeExpiredWindows(subs)
	normalizeSubscriptionStatus(subs)
	return subs, nil
}

func (s *SubscriptionService) ListPlanSubscriptions(ctx context.Context, planID int64, page, pageSize int) ([]UserSubscription, *pagination.PaginationResult, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	subs, pag, err := s.userSubRepo.ListByPlanID(ctx, planID, params)
	if err != nil {
		return nil, nil, err
	}
	normalizeExpiredWindows(subs)
	normalizeSubscriptionStatus(subs)
	return subs, pag, nil
}

func (s *SubscriptionService) List(ctx context.Context, page, pageSize int, userID, planID *int64, status, _platform, sortBy, sortOrder string) ([]UserSubscription, *pagination.PaginationResult, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	subs, pag, err := s.userSubRepo.List(ctx, params, userID, planID, status, "", sortBy, sortOrder)
	if err != nil {
		return nil, nil, err
	}
	normalizeExpiredWindows(subs)
	normalizeSubscriptionStatus(subs)
	return subs, pag, nil
}

func normalizeExpiredWindows(subs []UserSubscription) {
	for i := range subs {
		sub := &subs[i]
		if sub.NeedsDailyReset() {
			sub.DailyWindowStart = nil
			sub.DailyUsageUSD = 0
		}
		if sub.NeedsWeeklyReset() {
			sub.WeeklyWindowStart = nil
			sub.WeeklyUsageUSD = 0
		}
		if sub.NeedsMonthlyReset() {
			sub.MonthlyWindowStart = nil
			sub.MonthlyUsageUSD = 0
		}
	}
}

func normalizeSubscriptionStatus(subs []UserSubscription) {
	now := time.Now()
	for i := range subs {
		subs[i].Status = subs[i].EffectiveStatus(now)
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func (s *SubscriptionService) CheckAndActivateWindow(ctx context.Context, sub *UserSubscription) error {
	if sub.IsWindowActivated() {
		return nil
	}
	windowStart := startOfDay(time.Now())
	return s.userSubRepo.ActivateWindows(ctx, sub.ID, windowStart)
}

func (s *SubscriptionService) AdminResetQuota(ctx context.Context, subscriptionID int64, resetDaily, resetWeekly, resetMonthly bool) (*UserSubscription, error) {
	if !resetDaily && !resetWeekly && !resetMonthly {
		return nil, ErrInvalidInput
	}
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}
	windowStart := startOfDay(time.Now())
	if resetDaily {
		if err := s.userSubRepo.ResetDailyUsage(ctx, sub.ID, windowStart); err != nil {
			return nil, err
		}
	}
	if resetWeekly {
		if err := s.userSubRepo.ResetWeeklyUsage(ctx, sub.ID, windowStart); err != nil {
			return nil, err
		}
	}
	if resetMonthly {
		if err := s.userSubRepo.ResetMonthlyUsage(ctx, sub.ID, windowStart); err != nil {
			return nil, err
		}
	}
	return s.userSubRepo.GetByID(ctx, subscriptionID)
}

func (s *SubscriptionService) CheckAndResetWindows(ctx context.Context, sub *UserSubscription) error {
	windowStart := startOfDay(time.Now())
	if sub.NeedsDailyReset() {
		if err := s.userSubRepo.ResetDailyUsage(ctx, sub.ID, windowStart); err != nil {
			return err
		}
		sub.DailyWindowStart = &windowStart
		sub.DailyUsageUSD = 0
	}
	if sub.NeedsWeeklyReset() {
		if err := s.userSubRepo.ResetWeeklyUsage(ctx, sub.ID, windowStart); err != nil {
			return err
		}
		sub.WeeklyWindowStart = &windowStart
		sub.WeeklyUsageUSD = 0
	}
	if sub.NeedsMonthlyReset() {
		if err := s.userSubRepo.ResetMonthlyUsage(ctx, sub.ID, windowStart); err != nil {
			return err
		}
		sub.MonthlyWindowStart = &windowStart
		sub.MonthlyUsageUSD = 0
	}
	return nil
}

func (s *SubscriptionService) CheckUsageLimits(_ context.Context, sub *UserSubscription, _ *Group, additionalCost float64) error {
	if !sub.CheckDailyLimit(additionalCost) {
		return ErrDailyLimitExceeded
	}
	if !sub.CheckWeeklyLimit(additionalCost) {
		return ErrWeeklyLimitExceeded
	}
	if !sub.CheckMonthlyLimit(additionalCost) {
		return ErrMonthlyLimitExceeded
	}
	return nil
}

func (s *SubscriptionService) ValidateAndCheckLimits(sub *UserSubscription, _ *Group) (needsMaintenance bool, err error) {
	switch sub.EffectiveStatus(time.Now()) {
	case SubscriptionStatusExpired:
		return false, ErrSubscriptionExpired
	case SubscriptionStatusSuspended:
		return false, ErrSubscriptionSuspended
	case SubscriptionStatusPending:
		return false, ErrSubscriptionInvalid
	}
	if sub.NeedsDailyReset() {
		sub.DailyUsageUSD = 0
		needsMaintenance = true
	}
	if sub.NeedsWeeklyReset() {
		sub.WeeklyUsageUSD = 0
		needsMaintenance = true
	}
	if sub.NeedsMonthlyReset() {
		sub.MonthlyUsageUSD = 0
		needsMaintenance = true
	}
	if !sub.IsWindowActivated() {
		needsMaintenance = true
	}
	return needsMaintenance, s.CheckUsageLimits(context.Background(), sub, nil, 0)
}

func (s *SubscriptionService) DoWindowMaintenance(sub *UserSubscription) {
	if sub == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !sub.IsWindowActivated() {
		_ = s.CheckAndActivateWindow(ctx, sub)
	}
	_ = s.CheckAndResetWindows(ctx, sub)
}

func (s *SubscriptionService) RecordUsage(ctx context.Context, subscriptionID int64, costUSD float64) error {
	return s.userSubRepo.IncrementUsage(ctx, subscriptionID, costUSD)
}

type SubscriptionProgress struct {
	ID            int64                `json:"id"`
	PlanID        int64                `json:"plan_id"`
	PlanName      string               `json:"plan_name"`
	StartsAt      time.Time            `json:"starts_at"`
	ExpiresAt     time.Time            `json:"expires_at"`
	Status        string               `json:"status"`
	ExpiresInDays int                  `json:"expires_in_days"`
	Daily         *UsageWindowProgress `json:"daily,omitempty"`
	Weekly        *UsageWindowProgress `json:"weekly,omitempty"`
	Monthly       *UsageWindowProgress `json:"monthly,omitempty"`
}

type UsageWindowProgress struct {
	LimitUSD        float64   `json:"limit_usd"`
	UsedUSD         float64   `json:"used_usd"`
	RemainingUSD    float64   `json:"remaining_usd"`
	Percentage      float64   `json:"percentage"`
	WindowStart     time.Time `json:"window_start"`
	ResetsAt        time.Time `json:"resets_at"`
	ResetsInSeconds int64     `json:"resets_in_seconds"`
}

func (s *SubscriptionService) GetSubscriptionProgress(ctx context.Context, subscriptionID int64) (*SubscriptionProgress, error) {
	sub, err := s.userSubRepo.GetByID(ctx, subscriptionID)
	if err != nil {
		return nil, ErrSubscriptionNotFound
	}
	return s.calculateProgress(sub), nil
}

func (s *SubscriptionService) calculateProgress(sub *UserSubscription) *SubscriptionProgress {
	now := time.Now()
	progress := &SubscriptionProgress{
		ID:            sub.ID,
		PlanID:        sub.PlanID,
		StartsAt:      sub.StartsAt,
		ExpiresAt:     sub.ExpiresAt,
		Status:        sub.EffectiveStatus(now),
		ExpiresInDays: sub.DaysRemaining(),
	}
	if sub.Plan != nil {
		progress.PlanName = sub.Plan.Name
	}
	if limit, ok := normalizedWindowProgress(sub.DailyLimitUSD, sub.DailyUsageUSD, sub.DailyWindowStart, 24*time.Hour); ok {
		progress.Daily = limit
	}
	if limit, ok := normalizedWindowProgress(sub.WeeklyLimitUSD, sub.WeeklyUsageUSD, sub.WeeklyWindowStart, 7*24*time.Hour); ok {
		progress.Weekly = limit
	}
	if limit, ok := normalizedWindowProgress(sub.MonthlyLimitUSD, sub.MonthlyUsageUSD, sub.MonthlyWindowStart, 30*24*time.Hour); ok {
		progress.Monthly = limit
	}
	return progress
}

func normalizedWindowProgress(limit *float64, used float64, windowStart *time.Time, duration time.Duration) (*UsageWindowProgress, bool) {
	if limit == nil || *limit <= 0 || windowStart == nil {
		return nil, false
	}
	resetsAt := windowStart.Add(duration)
	remaining := *limit - used
	if remaining < 0 {
		remaining = 0
	}
	percentage := 0.0
	if *limit > 0 {
		percentage = (used / *limit) * 100
		if percentage > 100 {
			percentage = 100
		}
	}
	resetsIn := int64(time.Until(resetsAt).Seconds())
	if resetsIn < 0 {
		resetsIn = 0
	}
	return &UsageWindowProgress{
		LimitUSD:        *limit,
		UsedUSD:         used,
		RemainingUSD:    remaining,
		Percentage:      percentage,
		WindowStart:     *windowStart,
		ResetsAt:        resetsAt,
		ResetsInSeconds: resetsIn,
	}, true
}

func (s *SubscriptionService) GetUserSubscriptionsWithProgress(ctx context.Context, userID int64) ([]SubscriptionProgress, error) {
	subs, err := s.userSubRepo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	progresses := make([]SubscriptionProgress, 0, len(subs))
	for i := range subs {
		progresses = append(progresses, *s.calculateProgress(&subs[i]))
	}
	return progresses, nil
}

func (s *SubscriptionService) ValidateSubscription(ctx context.Context, sub *UserSubscription) error {
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	switch sub.EffectiveStatus(time.Now()) {
	case SubscriptionStatusExpired:
		_ = s.userSubRepo.UpdateStatus(ctx, sub.ID, SubscriptionStatusExpired)
		return ErrSubscriptionExpired
	case SubscriptionStatusSuspended:
		return ErrSubscriptionSuspended
	case SubscriptionStatusPending:
		return ErrSubscriptionInvalid
	default:
		return nil
	}
}
