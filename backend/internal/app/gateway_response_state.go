package app

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// provideOpenAIResponseState 由 app 持有唯一会话存储；构造只分配原状态，不打开连接或启动任务。
func provideOpenAIResponseState(cache session.GatewayCache) session.OpenAIWSStateStore {
	return session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
}
