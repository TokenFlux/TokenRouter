package forward

import (
	"encoding/json"
	"time"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// OpenAIResult 保存 OpenAI 兼容执行的观测结果，恢复报文仍由本次执行私有持有。
type OpenAIResult struct {
	RequestID  string
	ResponseID string
	// UpstreamHeaders 是直接上游的响应头，用于按账户配置解析上游请求标识。
	UpstreamHeaders map[string][]string
	Usage           protocolopenai.ForwardUsage
	Model           string // 原始模型（用于响应和日志显示）
	// BillingModel is the model used for cost calculation.
	// When non-empty, CalculateCost uses this instead of Model.
	// This is set by the Anthropic Messages conversion path where
	// the mapped upstream model differs from the client-facing model.
	BillingModel string
	// UpstreamModel is the actual model sent to the upstream provider after mapping.
	// Empty when no mapping was applied (requested model was used as-is).
	UpstreamModel string
	// UpstreamResponseServiceTier 是上游响应声明的实际服务档位，供计费只降档使用。
	UpstreamResponseServiceTier string
	// UpstreamEndpoint 是该请求实际使用的上游 API 路径，避免同一下游协议可选择
	// 多个上游端点时只能依赖推断。
	UpstreamEndpoint string
	// ServiceTier is the final tier sent upstream after policy rewriting.
	// The upstream response declaration remains separate above and is reconciled
	// at usage-recording time, where the credential protocol is available.
	ServiceTier *string
	// ReasoningEffort 是最终上游请求中的推理档位；nil 表示未提供或不适用。
	ReasoningEffort *string
	// RequestedReasoningEffort 是策略与模型映射前客户端请求的推理档位。
	RequestedReasoningEffort *string
	Stream                   bool
	OpenAIWSMode             bool
	// UpstreamTerminalEvent 记录 Responses WebSocket 请求观测到的规范化终止事件；
	// 空值保持旧调用方和非 WebSocket 请求的成功语义。
	UpstreamTerminalEvent string
	ResponseHeaders       map[string][]string
	Duration              time.Duration
	FirstTokenMs          *int
	ClientDisconnect      bool
	ImageCount            int
	ImageSize             string
	ImageInputSize        string
	ImageOutputSize       string
	ImageOutputSizes      []string
	ImageSizeSource       string
	ImageSizeBreakdown    map[string]int
	// UpstreamWarning 仅在上游成功完成传输但 terminal 事件携带风控拒绝时填充。
	UpstreamWarning *UpstreamWarning
	VideoCount      int
	VideoResolution string
	// VideoDurationSeconds 是提交时请求的生成时长（xAI 按输出秒数计费），已归一化到 1-15 秒。
	VideoDurationSeconds int
	// WebSearchCalls 是 Codex alpha/search 网页搜索调用次数（每次成功请求为 1）。
	// 上游不返回 usage 字段，>0 时走按次计费（分组单价 × 次数 × 倍率）。
	WebSearchCalls int
	// SearchCount 是 Grok 原生 web_search 或工具搜索调用次数，按每千次计价。
	SearchCount int
	// AudioUsage 在有值时携带 Voice 计费单位。
	AudioUsage *protocolcore.AudioUsage

	wsReplayInput                []json.RawMessage
	wsReplayInputExists          bool
	wsAccountFailoverReplayInput []json.RawMessage
}

// SucceededForScheduling 判断转发结果能否作为上游调度成功，并清除模型级短暂状态。
// 零值继续保持现有非 WebSocket 调用方的成功语义。
func (r *OpenAIResult) SucceededForScheduling() bool {
	if r == nil || !r.OpenAIWSMode || r.UpstreamTerminalEvent == "" {
		return true
	}
	switch r.UpstreamTerminalEvent {
	case "response.completed", "response.done":
		return true
	default:
		return false
	}
}

// SetWSReplayInput 保存本轮已经规范化的恢复输入，沿用调用方取得快照的时点。
func (r *OpenAIResult) SetWSReplayInput(input []json.RawMessage, exists bool) {
	r.wsReplayInput = input
	r.wsReplayInputExists = exists
}

// WSReplayInput 返回本次执行拥有的恢复输入，不重新推断是否存在 input 字段。
func (r *OpenAIResult) WSReplayInput() ([]json.RawMessage, bool) {
	return r.wsReplayInput, r.wsReplayInputExists
}

// SetWSAccountFailoverReplayInput 保存跨账号恢复收集器的既有结果。
func (r *OpenAIResult) SetWSAccountFailoverReplayInput(input []json.RawMessage) {
	r.wsAccountFailoverReplayInput = input
}

// WSAccountFailoverReplayInput 供当前轮恢复边界读取；不进入 JSON 或完成计费投影。
func (r *OpenAIResult) WSAccountFailoverReplayInput() []json.RawMessage {
	return r.wsAccountFailoverReplayInput
}
