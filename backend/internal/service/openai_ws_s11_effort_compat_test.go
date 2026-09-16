//go:build unit

// 旧 effort 断言仅在 unit 集合使用，仍委托同一会话状态。
package service

func (m *openAIWSPassthroughUsageMeta) captureRequestedReasoningEffort(body []byte, models ...string) {
	if m != nil {
		m.CaptureRequestedReasoningEffort(body, models...)
	}
}
