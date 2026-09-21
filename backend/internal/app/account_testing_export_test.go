//go:build integration

package app

// NewS16AccountTests 仅供外部集成测试调用真实组合根，不扩大生产 API。
var NewS16AccountTests = provideAccountTests

// NewS16AccountRecovery 仅供原生健康装配的真实存储验证，不进入生产 API。
var NewS16AccountRecovery = provideAccountRecovery

// 以下入口仅在集成测试中组合真实平台探测与应用关闭屏障。
var NewS16AntigravityRetry = provideAntigravityRetry
var NewS16AntigravityProbe = provideAntigravityProbe
var NewS16GatewayActivity = provideGatewayRequestActivity
