package identity

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// admin_contracts.go defines administrator account inputs and the services
// used to apply them.
// Administrator account input distinguishes omitted pointers from explicit
// zero or empty selections. Funding changes use dedicated balance operations,
// while provider binding carries a stable type/key/subject tuple and optional
// channel data whose ownership is checked by mutation storage. The service
// assembly retains shared dependencies and callbacks across those separate
// account, billing and cache effects.

// These billing aliases identify administrator adjustment history entries,
// separating balance deltas from concurrent-request limit differences
// recorded after account mutations.
const (
	// AdjustmentTypeAdminConcurrency identifies account concurrent-request
	// deltas recorded as administrator adjustment history after the account
	// mutation.
	AdjustmentTypeAdminConcurrency = billing.AdjustmentTypeAdminConcurrency
	AdjustmentTypeAdminBalance     = billing.AdjustmentTypeAdminBalance // USD account balance adjustment kind
)

// ErrRPMStatusUnavailable reports an unavailable administrator RPM display
// when the assembled service lacks its current-minute usage cache reader.
var ErrRPMStatusUnavailable = infraerrors.New(501, "RPM_STATUS_UNAVAILABLE", "RPM cache not available")

// CreateUserInput supplies administrator-selected account credentials, limits
// and initial group relationships.
type CreateUserInput struct {
	Email    string // initial account email text
	Password string // initial account password text
	Username string // initial account display name
	Notes    string // administrator account note text
	// Role selects admin or user when supplied exactly. Empty input uses user;
	// other text is rejected before account creation.
	Role string
	// Balance supplies initial USD funds when nonnil, including explicit zero.
	// Nil reads the configured default when available and otherwise uses zero.
	Balance     *float64
	Concurrency int // initial concurrent-request account limit
	RPMLimit    int // initial requests-per-minute account ceiling
	// APIKeyLimit selects initial Key allowance when nonnil. Nil reads the
	// default, explicit zero is unlimited and invalid database-range values are
	// rejected.
	APIKeyLimit *int
	// AllowedGroups supplies initial group grants. CreateUser passes this slice
	// into the working account, so callers keep its values stable through
	// persistence.
	AllowedGroups []int64
	// DisabledPublicGroups supplies initial public-group exclusions. CreateUser
	// retains the supplied slice until persistence, so callers keep it stable
	// throughout creation.
	DisabledPublicGroups []int64
	ActorAdminID         int64 // acting administrator audit identifier
}

// UpdateUserInput selects account/profile fields and separate group-rate
// changes for administrator updates.
type UpdateUserInput struct {
	// BalanceRechargeMultiplier sets a positive finite override when supplied.
	// Nil retains the stored value unless ClearBalanceRechargeMultiplier is true.
	BalanceRechargeMultiplier *float64
	// ClearBalanceRechargeMultiplier removes the override, making subsequent
	// recharge orders follow global settings. Supplying both edit forms is invalid.
	ClearBalanceRechargeMultiplier bool
	// Email submits replacement text when nonempty. Empty text leaves the
	// account mailbox unchanged, and changed text selects applicable
	// normalized-address write guards.
	Email string
	// Password submits replacement plaintext when nonempty. Empty text keeps the
	// hash, while supplied text is hashed before the selected account write.
	Password string
	// Username, Notes, Concurrency, RPMLimit, APIKeyLimit, AllowedGroups and
	// DisabledPublicGroups use nil to retain their stored values. Supplied
	// pointers select their value, including zero numbers or empty lists.
	Username *string // selected account display name
	Notes    *string // selected administrator account notes
	// Role submits exact admin/user text when nonempty. Empty keeps the role,
	// while administrator demotion checks a separately observed remaining
	// administrator count.
	Role string
	// Balance is retained in this input model but UpdateUser does not read it.
	// Funding changes use UpdateUserBalance and its dedicated atomic operations.
	Balance     *float64
	Concurrency *int // selected concurrent-request account limit
	RPMLimit    *int // selected requests-per-minute account ceiling
	// APIKeyLimit selects a database-range allowance when nonnil. Zero is
	// unlimited; invalid values are rejected before the account update.
	APIKeyLimit *int
	// Status submits account admission text when nonempty. Empty keeps the
	// state; UpdateUser rejects literal disabled for an account currently
	// holding admin role.
	Status               string
	AllowedGroups        *[]int64 // selected account group grant list
	DisabledPublicGroups *[]int64 // selected public-group exclusion list
	// GroupRates supplies separately synchronized group multipliers after
	// account persistence. Nil map skips synchronization; nil entries remove
	// overrides, while supplied nonpositive rates are rejected.
	GroupRates   map[int64]*float64
	ActorAdminID int64 // acting administrator audit identifier
}

