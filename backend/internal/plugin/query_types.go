package plugin

import (
	"context"
	"encoding/json"
	"time"
)

// query_types.go defines the narrow observations exposed by plugin read capabilities.
// Host adapters select these fields from existing domain services before protocol
// conversion. Account credentials, administration notes, payment-channel details,
// and arbitrary identity metadata have no representation in these result types.
// Account funding remains separate from the profile fields returned by user.read.
//
// Readers are grouped by owning business responsibility so each application
// adapter has a small dependency set. Results contain scalars and caller-owned
// slices, and monetary observations use decimal strings. The query service owns
// capability checks, caller grants, and range limits; these interfaces own data
// retrieval and preserve the existing public ranking and model-catalog projections.

// SiteView contains public site context used by plugin presentation and accounting.
type SiteView struct {
	Name              string   // resolved site display name
	BalanceUnitName   string   // resolved account unit name
	BalanceUnitSymbol string   // configured account unit symbol
	Timezone          string   // host IANA timezone name
	DefaultLocale     string   // configured default language tag
	Locales           []string // supported canonical language tags
}

// UserView contains profile fields permitted by the user-read capability family.
type UserView struct {
	ID             int64  // target account identifier
	Username       string // stored account display name
	Role           string // stored global account role
	Status         string // stored account lifecycle status
	CreatedUnix    int64  // creation in Unix seconds
	LastActiveUnix int64  // last activity in Unix seconds
	SignupSource   string // recorded account registration source
	AvatarURL      string // public account avatar location
	// Email is supplied to the host query service and cleared before returning a profile unless user.email was granted.
	Email string
}

// WalletView contains only the monetary observations permitted by wallet.read.
type WalletView struct {
	Balance        string // decimal current account balance
	TotalRecharged string // decimal cumulative recharge amount
}

// AccountView keeps funding separate from the independently authorized public profile projection.
type AccountView struct {
	User   UserView   // permitted account profile observations
	Wallet WalletView // independently authorized funding observations
}

// UserLookup selects exactly one bounded criterion for account discovery.
type UserLookup struct {
	IDs      []int64 // exact positive account identifiers
	Username string  // exact case-sensitive username selector
	Email    string  // exact case-insensitive email selector
}

// IdentityView contains a third-party binding and only its approved display metadata.
type IdentityView struct {
	ProviderType string // identity provider category name
	ProviderKey  string // configured identity provider identifier
	Subject      string // provider-scoped account subject identifier
	Username     string // whitelisted identity username metadata
	DisplayName  string // whitelisted public display metadata
	VerifiedUnix int64  // verification in Unix seconds
}

// PlanView describes an offered subscription plan without private billing configuration.
type PlanView struct {
	ID   int64  // subscription plan record identifier
	Name string // plan display name
	Days int32  // normalized default calendar-day duration
}

// SubscriptionView contains the permitted status and timeline of a user entitlement.
type SubscriptionView struct {
	ID          int64  // subscription entitlement record identifier
	PlanID      int64  // referenced subscription plan identifier
	PlanName    string // subscription plan display name
	Status      string // current entitlement lifecycle status
	StartsUnix  int64  // start in Unix seconds
	ExpiresUnix int64  // expiration in Unix seconds
}

// UsageSummaryView describes today and retained account usage using actual user charges.
type UsageSummaryView struct {
	TodayRequests int64  // current-day completed request count
	TodayTokens   int64  // current-day reported token count
	TodayCost     string // current-day decimal user charge
	TotalRequests int64  // retained completed request count
	TotalTokens   int64  // retained reported token count
	TotalCost     string // retained decimal user charge
}

// UserUsageView contains bounded-period usage for one requested actor account.
type UserUsageView struct {
	UserID   int64  // requested actor account identifier
	Requests int64  // period completed request count
	Tokens   int64  // period reported token count
	Cost     string // period decimal user charge
}

// RankingView retains the existing public ranking's sanitized identity and measurements.
type RankingView struct {
	Rank        int32  // one-based ranking position
	UserID      int64  // ranked account identifier
	DisplayName string // existing public ranking label
	AvatarURL   string // public account avatar location
	Requests    int64  // period completed request count
	Tokens      int64  // period reported token count
	Cost        string // period decimal user charge
}

