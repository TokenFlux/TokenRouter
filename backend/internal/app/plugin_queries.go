package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// plugin_queries.go projects existing domain observations into plugin read contracts.
// User lookup uses the dedicated exact reader, identity bindings select only
// standardized username and display-name metadata, and payment results exclude
// channel and callback fields. Public ranking labels and model catalog JSON
// reuse their owning projections instead of reconstructing more revealing data.
//
// Each adapter retains only its relevant domain collaborators. Batch usage combines
// the existing charge report with one bounded counter query because the native
// charge response does not contain requests or tokens. Scalars are copied into
// new plugin values, and monetary text preserves the domain service's reported
// number without introducing an exchange-rate conversion or a new billing effect.

// pluginSiteQueries reads public branding and the configured host timezone.
type pluginSiteQueries struct {
	service  *site.PublicService // existing public site settings reader
	timezone string              // configured host IANA timezone
}

// pluginUserQueries projects profile, funding, and third-party identity observations.
type pluginUserQueries struct {
	service *identity.UserService // existing account and profile reader
}

// pluginPurchaseQueries projects offered plans, user entitlements, and purchase history.
type pluginPurchaseQueries struct {
	plans         *billing.Plans               // public sale plan reader
	subscriptions *billing.SubscriptionService // existing account entitlement reader
	orders        *payment.OrderQueries        // existing owned purchase query service
}

// pluginUsageQueries preserves native account scopes and sanitized public ranking labels.
type pluginUsageQueries struct {
	service   *usage.UsageService     // account summary and ranking reader
	dashboard *usage.DashboardService // existing batch usage report reader
}

// pluginCatalogQueries exposes public model data and effective account group rates.
type pluginCatalogQueries struct {
	marketplace *routing.Marketplace  // existing public model catalog reader
	keys        *apikey.APIKeyService // account group eligibility and rate reader
}

// providePluginQueries binds narrow read adapters to the already shared domain services.
// The resulting service performs permission, grant, and input checks before these
// adapters run. No domain object is serialized directly except the public catalog DTO.
func providePluginQueries(
	cfg *config.Config,
	public *site.PublicService,
	users *identity.UserService,
	plans *billing.Plans,
	subscriptions *billing.SubscriptionService,
	payments *payment.Runtime,
	usageService *usage.UsageService,
	dashboard *usage.DashboardService,
	marketplace *routing.Marketplace,
	keys *apikey.APIKeyService,
	grants *plugin.Grants,
) *plugin.Queries {
	purchases := &pluginPurchaseQueries{plans: plans, subscriptions: subscriptions, orders: payments.OrderQueries}
	return plugin.NewQueries(plugin.QueryReaders{
		Site:          &pluginSiteQueries{service: public, timezone: cfg.Timezone},
		Users:         &pluginUserQueries{service: users},
		Subscriptions: purchases,
		Usage:         &pluginUsageQueries{service: usageService, dashboard: dashboard},
		Orders:        purchases,
		Catalog:       &pluginCatalogQueries{marketplace: marketplace, keys: keys},
	}, grants)
}

// SiteInfo copies only the public fields promised by the plugin context contract.
func (q *pluginSiteQueries) SiteInfo(ctx context.Context) (plugin.SiteView, error) {
	settings, err := q.service.GetPublicSettings(ctx)
	if err != nil {
		return plugin.SiteView{}, err
	}
	locales := make([]string, 0)
	for _, definition := range locale.Definitions() {
		locales = append(locales, definition.Code)
	}
	return plugin.SiteView{
		Name:              settings.SiteName,
		BalanceUnitName:   settings.BalanceUnitName,
		BalanceUnitSymbol: settings.BalanceUnitSymbol,
		Timezone:          q.timezone,
		DefaultLocale:     settings.DefaultLocale,
		Locales:           locales,
	}, nil
}

