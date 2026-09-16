// 旧执行入口仅决定是否为 Grok；过滤状态与算法由原生层唯一持有。
package service

import (
	"io"

	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const grokResponsesPingFrameMaxLines = nativegrok.ResponsesPingFrameMaxLines
const grokResponsesPingFrameMaxBytes = nativegrok.ResponsesPingFrameMaxBytes

func newGrokResponsesBillingPingFilterBody(source io.ReadCloser, account *Account, maxLineSize int) io.ReadCloser {
	if account == nil || account.Platform != PlatformGrok {
		return source
	}
	return nativegrok.NewGrokResponsesBillingPingFilterBody(source, maxLineSize)
}
