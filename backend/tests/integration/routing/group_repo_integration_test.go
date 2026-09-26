//go:build integration

package routing_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/suite"
)

type GroupRepoSuite struct {
	suite.Suite
	client *dbent.Client
	ctx    context.Context
	tx     *dbent.Tx
	repo   *routingpostgres.GroupStore
}

type forbidSQLExecutor struct {
	called bool
}

func (s *forbidSQLExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	s.called = true
	return nil, errors.New("unexpected sql exec")
}

func (s *forbidSQLExecutor) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	s.called = true
	return nil, errors.New("unexpected sql query")
}

func (s *GroupRepoSuite) SetupTest() {
	s.ctx = context.Background()
	tx := s.transaction(s.T())
	s.tx = tx
	s.repo = newGroupStoreFixture(tx.Client(), tx)
}

func TestGroupRepoSuite(t *testing.T) {
	suite.Run(t, new(GroupRepoSuite))
}

// --- Create / GetByID / Update / Delete ---

func (s *GroupRepoSuite) TestCreate() {
	webSearchPrice := 0.008
	group := &routing.Group{
		Name:                  "test-create",
		Platform:              capability.PlatformOpenAI,
		RateMultiplier:        1.0,
		IsExclusive:           false,
		Status:                billing.StatusActive,
		WebSearchPricePerCall: &webSearchPrice,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformOpenAI),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformOpenAI),
		ResponsesImagePolicy: "inherit",
	}

	err := s.repo.Create(s.ctx, group)
	s.Require().NoError(err, "Create")
	s.Require().NotZero(group.ID, "expected ID to be set")

	got, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("test-create", got.Name)
	s.Require().NotNil(got.WebSearchPricePerCall)
	s.Require().InDelta(webSearchPrice, *got.WebSearchPricePerCall, 1e-12)
}

func (s *GroupRepoSuite) TestCreateFromSourcePreservesPriorityAndFiltersIneligibleAccounts() {
	source := &routing.Group{
		Name:             "duplicate-source",
		Platform:         capability.PlatformOpenAI,
		RateMultiplier:   1,
		Status:           billing.StatusActive,
		RequireOAuthOnly: true,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformOpenAI),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformOpenAI),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, source))

	insertAccount := func(name, accountType string, deleted bool) int64 {
		var id int64
		deletedAt := any(nil)
		if deleted {
			deletedAt = "2026-07-16T00:00:00Z"
		}
		s.Require().NoError(postgresinfra.ScanSingleRow(
			s.ctx,
			s.tx,
			"INSERT INTO accounts (name, platform, type, deleted_at) VALUES ($1, $2, $3, $4) RETURNING id",
			[]any{name, capability.PlatformOpenAI, accountType, deletedAt},
			&id,
		))
		return id
	}
	oauthID := insertAccount("duplicate-oauth", capability.AccountTypeOAuth, false)
	apiKeyID := insertAccount("duplicate-apikey", capability.AccountTypeAPIKey, false)
	deletedID := insertAccount("duplicate-deleted", capability.AccountTypeOAuth, true)
	for _, accountID := range []int64{oauthID, apiKeyID, deletedID} {
		_, err := s.tx.ExecContext(
			s.ctx,
			"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
			accountID,
			source.ID,
		)
		s.Require().NoError(err)
	}

	duplicate := &routing.Group{
		Name:                 "duplicate-source (Copy)",
		Platform:             source.Platform,
		RateMultiplier:       source.RateMultiplier,
		Status:               "inactive",
		RequireOAuthOnly:     true,
		DuplicateOperationID: strings.Repeat("a", 64),

		AllowedProtocols:     capability.DefaultGroupClientProtocols(source.Platform),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(source.Platform),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.CreateFromSource(s.ctx, duplicate, source.ID))
	s.Require().EqualValues(1, duplicate.AccountCount)

	rows, err := s.tx.QueryContext(
		s.ctx,
		"SELECT account_id FROM account_groups WHERE group_id = $1 ORDER BY account_id",
		duplicate.ID,
	)
	s.Require().NoError(err)
	defer func() { _ = rows.Close() }()
	s.Require().True(rows.Next())
	var copiedAccountID int64
	s.Require().NoError(rows.Scan(&copiedAccountID))
	s.Require().Equal(oauthID, copiedAccountID)
	s.Require().False(rows.Next(), "API-key and soft-deleted accounts must not be copied")

	recovered, err := s.repo.FindByDuplicateOperationID(s.ctx, duplicate.DuplicateOperationID)
	s.Require().NoError(err)
	s.Require().Equal(duplicate.ID, recovered.ID)

	var outboxCount int
	s.Require().NoError(postgresinfra.ScanSingleRow(
		s.ctx,
		s.tx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE group_id = $1",
		[]any{duplicate.ID},
		&outboxCount,
	))
	s.Require().Equal(1, outboxCount)
}

