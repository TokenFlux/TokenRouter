# 运维文档目录

> 上级目录：[工程文档](../index.md)

## 范围

本分类记录构建部署、数据库演进、运行观测、数据维护、安全入口和仓库开发流程。面向最终部署者的逐命令指南可以作为相关资料存在，但不自动成为 Project Doc 成员。

## 文档

- [部署与数据库迁移](deployment_and_migrations.md)：构建产物、运行方式、首次初始化、迁移约束和升级恢复边界。读取时机：修改 Docker/二进制发布、启动装配、数据库迁移、备份或升级时读取。
- [可观测性与数据生命周期](observability_and_data_lifecycle.md)：日志、Ops、Usage、审计、聚合、清理和备份的数据面总览与专题路由。读取时机：判断数据所有权、留存、备份范围或进入详细观测专题前读取。
- [账号维护](account_maintenance.md)：凭据刷新、临时不可调度、账号测试、自动恢复、额度和能力探测。读取时机：修改 token refresh、账号状态、计划测试、quota/endpoint capability 探测或恢复策略时读取。
- [上游传输安全](upstream_transport_security.md)：代理生命周期、连接池隔离、TLS 指纹路由、目标/重定向和 Header 安全。读取时机：修改代理、HTTP client、TLS profile/router、base URL 或直连回退时读取。
- [运维监控与告警](ops_monitoring_and_alerting.md)：Ops 信号、实时/历史查询、告警规则、静默、邮件通知和计划报告。读取时机：修改 Ops collector、dashboard、错误采集、告警评估或报告任务时读取。
- [开发、验证与上游同步](development_workflow.md)：生成代码、测试分层、前后端工具链、发布和 fork 同步规则。读取时机：准备开发环境、改 schema/依赖、运行验证、发布或同步上游时读取。
- [边缘与 HTTP 入口安全](edge_security.md)：长连接条件下的入口限制、可信代理、流式传输和边缘防护边界。读取时机：修改 HTTP server、反向代理、请求限制、客户端 IP 解析、SSE 或 WebSocket 时读取。
- [使用记录与运维预聚合](pre_aggregation.md)：Usage 与运维查询的预聚合、回填、降级和清理规则。读取时机：修改聚合任务、Usage/仪表盘查询路由、时间边界或历史回填时读取。
