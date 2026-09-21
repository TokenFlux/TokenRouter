// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	context "context"
	rand "crypto/rand"
	hex "encoding/hex"
	errors "errors"
	fmt "fmt"
	strings "strings"
	time "time"

	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

var (
	ErrRedeemCodeExpired     = apperror.BadRequest("REDEEM_CODE_EXPIRED", "redeem code has expired")
	ErrRedeemCodeMaxUsed     = apperror.Conflict("REDEEM_CODE_MAX_USED", "redeem code has reached maximum uses")
	ErrRedeemCodeAlreadyUsed = apperror.Conflict("REDEEM_CODE_ALREADY_USED", "you have already used this redeem code")
	ErrRedeemRateLimited     = apperror.TooManyRequests("REDEEM_RATE_LIMITED", "too many failed attempts, please try again later")
	ErrRedeemCodeLocked      = apperror.Conflict("REDEEM_CODE_LOCKED", "redeem code is being processed, please try again")
)

const (
	redeemMaxErrorsPerHour  = 20
	redeemRateLimitDuration = time.Hour
	redeemLockDuration      = 10 * time.Second // 锁超时时间，防止死锁
)

type ctxKeySkipRedeemAffiliate struct{}

// ContextSkipRedeemAffiliate 返回跳过兑换层返利的上下文，支付订单会在订单层做带审计去重的返利。
func ContextSkipRedeemAffiliate(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeySkipRedeemAffiliate{}, true)
}

// RedeemTransactions 隐藏兑换权益与 usage/次数提交的数据库事务。
type RedeemTransactions interface {
	Within(context.Context, func(context.Context) error) error
	ApplyBalance(context.Context, int64, float64) error
	ApplyConcurrency(context.Context, int64, int) error
}
type RedeemAuthInvalidator interface{ InvalidateAuthCacheByUserID(context.Context, int64) }
type RedeemAffiliate interface {
	IsEnabled(context.Context) bool
	AccrueInviteRebate(context.Context, int64, float64) (float64, error)
}
type RedeemRuntime struct {
	Now        func() time.Time
	Observe    Observe
	Background func(string, func())
}

// RedeemService 唯一拥有兑换规则，提交完成后才执行原有失效及尽力返利。
type RedeemService struct {
	redeemRepo           RedeemCodeRepository
	userRepo             BalanceReader
	subscriptionService  *SubscriptionService
	cache                RedeemCache
	billingCacheService  *Eligibility
	transactions         RedeemTransactions
	authCacheInvalidator RedeemAuthInvalidator
	affiliateService     RedeemAffiliate
	runtime              RedeemRuntime
}

func NewRedeemService(repo RedeemCodeRepository, users BalanceReader, subs *SubscriptionService, cache RedeemCache, eligibility *Eligibility, transactions RedeemTransactions, auth RedeemAuthInvalidator, affiliate RedeemAffiliate, runtime RedeemRuntime) *RedeemService {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return &RedeemService{redeemRepo: repo, userRepo: users, subscriptionService: subs, cache: cache, billingCacheService: eligibility, transactions: transactions, authCacheInvalidator: auth, affiliateService: affiliate, runtime: runtime}
}

// GenerateRandomCode 生成随机兑换码
func (s *RedeemService) GenerateRandomCode() (string, error) {
	// 生成16字节随机数据
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	// 转换为十六进制字符串
	code := hex.EncodeToString(bytes)

	// 格式化为 XXXX-XXXX-XXXX-XXXX 格式
	parts := []string{
		strings.ToUpper(code[0:8]),
		strings.ToUpper(code[8:16]),
		strings.ToUpper(code[16:24]),
		strings.ToUpper(code[24:32]),
	}

	return strings.Join(parts, "-"), nil
}