func (s *GroupRepoSuite) TestGetByID_NotFound() {
	_, err := s.repo.GetByID(s.ctx, 999999)
	s.Require().Error(err, "expected error for non-existent ID")
	s.Require().ErrorIs(err, routing.ErrGroupNotFound)
}

func (s *GroupRepoSuite) TestGetByIDLite_DoesNotUseAccountCount() {
	group := &routing.Group{
		Name:           "lite-group",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	spy := &forbidSQLExecutor{}
	repo := newGroupStoreFixture(s.tx.Client(), spy)

	got, err := repo.GetByIDLite(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(group.ID, got.ID)
	s.Require().False(spy.called, "expected no direct sql executor usage")
}

func (s *GroupRepoSuite) TestUpdate() {
	group := &routing.Group{
		Name:           "original",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	group.Name = "updated"
	err := s.repo.Update(s.ctx, group)
	s.Require().NoError(err, "Update")

	got, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("updated", got.Name)
}

func (s *GroupRepoSuite) TestGetByID_PreservesMessagesDispatchModelConfig() {
	group := &routing.Group{
		Name:           "openai-dispatch",
		Platform:       capability.PlatformOpenAI,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,
		AllowedProtocols: []protocol.ProtocolID{
			protocol.ProtocolAnthropicMessages,
			protocol.ProtocolOpenAIResponses,
			protocol.ProtocolOpenAIChatCompletions,
		},
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.4",
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
			OpusMappedModel:   "gpt-5.4",
			SonnetMappedModel: "gpt-5.3-codex",
			HaikuMappedModel:  "gpt-5.4-mini",
			ExactModelMappings: map[string]string{
				"claude-sonnet-4.5": "gpt-5.4-nano",
			},
		},

		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformOpenAI),
		ResponsesImagePolicy: "inherit",
	}

	s.Require().NoError(s.repo.Create(s.ctx, group))

	got, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(group.AllowedProtocols, got.AllowedProtocols)
	s.Require().Equal(group.MessagesDispatchModelConfig, got.MessagesDispatchModelConfig)
}

func (s *GroupRepoSuite) TestDelete() {
	group := &routing.Group{
		Name:           "to-delete",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	err := s.repo.Delete(s.ctx, group.ID)
	s.Require().NoError(err, "Delete")

	_, err = s.repo.GetByID(s.ctx, group.ID)
	s.Require().Error(err, "expected error after delete")
	s.Require().ErrorIs(err, routing.ErrGroupNotFound)
}

// --- List / ListWithFilters ---

func (s *GroupRepoSuite) TestList() {
	baseGroups, basePage, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "List base")

	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g2",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))

	groups, page, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "List")
	s.Require().Len(groups, len(baseGroups)+2)
	s.Require().Equal(basePage.Total+2, page.Total)
}

func (s *GroupRepoSuite) TestListWithFilters_Platform() {
	baseGroups, _, err := s.repo.ListWithFilters(
		s.ctx,
		pagination.PaginationParams{Page: 1, PageSize: 10},
		capability.PlatformOpenAI,
		"",
		"",
		nil,
	)
	s.Require().NoError(err, "ListWithFilters base")

	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g2",
		Platform:       capability.PlatformOpenAI,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformOpenAI),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformOpenAI),
		ResponsesImagePolicy: "inherit",
	}))

	groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, capability.PlatformOpenAI, "", "", nil)
	s.Require().NoError(err)
	s.Require().Len(groups, len(baseGroups)+1)
	// Verify all groups are OpenAI platform
	for _, g := range groups {
		s.Require().Equal(capability.PlatformOpenAI, g.Platform)
	}
}

