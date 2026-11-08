package billing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// settlement.go normalizes billing commands, builds replay fingerprints and
// plans task fund capture.
// A financial operation key identifies a settlement independently of the local
// request displayed in usage records. Fingerprints retain supplied funding and
// usage facts before storage-bound counters are rounded, so ordinary request
// replay compares the raw charge while wallet and Key arithmetic share
// eight-decimal amounts.
//
// Tasks retain their reservation sources for later capture. Legacy snapshots
// spend held monetary amounts; versioned snapshots convert completed base
// dollars through each recorded source rate. Subscription entries are consumed
// in their supplied order. Unused portions become release records above the
// monetary tolerance. Capture normally caps balance coverage at the hold;
// tasks accepting excess completed usage explicitly charge the wallet difference.
// These calculations return plans for storage to apply in its transaction;
// command normalization itself mutates caller-owned data.

const (
	// UsageBillingMonetaryScale is the decimal scale for balance and API key monetary counters,
	// matching NUMERIC(20,8) columns such as users.balance and api_keys.quota_used.
	UsageBillingMonetaryScale = 8

	// taskCostEpsilon bounds monetary release and excess comparisons during
	// capture; smaller residuals remain within the accepted held-funds tolerance.
	taskCostEpsilon = 0.00000001

	// Task overage policies keep reservations bounded unless the task owner accepts
	// completed usage beyond its estimate and charges the uncovered wallet cost.
	TaskOverageReject        TaskOveragePolicy = ""               // reject charges above reserved funds
	TaskOverageChargeBalance TaskOveragePolicy = "charge_balance" // debit uncovered completed usage
)

// Task and request settlement errors distinguish missing financial identities,
// conflicting replays, unavailable task pricing and reservation failures for
// the store and its callers.
var (
	ErrTaskInsufficientBalance       = apperror.New(apperror.Category(402), "BATCH_IMAGE_INSUFFICIENT_BALANCE", "insufficient balance for batch image hold")
	ErrTaskNotFound                  = apperror.New(apperror.CategoryNotFound, "BATCH_IMAGE_JOB_NOT_FOUND", "batch image job not found")
	ErrUsageBillingRequestIDRequired = errors.New("usage billing request_id is required")
	ErrUsageBillingRequestConflict   = errors.New("usage billing request fingerprint conflict")

	// ErrTaskSettlementCostExceedsHold reports a capture that exceeds reserved funds.
	ErrTaskSettlementCostExceedsHold = apperror.Conflict("BATCH_IMAGE_SETTLEMENT_COST_EXCEEDS_HOLD", "batch image settlement cost exceeds held balance")

	ErrImageTaskPricingMissing = apperror.New(apperror.CategoryBadRequest, "BATCH_IMAGE_SETTLEMENT_PRICING_MISSING", "batch image settlement pricing is missing")
)