// GenerateCodes 批量生成兑换码
func (s *RedeemService) GenerateCodes(ctx context.Context, req GenerateCodesRequest) ([]RedeemCode, error) {
	if req.Count <= 0 {
		return nil, apperror.BadRequest("REDEEM_CODE_COUNT_INVALID", "count must be greater than 0")
	}

	// 邀请码类型不需要数值，其他类型需要非零值（支持负数用于退款）
	if req.Type != RedeemTypeInvitation && req.Value == 0 {
		return nil, apperror.BadRequest("REDEEM_CODE_VALUE_INVALID", "value must not be zero")
	}

	if req.Count > 1000 {
		return nil, apperror.BadRequest("REDEEM_CODE_COUNT_TOO_LARGE", "cannot generate more than 1000 codes at once")
	}

	codeType := req.Type
	if codeType == "" {
		codeType = RedeemTypeBalance
	}

	maxUses := 1
	if req.MaxUses != nil {
		if *req.MaxUses < 0 {
			return nil, apperror.BadRequest("REDEEM_CODE_MAX_USES_INVALID", "max_uses must be greater than or equal to 0")
		}
		maxUses = *req.MaxUses
	}

	customCode := strings.TrimSpace(req.Code)
	if customCode != "" {
		if req.Count != 1 {
			return nil, apperror.BadRequest("REDEEM_CODE_CUSTOM_COUNT_INVALID", "count must be 1 when code is provided")
		}
		if len(customCode) > 32 {
			return nil, apperror.BadRequest("REDEEM_CODE_TOO_LONG", "code must be at most 32 characters")
		}
	}

	// 邀请码类型的 value 设为 0
	value := req.Value
	if codeType == RedeemTypeInvitation {
		value = 0
		maxUses = 1
	}

	codes := make([]RedeemCode, 0, req.Count)
	for i := 0; i < req.Count; i++ {
		codeValue := customCode
		if codeValue == "" {
			code, err := s.GenerateRandomCode()
			if err != nil {
				return nil, fmt.Errorf("generate code: %w", err)
			}
			codeValue = code
		}

		codes = append(codes, RedeemCode{
			Code:      codeValue,
			Type:      codeType,
			Value:     value,
			Status:    StatusUnused,
			MaxUses:   maxUses,
			ExpiresAt: req.ExpiresAt,
		})
	}

	// 批量插入
	if err := s.redeemRepo.CreateBatch(ctx, codes); err != nil {
		return nil, fmt.Errorf("create batch codes: %w", err)
	}

	return codes, nil
}

// CreateCode creates a redeem code with caller-provided code value.
// It is primarily used by admin integrations that require an external order ID
// to be mapped to a deterministic redeem code.
func (s *RedeemService) CreateCode(ctx context.Context, code *RedeemCode) error {
	if code == nil {
		return errors.New("redeem code is required")
	}
	code.Code = strings.TrimSpace(code.Code)
	if code.Code == "" {
		return errors.New("code is required")
	}
	if code.Type == "" {
		code.Type = RedeemTypeBalance
	}
	if code.Type != RedeemTypeInvitation && code.Value == 0 {
		return errors.New("value must not be zero")
	}
	if code.Status == "" {
		code.Status = StatusUnused
	}
	if code.MaxUses <= 0 {
		code.MaxUses = 1
	}
	if code.UsedCount < 0 {
		code.UsedCount = 0
	}
	if code.Type == RedeemTypeInvitation {
		code.MaxUses = 1
	}
	code.Status = code.PersistedStatus()

	if err := s.redeemRepo.Create(ctx, code); err != nil {
		return fmt.Errorf("create redeem code: %w", err)
	}
	return nil
}

