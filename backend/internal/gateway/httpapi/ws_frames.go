package httpapi

import (
	"context"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	coderws "github.com/coder/websocket"
)

// WSClientFrames 只转换网络库的帧与关闭枚举，不实施 turn 或重试规则。
type WSClientFrames struct{ Conn *coderws.Conn }

var _ gatewayws.ClientSocket = WSClientFrames{}

func (c WSClientFrames) Read(ctx context.Context) (int, []byte, error) {
	typ, body, err := c.Conn.Read(ctx)
	return int(typ), body, err
}

// Write 同步反馈网络写入结果，供原生入站执行器保持原关闭与取消边界。
func (c WSClientFrames) Write(ctx context.Context, typ int, body []byte) error {
	return c.Conn.Write(ctx, coderws.MessageType(typ), body)
}

func (c WSClientFrames) Close(status int, reason string) error {
	return c.Conn.Close(coderws.StatusCode(status), reason)
}
func (c WSClientFrames) CloseNow() error { return c.Conn.CloseNow() }
