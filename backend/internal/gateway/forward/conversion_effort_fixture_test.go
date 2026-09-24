//go:build unit

package forward_test

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// 输出契约使用原来的扫描缓冲和真实 HTTP Adapter，不模拟转换结果。
func chatEffortFixture(body []byte, models ...string) *string {
	return forward.ExtractEffort(body, true, capability.NormalizeRecordedOpenAIEffortForModel, models...)
}
func responsesEffortFixture(body []byte, models ...string) *string {
	return forward.ExtractEffort(body, false, capability.NormalizeRecordedOpenAIEffortForModel, models...)
}

// 模式只在包初始化时写入，保留各测试的并行执行。
