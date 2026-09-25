package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

// provideOpenAIAuxiliary 复用 app 已构造的请求、输出、授权和后台观察实例。
func provideOpenAIAuxiliary(text *gatewayhttp.OpenAITextExecutor, authorization *account.OpenAIAuthorization, activity *gatewayRequestActivity) *gatewayhttp.OpenAIAuxiliary {
	executor := &gatewayhttp.OpenAIAuxiliary{Requests: text.Requests, Output: text.Output, CodexUsage: text.CodexUsage, Authorization: authorization, Enter: activity.Enter}
	return executor
}
