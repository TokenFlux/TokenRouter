// 旧执行入口仅决定是否为 Grok；过滤状态与算法由原生层唯一持有。
package service

import (
	"io"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func newGrokResponsesBillingPingFilterBody(source io.ReadCloser, account *Account, maxLineSize int) io.ReadCloser {
	if account == nil || account.Platform != capability.PlatformGrok {
		return source
	}
	return grok.NewGrokResponsesBillingPingFilterBody(source, maxLineSize)
}
