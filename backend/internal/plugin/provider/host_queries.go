package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// host_queries.go maps read-only host RPCs into the authorized query service.
// Explicit protocol projections copy only declared observations after core
// permission, grant, and input checks. Domain entities and arbitrary identity or
// payment metadata never pass through this transport adapter's response objects.

// GetSiteInfo returns the public branding and calendar context available to every plugin.
func (s *hostService) GetSiteInfo(ctx context.Context, _ *pluginpb.GetSiteInfoRequest) (*pluginpb.SiteInfo, error) {
	value, err := s.services.capabilities.Queries.SiteInfo(ctx)
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.SiteInfo{
		SiteName:          value.Name,
		BalanceUnitName:   value.BalanceUnitName,
		BalanceUnitSymbol: value.BalanceUnitSymbol,
		Timezone:          value.Timezone,
		DefaultLocale:     value.DefaultLocale,
		Locales:           value.Locales,
	}, nil
}

// GetUser returns a permitted profile after the core applies email visibility.
func (s *hostService) GetUser(ctx context.Context, request *pluginpb.GetUserRequest) (*pluginpb.User, error) {
	value, err := s.services.capabilities.Queries.User(ctx, s.binding, request.GetUserId())
	if err != nil {
		return nil, capabilityError(err)
	}
	return queryUser(value), nil
}

// LookupUsers returns exact profile matches after selector and email capability checks.
func (s *hostService) LookupUsers(ctx context.Context, request *pluginpb.LookupUsersRequest) (*pluginpb.LookupUsersResponse, error) {
	values, err := s.services.capabilities.Queries.LookupUsers(ctx, s.binding, plugin.UserLookup{IDs: request.GetIds(), Username: request.GetUsername(), Email: request.GetEmail()})
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.LookupUsersResponse{Users: make([]*pluginpb.User, 0, len(values))}
	for _, value := range values {
		response.Users = append(response.Users, queryUser(value))
	}
	return response, nil
}

// queryUser serializes only the already authorized profile fields in the protocol.
func queryUser(value plugin.UserView) *pluginpb.User {
	return &pluginpb.User{
		Id:             value.ID,
		Username:       value.Username,
		Role:           value.Role,
		Status:         value.Status,
		CreatedUnix:    value.CreatedUnix,
		LastActiveUnix: value.LastActiveUnix,
		SignupSource:   value.SignupSource,
		AvatarUrl:      value.AvatarURL,
		Email:          value.Email,
	}
}

// ListUserIdentities returns only third-party subjects and whitelisted public display metadata.
func (s *hostService) ListUserIdentities(ctx context.Context, request *pluginpb.ListUserIdentitiesRequest) (*pluginpb.ListUserIdentitiesResponse, error) {
	values, err := s.services.capabilities.Queries.Identities(ctx, s.binding, request.GetUserId())
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.ListUserIdentitiesResponse{Identities: make([]*pluginpb.UserIdentity, 0, len(values))}
	for _, value := range values {
		response.Identities = append(response.Identities, &pluginpb.UserIdentity{
			ProviderType: value.ProviderType,
			ProviderKey:  value.ProviderKey,
			Subject:      value.Subject,
			Username:     value.Username,
			DisplayName:  value.DisplayName,
			VerifiedUnix: value.VerifiedUnix,
		})
	}
	return response, nil
}

// GetWallet returns funding observations independently of profile and email permissions.
func (s *hostService) GetWallet(ctx context.Context, request *pluginpb.GetWalletRequest) (*pluginpb.Wallet, error) {
	value, err := s.services.capabilities.Queries.Wallet(ctx, s.binding, request.GetUserId())
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.Wallet{Balance: value.Balance, TotalRecharged: value.TotalRecharged}, nil
}

// ListSubscriptionPlans returns offered plans and their normalized default calendar-day durations.
func (s *hostService) ListSubscriptionPlans(ctx context.Context, _ *pluginpb.ListSubscriptionPlansRequest) (*pluginpb.ListSubscriptionPlansResponse, error) {
	values, err := s.services.capabilities.Queries.Plans(ctx, s.binding)
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.ListSubscriptionPlansResponse{Plans: make([]*pluginpb.SubscriptionPlan, 0, len(values))}
	for _, value := range values {
		response.Plans = append(response.Plans, &pluginpb.SubscriptionPlan{Id: value.ID, Name: value.Name, ValidityDays: value.Days})
	}
	return response, nil
}

// ListUserSubscriptions returns declared timeline fields from the existing entitlement reader.
func (s *hostService) ListUserSubscriptions(ctx context.Context, request *pluginpb.ListUserSubscriptionsRequest) (*pluginpb.ListUserSubscriptionsResponse, error) {
	values, err := s.services.capabilities.Queries.Subscriptions(ctx, s.binding, request.GetUserId(), request.GetActiveOnly())
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.ListUserSubscriptionsResponse{Subscriptions: make([]*pluginpb.UserSubscription, 0, len(values))}
	for _, value := range values {
		response.Subscriptions = append(response.Subscriptions, &pluginpb.UserSubscription{
			Id:          value.ID,
			PlanId:      value.PlanID,
			PlanName:    value.PlanName,
			Status:      value.Status,
			StartsUnix:  value.StartsUnix,
			ExpiresUnix: value.ExpiresUnix,
		})
	}
	return response, nil
}

