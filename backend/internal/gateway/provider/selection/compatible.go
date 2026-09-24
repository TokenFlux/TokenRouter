package selection

import (
	"context"
	"fmt"
	"strings"

	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// BindStickySession sets session -> account binding with standard TTL.
func (s *Compatible) BindStickySession(ctx context.Context, groupID *int64, sessionHash string, accountID int64) error {
	if sessionHash == "" || accountID <= 0 {
		return nil
	}
	if requeststate.PreserveGuardianParentBinding(ctx, sessionHash) {
		return nil
	}
	ttl := s.SessionStickyTTL()
	return s.setStickySessionAccountID(ctx, groupID, sessionHash, accountID, ttl)
}

// SelectAccount selects an OpenAI account with sticky session support
func (s *Compatible) SelectAccount(ctx context.Context, groupID *int64, sessionHash string) (*gatewayprovider.ExecutionAccount, error) {
	return s.SelectAccountForModel(ctx, groupID, sessionHash, "")
}

// SelectAccountForModel selects an account supporting the requested model
func (s *Compatible) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*gatewayprovider.ExecutionAccount, error) {
	return s.SelectAccountForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

// SelectAccountForModelWithExclusions selects an account supporting the requested model while excluding specified accounts.
// SelectAccountForModelWithExclusions 选择支持指定模型的账号，同时排除指定的账号。
func (s *Compatible) SelectAccountForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.ExecutionAccount, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	resolvedCtx, resolvedGroupID, err := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	ctx = resolvedCtx
	groupID = resolvedGroupID
	if s.groupUsesAdvancedScheduler(ctx, groupID) {
		selection, _, selectErr := s.SelectAccountWithScheduler(
			schedulercore.WithSelectOnly(ctx),
			groupID,
			"",
			sessionHash,
			requestedModel,
			excludedIDs, egress.OpenAIUpstreamTransportAny, false,
		)
		if selectErr != nil {
			return nil, selectErr
		}
		if selection == nil || selection.Account == nil {
			return nil, schedulercore.ErrNoAvailableAccounts
		}
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return selection.Account, nil
	}
	return s.selectAccountForModelWithExclusions(ctx, groupID, capability.PlatformOpenAI, sessionHash, requestedModel, excludedIDs, false, 0, "")
}

func shouldUseGroupModelUnsupportedError(ctx context.Context, accounts []gatewayprovider.ExecutionAccount, requestedModel string) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return false
	}
	hasRelevantAccount := false
	for i := range accounts {
		acc := &accounts[i]
		if !acc.View().IsOpenAI() || !acc.View().IsSchedulable() {
			continue
		}
		hasRelevantAccount = true
		if gatewayprovider.ExecutionModelPolicy(acc).SupportsCompatibleRouting(ctx, requestedModel) {
			return false
		}
	}
	return hasRelevantAccount
}

// NormalizeOpenAICompatiblePlatform 保留 OpenAI 网关正式支持的平台标识，
// 其它输入回退 OpenAI，避免把未知平台带入账号查询。
// SelectAccountForTokenCount selects an account for a non-billable token-count
// request. It applies the normal platform, model, capability, and runtime
// eligibility checks without acquiring or waiting for a generation slot.
func (s *Compatible) SelectAccountForTokenCount(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	requiredCapability accountcore.OpenAIEndpointCapability,
	platform string,
) (*gatewayprovider.ExecutionAccount, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	return s.selectAccountForModelWithExclusions(
		ctx,
		groupID,
		platform,
		sessionHash,
		requestedModel,
		nil,
		false,
		0,
		requiredCapability,
	)
}

// noAvailableOpenAISelectionErrorForRouting 使用账号层模型 C/D 判断能力，同时保留 R 的对外错误语义。
func noAvailableOpenAISelectionErrorForRouting(ctx context.Context, requestedModel string, routingModel string, compactBlocked bool, accounts ...[]gatewayprovider.ExecutionAccount) error {
	return noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, requestedModel, routingModel, compactBlocked, "", accounts...)
}

