package identity

import (
	"context"
	_ "image/png"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// user_store.go defines account repository operations and field masks for
// selected profile updates.
// Account profile writes select an explicit field mask so a stale user
// snapshot cannot replace unrelated funding or security values. Funding uses
// separate billing operations, while avatars, provider identities, group
// relations and usage timestamps have dedicated repository reads or writes
// with their own completion rules.

// UserListFilters selects account matches and optional related data for
// administrative lists.
type UserListFilters struct {
	Status    string // matching account status text
	Role      string // matching global role text
	Search    string // email, username, notes or Key search
	GroupName string // partial authorized group-name match
	// APIKeyGroupID matches undeleted API Keys through api_keys.group_id. Zero
	// omits this filter, and its membership test is separate from the account's
	// allowed_groups.
	APIKeyGroupID int64
	// Attributes requires every supplied definition ID to match its filter text.
	// PostgreSQL wraps values in ILIKE wildcards, so supplied wildcard
	// characters participate in matching.
	Attributes map[int64]string
	// IncludeSubscriptions controls active-status subscription loading for
	// ListWithFilters. Nil selects loading for compatibility, while false skips
	// those relation reads for the list.
	IncludeSubscriptions *bool
	// IncludeDeleted admits soft-deleted account rows for administrative
	// history. PostgreSQL applies it to user queries; related subscription/group
	// queries retain their normal soft-delete filtering.
	IncludeDeleted bool
}

// UserUpdateFields selects the columns and relations UserRepository.Update may write.
//
// Callers must select each field they intend to change. Unselected fields retain their
// database values, protecting concurrent balance, activity, limit, role, and status updates
// from a stale read-modify-write snapshot.
//
// Balance and total_recharged are excluded. Use atomic balance operations such as
// AdjustBalance, SetBalance, UpdateBalance, or DeductBalance to change funds.
type UserUpdateFields struct {
	BalanceRechargeMultiplier bool // selected recharge rate override
	PreferredLocale           bool // selected account language preference
	Email                     bool // selected account email address
	Username                  bool // selected account display name
	Notes                     bool // selected administrator account notes
	PasswordHash              bool // selected account password hash
	Role                      bool // selected global account role
	Status                    bool // selected account admission status
	Concurrency               bool // selected concurrent-request account limit
	RPMLimit                  bool // selected request-rate account limit
	APIKeyLimit               bool // selected API Key count limit
	SignupSource              bool // selected account registration source
	LastLoginAt               bool // selected account login observation
	LastActiveAt              bool // selected account activity observation
	// BalanceNotifySettings and BalanceNotifyExtraEmails select different
	// notification writes. Updating threshold controls leaves the recipient list
	// unchanged unless its separate field is also selected.
	BalanceNotifySettings    bool // selected balance notification threshold controls
	BalanceNotifyExtraEmails bool // selected notification recipient email list
	AllowedGroups            bool // selected authorized group relation list
	DisabledPublicGroups     bool // selected public group exclusion list
}

// BalanceChange is the billing result type exposed to identity callers.
type BalanceChange = billing.BalanceChange

// UserRepository supplies account persistence, profile relationships and
// atomic funding operations to identity callers.
type UserRepository interface {
	// Create persists a supplied account with selected group relationships and
	// its email identity. PostgreSQL joins a context-carried transaction or owns
	// creation completion; nil input returns success without writing.
	//
	// Destination-email uniqueness checks precede account creation, and related
	// group/email-identity writes precede an owned commit. Successful work fills
	// account identifiers/timestamps on the supplied user. Callers keep that
	// user stable throughout the operation and complete any externally owned
	// transaction before treating the returned account as durably created.
	Create(ctx context.Context, user *User) error
	// CreateWithNormalizedEmailGuard creates an account while checking the
	// supplied normalized registration address. PostgreSQL also locks the
	// destination email and saves selected relations/email identity; it joins a
	// context-carried transaction or owns its transaction completion.
	//
	// The normalized guard rejects a concurrent occupied address before
	// creation, while the stored account fields are taken from user. Callers
	// keep that account email and guard aligned and stable. A successful owned
	// commit updates the caller's account identifiers/timestamps; an externally
	// owned transaction still needs its caller's commit after this method
	// returns.
	CreateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string) error
	// GetByID reads a current undeleted account by ID and reports missing state
	// as ErrUserNotFound. PostgreSQL hydrates group restrictions for this point
	// read; profile callers load the separate avatar and subscriptions through
	// their own operations.
	GetByID(ctx context.Context, id int64) (*User, error)
	// GetByIDIncludeDeleted reads an account by ID including soft-deleted rows
	// for administrative history. PostgreSQL hydrates group restrictions but
	// keeps their normal relation filtering; callers keep this audit access
	// separate from login admission.
	GetByIDIncludeDeleted(ctx context.Context, id int64) (*User, error)
	// GetByEmail reads an undeleted account using trimmed/lowercased email
	// matching in PostgreSQL. Missing accounts return ErrUserNotFound and
	// multiple matches return a conflict; callers do not treat lookup success as
	// password or provider proof.
	GetByEmail(ctx context.Context, email string) (*User, error)
	// GetFirstAdmin reads the lowest-ID active undeleted administrator in
	// PostgreSQL. Missing state returns ErrUserNotFound; management-key
	// authentication validates its credential separately before using this
	// account as the administrator identity.
	GetFirstAdmin(ctx context.Context) (*User, error)
	// Update persists selected account columns and relations from a supplied
	// snapshot. Balance and total-recharged fields are excluded, and nil input
	// or an empty mask succeeds without a write; successful work refreshes
	// UpdatedAt.
	//
	// PostgreSQL updates selected groups and synchronizes the email identity in
	// its update transaction. It first attempts a transaction from its
	// configured client, so a separate transaction carried only by context does
	// not establish shared commit with this update. Callers use atomic funding
	// operations for balance changes and confirm actual store participation when
	// combining this write with other persistent work.
	Update(ctx context.Context, user *User, fields UserUpdateFields) error
	// UpdateWithNormalizedEmailGuard applies the field mask and an optional
	// destination-email guard. The normalized-address check is used when Email
	// is selected, preserving unselected account values while synchronizing the
	// resulting email identity and selected group relations.
	//
	// PostgreSQL first attempts a transaction on its configured client before
	// considering an already-started transaction, so callers confirm that
	// client/context participation matches their intended commit scope. Nil
	// input or an empty mask returns success without writing. The caller retains
	// stable account fields and a matching normalized guard through the
	// uniqueness check and selected update.
	UpdateWithNormalizedEmailGuard(ctx context.Context, user *User, normalizedEmail string, fields UserUpdateFields) error
	// Delete removes provider relationships and soft-deletes an account through
	// PostgreSQL's deletion transaction. The user-deletion trigger can reject a
	// team owner after relationship writes have been attempted inside that
	// transaction, returning the team ownership-transfer error.
	//
	// The repository clears identity references/channels before the user delete,
	// then completes its transaction according to actual client/context
	// participation. Administrator management-key deletion belongs to a separate
	// caller operation that can share its transaction. Callers retain returned
	// errors and publish applicable authentication changes after the intended
	// transaction completes.
	Delete(ctx context.Context, id int64) error
	// GetUserAvatar reads the account's separate avatar record. Missing state
	// returns nil without error in PostgreSQL; profile callers apply that
	// absence to their display view, while storage failures remain errors.
	GetUserAvatar(ctx context.Context, userID int64) (*UserAvatar, error)
	// UpsertUserAvatar persists prepared avatar fields for the account and
	// returns the stored view. The caller completes URL/inline preparation
	// first; PostgreSQL uses available transaction participation, and later
	// account-profile writes retain their own commit behavior.
	UpsertUserAvatar(ctx context.Context, userID int64, input UpsertUserAvatarInput) (*UserAvatar, error)
	// DeleteUserAvatar removes the account's separate avatar record. Missing
	// state succeeds in PostgreSQL, allowing blank profile input to clear an
	// absent avatar; surrounding profile flows publish their resulting display
	// view separately.
	DeleteUserAvatar(ctx context.Context, userID int64) error

	// List reads paginated current accounts with the repository's default filter
	// behavior. PostgreSQL loads active-status subscriptions by default along
	// with group restrictions; callers use ListWithFilters to control optional
	// relation loading and administrative filters.
	List(ctx context.Context, params pagination.PaginationParams) ([]User, *pagination.PaginationResult, error)
	// ListWithFilters reads matching accounts and pagination metadata using the
	// supplied relation/filter options. PostgreSQL applies all attribute IDs,
	// defaults subscription loading when its pointer is nil and includes deleted
	// account rows only when requested; related rows keep normal soft-delete
	// filtering.
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UserListFilters) ([]User, *pagination.PaginationResult, error)
	// GetLatestUsedAtByUserIDs reads usage_user_activity observations for
	// selected accounts. Missing observations are omitted from the returned map;
	// PostgreSQL returns UTC timestamp pointers, and callers distinguish usage
	// activity from authentication LastActiveAt.
	GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error)
	// GetLatestUsedAtByUserID reads the account's latest usage activity
	// observation. Missing observations return nil without error, while read
	// failures remain errors; profile/authentication timestamps are separate
	// from this usage summary.
	GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error)
	// UpdateUserLastActiveAt records the supplied account
	// authentication/activity time through an ordinary account update.
	// PostgreSQL replaces the stored timestamp as supplied; callers choose a
	// suitable observation and keep usage-derived latest-used timestamps
	// separate from this account field.
	UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error

	// AddBalance adds the supplied USD delta to stored balance without
	// increasing TotalRecharged. PostgreSQL delegates to billing persistence;
	// callers supply a usable amount and publish any account funding-cache
	// invalidation after the write succeeds.
	AddBalance(ctx context.Context, id int64, amount float64) error
	// UpdateBalance adds the supplied USD delta rather than replacing the
	// balance. PostgreSQL also increases TotalRecharged for positive amounts;
	// callers use SetBalance for replacement and handle cache invalidation after
	// persistence.
	UpdateBalance(ctx context.Context, id int64, amount float64) error
	// DeductBalance removes up to the requested amount from a positive raw
	// balance and returns the amount actually deducted. Existing debt remains
	// unchanged; callers provide finite nonnegative input and interpret this
	// separately from frozen-fund availability.
	DeductBalance(ctx context.Context, id int64, amount float64) (float64, error)
	// AdjustBalance atomically applies a USD delta and returns before/after
	// balances on success. A negative result returns ErrBalanceNegative; failure
	// companion observations can come from a later read, so callers use
	// successful results for administrative accounting.
	AdjustBalance(ctx context.Context, id int64, delta float64) (BalanceChange, error)
	// SetBalance atomically replaces stored balance with a nonnegative USD value
	// and returns before/after values on success. Invalid negative input or
	// storage failure remains an error; callers publish funding changes after a
	// successful write.
	SetBalance(ctx context.Context, id int64, value float64) (BalanceChange, error)
	// UpdateConcurrency adds the supplied delta to the stored concurrent-request
	// limit. It does not select an absolute limit; callers use the batch set
	// operation for replacement and perform applicable authentication-cache
	// invalidation after persistence.
	UpdateConcurrency(ctx context.Context, id int64, amount int) error
	// BatchSetConcurrency replaces limits for selected undeleted accounts and
	// returns the affected-row count. Negative input becomes zero and empty IDs
	// return zero without writing; PostgreSQL owns the single-statement update.
	BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error)
	// BatchAddConcurrency adds delta to selected undeleted account limits and
	// clamps resulting values at zero. Empty IDs return zero without writing;
	// the returned count describes affected rows rather than the number of
	// supplied IDs.
	BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error)
	// BatchUpdateLimits writes nonnil concurrency/RPM pointers for selected
	// undeleted accounts in one statement. Negative selected values become zero;
	// empty IDs or two nil pointers return zero without writing, retaining
	// unselected limits.
	BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error)
	// ExistsByEmail checks current account occupancy using the repository's
	// email lookup predicate. PostgreSQL normalizes trimmed/lowercased address
	// text; read failures remain errors, and a successful false preflight still
	// needs write-time uniqueness handling.
	ExistsByEmail(ctx context.Context, email string) (bool, error)
	// ExistsByNormalizedEmail checks current account occupancy for the supplied
	// normalized registration address. Callers apply their mailbox normalization
	// before this read; PostgreSQL returns storage errors and creation repeats
	// the guard while holding its registration lock.
	ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error)
	// LockRegistrationEmail acquires PostgreSQL's transaction-scoped lock for a
	// nonblank normalized address. Callers keep the occupancy check and account
	// write in that transaction; blank text and non-PostgreSQL clients skip this
	// lock.
	LockRegistrationEmail(ctx context.Context, normalizedEmail string) error
	// RemoveGroupFromAllowedGroups deletes all user/group grant relations for a
	// group and returns their affected count. Callers handle group lifecycle
	// policy and publish impacted authentication-cache invalidation separately
	// after the relation write.
	RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error)
	// AddGroupToAllowedGroups grants one account access to the supplied group
	// idempotently. PostgreSQL suppresses duplicate relations, while the caller
	// validates group/account eligibility and handles related
	// authentication-cache publication after persistence.
	AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error
	// RemoveGroupFromUserAllowedGroups deletes one account's supplied group
	// access relation. The caller performs grant-policy checks and cache
	// invalidation around this persistence operation; removing absent relations
	// succeeds in PostgreSQL.
	RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error
	// ListUserAuthIdentities reads the account's stored provider identity
	// observations for profile controls. Returned metadata/timestamp pointers
	// can remain shared with decoded records, so callers keep them stable while
	// selecting names, hints and unbind eligibility.
	ListUserAuthIdentities(ctx context.Context, userID int64) ([]UserAuthIdentityRecord, error)
	// UnbindUserAuthProvider removes a provider's account identities through
	// PostgreSQL's profile-identity transaction helper. Blank provider and email
	// are rejected; UserService separately checks supported self-service
	// providers and the remaining sign-in methods before this persistence
	// request.
	//
	// The adapter clears adoption decision identity links, deletes identity
	// channels and then deletes matching identities. Absence succeeds. Callers
	// confirm actual transaction participation when combining this with other
	// writes; refresh-session revocation and authentication-cache publication
	// belong to the surrounding flow after the relationship operation returns.
	UnbindUserAuthProvider(ctx context.Context, userID int64, provider string) error

	// UpdateTotpSecret stores encrypted TOTP text or clears it when nil. Setup
	// uses this operation before the separate EnableTotp write, so callers
	// handle an enable failure that follows successful secret persistence.
	UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error
	// EnableTotp writes the account enabled flag and enable timestamp.
	// PostgreSQL can perform this without requiring a saved secret;
	// CompleteSetup supplies its ciphertext first, and the operations have
	// separate write completion.
	EnableTotp(ctx context.Context, userID int64) error
	// DisableTotp clears the account's enabled flag, encrypted secret and enable
	// timestamp. The caller validates its required password/email proof before
	// this write, while persistence failures remain errors.
	DisableTotp(ctx context.Context, userID int64) error
}

