package ws

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

// OpenAIIngressHooks 定义入站 WS 每个 turn 的生命周期回调。
type OpenAIIngressHooks struct {
	// ClientLifecycleContext 是叠加 ingress 租约取消信号前的客户端请求上下文。
	// 下行写使用它保留客户端断连和服务关闭信号，同时避免租约丢失中断当前帧。
	ClientLifecycleContext context.Context
	// InitialRequestModel 是首帧渠道映射前的请求模型，只用于 usage metadata
	// 的 reasoning effort 后缀推导，禁止用于上游请求或计费模型。
	InitialRequestModel string
	// InitialTurnStartedAt 是首轮 response.create 被接受时的时间快照。
	InitialTurnStartedAt time.Time
	// MaxReasoningEffort 限制当前 WS 会话中显式指定的推理强度。
	MaxReasoningEffort string
	// MaxReasoningEffortOverLimit 控制显式推理强度超限时降档或拒绝。
	MaxReasoningEffortOverLimit string
	// ReasoningEffortMappings 在当前 WS 会话中改写显式指定的推理强度。
	ReasoningEffortMappings []routing.ReasoningEffortMapping
	// ResolveRoutingModel 在账号映射前逐轮把客户端模型 R 解析为渠道模型 C。
	// payload 用于按渠道映射后的完整请求判断该轮能力；返回错误时当前帧不得发送上游。
	ResolveRoutingModel func(turn int, requestedModel string, payload []byte) (string, error)
	// ResolveFastModePolicy 逐轮刷新 API Key Fast 策略，避免长连接永久沿用握手快照。
	ResolveFastModePolicy func(turn int) string
	// TurnStarted 报告每轮 response.create 的开始时刻；时间值应在策略处理前捕获。
	TurnStarted   func(turn int, startedAt time.Time)
	BeforeTurn    func(turn int) error
	BeforeRequest func(turn int, payload []byte, originalModel, previousResponseID string) ([]byte, error)
	// OnUpstreamError 在上游 WS 返回 error/failed 类事件时触发，用于记录 OpenAI cyber 等上游风控信号。
	OnUpstreamError func(turn int, originalModel string, statusCode int, responseBody []byte, message string)
	AfterTurn       func(capture OpenAITurnCapture)
}

// OpenAITurnCapture 描述一次 WS turn 完成后用于用量结算和错误处理的上下文。
type OpenAITurnCapture struct {
	Turn               int
	StartedAt          time.Time
	RequestBody        []byte
	OriginalModel      string
	PreviousResponseID string
	Result             *forwardcore.OpenAIResult
	Err                error
	PayloadSource      string
}