// noAvailableOpenAISelectionErrorForRoutingWithDetails 仅在通用无账号错误中追加调度诊断；
// compact 能力错误和 fork 的模型业务错误继续保留原有类型与消息。
func noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx context.Context, requestedModel string, routingModel string, compactBlocked bool, details string, accounts ...[]gatewayprovider.ExecutionAccount) error {
	if compactBlocked {
		return schedulercore.ErrNoAvailableCompactAccounts
	}
	if len(accounts) > 0 && shouldUseGroupModelUnsupportedError(ctx, accounts[0], routingModel) {
		if err := routing.NewGroupModelRejection(capability.PlatformOpenAI, requestedModel, modelRejectionSources(accounts[0])); err != nil {
			return err
		}
	}
	message := "no available OpenAI accounts"
	if requestedModel != "" {
		message = fmt.Sprintf("no available OpenAI accounts supporting model: %s", requestedModel)
	}
	if details != "" {
		message += " (" + details + ")"
	}
	return openAINoAvailableSelectionError{message: message}
}

// openAINoAvailableSelectionError 保留原有可读消息，同时支持 errors.Is 统一分类。
type openAINoAvailableSelectionError struct {
	message string
}

func (e openAINoAvailableSelectionError) Error() string {
	return e.message
}

func (e openAINoAvailableSelectionError) Unwrap() error {
	return schedulercore.ErrNoAvailableAccounts
}

func (s *Compatible) withOpenAIQuotaAutoPauseContext(ctx context.Context) context.Context {
	if s == nil || s.quotaSettings == nil {
		return ctx
	}
	return gatewayprovider.WithQuotaAutoPauseSettings(ctx, s.quotaSettings.GetOpenAIQuotaAutoPauseSettings(ctx))
}

func (s *Compatible) selectAccountForModelWithExclusions(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyAccountID int64, requiredCapability accountcore.OpenAIEndpointCapability) (*gatewayprovider.ExecutionAccount, error) {
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.selectAccountForModelWithExclusionsForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, stickyAccountID, requiredCapability)
}

// selectAccountForModelWithExclusionsForRouting 使用已解析的账号层模型执行旧版调度。
func (s *Compatible) selectAccountForModelWithExclusionsForRouting(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyAccountID int64, requiredCapability accountcore.OpenAIEndpointCapability) (*gatewayprovider.ExecutionAccount, error) {
	core, scope := (&compatiblePicker{service: s}).platformSelector()
	selected, err := core.SelectBasicOnly(ctx, schedulercore.PlatformSelectionInput{GroupID: groupID, Platform: platform, SessionHash: sessionHash, RequestedModel: requestedModel, RoutingModel: routingModel, ExcludedIDs: excludedIDs, RequireCompact: requireCompact, StickyAccountID: stickyAccountID, RequiredCapability: requiredCapability})
	return scope.oldAccount(selected), err
}

// SelectAccountWithLoadAwareness selects an account with load-awareness and wait plan.
func (s *Compatible) SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.SelectionResult, error) {
	return s.selectAccountWithLoadAwareness(s.withOpenAIQuotaAutoPauseContext(ctx), groupID, capability.PlatformOpenAI, sessionHash, requestedModel, excludedIDs, false, "")
}

func (s *Compatible) selectAccountWithLoadAwareness(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability accountcore.OpenAIEndpointCapability) (*gatewayprovider.SelectionResult, error) {
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, requiredCapability)
}

// selectAccountWithLoadAwarenessForRouting 使用已解析的账号层模型执行负载感知调度。
func (s *Compatible) selectAccountWithLoadAwarenessForRouting(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability accountcore.OpenAIEndpointCapability) (*gatewayprovider.SelectionResult, error) {
	core, scope := (&compatiblePicker{service: s}).platformSelector()
	selected, err := core.SelectBasic(ctx, schedulercore.PlatformSelectionInput{GroupID: groupID, Platform: platform, SessionHash: sessionHash, RequestedModel: requestedModel, RoutingModel: routingModel, ExcludedIDs: excludedIDs, RequireCompact: requireCompact, RequiredCapability: requiredCapability})
	return scope.restore(selected), err
}

func (s *Compatible) listSchedulableAccounts(ctx context.Context, groupID *int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)
	if s.schedulerSnapshot != nil {
		accounts, _, err := readSnapshotAccounts(ctx, s.schedulerSnapshot, groupID, platform, false)
		if err != nil {
			return accounts, err
		}
		accounts = s.filterOpenAIAccountsBySchedulingThreshold(ctx, accounts)
		if platform == capability.PlatformGrok {
			accounts = s.filterGrokFreeQuotaAccountsForOpenAI(ctx, accounts)
		}
		return accounts, nil
	}
	var accounts []gatewayprovider.ExecutionAccount
	var err error
	if s.options.Simple {
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, platform)
	} else if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	} else {
		accounts, err = s.accountRepo.ListSchedulableUngroupedByPlatform(ctx, platform)
	}
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}
	accounts = s.filterOpenAIAccountsBySchedulingThreshold(ctx, accounts)
	if platform == capability.PlatformGrok {
		accounts = s.filterGrokFreeQuotaAccountsForOpenAI(ctx, accounts)
	}
	return accounts, nil
}

