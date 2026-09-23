package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
)

// NewCompatibleTextExecutor 在 Recorder 完成绑定后构造，不增加共享运行状态。
func (h *GatewayHandler) NewCompatibleTextExecutor() *textflow.MessagesExecutor {
	runtime := &fixedMessagesRuntime{dependencies: newMessageExecutionDependencies(h)}
	return textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, StopOnCanceledContext: true, Observe: telemetry.Failover})
}
