package selection

import (
	"context"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// selectAccountByPreviousResponseIDForCapability 使用已完成渠道及分组映射的账号层模型校验响应链账号。
func (s *Compatible) selectAccountByPreviousResponseIDForCapability(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredCapability accountcore.OpenAIEndpointCapability,
	requireCompact bool,
) (*gatewayprovider.SelectionResult, error) {
	if s == nil {
		return nil, nil
	}
	accountID, account, responseID, store := s.resolveAccountByPreviousResponseIDForCapability(
		ctx,
		groupID,
		previousResponseID,
		routingModel,
		excludedIDs,
		requiredCapability,
		requireCompact,
	)
	if accountID <= 0 || account == nil || store == nil {
		return nil, nil
	}

	result, acquireErr := s.tryAcquireAccountSlot(ctx, accountID, account.Record.Concurrency)
	if acquireErr == nil && result.Acquired {
		gatewayprovider.LogOpenAIWSBindResponseAccountWarn(
			derefGroupID(groupID),
			accountID,
			responseID,
			store.BindResponseAccount(ctx, derefGroupID(groupID), responseID, accountID, s.OpenAIHTTPResponseStickyTTL()),
		)
		return &gatewayprovider.SelectionResult{
			Account:     account,
			Acquired:    true,
			ReleaseFunc: result.ReleaseFunc,
		}, nil
	}

	cfg := s.schedulingConfig()
	if s.concurrencyService != nil {
		return &gatewayprovider.SelectionResult{
			Account: account,
			WaitPlan: &schedulercore.AccountWaitPlan{
				AccountID:      accountID,
				MaxConcurrency: account.Record.Concurrency,
				Timeout:        cfg.StickySessionWaitTimeout,
				MaxWaiting:     cfg.StickySessionMaxWaiting,
			},
		}, nil
	}
	return nil, nil
}

// ResolveAccountIDByPreviousResponseIDForScheduler 使用账号层模型解析可继续承载指定响应链的账号。
func (s *Compatible) ResolveAccountIDByPreviousResponseIDForScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredCapability accountcore.OpenAIEndpointCapability,
	requireCompact bool,
) int64 {
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	accountID, _, _, _ := s.resolveAccountByPreviousResponseIDForCapability(
		ctx,
		groupID,
		previousResponseID,
		routingModel,
		excludedIDs,
		requiredCapability,
		requireCompact,
	)
	return accountID
}

// resolveAccountByPreviousResponseIDForCapability 校验响应链绑定账号的模型、能力和渠道限制。
func (s *Compatible) resolveAccountByPreviousResponseIDForCapability(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredCapability accountcore.OpenAIEndpointCapability,
	requireCompact bool,
) (int64, *gatewayprovider.ExecutionAccount, string, session.OpenAIWSStateStore) {
	if s == nil {
		return 0, nil, "", nil
	}
	responseID := strings.TrimSpace(previousResponseID)
	if responseID == "" {
		return 0, nil, "", nil
	}
	routingModel = strings.TrimSpace(routingModel)
	store := s.ResponseStateStore()
	if store == nil {
		return 0, nil, "", nil
	}

	accountID, err := store.GetResponseAccount(ctx, derefGroupID(groupID), responseID)
	if err != nil || accountID <= 0 {
		return 0, nil, "", nil
	}
	if excludedIDs != nil {
		if _, excluded := excludedIDs[accountID]; excluded {
			return 0, nil, "", nil
		}
	}

	account, err := s.getSchedulableAccount(ctx, accountID)
	if err != nil || account == nil {
		_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
		return 0, nil, "", nil
	}

	if s.ResolveTransport(account).Transport != egress.OpenAIUpstreamTransportResponsesWebsocketV2 && !account.View().IsOpenAIApiKey() {
		return 0, nil, "", nil
	}
	if shouldClearStickySession(account, routingModel) || !account.View().IsOpenAI() || !account.View().IsSchedulable() {
		_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
		return 0, nil, "", nil
	}
	if (hasOpenAIAccountGroupMetadata(account) && !s.openAIAccountMatchesSchedulingGroup(account, groupID)) || !s.openAIAccountPassesPrivacyRequirement(ctx, groupID, account) {
		return 0, nil, "", nil
	}
	if !s.shadowProtocolsAllowed(ctx, account) || !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(account), func(id int64) *accountcore.Record {
		return gatewayprovider.ExecutionRecord(s.parentAccountLookup(ctx)(id))
	}) {
		_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
		return 0, nil, "", nil
	}
	if !gatewayprovider.ExecutionModelPolicy(account).SupportsCompatibleRouting(ctx, routingModel) {
		return 0, nil, "", nil
	}
	if !accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(account), requiredCapability) {
		return 0, nil, "", nil
	}

	if paused, _ := gatewayprovider.OpenAIQuotaPause(ctx, account); paused {
		return 0, nil, "", nil
	}
	if s.schedulerSnapshot != nil && s.accountRepo != nil {
		latest, latestErr := s.accountRepo.GetByID(ctx, account.Record.ID)
		if latestErr != nil || latest == nil {
			_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
			return 0, nil, "", nil
		}
		if shouldClearStickySession(latest, routingModel) || !latest.View().IsOpenAI() || !latest.View().IsSchedulable() {
			_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
			return 0, nil, "", nil
		}
		if (hasOpenAIAccountGroupMetadata(latest) && !s.openAIAccountMatchesSchedulingGroup(latest, groupID)) || !s.openAIAccountPassesPrivacyRequirement(ctx, groupID, latest) {
			return 0, nil, "", nil
		}
		if !s.shadowProtocolsAllowed(ctx, latest) || !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(latest), func(id int64) *accountcore.Record {
			return gatewayprovider.ExecutionRecord(s.parentAccountLookup(ctx)(id))
		}) {
			_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
			return 0, nil, "", nil
		}
		if !gatewayprovider.ExecutionModelPolicy(latest).SupportsCompatibleRouting(ctx, routingModel) {
			return 0, nil, "", nil
		}
		if !accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(latest), requiredCapability) {
			return 0, nil, "", nil
		}
		if paused, _ := gatewayprovider.OpenAIQuotaPause(ctx, latest); paused {
			return 0, nil, "", nil
		}
		if s.isOpenAIAccountRequestRuntimeBlocked(latest, routingModel) {
			_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
			return 0, nil, "", nil
		}
		account = latest
	}
	if requireCompact && !gatewayprovider.AllowsCompatibleCompact(account) {
		_ = store.DeleteResponseAccount(ctx, derefGroupID(groupID), responseID)
		return 0, nil, "", nil
	}
	if groupID != nil && s.NeedsUpstreamChannelRestriction(ctx, groupID) &&
		s.UpstreamRoutingModelRestricted(ctx, *groupID, account, routingModel, requireCompact) {
		return 0, nil, "", nil
	}
	return accountID, account, responseID, store
}
