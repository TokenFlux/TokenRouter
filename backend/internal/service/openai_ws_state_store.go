// WS 会话缓存状态由 gateway/session 唯一持有；旧入口只委托。
package service

import "github.com/TokenFlux/TokenRouter/internal/gateway/session"

type OpenAIWSStateStore = session.OpenAIWSStateStore

const openAIWSStateStoreRedisTimeout = session.StateStoreRedisTimeout

func NewOpenAIWSStateStore(cache GatewayCache) OpenAIWSStateStore {
	return session.NewOpenAIWSStateStore(cache, logOpenAIWSModeInfo)
}