func (s *RedeemService) BatchUpdate(ctx context.Context, input *RedeemCodeBatchUpdateInput) (*RedeemCodeBatchUpdateResult, error) {
	if input == nil {
		return nil, apperror.BadRequest("REDEEM_CODE_BATCH_UPDATE_INVALID", "batch update input is required")
	}
	if len(input.IDs) == 0 {
		return nil, apperror.BadRequest("REDEEM_CODE_BATCH_UPDATE_IDS_REQUIRED", "ids are required")
	}
	if !input.Fields.HasChanges() {
		return nil, apperror.BadRequest("REDEEM_CODE_BATCH_UPDATE_EMPTY", "at least one field must be selected")
	}
	if input.Fields.HasCoreFieldChanges() {
		return nil, apperror.BadRequest("REDEEM_CODE_CORE_FIELDS_IMMUTABLE", "type, value, max_uses and plan_id cannot be batch updated")
	}

	ids := make([]int64, 0, len(input.IDs))
	seen := make(map[int64]struct{}, len(input.IDs))
	for _, id := range input.IDs {
		if id <= 0 {
			return nil, apperror.BadRequest("REDEEM_CODE_BATCH_UPDATE_INVALID_ID", "ids must be positive")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, apperror.BadRequest("REDEEM_CODE_BATCH_UPDATE_IDS_REQUIRED", "ids are required")
	}

	if input.Fields.Status != nil {
		switch *input.Fields.Status {
		case StatusUnused, StatusDisabled:
		default:
			return nil, apperror.BadRequest("REDEEM_CODE_STATUS_INVALID", "status must be unused or disabled")
		}
	}
	if input.Fields.ExpiresAt.Set && input.Fields.ExpiresAt.Value != nil {
		expiresAt := input.Fields.ExpiresAt.Value.UTC()
		if !expiresAt.After(s.runtime.Now().UTC()) {
			return nil, apperror.BadRequest("REDEEM_CODE_EXPIRES_AT_INVALID", "expires_at must be in the future")
		}
		input.Fields.ExpiresAt.Value = &expiresAt
	}

	updated, err := s.redeemRepo.BatchUpdate(ctx, ids, input.Fields)
	if err != nil {
		return nil, err
	}
	return &RedeemCodeBatchUpdateResult{Updated: updated}, nil
}

// checkRedeemRateLimit 检查用户兑换错误次数是否超限
func (s *RedeemService) checkRedeemRateLimit(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}

	count, err := s.cache.GetRedeemAttemptCount(ctx, userID)
	if err != nil {
		// Redis 出错时不阻止用户操作
		return nil
	}

	if count >= redeemMaxErrorsPerHour {
		return ErrRedeemRateLimited
	}

	return nil
}

// incrementRedeemErrorCount 增加用户兑换错误计数
func (s *RedeemService) incrementRedeemErrorCount(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}

	_ = s.cache.IncrementRedeemAttemptCount(ctx, userID)
}

// acquireRedeemLock 尝试获取兑换码的分布式锁
// 返回 true 表示获取成功，false 表示锁已被占用
func (s *RedeemService) acquireRedeemLock(ctx context.Context, code string) bool {
	if s.cache == nil {
		return true // 无 Redis 时降级为不加锁
	}

	ok, err := s.cache.AcquireRedeemLock(ctx, code, redeemLockDuration)
	if err != nil {
		// Redis 出错时不阻止操作，依赖数据库层面的状态检查
		return true
	}
	return ok
}

// releaseRedeemLock 释放兑换码的分布式锁
func (s *RedeemService) releaseRedeemLock(ctx context.Context, code string) {
	if s.cache == nil {
		return
	}

	_ = s.cache.ReleaseRedeemLock(ctx, code)
}

// unsupportedRedeemTypeError 将邀请码和未知类型统一转换为客户端可处理的请求错误。
func unsupportedRedeemTypeError(codeType string) error {
	if codeType == RedeemTypeInvitation {
		return apperror.BadRequest("REDEEM_CODE_UNSUPPORTED_TYPE", "invitation codes can only be used during registration")
	}
	return apperror.BadRequest("REDEEM_CODE_UNSUPPORTED_TYPE", fmt.Sprintf("unsupported redeem type: %s", codeType))
}

// Redeem 使用兑换码
func (s *RedeemService) Redeem(ctx context.Context, userID int64, code string) (*RedeemCode, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, ErrRedeemCodeNotFound
	}

	// 检查限流
	if err := s.checkRedeemRateLimit(ctx, userID); err != nil {
		return nil, err
	}

	// 获取分布式锁，防止同一兑换码并发使用
	if !s.acquireRedeemLock(ctx, code) {
		return nil, ErrRedeemCodeLocked
	}
	defer s.releaseRedeemLock(ctx, code)
	var redeemCode *RedeemCode
	err := s.transactions.Within(ctx, func(txCtx context.Context) error {
		var e error
		redeemCode, e = s.redeemInTx(ctx, txCtx, userID, code)
		return e
	})
	if err != nil {
		return nil, err
	}

	// 事务提交成功后失效缓存
	s.invalidateRedeemCaches(ctx, userID, redeemCode)

	// 余额类正数兑换码触发邀请返利；支付订单会传入跳过标记，避免双重返利。
	if redeemCode.Type == RedeemTypeBalance && redeemCode.Value > 0 {
		s.tryAccrueAffiliateRebateForRedeem(ctx, userID, redeemCode.Value)
	}

	// 重新获取更新后的兑换码
	redeemCode, err = s.redeemRepo.GetByID(ctx, redeemCode.ID)
	if err != nil {
		return nil, fmt.Errorf("get updated redeem code: %w", err)
	}

	return redeemCode, nil
}

