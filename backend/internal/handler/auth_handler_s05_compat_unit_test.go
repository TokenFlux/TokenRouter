//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package handler

import (
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func captchaProof(turnstileToken, tencentTicket, tencentRandstr string) service.CaptchaProof {
	return service.CaptchaProof{
		TurnstileToken: turnstileToken,
		TencentTicket:  tencentTicket,
		TencentRandstr: tencentRandstr,
	}
}