// UsageBillingCommand records one request financial identity, payer and
// captured usage for transactional settlement. Callers retain its optional
// pointers and give Normalize exclusive access to the command while storage
// uses it.
type UsageBillingCommand struct {
	// RequestID is trimmed by Normalize. SettlementStore rejects an empty key for
	// a nonnil command before starting financial transaction work.
	RequestID string // financial operation key
	APIKeyID  int64  // credential accounting identity
	// RequestFingerprint remains verbatim when nonblank. Normalize computes a
	// missing digest before rounding monetary counters, using the captured raw
	// amount facts.
	RequestFingerprint string // financial replay digest
	// RequestPayloadHash contributes trimmed nonempty text to an automatically
	// computed fingerprint. Normalize leaves the stored field unchanged, so
	// callers supply any payload hashing themselves.
	RequestPayloadHash string // optional request payload digest
	// APIKeyBillingMode and PreferredSubscriptionID preserve admission funding
	// together. Normalize defaults unsupported or omitted modes to auto and
	// clears the pinned identity outside subscription mode.
	APIKeyBillingMode       string // captured request funding mode
	PreferredSubscriptionID *int64 // optional pinned grant identity

	// UserID pays the charge, while ActorUserID and TeamID retain the actor.
	// PostgreSQL accumulates member usage only for a present TeamID and positive
	// actor distinct from the payer.
	UserID      int64  // financial payer user identity
	ActorUserID int64  // request acting user identity
	TeamID      *int64 // optional acting team identity
	ProviderID  int64  // executing provider record identity
	// GroupID selects subscription coverage and plan rate overrides when
	// positive. The request fingerprint omits this field; callers retain the
	// captured final-group selection.
	GroupID *int64 // optional final funding group
	// BillableAmountUSD and BaseAmountUSD select the allocation basis. A positive
	// BaseAmountUSD drives rate-dependent source coverage; otherwise
	// BillableAmountUSD supplies the monetary amount directly.
	BillableAmountUSD float64 // USD
	BaseAmountUSD     float64 // USD
	// SubscriptionRateMultiplier and SubscriptionRateMultiplierScale choose
	// subscription cost, while BalanceRateMultiplier prices uncovered base usage.
	// The scale applies to selected plan overrides; the supplied default
	// subscription factor is already scaled.
	SubscriptionRateMultiplier      float64 // default subscription charge factor
	SubscriptionRateMultiplierScale float64 // plan override scaling factor
	BalanceRateMultiplier           float64 // uncovered balance charge factor
	// DisablePlanGroupRateMultiplier selects the supplied subscription factor
	// even when a plan group override exists. Task reservation uses it to retain
	// its selected pricing policy.
	DisablePlanGroupRateMultiplier bool // plan override suppression flag
	// IncludeAllocationPricing asks AllocateSubscriptions to retain covered base
	// amount and chosen factor on returned subscription allocations. Request
	// fingerprinting omits this storage-detail flag.
	IncludeAllocationPricing bool // allocation pricing detail flag
	// ProviderType is fingerprinted after trimming. PostgreSQL quota writes
	// compare the supplied value to API Key and Bedrock types ignoring case,
	// without trimming that comparison.
	ProviderType         string // captured executing provider type
	Model                string // captured full billing model
	ServiceTier          string // captured billing service tier
	ReasoningEffort      string // captured final reasoning effort
	BillingType          int8   // usage record billing category
	InputTokens          int    // reported input token count
	OutputTokens         int    // reported output token count
	CacheCreationTokens  int    // reported aggregate cache writes
	CacheReadTokens      int    // reported cache read count
	ImageCacheReadTokens int    // explicit cached image token subset
	ImageFrameCount      int    // observed animation frame units
	ImageCount           int    // reported generated image count
	MediaType            string // captured billed media category

	// APIKeyQuotaCost, APIKeyRateLimitCost and ProviderQuotaCost are rounded
	// after fingerprinting. Their positive values enable respective writes; base
	// allocation substitutes the final user charge for the two Key amounts.
	APIKeyQuotaCost     float64 // USD
	APIKeyRateLimitCost float64 // USD
	ProviderQuotaCost   float64 // USD
}

// ProviderQuotaState contains post-increment provider usage and configured
// limits returned from the funding transaction.
type ProviderQuotaState struct {
	// TotalUsed and TotalLimit describe cumulative quota, DailyUsed and
	// DailyLimit the daily window, and WeeklyUsed and WeeklyLimit the weekly
	// window, and MonthlyUsed and MonthlyLimit the monthly window. Positive
	// limits govern threshold-crossing notifications against
	// these post-update observations.
	TotalUsed    float64 // USD
	TotalLimit   float64 // USD
	DailyUsed    float64 // USD
	DailyLimit   float64 // USD
	WeeklyUsed   float64 // USD
	WeeklyLimit  float64 // USD
	MonthlyUsed  float64 // USD
	MonthlyLimit float64 // USD
}

// UsageBillingApplyResult reports whether request funding was newly applied
// and the resulting source charges and quota observations.
type UsageBillingApplyResult struct {
	// Applied distinguishes newly committed financial work from a matching
	// replay. Settlement effects are triggered by the owning caller after
	// examining this application result.
	Applied bool // new financial application flag
	// APIKeyQuotaExhausted reports a positive Key quota crossing returned by SQL.
	// It can report the crossing while a soft-deleted Key keeps its stored
	// status.
	APIKeyQuotaExhausted bool // new quota crossing observation
	// NewBalance is present for a positive deducted balance amount. Nil leaves
	// callers without a new wallet observation, including matching replay
	// results.
	NewBalance *float64 // optional resulting wallet balance
	// QuotaState is present after an eligible positive provider increment. Nil
	// causes notification consumers to use their separate repository-read path
	// when needed.
	QuotaState            *ProviderQuotaState // optional updated provider quota
	SubscriptionAmountUSD float64             // USD
	BalanceAmountUSD      float64             // USD
	// BillingAllocations lists subscription sources before any positive balance
	// charge. Each returned record retains its pricing detail only when requested
	// during allocation.
	BillingAllocations []BillingAllocation // ordered committed funding records
	// EffectiveRateMultiplier is the combined billed amount divided by a positive
	// BaseAmountUSD. Legacy monetary allocation leaves this optional factor
	// absent.
	EffectiveRateMultiplier *float64 // optional effective source factor
}