// redeemInTx 的全部写入使用事务 Adapter 提供的同一个 context；不在此发布副作用。
func (s *RedeemService) redeemInTx(ctx, txCtx context.Context, userID int64, code string) (*RedeemCode, error) {

	redeemCode, err := s.redeemRepo.GetByCodeForUpdate(txCtx, code)
	if err != nil {
		if errors.Is(err, ErrRedeemCodeNotFound) {
			s.incrementRedeemErrorCount(ctx, userID)
			return nil, ErrRedeemCodeNotFound
		}
		return nil, fmt.Errorf("get redeem code: %w", err)
	}

	if err := s.validateRedeemCodeForUser(txCtx, redeemCode, userID); err != nil {
		s.incrementRedeemErrorCount(ctx, userID)
		return nil, err
	}

	// 邀请码只允许在注册流程使用，普通兑换接口仅接受能直接发放权益的类型。
	switch redeemCode.Type {
	case RedeemTypeBalance, RedeemTypeConcurrency:
	case RedeemTypeSubscription:
		if redeemCode.PlanID == nil || *redeemCode.PlanID <= 0 {
			return nil, apperror.BadRequest("REDEEM_CODE_INVALID", "invalid subscription redeem code: missing plan_id")
		}
	default:
		return nil, unsupportedRedeemTypeError(redeemCode.Type)
	}

	_, err = s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// 执行兑换逻辑（兑换码已被锁定，此时可安全操作）
	switch redeemCode.Type {
	case RedeemTypeBalance:
		if err := s.transactions.ApplyBalance(txCtx, userID, redeemCode.Value); err != nil {
			if errors.Is(err, ErrRedeemBalanceUnsupported) {
				return nil, err
			}
			return nil, fmt.Errorf("update user balance: %w", err)
		}
	case RedeemTypeConcurrency:
		if err := s.transactions.ApplyConcurrency(txCtx, userID, int(redeemCode.Value)); err != nil {
			if errors.Is(err, ErrRedeemConcurrencyUnsupported) {
				return nil, err
			}
			return nil, fmt.Errorf("update user concurrency: %w", err)
		}

	case RedeemTypeSubscription:
		_, _, err := s.subscriptionService.AssignOrExtendSubscription(txCtx, &AssignSubscriptionInput{
			UserID:     userID,
			PlanID:     *redeemCode.PlanID,
			AssignedBy: 0, // 系统分配
			Notes:      fmt.Sprintf("通过兑换码 %s 兑换", redeemCode.Code),
		})
		if err != nil {
			return nil, fmt.Errorf("assign or extend subscription: %w", err)
		}

	default:
		return nil, unsupportedRedeemTypeError(redeemCode.Type)
	}

	usageTime := s.runtime.Now()
	if err := s.redeemRepo.CreateUsage(txCtx, &RedeemCodeUsage{
		RedeemCodeID: redeemCode.ID,
		UserID:       userID,
		UsedAt:       usageTime,
	}); err != nil {
		return nil, fmt.Errorf("create redeem usage: %w", err)
	}

	redeemCode.UsedCount++
	redeemCode.UsedBy = &userID
	redeemCode.UsedAt = &usageTime
	redeemCode.Status = redeemCode.PersistedStatus()

	if err := s.redeemRepo.Update(txCtx, redeemCode); err != nil {
		return nil, fmt.Errorf("update redeem code usage snapshot: %w", err)
	}

	return redeemCode, nil
}

