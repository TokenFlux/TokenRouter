// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/notification"

const NotificationEmailEventAuthVerifyCode = native.NotificationEmailEventAuthVerifyCode
const NotificationEmailEventAuthPasswordReset = native.NotificationEmailEventAuthPasswordReset
const NotificationEmailEventNotificationEmailVerifyCode = native.NotificationEmailEventNotificationEmailVerifyCode
const NotificationEmailEventTeamInvitation = native.NotificationEmailEventTeamInvitation
const NotificationEmailEventSubscriptionPurchaseSuccess = native.NotificationEmailEventSubscriptionPurchaseSuccess
const NotificationEmailEventSubscriptionExpiryReminder = native.NotificationEmailEventSubscriptionExpiryReminder
const NotificationEmailEventBalanceLow = native.NotificationEmailEventBalanceLow
const NotificationEmailEventBalanceRechargeSuccess = native.NotificationEmailEventBalanceRechargeSuccess
const NotificationEmailEventAccountQuotaAlert = native.NotificationEmailEventAccountQuotaAlert
const NotificationEmailEventContentModerationViolation = native.NotificationEmailEventContentModerationViolation
const NotificationEmailEventContentModerationDisabled = native.NotificationEmailEventContentModerationDisabled
const NotificationEmailEventOpsAlert = native.NotificationEmailEventOpsAlert
const NotificationEmailEventOpsScheduledReport = native.NotificationEmailEventOpsScheduledReport

type NotificationEmailService = native.NotificationEmailService
type NotificationEmailEventInfo = native.NotificationEmailEventInfo
type NotificationEmailTemplate = native.NotificationEmailTemplate
type NotificationEmailPreview = native.NotificationEmailPreview
type NotificationEmailPreviewInput = native.NotificationEmailPreviewInput
type NotificationEmailSendInput = native.NotificationEmailSendInput
type NotificationEmailUnsubscribeResult = native.NotificationEmailUnsubscribeResult

func NewNotificationEmailService(repo SettingRepository, email *EmailService) *NotificationEmailService {
	if email == nil {
		return native.NewNotificationEmailService(repo, nil)
	}
	n := native.NewNotificationEmailService(repo, email)
	if email != nil {
		email.SetNotificationEmailService(n)
	}
	return n
}

func shouldFallbackNotificationEmail(err error) bool {
	return native.ShouldFallbackNotificationEmail(err)
}

func renderNotificationEmail(event, subject, html string, variables, raw map[string]string) (NotificationEmailPreview, error) {
	return native.RenderNotificationEmail(event, subject, html, variables, raw)
}
