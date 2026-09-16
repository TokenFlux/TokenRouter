//go:build unit

// 历史名称只在 unit 构建保留，测试继续覆盖唯一搜索核心。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
)

func isOnlyWebSearchToolInBody(body []byte) bool { return searchtools.IsOnlyWebSearchToolInBody(body) }
func extractSearchQueryFromBody(body []byte) string {
	return searchtools.ExtractSearchQueryFromBody(body)
}
func buildSearchResultBlocks(results []contract.SearchResult) []map[string]string {
	return searchtools.BuildSearchResultBlocks(results)
}
func buildTextSummary(query string, results []contract.SearchResult) string {
	return searchtools.BuildTextSummary(query, results)
}
