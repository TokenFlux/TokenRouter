package httpapi

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

func (s *managementListFixture) ListAccounts(ctx context.Context, page, pageSize int, platform, accountType, status, search string, groupID int64, privacyMode string, sortBy, sortOrder string) ([]account.Record, int64, error) {
	s.lastListAccounts.platform = platform
	s.lastListAccounts.accountType = accountType
	s.lastListAccounts.status = status
	s.lastListAccounts.search = search
	s.lastListAccounts.groupID = groupID
	s.lastListAccounts.privacyMode = privacyMode
	s.lastListAccounts.sortBy = sortBy
	s.lastListAccounts.sortOrder = sortOrder
	s.lastListAccounts.calls++
	accounts := s.accounts
	total := len(accounts)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = total
	}
	start := (page - 1) * pageSize
	if start >= total {
		return []account.Record{}, int64(total), nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return accounts[start:end], int64(total), nil
}

func (s *managementListFixture) ListAccountsForSchedulerScoreFilter(_ context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]account.Record, error) {
	s.schedulerScoreFilterCalls++
	if s.accountSchedulerScoreFilterAccounts != nil {
		return s.accountSchedulerScoreFilterAccounts, nil
	}
	return s.accounts, nil
}

func (s *managementListFixture) ListSchedulableAccountsForAdvancedSchedulerScore(_ context.Context, groupID *int64, platform string) ([]account.Record, error) {
	s.openAISchedulerScorePoolCalls++
	if groupID != nil {
		s.openAISchedulerScorePoolGroupIDs = append(s.openAISchedulerScorePoolGroupIDs, *groupID)
	}
	accounts := s.openAISchedulerScorePoolAccounts
	if accounts == nil {
		accounts = s.accounts
	}
	out := make([]account.Record, 0, len(accounts))
	for _, account := range accounts {
		if account.Platform != platform || !account.IsSchedulable() {
			continue
		}
		if groupID == nil {
			if len(account.AccountGroups) == 0 && len(account.GroupIDs) == 0 {
				out = append(out, account)
			}
			continue
		}
		for _, accountGroup := range account.AccountGroups {
			if accountGroup.GroupID == *groupID {
				out = append(out, account)
				break
			}
		}
	}
	return out, nil
}

// managementListFixture 只拥有列表与评分候选读模型，保留原分页和调用计数。
type managementListFixture struct {
	AccountManagement
	accounts                                                                []account.Record
	accountSchedulerScoreFilterAccounts                                     []account.Record
	openAISchedulerScorePoolAccounts                                        []account.Record
	schedulerScoreFilterCalls, openAISchedulerScorePoolCalls, getGroupCalls int
	openAISchedulerScorePoolGroupIDs                                        []int64
	lastListAccounts                                                        struct {
		platform, accountType, status, search, privacyMode, sortBy, sortOrder string
		groupID                                                               int64
		calls                                                                 int
	}
}

func newManagementListFixture() *managementListFixture {
	now := time.Now().UTC()
	return &managementListFixture{accounts: []account.Record{{ID: 3, Name: "account", Platform: account.PlatformAnthropic, Type: account.AccountTypeOAuth, Status: account.StatusActive, CreatedAt: now, UpdatedAt: now}}}
}

// newManagementListFixtureHandler 使用真实列表、评分和展示实现，静态缺省值保持原独立构造。
func newManagementListFixtureHandler(source *managementListFixture) *ManagementHandler {
	runtime := account.NewRuntimeStatusReader(account.RuntimeStatusOptions{})
	scores := account.NewSchedulerScoreView(source, provider.SchedulerScoreOptions(nil, nil, func(_ context.Context, group *accessview.GroupConfig) policy.EffectiveSettings {
		var overrides policy.GroupAdvancedSchedulerOverrides
		if group != nil && group.SchedulerType == "advanced" {
			overrides = group.AdvancedSchedulerOverrides
		}
		return policy.ResolveEffective(7, policy.ScoreWeights{Priority: 1, Load: 1, Queue: 0.7, ErrorRate: 0.8, TTFT: 0.5, Previous: 5, SessionSticky: 3}, policy.RuntimeSettings{}, overrides)
	}))
	presenter := NewRuntimePresenter(runtime, source, nil)
	return NewManagementHandler(source, ManagementOptions{List: account.NewManagementList(source, runtime, scores, nil), RuntimePresenter: presenter})
}
