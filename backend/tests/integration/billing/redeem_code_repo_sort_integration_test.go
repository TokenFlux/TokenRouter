//go:build integration

package billing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

func (s *RedeemCodeRepoSuite) TestListWithFilters_SortByValueAsc() {
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "VALUE-20", Type: billing.RedeemTypeBalance, Value: 20, Status: billing.StatusUnused}))
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "VALUE-10", Type: billing.RedeemTypeBalance, Value: 10, Status: billing.StatusUnused}))

	codes, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "value",
		SortOrder: "asc",
	}, "", "", "")
	s.Require().NoError(err)
	s.Require().Len(codes, 2)
	s.Require().Equal("VALUE-10", codes[0].Code)
	s.Require().Equal("VALUE-20", codes[1].Code)
}
