package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// redeem_service.go grants redemption entitlements and records credential
// consumption within a transaction.
// A redemption ties entitlement issuance to a credential's individual usage
// record and lifetime count. Advisory cache limits and code exclusion run
// before the transaction, while the transaction locks the code, checks
// expiry/history/capacity and required payment eligibility, then writes the
// selected grant and usage snapshot. This grant path does not independently
// reject a disabled status, so its checks should not be confused with the
// separate CanUse predicate.
//
// Successful completion precedes authentication/balance invalidation and
// best-effort affiliate accrual. Payment workflows can mark rebate ownership
// to keep their own audited accrual path. Response reloading happens after
// these steps and can fail after issuance succeeded; cache and rebate failures
// have their own suppression rules and do not establish transaction rollback.

const (
	// redeemMaxErrorsPerHour is the saved failed-attempt count ceiling used by
	// admission. Counter timing belongs to the cache implementation, whose Redis
	// lease is twenty-four hours.
	redeemMaxErrorsPerHour = 20

	// redeemLockDuration bounds the advisory code exclusion key at ten seconds.
	// The database transaction separately locks and updates durable redemption
	// state during the attempt.
	redeemLockDuration = 10 * time.Second // Expiry releases abandoned cache locks.
)

var (
	// Credential persistence and usage errors distinguish duplicate, missing and
	// unavailable records so callers retain the failed grant-state reason.
	ErrRedeemCodeExists   = apperror.Conflict("REDEEM_CODE_EXISTS", "redeem code already exists")
	ErrRedeemCodeNotFound = apperror.NotFound("REDEEM_CODE_NOT_FOUND", "redeem code not found")
	ErrRedeemCodeUsed     = apperror.Conflict("REDEEM_CODE_USED", "redeem code already used")

	// Eligibility errors identify payment, expiry, use capacity, prior recipient
	// usage and advisory admission failures before the selected entitlement write
	// is attempted.
	ErrRedeemPaymentRequired               = apperror.Forbidden("REDEEM_PAYMENT_REQUIRED", "a successful payment is required to redeem this code")
	ErrRedeemPaymentRequirementUnsupported = apperror.BadRequest("REDEEM_PAYMENT_REQUIREMENT_UNSUPPORTED", "invitation codes cannot require a payment")
	ErrRedeemCodeExpired                   = apperror.BadRequest("REDEEM_CODE_EXPIRED", "redeem code has expired")
	ErrRedeemCodeMaxUsed                   = apperror.Conflict("REDEEM_CODE_MAX_USED", "redeem code has reached maximum uses")
	ErrRedeemCodeAlreadyUsed               = apperror.Conflict("REDEEM_CODE_ALREADY_USED", "you have already used this redeem code")
	ErrRedeemRateLimited                   = apperror.TooManyRequests("REDEEM_RATE_LIMITED", "too many failed attempts, please try again later")
	ErrRedeemCodeLocked                    = apperror.Conflict("REDEEM_CODE_LOCKED", "redeem code is being processed, please try again")

	// Negative entitlement writes require the injected floor capability; these
	// errors identify the missing balance or concurrency operation before its
	// grant mutation can run.
	ErrRedeemBalanceUnsupported     = errors.New("user repository does not support atomic redeem balance adjustments")
	ErrRedeemConcurrencyUnsupported = errors.New("user repository does not support atomic redeem concurrency adjustments")
)

// ctxKeySkipRedeemAffiliate identifies payment workflow ownership of the
// redemption rebate step.
type ctxKeySkipRedeemAffiliate struct{}

// RedeemTransactions combines transaction execution and entitlement writes
// used by complete redemption.
type RedeemTransactions interface {
	// Within runs the callback on its transaction context and completes the owned
	// grant transaction after successful writes. Callers keep entitlement and
	// usage operations on that context; the adapter defines transaction creation
	// and failure handling.
	Within(context.Context, func(context.Context) error) error
	// ApplyBalance mutates user funds on the supplied transaction context for the
	// selected redemption amount. Negative amounts require the floor capability,
	// and caller transaction completion coordinates the benefit with usage
	// history.
	ApplyBalance(context.Context, int64, float64) error
	// ApplyConcurrency mutates user concurrency on the supplied transaction
	// context for the selected redemption delta. Negative amounts require the
	// floor capability, and caller transaction completion coordinates the benefit
	// with usage history.
	ApplyConcurrency(context.Context, int64, int) error
}