// TaskFundsCommand carries one reserve, capture or release identity and the
// task funding snapshot. Its mutable allowance and allocation metadata support
// storage reconciliation; callers keep shared optional pointers unchanged
// during the operation.
type TaskFundsCommand struct {
	// RequestID is trimmed by Normalize and identifies this reserve, capture or
	// release action with APIKeyID. Storage rejects an empty nonnil-command key.
	RequestID string // financial task action key
	APIKeyID  int64  // credential accounting identity
	// RequestFingerprint remains verbatim when nonblank. A missing digest uses
	// the snapshot-version encoding before storage updates mutable hold or actual
	// amounts.
	RequestFingerprint string // financial task replay digest
	// RequestPayloadHash contributes trimmed nonempty text to the generated
	// digest. Batch-image and creative command producers supply their recorded
	// request payload identity here.
	RequestPayloadHash string // optional task payload digest
	// UserID pays the task, while ActorUserID and TeamID identify its member
	// usage. PostgreSQL updates member allowance for a present team and a
	// positive actor distinct from the payer.
	UserID      int64  // financial payer user identity
	ActorUserID int64  // task acting user identity
	TeamID      *int64 // optional acting team identity
	// GroupID selects subscription coverage when reserving funds. Capture and
	// release use recorded allocations, and task fingerprint encoding omits this
	// group field.
	GroupID *int64 // optional reservation funding group
	// APIKeyBillingMode and PreferredSubscriptionID retain submission funding for
	// later calls. Normalize chooses auto for unsupported modes and clears the
	// pinned ID outside subscription mode; v3 fingerprints both selected values.
	APIKeyBillingMode       string        // captured submission funding mode
	PreferredSubscriptionID *int64        // optional pinned grant identity
	Task                    TaskReference // registered task funding reference
	// HoldAmount and ActualAmount carry monetary estimates and completed charges
	// for legacy capture. Versioned reservation can replace HoldAmount with the
	// mixed-source hold, and capture replaces ActualAmount with its planned
	// retained charge.
	HoldAmount   float64 // USD
	ActualAmount float64 // USD
	// OveragePolicy permits completed usage beyond the reservation only when set
	// to TaskOverageChargeBalance. Subscription holds remain capped; excess uses
	// the saved balance rate and can leave the available wallet negative.
	OveragePolicy TaskOveragePolicy // completed usage overage policy
	// PricingSnapshotVersion selects monetary fingerprints below two and
	// base/rate fingerprints from two. Version three additionally fingerprints
	// the captured funding mode and pinned subscription.
	PricingSnapshotVersion int // stored pricing format version
	// BaseAmountUSD and ActualBaseAmountUSD carry unmultiplied reservation and
	// completed cost. Positive BaseAmountUSD with version at least two selects
	// per-source base capture; otherwise monetary ActualAmount drives capture.
	BaseAmountUSD       float64 // USD
	ActualBaseAmountUSD float64 // USD
	// SubscriptionRateMultiplier and SubscriptionRateMultiplierScale choose
	// reservation subscription rates, while BalanceRateMultiplier prices
	// uncovered reservation base. Captured allocation rates then combine with
	// SettlementRateScale for the completed task.
	SubscriptionRateMultiplier      float64 // default reservation subscription factor
	SubscriptionRateMultiplierScale float64 // reservation plan override factor
	BalanceRateMultiplier           float64 // reservation balance charge factor
	// SettlementRateScale multiplies recorded source rates during versioned
	// capture. Negative values become zero; a zero balance rate can leave base
	// usage with no balance charge.
	SettlementRateScale float64 // capture reservation price factor
	// DisablePlanGroupRateMultiplier makes reservation use the supplied
	// subscription factor. Its value is included in generated
	// version-two-and-later task fingerprints with the rate snapshot.
	DisablePlanGroupRateMultiplier bool // plan override suppression flag
	// BalanceHoldAmount and SubscriptionHoldAllocations record the reserved
	// funding split. Positive balance hold or any allocation entry selects split
	// interpretation; when both are absent, positive HoldAmount is treated as a
	// legacy balance hold.
	BalanceHoldAmount float64 // USD
	// SubscriptionHoldAllocations remains input storage until capture reads it.
	// PostgreSQL retry cloning copies the entries but keeps their optional ID
	// pointers shared; callers leave those values unchanged during an operation.
	SubscriptionHoldAllocations []BillingAllocation // recorded subscription hold entries
	// AllowanceReserved tells PostgreSQL capture and release to reconcile
	// pre-recorded Key and member usage. It is mutable operation state and
	// excluded from task fingerprints.
	AllowanceReserved bool // persisted allowance reservation flag
	// ReservedAt identifies allowance and subscription windows eligible for
	// release. Fingerprinting uses its UTC RFC3339Nano representation; the
	// reservation path can substitute current time for its Key write when zero.
	ReservedAt time.Time // original reservation window instant
}

// TaskOveragePolicy determines how capture handles completed usage above reserved funds.
type TaskOveragePolicy string

