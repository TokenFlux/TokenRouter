package sdk

import (
	"context"
	"encoding/json"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// host.go exposes typed host capabilities grouped by the plugin author's task.
// Methods preserve decimal amount strings and translate host ErrorInfo reasons
// into SDK sentinels. Caller-scoped methods obtain private authorization data
// from the SDK caller value instead of requiring plugins to manage grants.

// Host groups the authenticated capabilities available through one broker connection.
type Host struct {
	Site          Site          // site context queries
	Users         Users         // account identity queries
	Wallet        Wallet        // balance reads and adjustments
	Redeem        Redeem        // single-use redeem code issuance
	Subscriptions Subscriptions // subscription queries and grants
	Usage         Usage         // account usage aggregate queries
	Orders        Orders        // sanitized payment order queries
	Catalog       Catalog       // model and group queries
	Email         Email         // idempotent notification email delivery
	Captcha       Captcha       // site captcha verification
	KV            KV            // plugin-isolated persistent JSON storage
}

// hostClient retains the shared transport used by each capability group.
type hostClient struct {
	rpc pluginpb.HostClient // authenticated host RPC client
}

// Site reads public presentation context from the currently connected host.
type Site hostClient

// Users reads permitted account fields and exact identity lookup results.
type Users hostClient

// Wallet reads balances and requests bounded idempotent balance adjustments.
type Wallet hostClient

// Redeem creates single-use codes through the host's existing entitlement rules.
type Redeem hostClient

// Subscriptions queries plans and requests grants from administrator-approved plans.
type Subscriptions hostClient

// Usage reads caller-scoped or explicitly permitted account usage aggregates.
type Usage hostClient

// Orders reads sanitized user payment orders without payment channel credentials.
type Orders hostClient

// Catalog reads the host's public models and effective user groups.
type Catalog hostClient

// Email sends bounded idempotent notifications through the host's subscription preferences.
type Email hostClient

// Captcha verifies a site captcha using the host-provided request address.
type Captcha hostClient

// CreditRequest describes a positive, idempotent balance credit for one user.
type CreditRequest struct {
	UserID         int64  // target account identifier
	Amount         string // positive decimal balance amount
	IdempotencyKey string // plugin-wide operation replay key
	Reason         string // user-visible balance adjustment explanation
}

// DebitRequest describes a positive debit constrained by the supplied caller grant.
type DebitRequest CreditRequest

// NewHost groups an authenticated RPC client without initiating any calls.
// The caller owns the transport connection and keeps it alive while plugin
// handlers or tasks use the returned capability groups.
func NewHost(client pluginpb.HostClient) *Host {
	return &Host{
		Site: Site{rpc: client}, Users: Users{rpc: client}, Wallet: Wallet{rpc: client},
		Redeem: Redeem{rpc: client}, Subscriptions: Subscriptions{rpc: client}, Usage: Usage{rpc: client},
		Orders: Orders{rpc: client}, Catalog: Catalog{rpc: client}, Email: Email{rpc: client},
		Captcha: Captcha{rpc: client}, KV: KV{rpc: client},
	}
}

// Info returns site names, balance presentation units, timezone, and locales.
func (s Site) Info(ctx context.Context) (*pluginpb.SiteInfo, error) {
	return hostResult(s.rpc.GetSiteInfo(ctx, &pluginpb.GetSiteInfoRequest{}))
}

// Get reads an account, omitting its email unless user.email is granted.
func (s Users) Get(ctx context.Context, userID int64) (*pluginpb.User, error) {
	return hostResult(s.rpc.GetUser(ctx, &pluginpb.GetUserRequest{UserId: userID}))
}

// LookupIDs finds up to one hundred exact account identifiers through the host.
func (s Users) LookupIDs(ctx context.Context, ids []int64) (*pluginpb.LookupUsersResponse, error) {
	return hostResult(s.rpc.LookupUsers(ctx, &pluginpb.LookupUsersRequest{Ids: ids}))
}

// LookupUsername finds the account with the exact supplied username spelling.
func (s Users) LookupUsername(ctx context.Context, username string) (*pluginpb.LookupUsersResponse, error) {
	return hostResult(s.rpc.LookupUsers(ctx, &pluginpb.LookupUsersRequest{Username: username}))
}

// LookupEmail performs a case-insensitive exact email lookup with user.email permission.
func (s Users) LookupEmail(ctx context.Context, email string) (*pluginpb.LookupUsersResponse, error) {
	return hostResult(s.rpc.LookupUsers(ctx, &pluginpb.LookupUsersRequest{Email: email}))
}

// Identities returns permitted third-party identity fields without their authentication tokens.
func (s Users) Identities(ctx context.Context, userID int64) (*pluginpb.ListUserIdentitiesResponse, error) {
	return hostResult(s.rpc.ListUserIdentities(ctx, &pluginpb.ListUserIdentitiesRequest{UserId: userID}))
}

// Get reads the current balance and cumulative recharge as decimal strings.
func (s Wallet) Get(ctx context.Context, userID int64) (*pluginpb.Wallet, error) {
	return hostResult(s.rpc.GetWallet(ctx, &pluginpb.GetWalletRequest{UserId: userID}))
}

// Credit requests a balance reward under the administrator's granted limits.
// Reuse the same idempotency key and input when retrying an uncertain call;
// changing either financial intent or recipient requires a new key.
func (s Wallet) Credit(ctx context.Context, request CreditRequest) (*pluginpb.WalletResponse, error) {
	return hostResult(s.rpc.WalletCredit(ctx, &pluginpb.WalletRequest{UserId: request.UserID, Amount: request.Amount, IdempotencyKey: request.IdempotencyKey, Reason: request.Reason}))
}

// Debit requests a debit authorized by the supplied HTTP caller's grant.
// The host checks that request.UserID matches the caller and rejects expired
// grants. Retrying the same financial intent requires the original idempotency key.
func (s Wallet) Debit(ctx context.Context, caller Caller, request DebitRequest) (*pluginpb.WalletResponse, error) {
	return hostResult(s.rpc.WalletDebit(ctx, &pluginpb.WalletRequest{UserId: request.UserID, Amount: request.Amount, IdempotencyKey: request.IdempotencyKey, Reason: request.Reason, Grant: caller.grant}))
}

// Issue requests a single-use redeem code under configured entitlement limits.
// Repeated calls with the same key and input return the original code. The
// eventual redemption follows the host's existing redemption and rebate rules.
func (s Redeem) Issue(ctx context.Context, request *pluginpb.IssueRedeemCodeRequest) (*pluginpb.IssueRedeemCodeResponse, error) {
	return hostResult(s.rpc.IssueRedeemCode(ctx, request))
}

// Plans lists currently offered subscription plans and their default durations.
func (s Subscriptions) Plans(ctx context.Context) (*pluginpb.ListSubscriptionPlansResponse, error) {
	return hostResult(s.rpc.ListSubscriptionPlans(ctx, &pluginpb.ListSubscriptionPlansRequest{}))
}

// ListForUser returns subscriptions with the requested active-only host filter.
func (s Subscriptions) ListForUser(ctx context.Context, userID int64, activeOnly bool) (*pluginpb.ListUserSubscriptionsResponse, error) {
	return hostResult(s.rpc.ListUserSubscriptions(ctx, &pluginpb.ListUserSubscriptionsRequest{UserId: userID, ActiveOnly: activeOnly}))
}

// Grant requests an idempotent subscription from the administrator's plan allowlist.
// A queued result starts after an existing grant. Replays preserve the original
// subscription identity and do not extend or issue a second grant.
func (s Subscriptions) Grant(ctx context.Context, request *pluginpb.GrantSubscriptionRequest) (*pluginpb.GrantSubscriptionResponse, error) {
	return hostResult(s.rpc.GrantSubscription(ctx, request))
}

// Self reads usage for the authenticated caller using its private grant.
func (s Usage) Self(ctx context.Context, caller Caller) (*pluginpb.UsageSummary, error) {
	return hostResult(s.rpc.GetUsageSummary(ctx, &pluginpb.GetUsageSummaryRequest{UserId: caller.UserID, Grant: caller.grant}))
}

// Summary reads another user's usage when usage.read has been granted.
func (s Usage) Summary(ctx context.Context, userID int64) (*pluginpb.UsageSummary, error) {
	return hostResult(s.rpc.GetUsageSummary(ctx, &pluginpb.GetUsageSummaryRequest{UserId: userID}))
}

// Stats requests bounded aggregate usage for the supplied account identifiers.
func (s Usage) Stats(ctx context.Context, request *pluginpb.GetUsageStatsRequest) (*pluginpb.GetUsageStatsResponse, error) {
	return hostResult(s.rpc.GetUsageStats(ctx, request))
}

// Ranking returns host-formatted public account presentation with bounded usage rankings.
func (s Usage) Ranking(ctx context.Context, request *pluginpb.GetUsageRankingRequest) (*pluginpb.GetUsageRankingResponse, error) {
	return hostResult(s.rpc.GetUsageRanking(ctx, request))
}

// ListForUser returns sanitized order fields for the requested account page.
func (s Orders) ListForUser(ctx context.Context, request *pluginpb.ListUserOrdersRequest) (*pluginpb.ListUserOrdersResponse, error) {
	return hostResult(s.rpc.ListUserOrders(ctx, request))
}

// Models returns the JSON document exposed by the public marketplace endpoint.
func (s Catalog) Models(ctx context.Context) (json.RawMessage, error) {
	response, err := hostResult(s.rpc.ListModels(ctx, &pluginpb.ListModelsRequest{}))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(response.ModelsJson), nil
}

// GroupsForUser returns available groups with the user's effective rate multipliers.
func (s Catalog) GroupsForUser(ctx context.Context, userID int64) (*pluginpb.ListUserGroupsResponse, error) {
	return hostResult(s.rpc.ListUserGroups(ctx, &pluginpb.ListUserGroupsRequest{UserId: userID}))
}

// Send requests one idempotent plain-text notification through host templates.
// An uncertain delivery produces ErrEmailDeliveryUnknown and must not be
// retried with a new key, which could send the same message twice.
func (s Email) Send(ctx context.Context, request *pluginpb.SendEmailRequest) (*pluginpb.SendEmailResponse, error) {
	return hostResult(s.rpc.SendEmail(ctx, request))
}

// Verify checks a captcha token using the address carried by its HTTP caller.
// A disabled site captcha returns enabled=false and success=true. Plugins must
// decide whether their own workflow permits that disabled-site result.
func (s Captcha) Verify(ctx context.Context, caller Caller, token string) (*pluginpb.VerifyCaptchaResponse, error) {
	return hostResult(s.rpc.VerifyCaptcha(ctx, &pluginpb.VerifyCaptchaRequest{Token: token, ClientIp: caller.clientIP}))
}
