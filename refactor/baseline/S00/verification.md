# S00 验证结果

基线 HEAD：1115294a9f69fcccd488c9a58006ebe875a32705。Go 构建/测试固定 GOTOOLCHAIN=go1.27.0；本地 golangci-lint 2.13.2（由 Go 1.27.0 编译），系统 Go 已由 Homebrew 更新为 1.27.1。工具证据见 [tools.json](tools.json) 与 [升级日志](logs/tool-upgrade.log)。

## 执行结果

通过/失败/跳过计数包含顶层测试和子测试，按 Go 事件中的唯一 Package + Test 去重。

| 验证 | 退出码 | 通过 / 失败 / 跳过 | 耗时秒 | 证据 |
| --- | --- | --- | --- | --- |
| 普通服务构建 | 0 | — | 20.0 | [build-normal](build-normal.result.json)；[日志](logs/build-normal.log) |
| 真实前端构建 | 0 | — | 28.86 | [build-frontend](build-frontend.result.json)；[日志](logs/build-frontend.log) |
| embed 服务构建 | 0 | — | 4.31 | [build-embed](build-embed.result.json)；[日志](logs/build-embed.log) |
| Linux amd64 服务构建 | 0 | — | 25.81 | [build-linux](build-linux.result.json)；[日志](logs/build-linux.log) |
| 升级后 lint 可用性 | 0 | — | 1.58 | [lint-smoke](lint-smoke.result.json)；[日志](logs/lint-smoke.log) |
| 普通测试 | 0 | 10978 / 0 / 5 | 179.13 | [test-normal](test-normal.result.json)；[日志](logs/test-normal.log.gz) |
| unit 测试 | 0 | 18963 / 0 / 9 | 206.47 | [test-unit](test-unit.result.json)；[日志](logs/test-unit.log.gz) |
| integration 基线 | 1 | 11601 / 37 / 9 | 146.97 | [test-integration](test-integration.result.json)；[日志](logs/test-integration.log.gz) |
| 普通 lint | 1 | — | 43.85 | [lint-normal](lint-normal.result.json)；[日志](logs/lint-normal.log) |
| unit lint | 1 | — | 62.96 | [lint-unit](lint-unit.result.json)；[日志](logs/lint-unit.log) |
| integration lint | 1 | — | 38.23 | [lint-integration](lint-integration.result.json)；[日志](logs/lint-integration.log) |
| 已有分组失败最小复现 | 1 | 0 / 3 / 0 | 6.45 | [test-integration-repro](test-integration-repro.result.json)；[日志](logs/test-integration-repro.log) |
| 新增退款原子性 | 0 | 3 / 0 / 0 | 7.23 | [test-refund-atomicity-final](test-refund-atomicity-final.result.json)；[日志](logs/test-refund-atomicity-final.log) |
| 新增退款定向 race | 0 | 3 / 0 / 0 | 48.85 | [test-refund-atomicity-race](test-refund-atomicity-race.result.json)；[日志](logs/test-refund-atomicity-race.log) |
| 新增测试所在仓储包 lint | 1 | — | 2.28 | [lint-refund-final](lint-refund-final.result.json)；[日志](logs/lint-refund-final.log) |

## 已有失败

### 分组协议约束

integration 出现 37 个失败条目（含 suite/子测试汇总），集中于 TestGroupRepoSuite 与 TestCreateGroupFromSourceRollsBackWhenOutboxInsertFails。最小复现使用独立测试进程/容器，仍在创建分组时触发 groups_protocol_policy_v1。

现有测试直接构造 service.Group 时省略 AllowedProtocols、ProtocolFallbacks、ResponsesImagePolicy；groupRepository.Create/Update 显式传递这些零值，而 migration 273 要求 JSON array/object 及有效图片策略。原有测试并未经过管理输入的规范化。CreateFromSource 的 outbox 回滚测试在触发预设 outbox 故障前已被该约束拒绝，因此不能声称 outbox 回滚已覆盖。

这是尚未改动的原始实现/测试基线问题；归 S03/S06 的协议与分组存储契约处理，不在 S00 修改运行行为、测试夹具或 SQL 约束。全部测试名及失败摘录见 [failures-and-skips.json](failures-and-skips.json)。

### lint

[lint-findings.json](lint-findings.json) 列出精确文件、行、规则及消息。
- lint-normal：2 项；staticcheck 2。
- lint-unit：68 项；depguard 6、errcheck 17、gofmt 1、gosec 1、govet 3、staticcheck 31、unused 9。
- lint-integration：10 项；errcheck 5、staticcheck 2、unused 3。

普通集合的两项为 domain/protocol_catalog.go 的 QF1003 与 service/account.go 的 QF1001。unit 集合另有 6 个既有 depguard 违规，不能在 S01 自动转成许可；其余按原文件归属阶段跟进。新增退款文件未产生 lint 诊断，仓储包剩余 7 项均为已有问题。

## 新增测试与初次夹具修正

原有退款测试只使用 SQLite/替身仓储。新增 payment_refund_atomicity_integration_test.go 通过公开 QueryAndFinalizeRefund、真实 userRepository 和 PostgreSQL 触发器，验证审计失败前扣款已在同事务可见，随后余额/订单/审计整体回滚；失败后重试仅扣一次，并发确认也只允许一个调用提交。

第一次运行中回滚场景通过，但第二个场景的建单因前一场景订单未被 users 的级联清理删除而失败。这是 S00 新测试的夹具问题，已增加订单/审计的精确清理；最终 integration 与定向 race 均通过。初次失败保存在 [初次结果](test-refund-atomicity.result.json)，不计入原有失败。

新增文件只在 integration 入选，普通/unit 集合未变，因此保留先前全量结果；新增测试后运行其 integration、race 和所在仓储包 lint，不重复无关全量测试。

## 跳过与环境边界

全部 Skip 及其原始原因列于 failures-and-skips.json。真实 Qoder/API/token 估算需显式测试凭据；TLS 捕获类测试缺少部分环境；integration 中已有 TestConcurrencyCacheSuite/TestGetAccountsLoadBatch 主动 Skip。跳过不算覆盖。

E2E 未执行：BASE_URL/CLAUDE_API_KEY/GEMINI_API_KEY 未配置，backend/scripts/e2e-test.sh 不存在。E2E 构建集合已核对，S16 跟进脚本/路径；datamanagement 源树不存在，未将其列为必过项。

本机 Darwin 和 Linux amd64 均完成服务构建；Linux 不支持 attestation 的行为尚无 Linux 运行证据。wireinject 只核对源集合，不执行生成器。前端构建只有大 chunk 的既有提示，没有提交 dist 或占位资源。

## 文档影响与漂移候选

S00 未改变运行架构、路由、持久化或锚点，Project Doc 正文保持当前结构。本次 54 个 Go 代码文档锚点全部能解析到稳定 ID，31 条构建/脚本/CI 引用见 [references.json](references.json)。

SubscriptionService.InvalidateSubCache 当前是空兼容方法，不能把它当作文档所述订阅缓存失效的证据；S04 应沿实际读取/失效路径核对文档和实现。已有分组测试与协议约束失配、E2E 脚本缺失均保留为基线限制。
