//go:build unit

// 生产消费者已迁出；原 unit 断言通过唯一实现的兼容入口验证。
package service

import (
	uuid "github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func sanitizeGrokResponsesTools(body []byte) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		SanitizeGrokResponsesTools(body)
}