// OrderView contains purchase observations without channel identifiers or callback data.
type OrderView struct {
	ID          string // decimal local order identifier
	Type        string // stored purchase entitlement type
	Status      string // stored payment lifecycle status
	Amount      string // decimal original order amount
	PlanID      int64  // optional subscription plan identifier
	CreatedUnix int64  // creation in Unix seconds
	PaidUnix    int64  // payment in Unix seconds
}

// OrderQuery selects one account's bounded page and optional exact status.
type OrderQuery struct {
	UserID   int64  // requested purchasing account identifier
	Page     int    // one-based requested page number
	PageSize int    // maximum requested order count
	Status   string // optional exact order status
}

// OrderPage contains sanitized purchases and the native query's total matching count.
type OrderPage struct {
	Orders []OrderView // caller-owned sanitized purchase observations
	Total  int64       // total matching purchase count
}

// GroupView describes an available group and the account's effective rate multiplier.
type GroupView struct {
	ID             int64   // available group record identifier
	Name           string  // configured group internal name
	DisplayName    string  // public group display name
	Description    string  // public group description text
	RateMultiplier float64 // effective account-specific rate multiplier
}

// SiteReader retrieves the public presentation context used by all plugins.
type SiteReader interface {
	// SiteInfo returns current branding, language, balance-unit, and host-timezone observations.
	SiteInfo(context.Context) (SiteView, error)
}

// UserReader retrieves explicitly selected profiles, funding, and third-party identity bindings.
type UserReader interface {
	// Account returns a live account's profile and funding as separate projections.
	// Missing and deleted users return ErrHostUserNotFound. Consumers enforce the
	// separate profile, email, and wallet permissions before returning these observations.
	Account(context.Context, int64) (AccountView, error)
	// Lookup performs bounded exact discovery without administrative notes or key matching.
	// Unknown IDs produce no matching record. The result contains independently owned
	// profile values and uses case-insensitive equality only for the email selector.
	Lookup(context.Context, UserLookup) ([]UserView, error)
	// Identities returns only third-party binding fields and whitelisted display metadata.
	// It excludes local email identities and arbitrary metadata. Missing or deleted
	// target accounts fail instead of exposing retained bindings from a removed user.
	Identities(context.Context, int64) ([]IdentityView, error)
}

// SubscriptionReader retrieves public sale plans and selected account entitlement timelines.
type SubscriptionReader interface {
	// Plans returns offered plans with their normalized default duration in days.
	Plans(context.Context) ([]PlanView, error)
	// UserSubscriptions returns account entitlements using the existing service's active filtering.
	UserSubscriptions(context.Context, int64, bool) ([]SubscriptionView, error)
}

// UsageReader supplies existing account scopes and public identity masking for usage reports.
type UsageReader interface {
	// Summary returns today and retained statistics using the native user dashboard scope.
	Summary(context.Context, int64) (UsageSummaryView, error)
	// Stats returns actor-account observations inside the supplied exclusive-end time range.
	Stats(context.Context, []int64, time.Time, time.Time) ([]UserUsageView, error)
	// Ranking retains native public display-name masking independently of the website's visibility switch.
	Ranking(context.Context, time.Time, time.Time, int, string) ([]RankingView, error)
}

// OrderReader retrieves only the requested account's sanitized purchase page.
type OrderReader interface {
	// Orders applies native ownership and exact-status filtering to the validated page.
	Orders(context.Context, OrderQuery) (OrderPage, error)
}

// CatalogReader retrieves the public model response and account-available group observations.
type CatalogReader interface {
	// Models returns JSON with the same envelope and data projection as the public endpoint.
	Models(context.Context) (json.RawMessage, error)
	// Groups returns available groups with the account's effective configured rate overrides.
	Groups(context.Context, int64) ([]GroupView, error)
}

// QueryReaders binds host-owned read collaborators before a query service is published.
type QueryReaders struct {
	Site          SiteReader         // public site context reader
	Users         UserReader         // profile and funding reader
	Subscriptions SubscriptionReader // offered plan and entitlement reader
	Usage         UsageReader        // scoped usage reporting reader
	Orders        OrderReader        // sanitized purchase history reader
	Catalog       CatalogReader      // public model and group reader
}