// RegistrationEmailDomainRepository supplies quota counts and guarded
// registration writes under current domain policy.
type RegistrationEmailDomainRepository interface {
	// CountUsersByEmailDomain counts current users under the supplied
	// registration-domain grouping. Callers normalize the domain under
	// public-suffix policy; this preflight count does not reserve capacity
	// before the guarded creation operation.
	CountUsersByEmailDomain(ctx context.Context, domain string) (int, error)
	// CreateWithRegistrationEmailGuards creates an account under
	// normalized-address and domain-capacity checks. PostgreSQL holds
	// destination/domain scoped locks, rechecks a nonblank domain's occupied
	// slot, then writes account, selected relations and its email identity.
	//
	// It joins a context-carried transaction or owns creation commit, and
	// successful work updates caller account metadata. The caller computes
	// matching domain/normalized email values from current registration policy;
	// an empty domain omits the quota guard. Concurrent attempts can fail after
	// an earlier preflight passed, so callers preserve the returned
	// occupancy/domain error instead of treating the count as a capacity claim.
	CreateWithRegistrationEmailGuards(ctx context.Context, user *User, normalizedEmail, domain string) error
}

// UserAuthIdentityRecord carries stored provider identity observations for
// profile relationship selection.
type UserAuthIdentityRecord struct {
	ProviderType    string // stored authentication provider type
	ProviderKey     string // stored provider identity namespace
	ProviderSubject string // stable provider subject value
	// VerifiedAt retains the record's optional proof timestamp pointer. Profile
	// selection falls back to updated/created observations when this timestamp
	// is absent or zero.
	VerifiedAt *time.Time
	// Issuer retains the decoded record's optional issuer pointer. PostgreSQL
	// ListUserAuthIdentities passes it through, so callers copy its text before
	// independently changing that observation.
	Issuer *string
	// Metadata carries decoded provider hints and source labels. Profile helpers
	// inspect these values shallowly, so callers retain stable nested data while
	// choosing display and sign-in eligibility.
	Metadata  map[string]any
	CreatedAt time.Time // identity record creation time
	UpdatedAt time.Time // identity record update time
}

