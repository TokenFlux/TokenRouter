//go:build integration

package postgres

import (
	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/google/uuid"
)

func (s *UsageLogRepoSuite) TestListWithFilters_SortByModelAsc() {
	user := mustCreateUser(s.T(), s.client, &identity.User{Email: "usage-sort@example.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &apikey.APIKey{UserID: user.ID, Key: "sk-usage-sort", Name: "k"})
	account := mustCreateAccount(s.T(), s.client, &accountcore.Record{Name: "usage-sort-account"})

	first := &usage.UsageLog{
		UserID:         user.ID,
		APIKeyID:       apiKey.ID,
		AccountID:      account.ID,
		RequestID:      uuid.New().String(),
		Model:          "z-model",
		RequestedModel: "z-model",
		InputTokens:    10,
		OutputTokens:   20,
		TotalCost:      0.5,
		ActualCost:     0.5,
		CreatedAt:      time.Now(),
	}
	_, err := s.repo.Create(s.ctx, first)
	s.Require().NoError(err)

	second := &usage.UsageLog{
		UserID:         user.ID,
		APIKeyID:       apiKey.ID,
		AccountID:      account.ID,
		RequestID:      uuid.New().String(),
		Model:          "a-model",
		RequestedModel: "a-model",
		InputTokens:    10,
		OutputTokens:   20,
		TotalCost:      0.5,
		ActualCost:     0.5,
		CreatedAt:      time.Now().Add(time.Second),
	}
	_, err = s.repo.Create(s.ctx, second)
	s.Require().NoError(err)

	logs, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "model",
		SortOrder: "asc",
	}, usage.UsageLogFilters{UserID: user.ID})
	s.Require().NoError(err)
	s.Require().Len(logs, 2)
	s.Require().Equal("a-model", logs[0].RequestedModel)
	s.Require().Equal("z-model", logs[1].RequestedModel)
}
