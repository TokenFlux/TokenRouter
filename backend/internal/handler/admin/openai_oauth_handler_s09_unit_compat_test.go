//go:build unit

// 原行为测试继续经过相同转接；生产消费者清零后仅保留 unit 兼容。
package admin

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
)

const openAIQuotaResetWarningCacheRefreshFailed = accounthttp.OpenAIQuotaResetWarningCacheRefreshFailed

const openAIQuotaResetWarningAccountRecoveryFailed = accounthttp.OpenAIQuotaResetWarningAccountRecoveryFailed

type openAIQuotaResetResponse = accounthttp.OpenAIQuotaResetResponse

type openAIQuotaRefreshResponse = accounthttp.OpenAIQuotaRefreshResponse
