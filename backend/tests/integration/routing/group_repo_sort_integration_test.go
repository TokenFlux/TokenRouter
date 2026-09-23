//go:build integration

package routing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestListWithAccountCountSort_AttachesActiveCount 验证通过 account_count 排序时，
// ActiveAccountCount 与 AccountCount 都被正确附加到返回结果中，
// 且排序基于 total 账号数而非 active 账号数。
func (s *GroupRepoSuite) TestListWithAccountCountSort_AttachesActiveCount() {
	// 分组 A：total=2，active=1（包含 1 个 disabled 账号）。
	gA := &routing.Group{Name: "sort-count-a", Platform: capability.PlatformAnthropic, RateMultiplier: 1, Status: billing.StatusActive,
		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	// 分组 B：total=1，active=1。
	gB := &routing.Group{Name: "sort-count-b", Platform: capability.PlatformAnthropic, RateMultiplier: 1, Status: billing.StatusActive,
		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, gA))
	s.Require().NoError(s.repo.Create(s.ctx, gB))

	insertAccount := func(name, status string) int64 {
		var id int64
		s.Require().NoError(postgresinfra.ScanSingleRow(s.ctx, s.tx,
			"INSERT INTO accounts (name, platform, type, status) VALUES ($1, $2, $3, $4) RETURNING id",
			[]any{name, capability.PlatformAnthropic, capability.AccountTypeOAuth, status},
			&id))
		return id
	}
	link := func(accountID, groupID int64) {
		_, err := s.tx.ExecContext(s.ctx,
			"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
			accountID, groupID)
		s.Require().NoError(err)
	}

	// gA：1 active + 1 disabled，因此 total=2，active=1。
	link(insertAccount("sa-active", billing.StatusActive), gA.ID)
	link(insertAccount("sa-disabled", billing.StatusDisabled), gA.ID)
	// gB：1 active，因此 total=1，active=1。
	link(insertAccount("sb-active", billing.StatusActive), gB.ID)

	groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page: 1, PageSize: 100, SortBy: "account_count", SortOrder: "desc",
	}, capability.PlatformAnthropic, billing.StatusActive, "", nil)
	s.Require().NoError(err)

	byID := make(map[int64]routing.Group, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}

	s.Require().Contains(byID, gA.ID, "gA must appear in results")
	s.Require().Contains(byID, gB.ID, "gB must appear in results")

	cA := byID[gA.ID]
	s.Assert().Equal(int64(2), cA.AccountCount, "gA AccountCount must be 2")
	s.Assert().Equal(int64(1), cA.ActiveAccountCount, "gA ActiveAccountCount must be 1")

	cB := byID[gB.ID]
	s.Assert().Equal(int64(1), cB.AccountCount, "gB AccountCount must be 1")
	s.Assert().Equal(int64(1), cB.ActiveAccountCount, "gB ActiveAccountCount must be 1")

	// 排序按 total 而不是 active：desc 下 gA(total=2) 必须排在 gB(total=1) 前面。
	indexByID := make(map[int64]int, len(groups))
	for i, g := range groups {
		indexByID[g.ID] = i
	}
	s.Assert().Less(indexByID[gA.ID], indexByID[gB.ID], "gA (total=2) must rank above gB (total=1) with account_count desc")
}

func (s *GroupRepoSuite) TestList_DefaultSortBySortOrderAsc() {
	g1 := &routing.Group{Name: "g1", Platform: capability.PlatformAnthropic, RateMultiplier: 1, Status: billing.StatusActive, SortOrder: 20,
		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	g2 := &routing.Group{Name: "g2", Platform: capability.PlatformAnthropic, RateMultiplier: 1, Status: billing.StatusActive, SortOrder: 10,
		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g1))
	s.Require().NoError(s.repo.Create(s.ctx, g2))

	groups, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 100})
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(len(groups), 2)
	indexByID := make(map[int64]int, len(groups))
	for i, g := range groups {
		indexByID[g.ID] = i
	}
	s.Require().Contains(indexByID, g1.ID)
	s.Require().Contains(indexByID, g2.ID)
	// g2 has SortOrder=10, g1 has SortOrder=20; ascending means g2 comes first
	s.Require().Less(indexByID[g2.ID], indexByID[g1.ID])
}

func (s *GroupRepoSuite) TestList_SortBySortOrderDesc() {
	g1 := &routing.Group{Name: "g1", Platform: capability.PlatformAnthropic, RateMultiplier: 1, Status: billing.StatusActive, SortOrder: 40,
		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	g2 := &routing.Group{Name: "g2", Platform: capability.PlatformAnthropic, RateMultiplier: 1, Status: billing.StatusActive, SortOrder: 50,
		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g1))
	s.Require().NoError(s.repo.Create(s.ctx, g2))

	groups, _, err := s.repo.List(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "sort_order",
		SortOrder: "desc",
	})
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(len(groups), 2)
	indexByID := make(map[int64]int, len(groups))
	for i, group := range groups {
		indexByID[group.ID] = i
	}
	s.Require().Contains(indexByID, g1.ID)
	s.Require().Contains(indexByID, g2.ID)
	s.Require().Less(indexByID[g2.ID], indexByID[g1.ID])
}
