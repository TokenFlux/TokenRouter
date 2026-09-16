// 邮件队列的状态与实现由 notification 唯一持有。
package service

import "github.com/TokenFlux/TokenRouter/internal/notification"

type EmailQueueService = notification.EmailQueueService
type EmailTask = notification.EmailTask

const TaskTypeVerifyCode = notification.TaskTypeVerifyCode
const TaskTypePasswordReset = notification.TaskTypePasswordReset

func NewEmailQueueService(s *EmailService, workers int) *EmailQueueService {
	return notification.NewEmailQueueService(s, workers)
}
