package ws

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// TextFrame 保留 WebSocket 文本帧枚举，不依赖具体网络库。
const TextFrame = 1

// FrameConn 提供同步读写和关闭，具体协议连接属于 Adapter。
type FrameConn interface {
	ReadFrame(context.Context) (int, []byte, error)
	WriteFrame(context.Context, int, []byte) error
	Close() error
}

var ErrFirstOutputTimeout = errors.New("openai websocket passthrough first output timeout")
var ErrActiveTurnTimeout = errors.New("openai websocket passthrough active turn read timeout")

type deadlinePhase uint8

const (
	deadlinePhaseFirstSemantic deadlinePhase = iota + 1
	deadlinePhaseActiveRead
)

type Deadline struct {
	Timeout         time.Duration
	StartedAt       time.Time
	RequestModel    string
	ReasoningEffort string
	phase           deadlinePhase
}

type FirstOutputTimeoutError struct {
	Deadline Deadline
}

func (e *FirstOutputTimeoutError) Error() string {
	return ErrFirstOutputTimeout.Error()
}

func (e *FirstOutputTimeoutError) Unwrap() error {
	return ErrFirstOutputTimeout
}

type ActiveTurnTimeoutError struct{}

func (e *ActiveTurnTimeoutError) Error() string {
	return ErrActiveTurnTimeout.Error()
}

func (e *ActiveTurnTimeoutError) Unwrap() error {
	return ErrActiveTurnTimeout
}

type firstOutputDeadlineState struct {
	armed      bool
	generation uint64
	deadline   Deadline
}

type DeadlineConn struct {
	closedError       error
	inner             FrameConn
	resolveDeadline   func(payload []byte) Deadline
	activeReadTimeout time.Duration

	mu              sync.Mutex
	state           firstOutputDeadlineState
	deadlineChanged chan struct{}
}

func (c *DeadlineConn) ReadFrame(ctx context.Context) (int, []byte, error) {
	if c == nil || c.inner == nil {
		return TextFrame, nil, c.connectionClosedError()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	type readResult struct {
		msgType int
		payload []byte
		err     error
	}
	readCtx, cancelRead := context.WithCancel(ctx)
	readResultCh := make(chan readResult, 1)
	go func() {
		msgType, payload, err := c.inner.ReadFrame(readCtx)
		readResultCh <- readResult{msgType: msgType, payload: payload, err: err}
	}()

	var timer *time.Timer
	var timerCh <-chan time.Time
	resetTimer := func() {
		state := c.deadlineState()
		if timer != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		if !state.armed || state.deadline.Timeout <= 0 {
			timerCh = nil
			return
		}
		remaining := time.Until(state.deadline.StartedAt.Add(state.deadline.Timeout))
		if remaining < 0 {
			remaining = 0
		}
		if timer == nil {
			timer = time.NewTimer(remaining)
		} else {
			timer.Reset(remaining)
		}
		timerCh = timer.C
	}
	resetTimer()

	defer func() {
		cancelRead()
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case result := <-readResultCh:
			if result.err == nil {
				c.observeUpstreamActivity(result.msgType, result.payload)
			}
			return result.msgType, result.payload, result.err
		case <-c.deadlineChanged:
			resetTimer()
		case <-timerCh:
			state := c.deadlineState()
			if !state.armed || state.deadline.Timeout <= 0 || time.Now().Before(state.deadline.StartedAt.Add(state.deadline.Timeout)) {
				resetTimer()
				continue
			}
			if ctx.Err() != nil {
				cancelRead()
				<-readResultCh
				return TextFrame, nil, ctx.Err()
			}
			cancelRead()
			<-readResultCh
			if state.deadline.phase == deadlinePhaseActiveRead {
				return TextFrame, nil, &ActiveTurnTimeoutError{}
			}
			return TextFrame, nil, &FirstOutputTimeoutError{Deadline: state.deadline}
		case <-ctx.Done():
			cancelRead()
			<-readResultCh
			return TextFrame, nil, ctx.Err()
		}
	}
}

