// FrameConn 保留帧种类和同步读写；升级、认证及进程监听由 HTTP Adapter 拥有。
package upstream

import "context"

type FrameKind int

const (
	FrameText   FrameKind = 1
	FrameBinary FrameKind = 2
)

type FrameConn interface {
	ReadFrame(context.Context) (FrameKind, []byte, error)
	WriteFrame(context.Context, FrameKind, []byte) error
	Close() error
}
