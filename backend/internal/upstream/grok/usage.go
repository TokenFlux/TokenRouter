// 旧平台入口委托共同的 usage 数值关系，不复制计量算法。
package grok

import "github.com/TokenFlux/TokenRouter/internal/protocol"

func IncludeIndependentReasoningTokens(input, output, total, reasoning int64) int64 {
	return protocol.IncludeIndependentReasoningTokens(input, output, total, reasoning)
}