// GetUsageSummary applies caller-only or broad usage authority before returning account totals.
func (s *hostService) GetUsageSummary(ctx context.Context, request *pluginpb.GetUsageSummaryRequest) (*pluginpb.UsageSummary, error) {
	value, err := s.services.capabilities.Queries.UsageSummary(ctx, s.binding, request.GetUserId(), request.GetGrant())
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.UsageSummary{
		TodayRequests: value.TodayRequests,
		TodayTokens:   value.TodayTokens,
		TodayCost:     value.TodayCost,
		TotalRequests: value.TotalRequests,
		TotalTokens:   value.TotalTokens,
		TotalCost:     value.TotalCost,
	}, nil
}

// GetUsageStats returns bounded-period actor counts and charges for the requested batch.
func (s *hostService) GetUsageStats(ctx context.Context, request *pluginpb.GetUsageStatsRequest) (*pluginpb.GetUsageStatsResponse, error) {
	values, err := s.services.capabilities.Queries.UsageStats(ctx, s.binding, request.GetUserIds(), request.GetStartUnix(), request.GetEndUnix())
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.GetUsageStatsResponse{Stats: make([]*pluginpb.UserUsageStats, 0, len(values))}
	for _, value := range values {
		response.Stats = append(response.Stats, &pluginpb.UserUsageStats{UserId: value.UserID, Requests: value.Requests, Tokens: value.Tokens, Cost: value.Cost})
	}
	return response, nil
}

// GetUsageRanking preserves native public identity masking and authorized measurement fields.
func (s *hostService) GetUsageRanking(ctx context.Context, request *pluginpb.GetUsageRankingRequest) (*pluginpb.GetUsageRankingResponse, error) {
	values, err := s.services.capabilities.Queries.UsageRanking(ctx, s.binding, request.GetStartUnix(), request.GetEndUnix(), int(request.GetLimit()), request.GetSortBy())
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.GetUsageRankingResponse{Rows: make([]*pluginpb.UsageRankingRow, 0, len(values))}
	for _, value := range values {
		response.Rows = append(response.Rows, &pluginpb.UsageRankingRow{
			Rank:        value.Rank,
			UserId:      value.UserID,
			DisplayName: value.DisplayName,
			AvatarUrl:   value.AvatarURL,
			Requests:    value.Requests,
			Tokens:      value.Tokens,
			Cost:        value.Cost,
		})
	}
	return response, nil
}

// ListUserOrders maps a bounded owned purchase page without channel or callback data.
func (s *hostService) ListUserOrders(ctx context.Context, request *pluginpb.ListUserOrdersRequest) (*pluginpb.ListUserOrdersResponse, error) {
	page, err := s.services.capabilities.Queries.Orders(ctx, s.binding, plugin.OrderQuery{UserID: request.GetUserId(), Page: int(request.GetPage()), PageSize: int(request.GetPageSize()), Status: request.GetStatus()})
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.ListUserOrdersResponse{Orders: make([]*pluginpb.Order, 0, len(page.Orders)), Total: page.Total}
	for _, value := range page.Orders {
		response.Orders = append(response.Orders, &pluginpb.Order{
			Id:          value.ID,
			Type:        value.Type,
			Status:      value.Status,
			Amount:      value.Amount,
			PlanId:      value.PlanID,
			CreatedUnix: value.CreatedUnix,
			PaidUnix:    value.PaidUnix,
		})
	}
	return response, nil
}

// ListModels returns the public model endpoint's encoded success envelope without reinterpretation.
func (s *hostService) ListModels(ctx context.Context, _ *pluginpb.ListModelsRequest) (*pluginpb.ListModelsResponse, error) {
	data, err := s.services.capabilities.Queries.Models(ctx, s.binding)
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.ListModelsResponse{ModelsJson: data}, nil
}

// ListUserGroups returns only eligible group labels and effective account rate multipliers.
func (s *hostService) ListUserGroups(ctx context.Context, request *pluginpb.ListUserGroupsRequest) (*pluginpb.ListUserGroupsResponse, error) {
	values, err := s.services.capabilities.Queries.Groups(ctx, s.binding, request.GetUserId())
	if err != nil {
		return nil, capabilityError(err)
	}
	response := &pluginpb.ListUserGroupsResponse{Groups: make([]*pluginpb.UserGroup, 0, len(values))}
	for _, value := range values {
		response.Groups = append(response.Groups, &pluginpb.UserGroup{Id: value.ID, Name: value.Name, DisplayName: value.DisplayName, Description: value.Description, RateMultiplier: value.RateMultiplier})
	}
	return response, nil
}
