package plugin

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// host_queries.go authorizes and bounds read-only plugin host requests before
// dispatching to existing domain readers. User profile and wallet permissions
// remain separate, and email requires its additional grant for both output and
// exact-email lookup. Caller-only usage queries verify the short-lived user grant;
// broader usage permission permits the same report for any selected account.
//
// Batches, reporting intervals, ranking counts, and order pages are bounded at
// this boundary. IDs are copied and normalized without changing caller input,
// and profile slices are copied before optional email masking. Ranking identity
// redaction and model JSON construction remain with their existing public
// projections, preventing this service from reconstructing a more revealing view.

// Queries enforces read capability authority independently of business storage adapters.
// @project-doc docs/architecture/plugin_system.md#read_capabilities
type Queries struct {
	readers QueryReaders // immutable host-owned query collaborators
	grants  *Grants      // caller-scoped usage authorization verifier
}

// NewQueries binds read collaborators and the host's caller-grant verifier.
// Every reader used by the exposed RPC set must be configured. The caller
// retains those services until admitted plugin callbacks have finished during shutdown.
func NewQueries(readers QueryReaders, grants *Grants) *Queries {
	return &Queries{readers: readers, grants: grants}
}

// SiteInfo returns public host context without requiring a plugin capability grant.
func (q *Queries) SiteInfo(ctx context.Context) (SiteView, error) {
	return q.readers.Site.SiteInfo(ctx)
}

// User returns a permitted live profile with independently authorized email visibility.
// Reader results are values, so clearing email cannot alter a retained account
// snapshot used by wallet reads or another plugin with a different permission set.
func (q *Queries) User(ctx context.Context, binding Binding, id int64) (UserView, error) {
	if err := requireQueryUser(binding, "user.read", id); err != nil {
		return UserView{}, err
	}
	account, err := q.readers.Users.Account(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	if !slices.Contains(binding.Permissions, "user.email") {
		account.User.Email = ""
	}
	return account.User, nil
}

// LookupUsers performs exact bounded discovery and applies separate email authorization.
// Only one selector is accepted. Profile records are copied before masking email,
// preserving any reader-owned values while returning a plugin-specific projection.
// Email-selector admission happens before the reader runs, and ID or username
// selection never implies permission to receive email. The same accepted email
// capability controls both decisions. Normalized ID batches belong to this call,
// leaving the caller's original ordering and any shared input slice unchanged.
func (q *Queries) LookupUsers(ctx context.Context, binding Binding, input UserLookup) ([]UserView, error) {
	if !slices.Contains(binding.Permissions, "user.lookup") {
		return nil, ErrPermissionNotGranted
	}
	input.Username = strings.TrimSpace(input.Username)
	input.Email = strings.TrimSpace(input.Email)
	choices := 0
	if len(input.IDs) > 0 {
		choices++
	}
	if input.Username != "" {
		choices++
	}
	if input.Email != "" {
		choices++
	}
	if choices != 1 {
		return nil, ErrInvalidArgument
	}
	ids, err := queryIDs(input.IDs)
	if err != nil {
		return nil, err
	}
	input.IDs = ids
	emailAllowed := slices.Contains(binding.Permissions, "user.email")
	if input.Email != "" && !emailAllowed {
		return nil, ErrPermissionNotGranted
	}
	profiles, err := q.readers.Users.Lookup(ctx, input)
	if err != nil {
		return nil, err
	}
	profiles = slices.Clone(profiles)
	if !emailAllowed {
		for i := range profiles {
			profiles[i].Email = ""
		}
	}
	return profiles, nil
}

// Identities returns approved binding fields for a permitted positive target account.
func (q *Queries) Identities(ctx context.Context, binding Binding, id int64) ([]IdentityView, error) {
	if err := requireQueryUser(binding, "user.identities", id); err != nil {
		return nil, err
	}
	return q.readers.Users.Identities(ctx, id)
}

// Wallet returns only account funding after the independent wallet.read permission check.
func (q *Queries) Wallet(ctx context.Context, binding Binding, id int64) (WalletView, error) {
	if err := requireQueryUser(binding, "wallet.read", id); err != nil {
		return WalletView{}, err
	}
	account, err := q.readers.Users.Account(ctx, id)
	return account.Wallet, err
}

// Plans lists current sale plans under the subscription.read capability grant.
func (q *Queries) Plans(ctx context.Context, binding Binding) ([]PlanView, error) {
	if !slices.Contains(binding.Permissions, "subscription.read") {
		return nil, ErrPermissionNotGranted
	}
	return q.readers.Subscriptions.Plans(ctx)
}

// Subscriptions returns the selected account's optionally active entitlement records.
func (q *Queries) Subscriptions(ctx context.Context, binding Binding, id int64, activeOnly bool) ([]SubscriptionView, error) {
	if err := requireQueryUser(binding, "subscription.read", id); err != nil {
		return nil, err
	}
	return q.readers.Subscriptions.UserSubscriptions(ctx, id, activeOnly)
}

// UsageSummary accepts broad usage authority or a matching caller-only grant.
// A valid target remains required for both paths. A broader usage.read grant
// does not require the optional caller ticket to match the requested account.
// The caller-only path verifies plugin identity, target identity, and expiration
// before reading statistics. A valid ticket supplies user scope but never grants
// the capability by itself. This distinction lets operators authorize account-wide
// reporting without weakening the narrower usage.self contract for other plugins.
func (q *Queries) UsageSummary(ctx context.Context, binding Binding, id int64, grant string) (UsageSummaryView, error) {
	broad := slices.Contains(binding.Permissions, "usage.read")
	if !broad && !slices.Contains(binding.Permissions, "usage.self") {
		return UsageSummaryView{}, ErrPermissionNotGranted
	}
	if id <= 0 {
		return UsageSummaryView{}, ErrInvalidArgument
	}
	if !broad {
		if err := q.grants.Verify(binding.PluginID, id, grant); err != nil {
			return UsageSummaryView{}, err
		}
	}
	return q.readers.Usage.Summary(ctx, id)
}

// UsageStats reads at most one hundred actors within a bounded reporting interval.
// Empty ID lists return no rows after range validation. Duplicate IDs are removed
// from an owned copy before the business reader receives its batch.
func (q *Queries) UsageStats(ctx context.Context, binding Binding, ids []int64, start, end int64) ([]UserUsageView, error) {
	if !slices.Contains(binding.Permissions, "usage.read") {
		return nil, ErrPermissionNotGranted
	}
	selected, err := queryIDs(ids)
	if err != nil {
		return nil, err
	}
	from, until, err := queryRange(start, end)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return []UserUsageView{}, nil
	}
	return q.readers.Usage.Stats(ctx, selected, from, until)
}

