// 本文件维护 pagination 的所属能力；兼容入口复用唯一实现。
package pagination

func Slice[T any](items []T, params PaginationParams) []T {
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
