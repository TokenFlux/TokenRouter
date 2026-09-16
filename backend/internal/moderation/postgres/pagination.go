// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

func paginationResultFromTotal(total int64, params pagination.PaginationParams) *pagination.PaginationResult {
	return pagination.ResultFromTotal(total, params)
}