type (
	// RedeemAuthInvalidator requests user authentication snapshot refresh after
	// completed entitlement changes.
	RedeemAuthInvalidator interface {
		// InvalidateAuthCacheByUserID requests removal of the user's authentication
		// snapshots after a supported grant completes. The caller supplies the grant
		// context; this void callback owns its observation/publication handling.
		InvalidateAuthCacheByUserID(context.Context, int64)
	}
	// RedeemAffiliate supplies enablement and rebate accrual for completed
	// positive balance grants.
	RedeemAffiliate interface {
		// IsEnabled reports whether affiliate accrual is enabled for the supplied
		// redemption context.
		IsEnabled(context.Context) bool
		// AccrueInviteRebate attempts a rebate for the redeeming user and positive
		// grant amount, returning the accrued value. Errors remain available to the
		// redemption helper for observation after grant completion.
		AccrueInviteRebate(context.Context, int64, float64) (float64, error)
	}
)

// RedeemRuntime supplies payment-history eligibility, grant timestamps,
// observation and post-grant dispatch.
type RedeemRuntime struct {
	// HasPaidOrder is required when a code carries RequiresPayment. It receives
	// txCtx before issuance, and an absent callback or lookup error stops that
	// grant attempt.
	HasPaidOrder func(context.Context, int64) (bool, error) // qualifying payment history lookup
	// Now supplies BatchUpdate future-expiry validation and successful usage
	// timestamps. Credential IsExpired reads wall-clock time independently;
	// NewRedeemService defaults this callback to time.Now when nil.
	Now func() time.Time // grant and validation observation clock
	// Observe receives rebate success/failure messages through its nil-safe
	// Printf method. A nil observer keeps those post-grant outcomes silent
	// without changing the redemption result.
	Observe Observe // redemption rebate event observer
	// Background controls balance-cache invalidation dispatch. Nil invokes the
	// supplied callback synchronously; captured values remain usable until the
	// configured runner finishes that callback.
	Background func(string, func()) // optional post-grant callback runner
}

// RedeemService coordinates credential validation, transactional grants and
// post-completion cache/rebate work.
type RedeemService struct {
	redeemRepo RedeemCodeRepository // credential and usage persistence
	// userRepo is required by redeemInTx to verify the recipient using the
	// original ctx. The returned summary is ignored; read errors stop the grant
	// attempt.
	userRepo BalanceReader // grant recipient existence reader
	// subscriptionService is needed for subscription credentials after plan
	// validation. Balance and concurrency paths use their respective transaction
	// writers instead of this service.
	subscriptionService *SubscriptionService // subscription grant mutation service
	// cache is optional advisory state. Absent or failed counter/lock reads
	// permit database processing, while increments/releases are best-effort and
	// do not replace grant errors.
	cache RedeemCache // optional advisory admission cache
	// billingCacheService is optional and used after successful balance grants.
	// Its invalidation gets an independent background budget; other entitlement
	// types do not schedule this balance action.
	billingCacheService *Eligibility // optional balance cache invalidation service
	// transactions supplies the grant callback context and balance/concurrency
	// writes. The service uses that context for benefit, usage and latest code
	// summary writes before post-completion effects.
	transactions RedeemTransactions // grant transaction and entitlement writes
	// authCacheInvalidator is optional. Supported balance, concurrency and
	// subscription grants invoke it after successful transaction completion,
	// using the caller's supplied context.
	authCacheInvalidator RedeemAuthInvalidator // optional user auth snapshot invalidator
	// affiliateService is optional and consulted for positive balance grants
	// after completion. Context rebate-ownership markers or disabled support skip
	// its accrual attempt.
	affiliateService RedeemAffiliate // optional positive-balance rebate source
	runtime          RedeemRuntime   // payment clock observation and dispatch
}

