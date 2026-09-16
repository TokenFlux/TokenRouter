# S11 验收证据与限制

S11 约定的行为测试、构建和门禁已完成；已登记跳过及外部环境限制继续保留。以下记录区分最终结果、固定修复和中间失败。事件数量包含父子测试，集合重叠不可相加。

## 固定修复

[规划原实现问题与复现](planning/fixed-issues.json)保持冻结；[修复后实际事件索引](fixed-regression-evidence.json)只纳入命令成功的结果。B06 另保留并行任务的原始 race JSON。

| 问题 | 当前回归测试 | 关键行为 |
| --- | --- | --- |
| B01 | TestStopWaitsForSynchronousOverflow | 停止等待已接受的内联任务；预算超时不算成功 |
| B02 | TestStartAfterStopDoesNotCreateWorker | Stop 不可逆，禁止重建扩缩容 worker |
| B03 | TestRuleUpdateCannotBeOverwrittenByOlderLoad | 同一实例串行协调旧回源和新管理发布 |
| B04 | TestRuleSnapshotOwnership | 输入、编译和返回规则的嵌套值互不污染 |
| B05 | TestStopCancelsStartupRuleLoad、TestStopCancelsSubscriptionRuleLoad | 运行 context 覆盖启动及订阅回源 |
| B06 | TestSidebandCancelledAfterClaimReleasesWithoutTarget | 取消后归还控制权，不再查执行目标或拨号 |

B01/B02 不增加持久完成队列；B03 不增加分布式协调、版本列或通知协议。Live 继续只记录零费用用量。

## 行为与集成

- [真实进程关闭](gateway-process-shutdown.result.json)：standard/simple 各执行启动及 SIGTERM，共 3 条父子事件通过，核对新请求屏障、完成器和共享依赖顺序。
- [固定 Execute 的真实存储链](fixed-execute-storage-race.result.json)：14 条通过，包含 Qoder 本地上游 + PostgreSQL/Redis 的结算/记录/重放/取消，以及错误规则存储和订阅。
- [会话真实存储 race](session-state-storage-race-final.result.json)：24 条通过。
- [请求生命周期 race](gateway-scope-race-reconciled.result.json)：确认已进入的请求/attempt 被等待，停止后禁止新操作。
- [入口契约矩阵](entry-contract-matrix.md)：区分路由覆盖清点与实际本地场景验证；未调用生产供应商作影子推理。

## 迁移中间失败

保留编译中间态、fixture、depguard 和最终回归的原日志；它们不是历史基线，也不计通过。

- 删除旧 Qoder 例外后，漏清普通规则中的精确排除项。首次夹具捕获后同步修正规则和排除项，[后续 57 项](depguard-fixtures.json)全部匹配预期。
- 首轮全量 unit 在 CN 显式零价候选断言失败，[原结果](final-unit.result.json)保留；S11 的完成投影调用会回写旧 Key 的兼容判断，替换了调用方分组引用。改为必要字段的只读投影，保留金额算法和原断言；新增输入引用/显式零价回归。最终重跑结果另列。
- 约定的消费者 race 集合偶然触发已登记的 Gin 全局 SetMode 夹具竞争。保留当次失败；按独立顶层测试补验，保留内部并发与所有断言。没有开展清单外历史修复或扩大故障注入。

## 环境与保证边界

真实供应商、外部 TLS 捕获、硬件及 E2E 限制沿用 S10。普通/unit/integration 的跳过逐项记录，不算行为通过；跨平台 build 只证明可构建。S07 outbox 周期重建恢复边界保持。没有新增邮件/任务恢复、Live 收费或资金提交保证。

完整文件、类型引用、接口实现、构建选择和精确例外分别由[迁移账本](migration-ledger.md)链接。S00—S10、SQL、Ent 与其他任务文件由[保护摘要](protected-final.json)核对。

## 最终全量测试

| 集合 | 命令结果 | 通过事件 | 跳过事件 | 失败 |
| --- | --- | --- | --- | --- |
| ordinary | [重跑结果](final-normal-recheck.result.json) / [事件](final-normal-recheck.events.json.gz) | 11,749 | 4 | 0 |
| unit | [重跑结果](final-unit-recheck.result.json) / [事件](final-unit-recheck.events.json.gz) | 19,773 | 8 | 0 |
| integration，包并发 4 | [结果](final-integration.result.json) / [事件](final-integration.events.json.gz) | 12,698 | 5 | 0 |

三组按顺序单独执行，没有降低测试内部并发。[跳过明细](final-skips.json)保留完整原因。integration 比前阶段多出一个[现有业务日时间条件跳过](usage-time-conditional-skip.json)：对应 usage 测试源码未变、S10 有通过事件；本阶段当前 Honolulu 业务日完整小时不足，不能计作通过，也未扩展修复范围。

## 构建、门禁与资料

- [三组完整 lint 对比](final-lint-comparison.json)：normal/unit/integration 为 **1 / 284 / 17**；按路径、规则、完整消息逐项相同，新增 0、移除 0。lint 命令因原有诊断退出 1，不能写成 lint 零错误；未增加忽略规则。
- [普通服务](final-build.result.json)、[JWT 工具](final-build-jwtgen.result.json)、[清理工具](final-build-cleanup-ingress-reject-logs.result.json)、[embed](final-build-embed.result.json)、[Linux amd64](final-build-linux.result.json)均构建成功；[版本输出](final-version.result.json)成功。
- [前端测试](frontend-test.result.json)、[真实前端构建](frontend-build.result.json)、[embed 行为测试](embed-ui-test.result.json)成功；embed 行为为 101 条通过事件。产物和构建结果不代替完整真实供应商行为。
- [Wire 重生成](final-wire-repeat.result.json)、[幂等摘要](wire-idempotence.json)确认再次生成无差异；未执行 Ent 生成。
- [八组构建选择](build-selection-results.json)、[类型/符号加载](final-symbol-summary.json)、[逐文件清单](final-migration-files.json.gz)已刷新。578 个 Go 文件（400 新增、14 删除），三种类型集合均无加载诊断。
- [最后一轮精确许可清理](final-gate-cleanup.json)记录 19 项失效 import 和 1 条冗余规则/排除项的删除；最终普通/unit/integration 的 [57 项夹具](depguard-fixtures.json)全部匹配预期，夹具已删除。

全部命令、退出码和日志位置汇总于[最终验证数据](final-validation.json)。构建使用项目 Go 1.27.0；日志/测试事件保存压缩原始输出，失败及跳过不计通过。

[最终保护核对](protected-final.json)：6,646 项原文件未变，原有 50 个未跟踪文件保持；计划正文前 17,012 字节摘要不变。HEAD/main 和空索引保持，源码与最终清单摘要一致。SQL、Ent 与 S00—S10 冻结资料无意外变化。文档链接/锚点和新增/已有文件 diff 检查见[链接结果](final-doc-links.json)、[差异检查](diff-check.json)。未自动提交。