// UsageRanking returns the native sanitized ranking with bounded rows and duration.
// Nonpositive limits use the native twenty-row default, and an empty metric selects
// tokens. The website visibility switch does not restrict an authorized plugin.
func (q *Queries) UsageRanking(ctx context.Context, binding Binding, start, end int64, limit int, sortBy string) ([]RankingView, error) {
	if !slices.Contains(binding.Permissions, "usage.read") {
		return nil, ErrPermissionNotGranted
	}
	from, until, err := queryRange(start, end)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	sortBy = strings.TrimSpace(sortBy)
	if sortBy == "" {
		sortBy = "tokens"
	}
	if limit > 100 || !slices.Contains([]string{"tokens", "requests", "cost"}, sortBy) {
		return nil, ErrInvalidArgument
	}
	return q.readers.Usage.Ranking(ctx, from, until, limit, sortBy)
}

// Orders reads a positive account's purchase page without exposing channel metadata.
// Nonpositive page and size select one and twenty. Sizes above fifty fail rather
// than silently changing pagination, and status remains an exact native filter.
func (q *Queries) Orders(ctx context.Context, binding Binding, input OrderQuery) (OrderPage, error) {
	if err := requireQueryUser(binding, "payment.read", input.UserID); err != nil {
		return OrderPage{}, err
	}
	if input.Page <= 0 {
		input.Page = 1
	}
	if input.PageSize <= 0 {
		input.PageSize = 20
	}
	if input.PageSize > 50 || len(input.Status) > 64 || strings.ContainsRune(input.Status, 0) {
		return OrderPage{}, ErrInvalidArgument
	}
	return q.readers.Orders.Orders(ctx, input)
}

// Models returns the existing public model endpoint's JSON under catalog.read authority.
func (q *Queries) Models(ctx context.Context, binding Binding) (json.RawMessage, error) {
	if !slices.Contains(binding.Permissions, "catalog.read") {
		return nil, ErrPermissionNotGranted
	}
	return q.readers.Catalog.Models(ctx)
}

// Groups returns account-available groups with their native effective rate multipliers.
func (q *Queries) Groups(ctx context.Context, binding Binding, id int64) ([]GroupView, error) {
	if err := requireQueryUser(binding, "catalog.read", id); err != nil {
		return nil, err
	}
	return q.readers.Catalog.Groups(ctx, id)
}

// requireQueryUser checks capability before validating a user-specific read's target identity.
func requireQueryUser(binding Binding, permission string, id int64) error {
	if !slices.Contains(binding.Permissions, permission) {
		return ErrPermissionNotGranted
	}
	if id <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

// queryIDs validates, copies, sorts, and deduplicates a bounded user-ID input batch.
// Empty batches remain empty. The original slice is never sorted or modified,
// and every supplied identifier must be positive before any storage call occurs.
func queryIDs(ids []int64) ([]int64, error) {
	if len(ids) > 100 {
		return nil, ErrInvalidArgument
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, ErrInvalidArgument
		}
	}
	result := slices.Clone(ids)
	slices.Sort(result)
	return slices.Compact(result), nil
}

// queryRange validates a positive exclusive-end interval no longer than ninety-three days.
// Calendar conversion happens only after integer bounds are checked. The year-9999
// ceiling keeps overflowing and unsupported application calendar values out of SQL.
func queryRange(start, end int64) (time.Time, time.Time, error) {
	if start <= 0 || end <= start || end > 253402300799 || end-start > 93*24*60*60 {
		return time.Time{}, time.Time{}, ErrInvalidArgument
	}
	return time.Unix(start, 0), time.Unix(end, 0), nil
}
