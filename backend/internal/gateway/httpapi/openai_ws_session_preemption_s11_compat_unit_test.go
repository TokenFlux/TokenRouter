//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
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
