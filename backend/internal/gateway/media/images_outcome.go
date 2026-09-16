package media

// ImageOutcome 决定已观测图片是否足以保留失败结果，并维持不同传输的计数回退。
// OAuth 已完成的正常响应允许使用请求张数；API Key 的 SSE 则必须有实际产出。
func ImageOutcome(stream, oauth, eventStream bool, requested, observed int, failure error) (int, bool) {
	if failure != nil && (!stream || observed <= 0) {
		return 0, false
	}
	count := observed
	if failure == nil && count <= 0 && (oauth || !stream || !eventStream) {
		count = requested
	}
	return count, true
}
