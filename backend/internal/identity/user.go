package identity

import (
	"math"
	"slices"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
)

// user.go defines local account state and implements password, role, and
// account eligibility checks.
// Local account state is distinct from its external sign-in identities and
// transient authentication observations. Stored email and password hash feed
// the session credential fingerprint, while group lists govern account binding
// eligibility and notification settings feed billing's balance-alert selection.
//
// Several view fields are populated only by their particular loaders: avatars
// come from separate storage, subscriptions are optionally loaded for lists,
// and resolved token versions or group RPM overrides belong to request
// snapshots. CopyUser copies the struct alone, so its collections and pointers
// remain shared until a cache or other consumer performs the deep copying its
// lifetime needs.

// These role and status labels drive account predicates and authorization
// callers; active status and administrator role are checked independently.
const (
	RoleAdmin    = "admin"  // global administration role
	RoleUser     = "user"   // ordinary account role
	StatusActive = "active" // enabled account status

	// Account Key count defaults retain a finite allowance, while the maximum
	// matches the supported database integer range validated by user operations.
	DefaultUserAPIKeyLimit = 100
	MaxUserAPIKeyLimit     = 2_147_483_647
	// These reserved domain suffixes identify placeholder account addresses made
	// from provider identities; authentication callers treat them as lacking a
	// real mailbox.
	LinuxDoConnectSyntheticEmailDomain  = "@linuxdo-connect.invalid"
	OIDCConnectSyntheticEmailDomain     = "@oidc-connect.invalid"
	WeChatConnectSyntheticEmailDomain   = "@wechat-connect.invalid"
	DingTalkConnectSyntheticEmailDomain = "@dingtalk-connect.invalid"
)

// ErrUserNotFound identifies a missing current account for identity and billing
// lookups.
var ErrUserNotFound = billing.ErrUserNotFound

// User holds stored account state and loader-specific authentication or profile
// observations. Collections and pointers can be shared by shallow copies; each
// consumer owns the copying needed before it mutates or retains those values
// independently.
type User struct {
	// Subscriptions is loaded for user lists when subscription loading is
	// selected. Ordinary GetByID/GetByEmail repository reads leave this
	// association unset.
	Subscriptions []billing.UserSubscription
	ID            int64  // local account row identifier
	Email         string // stored account email address
	// PreferredLocale selects an account language when usable. An absent value
	// leaves locale selection to the request and site defaults used by consumers.
	PreferredLocale *string
	Username        string // account display name text
	Notes           string // administrator account note text
	// AvatarURL, AvatarSource, AvatarMIME, AvatarByteSize and AvatarSHA256 are
	// hydrated from separate avatar storage. ProfileApplyUserAvatar clears the
	// complete view when no avatar record is returned, keeping these observations
	// together.
	AvatarURL      string  // loaded profile avatar URL
	AvatarSource   string  // loaded avatar storage provider
	AvatarMIME     string  // loaded image MIME type
	AvatarByteSize int     // bytes
	AvatarSHA256   string  // hexadecimal avatar content digest
	PasswordHash   string  // stored bcrypt password hash
	Role           string  // stored global account role
	Balance        float64 // available account balance in USD
	FrozenBalance  float64 // held account balance in USD
	Concurrency    int     // concurrent request allowance
	Status         string  // stored account lifecycle status
	// AllowedGroups lists explicit exclusive-group grants used by CanBindGroup.
	// Public-group eligibility instead reads DisabledPublicGroups, keeping grants
	// and denials distinct.
	AllowedGroups []int64
	// DisabledPublicGroups lists administrator-denied public group IDs. Their
	// membership rejects CanBindGroup even though other public groups need no
	// exclusive grant.
	DisabledPublicGroups []int64
	// GroupRestrictionsLoaded distinguishes complete empty grant/denial lists from
	// missing lookup data. Authentication snapshots set it after both restrictions
	// are loaded.
	GroupRestrictionsLoaded bool
	// TokenVersion is a seed until TokenVersionResolved marks the fingerprint
	// result. Session callers use ResolvedTokenVersion rather than treat this
	// field as an independently persisted revocation counter.
	TokenVersion int64
	// TokenVersionResolved marks TokenVersion as already fingerprint-resolved.
	// Consumers retaining such snapshots keep that value consistent with their
	// email and password fields before comparing it with session claims.
	TokenVersionResolved bool
	SignupSource         string     // recorded account signup source
	LastLoginAt          *time.Time // latest recorded login timestamp
	LastActiveAt         *time.Time // latest panel activity timestamp
	LastUsedAt           *time.Time // request-use summary timestamp
	CreatedAt            time.Time  // account record creation timestamp
	UpdatedAt            time.Time  // account record update timestamp
	DeletedAt            *time.Time // nil: current account row

	// GroupRates holds loaded account/group price overrides keyed by group ID.
	// Loaders select this relation separately, and shallow User copies share the
	// mutable map.
	GroupRates map[int64]float64
	// BalanceRechargeMultiplier overrides the global payment recharge rate when
	// present. Nil follows the current global rate; it never scales manual funds.
	BalanceRechargeMultiplier *float64

	// TotpSecretEncrypted, TotpEnabled and TotpEnabledAt record the stored
	// authenticator secret, activation decision and activation time. Setup can
	// persist the secret before the enabled flag; disabling clears the recorded
	// state.
	TotpSecretEncrypted *string    // encrypted TOTP secret text
	TotpEnabled         bool       // stored authenticator activation flag
	TotpEnabledAt       *time.Time // stored authenticator activation timestamp

	// BalanceNotifyEnabled gates this user's alert delivery.
	// BalanceNotifyThreshold overrides the global amount when present;
	// BalanceNotifyThresholdType chooses fixed or percentage interpretation, and
	// TotalRecharged supplies a positive percentage base. BalanceNotifyExtraEmails
	// carries additional delivery recipients.
	BalanceNotifyEnabled       bool   // account balance alert switch
	BalanceNotifyThresholdType string // fixed or percentage threshold kind
	// BalanceNotifyThreshold is nil to use the global amount. A present amount
	// participates in billing's threshold selection, where a nonpositive selected
	// amount disables alerting.
	BalanceNotifyThreshold   *float64
	BalanceNotifyExtraEmails []NotifyEmailEntry // saved additional notification recipients
	TotalRecharged           float64            // cumulative recharge amount in USD

	// RPMLimit is the per-user requests-per-minute limit; zero means unlimited. It
	// applies when the group has no rpm_limit and the user/group pair has no
	// rpm_override, using rpm:u:{userID}:{min}.
	RPMLimit int
	// APIKeyLimit bounds account-owned Key creation. Zero is unlimited; account
	// creation captures a configured default and later settings changes do not
	// rewrite this value.
	APIKeyLimit int

	// UserGroupRPMOverride contains the auth snapshot's user/group RPM override.
	// Nil means no override for this key's user/group pair; checkRPM uses a
	// non-nil value directly. The field is transient and avoids a database lookup
	// on each request.
	UserGroupRPMOverride *int
}

