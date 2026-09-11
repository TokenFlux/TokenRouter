# S04 验证摘要

所有项目命令使用 `GOTOOLCHAIN=go1.27.0`，本地 golangci-lint 2.13.2。实际命令、退出码、时长及日志位置记录于各 `.result.json`；初始工具版本见 [工具记录](tools.json)。中间失败用于追踪实施修正，不混入最终交付基线。

## 全量验证

| 集合 | 通过事件 | 失败 | 跳过 | 命令结果 |
| --- | ---: | ---: | ---: | --- |
| normal | 11064 | 0 | 5 | [final-test-normal](final-test-normal.result.json) |
| unit | 19049 | 0 | 9 | [final-test-unit-serial](final-test-unit-serial.result.json) |
| integration | 11769 | 0 | 6 | [validation-integration](validation-integration.result.json) |

事件包含父测试与子测试，不含包级汇总。跳过不计行为通过。首轮 unit 的两个 SQLite 行锁用例保留原事务身份断言、通过窄事务替身测试；真实锁和回滚另由 PostgreSQL 验证。一次并行 normal/unit 触发 Ent schema 加载器共享临时目录冲突，未修改 Ent 实现，串行重跑 unit 已完整通过。

| lint 集合 | S03 | S04 | 新增 | 实际诊断 |
| --- | ---: | ---: | ---: | --- |
| normal | 1 | 1 | 0 | [delivery-lint-normal](delivery-lint-normal.json) |
| unit | 285 | 285 | 0 | [delivery-lint-unit](delivery-lint-unit.json) |
| integration | 19 | 19 | 0 | [delivery-lint-integration](delivery-lint-integration.json) |

lint 退出码均为 1，表示这些原有诊断仍然存在，没有宣称 lint 全部通过。按文件、规则和完整消息比较见 [lint-comparison.json](lint-comparison.json)；唯一搬迁路径对应原额度 HTTP 测试中的空分支。六项原 unit depguard 违规保持不变，未扩大忽略规则。

## 资金与行为契约

| 验证面 | 已取得证据 |
| --- | --- |
| 普通结算及金额 | 原 `TestUsageBillingRepository*` 的去重、冲突、owner/actor、三种模式、溢出、8/10 位精度、账号独立成本及 outbox 回滚经唯一新 SQL/纯分配实现执行；普通/unit/integration 全量与 [真实事务](allocation-real-transactions.result.json)通过。 |
| 外层事务与兑换 | `TestS04SubscriptionParticipantReadsUncommittedAndRollsBack` 用未提交用户/套餐证明相同连接；`TestS04RedeemEffectsWaitForCommit` 对余额、并发、订阅分别在真实 usage 插入后失败，确认全部回滚且没有提前失效认证。[结果](entitlement-real-transactions-fixed.result.json)。 |
| 套餐/订阅与并发 | 来源订单、时间链、撤销/恢复/Key 改绑、窗口及套餐 null 语义的原测试继续运行。`TestS04ConcurrentSubscriptionExtensionsUseLockedState` 与 `TestS04ValidityChangeParticipatesInOuterTransaction` 保留旧 HEAD 失败，修复后通过。[结果](entitlement-concurrency-fixed.result.json)。 |
| 任务资金 | BatchImage/Creative 的历史快照、严格预占、捕获/释放及 allowance 测试继续通过旧投影调用 Funds；真实 PG 的任务/Key/成员与资金更新维持单次提交。[结果](allocation-real-transactions.result.json)。 |
| 单实例额度竞争 | flusher 与重置、singleflight 回填、两种重置顺序、异步镜像、多个 flusher、配置删除、取消及拒绝入队后释放锁。[扩展交错](quota-coordination-expanded.result.json)、[缺失缓存回填](quota-after-reset-fixed.result.json)。不声明多进程协调或跨存储原子提交。 |
| HTTP/JSON/幂等/CSV | 原用户/管理员 handler、导出、重放和 DTO 测试保留断言并命中新入口；`TestBillingPlanHTTPPreservesEntJSON` 经实际新 handler 对照空值、显式零值及完整 Ent JSON。[套餐 JSON](final-billing-http-json.result.json)。 |
| 生命周期 | 订阅维护构造不启动、重复启动、取消等待和阻塞预算通过；进程测试证明资金 cache/flusher/expiry 各只有一次启停，停止先于 Redis。standard/simple、真实 SIGTERM、setup/CLI/AUTO_SETUP、监听失败及版本注入实际运行。[进程结果](final-test-process.result.json)。 |
| 未迁流程 | 注册默认权益、支付履约、Promo、返利及退款原测试继续通过；`TestPaymentRefundPostgresAtomicity` 保留公开退款入口和审计失败整体回滚。[真实事务](allocation-real-transactions.result.json)。 |
| 定向 race | [核心缓存/队列/维护](final-core-race.result.json)、[真实 PostgreSQL/Redis 竞争](final-storage-race.result.json)、[旧缓存/倍率入口](final-compat-race.result.json)均通过，未运行全仓 race 或 benchmark。 |

