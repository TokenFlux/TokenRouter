// 搜索值由 search 唯一拥有，旧路径待消费者清零后删除。
package websearch

import "github.com/TokenFlux/TokenRouter/internal/search"

type SearchResult = search.SearchResult
type SearchRequest = search.SearchRequest
type SearchResponse = search.SearchResponse
type ProviderConfig = search.ProviderConfig

const ProviderTypeBrave = search.ProviderTypeBrave
const ProviderTypeTavily = search.ProviderTypeTavily
