package billing

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// QuotaEvent 保留管理审计需要的值，不把 HTTP 上下文带入资金用例。
type QuotaEvent struct {
	Kind      string
	Operation string
	ActorID   int64
	UserID    int64
	Platform  string
	Window    string
	Before    []UserPlatformQuotaRecord
	Records   []UserPlatformQuotaRecord
	BeforeErr error
	Err       error
}

// PlatformQuotas 拥有平台额度的管理写入与提交后缓存失效。
// 管理操作与准入、镜像写回必须共享 app 注入的协调器。
type PlatformQuotas struct {
	repository  UserPlatformQuotaRepository
	cache       BillingCache
	users       BalanceReader
	coordinator *QuotaCoordinator
	now         func() time.Time
	observe     func(QuotaEvent)
}

func NewPlatformQuotas(repository UserPlatformQuotaRepository, cache BillingCache, users BalanceReader, coordinator *QuotaCoordinator, now func() time.Time, observe func(QuotaEvent)) *PlatformQuotas {
	return &PlatformQuotas{repository: repository, cache: cache, users: users, coordinator: coordinator, now: now, observe: observe}
}

// Available 保留未配置额度仓储时 GET 返回空列表、写入返回 503 的边界。
func (s *PlatformQuotas) Available() bool { return s != nil && s.repository != nil }

func (s *PlatformQuotas) ListForUser(ctx context.Context, userID int64) ([]UserPlatformQuotaRecord, error) {
	if !s.Available() {
		return []UserPlatformQuotaRecord{}, nil
	}
	return s.repository.ListByUser(ctx, userID)
}

func (s *PlatformQuotas) ListForAdmin(ctx context.Context, userID int64) ([]UserPlatformQuotaRecord, error) {
	if !s.Available() {
		return []UserPlatformQuotaRecord{}, nil
	}
	if _, err := s.users.GetByID(ctx, userID); err != nil {
		return nil, err
	}
	return s.repository.ListByUser(ctx, userID)
}

// ValidatePlatformQuotas 保留 nil、零值、重复项及非法金额的既有校验顺序。
func ValidatePlatformQuotas(records []UserPlatformQuotaRecord) error {
	if len(records) > len(AllowedQuotaPlatforms) {
		return apperror.BadRequest("", fmt.Sprintf("quotas length must be <= %d", len(AllowedQuotaPlatforms)))
	}
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if !IsAllowedQuotaPlatform(record.Platform) {
			return apperror.BadRequest("", "invalid platform: "+record.Platform)
		}
		if _, exists := seen[record.Platform]; exists {
			return apperror.BadRequest("", "duplicate platform: "+record.Platform)
		}
		seen[record.Platform] = struct{}{}
		for _, field := range []struct {
			name  string
			value *float64
		}{
			{"daily_limit_usd", record.DailyLimitUSD},
			{"weekly_limit_usd", record.WeeklyLimitUSD},
			{"monthly_limit_usd", record.MonthlyLimitUSD},
		} {
			if field.value == nil {
				continue
			}
			if *field.value < 0 {
				return apperror.BadRequest("", field.name+" must be >= 0")
			}
			if math.IsNaN(*field.value) || math.IsInf(*field.value, 0) {
				return apperror.BadRequest("", field.name+" must be a finite number")
			}
		}
	}
	return nil
}

func (s *PlatformQuotas) Replace(ctx context.Context, actorID, userID int64, input []UserPlatformQuotaRecord) ([]UserPlatformQuotaRecord, error) {
	if err := ValidatePlatformQuotas(input); err != nil {
		return nil, err
	}
	unlock, err := s.coordinator.Acquire(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := s.users.GetByID(ctx, userID); err != nil {
		return nil, err
	}
	before, beforeErr := s.repository.ListByUser(ctx, userID)
	if beforeErr != nil {
		s.emit(QuotaEvent{Kind: "before_read_failed", UserID: userID, Err: beforeErr})
	}
	records := append([]UserPlatformQuotaRecord(nil), input...)
	for i := range records {
		records[i].UserID = userID
	}
	if err := s.repository.UpsertForUser(ctx, userID, records); err != nil {
		return nil, err
	}
	s.emit(QuotaEvent{Kind: "updated", ActorID: actorID, UserID: userID, Before: before, Records: records, BeforeErr: beforeErr})
	for _, platform := range AllowedQuotaPlatforms {
		s.invalidate(ctx, userID, platform, "UpsertForUser")
	}
	return s.repository.ListByUser(ctx, userID)
}

func (s *PlatformQuotas) Reset(ctx context.Context, actorID, userID int64, platform, window string) ([]UserPlatformQuotaRecord, error) {
	if !IsAllowedQuotaPlatform(platform) {
		return nil, apperror.BadRequest("", "invalid platform: "+platform)
	}
	switch window {
	case "daily", "weekly", "monthly":
	default:
		return nil, apperror.BadRequest("", "invalid window: "+window)
	}
	unlock, err := s.coordinator.Acquire(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := s.users.GetByID(ctx, userID); err != nil {
		return nil, err
	}
	if err := s.repository.ResetExpiredWindow(ctx, userID, platform, window, s.now().UTC()); err != nil {
		return nil, err
	}
	s.emit(QuotaEvent{Kind: "reset", ActorID: actorID, UserID: userID, Platform: platform, Window: window})
	s.invalidate(ctx, userID, platform, "ResetExpiredWindow")
	return s.repository.ListByUser(ctx, userID)
}

func (s *PlatformQuotas) invalidate(ctx context.Context, userID int64, platform, operation string) {
	if s.cache == nil {
		return
	}
	if err := s.cache.DeleteUserPlatformQuotaCache(ctx, userID, platform); err != nil {
		s.emit(QuotaEvent{Kind: "invalidation_failed", Operation: operation, UserID: userID, Platform: platform, Err: err})
	}
}

func (s *PlatformQuotas) emit(event QuotaEvent) {
	if s.observe != nil {
		s.observe(event)
	}
}