// UserIdentitySummary carries one profile relationship's display hints and
// available account actions.
type UserIdentitySummary struct {
	Provider string `json:"provider"` // profile authentication provider label
	Bound    bool   `json:"bound"`    // observed provider relationship flag
	// BoundCount counts selected provider records. Email summaries can report
	// one from a nonreserved account mailbox when the identity backfill row is
	// still absent.
	BoundCount  int    `json:"bound_count"`
	DisplayName string `json:"display_name,omitempty"` // selected relationship display hint
	AvatarURL   string `json:"-"`                      // selected provider avatar hint
	SubjectHint string `json:"subject_hint,omitempty"` // masked provider subject hint
	ProviderKey string `json:"provider_key,omitempty"` // selected primary provider namespace
	// VerifiedAt retains the selected identity record's timestamp pointer.
	// Profile callers treat it as a shared proof observation when handling this
	// summary.
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	BindStartPath string     `json:"bind_start_path,omitempty"` // protected provider binding-start path
	// CanBind describes the offered profile bind action. Runtime availability
	// can disable it afterward, and start handlers still validate configuration
	// and authenticated intent.
	CanBind bool `json:"can_bind"`
	// CanUnbind describes eligibility among supported remaining sign-in methods.
	// The unbind entry point rechecks current relationships before persistence
	// and later session cleanup.
	CanUnbind bool   `json:"can_unbind"`
	NoteKey   string `json:"note_key,omitempty"` // relationship explanation localization key
	Note      string `json:"note,omitempty"`     // relationship explanation display text
}