func (s *GroupRepoSuite) TestListWithFilters_Status() {
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g2",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusDisabled,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))

	groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", billing.StatusDisabled, "", nil)
	s.Require().NoError(err)
	s.Require().Len(groups, 1)
	s.Require().Equal(billing.StatusDisabled, groups[0].Status)
}

func (s *GroupRepoSuite) TestListWithFilters_IsExclusive() {
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g2",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    true,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))

	isExclusive := true
	groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", "", &isExclusive)
	s.Require().NoError(err)
	s.Require().Len(groups, 1)
	s.Require().True(groups[0].IsExclusive)
}

func (s *GroupRepoSuite) TestListWithFilters_Search() {
	newRepo := func() (*routingpostgres.GroupStore, context.Context) {
		tx := s.transaction(s.T())
		return newGroupStoreFixture(tx.Client(), tx), context.Background()
	}

	containsID := func(groups []routing.Group, id int64) bool {
		for i := range groups {
			if groups[i].ID == id {
				return true
			}
		}
		return false
	}

	mustCreate := func(repo *routingpostgres.GroupStore, ctx context.Context, g *routing.Group) *routing.Group {
		s.Require().NoError(repo.Create(ctx, g))
		s.Require().NotZero(g.ID)
		return g
	}

	newGroup := func(name string) *routing.Group {
		return &routing.Group{
			Name:           name,
			Platform:       capability.PlatformAnthropic,
			RateMultiplier: 1.0,
			IsExclusive:    false,
			Status:         billing.StatusActive,

			AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
			ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
			ResponsesImagePolicy: "inherit",
		}
	}

	s.Run("search_name_should_match", func() {
		repo, ctx := newRepo()

		target := mustCreate(repo, ctx, newGroup("it-group-search-name-target"))
		other := mustCreate(repo, ctx, newGroup("it-group-search-name-other"))

		groups, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 50}, "", "", "name-target", nil)
		s.Require().NoError(err)
		s.Require().True(containsID(groups, target.ID), "expected target group to match by name")
		s.Require().False(containsID(groups, other.ID), "expected other group to be filtered out")
	})

	s.Run("search_description_should_match", func() {
		repo, ctx := newRepo()

		target := newGroup("it-group-search-desc-target")
		target.Description = "something about desc-needle in here"
		target = mustCreate(repo, ctx, target)

		other := newGroup("it-group-search-desc-other")
		other.Description = "nothing to see here"
		other = mustCreate(repo, ctx, other)

		groups, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 50}, "", "", "desc-needle", nil)
		s.Require().NoError(err)
		s.Require().True(containsID(groups, target.ID), "expected target group to match by description")
		s.Require().False(containsID(groups, other.ID), "expected other group to be filtered out")
	})

	s.Run("search_nonexistent_should_return_empty", func() {
		repo, ctx := newRepo()

		_ = mustCreate(repo, ctx, newGroup("it-group-search-nonexistent-baseline"))

		search := s.T().Name() + "__no_such_group__"
		groups, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 50}, "", "", search, nil)
		s.Require().NoError(err)
		s.Require().Empty(groups)
	})

	s.Run("search_should_be_case_insensitive", func() {
		repo, ctx := newRepo()

		target := mustCreate(repo, ctx, newGroup("MiXeDCaSe-Needle"))
		other := mustCreate(repo, ctx, newGroup("it-group-search-case-other"))

		groups, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 50}, "", "", "mixedcase-needle", nil)
		s.Require().NoError(err)
		s.Require().True(containsID(groups, target.ID), "expected case-insensitive match")
		s.Require().False(containsID(groups, other.ID), "expected other group to be filtered out")
	})

	s.Run("search_should_escape_like_wildcards", func() {
		repo, ctx := newRepo()

		percentTarget := mustCreate(repo, ctx, newGroup("it-group-search-100%-target"))
		percentOther := mustCreate(repo, ctx, newGroup("it-group-search-100X-other"))

		groups, _, err := repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 50}, "", "", "100%", nil)
		s.Require().NoError(err)
		s.Require().True(containsID(groups, percentTarget.ID), "expected literal %% match")
		s.Require().False(containsID(groups, percentOther.ID), "expected %% not to act as wildcard")

		underscoreTarget := mustCreate(repo, ctx, newGroup("it-group-search-ab_cd-target"))
		underscoreOther := mustCreate(repo, ctx, newGroup("it-group-search-abXcd-other"))

		groups, _, err = repo.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 50}, "", "", "ab_cd", nil)
		s.Require().NoError(err)
		s.Require().True(containsID(groups, underscoreTarget.ID), "expected literal _ match")
		s.Require().False(containsID(groups, underscoreOther.ID), "expected _ not to act as wildcard")
	})
}

