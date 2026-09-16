// 供应商客户端唯一实现属于 search/provider。
package websearch

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/search/provider"
)

type TavilyProvider = provider.TavilyProvider

func NewTavilyProvider(key string, c *http.Client) *TavilyProvider {
	return provider.NewTavilyProvider(key, c)
}