// AdminBindAuthIdentityInput supplies the provider and optional channel
// observations selected for administrative binding.
type AdminBindAuthIdentityInput struct {
	// ProviderType, ProviderKey and ProviderSubject identify the same stable
	// relationship. Administrative binding trims them, validates the supported
	// type and prepares compatibility lookup keys before storage checks
	// ownership.
	ProviderType    string // selected authentication provider type
	ProviderKey     string // selected provider identity namespace
	ProviderSubject string // stable provider subject value
	// Issuer supplies optional identity issuer text. Binding trims a supplied
	// nonblank value; absent or blank input omits the update setter and retains
	// earlier stored issuer.
	Issuer *string
	// Metadata supplies replacement identity observations when nonnil.
	// PostgreSQL clones it during persistence; empty maps clear observations and
	// nil skips metadata replacement on update.
	Metadata map[string]any
	// Channel supplies optional application-local binding data. Nil skips
	// channel mutation; a supplied channel requires nonblank
	// mode/application/subject fields after normalization.
	Channel *AdminBindAuthIdentityChannelInput
}

// AdminBindAuthIdentityChannelInput identifies one application/channel-local
// subject under the selected provider identity.
type AdminBindAuthIdentityChannelInput struct {
	// Channel, ChannelAppID and ChannelSubject identify one application-local
	// relationship under the selected provider. All three are required together
	// when administrative binding includes channel input.
	Channel        string // selected provider channel mode
	ChannelAppID   string // selected channel application identifier
	ChannelSubject string // application-local provider subject value
	// Metadata supplies optional replacement channel observations. The
	// normalizer clones it through JSON where possible, retaining shallow
	// references on marshal/unmarshal failure and nil omission on updates.
	Metadata map[string]any
}

// AdminBoundAuthIdentity exposes the persisted relationship returned from an
// administrative binding operation.
type AdminBoundAuthIdentity struct {
	UserID          int64  `json:"user_id"`          // bound identity account identifier
	ProviderType    string `json:"provider_type"`    // stored authentication provider type
	ProviderKey     string `json:"provider_key"`     // stored provider identity namespace
	ProviderSubject string `json:"provider_subject"` // stable provider subject value
	// VerifiedAt retains the stored identity timestamp pointer. The
	// administrative operation stamps its current UTC time, and the response
	// exposes that observation with shared pointer storage.
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// Issuer retains the selected entity's optional issuer pointer through
	// response construction. Callers copy that pointed-to text before changing
	// this observation independently of the selected identity.
	Issuer *string `json:"issuer,omitempty"`
	// Metadata is copied through AdminCloneAdminAuthIdentityMetadata for
	// response construction. Successful JSON round trips decode new values;
	// marshal or unmarshal failure retains shallow nested references, so callers
	// distinguish those ownership cases.
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"` // identity record creation time
	UpdatedAt time.Time      `json:"updated_at"` // identity record update time
	// Channel describes the optional channel written or selected by this
	// operation. Nil response does not enumerate or remove other channel records
	// already stored for the identity.
	Channel *AdminBoundAuthIdentityChannel `json:"channel,omitempty"`
}

