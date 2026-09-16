package live

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// TextFrame 保留文本帧类型；HTTP Adapter 负责与具体 WebSocket 库转换。
const TextFrame = 1

// FrameConn 是控制连接的同步帧端口，不拥有 HTTP 升级或协议库。
type FrameConn interface {
	ReadFrame(context.Context) (int, []byte, error)
	WriteFrame(context.Context, int, []byte) error
	SetReadLimit(int64)
	Close() error
}

// Target 只提供已校验执行账号的供应商连接和路由投影能力。
// 账号凭据始终留在适配层，不进入会话记录或公共输出。
type Target interface {
	Dial(context.Context) (FrameConn, error)
	Rewrite(context.Context, []byte) ([]byte, string, []string, error)
}

// Ports 提供会话、调度租约及生命周期能力；不持有数据库或 HTTP 客户端。
type Ports interface {
	Store() (session.LiveCallStore, error)
	Leases() (scheduler.LiveConcurrencyCache, error)
	Target(context.Context, *session.LiveCallRecord) (Target, error)
	BeginObserver(string) (context.Context, func(), bool)
	RecordZeroUsage(context.Context, *session.LiveCallRecord, int)
}

// Service 拥有 Live 控制权、续租、到期及 observer 编排。
// 可变会话和运行登记由同一应用实例提供，构造本身不启动任务。
type Service struct {
	ports         Ports
	retryInterval time.Duration
	readLimit     int64
}

// New 使用原来的观察重试和消息大小配置，不创建另一份缓存。
func New(ports Ports, retryInterval time.Duration, readLimit int64) *Service {
	return &Service{ports: ports, retryInterval: retryInterval, readLimit: readLimit}
}

func (s *Service) dial(ctx context.Context, record *session.LiveCallRecord) (FrameConn, error) {
	target, err := s.ports.Target(ctx, record)
	if err != nil {
		return nil, err
	}
	return target.Dial(ctx)
}
