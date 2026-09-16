// 供应商客户端唯一实现属于 search/provider。
package websearch

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/search/provider"
)

type BraveProvider = provider.BraveProvider

func NewBraveProvider(key string, c *http.Client) *BraveProvider {
	return provider.NewBraveProvider(key, c)
}