func (s *GroupRepoSuite) TestUpdateSortOrders_BatchCaseWhen() {
	g1 := &routing.Group{
		Name:           "sort-g1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	g2 := &routing.Group{
		Name:           "sort-g2",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	g3 := &routing.Group{
		Name:           "sort-g3",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g1))
	s.Require().NoError(s.repo.Create(s.ctx, g2))
	s.Require().NoError(s.repo.Create(s.ctx, g3))

	err := s.repo.UpdateSortOrders(s.ctx, []routing.GroupSortOrderUpdate{
		{ID: g1.ID, SortOrder: 30},
		{ID: g2.ID, SortOrder: 10},
		{ID: g3.ID, SortOrder: 20},
		{ID: g2.ID, SortOrder: 15}, // 重复 ID 应以最后一次为准
	})
	s.Require().NoError(err)

	got1, err := s.repo.GetByID(s.ctx, g1.ID)
	s.Require().NoError(err)
	got2, err := s.repo.GetByID(s.ctx, g2.ID)
	s.Require().NoError(err)
	got3, err := s.repo.GetByID(s.ctx, g3.ID)
	s.Require().NoError(err)
	s.Require().Equal(30, got1.SortOrder)
	s.Require().Equal(15, got2.SortOrder)
	s.Require().Equal(20, got3.SortOrder)
}

func (s *GroupRepoSuite) TestUpdateSortOrders_MissingGroupNoPartialUpdate() {
	g1 := &routing.Group{
		Name:           "sort-no-partial",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g1))

	before, err := s.repo.GetByID(s.ctx, g1.ID)
	s.Require().NoError(err)
	beforeSort := before.SortOrder

	err = s.repo.UpdateSortOrders(s.ctx, []routing.GroupSortOrderUpdate{
		{ID: g1.ID, SortOrder: 99},
		{ID: 99999999, SortOrder: 1},
	})
	s.Require().Error(err)
	s.Require().ErrorIs(err, routing.ErrGroupNotFound)

	after, err := s.repo.GetByID(s.ctx, g1.ID)
	s.Require().NoError(err)
	s.Require().Equal(beforeSort, after.SortOrder)
}

func (s *GroupRepoSuite) TestListWithFilters_AccountCount() {
	g1 := &routing.Group{
		Name:           "g1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	g2 := &routing.Group{
		Name:           "g2",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    true,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g1))
	s.Require().NoError(s.repo.Create(s.ctx, g2))

	var accountID int64
	s.Require().NoError(postgresinfra.ScanSingleRow(
		s.ctx,
		s.tx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		[]any{"acc1", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&accountID,
	))
	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", accountID, g1.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", accountID, g2.ID)
	s.Require().NoError(err)

	isExclusive := true
	groups, page, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, capability.PlatformAnthropic, billing.StatusActive, "", &isExclusive)
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total)
	s.Require().Len(groups, 1)
	s.Require().Equal(g2.ID, groups[0].ID, "ListWithFilters returned wrong group")
	s.Require().Equal(int64(1), groups[0].AccountCount, "AccountCount mismatch")
}

// --- ListActive / ListActiveByPlatform ---