// Account copies profile and funding observations into independently authorized projections.
// The existing user service excludes deleted accounts and hydrates avatars. Its
// private credential and management fields never enter either returned plugin value.
func (q *pluginUserQueries) Account(ctx context.Context, id int64) (plugin.AccountView, error) {
	account, err := q.service.GetByID(ctx, id)
	if err != nil {
		return plugin.AccountView{}, pluginUserReadError(err)
	}
	return plugin.AccountView{
		User: plugin.UserView{
			ID:             account.ID,
			Username:       account.Username,
			Role:           account.Role,
			Status:         account.Status,
			CreatedUnix:    pluginTimestamp(&account.CreatedAt),
			LastActiveUnix: pluginTimestamp(account.LastActiveAt),
			SignupSource:   account.SignupSource,
			AvatarURL:      account.AvatarURL,
			Email:          account.Email,
		},
		Wallet: plugin.WalletView{Balance: pluginDecimal(account.Balance), TotalRecharged: pluginDecimal(account.TotalRecharged)},
	}, nil
}

// Lookup maps the dedicated exact profile reader without loading administrative search data.
// Domain validation errors become the plugin's argument error. Email stays available
// only inside the host until the query service applies its separate permission check.
func (q *pluginUserQueries) Lookup(ctx context.Context, input plugin.UserLookup) ([]plugin.UserView, error) {
	profiles, err := q.service.LookupUsersExact(ctx, identity.UserLookup{IDs: input.IDs, Username: input.Username, Email: input.Email})
	if errors.Is(err, identity.ErrUserLookupInvalid) {
		return nil, plugin.ErrInvalidArgument
	}
	if err != nil {
		return nil, pluginUserReadError(err)
	}
	result := make([]plugin.UserView, 0, len(profiles))
	for _, profile := range profiles {
		result = append(result, plugin.UserView{
			ID:             profile.ID,
			Username:       profile.Username,
			Role:           profile.Role,
			Status:         profile.Status,
			CreatedUnix:    pluginTimestamp(&profile.CreatedAt),
			LastActiveUnix: pluginTimestamp(profile.LastActiveAt),
			SignupSource:   profile.SignupSource,
			AvatarURL:      profile.AvatarURL,
			Email:          profile.Email,
		})
	}
	return result, nil
}

// Identities returns live third-party bindings with only standardized display metadata.
// A preliminary exact account read excludes deleted users. Local email identities
// are omitted, and arbitrary upstream claims or credential-bearing metadata are never copied.
func (q *pluginUserQueries) Identities(ctx context.Context, id int64) ([]plugin.IdentityView, error) {
	profiles, err := q.service.LookupUsersExact(ctx, identity.UserLookup{IDs: []int64{id}})
	if err != nil {
		return nil, pluginUserReadError(err)
	}
	if len(profiles) == 0 {
		return nil, plugin.ErrHostUserNotFound
	}
	records, err := q.service.ProfileListUserAuthIdentities(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]plugin.IdentityView, 0, len(records))
	for _, record := range records {
		if record.ProviderType == "email" {
			continue
		}
		// AuthState writes these two standardized profile hints for upstream providers; every other metadata key remains outside the plugin projection.
		username, _ := record.Metadata["username"].(string)
		displayName, _ := record.Metadata["display_name"].(string)
		result = append(result, plugin.IdentityView{
			ProviderType: record.ProviderType,
			ProviderKey:  record.ProviderKey,
			Subject:      record.ProviderSubject,
			Username:     username,
			DisplayName:  displayName,
			VerifiedUnix: pluginTimestamp(record.VerifiedAt),
		})
	}
	return result, nil
}

// Plans returns offered plan names and the day duration used by native issuance.
func (q *pluginPurchaseQueries) Plans(ctx context.Context) ([]plugin.PlanView, error) {
	plans, err := q.plans.ListPlansForSale(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]plugin.PlanView, 0, len(plans))
	for _, plan := range plans {
		days := billing.NormalizeAssignValidityDays(billing.ComputeValidityDays(plan.ValidityDays, plan.ValidityUnit))
		result = append(result, plugin.PlanView{ID: plan.ID, Name: plan.Name, Days: int32(days)})
	}
	return result, nil
}

