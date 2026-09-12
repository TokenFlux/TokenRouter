// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	service "github.com/TokenFlux/TokenRouter/internal/service"
	team "github.com/TokenFlux/TokenRouter/internal/team"
)

// TeamNotifications 转接原邮件模板与发送语义，通知模块迁移时退出。
func TeamNotifications(email *service.EmailService, users service.UserRepository) team.Notifier {
	return service.NewTeamNotificationDelivery(email, users)
}
