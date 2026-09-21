//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	grok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	uuid "github.com/google/uuid"
)

func buildGrokCompactRequestBody(body []byte) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		BuildGrokCompactRequestBody(body)
}
