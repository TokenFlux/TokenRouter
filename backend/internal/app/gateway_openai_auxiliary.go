package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideOpenAIAuxiliary 复用 app 已构造的请求、输出、授权和后台观察实例。
func provideOpenAIAuxiliary(source *service.OpenAIGatewayService, authorization *account.OpenAIAuthorization, activity *gatewayRequestActivity) *gatewayhttp.OpenAIAuxiliary {
	executor := &gatewayhttp.OpenAIAuxiliary{Requests: source.Requests, Output: source.Text.Output, CodexUsage: source.Text.CodexUsage, Authorization: authorization, Enter: activity.Enter}
	source.Auxiliary = executor
	return executor
}
