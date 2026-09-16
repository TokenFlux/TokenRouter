// 通知只接收已确认的事件和技术发送端口。
package notification

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/notification/contract"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

type SettingRepository = settings.Repository
type Setting = settings.Setting

var ErrSettingNotFound = settings.ErrSettingNotFound

type NotificationEmailSendInput = SendRequest
type Sender interface {
	SendEmail(context.Context, string, string, string) error
}
type SMTPConfig = contract.SMTPConfig
type SMTPTransport interface {
	Send(context.Context, *SMTPConfig, string, string, string) error
	Test(context.Context, *SMTPConfig) error
}

const defaultSiteName = "Sub2API"
const SettingKeySiteName = "site_name"
const SettingKeyAPIBaseURL = "api_base_url"
const SettingKeyFrontendURL = "frontend_url"
