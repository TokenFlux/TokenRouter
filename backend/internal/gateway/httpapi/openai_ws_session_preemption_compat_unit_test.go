//go:build unit

// 测试私有兼容入口委托所属模块的生产实现。
package httpapi

import (
	"context"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
)

func openAIWSSessionPreemptCacheHash(apiKeyID int64, sessionHash string) string {
	return gatewayws.CacheHash(apiKeyID, sessionHash)
}
func (s *wsExecutionFixture) claimOpenAIWSSessionPreemptOwner(ctx context.Context, key openAIWSSessionPreemptKey, owner string) (string, bool) {
	return s.wsPreemption().Claim(ctx, gatewayws.PreemptKey{GroupID: key.groupID, APIKeyID: key.apiKeyID, SessionHash: key.sessionHash}, owner)
}
func (s *wsExecutionFixture) releaseOpenAIWSSessionPreemptOwner(ctx context.Context, key openAIWSSessionPreemptKey, owner string) {
	s.wsPreemption().Release(ctx, gatewayws.PreemptKey{GroupID: key.groupID, APIKeyID: key.apiKeyID, SessionHash: key.sessionHash}, owner)
}
