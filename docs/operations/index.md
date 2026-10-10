# 运维文档目录

> 上级目录：[工程文档](../index.md)

## 范围

本分类记录构建和部署、数据库演进、运行观测、数据维护、入口安全和仓库的开发流程。给部署者的逐条命令手册在 `guides/`，作为相关资料引用，不属于 Project Doc。

## 文档

- [部署与数据库迁移](deployment_and_migrations.md)：构建产物、运行方式、首次初始化、迁移机制，以及通用的升级、备份和回退规则。读取时机：修改 Docker 或二进制发布、启动装配、数据库迁移、备份或升级流程时读取。
- [版本升级说明](upgrade_notes.md)：记录模型目录变化，并按迁移编号列出需要特别处理的升级：停机要求、缓存版本变化、验证和回退方式，并标出已被取代的专题。读取时机：升级跨越多个版本、编写新的破坏性迁移，或核对某个历史字段的来龙去脉时读取。
- [可观测性与数据生命周期](observability_and_data_lifecycle.md)：日志、Ops、Usage、审计、聚合、清理和备份的总览，以及到详细文档的入口。读取时机：判断数据归属、留存、备份范围，或进入观测的详细文档之前读取。
- [请求 ID 与请求查询](request_lookup.md)：统一请求标识、内存批写、外部别名、计费兼容和请求详情。读取时机：修改请求 ID、记录、搜索、详情权限或请求记录留存时读取。
- [提供商维护](provider_maintenance.md)：凭据刷新、管理操作、临时不可调度、提供商测试、自动恢复、额度和能力探测、OAuth 用量查询。读取时机：修改 token refresh、提供商状态、计划测试、quota 和 endpoint capability 探测或恢复策略时读取。
- [上游传输安全](upstream_transport_security.md)：代理生命周期、连接池隔离、TLS 指纹路由、目标和重定向校验、Header 安全。读取时机：修改代理、HTTP client、TLS profile 和 router、base URL 或直连回退时读取。
- [运维监控与告警](ops_monitoring_and_alerting.md)：Ops 信号、实时和历史查询、告警规则、静默、邮件通知和计划报告。读取时机：修改 Ops collector、dashboard、错误采集、告警评估或报告任务时读取。
- [开发、验证与上游同步](development_workflow.md)：工具链、依赖规则、编码约定、后端文件组织、生成代码、测试分层、发布和 fork 同步。读取时机：准备开发环境、修改 schema 或依赖、新增或改名后端文件、运行验证、发布或同步上游时读取。
- [边缘与 HTTP 入口安全](edge_security.md)：长连接场景下的入口限制、可信代理、流式传输，以及应用和网络边缘在 DDoS 防护上的分工。读取时机：修改 HTTP server、反向代理、请求限制、客户端 IP 解析、SSE 或 WebSocket 时读取。
- [使用记录与运维预聚合](pre_aggregation.md)：Usage 和运维查询的预聚合、回填、降级和清理规则。读取时机：修改聚合任务、Usage 和仪表盘的查询路由、时间桶或历史回填时读取。