func (s *GroupRepoSuite) TestListActive() {
	baseGroups, err := s.repo.ListActive(s.ctx)
	s.Require().NoError(err, "ListActive base")

	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "active1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "inactive1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusDisabled,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))

	groups, err := s.repo.ListActive(s.ctx)
	s.Require().NoError(err, "ListActive")
	s.Require().Len(groups, len(baseGroups)+1)
	// Verify our test group is in the results
	var found bool
	for _, g := range groups {
		if g.Name == "active1" {
			found = true
			break
		}
	}
	s.Require().True(found, "active1 group should be in results")
}

func (s *GroupRepoSuite) TestListActiveByPlatform() {
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g1",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g2",
		Platform:       capability.PlatformOpenAI,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformOpenAI),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformOpenAI),
		ResponsesImagePolicy: "inherit",
	}))
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "g3",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusDisabled,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))

	groups, err := s.repo.ListActiveByPlatform(s.ctx, capability.PlatformAnthropic)
	s.Require().NoError(err, "ListActiveByPlatform")
	// 1 default anthropic group + 1 test active anthropic group = 2 total
	s.Require().Len(groups, 2)
	// Verify our test group is in the results
	var found bool
	for _, g := range groups {
		if g.Name == "g1" {
			found = true
			break
		}
	}
	s.Require().True(found, "g1 group should be in results")
}

// --- ExistsByName ---

func (s *GroupRepoSuite) TestExistsByName() {
	s.Require().NoError(s.repo.Create(s.ctx, &routing.Group{
		Name:           "existing-group",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}))

	exists, err := s.repo.ExistsByName(s.ctx, "existing-group")
	s.Require().NoError(err, "ExistsByName")
	s.Require().True(exists)

	notExists, err := s.repo.ExistsByName(s.ctx, "non-existing")
	s.Require().NoError(err)
	s.Require().False(notExists)
}

// --- GetAccountCount ---