// UserSubscriptions preserves native active filtering and entitlement status normalization.
// Only declared timeline fields are copied. Missing hydrated plan data leaves
// its display name empty while retaining the stored plan identifier.
func (q *pluginPurchaseQueries) UserSubscriptions(ctx context.Context, id int64, activeOnly bool) ([]plugin.SubscriptionView, error) {
	var records []billing.UserSubscription
	var err error
	if activeOnly {
		records, err = q.subscriptions.ListActiveUserSubscriptions(ctx, id)
	} else {
		records, err = q.subscriptions.ListUserSubscriptions(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	result := make([]plugin.SubscriptionView, 0, len(records))
	for _, record := range records {
		name := ""
		if record.Plan != nil {
			name = billing.LocalizePlan(record.Plan, locale.FromContext(ctx)).Name
		}
		result = append(result, plugin.SubscriptionView{
			ID:          record.ID,
			PlanID:      record.PlanID,
			PlanName:    name,
			Status:      record.Status,
			StartsUnix:  pluginTimestamp(&record.StartsAt),
			ExpiresUnix: pluginTimestamp(&record.ExpiresAt),
		})
	}
	return result, nil
}

// Orders copies native purchase observations without payment channels or callback fields.
// Amount keeps the native order's original denomination and is not converted to
// another currency. Balance-order quantities retain their configured account-unit meaning.
func (q *pluginPurchaseQueries) Orders(ctx context.Context, input plugin.OrderQuery) (plugin.OrderPage, error) {
	orders, total, err := q.orders.GetUserOrders(ctx, input.UserID, payment.OrderListParams{Page: input.Page, PageSize: input.PageSize, Status: input.Status})
	if err != nil {
		return plugin.OrderPage{}, err
	}
	result := plugin.OrderPage{Orders: make([]plugin.OrderView, 0, len(orders)), Total: int64(total)}
	for _, order := range orders {
		planID := int64(0)
		if order.PlanID != nil {
			planID = *order.PlanID
		}
		result.Orders = append(result.Orders, plugin.OrderView{
			ID:          strconv.FormatInt(order.ID, 10),
			Type:        order.OrderType,
			Status:      order.Status,
			Amount:      pluginDecimal(order.Amount),
			PlanID:      planID,
			CreatedUnix: pluginTimestamp(&order.CreatedAt),
			PaidUnix:    pluginTimestamp(order.PaidAt),
		})
	}
	return result, nil
}

// Summary uses actual user charges and native dashboard request/token scopes.
func (q *pluginUsageQueries) Summary(ctx context.Context, id int64) (plugin.UsageSummaryView, error) {
	stats, err := q.service.GetUserDashboardStats(ctx, id)
	if err != nil {
		return plugin.UsageSummaryView{}, err
	}
	return plugin.UsageSummaryView{
		TodayRequests: stats.TodayRequests,
		TodayTokens:   stats.TodayTokens,
		TodayCost:     pluginDecimal(stats.TodayActualCost),
		TotalRequests: stats.TotalRequests,
		TotalTokens:   stats.TotalTokens,
		TotalCost:     pluginDecimal(stats.TotalActualCost),
	}, nil
}

// Stats combines native batch charges with one explicit actor counter query.
// Charge behavior and platform handling stay with the existing dashboard method.
// Missing map entries contribute zero observations for their requested actor ID.
// Both reports use the same requested interval but remain separate database
// observations, so concurrent ingestion can advance data between their reads.
// Rows are joined by the explicit user ID, never map iteration order. Two bulk
// reads supply the extra counters without adding a query for every requested user.
func (q *pluginUsageQueries) Stats(ctx context.Context, ids []int64, start, end time.Time) ([]plugin.UserUsageView, error) {
	charges, err := q.dashboard.GetBatchUserUsageStats(ctx, ids, start, end)
	if err != nil {
		return nil, err
	}
	counts, err := q.dashboard.GetBatchUserUsageCounts(ctx, ids, start, end)
	if err != nil {
		return nil, err
	}
	result := make([]plugin.UserUsageView, 0, len(ids))
	for _, id := range ids {
		cost := 0.0
		if value := charges[id]; value != nil {
			cost = value.TotalActualCost
		}
		result = append(result, plugin.UserUsageView{UserID: id, Requests: counts[id].Requests, Tokens: counts[id].Tokens, Cost: pluginDecimal(cost)})
	}
	return result, nil
}

// Ranking retains the public display-name masking already performed by usage storage.
// The plugin capability authorizes this query independently of website visibility.
// Sort names are translated to the existing fixed native ordering identifiers.
func (q *pluginUsageQueries) Ranking(ctx context.Context, start, end time.Time, limit int, sortBy string) ([]plugin.RankingView, error) {
	metric := usage.UsageRankingSortByTotalTokens
	switch sortBy {
	case "requests":
		metric = usage.UsageRankingSortByRequests
	case "cost":
		metric = usage.UsageRankingSortByActualCost
	}
	report, err := q.service.GetUsageRanking(ctx, start, end, limit, metric)
	if err != nil {
		return nil, err
	}
	result := make([]plugin.RankingView, 0, len(report.Ranking))
	for _, row := range report.Ranking {
		result = append(result, plugin.RankingView{
			Rank:        int32(row.Rank),
			UserID:      row.UserID,
			DisplayName: row.DisplayName,
			AvatarURL:   row.AvatarURL,
			Requests:    row.Requests,
			Tokens:      row.TotalTokens,
			Cost:        pluginDecimal(row.ActualCost),
		})
	}
	return result, nil
}

// Models serializes the same public catalog DTO and success envelope as its HTTP endpoint.
// Capacity uses that endpoint's default enabled choice. Projection fields remain
// read-only during encoding, preserving the routing service's shared catalog observations.
func (q *pluginCatalogQueries) Models(ctx context.Context) (json.RawMessage, error) {
	groups, err := q.marketplace.ListPublic(ctx, routing.MarketplaceListOptions{IncludeCapacity: true})
	if err != nil {
		return nil, err
	}
	return json.Marshal(httpx.Response{Code: 0, Message: "success", Data: dto.ModelMarketplaceGroupsFromRouting(groups)})
}

// Groups returns native eligibility and applies account overrides including explicit zero rates.
// Public display copy is localized through routing's existing helper. Internal
// credentials, provider mappings, and group policy objects never enter the result.
func (q *pluginCatalogQueries) Groups(ctx context.Context, id int64) ([]plugin.GroupView, error) {
	groups, err := q.keys.GetAvailableGroups(ctx, id)
	if err != nil {
		return nil, pluginUserReadError(err)
	}
	rates, err := q.keys.GetUserGroupRates(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]plugin.GroupView, 0, len(groups))
	for _, group := range groups {
		rate := group.RateMultiplier
		if override, exists := rates[group.ID]; exists {
			rate = override
		}
		display, _ := routing.GroupDisplay(&group, locale.FromContext(ctx))
		result = append(result, plugin.GroupView{ID: group.ID, Name: group.Name, DisplayName: display.DisplayName, Description: display.Description, RateMultiplier: rate})
	}
	return result, nil
}

// pluginTimestamp converts optional domain observations to the protocol's absent-time zero.
// Missing or zero timestamps return zero. Other values retain their instant
// through Unix seconds without changing the domain object's timezone or precision.
func pluginTimestamp(value *time.Time) int64 {
	if value == nil || value.IsZero() {
		return 0
	}
	return value.Unix()
}

// pluginDecimal preserves the domain's reported finite number as plain decimal text.
func pluginDecimal(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// pluginUserReadError preserves known account absence under the stable plugin error reason.
func pluginUserReadError(err error) error {
	if errors.Is(err, identity.ErrUserNotFound) {
		return plugin.ErrHostUserNotFound
	}
	return err
}