// TaskFundsResult reports task funding application, wallet observations and
// allocations reconstructed or returned by storage.
type TaskFundsResult struct {
	// Applied is true after a new task funding transaction commits. Matching
	// replays can contain reconstructed allocations with false, so callers
	// inspect application independently of amounts.
	Applied bool // new financial application flag
	// NewBalance and FrozenBalance are wallet observations returned by balance
	// SQL. Empty or reconstructed replay results may omit both; consumers retain
	// their prior wallet state when these pointers are nil.
	NewBalance    *float64 // optional resulting wallet balance
	FrozenBalance *float64 // optional resulting frozen balance
	// HoldAmountUSD reports the summed source reservation for reserve and capture
	// results. Empty release results can omit this amount even when wallet funds
	// were returned.
	HoldAmountUSD float64 // USD
	// EstimatedAmountUSD includes settlement scaling in versioned reservation
	// estimates. Capture results and matching capture replays report their
	// retained actual charge here.
	EstimatedAmountUSD float64 // USD
	// ActualAmountUSD reports the retained capture plan charge. Reservation
	// results can leave it zero while returning a nonzero hold and estimated
	// charge.
	ActualAmountUSD       float64 // USD
	SubscriptionAmountUSD float64 // USD
	BalanceAmountUSD      float64 // USD
	// BillingAllocations can be reconstructed from the command on replay. Task
	// owners copy the returned records when retaining a reservation snapshot for
	// subsequent capture or release.
	BillingAllocations []BillingAllocation // ordered task funding records
}

// TaskCapturePlan describes retained task charges and subscription amounts to
// release when storage captures the reservation.
type TaskCapturePlan struct {
	BalanceHoldAmount     float64 // USD
	ActualAmountUSD       float64 // USD
	SubscriptionAmountUSD float64 // USD
	BalanceAmountUSD      float64 // USD
	// BillingAllocations retains consumed sources in their supplied subscription
	// order before balance, while SubscriptionReleases records unused positive
	// subscription holds above taskCostEpsilon. Returned subscription and plan ID
	// pointers are cloned.
	BillingAllocations   []BillingAllocation // retained ordered funding records
	SubscriptionReleases []BillingAllocation // unused subscription hold records
}

// TaskScope identifies the task family registered with the funding store for
// transaction participation.
type TaskScope string

// TaskReference identifies the task record and its original reservation action
// for the registered store. Funding fingerprints include ID, while Scope and
// ReserveRequestID remain lookup information supplied by the task owner.
type TaskReference struct {
	// Scope selects the registered transaction participant in SettlementStore.
	// Funds requires a nonempty scope for nonnil task commands; fingerprint
	// construction omits it.
	Scope TaskScope // registered task family identity
	// ID identifies the task row to its participant and contributes trimmed text
	// to generated task fingerprints. Normalize trims the stored field without
	// imposing nonempty validation.
	ID string // opaque task record identity
	// ReserveRequestID supplies the hold lookup key for later capture or release.
	// Funds requires a nonempty value, while generated fingerprints omit this
	// owner-supplied lookup identity.
	ReserveRequestID string // original financial reservation key
}

// Normalize trims the financial key, resolves funding mode and prepares
// request replay data in place. Nil commands return immediately, unsupported
// modes select auto, and the pinned ID survives only subscription funding.
//
// A supplied nonblank fingerprint remains verbatim. For a missing one, a
// shallow command copy keeps an omitted mode empty for compatibility while
// supplied modes use their normalized value; raw amounts enter the digest
// before the three storage-bound counters are quantized. Callers supply
// exclusive command access and retain the raw captured values when
// constructing another replay command.
func (c *UsageBillingCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	rawMode := strings.TrimSpace(c.APIKeyBillingMode)
	mode, ok := NormalizeAPIKeyBillingMode(c.APIKeyBillingMode)
	// Unsupported funding text selects auto allocation, so later mode handling
	// clears the unrelated pinned identity instead of treating invalid text as
	// subscription-only funding.
	if !ok {
		mode = APIKeyBillingModeAuto
	}
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		// Build the digest from a shallow raw-amount copy. Keeping omitted mode text
		// empty lets historical auto-mode retries retain their established financial
		// identity encoding.
		fingerprintCommand := *c
		if rawMode == "" {
			fingerprintCommand.APIKeyBillingMode = ""
		} else {
			fingerprintCommand.APIKeyBillingMode = mode
		}
		if mode != APIKeyBillingModeSubscription {
			fingerprintCommand.PreferredSubscriptionID = nil
		}
		c.RequestFingerprint = buildUsageBillingFingerprint(&fingerprintCommand)
	}
	c.APIKeyBillingMode = mode
	if mode != APIKeyBillingModeSubscription {
		c.PreferredSubscriptionID = nil
	}
	// Round storage counters after retaining their raw fingerprint facts.
	// Reversing these phases can turn an unchanged captured request into a replay
	// conflict across precision changes.
	c.quantizeMonetaryFields()
}