func (s *Compatible) tryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*schedulercore.AcquireResult, error) {
	if schedulercore.IsSelectOnly(ctx) {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	if s.concurrencyService == nil {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	return s.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
}

func (s *Compatible) resolveFreshSchedulableOpenAIAccount(ctx context.Context, account *gatewayprovider.ExecutionAccount, platform string, requestedModel string, requireCompact bool, requiredCapability accountcore.OpenAIEndpointCapability) *gatewayprovider.ExecutionAccount {
	if account == nil {
		return nil
	}
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)

	fresh := account
	if s.schedulerSnapshot != nil {
		current, err := s.getSchedulableAccount(ctx, account.Record.ID)
		if err != nil || current == nil {
			return nil
		}
		fresh = current
	}

	if !gatewayprovider.CompatibleAccountEligible(ctx, fresh, platform, requestedModel, requireCompact, requiredCapability) {
		return nil
	}
	if !s.shadowProtocolsAllowed(ctx, fresh) || !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(fresh), func(id int64) *accountcore.Record {
		return gatewayprovider.ExecutionRecord(s.parentAccountLookup(ctx)(id))
	}) {
		return nil
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(fresh, requestedModel) {
		return nil
	}
	if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, fresh) {
		return nil
	}
	if s.isOpenAIProxyStreamQuarantined(ctx, fresh) {
		return nil
	}
	return fresh
}

// parentAccountLookup 返回供 parentHealthyForShadow 使用的母账号解析闭包:经 accountRepo
// 按 ID 取当前 Account(repo 为空时 fail-closed 返回 nil)。统一调度/粘连各路径的母账号解析,
// 取代各调用点重复内联的同一闭包(历史上 recheck 等路径还漏写过 accountRepo==nil 守卫)。
// L2 候选循环改用带 per-pass 缓存的 parentLookupL2,不走此方法。
func (s *Compatible) parentAccountLookup(ctx context.Context) func(int64) *gatewayprovider.ExecutionAccount {
	return func(id int64) *gatewayprovider.ExecutionAccount {
		if s.accountRepo == nil {
			return nil
		}
		a, _ := s.accountRepo.GetByID(ctx, id)
		return a
	}
}

func (s *Compatible) recheckSelectedOpenAIAccountFromDB(ctx context.Context, account *gatewayprovider.ExecutionAccount, groupID *int64, platform string, requestedModel string, requireCompact bool, requiredCapability accountcore.OpenAIEndpointCapability) *gatewayprovider.ExecutionAccount {
	if account == nil {
		return nil
	}
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)
	if s.schedulerSnapshot == nil || s.accountRepo == nil {
		if !s.openAIAccountPassesPrivacyRequirement(ctx, groupID, account) {
			return nil
		}
		if !gatewayprovider.CompatibleAccountEligible(ctx, account, platform, requestedModel, requireCompact, requiredCapability) {
			return nil
		}
		if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, account) {
			return nil
		}
		if !s.shadowProtocolsAllowed(ctx, account) || !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(account), func(id int64) *accountcore.Record {
			return gatewayprovider.ExecutionRecord(s.parentAccountLookup(ctx)(id))
		}) {
			return nil
		}
		if s.isOpenAIProxyStreamQuarantined(ctx, account) {
			return nil
		}
		return account
	}

	latest, err := s.accountRepo.GetByID(ctx, account.Record.ID)
	if err != nil || latest == nil {
		return nil
	}
	if !s.openAIAccountMatchesSchedulingGroup(latest, groupID) {
		return nil
	}
	if !s.openAIAccountPassesPrivacyRequirement(ctx, groupID, latest) {
		return nil
	}
	if !gatewayprovider.CompatibleAccountEligible(ctx, latest, platform, requestedModel, requireCompact, requiredCapability) {
		return nil
	}
	if !s.shadowProtocolsAllowed(ctx, latest) || !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(latest), func(id int64) *accountcore.Record {
		return gatewayprovider.ExecutionRecord(s.parentAccountLookup(ctx)(id))
	}) {
		return nil
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(latest, requestedModel) {
		return nil
	}
	if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, latest) {
		return nil
	}
	if s.isOpenAIProxyStreamQuarantined(ctx, latest) {
		return nil
	}
	return latest
}

