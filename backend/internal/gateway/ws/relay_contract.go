package ws

import (
	"context"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// RelayInput 只提供同步帧端口和事件观察，供应商 relay 算法由 Adapter 调用。
type RelayInput struct {
	Ctx                context.Context
	ClientConn         FrameConn
	UpstreamConn       FrameConn
	FirstClientMessage []byte
	Options            RelayOptions
}
type RelayResult struct {
	RequestModel string
	// ResponseServiceTier 是终止响应声明的上游实际服务档位。
	ResponseServiceTier     string
	Usage                   wire.ForwardUsage
	RequestID               string
	TerminalEventType       string
	FirstTokenMs            *int
	Duration                time.Duration
	ClientToUpstreamFrames  int64
	UpstreamToClientFrames  int64
	DroppedDownstreamFrames int64
}

type RelayTurnResult struct {
	RequestModel        string
	ResponseServiceTier string
	Usage               wire.ForwardUsage
	RequestID           string
	TerminalEventType   string
	StartedAt           time.Time
	Duration            time.Duration
	FirstTokenMs        *int
}

type RelayExit struct {
	Stage           string
	Err             error
	Graceful        bool
	WroteDownstream bool
}

type RelayOptions struct {
	WriteTimeout                    time.Duration
	IdleTimeout                     time.Duration
	UpstreamDrainTimeout            time.Duration
	FirstTurnStartedAt              time.Time
	TakeNextTurnStartedAt           func() time.Time
	FirstMessageType                int
	FirstMessageSent                bool
	StartClientAfterFirstDownstream bool
	OnUsageParseFailure             func(eventType string, usageRaw string)
	// OnUpstreamEvent 在每个上游文本事件解析出 type 后回调，由 service 层统一筛选 warning 事件。
	OnUpstreamEvent   func(eventType string, payload []byte)
	OnTurnComplete    func(turn RelayTurnResult)
	BeforeWriteClient func(msgType int, payload []byte, wroteDownstream bool) error
	BeforeClientWrite func(msgType int, payload []byte)
	AfterClientWrite  func(msgType int, payload []byte, writeErr error)
	BeforeRelayCancel func(exit RelayExit)
	ReadClientFrame   func(ctx context.Context, clientConn FrameConn) (int, []byte, error)
	OnTrace           func(event RelayTraceEvent)
	Now               func() time.Time
}

type RelayTraceEvent struct {
	Stage           string
	Direction       string
	MessageType     string
	PayloadBytes    int
	Graceful        bool
	WroteDownstream bool
	Error           string
}
