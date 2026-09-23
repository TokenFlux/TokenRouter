package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
)

// NewMessagesExecutor 供 app 固定绑定；先完成唯一 Recorder 绑定，再构造此执行器。
func (h *GatewayHandler) NewMessagesExecutor() *textflow.MessagesExecutor {
	runtime := &fixedMessagesRuntime{dependencies: newMessageExecutionDependencies(h)}
	return textflow.NewMessagesExecutor(runtime,
		textflow.MessageOptions{MaxSwitches: h.maxAccountSwitches, CompletePartialFailure: true, Observe: telemetry.Failover},
		textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, Observe: telemetry.Failover},
	)
}