// ContextSkipRedeemAffiliate returns a derived context marking rebate
// ownership by the payment workflow. The caller passes a usable parent
// context; any non-nil marker value causes the redemption rebate helper to
// skip accrual.
func ContextSkipRedeemAffiliate(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeySkipRedeemAffiliate{}, true)
}

// NewRedeemService binds redemption dependencies and defaults a nil Now
// callback to time.Now. Cache, eligibility invalidation, auth invalidation and
// affiliate support are optional; grant execution needs repository, user
// reader, transactions and subscription service for that type. Nil Background
// runs callbacks synchronously.
func NewRedeemService(repo RedeemCodeRepository, users BalanceReader, subs *SubscriptionService, cache RedeemCache, eligibility *Eligibility, transactions RedeemTransactions, auth RedeemAuthInvalidator, affiliate RedeemAffiliate, runtime RedeemRuntime) *RedeemService {
	if runtime.Now == nil {
		runtime.Now = time.Now
	}
	return &RedeemService{redeemRepo: repo, userRepo: users, subscriptionService: subs, cache: cache, billingCacheService: eligibility, transactions: transactions, authCacheInvalidator: auth, affiliateService: affiliate, runtime: runtime}
}

// CreateCode normalizes and persists a non-nil deterministic credential record
// in place. It trims code, defaults empty type/status, replaces nonpositive
// capacity with one and negative use count with zero. Subscription credentials
// require a positive plan identity; other non-invitation types require nonzero Value.
//
// Invitations reject payment requirements and retain one use. PersistedStatus
// derives the final label before creation, so this path differs from
// administrator generation that accepts unlimited zero capacity. Callers
// supply supported entitlement terms and handle repository
// validation/uniqueness failures; this method does not issue a grant.
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
	if code.Type != RedeemTypeInvitation && code.Type != RedeemTypeSubscription && code.Value == 0 {
		return errors.New("value must not be zero")
	}
	if code.Type == RedeemTypeSubscription && (code.PlanID == nil || *code.PlanID <= 0) {
		return errors.New("subscription plan_id is required")
	}
	if code.ValidityDays != nil && (code.Type != RedeemTypeSubscription || *code.ValidityDays < 1 || *code.ValidityDays > MaxValidityDays) {
		return errors.New("subscription validity_days is outside the supported range")
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
		if code.RequiresPayment {
			return ErrRedeemPaymentRequirementUnsupported
		}
		code.MaxUses = 1
	}
	code.Status = code.PersistedStatus()

	if err := s.redeemRepo.Create(ctx, code); err != nil {
		return fmt.Errorf("create redeem code: %w", err)
	}
	return nil
}

// BatchUpdate validates common changes and persists distinct positive target
// IDs. Nil/empty inputs and core entitlement changes are rejected; supplied
// status must be unused or disabled, and a supplied non-null expiry must be
// after the configured current instant.
//
// Valid expiry is replaced in the input by its UTC value before persistence.
// The presence flag retains clear versus omission, while the repository owns
// target/used-code checks and transaction scope. The result reports the
// adapter's affected count after success, with input normalization retained
// even if a later write fails.
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
	// A supplied timestamp is checked as an instant and stored in UTC form; the
	// separate presence flag keeps null clearing distinct from omission.
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

// checkRedeemRateLimit rejects a saved failure count at or above twenty. An
// absent cache or read error permits progress; the cache implementation owns
// the count's expiry period, which the Redis adapter renews for twenty-four
// hours.
func (s *RedeemService) checkRedeemRateLimit(ctx context.Context, userID int64) error {
	if s.cache == nil {
		return nil
	}

	count, err := s.cache.GetRedeemAttemptCount(ctx, userID)
	if err != nil {
		// A failed advisory count read supplies no blocking evidence; continue to
		// the database validation rather than replacing it with a cache failure.
		return nil
	}

	// The saved count reaches this admission ceiling; expiry and renewal of that
	// count are owned by the configured cache.
	if count >= redeemMaxErrorsPerHour {
		return ErrRedeemRateLimited
	}

	return nil
}

