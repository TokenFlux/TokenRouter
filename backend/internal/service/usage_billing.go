package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/domain"
)

var ErrUsageBillingRequestIDRequired = errors.New("usage billing request_id is required")
var ErrUsageBillingRequestConflict = errors.New("usage billing request fingerprint conflict")

// UsageBillingCommand describes one billable request that must be applied at most once.
type UsageBillingCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string

	UserID                          int64
	AccountID                       int64
	GroupID                         *int64
	BillableAmountUSD               float64
	BaseAmountUSD                   float64
	SubscriptionRateMultiplier      float64
	SubscriptionRateMultiplierScale float64
	BalanceRateMultiplier           float64
	AccountType                     string
	Model                           string
	ServiceTier                     string
	ReasoningEffort                 string
	BillingType                     int8
	InputTokens                     int
	OutputTokens                    int
	CacheCreationTokens             int
	CacheReadTokens                 int
	ImageCount                      int
	MediaType                       string

	APIKeyQuotaCost     float64
	APIKeyRateLimitCost float64
	AccountQuotaCost    float64
}

func (c *UsageBillingCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		c.RequestFingerprint = buildUsageBillingFingerprint(c)
	}
}

func buildUsageBillingFingerprint(c *UsageBillingCommand) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf(
		"%d|%d|%d|%s|%s|%s|%s|%d|%d|%d|%d|%d|%d|%s|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f|%0.10f",
		c.UserID,
		c.AccountID,
		c.APIKeyID,
		strings.TrimSpace(c.AccountType),
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
		c.AccountQuotaCost,
	)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func HashUsageRequestPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// AccountQuotaState holds the post-increment quota state returned by the DB transaction.
// All values are post-update (i.e., already include the increment).
type AccountQuotaState struct {
	TotalUsed   float64
	TotalLimit  float64
	DailyUsed   float64
	DailyLimit  float64
	WeeklyUsed  float64
	WeeklyLimit float64
}

type UsageBillingApplyResult struct {
	Applied                 bool
	APIKeyQuotaExhausted    bool
	NewBalance              *float64           // post-deduction balance (nil = no balance deduction)
	QuotaState              *AccountQuotaState // post-increment quota state (nil = no quota increment)
	SubscriptionAmountUSD   float64
	BalanceAmountUSD        float64
	BillingAllocations      []domain.BillingAllocation
	EffectiveRateMultiplier *float64
}

// BatchImageBalanceHoldCommand describes an idempotent balance hold operation.
type BatchImageBalanceHoldCommand struct {
	RequestID          string
	APIKeyID           int64
	RequestFingerprint string
	RequestPayloadHash string
	UserID             int64
	BatchID            string
	HoldAmount         float64
	ActualAmount       float64
}

func (c *BatchImageBalanceHoldCommand) Normalize() {
	if c == nil {
		return
	}
	c.RequestID = strings.TrimSpace(c.RequestID)
	c.BatchID = strings.TrimSpace(c.BatchID)
	if strings.TrimSpace(c.RequestFingerprint) == "" {
		c.RequestFingerprint = buildBatchImageBalanceHoldFingerprint(c)
	}
}

func buildBatchImageBalanceHoldFingerprint(c *BatchImageBalanceHoldCommand) string {
	if c == nil {
		return ""
	}
	raw := fmt.Sprintf(
		"%d|%d|%s|%0.10f|%0.10f",
		c.UserID,
		c.APIKeyID,
		strings.TrimSpace(c.BatchID),
		c.HoldAmount,
		c.ActualAmount,
	)
	if payloadHash := strings.TrimSpace(c.RequestPayloadHash); payloadHash != "" {
		raw += "|" + payloadHash
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type BatchImageBalanceHoldResult struct {
	Applied       bool
	NewBalance    *float64
	FrozenBalance *float64
}

type UsageBillingRepository interface {
	Apply(ctx context.Context, cmd *UsageBillingCommand) (*UsageBillingApplyResult, error)
	ReserveBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	CaptureBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
	ReleaseBatchImageBalance(ctx context.Context, cmd *BatchImageBalanceHoldCommand) (*BatchImageBalanceHoldResult, error)
}
