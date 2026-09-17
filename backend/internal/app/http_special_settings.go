package app

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// providePreAggregationHTTP 直接注入原生任务实例，保持原管理入口的权限与时序。
func providePreAggregationHTTP(settings *preaggregation.PreAggregationSettingsService, usage *usage.DashboardAggregationService, ops *ops.OpsAggregationService) *settingshttp.PreAggregationHandler {
	return settingshttp.NewPreAggregationHandler(settings, usage, ops)
}

// provideCreativeSettingsHTTP 直接读取唯一创作运行时与模型目录。
func provideCreativeSettingsHTTP(public *creative.Public, worker *creative.CreativeWorkerRuntime) *creativehttp.SettingsHandler {
	return creativehttp.NewSettingsHandler(public, worker.Status)
}
