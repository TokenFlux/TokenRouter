//go:build unit

package googleforward_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// stubSmartRetryCache 用于 handleSmartRetry 测试的 GatewayCache mock
// 仅关注 DeleteSessionAccountID 的调用记录
type stubSmartRetryCache struct {
	session.GatewayCache // 嵌入接口，未实现的方法 panic（确保只调用预期方法）
	deleteCalls          []deleteSessionCall
}

type deleteSessionCall struct {
	groupID     int64
	sessionHash string
}

func (c *stubSmartRetryCache) DeleteSessionAccountID(_ context.Context, groupID int64, sessionHash string) error {
	c.deleteCalls = append(c.deleteCalls, deleteSessionCall{groupID: groupID, sessionHash: sessionHash})
	return nil
}
