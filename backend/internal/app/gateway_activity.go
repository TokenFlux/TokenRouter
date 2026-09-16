package app

import "github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

// gatewayRequestActivity 让请求准入、平台尝试和完成入队共用一次关闭屏障。
// 停止不替换供应商请求 context，Qoder 尾部用量仍遵循原独立预算。
type gatewayRequestActivity struct{ *lifecycle.Operations }

func provideGatewayRequestActivity(manager *lifecycle.Manager) *gatewayRequestActivity {
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("GatewayRequestsAndAttempts")}
	manager.Register(lifecycle.Hook{Name: "GatewayRequestsAndAttempts", StopOrder: 15, Stop: activity.StopContext})
	return activity
}
