package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ledger.go defines replay identity, exact money validation, and transaction
// contracts shared by plugin write capabilities. A plugin-wide operation key
// identifies one normalized intent across capability kinds. Stored results are
// authoritative after commit, and expiring caller grants remain outside that
// intent so a renewed authorization can retrieve the same completed operation.
//
// Money requests use eight decimal places in integer units before crossing the
// existing billing adapter's numeric boundary. Daily quota totals remain decimal
// strings from PostgreSQL and are compared with exact rational arithmetic.
// Transaction ownership lives behind LedgerStore, which supplies the context
// existing billing operations use to join its connection. The caller performs
// optional cache and history effects only after that transaction returns success.

const (
	// Ledger kinds classify effects and their daily quota queries while sharing one replay-key namespace within each plugin installation identity.
	LedgerWalletCredit      LedgerKind = "wallet_credit"      // balance credit operation
	LedgerWalletDebit       LedgerKind = "wallet_debit"       // balance debit operation
	LedgerRedeemIssue       LedgerKind = "redeem_issue"       // redeem credential issuance
	LedgerSubscriptionGrant LedgerKind = "subscription_grant" // subscription entitlement issuance
	LedgerEmailSend         LedgerKind = "email_send"         // notification delivery claim

	// Ledger states distinguish completed results from email claims whose external delivery cannot participate in the database transaction.
	LedgerCompleted LedgerState = "completed" // durable final operation result
	LedgerPending   LedgerState = "pending"   // committed email delivery claim
	LedgerFailed    LedgerState = "failed"    // durable known operation failure
	LedgerUnknown   LedgerState = "unknown"   // uncertain external delivery outcome

	// moneyScale converts positive request decimals into exact units before quota comparison or delegation to existing billing adjustments.
	moneyScale int64 = 100000000
)