// incrementRedeemErrorCount requests advisory failed-attempt recording when a
// cache exists. Cache errors are discarded so they cannot replace the
// credential validation failure being returned; the cache owns expiry and
// partial/retry effects.
func (s *RedeemService) incrementRedeemErrorCount(ctx context.Context, userID int64) {
	if s.cache == nil {
		return
	}

	// Record this advisory failure best-effort; a cache write error cannot
	// replace the credential validation result being reported to the recipient.
	_ = s.cache.IncrementRedeemAttemptCount(ctx, userID)
}

// acquireRedeemLock attempts the exact code's ten-second advisory exclusion
// key. A held key returns false; absent cache or cache error returns true so
// the database code-row lock and later checks can govern this attempt.
func (s *RedeemService) acquireRedeemLock(ctx context.Context, code string) bool {
	if s.cache == nil {
		return true // Continue with database checks when Redis is unavailable.
	}

	ok, err := s.cache.AcquireRedeemLock(ctx, code, redeemLockDuration)
	if err != nil {
		// A failed advisory lock call leaves acquisition uncertain; allow database
		// row locking and the subsequent credential checks to govern this attempt.
		return true
	}
	return ok
}

// releaseRedeemLock requests advisory exclusion release when a cache exists.
// Its error is discarded after the attempt, and the Redis implementation
// deletes the current code key without owner-token checks; callers rely on
// database grant-state protection.
func (s *RedeemService) releaseRedeemLock(ctx context.Context, code string) {
	if s.cache == nil {
		return
	}

	// Release advisory exclusion after the attempt best-effort; durable
	// transaction outcome remains represented by the grant and usage state
	// already read or written.
	_ = s.cache.ReleaseRedeemLock(ctx, code)
}

// unsupportedRedeemTypeError returns the invitation-specific or unknown-type
// direct-redemption request error.
func unsupportedRedeemTypeError(codeType string) error {
	if codeType == RedeemTypeInvitation {
		return apperror.BadRequest("REDEEM_CODE_UNSUPPORTED_TYPE", "invitation codes can only be used during registration")
	}
	return apperror.BadRequest("REDEEM_CODE_UNSUPPORTED_TYPE", fmt.Sprintf("unsupported redeem type: %s", codeType))
}

// Redeem trims credential text, checks advisory failure admission and code
// exclusion, then applies the grant/usage snapshot through
// transactions.Within. Empty text returns not-found; a held cache key returns
// locked, while cache failures permit database validation to continue.
//
// The transactional helper checks expiry, prior user usage, remaining
// capacity, supported type and any required paid-order history before reaching
// entitlement writes; it does not independently reject disabled status. After
// successful transaction completion, cache effects and optional
// positive-balance rebate run. A final response reread can fail after the
// grant transaction succeeded, so an error at that phase does not establish
// that issuance was rolled back.
// @project-doc docs/domains/payments_and_entitlements.md#redeem_eligibility
func (s *RedeemService) Redeem(ctx context.Context, userID int64, code string) (*RedeemCode, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, ErrRedeemCodeNotFound
	}

	// Check the saved failure ceiling before attempting code exclusion, so
	// blocked users do not occupy that advisory key during database grant
	// processing.
	if err := s.checkRedeemRateLimit(ctx, userID); err != nil {
		return nil, err
	}

	// Advisory exclusion reduces simultaneous code attempts; the callback still
	// locks the database row and coordinates grant/use writes through its
	// selected transaction.
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

	// Transaction completion has succeeded, so request entitlement cache
	// refreshes before optional rebate work and the response's independent
	// persistence reread.
	s.invalidateRedeemCaches(ctx, userID, redeemCode)

	// Positive balance grants may accrue an affiliate rebate after completion; a
	// payment workflow ownership marker delegates this step to its audited
	// accrual path.
	if redeemCode.Type == RedeemTypeBalance && redeemCode.Value > 0 {
		s.tryAccrueAffiliateRebateForRedeem(ctx, userID, redeemCode.Value)
	}

	// Read the stored grant summary for the response after completion; a failure
	// here returns an error while the earlier grant may remain committed.
	redeemCode, err = s.redeemRepo.GetByID(ctx, redeemCode.ID)
	if err != nil {
		return nil, fmt.Errorf("get updated redeem code: %w", err)
	}

	return redeemCode, nil
}

