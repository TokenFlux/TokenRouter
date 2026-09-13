// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
)

const AccountSchedulingThresholdReasonSource = account.AccountSchedulingThresholdReasonSource

type AccountSchedulingThresholdReasonInput = account.AccountSchedulingThresholdReasonInput

// BuildTempUnschedReasonPayload 委托唯一原因格式规则。
func BuildTempUnschedReasonPayload(source string, errorMessage string) string {
	return account.BuildTempUnschedReasonPayload(source, errorMessage)
}

// BuildAccountSchedulingThresholdReason 委托唯一原因格式规则。
func BuildAccountSchedulingThresholdReason(errorMessage string) string {
	return account.BuildAccountSchedulingThresholdReason(errorMessage)
}

// BuildDetailedAccountSchedulingThresholdReason 委托唯一原因格式规则。
func BuildDetailedAccountSchedulingThresholdReason(input AccountSchedulingThresholdReasonInput) string {
	return account.BuildDetailedAccountSchedulingThresholdReason(input)
}

// IsAccountSchedulingThresholdReason 委托唯一原因格式规则。
func IsAccountSchedulingThresholdReason(rawReason string) bool {
	return account.IsAccountSchedulingThresholdReason(rawReason)
}
