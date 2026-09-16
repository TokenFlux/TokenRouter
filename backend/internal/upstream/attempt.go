package upstream

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// ExecutionTarget 是已选目标的受控句柄，具体凭据只对对应平台执行器可见。
type ExecutionTarget interface{ TargetID() int64 }

// AttemptInput 固化一次尝试的协议、报文和目标，不携带旧实体或可变业务服务。
type AttemptInput struct {
	Protocol      protocol.ProtocolID
	Body          []byte
	ResponseModel string
	Stream        bool
	Target        ExecutionTarget
}

// AttemptResult 与 error 独立返回；失败也可以携带已发生服务及上游已观测的用量。
type AttemptResult struct {
	// HTTP 提交与关闭重试窗口分别投影，不能从 TTFT 或响应 ID 推断。
	HTTPCommitted, RetryCommitted bool
	RequestID                     string
	// Responses 补充观测不参与平台之外的重试或资金决策。
	ResponseID                                 string
	SearchCount, ImageInputTokens              int
	ImageOutputSizes                           []string
	UpstreamHeaders                            http.Header
	Model, UpstreamModel                       string
	Usage                                      TokenUsage
	HasUsage, Served, Stream, ClientDisconnect bool
	// FailureClass 只描述技术失败，资金和重试裁决由外层拥有。
	// EstimatedTokenCount 仅承载既有 countTokens 本地回退，不是可结算 usage。
	EstimatedTokenCount *int
	// ObservedImages 只报告已观测的图片张数，价格与回退规则由调用方拥有。
	ObservedImages int
	// AudioUsage 保留既有语音计量，具体价格和完成处理由调用方负责。
	AudioUsage *protocol.AudioUsage
	// MediaBody 是本次有界读取并输出的媒体 JSON，任务完成与资金规则由调用方处理。
	MediaBody    []byte
	FailureClass string
	Cancelled    bool
	Duration     time.Duration
	// FirstSemanticOutput 与旧 TTFT 字段独立；前导进度不产生该观测。
	FirstSemanticOutput          *time.Duration
	FirstTokenMs                 *int
	ServiceTier, ReasoningEffort string
}

// Executor 只完成本次尝试，账号切换和资金完成处理由网关拥有。
// @project-doc docs/architecture/gateway_request_lifecycle.md#upstream_attempt_ownership
type Executor interface {
	Execute(context.Context, AttemptInput, OutputSink) (AttemptResult, error)
}
