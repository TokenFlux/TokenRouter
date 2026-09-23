package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/textattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
)

// NewGeminiNativeExecutor 在 Recorder 完成绑定后构造，不增加共享运行状态。
func (h *GatewayHandler) NewGeminiNativeExecutor() *textflow.MessagesExecutor {
	runtime := textattempt.New(messageAttemptBindings(h))
	return textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, StopOnCanceledContext: false, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, StopOnCanceledContext: false, Observe: telemetry.Failover})
}