func (s *Compatible) openAIAccountMatchesSchedulingGroup(account *gatewayprovider.ExecutionAccount, groupID *int64) bool {
	if s != nil && s.options.Simple {
		return account != nil
	}
	return openAIStickyAccountMatchesGroup(account, groupID)
}

// openAIAccountPassesPrivacyRequirement 判断账号是否满足当前分组的隐私资格。
func (s *Compatible) openAIAccountPassesPrivacyRequirement(ctx context.Context, groupID *int64, account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && (!s.openAIGroupRequiresPrivacySet(ctx, groupID) || account.View().IsPrivacySet())
}

func (s *Compatible) getSchedulableAccount(ctx context.Context, accountID int64) (*gatewayprovider.ExecutionAccount, error) {
	var (
		account *gatewayprovider.ExecutionAccount
		err     error
	)
	if s.schedulerSnapshot != nil {
		account, err = readSnapshotAccount(ctx, s.schedulerSnapshot, accountID)
	} else {
		account, err = s.accountRepo.GetByID(ctx, accountID)
	}
	if err != nil || account == nil {
		return account, err
	}
	if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, account) {
		return nil, nil
	}

	if account.View().IsGrok() {
		if gated := s.filterGrokFreeQuotaAccountsForOpenAI(ctx, []gatewayprovider.ExecutionAccount{*account}); len(gated) == 0 {
			return nil, nil
		}
	}
	return account, nil
}

// filterGrokFreeQuotaAccountsForOpenAI 为 OpenAI 兼容旧版选择路径应用与
// 本地免费层软性限制与通用选择使用同一规则。
func (s *Compatible) filterGrokFreeQuotaAccountsForOpenAI(ctx context.Context, accounts []gatewayprovider.ExecutionAccount) []gatewayprovider.ExecutionAccount {
	if s == nil {
		return accounts
	}
	return filterFreeQuotaProjection(s.freeQuotaGate, accounts)
}

func (s *Compatible) filterOpenAIAccountsBySchedulingThreshold(ctx context.Context, accounts []gatewayprovider.ExecutionAccount) []gatewayprovider.ExecutionAccount {
	if len(accounts) == 0 {
		return accounts
	}

	filtered := make([]gatewayprovider.ExecutionAccount, 0, len(accounts))
	for i := range accounts {
		if s.isOpenAIAccountBlockedBySchedulingThreshold(ctx, &accounts[i]) {
			continue
		}
		filtered = append(filtered, accounts[i])
	}
	return filtered
}

func (s *Compatible) isOpenAIAccountBlockedBySchedulingThreshold(ctx context.Context, account *gatewayprovider.ExecutionAccount) bool {
	if s == nil || s.healthObserver == nil || account == nil {
		return false
	}
	return gatewayprovider.ApplyExecutionSchedulingThreshold(ctx, s.healthObserver, account)

}

func (s *Compatible) hydrateSelectedAccount(ctx context.Context, account *gatewayprovider.ExecutionAccount) (*gatewayprovider.ExecutionAccount, error) {
	if account == nil || s.schedulerSnapshot == nil {
		return account, nil
	}
	hydrated, err := readSnapshotAccount(ctx, s.schedulerSnapshot, account.Record.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected openai account %d not found during hydration", account.Record.ID)
	}
	return hydrated, nil
}

func (s *Compatible) newSelectionResult(ctx context.Context, account *gatewayprovider.ExecutionAccount, acquired bool, release func(), waitPlan *schedulercore.AccountWaitPlan) (*gatewayprovider.SelectionResult, error) {
	hydrated, err := s.hydrateSelectedAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	return &gatewayprovider.SelectionResult{
		Account:     hydrated,
		Acquired:    acquired,
		ReleaseFunc: release,
		WaitPlan:    waitPlan,
	}, nil
}

func (s *Compatible) newAcquiredSelectionResult(ctx context.Context, account *gatewayprovider.ExecutionAccount, release func()) (*gatewayprovider.SelectionResult, error) {
	selection, err := s.newSelectionResult(ctx, account, true, release, nil)
	if err != nil && release != nil {
		release()
	}
	return selection, err
}

func (s *Compatible) schedulingConfig() schedulercore.FlowOptions {
	return s.options.Scheduling

}
