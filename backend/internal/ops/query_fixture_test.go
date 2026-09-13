package ops

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const PlatformOpenAI = capability.PlatformOpenAI
const PlatformAnthropic = capability.PlatformAnthropic
const StatusActive = "active"
const StatusError = "error"

func newLegacyShapeOpsService(repo OpsRepository, settings Settings, cfg *Options, a AccountReader, u UserReader, c ConcurrencyReader, _ any, _ any, _ any, _ any, sink *OpsSystemLogSink) *OpsService {
	return NewOpsService(repo, settings, cfg, a, u, c, sink, nil)
}
func newTestSystemLogSink(repo SystemLogWriter) *OpsSystemLogSink {
	return NewOpsSystemLogSink(repo, SystemLogSinkOptions{})
}
