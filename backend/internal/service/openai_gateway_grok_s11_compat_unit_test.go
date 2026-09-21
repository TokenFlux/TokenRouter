//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	uuid "github.com/google/uuid"
)

func patchGrokResponsesBodyWithClientTools(body []byte, upstreamModel string) ([]byte, bridge.ResponsesClientToolMapping, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		PatchGrokResponsesBodyWithClientTools(body, upstreamModel)
}
