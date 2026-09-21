// 读取错误仅引用原生唯一实现，本地大小限制仍由 HTTP 入口明确传入。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

func shouldClassifyOpenAIUpstreamStreamReadError(err error, contexts ...context.Context) bool {
	return openai.ShouldClassifyUpstreamStreamReadError(err, ErrUpstreamResponseBodyTooLarge, contexts...)
}