// quantizeMonetaryFields rounds the three directly stored monetary counters to
// UsageBillingMonetaryScale on a nonnil command. Normalize calls it after
// fingerprinting; billable and base amounts retain their precision, and
// PostgreSQL rounds the eventual balance deduction after allocation.
func (c *UsageBillingCommand) quantizeMonetaryFields() {
	c.APIKeyQuotaCost = QuantizeUsageBillingAmount(c.APIKeyQuotaCost)
	c.APIKeyRateLimitCost = QuantizeUsageBillingAmount(c.APIKeyRateLimitCost)
	c.ProviderQuotaCost = QuantizeUsageBillingAmount(c.ProviderQuotaCost)
}

// QuantizeUsageBillingAmount rounds finite nonzero amounts to eight decimals
// with decimal half-away-from-zero arithmetic. Zero, NaN and either infinity
// pass through unchanged; callers perform financial validity checks separately
// from precision normalization.
func QuantizeUsageBillingAmount(v float64) float64 {
	if v == 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	// Use the float's shortest decimal representation before rounding. The
	// conversion's exactness result is discarded because the public result
	// remains a float64 monetary amount.
	quantized, _ := decimal.NewFromFloat(v).Round(UsageBillingMonetaryScale).Float64()
	return quantized
}

// buildUsageBillingFingerprint returns a SHA-256 hex digest of the supplied
// request funding and usage facts. Nil input returns empty text; absent team
// and pinned identities encode as zero. Selected text fields and an optional
// payload digest are trimmed, while amounts use ten digits after the decimal
// point.
//
// Normalize supplies the compatibility mode and raw counters before storage
// rounding. The financial key, final GroupID and allocation-detail flags are
// excluded, so callers keep those request facts consistent when retrying the
// same key. Callers supply valid captured financial facts; selected text
// fields use the established pipe-delimited encoding before hashing.
func buildUsageBillingFingerprint(c *UsageBillingCommand) string {
	if c == nil {
		return ""
	}
	teamID := int64(0)
	if c.TeamID != nil {
		teamID = *c.TeamID
	}
	preferredSubscriptionID := int64(0)
	if c.PreferredSubscriptionID != nil {
		preferredSubscriptionID = *c.PreferredSubscriptionID
	}
	// Encode amounts to ten decimal places for the established request identity.
	// Smaller differences can collapse to the same text, while crossing a printed
	// rounding boundary still changes it.
	raw := fmt.Sprintf(
		"%d|%d|%d|%d|%d|%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%s|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f",
		c.UserID,
		c.ActorUserID,
		teamID,
		c.ProviderID,
		c.APIKeyID,
		strings.TrimSpace(c.ProviderType),
		strings.TrimSpace(c.Model),
		strings.TrimSpace(c.ServiceTier),
		strings.TrimSpace(c.ReasoningEffort),
		c.BillingType,
		c.InputTokens,
		c.OutputTokens,
		c.CacheCreationTokens,
		c.CacheReadTokens,
		c.ImageCount,
		strings.TrimSpace(c.MediaType),
		c.BillableAmountUSD,
		c.BaseAmountUSD,
		c.SubscriptionRateMultiplier,
		c.SubscriptionRateMultiplierScale,
		c.BalanceRateMultiplier,
		c.APIKeyQuotaCost,
		c.APIKeyRateLimitCost,
		c.ProviderQuotaCost,
	)
	// Extend only animated identities so replaying an older static settlement
	// retains its original fingerprint after the new frame field is introduced.
	if c.ImageCacheReadTokens > 0 {
		raw += fmt.Sprintf("|image_cache:%d", c.ImageCacheReadTokens)
	}
	if c.ImageFrameCount > 0 {
		raw += fmt.Sprintf("|image_frames:%d", c.ImageFrameCount)
	}
	raw += fmt.Sprintf("|%s|%d", c.APIKeyBillingMode, preferredSubscriptionID)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// HashUsageRequestPayload hashes the supplied nonempty payload bytes with
// SHA-256 and returns lowercase hex. Empty input returns empty text, letting
// callers omit payload identity from their financial fingerprint.
func HashUsageRequestPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Normalize trims the action key and task ID, resolves funding mode and
// supplies a task fingerprint in place. Nil input returns immediately;
// unsupported modes become auto, and a pinned subscription remains only for
// subscription funding.
//
// A nonblank fingerprint is retained verbatim. A missing one uses a shallow
// copy that keeps an omitted mode empty for versioned compatibility and clears
// unrelated pinned identity before encoding. Snapshot version selects which
// captured amount and rate facts participate; mutable allowance state and held
// allocations are excluded so storage can reconcile them after claiming the
// financial action.
func (c *TaskFundsCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	c.Task.ID = strings.TrimSpace(c.Task.ID)
	rawMode := strings.TrimSpace(c.APIKeyBillingMode)
	mode, ok := NormalizeAPIKeyBillingMode(c.APIKeyBillingMode)
	// Unsupported funding text selects auto allocation, so later mode handling
	// clears the unrelated pinned identity instead of treating invalid text as
	// subscription-only funding.
	if !ok {
		mode = APIKeyBillingModeAuto
	}
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		// Keep the omitted mode spelling empty in the fingerprint copy.
		// Version-three serialization thereby remains compatible with prior task
		// actions while runtime funding still selects auto.
		fingerprintCommand := *c
		if rawMode == "" {
			fingerprintCommand.APIKeyBillingMode = ""
		} else {
			fingerprintCommand.APIKeyBillingMode = mode
		}
		if mode != APIKeyBillingModeSubscription {
			fingerprintCommand.PreferredSubscriptionID = nil
		}
		c.RequestFingerprint = buildTaskFundsFingerprint(&fingerprintCommand)
	}
	c.APIKeyBillingMode = mode
	if mode != APIKeyBillingModeSubscription {
		c.PreferredSubscriptionID = nil
	}
}

