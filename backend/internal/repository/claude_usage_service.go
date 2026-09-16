// 旧 HTTP 接口只投影技术调用，唯一请求实现位于 Anthropic 客户端。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

type claudeUsageService = native.UsageClient

func NewClaudeUsageFetcher(upstream service.HTTPUpstream) service.ClaudeUsageFetcher {
	if upstream == nil {
		return native.NewUsageClient(nil)
	}
	return native.NewUsageClient(upstream.DoWithTLS)
}
