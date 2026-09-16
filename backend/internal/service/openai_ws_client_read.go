package service

import (
	"context"
	"errors"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	coderws "github.com/coder/websocket"
)

// ReadOpenAIWSClientMessage 委托网关读循环，保留旧关闭错误类型。
func ReadOpenAIWSClientMessage(ctx context.Context, conn *coderws.Conn, timeout time.Duration, status coderws.StatusCode, reason string) (coderws.MessageType, []byte, error) {
	return readOpenAIWSClientMessageWithTimeoutStart(ctx, conn, timeout, status, reason, nil, nil)
}
func readOpenAIWSClientMessageWithTimeoutStart(ctx context.Context, conn *coderws.Conn, timeout time.Duration, status coderws.StatusCode, reason string, start <-chan struct{}, active func() bool) (coderws.MessageType, []byte, error) {
	var input gatewayws.ClientConn
	if conn != nil {
		input = gatewayhttp.WSClientFrames{Conn: conn}
	}
	typ, body, err := gatewayws.ReadClientMessageWithTimeoutStart(ctx, input, timeout, int(status), reason, start, active)
	var closeErr *gatewayws.ClientCloseError
	if errors.As(err, &closeErr) {
		err = NewOpenAIWSClientCloseError(coderws.StatusCode(closeErr.Status), closeErr.Reason, closeErr.Cause)
	}
	return coderws.MessageType(typ), body, err
}
