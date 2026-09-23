//go:build integration

package app

// NewS16AccountTests 仅供外部集成测试调用真实组合根，不扩大生产 API。
var NewS16AccountTests = provideAccountTests

// 以下入口仅在集成测试中组合真实平台探测与应用关闭屏障。
var NewS16AntigravityRetry = provideAntigravityRetry
var NewS16AntigravityProbe = provideAntigravityProbe
var NewS16GatewayActivity = provideGatewayRequestActivity

// 快照回放测试复用生产装配与原 outbox 发布器。
var NewS16Snapshot = provideSchedulerSnapshot
var NewS16AccountEvents = newAccountEvents

// 集成测试直接使用原生装配及单向兼容绑定。
var NewS16AccountHealthRuntime = provideAccountHealthRuntime
var S16UpstreamHealth = provideUpstreamHealth

// 原生完成装配仅向隔离存储测试开放，不增加生产 API。
var NewS16CompletionRecorders = ProvideGatewayCompletionRecorders
var NewS16GatewayBillingRates = provideGatewayBillingRates

// 执行账号集成合同通过真实装配绑定相同存储，不扩大生产接口。
var NewS16ExecutionAccountStore = provideExecutionAccountStore

// 账号存储合同复用生产配置投影与事件绑定。
var NewS16AccountStore = provideAccountStore
