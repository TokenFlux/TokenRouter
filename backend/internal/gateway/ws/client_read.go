package ws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// ClientConn 让读循环在关闭帧发出后仍能等待唯一的读取任务结束。
type ClientConn interface {
	Read(context.Context) (int, []byte, error)
	Close(int, string) error
	CloseNow() error
}

// ClientCloseError 描述 HTTP Adapter 必须发送的关闭帧，不依赖 WebSocket 库。
type ClientCloseError struct {
	Status int
	Reason string
	Cause  error
}

func (e *ClientCloseError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return fmt.Sprintf("openai ws client close: %d %s", e.Status, strings.TrimSpace(e.Reason))
	}
	return fmt.Sprintf("openai ws client close: %d %s: %v", e.Status, strings.TrimSpace(e.Reason), e.Cause)
}
func (e *ClientCloseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
func NewClientCloseError(status int, reason string, cause error) *ClientCloseError {
	return &ClientCloseError{Status: status, Reason: strings.TrimSpace(reason), Cause: cause}
}

type clientReadResult struct {
	messageType int
	payload     []byte
	err         error
}

// ReadClientMessage 在控制事件发送关闭帧期间保留唯一读协程，
// 随后强制关闭底层连接并等待读协程退出。
func ReadClientMessage(
	controlCtx context.Context,
	conn ClientConn,
	timeout time.Duration,
	timeoutStatus int,
	timeoutReason string,
) (int, []byte, error) {
	return ReadClientMessageWithTimeoutStart(
		controlCtx,
		conn,
		timeout,
		timeoutStatus,
		timeoutReason,
		nil,
		nil,
	)
}

// ReadClientMessageWithTimeoutStart 支持在状态转换后才开始计时，
// 例如 passthrough 一轮完成后等待下一轮。timeoutActive 为 nil 时，正数超时会立即起算。
func ReadClientMessageWithTimeoutStart(
	controlCtx context.Context,
	conn ClientConn,
	timeout time.Duration,
	timeoutStatus int,
	timeoutReason string,
	timeoutStart <-chan struct{},
	timeoutActive func() bool,
) (int, []byte, error) {
	if conn == nil {
		return 0, nil, errors.New("openai websocket client connection is nil")
	}
	if controlCtx == nil {
		controlCtx = context.Background()
	}

	readDone := make(chan clientReadResult, 1)
	go func() {
		messageType, payload, err := conn.Read(context.Background())
		readDone <- clientReadResult{messageType: messageType, payload: payload, err: err}
	}()

	var timer *time.Timer
	var timeoutCh <-chan time.Time
	startTimeout := func() {
		if timeout <= 0 || (timeoutActive != nil && !timeoutActive()) {
			return
		}
		if timer == nil {
			timer = time.NewTimer(timeout)
		} else {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)
		}
		timeoutCh = timer.C
	}
	if timeoutActive == nil || timeoutActive() {
		startTimeout()
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	closeAndJoin := func(status int, reason string, cause error) (int, []byte, error) {
		_ = conn.Close(status, reason)
		_ = conn.CloseNow()
		<-readDone
		return 0, nil, NewClientCloseError(status, reason, cause)
	}

	for {
		select {
		case result := <-readDone:
			return result.messageType, result.payload, result.err
		case <-timeoutStart:
			startTimeout()
		case <-timeoutCh:
			return closeAndJoin(timeoutStatus, timeoutReason, context.DeadlineExceeded)
		case <-controlCtx.Done():
			cause := context.Cause(controlCtx)
			if errors.Is(cause, scheduler.ErrOpenAIWSIngressLeaseLost) {
				return closeAndJoin(
					1013,
					"websocket ingress capacity lease lost; please reconnect",
					cause,
				)
			}
			return closeAndJoin(1001, "websocket request canceled", cause)
		}
	}
}
