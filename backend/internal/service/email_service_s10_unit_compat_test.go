//go:build unit

// 保留原标签测试的私有委托，生产代码不保留测试专用入口。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/notification"
)

func (s *EmailService) buildVerifyCodeEmailBody(code, site string) string {
	return notification.VerifyCodeBody(code, site)
}

func (s *EmailService) buildPasswordResetEmailBody(url, site string) string {
	return notification.PasswordResetBody(url, site)
}