// operationKeyPattern bounds replay keys to the portable ASCII alphabet shared by SDK requests, PostgreSQL uniqueness, and deterministic redeem-code derivation.
var operationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9:_.-]{1,128}$`)

// LedgerKind identifies the financial, entitlement, or notification operation being recorded.
type LedgerKind string

// LedgerState describes whether a recorded operation has a durable final result.
type LedgerState string

// Amount represents a nonnegative monetary request in exact hundred-millionth units.
type Amount int64

// Binding carries the process identity and capabilities accepted by its administrator.
type Binding struct {
	PluginID string // authenticated plugin installation identity
	Version  string // executing plugin release version
	// Permissions is copied by the process adapter; callers keep a binding's accepted capability set unchanged while its requests are executing.
	Permissions []string
}

// LedgerEntry preserves normalized intent and its result independently of plugin installation removal.
type LedgerEntry struct {
	PluginID  string          // authenticated plugin installation identity
	Key       string          // plugin-wide operation replay key
	Kind      LedgerKind      // recorded capability operation kind
	UserID    *int64          // optional target account identifier
	Amount    string          // exact nonnegative operation amount
	Request   json.RawMessage // normalized intent excluding caller grants
	Result    json.RawMessage // durable capability result representation
	State     LedgerState     // completed or external-delivery claim state
	Reason    string          // user-visible operation explanation
	CreatedAt time.Time       // effect accounting observation timestamp
}

// LedgerQuery selects one daily capability total under the plugin transaction lock.
type LedgerQuery struct {
	Kind    LedgerKind // selected capability operation kind
	UserID  int64      // zero selects every target user
	Subtype string     // optional request type for redeem quotas
	Since   time.Time  // inclusive quota window start
	Until   time.Time  // exclusive quota window end
}

// LedgerTotals contains exact amount and count observations from the locked ledger.
type LedgerTotals struct {
	Amount string // exact aggregate decimal amount
	Count  int64  // aggregate accepted operation count
}

// LedgerTransaction exposes replay and quota storage on one exclusively locked plugin transaction.
type LedgerTransaction interface {
	// Find returns an existing key or nil when no operation has claimed it.
	// The transaction already owns the plugin's advisory lock. Returned JSON remains
	// caller-owned and can be decoded without retaining a database row cursor.
	Find(context.Context, string) (*LedgerEntry, error)
	// Totals reads a daily capability sum and count under the same plugin lock.
	// Pending and uncertain email claims still count toward quotas. The caller
	// supplies one host-timezone window with an exclusive next-day boundary.
	Totals(context.Context, LedgerQuery) (LedgerTotals, error)
	// Insert records accepted intent and its result before the caller commits.
	// The plugin-wide key must still be unclaimed. Ledger rows never depend on
	// an installation foreign key, so uninstall cannot erase replay protection.
	Insert(context.Context, *LedgerEntry) error
}

// LedgerStore owns the database transaction that existing billing operations join.
type LedgerStore interface {
	// Transact serializes this plugin's replay and quota decisions with an advisory lock.
	// The callback receives a transaction-bound context for both ledger and business
	// writes. Any callback error rolls back both; success returns only after commit.
	Transact(context.Context, string, func(context.Context, LedgerTransaction) error) error
}

// PolicyReader retrieves current installation-level limits independently of process snapshots.
type PolicyReader interface {
	// Get reads the installation whose current limits govern an admitted callback.
	// A disabled record remains readable while existing work drains. Missing records
	// fail because uninstall cannot finish before its admitted callbacks return.
	Get(context.Context, string) (*Installation, error)
}

// ParseAmount accepts a positive decimal within the protocol's precision and width limits.
// Values retain exact eight-decimal units. Signs, exponent notation, leading zeroes,
// excessive precision, and zero fail before any balance mutation or quota query.
func ParseAmount(value string) (Amount, error) {
	if !limitAmountPattern.MatchString(value) {
		return 0, ErrInvalidArgument
	}
	whole, fraction, _ := strings.Cut(value, ".")
	fraction += strings.Repeat("0", 8-len(fraction))
	units, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil || units <= 0 {
		return 0, ErrInvalidArgument
	}
	return Amount(units), nil
}

// String returns the canonical decimal identity used by replay and ledger records.
// Callers obtain positive amounts through ParseAmount; zero is also representable
// for internal quota baselines. Negative values are outside this amount type's contract.
func (a Amount) String() string {
	whole := strconv.FormatInt(int64(a)/moneyScale, 10)
	fraction := strconv.FormatInt(int64(a)%moneyScale+moneyScale, 10)[1:]
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return whole
	}
	return whole + "." + fraction
}

// Float64 converts an already validated amount for the existing billing adjustment interface.
// Request validation precedes this conversion. Quota arithmetic continues to use
// the exact Amount value independently, while the billing store applies the delta
// using its existing numeric-column precision rules.
func (a Amount) Float64() float64 {
	return float64(a) / float64(moneyScale)
}

// validateOperation checks replay-key syntax and a bounded UTF-8 operation explanation.
// Reasons retain their original text after validation, making replay comparison
// independent of transport formatting while preserving the administrator-visible explanation.
func validateOperation(key, reason string) error {
	if !operationKeyPattern.MatchString(key) || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 200 {
		return ErrInvalidArgument
	}
	if strings.ContainsRune(reason, 0) {
		// PostgreSQL text cannot store NUL, so reject it before a transaction could fail after executing the business effect.
		return ErrInvalidArgument
	}
	return nil
}

// sameIntent compares canonical JSON objects without rounding numeric identities.
// PostgreSQL may reorder JSONB keys and whitespace. Both inputs decode with
// preserved numbers before deterministic encoding, so those storage differences
// cannot turn a valid replay into another financial effect or a false conflict.
// Large user identifiers therefore remain distinguishable beyond float64 precision.
// Monetary inputs are normalized separately by their capability before storage.
// Malformed JSON never compares equal, so damaged replay evidence cannot authorize
// a replacement effect through this comparison.
func sameIntent(left, right []byte) bool {
	canonical := func(data []byte) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, ErrInvalidArgument
		}
		return json.Marshal(value)
	}
	a, err := canonical(left)
	if err != nil {
		return false
	}
	b, err := canonical(right)
	return err == nil && bytes.Equal(a, b)
}

// exceedsAmount compares a prospective daily amount against an exact decimal ceiling.
// A zero ceiling prohibits the operation. Invalid stored totals fail closed as
// an exceeded limit, and arithmetic never converts quota values to floating point.
func exceedsAmount(total string, next Amount, ceiling json.Number) bool {
	used, ok := new(big.Rat).SetString(total)
	if !ok || used.Sign() < 0 {
		return true
	}
	limit, ok := new(big.Rat).SetString(string(ceiling))
	if !ok || limit.Sign() <= 0 {
		return true
	}
	increase := new(big.Rat).SetFrac64(int64(next), moneyScale)
	return used.Add(used, increase).Cmp(limit) > 0
}

// dayWindow returns the host calendar day containing the supplied accounting instant.
// Advancing by calendar date preserves daylight-saving boundaries. A nil location
// uses the application's time.Local instead of assuming every day lasts 24 hours.
func dayWindow(now time.Time, location *time.Location) (time.Time, time.Time) {
	if location == nil {
		location = time.Local
	}
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return start, start.AddDate(0, 0, 1)
}
