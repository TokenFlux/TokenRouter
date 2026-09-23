//go:build wireinject

package app

import (
	"github.com/google/wire"
)

// notification 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var notificationAssemblyProviders = wire.NewSet(
	provideRiskDelivery,
	provideAlertDelivery,
	provideMailer,
	provideNotification,
	provideEmailQueue,
	provideNotificationHTTP,
)
