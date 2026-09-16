//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
)

func patchGrokResponsesBodyWithClientTools(body []byte, upstreamModel string) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	return grokBodyCodec().PatchGrokResponsesBodyWithClientTools(body, upstreamModel)
}