// AdminBoundAuthIdentityChannel exposes the channel selected or updated
// during administrative binding.
type AdminBoundAuthIdentityChannel struct {
	Channel        string `json:"channel"`         // stored provider channel mode
	ChannelAppID   string `json:"channel_app_id"`  // stored channel application identifier
	ChannelSubject string `json:"channel_subject"` // application-local provider subject value
	// Metadata uses the administrator metadata-copy helper for response
	// construction. Successful JSON round trips can change numeric
	// representations, while either conversion failure retains shallow nested
	// references.
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"` // channel record creation time
	UpdatedAt time.Time      `json:"updated_at"` // channel record update time
}

// ReplaceUserGroupResult reports the number of matching account Keys migrated
// between groups.
type ReplaceUserGroupResult struct {
	MigratedKeys int64 // matching migrated Key count
}

// UserRPMStatus combines observed account request counts with selected
// per-group ceilings.
type UserRPMStatus struct {
	UserRPMUsed  int `json:"user_rpm_used"`  // observed account minute requests
	UserRPMLimit int `json:"user_rpm_limit"` // stored account minute ceiling
	// PerGroup contains sorted positive primary groups observed in the first
	// thousand Key records. Optional metadata/count failures can leave partial
	// entries with their available observations.
	PerGroup []UserGroupRPMStatus `json:"per_group"`
}

// UserGroupRPMStatus combines a group usage observation with its selected
// account-specific ceiling.
type UserGroupRPMStatus struct {
	GroupID   int64  `json:"group_id"`   // observed primary group identifier
	GroupName string `json:"group_name"` // observed group display name
	Used      int    `json:"used"`       // observed account/group minute requests
	// Limit uses the group ceiling until a readable nonnil account override
	// replaces it. An explicit zero override retains its override source and
	// unlimited value.
	Limit int `json:"limit"`
	// Source names readable group metadata or a nonnil account override. It
	// stays empty when neither optional selector supplies a usable limit
	// observation.
	Source string `json:"source"`
}

type (
	// UserAdmin uses retained account, billing and cache services for
	// administrator account operations.
	UserAdmin struct {
		// AdminDependencies is copied at construction, retaining its shared stores
		// and callbacks. Callers assemble the dependencies used by their selected
		// administrator operation.
		AdminDependencies
	}
	// AdminDependencies carries the shared services and callbacks selected by
	// administrator operation assembly.
	AdminDependencies struct {
		// Now supplies administrator operation time when nonnil. The service uses
		// wall time otherwise, including administrative identity binding
		// timestamps.
		Now    func() time.Time
		Users  UserRepository   // current account persistence service
		Groups AdminGroupReader // group eligibility and metadata reader
		// Keys supplies account credential observations. Deletion permits absence,
		// while RPM display and configured group-replacement invalidation require
		// this reader to be assembled.
		Keys AdminKeyReader
		// Rates supplies optional group multiplier/ceiling reads and
		// synchronization. Missing service skips those enhancements; account
		// updates persist before any requested multiplier sync.
		Rates billing.UserGroupRateRepository
		// RPM supplies cached request observations for administrator display.
		// Missing service returns ErrRPMStatusUnavailable before account/Key
		// queries in GetUserRPMStatus.
		RPM      AdminRPMReader
		Settings AdminUserSettings // current administrator account-default reader
		// Subscriptions supplies optional default-plan grants after administrator
		// account creation. Missing settings or assigner skips work, and individual
		// assignment failures remain diagnostic.
		Subscriptions DefaultSubscriptionAssigner
		// Balances supplies the required atomic funding operation for
		// UpdateUserBalance. The mutation occurs before account readback and
		// optional history/cache/rebate effects.
		Balances billing.BalanceAdjuster
		// Records supplies adjustment history and its queries. Balance/concurrency
		// changes call it after account persistence and diagnose write failure;
		// those paths require a configured recorder.
		Records AdminAdjustmentRecords
		// Invalidator publishes applicable account/credential changes when
		// configured. Its callbacks follow successful mutation returns and expose
		// no acknowledgement of cache propagation completion.
		Invalidator AdminInvalidator
		// BalanceCache supplies optional funding-cache cleanup after administrator
		// adjustments. The background callback uses its own timeout and diagnoses
		// failures after persistence.
		BalanceCache UserBalanceCache
		// Affiliates supplies optional positive-add recharge rebate requests. The
		// current policy must also permit accrual; failed requests remain
		// diagnostic after balance adjustment.
		Affiliates AdminAffiliateAccruer
		// Transactions supplies persistent lifecycle sequences. Identity binding
		// checks database readiness, while deletion and group replacement require
		// this dependency from assembly before use.
		Transactions AdminTransactions
		Observer     Observer // administrator diagnostic event callbacks
		// Background selects the callback runner when configured. RunBackground
		// otherwise invokes the function synchronously; a configured runner's false
		// result supplies no automatic fallback.
		Background func(string, func()) bool
	}
)

