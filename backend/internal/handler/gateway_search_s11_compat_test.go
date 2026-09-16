// 原独立搜索测试入口只在测试构建保留，平台算法由 upstream/grok 唯一实现。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type grokStandaloneSearchRequest = searchtools.StandaloneRequest

func buildGrokXSearchResponsesBody(request grokStandaloneSearchRequest, model string) ([]byte, error) {
	return xai.BuildGrokXSearchResponsesBody(projectNativeStandaloneSearch(request), model)
}
