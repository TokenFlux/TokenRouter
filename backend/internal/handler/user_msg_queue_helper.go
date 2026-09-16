// 串行队列 HTTP 兼容入口复用唯一实现。
package handler

import (
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type UserMsgQueueHelper = gatewayhttp.UserMsgQueueHelper

func NewUserMsgQueueHelper(s *service.UserMessageQueueService, f SSEPingFormat, d time.Duration) *UserMsgQueueHelper {
	return gatewayhttp.NewUserMsgQueueHelper(s, f, d)
}
