// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type TotpHandler = identityhttp.TotpHandler

// NewTotpHandler 委托所属模块的唯一实现。
func NewTotpHandler(totpService *service.TotpService) *TotpHandler {
	return identityhttp.NewTotpHandler(totpService)
}

type TotpStatusResponse = identityhttp.TotpStatusResponse

type TotpSetupRequest = identityhttp.TotpSetupRequest

type TotpSetupResponse = identityhttp.TotpSetupResponse

type TotpEnableRequest = identityhttp.TotpEnableRequest

type TotpDisableRequest = identityhttp.TotpDisableRequest

type TotpStepUpRequest = identityhttp.TotpStepUpRequest

type TotpStepUpResponse = identityhttp.TotpStepUpResponse