// redeemInTx reads the locked code and applies its supported entitlement,
// usage row and latest summary through txCtx. ctx remains the
// user-lookup/advisory-counter context; the caller supplies a live transaction
// and dependencies for the selected grant type.
//
// Payment-history checks use txCtx before issuance, while credential
// validation covers nil, expiry, prior usage and capacity with no
// disabled-state guard. Grant writing precedes usage insertion and the
// count/recipient/time update, so their selected transaction can complete or
// roll back together. Cache invalidation and affiliate work remain with Redeem
// after that completion.
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

	// Select supported direct grant types before issuance; invitation credentials
	// use the separate registration path, and subscription grants need a positive
	// plan identifier.
	switch redeemCode.Type {
	case RedeemTypeBalance, RedeemTypeConcurrency:
	case RedeemTypeSubscription:
		if redeemCode.PlanID == nil || *redeemCode.PlanID <= 0 {
			return nil, apperror.BadRequest("REDEEM_CODE_INVALID", "invalid subscription redeem code: missing plan_id")
		}
	default:
		return nil, unsupportedRedeemTypeError(redeemCode.Type)
	}

	// Required paid-order history is checked on the grant transaction context
	// before benefit/usage writes, retaining the credential for a later eligible
	// attempt after rejection.
	if redeemCode.RequiresPayment {
		if s.runtime.HasPaidOrder == nil {
			return nil, errors.New("payment history lookup is unavailable")
		}
		paid, err := s.runtime.HasPaidOrder(txCtx, userID)
		if err != nil {
			return nil, fmt.Errorf("check redeem payment history: %w", err)
		}
		if !paid {
			return nil, ErrRedeemPaymentRequired
		}
	}

	_, err = s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}

	// Keep grant, individual usage and latest credential count/recipient/time
	// updates on txCtx, so a selected transaction failure can roll back their
	// coordinated writes.
	switch redeemCode.Type {
	case RedeemTypeBalance:
		if err := s.transactions.ApplyBalance(txCtx, userID, redeemCode.Value); err != nil {
			if errors.Is(err, ErrRedeemBalanceUnsupported) {
				return nil, err
			}
			return nil, fmt.Errorf("update user balance: %w", err)
		}
	case RedeemTypeConcurrency:
		// Concurrency consumes an integer delta from the stored value; valid
		// credential producers supply integer amounts, since this conversion
		// otherwise truncates fractions.
		if err := s.transactions.ApplyConcurrency(txCtx, userID, int(redeemCode.Value)); err != nil {
			if errors.Is(err, ErrRedeemConcurrencyUnsupported) {
				return nil, err
			}
			return nil, fmt.Errorf("update user concurrency: %w", err)
		}

	case RedeemTypeSubscription:
		// A recorded duration survives later plan edits; legacy NULL keeps the ordinary live-plan default by passing a zero override.
		validityDays := 0
		if redeemCode.ValidityDays != nil {
			validityDays = *redeemCode.ValidityDays
		}
		_, _, err := s.subscriptionService.AssignOrExtendSubscription(txCtx, &AssignSubscriptionInput{
			UserID:       userID,
			PlanID:       *redeemCode.PlanID,
			ValidityDays: validityDays,
			AssignedBy:   0, // System assignment.
			Notes:        fmt.Sprintf("通过兑换码 %s 兑换", redeemCode.Code),
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

// invalidateRedeemCaches applies the successful grant's optional
// authentication invalidation for balance, concurrency or subscription.
// Balance also schedules best-effort billing-cache invalidation on a fresh
// five-second background context; absent runner means synchronous execution,
// and its error is discarded. Other types schedule no balance-cache write.
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
			// Use an independent five-second budget for balance invalidation after
			// issuance, so completion of the caller's request context does not cancel
			// this cache refresh immediately.
			cacheCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Cache refresh failure is best-effort after issuance; discard its error
			// here because the successful grant is already represented by persistent
			// entitlement and usage data.
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

// tryAccrueAffiliateRebateForRedeem accrues a positive-balance redemption
// rebate when affiliate support is present and enabled. A non-nil
// payment-ownership marker skips this step; accrual errors are observed and do
// not replace completed issuance, while positive returned rebates are logged.
func (s *RedeemService) tryAccrueAffiliateRebateForRedeem(ctx context.Context, userID int64, amount float64) {
	if ctx.Value(ctxKeySkipRedeemAffiliate{}) != nil {
		return
	}
	if s.affiliateService == nil || !s.affiliateService.IsEnabled(ctx) {
		return
	}
	rebate, err := s.affiliateService.AccrueInviteRebate(ctx, userID, amount)
	if err != nil {
		// The grant transaction already completed; observe accrual failure and
		// return from this optional step while keeping the issued benefit
		// represented by its saved usage.
		s.runtime.Observe.Printf("service.redeem", "[Redeem] affiliate rebate failed for user %d amount %.2f: %v", userID, amount, err)
		return
	}
	if rebate > 0 {
		s.runtime.Observe.Printf("service.redeem", "[Redeem] affiliate rebate accrued %.8f for inviter of user %d", rebate, userID)
	}
}

// GetByID delegates reading a credential by ID and wraps persistence errors.
func (s *RedeemService) GetByID(ctx context.Context, id int64) (*RedeemCode, error) {
	code, err := s.redeemRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get redeem code: %w", err)
	}
	return code, nil
}

// GetByCode delegates reading the supplied credential spelling and wraps
// persistence errors.
func (s *RedeemService) GetByCode(ctx context.Context, code string) (*RedeemCode, error) {
	redeemCode, err := s.redeemRepo.GetByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("get redeem code: %w", err)
	}
	return redeemCode, nil
}

// List delegates administrator pagination and returns the repository rows and
// page metadata.
func (s *RedeemService) List(ctx context.Context, params pagination.PaginationParams) ([]RedeemCode, *pagination.PaginationResult, error) {
	codes, pagination, err := s.redeemRepo.List(ctx, params)
	if err != nil {
		return nil, nil, fmt.Errorf("list redeem codes: %w", err)
	}
	return codes, pagination, nil
}

// Delete rejects recorded use before requesting repository deletion, retaining
// linked history for successful grants. Read and delete errors are wrapped;
// this method opens no locked transaction around its precheck and requires the
// repository's returned code on success.
func (s *RedeemService) Delete(ctx context.Context, id int64) error {
	// Read the code to check whether it has been used.
	code, err := s.redeemRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get redeem code: %w", err)
	}

	// Reject deletion after recorded use because history dereferences the
	// credential's value and type; removing that record would discard grant
	// detail needed by the user view.
	if !code.CanDelete() {
		return apperror.Conflict("REDEEM_CODE_DELETE_USED", "cannot delete redeem code that has usage records")
	}

	if err := s.redeemRepo.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete redeem code: %w", err)
	}

	return nil
}

