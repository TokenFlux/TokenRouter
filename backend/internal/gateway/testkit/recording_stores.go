package testkit

import (
	"context"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

type UsageLogStore struct {
	usagecore.UsageLogRepository

	Inserted   bool
	Err        error
	Calls      int
	LastLog    *usagecore.UsageLog
	LastCtxErr error
}

func (s *UsageLogStore) Create(ctx context.Context, log *usagecore.UsageLog) (bool, error) {
	s.Calls++
	s.LastLog = log
	s.LastCtxErr = ctx.Err()
	return s.Inserted, s.Err
}

type SettlementStore struct {
	completion.Store

	Result       *billing.UsageBillingApplyResult
	Err          error
	Calls        int
	LastCmd      *billing.UsageBillingCommand
	LastCtxErr   error
	ResolveSub   *billing.UserSubscription
	ResolveCalls int
}
type AccountLookup struct {
	Account *accountcore.Record
	Calls   int
}

func (s *AccountLookup) GetByID(_ context.Context, _ int64) (*accountcore.Record, error) {
	s.Calls++
	return s.Account, nil
}
func (s *SettlementStore) Apply(ctx context.Context, cmd *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error) {
	s.Calls++
	s.LastCmd = cmd
	s.LastCtxErr = ctx.Err()
	if s.Err != nil {
		return nil, s.Err
	}
	if s.Result != nil {
		return s.Result, nil
	}
	result := &billing.UsageBillingApplyResult{Applied: true}
	if cmd != nil {
		switch cmd.BillingType {
		case usagecore.BillingTypeSubscription:
			result.SubscriptionAmountUSD = cmd.BillableAmountUSD
		default:
			result.BalanceAmountUSD = cmd.BillableAmountUSD
		}
	}
	return result, nil
}
func (s *SettlementStore) ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*billing.UserSubscription, error) {
	s.ResolveCalls++
	return s.ResolveSub, nil
}

type QuotaCache struct {
	Entry     *billing.UserPlatformQuotaCacheEntry
	GetCalls  []QuotaCacheGet
	IncrCalls []QuotaCacheIncrement
}
type QuotaCacheGet struct {
	UserID   int64
	Platform string
}
type QuotaCacheIncrement struct {
	UserID   int64
	Platform string
	Cost     float64
}

func (s *QuotaCache) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	return 0, nil
}
func (s *QuotaCache) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	return nil
}
func (s *QuotaCache) DeductUserBalance(ctx context.Context, userID int64, amount float64) error {
	return nil
}
func (s *QuotaCache) InvalidateUserBalance(ctx context.Context, userID int64) error {
	return nil
}
func (s *QuotaCache) GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*billing.APIKeyRateLimitCacheData, error) {
	return nil, nil
}
func (s *QuotaCache) SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *billing.APIKeyRateLimitCacheData) error {
	return nil
}
func (s *QuotaCache) UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error {
	return nil
}
func (s *QuotaCache) InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error {
	return nil
}
func (s *QuotaCache) GetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) (*billing.UserPlatformQuotaCacheEntry, bool, error) {
	s.GetCalls = append(s.GetCalls, QuotaCacheGet{UserID: userID, Platform: platform})
	if s.Entry == nil {
		return nil, false, nil
	}
	return s.Entry, true, nil
}
func (s *QuotaCache) SetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string, entry *billing.UserPlatformQuotaCacheEntry, ttl time.Duration) error {
	s.Entry = entry
	return nil
}
func (s *QuotaCache) DeleteUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) error {
	s.Entry = nil
	return nil
}
func (s *QuotaCache) IncrUserPlatformQuotaUsageCache(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) error {
	s.IncrCalls = append(s.IncrCalls, QuotaCacheIncrement{UserID: userID, Platform: platform, Cost: cost})
	return nil
}
func (s *QuotaCache) PopDirtyUserPlatformQuotaKeys(ctx context.Context, n int) ([]billing.UserPlatformQuotaKey, error) {
	return nil, nil
}
func (s *QuotaCache) ReaddDirtyUserPlatformQuotaKeys(ctx context.Context, keys []billing.UserPlatformQuotaKey) error {
	return nil
}
func (s *QuotaCache) BatchGetUserPlatformQuotaCache(ctx context.Context, keys []billing.UserPlatformQuotaKey) ([]*billing.UserPlatformQuotaCacheEntry, error) {
	return make([]*billing.UserPlatformQuotaCacheEntry, len(keys)), nil
}

type PlatformQuotaStore struct{}

func (s *PlatformQuotaStore) GetByUserPlatform(ctx context.Context, userID int64, platform string) (*billing.UserPlatformQuotaRecord, error) {
	return nil, nil
}
func (s *PlatformQuotaStore) BulkInsertInitial(ctx context.Context, records []billing.UserPlatformQuotaRecord) error {
	return nil
}
func (s *PlatformQuotaStore) IncrementUsageWithReset(ctx context.Context, userID int64, platform string, cost float64, now time.Time) error {
	return nil
}
func (s *PlatformQuotaStore) ListByUser(ctx context.Context, userID int64) ([]billing.UserPlatformQuotaRecord, error) {
	return nil, nil
}
func (s *PlatformQuotaStore) UpsertForUser(ctx context.Context, userID int64, records []billing.UserPlatformQuotaRecord) error {
	return nil
}
func (s *PlatformQuotaStore) ResetExpiredWindow(ctx context.Context, userID int64, platform string, window string, newStart time.Time) error {
	return nil
}
func (s *PlatformQuotaStore) BatchSnapshotUsage(ctx context.Context, snapshots []billing.UserPlatformQuotaSnapshot, now time.Time) error {
	return nil
}

type UserStore struct {
	identity.UserRepository

	DeductCalls int
	DeductErr   error
	LastAmount  float64
	LastCtxErr  error
}

func (s *UserStore) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	s.DeductCalls++
	s.LastAmount = amount
	s.LastCtxErr = ctx.Err()
	if s.DeductErr != nil {
		return 0, s.DeductErr
	}
	return amount, nil
}
func (s *UserStore) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}
func (s *UserStore) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

type SubscriptionStore struct {
	billing.UserSubscriptionRepository

	IncrementCalls int
	IncrementErr   error
	LastCtxErr     error
}

func (s *SubscriptionStore) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	s.IncrementCalls++
	s.LastCtxErr = ctx.Err()
	return s.IncrementErr
}

type KeyQuotaUpdater struct {
	QuotaCalls          int
	RateLimitCalls      int
	Err                 error
	LastAmount          float64
	LastQuotaCtxErr     error
	LastRateLimitCtxErr error
}

func (s *KeyQuotaUpdater) UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error {
	s.QuotaCalls++
	s.LastAmount = cost
	s.LastQuotaCtxErr = ctx.Err()
	return s.Err
}
func (s *KeyQuotaUpdater) UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error {
	s.RateLimitCalls++
	s.LastAmount = cost
	s.LastRateLimitCtxErr = ctx.Err()
	return s.Err
}

type GroupRateStore struct {
	billing.UserGroupRateRepository

	Rate  *float64
	Err   error
	Calls int
}

func (s *GroupRateStore) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.Calls++
	if s.Err != nil {
		return nil, s.Err
	}
	return s.Rate, nil
}