// AdminUserSettings reads defaults and optional grant policies used by
// administrator account operations.
type AdminUserSettings interface {
	// GetDefaultBalance reads the USD balance used when administrator creation
	// omits its balance pointer. An explicit supplied zero overrides this
	// observed default, and later global changes do not update that account.
	GetDefaultBalance(context.Context) float64
	// GetDefaultUserAPIKeyLimit reads the account Key allowance snapshotted by
	// administrator creation. An explicit supplied zero is unlimited and
	// overrides the default; callers validate selected values against the
	// supported database integer range.
	GetDefaultUserAPIKeyLimit(context.Context) int
	// IsRegistrationEmailNormalizationEnabled reports whether administrator
	// credential writes request normalized mailbox guards.
	IsRegistrationEmailNormalizationEnabled(context.Context) bool
	// GetDefaultSubscriptions reads plan selections for optional
	// administrator-created account grants. Creation invokes the assigner after
	// account persistence and diagnoses individual plan failures; changing this
	// default leaves existing grants to their own lifecycle.
	GetDefaultSubscriptions(context.Context) []DefaultSubscriptionSetting
	// IsAffiliateAdminRechargeEnabled reports whether positive administrator add
	// operations request affiliate rebate accrual.
	IsAffiliateAdminRechargeEnabled(context.Context) bool
}

// AdminAdjustmentRecords writes adjustment history and reads account funding
// record pages.
type AdminAdjustmentRecords interface {
	// RecordAdjustment records the completed account balance/concurrency
	// difference and supplied notes. Administrator mutations diagnose history
	// failure after the account write, so an error does not undo that already
	// completed adjustment.
	RecordAdjustment(context.Context, int64, string, float64, string) error
	// GetUserBalanceHistory reads account adjustment records using page, size
	// and optional code type. Returned totals include the billing reader's
	// aggregate amount; this query does not modify the account balance or
	// grants.
	GetUserBalanceHistory(context.Context, int64, int, int, string) ([]billing.RedeemCode, int64, float64, error)
}

// AdminGroup carries the eligibility and display metadata needed by
// administrator group operations.
type AdminGroup struct {
	ID           int64  // observed routing group identifier
	Name, Status string // Name: configured group display label. Status: current group admission state.
	IsExclusive  bool   // exclusive group access flag
	RPMLimit     int    // configured group minute ceiling
}

// AdminGroupReader reads group validation/display metadata before
// administrator operations.
type AdminGroupReader interface {
	// GetByID reads the group metadata used to validate an administrator
	// migration target. ReplaceUserGroup requires an active exclusive group
	// before mutation; storage failures remain errors and the caller retains
	// later transaction handling.
	GetByID(context.Context, int64) (*AdminGroup, error)
	// GetByIDLite reads display and RPM metadata for an observed group.
	// GetUserRPMStatus diagnoses lookup failure while retaining its already
	// observed usage values, so this optional metadata read does not decide
	// account existence.
	GetByIDLite(context.Context, int64) (*AdminGroup, error)
}

// AdminKeySummary carries an observed Key ID, raw credential and optional
// primary group.
type AdminKeySummary struct {
	ID int64 // observed Key record identifier
	// Key retains the raw credential used by deletion invalidation. This
	// internal observation requires masking before user-facing output, and it
	// can change after the snapshot read.
	Key string
	// GroupID identifies the observed primary group when positive. RPM display
	// skips nil/nonpositive values and does not expand other group bindings from
	// this summary.
	GroupID *int64
}

