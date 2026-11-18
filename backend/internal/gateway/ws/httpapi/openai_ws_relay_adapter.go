package httpapi

import (
	"context"

	coderws "github.com/coder/websocket"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	openaiwsv2 "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws/relay"
)

// openai_ws_relay_adapter.go connects gateway session callbacks and result
// records to the dedicated upstream relay.
// The dedicated upstream relay reports usage, terminal identity and timing for
// individual turns as well as aggregate connection observations. Callback
// conversion passes those values to the gateway controller with message-type
// translation, keeping paired client writes and close preparation attached to
// the same relay execution.

// wsPlatformFrames converts gateway frame types to the upstream relay enum.
type wsPlatformFrames struct {
	gatewayws.FrameConn /* gateway frame transport reference */
}

// ReadFrame forwards a gateway read and converts its integer type to the
// upstream enum. It returns the same bytes and error, with read cancellation
// governed by the supplied context and wrapped transport.
func (c wsPlatformFrames) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	typ, body, err := c.FrameConn.ReadFrame(ctx)
	return coderws.MessageType(typ), body, err
}

// WriteFrame converts an upstream message type and forwards its payload to the
// gateway transport. The caller's context controls the synchronous write;
// final transport release remains owned by the surrounding session.
func (c wsPlatformFrames) WriteFrame(ctx context.Context, typ coderws.MessageType, body []byte) error {
	return c.FrameConn.WriteFrame(ctx, int(typ), body)
}

// relayUsage copies upstream relay token counters into the gateway's usage
// representation.
func relayUsage(u openaiwsv2.Usage) wire.ForwardUsage {
	return wire.ForwardUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheCreationInputTokens: u.CacheCreationInputTokens, CacheWriteDetails: u.CacheWriteDetails, CacheReadInputTokens: u.CacheReadInputTokens, ImageOutputTokens: u.ImageOutputTokens}
}

// relayExitProjection copies relay exit cause, stage and output observations
// for the gateway controller.
func relayExitProjection(e openaiwsv2.RelayExit) gatewayws.RelayExit {
	return gatewayws.RelayExit{Stage: e.Stage, Err: e.Err, Graceful: e.Graceful, WroteDownstream: e.WroteDownstream}
}

// RunRelay invokes the upstream bidirectional relay with gateway transports
// and converted callbacks. Optional callbacks retain their ordering, accepted
// timestamps, terminal values and write outcomes; FirstMessageSent tells the
// upstream relay whether initial bytes already reached the provider. Returned
// observations can coexist with an exit error and must be assessed by the
// gateway session.
//
// Transport and diagnostic callbacks may run from different workers, so
// supplied callbacks satisfy the gateway RelayOptions concurrency rules.
// BeforeClientWrite and AfterClientWrite remain paired around terminal
// delivery, while BeforeRelayCancel lets the controller prepare client closure
// before internal cancellation. The upstream relay owns worker coordination;
// the surrounding session owns final client presentation and upstream close.
func (p *wsPassthroughAdapter) RunRelay(input gatewayws.RelayInput) (gatewayws.RelayResult, *gatewayws.RelayExit) {
	o := input.Options
	opts := openaiwsv2.RelayOptions{
		BeginPerformance: o.BeginPerformance,
		WriteTimeout:     o.WriteTimeout, WriteTimeoutForTurn: o.WriteTimeoutForTurn, IdleTimeout: o.IdleTimeout, UpstreamDrainTimeout: o.UpstreamDrainTimeout,
		FirstTurnStartedAt: o.FirstTurnStartedAt, TakeNextTurnStartedAt: o.TakeNextTurnStartedAt,
		FirstMessageType: coderws.MessageType(o.FirstMessageType), FirstMessageSent: o.FirstMessageSent, StartClientAfterFirstDownstream: o.StartClientAfterFirstDownstream,
		OnUsageParseFailure: o.OnUsageParseFailure, OnUpstreamEvent: o.OnUpstreamEvent, Now: o.Now,
	}
	if o.OnTurnComplete != nil {
		opts.OnTurnComplete = func(t openaiwsv2.RelayTurnResult) {
			o.OnTurnComplete(gatewayws.RelayTurnResult{
				RequestModel:          t.RequestModel,
				ResponseServiceTier:   t.ResponseServiceTier,
				UpstreamResponseModel: t.UpstreamResponseModel,
				Usage:                 relayUsage(t.Usage),
				RequestID:             t.RequestID,
				TerminalEventType:     t.TerminalEventType,
				StartedAt:             t.StartedAt,
				Duration:              t.Duration,
				Timings:               t.Timings,
			})
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
	out := gatewayws.RelayResult{
		RequestModel:            result.RequestModel,
		ResponseServiceTier:     result.ResponseServiceTier,
		UpstreamResponseModel:   result.UpstreamResponseModel,
		Usage:                   relayUsage(result.Usage),
		RequestID:               result.RequestID,
		TerminalEventType:       result.TerminalEventType,
		Timings:                 result.Timings,
		Duration:                result.Duration,
		ClientToUpstreamFrames:  result.ClientToUpstreamFrames,
		UpstreamToClientFrames:  result.UpstreamToClientFrames,
		DroppedDownstreamFrames: result.DroppedDownstreamFrames,
	}
	if exit == nil {
		return out, nil
	}
	e := relayExitProjection(*exit)
	return out, &e
}
