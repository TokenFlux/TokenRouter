package httpapi

import (
	"context"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/wsrelay"
	coderws "github.com/coder/websocket"
)

// wsPlatformFrames 只在边界转换帧枚举，供应商 relay 继续使用唯一实现。
type wsPlatformFrames struct{ gatewayws.FrameConn }

func (c wsPlatformFrames) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	typ, body, err := c.FrameConn.ReadFrame(ctx)
	return coderws.MessageType(typ), body, err
}
func (c wsPlatformFrames) WriteFrame(ctx context.Context, typ coderws.MessageType, body []byte) error {
	return c.FrameConn.WriteFrame(ctx, int(typ), body)
}

func relayUsage(u openaiwsv2.Usage) wire.ForwardUsage {
	return wire.ForwardUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheCreationInputTokens: u.CacheCreationInputTokens, CacheReadInputTokens: u.CacheReadInputTokens, ImageOutputTokens: u.ImageOutputTokens}
}
func relayExitProjection(e openaiwsv2.RelayExit) gatewayws.RelayExit {
	return gatewayws.RelayExit{Stage: e.Stage, Err: e.Err, Graceful: e.Graceful, WroteDownstream: e.WroteDownstream}
}

// RunRelay 接入帧执行原语；回调中的网关资格、快照及完成规则全部由网关核心提供。
func (p *wsPassthroughAdapter) RunRelay(input gatewayws.RelayInput) (gatewayws.RelayResult, *gatewayws.RelayExit) {
	o := input.Options
	opts := openaiwsv2.RelayOptions{
		WriteTimeout: o.WriteTimeout, IdleTimeout: o.IdleTimeout, UpstreamDrainTimeout: o.UpstreamDrainTimeout,
		FirstTurnStartedAt: o.FirstTurnStartedAt, TakeNextTurnStartedAt: o.TakeNextTurnStartedAt,
		FirstMessageType: coderws.MessageType(o.FirstMessageType), FirstMessageSent: o.FirstMessageSent, StartClientAfterFirstDownstream: o.StartClientAfterFirstDownstream,
		OnUsageParseFailure: o.OnUsageParseFailure, OnUpstreamEvent: o.OnUpstreamEvent, Now: o.Now,
	}
	if o.OnTurnComplete != nil {
		opts.OnTurnComplete = func(t openaiwsv2.RelayTurnResult) {
			o.OnTurnComplete(gatewayws.RelayTurnResult{RequestModel: t.RequestModel, ResponseServiceTier: t.ResponseServiceTier, Usage: relayUsage(t.Usage), RequestID: t.RequestID, TerminalEventType: t.TerminalEventType, StartedAt: t.StartedAt, Duration: t.Duration, FirstTokenMs: t.FirstTokenMs})
		}
	}
	if o.BeforeWriteClient != nil {
		opts.BeforeWriteClient = func(typ coderws.MessageType, body []byte, wrote bool) error {
			return o.BeforeWriteClient(int(typ), body, wrote)
		}
	}
	if o.BeforeClientWrite != nil {
		opts.BeforeClientWrite = func(typ coderws.MessageType, body []byte) { o.BeforeClientWrite(int(typ), body) }
	}
	if o.AfterClientWrite != nil {
		opts.AfterClientWrite = func(typ coderws.MessageType, body []byte, err error) { o.AfterClientWrite(int(typ), body, err) }
	}
	if o.BeforeRelayCancel != nil {
		opts.BeforeRelayCancel = func(e openaiwsv2.RelayExit) { o.BeforeRelayCancel(relayExitProjection(e)) }
	}
	if o.ReadClientFrame != nil {
		opts.ReadClientFrame = func(ctx context.Context, conn openaiwsv2.FrameConn) (coderws.MessageType, []byte, error) {
			typ, body, err := o.ReadClientFrame(ctx, openAIWSCoreFrames{conn})
			return coderws.MessageType(typ), body, err
		}
	}
	if o.OnTrace != nil {
		opts.OnTrace = func(e openaiwsv2.RelayTraceEvent) {
			o.OnTrace(gatewayws.RelayTraceEvent{Stage: e.Stage, Direction: e.Direction, MessageType: e.MessageType, PayloadBytes: e.PayloadBytes, Graceful: e.Graceful, WroteDownstream: e.WroteDownstream, Error: e.Error})
		}
	}
	result, exit := openaiwsv2.RunEntry(openaiwsv2.EntryInput{Ctx: input.Ctx, ClientConn: wsPlatformFrames{input.ClientConn}, UpstreamConn: wsPlatformFrames{input.UpstreamConn}, FirstClientMessage: input.FirstClientMessage, Options: opts})
	out := gatewayws.RelayResult{RequestModel: result.RequestModel, ResponseServiceTier: result.ResponseServiceTier, Usage: relayUsage(result.Usage), RequestID: result.RequestID, TerminalEventType: result.TerminalEventType, FirstTokenMs: result.FirstTokenMs, Duration: result.Duration, ClientToUpstreamFrames: result.ClientToUpstreamFrames, UpstreamToClientFrames: result.UpstreamToClientFrames, DroppedDownstreamFrames: result.DroppedDownstreamFrames}
	if exit == nil {
		return out, nil
	}
	e := relayExitProjection(*exit)
	return out, &e
}
