//go:build unit

package billing_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type redeemRepoStubForAdminList struct {
	billing.RedeemCodeRepository

	listWithFiltersCalls  int
	listWithFiltersParams pagination.PaginationParams
	listWithFiltersType   string
	listWithFiltersStatus string
	listWithFiltersSearch string
	listWithFiltersCodes  []billing.RedeemCode
	listWithFiltersResult *pagination.PaginationResult
	listWithFiltersErr    error
}

func (s *redeemRepoStubForAdminList) ListWithFilters(_ context.Context, params pagination.PaginationParams, codeType, status, search string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	s.listWithFiltersCalls++
	s.listWithFiltersParams = params
	s.listWithFiltersType = codeType
	s.listWithFiltersStatus = status
	s.listWithFiltersSearch = search

	if s.listWithFiltersErr != nil {
		return nil, nil, s.listWithFiltersErr
	}

	result := s.listWithFiltersResult
	if result == nil {
		result = &pagination.PaginationResult{
			Total:    int64(len(s.listWithFiltersCodes)),
			Page:     params.Page,
			PageSize: params.PageSize,
		}
	}

	return s.listWithFiltersCodes, result, nil
}

func (s *redeemRepoStubForAdminList) ListByUserPaginated(_ context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (s *redeemRepoStubForAdminList) SumPositiveBalanceByUser(_ context.Context, userID int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

func TestAdminService_ListRedeemCodes_WithSearch(t *testing.T) {
	t.Run("search 参数正常传递到 repository 层", func(t *testing.T) {
		repo := &redeemRepoStubForAdminList{
			listWithFiltersCodes:  []billing.RedeemCode{{ID: 4, Code: "ABC"}},
			listWithFiltersResult: &pagination.PaginationResult{Total: 3},
		}
		svc := billing.NewRedeemAdmin(repo, nil, nil)

		codes, total, err := svc.ListRedeemCodes(context.Background(), 1, 20, billing.RedeemTypeBalance, billing.StatusUnused, "ABC", "value", "ASC")
		require.NoError(t, err)
		require.Equal(t, int64(3), total)
		require.Equal(t, []billing.RedeemCode{{ID: 4, Code: "ABC"}}, codes)

		require.Equal(t, 1, repo.listWithFiltersCalls)
		require.Equal(t, pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "value", SortOrder: "ASC"}, repo.listWithFiltersParams)
		require.Equal(t, billing.RedeemTypeBalance, repo.listWithFiltersType)
		require.Equal(t, billing.StatusUnused, repo.listWithFiltersStatus)
		require.Equal(t, "ABC", repo.listWithFiltersSearch)
	})
}
