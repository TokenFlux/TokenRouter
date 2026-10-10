// Package app 构造 TokenRouter 的服务实例，连接 HTTP 入口、存储与后台任务，并登记进程启停顺序。
//
// 阅读入口：
//   - application.go：初始化应用，启动服务并等待资源关闭。
//   - wire.go：声明完整应用的依赖注入入口。
//   - http_routes.go：组合认证、用户、网关和支付路由。
//
// 文件分组：
//   - assembly_*.go：按业务模块登记 Wire 构造函数和接口绑定。
//   - gateway_*.go、qoder_*.go：装配请求准入、平台执行、响应输出和完成记录。
//   - provider_*.go：装配提供商管理、凭据刷新、健康状态和用量查询。
//   - identity_*.go、apikey*.go、team.go：绑定身份、API Key 与团队服务。
//   - billing.go、payment_*.go、routing_*.go：装配资金、支付、分组和价格读取。
//   - tasks.go、task_storage.go、creative_*.go、batch_image_*.go：装配持久任务、创作台和批量图片服务。
//   - settings_*.go、site_public.go、http_*.go：绑定运行时设置、公开站点数据和 HTTP 入口。
//   - storage.go、scheduler*.go、runtime*.go：构造共享资源和登记后台服务启停。
//   - ops*.go、observability_foundation.go、usage*.go、notification.go：装配监控、审计、用量和通知。
package app