// AdminKeyReader reads account credential observations for deletion, RPM
// display and invalidation.
type AdminKeyReader interface {
	// List reads account Key observations with pagination, search and sort
	// inputs. Deletion requests ascending IDs across pages, while RPM display
	// reads the first thousand; callers retain snapshot IDs/secrets and handle
	// subsequent mutation separately.
	List(context.Context, int64, int, int, string, string) ([]AdminKeySummary, int64, error)
	// ListKeysByUserID reads account credential strings for post-migration
	// authentication invalidation. A failed read can follow a successful group
	// mutation and is suppressed by that caller, leaving cache publication
	// unconfirmed.
	ListKeysByUserID(context.Context, int64) ([]string, error)
}

// AdminKeyParticipant supplies Key mutations participating in administrator
// account lifecycle operations.
type AdminKeyParticipant interface {
	// DeleteWithAudit deletes the selected Key through the supplied participant.
	// Administrator deletion uses it before user deletion; actual transaction
	// participation follows the adapter/context and returned failures stop the
	// remaining account deletion sequence.
	DeleteWithAudit(context.Context, int64) error
	// UpdateGroupIDByUserAndGroup moves matching account/old-group Keys to the
	// new group and returns the affected count. Group replacement grants target
	// access first and removes old access afterward through its transaction
	// participant.
	UpdateGroupIDByUserAndGroup(context.Context, int64, int64, int64) (int64, error)
}

// AdminRPMReader reads cached current-minute request observations for account
// RPM display.
type AdminRPMReader interface {
	// GetUserRPM reads the account's observed current-minute request count.
	// GetUserRPMStatus diagnoses a failed read and still retains the returned
	// count alongside account limits; cache absence is handled before this
	// query.
	GetUserRPM(context.Context, int64) (int, error)
	// GetUserGroupRPM reads the account/group's observed current-minute request
	// count. GetUserRPMStatus diagnoses read failure while retaining its
	// companion value and combines that observation with group/override limit
	// selection.
	GetUserGroupRPM(context.Context, int64, int64) (int, error)
}

// AdminInvalidator publishes credential/account changes to authentication
// cache consumers after administrator writes.
type AdminInvalidator interface {
	// InvalidateAuthCacheByUserID requests publication of changed account
	// authentication observations. Administrator mutations call it after
	// applicable successful writes; the callback exposes no error result and its
	// implementation owns propagation to consumers.
	InvalidateAuthCacheByUserID(context.Context, int64)
	// InvalidateAuthCacheByKey requests publication for a credential changed or
	// removed by administrator mutations. Callers use saved or reloaded
	// credential strings after persistence; this callback exposes no
	// acknowledgement of propagation completion.
	InvalidateAuthCacheByKey(context.Context, string)
}

// AdminAffiliateAccruer supplies optional affiliate rebates after positive
// administrator recharge additions.
type AdminAffiliateAccruer interface {
	// AccrueInviteRebate requests affiliate rebate for a positive administrator
	// add amount when policy permits it. The account adjustment already
	// completed, so the caller diagnoses failure while retaining the recharge
	// result.
	AccrueInviteRebate(context.Context, int64, float64) (float64, error)
}

// AdminGroupRateBatchReader supplies optional bulk account/group multiplier
// reads for user listing.
type AdminGroupRateBatchReader interface {
	// GetByUserIDs reads user-specific group multiplier maps in one batch.
	// ListUsers falls back to individual reads after failure; returned nested
	// maps can remain shared with the reader and are held stable by display
	// consumers.
	GetByUserIDs(context.Context, []int64) (map[int64]map[int64]float64, error)
}

