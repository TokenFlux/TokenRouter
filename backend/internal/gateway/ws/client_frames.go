package ws

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

type clientFrameConn struct {
	closeError           func(int, string, error) error
	closedError          error
	normalizeCompleted   func([]byte) ([]byte, bool)
	conn                 ClientSocket
	controlCtx           context.Context
	interTurnIdleTimeout time.Duration
	interTurnStarted     chan struct{}
	waitingForNextTurn   atomic.Bool
	// The relay observes upstream payloads, while clients must keep seeing the
	// model identifier they supplied for the current turn.
	restoreResponseModel func([]byte) []byte
	restoreToolNames     func([]byte) []byte
}

// policyFrameConn wraps a client-side FrameConn and runs
// every client→upstream frame through the OpenAI Fast Policy. It is the
// passthrough-relay equivalent of the parseClientPayload integration in the
// ingress session path. filter returns:
//   - newPayload, nil, nil: forward the (possibly mutated) payload
//   - _, *PolicyBlocked, nil: block — the wrapper sends an error
//     event via onBlock and surfaces a transport-level error so the relay
//     stops reading from the client.
//   - _, _, err: a transport error other than block.
type policyFrameConn struct {
	closeError  func(int, string, error) error
	closedError error
	inner       FrameConn
	filter      func(msgType int, payload []byte) ([]byte, *PolicyBlocked, error)
	writeFilter func(msgType int, payload []byte) ([]byte, error)
	onBlock     func(blocked *PolicyBlocked)
}

func (c *policyFrameConn) ReadFrame(ctx context.Context) (int, []byte, error) {
	if c == nil || c.inner == nil {
		return TextFrame, nil, c.closedError
	}
	msgType, payload, err := c.inner.ReadFrame(ctx)
	if err != nil {
		return msgType, payload, err
	}
	if c.filter == nil {
		return msgType, payload, nil
	}
	updated, blocked, filterErr := c.filter(msgType, payload)
	if filterErr != nil {
		return msgType, payload, filterErr
	}
	if blocked != nil {
		if c.onBlock != nil {
			c.onBlock(blocked)
		}
		return msgType, nil, c.closeError(1008, blocked.Message, blocked)
	}
	return msgType, updated, nil
}

func (c *policyFrameConn) WriteFrame(ctx context.Context, msgType int, payload []byte) error {
	if c == nil || c.inner == nil {
		return c.closedError
	}
	if c.writeFilter != nil {
		updated, err := c.writeFilter(msgType, payload)
		if err != nil {
			return err
		}
		payload = updated
	}
	return c.inner.WriteFrame(ctx, msgType, payload)
}

func (c *policyFrameConn) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *clientFrameConn) ReadFrame(ctx context.Context) (int, []byte, error) {
	if c == nil || c.conn == nil {
		return TextFrame, nil, c.closedError
	}
	controlCtx := ctx
	if c.controlCtx != nil {
		controlCtx = c.controlCtx
	}
	msgType, payload, err := ReadClientMessageWithTimeoutStart(
		controlCtx,
		c.conn,
		c.interTurnIdleTimeout,
		1000,
		"websocket idle timeout",
		c.interTurnStarted,
		func() bool { return c.waitingForNextTurn.Load() },
	)
	var closeErr *ClientCloseError
	if errors.As(err, &closeErr) {
		err = c.closeError(closeErr.Status, closeErr.Reason, closeErr.Cause)
	}
	return msgType, payload, err
}

func (c *clientFrameConn) markTurnStarted() {
	if c != nil {
		TurnActivity{Waiting: &c.waitingForNextTurn, Started: c.interTurnStarted}.MarkStarted()
	}
}
func (c *clientFrameConn) markTurnCompleted() {
	if c != nil {
		TurnActivity{Waiting: &c.waitingForNextTurn, Started: c.interTurnStarted}.MarkCompleted()
	}
}

func (c *clientFrameConn) WriteFrame(ctx context.Context, msgType int, payload []byte) error {
	if c == nil || c.conn == nil {
		return c.closedError
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if msgType == TextFrame {
		if normalized, changed := c.normalizeCompleted(payload); changed {
			payload = normalized
		}
		if c.restoreResponseModel != nil {
			payload = c.restoreResponseModel(payload)
		}
		if c.restoreToolNames != nil {
			payload = c.restoreToolNames(payload)
		}
	}
	// 控制面取消必须由读路径发送带原因的关闭帧；若直接继承父取消，coder/websocket
	// 可能在当前帧已经到达客户端但 Write 尚未返回时硬关 TCP。这里保留原 deadline，
	// 仅让已经开始的写入完成，再由关闭握手统一结束连接。
	writeCtx := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		writeCtx, cancel = context.WithDeadline(writeCtx, deadline)
		defer cancel()
	}
	return c.conn.Write(writeCtx, msgType, payload)
}

func (c *clientFrameConn) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	_ = c.conn.Close(1000, "")
	_ = c.conn.CloseNow()
	return nil
}

// NewPolicyFrames 组合同步入站/出站过滤，供 HTTP 与协议 Adapter 复用。
func NewPolicyFrames(inner FrameConn, filter func(int, []byte) ([]byte, *PolicyBlocked, error), writeFilter func(int, []byte) ([]byte, error), onBlock func(*PolicyBlocked), closeError func(int, string, error) error, closedError error) FrameConn {
	return &policyFrameConn{inner: inner, filter: filter, writeFilter: writeFilter, onBlock: onBlock, closeError: closeError, closedError: closedError}
}

// TurnActivity 保留终态提交与下一轮空闲等待的同一个原子标记。
type TurnActivity struct {
	Waiting *atomic.Bool
	Started chan struct{}
}

func (a TurnActivity) MarkStarted() { a.Waiting.Store(false) }
func (a TurnActivity) MarkCompleted() {
	a.Waiting.Store(true)
	select {
	case a.Started <- struct{}{}:
	default:
	}
}
