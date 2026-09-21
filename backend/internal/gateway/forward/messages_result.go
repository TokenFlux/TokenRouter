package forward

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// MessagesResult 保存通用 Messages 执行的原始用量与完成元数据，不携带旧业务实体。
type MessagesResult struct {
	RequestID string
	// UpstreamHeaders 是直接上游的响应头，用于按账户配置解析上游请求标识。
	UpstreamHeaders map[string][]string
	Usage           upstream.TokenUsage
	Model           string
	// UpstreamModel is the actual upstream model after mapping.
	// Prefer empty when it is identical to Model; persistence normalizes equal values away as no-op mappings.
	UpstreamModel    string
	Stream           bool
	Duration         time.Duration
	FirstTokenMs     *int // 首字时间（流式请求）
	ClientDisconnect bool // 客户端是否在流式传输过程中断开
	ReasoningEffort  *string
	// RequestedReasoningEffort 保存客户端在兼容层映射前提交的推理档位。
	RequestedReasoningEffort *string
	// UpstreamResponseServiceTier 是上游响应声明的实际服务档位；空值表示未声明或无法确认。
	UpstreamResponseServiceTier string
	// ServiceTier 是请求侧声明的服务档位；计费时只允许按上游实际档位降档。
	ServiceTier *string
	// 图片生成计费字段（图片生成模型使用）
	ImageCount         int    // 生成的图片数量
	ImageSize          string // 最终计费尺寸 "1K", "2K", "4K"
	ImageInputSize     string // 请求中的原始图片尺寸
	ImageOutputSize    string // 上游响应中的图片尺寸
	ImageOutputSizes   []string
	ImageSizeSource    string
	ImageSizeBreakdown map[string]int
	SearchCount        int
	AudioUsage         *protocol.AudioUsage
}

// MessagesFromAttempt 按既有完成入口投影基础观测；Header 与媒体扩展仍由调用方按原时点附加。
func MessagesFromAttempt(result upstream.AttemptResult) *MessagesResult {
	return &MessagesResult{RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, Usage: result.Usage, Stream: result.Stream, Duration: result.Duration, ClientDisconnect: result.ClientDisconnect, FirstTokenMs: result.FirstTokenMs}
}