// AdminTransactions supplies account/Key/group/identity persistent sequences
// with adapter-owned transaction participation.
type AdminTransactions interface {
	// HasDatabase reports whether administrator mutation storage has its
	// database connection configured.
	HasDatabase() bool
	// DeleteUserAndKeys deletes the supplied positive Key IDs before the
	// account. PostgreSQL can own an Ent transaction and select a
	// transaction-bound Key participant; database-free fixtures run sequential
	// delegate calls without rollback support.
	//
	// The caller already rejects administrator deletion and collects the Key
	// observations. Actual shared completion requires participating user and Key
	// stores to honor the selected context/client. A failed Key deletion stops
	// account deletion; a failed account delete returns before success.
	// UserAdmin publishes its optional account/credential invalidation only
	// after this operation returns successfully.
	DeleteUserAndKeys(context.Context, int64, []AdminKeySummary) error
	// ReplaceUserGroup grants target access, migrates matching account/old-group
	// Keys and removes old access under the mutation adapter's transaction.
	// PostgreSQL requires its database client and commits after these ordered
	// writes before returning the migrated count.
	//
	// The caller validates the target as active and exclusive before this
	// operation; that read is separate from mutation. Participating stores use
	// the selected transaction context and Key participant. A later
	// persistence/commit failure remains an error, while successful callers
	// reload credentials for optional authentication-cache publication after the
	// group changes complete.
	ReplaceUserGroup(context.Context, int64, int64, int64) (int64, error)
	// BindAuthIdentity persists prepared administrator provider/channel binding
	// and returns its stored observations. PostgreSQL starts a transaction,
	// checks compatible identity/channel ownership, writes or updates the
	// selected owned relationship and commits before building the response.
	//
	// Nil metadata/issuer input retains earlier update values where the adapter
	// omits those setters; a supplied empty metadata map replaces metadata.
	// Input channel omission skips channel writes and the returned channel
	// describes the requested operation, rather than every stored channel. The
	// caller supplies authorized administrative input and operation time; this
	// persistence request performs no upstream token-proof verification.
	BindAuthIdentity(context.Context, PreparedAdminIdentityBinding) (*AdminBoundAuthIdentity, error)
}

// PreparedAdminIdentityBinding carries validated local binding observations
// to administrator mutation storage.
type PreparedAdminIdentityBinding struct {
	UserID int64 // target local account identifier
	// ProviderType, ProviderKey and ProviderSubject carry the same prepared
	// identity. BindUserAuthIdentity validates its supported provider type and
	// keeps the subject aligned with canonical and compatible key selection.
	// ProviderType: selected authentication provider type. ProviderKey: selected
	// provider identity namespace. ProviderSubject: stable provider subject
	// value.
	ProviderType, ProviderKey, ProviderSubject string
	// CompatibleKeys retains the lookup candidates aligned with ProviderKey. The
	// mutation adapter checks identity/channel owners under those values before
	// writing the selected namespace.
	CompatibleKeys []string
	Issuer         *string // optional normalized issuer text
	// Metadata retains the supplied identity replacement map until storage
	// copies it. Callers keep it and nested values stable through administrative
	// ownership checks and persistence.
	Metadata map[string]any
	// Channel supplies the optional normalized application-local tuple. Metadata
	// has already passed the normalizer's copy rule; omission skips channel
	// writes in storage.
	Channel    *AdminBindAuthIdentityChannelInput
	VerifiedAt time.Time // UTC administrative binding timestamp
}

// NewUserAdmin copies the dependency value while retaining its shared
// service/callback references. Construction performs no readiness checks;
// callers assemble the dependencies required by their selected creation,
// mutation, display or background path before invoking it.
func NewUserAdmin(d AdminDependencies) *UserAdmin { return &UserAdmin{d} }

// RunBackground delegates to the configured runner and returns its admission
// result, including false without a synchronous retry. A nil runner invokes
// the required callback immediately and returns true; callback panic handling
// belongs to that chosen execution path.
func (s *UserAdmin) RunBackground(name string, fn func()) bool {
	if s.Background != nil {
		return s.Background(name, fn)
	}
	// With no configured runner, execute cleanup synchronously so callers still
	// request the post-write effect; a rejected configured runner returns
	// earlier with its own outcome.
	fn()
	return true
}
