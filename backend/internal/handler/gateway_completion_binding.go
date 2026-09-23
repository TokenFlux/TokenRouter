// 旧 HTTP 对象只引用 app 绑定的完成器，不持有第二份缓存、资金规则或队列。
package handler

import "github.com/TokenFlux/TokenRouter/internal/gateway/completion"

// BindCompletionRecorder 在开放 HTTP 前接入应用唯一实例。
func (h *GatewayHandler) BindCompletionRecorder(r *completion.Recorder) { h.completionRecorder = r }
func (h *GatewayHandler) completionRuntime() *completion.Recorder {
	if h.completionRecorder != nil {
		return h.completionRecorder
	}
	return h.gatewayService.CompletionRecorder()
}

// BindCompletionRecorder 在开放 HTTP 前接入应用唯一实例。
func (h *OpenAIGatewayHandler) BindCompletionRecorder(r *completion.Recorder) {
	h.completionRecorder = r
}
func (h *OpenAIGatewayHandler) completionRuntime() *completion.Recorder {
	if h.completionRecorder != nil {
		return h.completionRecorder
	}
	return h.gatewayService.CompletionRecorder()
}
