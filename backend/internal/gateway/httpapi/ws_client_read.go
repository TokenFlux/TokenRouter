package httpapi

import (
	"context"
	"errors"
	"time"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	coderws "github.com/coder/websocket"
)

// ReadOpenAIWSClientMessage 将实际连接交给网关读循环，并转换客户端关闭帧错误。
func ReadOpenAIWSClientMessage(ctx context.Context, conn *coderws.Conn, timeout time.Duration, status coderws.StatusCode, reason string) (coderws.MessageType, []byte, error) {
	return ReadOpenAIWSClientMessageWithTimeoutStart(ctx, conn, timeout, status, reason, nil, nil)
}
func ReadOpenAIWSClientMessageWithTimeoutStart(ctx context.Context, conn *coderws.Conn, timeout time.Duration, status coderws.StatusCode, reason string, start <-chan struct{}, active func() bool) (coderws.MessageType, []byte, error) {
	var input gatewayws.ClientConn
	if conn != nil {
		input = WSClientFrames{Conn: conn}
	}
	typ, body, err := gatewayws.ReadClientMessageWithTimeoutStart(ctx, input, timeout, int(status), reason, start, active)
	var closeErr *gatewayws.ClientCloseError
	if errors.As(err, &closeErr) {
		err = NewOpenAIWSClientCloseError(coderws.StatusCode(closeErr.Status), closeErr.Reason, closeErr.Cause)
	}
	return coderws.MessageType(typ), body, err
}
