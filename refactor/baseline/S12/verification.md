# S12 验证记录

代码迁移、B01—B06 和相关装配已接入生产路径。S12 提交后，用户授权针对 R01 复现和最小修复；[修复与补验](r01-refresh-stop/README.md)通过，唯一待验项已关闭。所有中间失败均保留，不用后续通过覆盖原事实。

## 固定修复与行为证据

| 项目 | 主要证据 | 结果 |
| --- | --- | --- |
| B01/B02 | `TestPaymentS12ExpiryRepeatedStart`、`StartAfterStop`、`StopCancelsQuery`；原生 `TestOrderExpiryConstructionAndCancellation`、`StopBudgetReportsUnfinished` | 精确服务 race 与原生包 race 通过；[本地 HTTP 渠道取消链 race 通过](expiry-channel-race.result.json)，未进入下一阶段 |
| B03 | `TestPaymentS12ProvidersFailedInitialLoadRetries`、`FailedRefreshPreservesPublished`、`TestRegistryReplacePublishesOnlyCompleteMap` | 失败可重试、旧表保留和无中间空表通过 |
| B04 | `TestS12StaleRefundFailureOverwritesSuccess` | PostgreSQL/race 保持最终 REFUNDED，不被迟到失败覆盖 |
| B05 | `TestPaymentRefundPostgresAtomicity`、S12 prepared/pending/success 审计失败、恢复、缺失/损坏/矛盾记录与重复尝试用例 | 真实 PostgreSQL/race 通过；渠道不持事务，不重复预扣，不自动重发 |
| B06 | `TestAffiliateCapConcurrentAccrualUsesLockedLatestTotal` | 两个调用在加锁前汇合，上限 10 时实际累计为 10 |
| Promo/返利转余额 | Promo usage/次数失败触发器；`TestAffiliateTransferLedgerFailureRollsBackFunds` | 同连接余额/累计充值/次数/流水回滚通过 |
| HTTP | 原支付/管理/微信/Webhook 契约，以及 `TestRefundRecoveryErrorIsVisibleToAdministrator` | 新旧 DTO 与原断言通过；人工核实错误保留明确 reason/message |
| 运行模式与关闭 | `TestS02ProcessModes` 的 standard/simple、SIGTERM，以及 S12 支付→任务→邮件→Redis/Ent 断言 | 最终代码全量 integration 通过 |

原实现预期失败见 [规划记录](planning/fixed-issues.json)。退款记录沿用原 `(order_id, action)` 唯一约束，后续动作版本将旧事实归档到 `history`；新增真实约束回归，不更改 migration/Ent。旧 pending 的显式不扣减选择仍优先，缺字段不能按 false 解释。

## 执行结果

| 集合 | 结果/证据 |
| --- | --- |
| 普通全量 | 原 [11,751 pass / 1 fail / 4 skip](final-normal.result.json) 保留；R01 修复后 [11,754 pass / 0 fail / 4 skip](r01-refresh-stop/results.json) |
| 当前完整 unit | [19,782 pass / 0 fail / 8 skip](current-full-unit.result.json) |
| 当前完整 integration | [12,722 pass / 0 fail / 4 skip](current-full-integration.result.json) |
| 精确旧服务消费者 race | [按迁移文件选择 256 个入口](service-race-selection.json)，[451 pass / 0 fail / 0 skip](exact-service-race.result.json) |
| 新模块 race | [353 pass / 0 fail / 0 skip](native-modules-race.result.json) |
| 最终资金 PostgreSQL race | [29 pass / 0 fail / 0 skip](refund-journal-contract-race.result.json) |
| 依赖门禁 | [51 个夹具全部匹配](depguard-fixtures.json)，普通/unit/integration，临时文件已删除 |
| 全量 lint | [1 / 284 / 17，与 S11 逐文件/规则/消息一致](final-lint-comparison.json)，新增/移除均为 0 |
| 后端普通构建 | [通过](backend-build.result.json) |
| 前端测试/产物 | [lint/typecheck + 14 文件 199 测试通过](frontend-test.result.json)；[构建通过](frontend-build.result.json) |
| embed/Linux | [embed 构建](embed-build.result.json)、[Linux amd64 构建](linux-build.result.json)通过；[真实产物测试 106 pass](real-embed-test.result.json) |
| 精简命令 | [jwtgen](jwtgen-build.result.json)、[cleanup-ingress-reject-logs](cleanup-command-build.result.json)构建通过 |

Go JSON 事件数包含父子测试，集合重叠，不能相加。编译、跳过和真实行为验证分别记录。lint 退出码仍为 1，对应原有诊断；没有增加忽略规则。移出的无生产消费者的私有转接先归入单测兼容，再删除实际无单测消费者部分，见 [清单](test-only-compatibility.json)。

## 清单外观察与限制

- **R01（已关闭）**：[账号刷新原观察](unplanned-account-refresh-observation.json)保留。用户明确授权后，在原 HEAD 相关文件 overlay 下确定性复现，并增加取得账号锁后的停止屏障检查；[原失败、修复及补验](r01-refresh-stop/README.md)独立归档。原断言不变，未扩展排查。
- **R02（仅登记，S13 规划输入）**：[误匹配的创作测试 race](unplanned-creative-race-observation.json)。一条带 `Provider` 的宽泛匹配选中了非 S12 的创作并行测试，其替身报告竞争。误选命令的失败保留；未额外复现/修复，随后使用迁移文件明确列出的测试入口。
- 沿用真实供应商/硬件/外部 TLS/E2E 限制；没有真实付款或退款。S07 outbox 周期重建恢复边界与单服务进程范围不变。
- 退款必要事实不能删除。降级前须处理或登记未完成退款，不得仅回退二进制后盲目重发渠道退款。

## 收尾完整性

- [Wire 再生成无差异](wire-idempotence.json)。普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 的 [go list 选择](build-selection-results.json)全部退出 0；跨平台选择和构建不计作跨平台行为执行。
- [normal](final-symbol-references-normal.json.gz)、[unit](final-symbol-references-unit.json.gz)、[integration](final-symbol-references-integration.json.gz) 静态符号/接口实现/消费者清点均无诊断；反射和字符串引用不冒充完整静态调用图，Wire、生命周期名及文档另见账本。
- [完整性](final-integrity.json)：7,852 个受保护文件无变，原 50 个无关未跟踪文件保留，HEAD/main 和空索引保持，计划原文摘要不变，跟踪与新增文本的 diff 检查通过。
- S12 迁移已按用户要求提交为 `f7f970c3d`。R01 后续修复及补验尚未提交；验收事项关闭后状态更新为已完成、13 / 17。未推送。原 unit/integration 全量证据与本次定向 race 分开记录，不宣称再次执行了两组全量。
