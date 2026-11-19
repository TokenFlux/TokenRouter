# 请求 ID 与请求查询

本文记录统一请求标识、上游标识采集、请求摘要内存批写、计费键去重及管理员请求查询规则。用量预聚合见[使用记录与运维预聚合](pre_aggregation.md)，迁移步骤见[版本升级说明](upgrade_notes.md#request_id_unification)。

## 章节导航

- [请求标识](#request_identity)：本地 UUID 生成、响应头保护与外部别名规则。
- [标识来源采集](#request_identifier_sources)：调用方 ID、上游请求头与响应 ID 的采集与归属。
- [记录与内存批写](#request_storage)：摘要字段、批写退避、停机排空与留存配置。
- [计费兼容](#billing_identity)：`request_id` 与 `billing_key` 的去重和历史兼容。
- [查询与权限](#request_query)：分阶段索引查找、执行计划回归与管理员页面。

<a id="request_identity"></a>
## 请求标识

- 生成与响应头：全局 HTTP 入口生成本地 UUID，网关复用并在提交正文、刷新和连接升级前强制写回 `X-Request-ID`、`X-TokenRouter-Request-ID` 和兼容头 `X-Sub2API-Request-ID`（浏览器经 CORS 可读），防止上游响应头覆盖本地 ID。
- 调用方别名：客户端传入的 `X-Request-ID` 与 `X-Client-Request-ID` 存为调用方别名；合法 `X-Client-Request-ID` 回显，缺失时回显本地 ID。别名仅用于查询，不绕过权限校验。
- 重试、WS 与异步任务：同一次 HTTP 请求的提供商重试/切换共用入口 ID；WS 每轮生成独立请求 ID（同轮重试复用，并关联连接请求 ID）；异步任务从稳定业务键派生统一 ID，重放得同值。

<a id="request_identifier_sources"></a>
## 标识来源采集

请求查询区分三类外部标识：调用方请求 ID（客户端请求头）、上游请求 ID（上游响应头）和响应 ID（协议解析结果，如 Responses `response.id`）。缺少分类证据的历史 `upstream` 别名标为「类型未记录」，不按前缀猜测。

- 上游请求头规则：提供商未配 `upstream_request_id_header` 时默认读上游 `X-Request-ID`；配置后仅读指定头，缺失或为空时不记录也不回退。`usage_logs.upstream_request_id` 沿用同一规则，修剪并按 UTF-8 安全截断至 128 字节（WS 轮次为空）。
- 逐次尝试隔离：每次上游派发固定提供商、尝试编号与头名，响应头到达及流解析到 `response.id` 时立即记录；后续空观测不覆盖，延迟响应按派发引用更新所属尝试。无上游响应的认证失败记为未编号记录，与已派发尝试分开展示。
- 媒体、WS 桥接与计数：
  - 图片/视频双向转换保留原派发引用；CDN 图片下载不新增模型尝试；视频状态查询与内容下载分次记录。
  - WS 转 HTTP 使用传输层派发编号，HTTP 内部重试重新计时（不混入前次被拒等待），整轮总耗时仍从轮次起点起算。
  - 发往上游的 token 计数在读体校验结束时记尝试终态；上游拒绝后改走本地估算仍保留失败尝试，纯本地估算不新增上游尝试。

<a id="request_storage"></a>
## 记录与内存批写

`requestlog` 记录所有到达应用中间件的请求摘要（含成功、拒绝、失败、取消、未完成、健康检查与 404）；应用层之前的网络失败无记录。

### 摘要内容与性能口径

- `request_records` 保存起止时间、状态、路径、认证归属、模型、失败阶段诊断、上游尝试与关联标识；完整 prompt/报文遵循各业务既有留存规则。
- 客户端断连即固定已观测结束时间，后台继续读 usage 不延长该生命周期。
- 正常请求的性能保存在 `usage_logs.timings` 并按尝试编号关联展示；无 usage 的鉴权/准入/连接失败将阶段计时写入 `failure_timings`。
- `usage_write_state` 区分待写入、确认写入、确认未写入、跳过与结果未知，写入自身异常属于诊断，不改写请求的失败或取消终态。
- `running` 表示尚未落终态（执行中或异常退出中断）；创作台与批量图片详情直接联查任务表状态；Grok 视频轮询在计费认领前按 `done`/`failed`/`expired`/取消同步摘要终态。

### 批写参数、失败退避与留存

| 机制 | 参数或行为 |
| --- | --- |
| 内存缓冲与批写 | 每 20ms 批写最多 256 条，缓冲上限 4096（同 ID 按版本合并）；满时调用侧同步写库；入队前清理空字符并按 UTF-8 截断 |
| 失败退避 | 每次写超时 5 秒；批写失败同窗口逐条重试。JSON 错误或 PG `SQLSTATE 22` 确认不可存后丢弃并累计故障；其余错误保留快照并将批写间隔拉长至 1 秒，成功后恢复 |
| 停机与健康 | 正常关机等待同步写并排空缓冲；停机后新快照同步写。`/api/v1/admin/requests/health` 暴露待写数与累计故障；DB 不可用时按本地 ID 可查本机待写摘要 |
| 留存配置 | `request_log.retention_days`（默认 30 天，0 取默认，修改需重启）；独立清理任务管理，备份随 Ops 日志选项控制 |

<a id="billing_identity"></a>
## 计费兼容

- 新使用记录的 `request_id` 存统一请求 ID，`billing_key` 存资金去重值（账本与去重表的既有 `request_id` 仍指计费键，外部别名不替代计费键）。
- 使用记录按 `(COALESCE(billing_key, request_id), api_key_id)` 去重，兼容历史 `billing_key` 为空的行，保证任务跨版本重放共用同一条用量。
- `client:`、`local:`、`generated:` 等历史前缀由查询兼容读取，不靠字符串相似度猜测映射。

<a id="request_query"></a>
## 查询与权限

- 接口与角色：管理员通过 `GET /api/v1/admin/requests?request_id=...` 或 `/api/v1/admin/requests/:request_id` 查询（校验管理员角色，单次最多返回 100 条并带 `has_more`），系统模型请求可按父测试 ID 关联多次尝试。普通用户仅可按请求 ID 筛选自己的使用记录并复制 ID，请求 ID 本身不授访问权。
- 分阶段索引查找：`request_lookup_ids` 优先按主键精确命中本地 ID（命中时不展开同名外部别名），未命中时再查调用方、计费键、上游 ID 与历史日志，收集候选数组供摘要、用量、错误、审计（使用 `(request_id, created_at DESC)` 索引）共享 `= ANY(...)` 查询。父请求同步解析关联子记录的计费键与历史引用。
- 执行计划约束：详情用量查询按请求 ID、上游 ID、父 ID 三个分支先取轻量化候选并去重、过滤权限、统一排序截断最新 101 行后再回表，避免表达式索引缺统计时退化为时间索引大范围扫描；回归测试用 `auto_explain.log_nested_statements` 验证实际扫描行数。
- 管理端展示：`/admin/requests` 按总耗时刻度绘制失败阶段与各用量性能时间线，批量回查 `provider_names`（含软删除提供商）。使用记录、错误、系统日志、审计和审核列表均支持按请求 ID 筛选（筛选时忽略日期范围、隐藏趋势图，汇总与导出沿用同一筛选）。
