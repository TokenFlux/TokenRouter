// 搜索核心只使用明确的技术端口和纯请求值。
package search

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/search/contract"
)

type SearchRequest = contract.SearchRequest
type SearchResponse = contract.SearchResponse
type SearchResult = contract.SearchResult
type ProviderConfig = contract.ProviderConfig
type Provider = contract.Provider

const ProviderTypeBrave = contract.ProviderTypeBrave
const ProviderTypeTavily = contract.ProviderTypeTavily
const defaultMaxResults = contract.DefaultMaxResults

type QuotaState interface {
	Increment(context.Context, string, time.Duration) (int64, error)
	Decrement(context.Context, string) error
	Usage(context.Context, string) (int64, error)
	Reset(context.Context, string) error
	MarkProxy(context.Context, int64, time.Duration) error
	ProxyAvailable(context.Context, int64) bool
}
type Executor interface {
	Search(context.Context, ProviderConfig, SearchRequest) (*SearchResponse, error)
	IsProxyError(error) bool
	CloseIdle()
}

// CloneProviderConfigs 为每个跨实例边界复制所有可变指针。
func CloneProviderConfigs(in []ProviderConfig) []ProviderConfig {
	if in == nil {
		return nil
	}
	out := make([]ProviderConfig, len(in))
	copy(out, in)
	for i := range out {
		out[i].SubscribedAt = CloneInt64(out[i].SubscribedAt)
		out[i].ExpiresAt = CloneInt64(out[i].ExpiresAt)
	}
	return out
}
func CloneInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}