// NotifyEmailEntry is a saved notification recipient with verification and
// delivery eligibility flags.
type NotifyEmailEntry = contact.Entry

// Principal describes the verified caller and the credential used by HTTP
// authorization.
type Principal struct {
	UserID int64  // authenticated local account identifier
	Role   string // current global account role
	// SessionID carries JWT session identity when present. Administrator API Key
	// principals leave it empty, so sid-based step-up grants cannot use that
	// credential. Confirmed MCP dispatches carry the approving browser session.
	SessionID      string
	CredentialKind string // jwt, admin_api_key, api_key, or mcp_token
}

// IsAdmin reports whether the stored global role equals the administrator
// label.
func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}

// IsActive reports whether the stored account status equals the active label.
func (u *User) IsActive() bool {
	return u.Status == StatusActive
}

// CanBindGroup reports whether the account's current lists permit the group.
// Nil users or nonpositive IDs return false. Public groups are accepted unless
// specifically denied, while exclusive groups require an explicit AllowedGroups
// entry.
func (u *User) CanBindGroup(groupID int64, isExclusive bool) bool {
	if u == nil || groupID <= 0 {
		return false
	}
	if !isExclusive {
		// Public groups use the user-specific deny list.
		return !slices.Contains(u.DisabledPublicGroups, groupID)
	}
	// Exclusive groups require membership in AllowedGroups.
	return slices.Contains(u.AllowedGroups, groupID)
}

// SetPassword replaces the in-memory password hash using bcrypt's default cost.
// The caller provides a non-nil user and persists the selected hash afterward;
// this method changes no stored row or resolved-token snapshot flag by itself.
func (u *User) SetPassword(password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	u.PasswordHash = string(hash)
	return nil
}

// CheckPassword reports whether bcrypt accepts the supplied password for this
// user. A non-nil user is required. Password mismatch and malformed hash errors
// both become false so callers receive one authentication decision.
func (u *User) CheckPassword(password string) bool {
	// Bcrypt errors include ordinary mismatch and invalid hash format; reduce both
	// to the rejected password decision expected by authentication callers.
	return bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) == nil
}

// ParseNotifyEmails decodes saved notification recipients through the contact
// parser. Record input retains flags, while legacy strings become enabled,
// unverified recipients; malformed input returns no usable entries for delivery
// selection.
func ParseNotifyEmails(raw string) []NotifyEmailEntry { return contact.ParseNotifyEmails(raw) }

// MarshalNotifyEmails encodes recipient records through the shared contact
// serializer. Empty input becomes an empty JSON array, preserving the stored
// list shape; serializer fallback also supplies that shape when encoding fails.
func MarshalNotifyEmails(entries []NotifyEmailEntry) string {
	return contact.MarshalNotifyEmails(entries)
}

// IsReservedEmail reports whether trimmed lowercase text ends in a reserved
// provider-placeholder suffix. It recognizes LinuxDo, OIDC, WeChat and DingTalk
// addresses for mailbox eligibility checks and performs no complete email
// validation.
func IsReservedEmail(email string) bool {
	normalized := strings.ToLower(strings.TrimSpace(email))
	return strings.HasSuffix(normalized, LinuxDoConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, OIDCConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, WeChatConnectSyntheticEmailDomain) ||
		strings.HasSuffix(normalized, DingTalkConnectSyntheticEmailDomain)
}

// IsValidBalanceRechargeMultiplier reports whether a recharge override is finite
// and strictly positive.
func IsValidBalanceRechargeMultiplier(multiplier float64) bool {
	return multiplier > 0 && !math.IsNaN(multiplier) && !math.IsInf(multiplier, 0)
}

// CopyUser returns a new struct containing the supplied user's field values.
// Nil input returns nil; slices, maps and pointers remain shared, so callers
// make the deeper copies needed before changing or independently retaining
// those values.
func CopyUser(user *User) *User {
	if user == nil {
		return nil
	}
	copy := *user
	return &copy
}
