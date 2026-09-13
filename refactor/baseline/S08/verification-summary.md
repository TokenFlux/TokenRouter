# S08 验证摘要

项目命令使用 Go1.27.0，lint2.13.2；普通/unit/integration 全量测试及收尾命令串行执行。完整命令、耗时、退出码、日志位置见 [verification-summary.json](verification-summary.json)。通过数为含父子测试的事件数，非唯一测试数；不与定向重复执行结果相加。

| 全量验证 | 退出码 | 实际通过 / 跳过 |
| --- | ---: | --- |
| [final-normal](final-normal.result.json) | 0 | 11338 / 4 |
| [final-unit](final-unit.result.json) | 0 | 19377 / 8 |
| [final-integration](final-integration.result.json) | 0 | 12251 / 4 |
| [final-embed-test](final-embed-test.result.json) | 0 | 101 / 0 |
| 前端测试 | 0 | 14 个文件、199 个测试通过 |
| 常规 / embed / Linux / 两个维护命令构建 | 0 | 可构建；不计行为测试 |
| Wire 生成与重复生成 | 0 | SHA256 完全一致 |

最后补入的审计 HTTP 契约只修改 `audit/httpapi/middleware_test.go`，三种集合定向 race 各 15 条通过事件、lint 均为 0。生产代码与门禁未改变，全量测试计数保留原实际结果，不虚增为最终重复执行。

## 契约行为索引

[contract-matrix.json.gz](contract-matrix.json.gz)按约定验证面索引实际事件；[逐符号账本](ownership-ledger.json.gz)提供测试声明、构建选择与生产引用。下表说明这些证据的实际范围。

| 验证面 | 实际证据与限制 |
| --- | --- |
| B01—B05 | [fixed-issues](fixed-issues.md)列出原失败复现和修复后测试；三项真实 PG race，加两类 writer/取消 race；没有新历史修复 |
| 写事实与资金 | usage/postgres 原 insert/batch/duplicate/cancel/deadlock/事务测试；旧 usage_billing 的同步 FK 锁与 outbox/退款原子测试继续执行；日志失败不再次扣款。B05 PG 测试断言余额未变 |
| 查询 / 展示 | 用户、管理员及公共 usage 140 条定向事件；原 request_type、team、付款/行为、账号成本隐藏、请求模型/内部模型、精确总数、分页排序、历史 DTO 与 CSV 规则随新 handler 执行 |
| 聚合 / 清理 | 原 raw/小时/日、覆盖缺口、UTC/时区、回填游标与重算测试随 Store/核心迁移；B01 的两种交错与 B05 部分取消使用 PostgreSQL；清理结束端点不统一 |
| 查询形状 | [query-shape-comparison](query-shape-comparison.json)：4/8 用户、4/8 Key 均 1 SQL，排行 1 SQL；同 8 用户/Key、32 行 fixture Explain 节点不变，仅代表该规模 |
| 缓存 | `TestQueryCacheSnapshotIsolation`、`TestQueryCacheConcurrentLoadReturnsIndependentValues`；原 snapshot/ETag/TTL 测试；Dashboard 旧 raw Redis key 与 update cache 真实 Redis 断言 |
| 审计 | 原中间件请求体恢复/遗漏敏感体/脱敏；新增 `TestS08AuditClearHTTPAuthorizationAndTrace` 验证 TOTP 现场调用在 ClearWithTrace 之前、管理员 Key 拒绝、错误映射、同步失败、成功跳过异步重复留痕；数据库回滚由真实 PG 另验，不用替身证明原子性 |
| Ops | 原告警/静默/邮件失败、报告去重、设置刷新/回滚、清理/分类、系统日志 2–60s 退避与恢复、错误队列容量/字节预算/停止；本地 GitHub HTTP fixture 和历史 ingress PostgreSQL dry-run/execute |
| 生命周期 | [进程级验证](s08-process-observation-contracts.result.json)10 条通过，含 standard/simple 真正 SIGTERM、观测 Hook 单次执行及 Redis/Ent 最后关闭；原 stop/idle/queue 与新 B03/B04 race 共同验证 |
| 定向 race | [usage 缓存](usage-query-cache-unit-race-2.result.json)、[Ops 状态](ops-runtime-unit-race.result.json)、[真实存储/缓存/查询](query-cache-read-contracts-integration-race.result.json)均通过；没有全仓 race/benchmark |
| 依赖方向 | [dependency-fixtures](dependency-fixtures.json)14 次 positive/negative 全部符合预期，覆盖 normal/unit/integration/wireinject/embed/e2e/Darwin/Linux；夹具已移除。普通、嵌套新文件和非法子包不能继承旧许可 |
| 文件选择 | [build-selections](build-selections.json.gz)7 组 go list，214 包（e2e215），无包加载错误；OS/标签编译仅作选择证据 |

## 基线 lint 与限制

全量 lint 为 **1 / 285 / 17**，退出码均为 1。[逐诊断比较](lint-comparison.json)按原文件映射、规则和完整消息核对，新增为零。原六项 unit depguard 违规仍保留。两个 integration errcheck 因旧接口构造改为具体 `*Store` 返回而不再需要 unchecked 类型断言，自然消失；原测试断言没有删除，未增加忽略规则。

[skips.json](skips.json)保留完整跳过原因，[skip-comparison.json](skip-comparison.json)确认与 S07 完全一致：normal4、unit8、integration4。涉及 Qoder 真实账号/本地凭据、OpenAI API key、外部 TLS、SQLite 无法表达历史 PostgreSQL NULL 行，以及原非 response.create WS fixture。无测试文件/仅编译的包不算行为通过。

真实供应商 E2E、硬件 Passkey 和外部 TLS 不在本地验收环境，沿用前阶段限制；未触碰生产凭据。PostgreSQL `postgres:18.1-alpine3.23`、Redis `redis:8.4-alpine` 的隔离容器负责本阶段真实存储测试。前端已有 Vue fixture 与 chunk 大小提示原样归档，不开展额外修复。

中间迁移过程中失败的 compile/test/lint 记录全部保留在同目录；它们是迭代证据，已由后续成功定向和最终结果覆盖，不标记为既有生产问题。规划目录的预期失败与最终成功日志分别归档。
