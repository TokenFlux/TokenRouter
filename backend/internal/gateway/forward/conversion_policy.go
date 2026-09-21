package forward

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ConversionOptionsForModel 在网关边界选择既有型号策略；协议转换器只接收行为开关。
func ConversionOptionsForModel(model string) bridge.RequestOptions {
	return bridge.RequestOptions{
		DropSampling:      capability.ResponsesBridgeDropsSampling(model),
		SupportsMaxEffort: capability.ResponsesBridgeSupportsMaxEffort(model),
	}
}