func (s *GroupRepoSuite) TestGetAccountCount() {
	group := &routing.Group{
		Name:           "g-count",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	var a1 int64
	s.Require().NoError(postgresinfra.ScanSingleRow(
		s.ctx,
		s.tx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		[]any{"a1", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&a1,
	))
	var a2 int64
	s.Require().NoError(postgresinfra.ScanSingleRow(
		s.ctx,
		s.tx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		[]any{"a2", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&a2,
	))

	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", a1, group.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", a2, group.ID)
	s.Require().NoError(err)

	count, _, err := s.repo.GetAccountCount(s.ctx, group.ID)
	s.Require().NoError(err, "GetAccountCount")
	s.Require().Equal(int64(2), count)
}

func (s *GroupRepoSuite) TestGetAccountCount_Empty() {
	group := &routing.Group{
		Name:           "g-empty",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	count, _, err := s.repo.GetAccountCount(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Zero(count)
}

// TestListWithFilters_ActiveAccountCount_LessThanTotal 验证 ActiveAccountCount 正确区分可用与不可用账号。
// 当分组内存在 disabled 或 schedulable=false 的账号时，ActiveAccountCount 必须小于 AccountCount，
// 且与 GetAccountCount 返回的 active 值一致。
func (s *GroupRepoSuite) TestListWithFilters_ActiveAccountCount_LessThanTotal() {
	g := &routing.Group{
		Name:           "g-mixed-status",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g))

	insertAccount := func(name, status string, schedulable bool) int64 {
		var id int64
		s.Require().NoError(postgresinfra.ScanSingleRow(
			s.ctx, s.tx,
			"INSERT INTO accounts (name, platform, type, status, schedulable) VALUES ($1, $2, $3, $4, $5) RETURNING id",
			[]any{name, capability.PlatformAnthropic, capability.AccountTypeOAuth, status, schedulable},
			&id,
		))
		return id
	}
	link := func(accountID int64) {
		_, err := s.tx.ExecContext(s.ctx,
			"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
			accountID, g.ID)
		s.Require().NoError(err)
	}

	// 账号 1：active + schedulable，同时计入 total 与 active。
	link(insertAccount("acc-active-sched", billing.StatusActive, true))
	// 账号 2：disabled，仅计入 total。
	link(insertAccount("acc-disabled", billing.StatusDisabled, true))
	// 账号 3：active 但不可调度，仅计入 total。
	link(insertAccount("acc-unschedulable", billing.StatusActive, false))

	// --- ListWithFilters 路径 ---
	isExclusive := false
	groups, _, err := s.repo.ListWithFilters(s.ctx,
		pagination.PaginationParams{Page: 1, PageSize: 100},
		capability.PlatformAnthropic, billing.StatusActive, "", &isExclusive)
	s.Require().NoError(err)

	var found *routing.Group
	for i := range groups {
		if groups[i].ID == g.ID {
			found = &groups[i]
			break
		}
	}
	s.Require().NotNil(found, "created group must appear in ListWithFilters result")
	s.Assert().Equal(int64(3), found.AccountCount, "AccountCount must count all 3 accounts")
	s.Assert().Equal(int64(1), found.ActiveAccountCount, "ActiveAccountCount must count only the active+schedulable account")

	// --- GetAccountCount 必须返回相同统计口径 ---
	total, active, err := s.repo.GetAccountCount(s.ctx, g.ID)
	s.Require().NoError(err)
	s.Assert().Equal(found.AccountCount, total, "GetAccountCount total must match ListWithFilters AccountCount")
	s.Assert().Equal(found.ActiveAccountCount, active, "GetAccountCount active must match ListWithFilters ActiveAccountCount")
}

// TestListWithFilters_RateLimitedAccountCount 验证临时受限账号不会计入可用账号数。
// rate_limit / overload / temp_unschedulable 都会让账号退出当前调度池，
// 因此 ActiveAccountCount 必须与真实调度查询口径一致。
func (s *GroupRepoSuite) TestListWithFilters_RateLimitedAccountCount() {
	g := &routing.Group{
		Name:           "g-rate-limited",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g))

	var normalID int64
	s.Require().NoError(postgresinfra.ScanSingleRow(s.ctx, s.tx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		[]any{"acc-normal", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&normalID))

	var rateLimitedID int64
	s.Require().NoError(postgresinfra.ScanSingleRow(s.ctx, s.tx,
		"INSERT INTO accounts (name, platform, type, rate_limit_reset_at) VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour') RETURNING id",
		[]any{"acc-rate-limited", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&rateLimitedID))

	var overloadedID int64
	s.Require().NoError(postgresinfra.ScanSingleRow(s.ctx, s.tx,
		"INSERT INTO accounts (name, platform, type, overload_until) VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour') RETURNING id",
		[]any{"acc-overloaded", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&overloadedID))

	var tempUnschedulableID int64
	s.Require().NoError(postgresinfra.ScanSingleRow(s.ctx, s.tx,
		"INSERT INTO accounts (name, platform, type, temp_unschedulable_until) VALUES ($1, $2, $3, NOW() + INTERVAL '1 hour') RETURNING id",
		[]any{"acc-temp-unschedulable", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&tempUnschedulableID))

	var expiredID int64
	s.Require().NoError(postgresinfra.ScanSingleRow(s.ctx, s.tx,
		"INSERT INTO accounts (name, platform, type, expires_at, auto_pause_on_expired) VALUES ($1, $2, $3, NOW() - INTERVAL '1 hour', TRUE) RETURNING id",
		[]any{"acc-expired", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&expiredID))

	_, err := s.tx.ExecContext(s.ctx,
		"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
		normalID, g.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx,
		"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
		rateLimitedID, g.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx,
		"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
		overloadedID, g.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx,
		"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
		tempUnschedulableID, g.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx,
		"INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())",
		expiredID, g.ID)
	s.Require().NoError(err)

	isExclusive := false
	groups, _, err := s.repo.ListWithFilters(s.ctx,
		pagination.PaginationParams{Page: 1, PageSize: 100},
		capability.PlatformAnthropic, billing.StatusActive, "", &isExclusive)
	s.Require().NoError(err)

	var found *routing.Group
	for i := range groups {
		if groups[i].ID == g.ID {
			found = &groups[i]
			break
		}
	}
	s.Require().NotNil(found, "created group must appear in ListWithFilters result")
	s.Assert().Equal(int64(5), found.AccountCount, "AccountCount must include all linked accounts")
	s.Assert().Equal(int64(1), found.ActiveAccountCount, "ActiveAccountCount must include only currently schedulable accounts")
	s.Assert().Equal(int64(3), found.RateLimitedAccountCount, "RateLimitedAccountCount must include temporarily limited accounts")

	total, active, err := s.repo.GetAccountCount(s.ctx, g.ID)
	s.Require().NoError(err)
	s.Assert().Equal(found.AccountCount, total, "GetAccountCount total must match ListWithFilters AccountCount")
	s.Assert().Equal(found.ActiveAccountCount, active, "GetAccountCount active must match ListWithFilters ActiveAccountCount")

	detail, err := s.repo.GetByID(s.ctx, g.ID)
	s.Require().NoError(err)
	s.Assert().Equal(found.AccountCount, detail.AccountCount, "GetByID AccountCount must match ListWithFilters")
	s.Assert().Equal(found.ActiveAccountCount, detail.ActiveAccountCount, "GetByID ActiveAccountCount must match ListWithFilters")
	s.Assert().Equal(found.RateLimitedAccountCount, detail.RateLimitedAccountCount, "GetByID RateLimitedAccountCount must match ListWithFilters")
}

// --- DeleteAccountGroupsByGroupID ---

func (s *GroupRepoSuite) TestDeleteAccountGroupsByGroupID() {
	g := &routing.Group{
		Name:           "g-del",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g))
	var accountID int64
	s.Require().NoError(postgresinfra.ScanSingleRow(
		s.ctx,
		s.tx,
		"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
		[]any{"acc-del", capability.PlatformAnthropic, capability.AccountTypeOAuth},
		&accountID,
	))
	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", accountID, g.ID)
	s.Require().NoError(err)

	affected, err := s.repo.DeleteAccountGroupsByGroupID(s.ctx, g.ID)
	s.Require().NoError(err, "DeleteAccountGroupsByGroupID")
	s.Require().Equal(int64(1), affected, "expected 1 affected row")

	count, _, err := s.repo.GetAccountCount(s.ctx, g.ID)
	s.Require().NoError(err, "GetAccountCount")
	s.Require().Equal(int64(0), count, "expected 0 account groups")
}

func (s *GroupRepoSuite) TestDeleteAccountGroupsByGroupID_MultipleAccounts() {
	g := &routing.Group{
		Name:           "g-multi",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, g))

	insertAccount := func(name string) int64 {
		var id int64
		s.Require().NoError(postgresinfra.ScanSingleRow(
			s.ctx,
			s.tx,
			"INSERT INTO accounts (name, platform, type) VALUES ($1, $2, $3) RETURNING id",
			[]any{name, capability.PlatformAnthropic, capability.AccountTypeOAuth},
			&id,
		))
		return id
	}
	a1 := insertAccount("a1")
	a2 := insertAccount("a2")
	a3 := insertAccount("a3")
	_, err := s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", a1, g.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", a2, g.ID)
	s.Require().NoError(err)
	_, err = s.tx.ExecContext(s.ctx, "INSERT INTO account_groups (account_id, group_id, created_at) VALUES ($1, $2, NOW())", a3, g.ID)
	s.Require().NoError(err)

	affected, err := s.repo.DeleteAccountGroupsByGroupID(s.ctx, g.ID)
	s.Require().NoError(err)
	s.Require().Equal(int64(3), affected)

	count, _, _ := s.repo.GetAccountCount(s.ctx, g.ID)
	s.Require().Zero(count)
}

// --- 软删除过滤测试 ---

func (s *GroupRepoSuite) TestDelete_SoftDelete_NotVisibleInList() {
	group := &routing.Group{
		Name:           "to-soft-delete",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	// 获取删除前的列表数量
	listBefore, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 100})
	s.Require().NoError(err)
	beforeCount := len(listBefore)

	// 软删除
	err = s.repo.Delete(s.ctx, group.ID)
	s.Require().NoError(err, "Delete (soft delete)")

	// 验证列表中不再包含软删除的 group
	listAfter, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 100})
	s.Require().NoError(err)
	s.Require().Len(listAfter, beforeCount-1, "soft deleted group should not appear in list")

	// 验证 GetByID 也无法找到
	_, err = s.repo.GetByID(s.ctx, group.ID)
	s.Require().Error(err)
	s.Require().ErrorIs(err, routing.ErrGroupNotFound)
}

func (s *GroupRepoSuite) TestDelete_SoftDeletedGroup_lockForUpdate() {
	group := &routing.Group{
		Name:           "lock-soft-delete",
		Platform:       capability.PlatformAnthropic,
		RateMultiplier: 1.0,
		IsExclusive:    false,
		Status:         billing.StatusActive,

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformAnthropic),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformAnthropic),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))

	// 软删除
	err := s.repo.Delete(s.ctx, group.ID)
	s.Require().NoError(err)

	// 验证软删除的 group 在 GetByID 时返回 ErrGroupNotFound
	// 这证明 lockForUpdate 的 deleted_at IS NULL 过滤正在工作
	_, err = s.repo.GetByID(s.ctx, group.ID)
	s.Require().Error(err, "should fail to get soft-deleted group")
	s.Require().ErrorIs(err, routing.ErrGroupNotFound)
}