// invalidateRedeemCaches 失效兑换相关的缓存
func (s *RedeemService) invalidateRedeemCaches(ctx context.Context, userID int64, redeemCode *RedeemCode) {
	switch redeemCode.Type {
	case RedeemTypeBalance:
		if s.authCacheInvalidator != nil {
			s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
		if s.billingCacheService == nil {
			return
		}
		s.runBackground("service/redeem_service.go:invalidateRedeemCaches", func() {
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = s.billingCacheService.InvalidateUserBalance(cacheCtx, userID)
		})
	case RedeemTypeConcurrency:
		if s.authCacheInvalidator != nil {
			s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
		if s.billingCacheService == nil {
			return
		}
	case RedeemTypeSubscription:
		if s.authCacheInvalidator != nil {
			s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, userID)
		}
		return
	}
}

func (s *RedeemService) tryAccrueAffiliateRebateForRedeem(ctx context.Context, userID int64, amount float64) {
	if ctx.Value(ctxKeySkipRedeemAffiliate{}) != nil {
		return
	}
	if s.affiliateService == nil || !s.affiliateService.IsEnabled(ctx) {
		return
	}
	rebate, err := s.affiliateService.AccrueInviteRebate(ctx, userID, amount)
	if err != nil {
		s.runtime.Observe.Printf("service.redeem", "[Redeem] affiliate rebate failed for user %d amount %.2f: %v", userID, amount, err)
		return
	}
	if rebate > 0 {
		s.runtime.Observe.Printf("service.redeem", "[Redeem] affiliate rebate accrued %.8f for inviter of user %d", rebate, userID)
	}
}

// GetByID 根据ID获取兑换码
func (s *RedeemService) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	code, err := s.redeemRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get redeem code: %w", err)
	}
	return code, nil
}

// GetByCode 根据Code获取兑换码
func (s *RedeemService) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	redeemCode, err := s.redeemRepo.GetByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("get redeem code: %w", err)
	}
	return redeemCode, nil
}

// List 获取兑换码列表（管理员功能）
func (s *RedeemService) List(ctx context.Context, params pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	codes, pagination, err := s.redeemRepo.List(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("list redeem codes: %w", err)
	}
	return codes, pagination, nil
}

// Delete 删除兑换码（管理员功能）
func (s *RedeemService) Delete(ctx context.Context, id int64) error {
	// 检查兑换码是否存在
	code, err := s.redeemRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get redeem code: %w", err)
	}

	// 仅允许删除从未被兑换过的兑换码
	if !code.CanDelete() {
		return apperror.Conflict("REDEEM_CODE_DELETE_USED", "cannot delete redeem code that has usage records")
	}

	if err := s.redeemRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete redeem code: %w", err)
	}

	return nil
}

func (s *RedeemService) validateRedeemCodeForUser(ctx context.Context, redeemCode *RedeemCode, userID int64) error {
	if redeemCode == nil {
		return ErrRedeemCodeNotFound
	}
	if redeemCode.IsExpired() {
		return ErrRedeemCodeExpired
	}

	existingUsage, err := s.redeemRepo.GetUsageByRedeemCodeAndUser(ctx, redeemCode.ID, userID)
	if err != nil {
		return fmt.Errorf("check redeem usage: %w", err)
	}
	if existingUsage != nil {
		return ErrRedeemCodeAlreadyUsed
	}
	if !redeemCode.HasRemainingUses() {
		return ErrRedeemCodeMaxUsed
	}
	return nil
}

// GetStats 获取兑换码统计信息
func (s *RedeemService) GetStats(ctx context.Context) (map[string]any, error) {
	// TODO: 实现统计逻辑
	// 统计未使用、已使用的兑换码数量
	// 统计总面值等

	stats := map[string]any{
		"total_codes":  0,
		"unused_codes": 0,
		"used_codes":   0,
		"total_value":  0.0,
	}

	return stats, nil
}

// GetUserHistory 获取用户的兑换历史
func (s *RedeemService) GetUserHistory(ctx context.Context, userID int64, limit int) ([]RedeemCode, error) {
	codes, err := s.redeemRepo.ListByUser(ctx, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("get user redeem history: %w", err)
	}
	return codes, nil
}

// 不支持负向原子权益的兼容仓储保留原错误文本。
var ErrRedeemBalanceUnsupported = errors.New("user repository does not support atomic redeem balance adjustments")
var ErrRedeemConcurrencyUnsupported = errors.New("user repository does not support atomic redeem concurrency adjustments")

func (s *RedeemService) runBackground(name string, fn func()) {
	if s.runtime.Background == nil {
		fn()
		return
	}
	s.runtime.Background(name, fn)
}
