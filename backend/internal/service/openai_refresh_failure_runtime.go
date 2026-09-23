package service

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// BindRuntimeBlockState 在请求开放前接入应用唯一的内存健康状态。
func (s *OpenAIGatewayService) BindRuntimeBlockState(state *account.RuntimeBlockState) {
	s.runtimeBlocks.Store(state)
}

// 独立构造的消费者保持独占状态；生产由 app 预先绑定，不创建第二份缓存。
func (s *OpenAIGatewayService) runtimeBlockState() *account.RuntimeBlockState {
	if state := s.runtimeBlocks.Load(); state != nil {
		return state
	}
	state := account.NewRuntimeBlockState(time.Now)
	if s.runtimeBlocks.CompareAndSwap(nil, state) {
		return state
	}
	return s.runtimeBlocks.Load()
}

// PrepareRefreshFailure 委托原生代次保护，保留存储前取得发布资格的时点。
func (s *OpenAIGatewayService) PrepareRefreshFailure(id int64) func(account.RefreshFailureNotice) {
	if s == nil {
		return func(account.RefreshFailureNotice) {}
	}
	return s.runtimeBlockState().PrepareRefreshFailure(id)
}