// TestModelPricingRoundTrip 验证分组完整价卡通过 JSONB 创建、更新和清空，不需要新增表列。
func (s *GroupRepoSuite) TestModelPricingRoundTrip() {
	fast, flex, max, price, outputMultiplier := 1.5, 0.4, 2.0, 0.0, 3.0
	group := &routing.Group{
		Name: "pricing-roundtrip", Platform: capability.PlatformOpenAI, RateMultiplier: 1,
		Status: billing.StatusActive, LongContextPricingEnabled: true, FreeOpenAIFast: true,
		ModelPricing: []routing.ModelPricingEntry{{
			Platform: capability.PlatformOpenAI, Models: []string{"gpt-test"}, BillingMode: routing.BillingModeToken,
			InputPrice: &price, FastMultiplier: &fast, FlexMultiplier: &flex, MaxReasoningEffortMultiplier: &max,
			Intervals: []routing.PricingInterval{{MinTokens: 100, OutputMultiplier: &outputMultiplier}},
			TimePricing: &routing.TimePricingConfig{
				Timezone: "Asia/Tokyo", WeekdaysOnly: true,
				Periods: []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "10:00", Multiplier: 0.5}},
			},
		}},

		AllowedProtocols:     capability.DefaultGroupClientProtocols(capability.PlatformOpenAI),
		ProtocolFallbacks:    capability.DefaultProtocolFallbacks(capability.PlatformOpenAI),
		ResponsesImagePolicy: "inherit",
	}
	s.Require().NoError(s.repo.Create(s.ctx, group))
	got, err := s.repo.GetByID(s.ctx, group.ID)
	s.Require().NoError(err)
	s.Require().Equal(group.ModelPricing, got.ModelPricing)
	s.Require().True(got.LongContextPricingEnabled)
	s.Require().True(got.FreeOpenAIFast)
	got.ModelPricing[0].TimePricing.Periods[0].Multiplier = 0.25
	*got.ModelPricing[0].FastMultiplier = 1
	s.Require().NoError(s.repo.Update(s.ctx, got))
	updated, err := s.repo.GetByID(s.ctx, got.ID)
	s.Require().NoError(err)
	s.Require().Equal(got.ModelPricing, updated.ModelPricing)
	updated.ModelPricing = []routing.ModelPricingEntry{}
	s.Require().NoError(s.repo.Update(s.ctx, updated))
	empty, err := s.repo.GetByID(s.ctx, updated.ID)
	s.Require().NoError(err)
	s.Require().Empty(empty.ModelPricing)
}

// SetupSuite 保留同套件共享库、各测试独立事务的原隔离边界。
func (s *GroupRepoSuite) SetupSuite() { s.client, _ = routingDatabase(s.T()) }

func (s *GroupRepoSuite) transaction(t *testing.T) *dbent.Tx {
	t.Helper()
	tx, err := s.client.Tx(context.Background())
	s.Require().NoError(err)
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}