// UserIdentitySummarySet holds the five relationship views shown by the
// profile page.
type UserIdentitySummarySet struct {
	Email    UserIdentitySummary `json:"email"`    // local email relationship view
	LinuxDo  UserIdentitySummary `json:"linuxdo"`  // LinuxDo provider relationship view
	OIDC     UserIdentitySummary `json:"oidc"`     // issuer-scoped OIDC relationship view
	WeChat   UserIdentitySummary `json:"wechat"`   // WeChat provider relationship view
	DingTalk UserIdentitySummary `json:"dingtalk"` // DingTalk provider relationship view
}

// StartUserIdentityBindingRequest selects a supported provider and local
// browser return destination.
type StartUserIdentityBindingRequest struct {
	Provider   string // requested supported provider label
	RedirectTo string // requested local return path
}

// StartUserIdentityBindingResult supplies the frontend action used to start
// protected identity binding.
type StartUserIdentityBindingResult struct {
	Provider           string `json:"provider"`             // normalized selected provider label
	AuthorizeURL       string `json:"authorize_url"`        // backend protected start address
	Method             string `json:"method"`               // start request HTTP method
	UseBrowserRedirect bool   `json:"use_browser_redirect"` // frontend browser navigation flag
}

// UpdateProfileRequest distinguishes selected account/profile updates from
// fields left unchanged.
type UpdateProfileRequest struct {
	// ClearPreferredLocale clears account language before applying
	// PreferredLocale. A subsequently supplied supported language takes
	// precedence over this separate clearing request.
	ClearPreferredLocale bool `json:"-"`
	// PreferredLocale supplies a supported language code when nonnil.
	// ProfileUpdateProfile normalizes it and rejects an unusable value before
	// its account write.
	// PreferredLocale, Email, Username, AvatarURL, Concurrency,
	// BalanceNotifyEnabled and BalanceNotifyThreshold use nil to omit their
	// individual update. ProfileUpdateProfile applies supplied pointers in its
	// validation/write sequence, while ClearPreferredLocale separately requests
	// clearing.
	PreferredLocale *string `json:"preferred_locale"`
	// Email is recognized as a submitted field but rejected by
	// ProfileUpdateProfile. Callers use verified email binding rather than this
	// profile update path.
	Email    *string `json:"email"`
	Username *string `json:"username"` // selected account display name
	// AvatarURL selects a separate avatar write when nonnil. Blank trimmed input
	// deletes the avatar; account-profile persistence follows that avatar
	// operation.
	AvatarURL            *string `json:"avatar_url"`
	Concurrency          *int    `json:"concurrency"`            // selected concurrent-request account limit
	BalanceNotifyEnabled *bool   `json:"balance_notify_enabled"` // selected balance notification switch
	// BalanceNotifyThreshold selects an account override when positive.
	// Nonpositive input clears that override so later notification readers use
	// their configured global threshold.
	BalanceNotifyThreshold *float64 `json:"balance_notify_threshold"`
}

// UserAvatar carries the separately stored avatar representation and its
// optional inline metadata.
type UserAvatar struct {
	StorageProvider string // remote_url or inline representation
	StorageKey      string // optional external storage key
	// URL holds the prepared remote address or inline data URL. Inline
	// normalization can replace large submitted data with compressed JPEG before
	// persistence.
	URL         string
	ContentType string // inline payload MIME type
	ByteSize    int    // bytes
	SHA256      string // hex
}

// UpsertUserAvatarInput supplies prepared avatar representation fields for
// separate account persistence.
type UpsertUserAvatarInput struct {
	StorageProvider string // remote_url or inline representation
	StorageKey      string // optional external storage key
	// URL holds the prepared remote address or inline data URL. Inline
	// normalization can replace large submitted data with compressed JPEG before
	// persistence.
	URL         string
	ContentType string // inline payload MIME type
	ByteSize    int    // bytes
	SHA256      string // hex
}

// ChangePasswordRequest supplies current-password proof and the selected
// replacement password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"` // current account password proof
	NewPassword     string `json:"new_password"`     // selected replacement password text
}

// IsEmpty reports whether no account column or relation is selected.
func (f UserUpdateFields) IsEmpty() bool {
	return f == UserUpdateFields{}
}