// validateRedeemCodeForUser rejects absent records, explicit/natural expiry,
// existing recipient usage and exhausted lifetime capacity in that order.
// Usage-read failures return before the capacity check, and nil is rejected
// before any status helper is called.
//
// The helper does not check disabled status or CanUse, so a disabled unexpired
// record with capacity can pass these specific checks. Supported type and
// payment requirements are checked separately in redeemInTx before it reaches
// grant writes. Callers must not infer complete effective-status validation
// from this result.
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

// GetUserHistory returns the user's grant/adjustment page and its repository
// total in descending use-time order. Repository errors are wrapped; the
// adapter supplies successful page metadata and individual usage observations
// for the returned code views.
func (s *RedeemService) GetUserHistory(ctx context.Context, userID int64, page, pageSize int) ([]RedeemCode, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	codes, result, err := s.redeemRepo.ListByUserPaginated(ctx, userID, params, "")
	if err != nil {
		return nil, 0, fmt.Errorf("get user redeem history: %w", err)
	}
	return codes, result.Total, nil
}

// runBackground dispatches fn through the configured runner, or invokes it
// immediately when the runner is absent. Callers provide a callable function
// and retain any captured data until it finishes; runner scheduling and
// lifetime remain injected.
func (s *RedeemService) runBackground(name string, fn func()) {
	if s.runtime.Background == nil {
		// An absent runner executes this callback in the caller's goroutine, so its
		// work finishes before control returns to the post-grant sequence.
		fn()
		return
	}
	s.runtime.Background(name, fn)
}
