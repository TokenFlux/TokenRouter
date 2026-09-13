package handler

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// UserMsgQueueHelper 用户消息串行队列 Handler 层辅助
// 复用 ConcurrencyHelper 的退避 + SSE ping 模式
type UserMsgQueueHelper struct {
	queueService *service.UserMessageQueueService
	pingFormat   SSEPingFormat
	pingInterval time.Duration
}

// NewUserMsgQueueHelper 创建用户消息串行队列辅助
func NewUserMsgQueueHelper(
	queueService *service.UserMessageQueueService,
	pingFormat SSEPingFormat,
	pingInterval time.Duration,
) *UserMsgQueueHelper {
	if pingInterval <= 0 {
		pingInterval = defaultPingInterval
	}
	return &UserMsgQueueHelper{
		queueService: queueService,
		pingFormat:   pingFormat,
		pingInterval: pingInterval,
	}
}

// AcquireWithWait 等待获取串行锁，流式请求期间发送 SSE ping
// 返回的 releaseFunc 内部使用 sync.Once，确保只执行一次释放
func (h *UserMsgQueueHelper) AcquireWithWait(c *gin.Context, accountID int64, baseRPM int, isStream bool, streamStarted *bool, timeout time.Duration, reqLog *zap.Logger) (func(), error) {
	lease, err := h.queueService.AcquireWithWait(c.Request.Context(), accountID, baseRPM, timeout, h.observer(c, accountID, isStream, streamStarted, reqLog))
	if err != nil {
		return nil, err
	}
	return lease.Release, nil
}

// observer 保留请求日志字段与 SSE 格式，等待和资源所有权由 scheduler 处理。
func (h *UserMsgQueueHelper) observer(c *gin.Context, accountID int64, isStream bool, started *bool, reqLog *zap.Logger) scheduler.QueueObserver {
	return scheduler.QueueObserver{
		Wait: gatewayWaitObserver(c, h.pingFormat, h.pingInterval, isStream, started, false),
		Event: func(name string, id int64, err error) {
			if err != nil {
				reqLog.Warn(name, zap.Int64("account_id", id), zap.Error(err))
			} else {
				reqLog.Debug(name, zap.Int64("account_id", id))
			}
		}, Delay: func(delay time.Duration) {
			reqLog.Debug("gateway.umq_throttle_delay", zap.Int64("account_id", accountID), zap.Duration("delay", delay))
		},
	}
}

// ThrottleWithPing 保留 HTTP 入口，核心执行软限速等待。
func (h *UserMsgQueueHelper) ThrottleWithPing(c *gin.Context, accountID int64, baseRPM int, isStream bool, streamStarted *bool, timeout time.Duration, reqLog *zap.Logger) error {
	return h.queueService.ThrottleWithWait(c.Request.Context(), accountID, baseRPM, timeout, h.observer(c, accountID, isStream, streamStarted, reqLog))
}
