//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	"encoding/json"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

func appendRawJSON(existing json.RawMessage, fragment string) json.RawMessage {
	return forwardcore.AppendRawJSON(existing, fragment)
}