func (c *DeadlineConn) WriteFrame(ctx context.Context, msgType int, payload []byte) error {
	if c == nil || c.inner == nil {
		return c.connectionClosedError()
	}
	generation := uint64(0)
	if msgType == TextFrame && strings.TrimSpace(gjson.GetBytes(payload, "type").String()) == "response.create" {
		generation = c.armDeadline(payload)
	}
	if err := c.inner.WriteFrame(ctx, msgType, payload); err != nil {
		c.disarmDeadline(generation)
		return err
	}
	return nil
}

func (c *DeadlineConn) Close() error {
	if c == nil || c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *DeadlineConn) armDeadline(payload []byte) uint64 {
	if c == nil || c.resolveDeadline == nil {
		return 0
	}
	deadline := c.resolveDeadline(payload)
	if deadline.Timeout <= 0 {
		return 0
	}
	if deadline.StartedAt.IsZero() {
		deadline.StartedAt = time.Now()
	}
	deadline.phase = deadlinePhaseFirstSemantic
	c.mu.Lock()
	c.state.generation++
	generation := c.state.generation
	c.state.armed = true
	c.state.deadline = deadline
	c.mu.Unlock()
	c.notifyDeadlineChanged()
	return generation
}

func (c *DeadlineConn) observeUpstreamActivity(msgType int, payload []byte) {
	if c == nil {
		return
	}
	if msgType == TextFrame && IsTerminalOutput(payload) {
		c.disarmDeadline(0)
		return
	}
	state := c.deadlineState()
	if state.armed && state.deadline.phase == deadlinePhaseActiveRead {
		c.armActiveReadDeadline()
		return
	}
	if msgType == TextFrame && StartsSemanticOutput(payload) {
		c.armActiveReadDeadline()
	}
}

func (c *DeadlineConn) armActiveReadDeadline() {
	if c == nil {
		return
	}
	if c.activeReadTimeout <= 0 {
		c.disarmDeadline(0)
		return
	}
	c.mu.Lock()
	c.state.generation++
	c.state.armed = true
	c.state.deadline = Deadline{
		Timeout:   c.activeReadTimeout,
		StartedAt: time.Now(),
		phase:     deadlinePhaseActiveRead,
	}
	c.mu.Unlock()
	c.notifyDeadlineChanged()
}

func (c *DeadlineConn) disarmDeadline(generation uint64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if !c.state.armed || (generation != 0 && generation != c.state.generation) {
		c.mu.Unlock()
		return
	}
	c.state.armed = false
	c.mu.Unlock()
	c.notifyDeadlineChanged()
}

func (c *DeadlineConn) deadlineState() firstOutputDeadlineState {
	if c == nil {
		return firstOutputDeadlineState{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

func (c *DeadlineConn) notifyDeadlineChanged() {
	if c == nil || c.deadlineChanged == nil {
		return
	}
	select {
	case c.deadlineChanged <- struct{}{}:
	default:
	}
}

func StartsSemanticOutput(payload []byte) bool {
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	switch eventType {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	case "", "response.created", "response.in_progress", "response.output_item.added", "response.output_item.done":
		return false
	}
	return strings.Contains(eventType, ".delta") ||
		strings.HasPrefix(eventType, "response.output_text") ||
		strings.HasPrefix(eventType, "response.output")
}

func IsTerminalOutput(payload []byte) bool {
	switch strings.TrimSpace(gjson.GetBytes(payload, "type").String()) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}

// NewDeadlineConn 逐会话构造首语义输出和活跃读预算，不在空闲 turn 期间计时。
func NewDeadlineConn(inner FrameConn, activeReadTimeout time.Duration, resolve func([]byte) Deadline, closedError error) *DeadlineConn {
	return &DeadlineConn{inner: inner, activeReadTimeout: activeReadTimeout, resolveDeadline: resolve, closedError: closedError, deadlineChanged: make(chan struct{}, 1)}
}

// connectionClosedError 保持空连接也返回错误，旧调用方注入原错误身份。
func (c *DeadlineConn) connectionClosedError() error {
	if c != nil && c.closedError != nil {
		return c.closedError
	}
	return errors.New("openai ws connection closed")
}
