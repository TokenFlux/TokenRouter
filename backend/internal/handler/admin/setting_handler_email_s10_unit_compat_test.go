//go:build unit

// 保留原标签测试的私有委托，生产代码不保留测试专用入口。
package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func resolveSMTPUseTLS(v *bool, c *service.SMTPConfig) bool { return httpapi.ResolveSMTPUseTLS(v, c) }
