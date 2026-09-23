// 完成边界只负责旧形状的字段投影和端口转接；计费顺序由 completion 唯一拥有。
package service

import (
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

// BindCompletionRecorder 在开放请求前接收原生完成实例，不新建缓存或后台任务。
func (s *GatewayService) BindCompletionRecorder(value *completion.Recorder) {
	s.completionRecorder = value
}

// BindCompletionRecorder 在开放请求前接收原生完成实例，不新建缓存或后台任务。
func (s *OpenAIGatewayService) BindCompletionRecorder(value *completion.Recorder) {
	s.completionRecorder = value
}

// CompletionRecorder 返回组合根已绑定的唯一完成器，不再从旧网关字段重建应用依赖。
func (s *GatewayService) CompletionRecorder() *completion.Recorder { return s.completionRecorder }

// CompletionRecorder 返回与生产入口共享的原生实例。
func (s *OpenAIGatewayService) CompletionRecorder() *completion.Recorder { return s.completionRecorder }
