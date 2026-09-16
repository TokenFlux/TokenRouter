// Chat 图片工具入口复用原生唯一规则。
package service

import native "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

func stripRedundantGrokChatViewImageTool(body []byte) ([]byte, error) {
	return native.StripRedundantGrokChatViewImageTool(body)
}
