// 串行队列 HTTP 兼容入口复用唯一实现。
package handler

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

type UserMsgQueueHelper = gatewayhttp.UserMsgQueueHelper

func NewUserMsgQueueHelper(s *scheduler.UserMessageQueueService, f gatewayhttp.SSEPingFormat, d time.Duration) *UserMsgQueueHelper {
	return gatewayhttp.NewUserMsgQueueHelper(s, f, d)
}