// buildTaskFundsFingerprint returns a SHA-256 hex digest using the command's
// snapshot-version encoding. Nil input returns empty text. Versions below two
// encode HoldAmount and ActualAmount; later versions encode the version, base
// amounts, source rates, settlement scale and plan-override policy.
//
// Version three adds funding mode and pinned subscription identity, with
// absent optional IDs encoded as zero. Every version includes the payer,
// actor, team, Key, trimmed task ID and UTC RFC3339Nano reservation instant,
// plus any trimmed nonempty payload digest. Scope, reserve key, mutable
// allowance flag and split hold details are omitted so task reconciliation can
// reuse its historical identity encoding. A nondefault overage policy adds a
// suffix; omitted policies retain the fingerprints of previously settled tasks.
func buildTaskFundsFingerprint(c *TaskFundsCommand) string {
	if c == nil {
		return ""
	}
	teamID := int64(0)
	if c.TeamID != nil {
		teamID = *c.TeamID
	}
	var raw string
	// Choose the established version encoding before hashing. Later base-price
	// snapshots omit mutable monetary hold results while legacy snapshots retain
	// their original hold and actual amounts.
	if c.PricingSnapshotVersion >= 2 {
		raw = fmt.Sprintf(
			"%d|%d|%d|%d|%s|%d|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%t|%s",
			c.UserID,
			c.ActorUserID,
			teamID,
			c.APIKeyID,
			strings.TrimSpace(c.Task.ID),
			c.PricingSnapshotVersion,
			c.BaseAmountUSD,
			c.ActualBaseAmountUSD,
			c.SubscriptionRateMultiplier,
			c.SubscriptionRateMultiplierScale,
			c.BalanceRateMultiplier,
			c.SettlementRateScale,
			c.DisablePlanGroupRateMultiplier,
			c.ReservedAt.UTC().Format(time.RFC3339Nano),
		)
	} else {
		raw = fmt.Sprintf(
			"%d|%d|%d|%d|%s|%0.10f|%0.10f|%s",
			c.UserID,
			c.ActorUserID,
			teamID,
			c.APIKeyID,
			strings.TrimSpace(c.Task.ID),
			c.HoldAmount,
			c.ActualAmount,
			c.ReservedAt.UTC().Format(time.RFC3339Nano),
		)
	}
	if c.PricingSnapshotVersion >= 3 {
		preferredSubscriptionID := int64(0)
		if c.PreferredSubscriptionID != nil {
			preferredSubscriptionID = *c.PreferredSubscriptionID
		}
		raw += fmt.Sprintf("|%s|%d", c.APIKeyBillingMode, preferredSubscriptionID)
	}
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	if c.OveragePolicy != TaskOverageReject {
		// Keep existing reservation and bounded-capture identities unchanged while
		// distinguishing an explicit authorization to charge completed excess usage.
		raw += "|overage:" + string(c.OveragePolicy)
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// EffectiveTaskBalanceHoldAmount returns the nonnegative balance portion of a
// task reservation. Nil input yields zero. A positive BalanceHoldAmount or any
// SubscriptionHoldAllocations entry selects the recorded split, so a zero
// balance field remains zero even if HoldAmount is positive.
//
// With neither split indicator, the legacy HoldAmount becomes the balance hold
// and is clamped at zero. Callers supply finite financial inputs; the presence
// check does not validate allocation type, IDs or positive subscription
// amounts before deciding that the command has split metadata.
func EffectiveTaskBalanceHoldAmount(cmd *TaskFundsCommand) float64 {
	if cmd == nil {
		return 0
	}
	if cmd.BalanceHoldAmount > 0 || len(cmd.SubscriptionHoldAllocations) > 0 {
		return math.Max(cmd.BalanceHoldAmount, 0)
	}
	// No recorded split was selected; treat the legacy total as balance capacity
	// for older task snapshots whose stored metadata lacks separate source hold
	// amounts.
	return math.Max(cmd.HoldAmount, 0)
}

// TotalTaskHoldAmount sums the effective balance hold and positive
// subscription-type allocation amounts, returning zero for nil input. Callers
// supply finite reservation data; this calculation does not require IDs on
// entries whose amounts contribute to the total.
//
// EffectiveTaskBalanceHoldAmount first decides whether split metadata replaces
// the legacy total. Any allocation entry selects that split, even when this
// loop later omits its type or nonpositive amount. The result therefore counts
// usable supplied source amounts rather than validating that every hold entry
// names a persisted subscription reservation.
func TotalTaskHoldAmount(cmd *TaskFundsCommand) float64 {
	if cmd == nil {
		return 0
	}
	total := EffectiveTaskBalanceHoldAmount(cmd)
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type == BillingAllocationTypeSubscription && allocation.AmountUSD > 0 {
			total += allocation.AmountUSD
		}
	}
	return total
}

// PlanTaskCapture builds retained task charges and subscription releases
// without writing storage. Nil input returns an empty plan. Snapshot version
// at least two with positive BaseAmountUSD delegates to per-source base
// capture; other inputs use nonnegative ActualAmount as the monetary target.
//
// Legacy capture visits positive subscription entries with IDs in supplied
// order, keeping each amount up to the remaining charge and recording its
// unused part above taskCostEpsilon. TaskOverageChargeBalance assigns the entire
// remaining charge to balance while preserving the original wallet hold.
// Otherwise excess beyond that hold and tolerance returns
// ErrTaskSettlementCostExceedsHold; accepted residuals within tolerance can make
// the retained total slightly smaller than the target. Input amounts and ID
// pointers remain unchanged. Callers validate finite amount and rate inputs.
func PlanTaskCapture(cmd *TaskFundsCommand) (*TaskCapturePlan, error) {
	plan := &TaskCapturePlan{}
	if cmd == nil {
		return plan, nil
	}
	if cmd.PricingSnapshotVersion >= 2 && cmd.BaseAmountUSD > 0 {
		return planTaskBaseAmountCapture(cmd)
	}
	// Negative completed costs retain no charge from the recorded monetary holds.
	actualAmount := math.Max(cmd.ActualAmount, 0)
	remaining := actualAmount
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type != BillingAllocationTypeSubscription || allocation.AmountUSD <= 0 || allocation.SubscriptionID == nil {
			continue
		}
		// Consume these stored monetary holds in input order. Earlier subscription
		// coverage reduces the charge available to later entries before any balance
		// funds are retained.
		kept := math.Min(allocation.AmountUSD, remaining)
		if kept > 0 {
			keptAllocation := CloneBillingAllocation(allocation, kept)
			plan.BillingAllocations = append(plan.BillingAllocations, keptAllocation)
			plan.SubscriptionAmountUSD += kept
			remaining -= kept
		}
		// Emit unused monetary releases above taskCostEpsilon, so sub-tolerance
		// residue is not refunded through an additional source update in the capture
		// plan.
		if released := allocation.AmountUSD - kept; released > taskCostEpsilon {
			plan.SubscriptionReleases = append(plan.SubscriptionReleases, CloneBillingAllocation(allocation, released))
		}
	}

	// Keep the original hold separate from the full wallet charge. Storage releases
	// only this task's frozen funds and debits any explicitly accepted excess.
	plan.BalanceHoldAmount = EffectiveTaskBalanceHoldAmount(cmd)
	if cmd.OveragePolicy == TaskOverageChargeBalance {
		plan.BalanceAmountUSD = remaining
	} else if remaining-plan.BalanceHoldAmount > taskCostEpsilon {
		return nil, ErrTaskSettlementCostExceedsHold
	} else {
		plan.BalanceAmountUSD = math.Min(remaining, plan.BalanceHoldAmount)
	}
	if plan.BalanceAmountUSD > 0 {
		plan.BillingAllocations = append(plan.BillingAllocations, BillingAllocation{
			Type:      BillingAllocationTypeBalance,
			AmountUSD: plan.BalanceAmountUSD,
		})
	}
	plan.ActualAmountUSD = plan.SubscriptionAmountUSD + plan.BalanceAmountUSD
	return plan, nil
}