逐个实际测试事件见 [contract-events.json.gz](contract-events.json.gz)，分类用于检索，同一事件可以支持多项契约。

## 构建、装配与门禁

- [final-build](final-build.result.json)：`make build`，退出码 0。
- [final-test-frontend](final-test-frontend.result.json)：`make -C .. test-frontend`，退出码 0。
- [final-build-frontend](final-build-frontend.result.json)：`make -C .. build-frontend`，退出码 0。
- [final-build-embed](final-build-embed.result.json)：`go build -tags=embed -o bin/server-embed ./cmd/server`，退出码 0。
- [final-build-linux](final-build-linux.result.json)：`env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/server-linux-amd64 ./cmd/server`，退出码 0。
- [final-build-maintenance](final-build-maintenance.result.json)：`go build ./cmd/jwtgen ./cmd/cleanup-ingress-reject-logs`，退出码 0。
- [final-test-embed](final-test-embed.result.json)：`go test -count=1 -json -tags=embed ./internal/web/... ./internal/server/... ./internal/app/...`，退出码 0。
- [final-wire](final-wire.result.json)：`go generate ./cmd/server`，退出码 0。
- [final-wire-reproducible](final-wire-reproducible.result.json)：`go generate ./cmd/server`，退出码 0。

前端 lint/typecheck 和 14 个测试文件、199 个测试通过；真实前端产物生成后执行 embed 注入测试。Linux 交叉构建只算可构建性证据。Wire 重复生成前后摘要一致，见 [Wire 摘要](wire-reproducible.json)。本阶段未运行 Ent 生成。

[构建选择](build-selections.json)覆盖普通、unit、integration、wireinject、embed、e2e 和 Linux，均没有选择错误；实际行为仍以 JSON 测试事件为准。

[depguard 夹具](dependency-fixtures.json)使用完整现行配置的临时模块，六组正例均零诊断，反例精确命中 19/20 项，missing/unexpected 均为空。覆盖合法 Adapter、旧装配、同目录新增文件、迁出例外、旧文件新 import、非法子包、核心反向依赖以及新套餐 HTTP 测试的精确许可；临时模块已删除。原 service/handler 禁止规则未改写，见 [依赖规则](dependency-rules.json)。

## 归档、限制与后续

五项有旧 HEAD 复现的相关缺陷及修复范围见 [迁移清单](migration-map.md)。所有原始失败、夹具准备失败、修复回归与最终验证分开保留；不会把失败编译、整体跳过或无匹配测试算作行为通过。

已有跳过逐项记录在 [verification-limitations.json](verification-limitations.json)：真实 Qoder/OpenAI API、个人本地 auth、外部 TLS 网络测试需要显式环境；DingTalk sentinel、非 response.create WS fixture、历史 NULL 会话 SQLite fixture 和已有并发缓存 sentinel 继续保持原跳过。真实供应商 E2E 服务地址/密钥及缺失的 e2e-test.sh 沿既有限制，后续所属阶段与 S16 跟踪。

PostgreSQL/Redis 测试通过 Testcontainers 隔离容器执行。镜像与本次选择依据见 [容器记录](test-images.json)。受影响资金事务及单实例竞争没有环境待验项。

[十二组资金账本](funding-writes.md)与[文件/消费者清单](file-ownership.json.gz)明确保留身份/团队/Key、账号路由、usage、通知、平台网关、支付推广、任务及初始化恢复的 S05—S16 职责，不能据此宣布全项目资金写入已收敛。

源文件、SQL migration、Ent、旧冻结资料、其他任务文件与暂存区的最后核对见 [完整性记录](integrity.json)。阶段计划原文哈希保持不变；新增资料与代码、roadmap 和 Wire 差异可审查，不自动提交。
