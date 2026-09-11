package repository

import "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

func paginationResultFromTotal(total int64, params pagination.PaginationParams) *pagination.PaginationResult {
	return pagination.ResultFromTotal(total, params)
}

func paginateSlice[T any](items []T, params pagination.PaginationParams) []T {
	if len(items) == 0 {
		return []T{}
	}

	offset := params.Offset()
	if offset >= len(items) {
		return []T{}
	}

	limit := params.Limit()
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}

	return items[offset:end]
}