// planTaskBaseAmountCapture allocates nonnegative completed base cost through
// the reservation's recorded source rates. Its caller supplies a nonnil
// versioned command with positive BaseAmountUSD. SettlementRateScale
// multiplies each subscription allocation rate; a nonpositive selected rate
// skips that entry, including its release calculation.
//
// Positive-rate entries consume base in supplied order up to their held
// monetary amount and produce cloned retained IDs and unused monetary releases
// above tolerance. The nonnegative balance rate prices remaining base. An
// explicit TaskOverageChargeBalance policy retains that full charge; otherwise
// exceeding the effective wallet hold rejects capture. A zero balance rate can
// leave base uncovered with zero charge. The result describes storage work and
// leaves the reservation command unchanged, including all original source rates.
func planTaskBaseAmountCapture(cmd *TaskFundsCommand) (*TaskCapturePlan, error) {
	plan := &TaskCapturePlan{BalanceHoldAmount: EffectiveTaskBalanceHoldAmount(cmd)}
	// Clamp negative completed base cost and settlement scale before source-rate
	// conversion, leaving either value with no positive charge contribution.
	remainingBase := math.Max(cmd.ActualBaseAmountUSD, 0)
	settlementScale := math.Max(cmd.SettlementRateScale, 0)
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type != BillingAllocationTypeSubscription || allocation.AmountUSD <= 0 || allocation.SubscriptionID == nil {
			continue
		}
		rate := allocation.RateMultiplier * settlementScale
		// A nonpositive scaled subscription rate bypasses both retention and refund
		// calculation for this entry. The input hold remains outside this capture
		// plan's releases.
		if rate <= 0 {
			continue
		}
		// Translate remaining base dollars into this source's scaled monetary rate,
		// then divide the kept charge back to base before advancing to later held
		// sources.
		kept := math.Min(allocation.AmountUSD, remainingBase*rate)
		if kept > 0 {
			keptAllocation := CloneBillingAllocation(allocation, kept)
			keptAllocation.BaseAmountUSD = kept / rate
			keptAllocation.RateMultiplier = rate
			plan.BillingAllocations = append(plan.BillingAllocations, keptAllocation)
			plan.SubscriptionAmountUSD += kept
			remainingBase -= kept / rate
		}
		// Emit unused monetary releases above taskCostEpsilon, so sub-tolerance
		// residue is not refunded through an additional source update in the capture
		// plan.
		if released := allocation.AmountUSD - kept; released > taskCostEpsilon {
			plan.SubscriptionReleases = append(plan.SubscriptionReleases, CloneBillingAllocation(allocation, released))
		}
	}

	// Price the remaining base with the recorded balance factor and settlement
	// scale. A zero selected factor permits that residual base to produce no
	// balance charge.
	balanceRate := math.Max(cmd.BalanceRateMultiplier, 0) * settlementScale
	balanceAmount := remainingBase * balanceRate
	if cmd.OveragePolicy == TaskOverageChargeBalance {
		plan.BalanceAmountUSD = balanceAmount
	} else if balanceAmount-plan.BalanceHoldAmount > taskCostEpsilon {
		return nil, ErrTaskSettlementCostExceedsHold
	} else {
		plan.BalanceAmountUSD = math.Min(balanceAmount, plan.BalanceHoldAmount)
	}
	if plan.BalanceAmountUSD > 0 {
		baseAmount := 0.0
		if balanceRate > 0 {
			baseAmount = plan.BalanceAmountUSD / balanceRate
		}
		plan.BillingAllocations = append(plan.BillingAllocations, BillingAllocation{
			Type:           BillingAllocationTypeBalance,
			AmountUSD:      plan.BalanceAmountUSD,
			BaseAmountUSD:  baseAmount,
			RateMultiplier: balanceRate,
		})
		remainingBase -= baseAmount
	}
	if remainingBase > taskCostEpsilon && balanceRate > 0 {
		return nil, ErrTaskSettlementCostExceedsHold
	}
	plan.ActualAmountUSD = plan.SubscriptionAmountUSD + plan.BalanceAmountUSD
	return plan, nil
}

// CloneBillingAllocation copies an allocation, replaces its monetary AmountUSD
// and clones present subscription and plan ID pointers. BaseAmountUSD and
// RateMultiplier remain copied as supplied, so this helper does not rescale
// pricing detail to the replacement amount.
func CloneBillingAllocation(allocation BillingAllocation, amount float64) BillingAllocation {
	cloned := allocation
	cloned.AmountUSD = amount
	// Clone optional source IDs before returning the replacement allocation,
	// allowing callers to retain its identity pointers independently while scalar
	// pricing detail remains as supplied.
	if allocation.SubscriptionID != nil {
		value := *allocation.SubscriptionID
		cloned.SubscriptionID = &value
	}
	if allocation.PlanID != nil {
		value := *allocation.PlanID
		cloned.PlanID = &value
	}
	return cloned
}
