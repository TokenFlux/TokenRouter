# S08 固定问题与回退边界

历史清单仅 B01—B05；规划阶段原 HEAD 的失败复现已归档在 [planning/manifest](planning/manifest.json)。预期失败是旧行为证据，不计为通过。实施没有新增历史问题审计或清单外修复；中间迁移编译/测试错误由本阶段修改造成并修正，日志保留，最终成功结果单独列出。

| 编号 | 新实现与行为变化 | 修复后证据 | 回退恢复的风险 |
| --- | --- | --- | --- |
| B01 | usage/postgres 按字段拥有权应用状态变更，短事务锁状态行，条件推进/清除手工游标，保留实时水位 | `TestS08ManualBackfillPreservesConcurrentState` (真实 PG 两种交错)；`TestS08RegressionManualBackfillPreservesLiveProgress` | 手工请求或旧后台快照覆盖新水位/目标 |
| B02 | audit ClearWithTrace 同 Tx 锁/计数/清空/留痕，任一步失败回滚 | `TestS08AuditClearTraceFailureRollsBack`，真实 PG 触发留痕失败后原记录存在 | TRUNCATE 已提交而留痕失败，审计链断裂 |
| B03 | audit 与系统 sink 各自 Start/Stop 屏障，worker/退避状态唯一，Stop 后拒绝重开 | `TestS08RegressionAuditDuplicateStart`、`TestS08RegressionSystemLogDuplicateStart`；定向 race | 重复 worker、队列竞争、退避状态分裂 |
| B04 | 聚合运行 context 取消在途与重试，停止登记和等待受预算约束 | `TestS08RegressionAggregationStopCancelsWork`；进程 integration 检查关闭顺序 | Stop 不取消可取消的重算，退出等待拖延 |
| B05 | 所有已经删除的任务路径在统一收尾登记受运行时管理的重算；取消仍是取消 | `TestS08CanceledPartialCleanupRepairsCommittedData`，真实 PG 检查剩余事实/统计/余额；`TestS08RegressionCanceledCleanupRepairsAggregates` | 部分删除后取消导致聚合统计滞后 |

真实 PG 三项组合见 [fixed-postgres-race](fixed-postgres-race.result.json)，最终再次在 [query-cache-read-contracts-integration-race](query-cache-read-contracts-integration-race.result.json) 和全量 integration 执行。单测分别见 [aggregation-fixed-unit-race](aggregation-fixed-unit-race.result.json)、[audit-contracts](audit-contracts.result.json)、[log-sink-fixed-race](log-sink-fixed-race.result.json)。具体父子测试事件见对应 `.events.json.gz`，没有用 mock 代替数据库原子性验收。

B02 不影响普通尽力异步审计、Ops 清理留痕或退款强事务。B05 不新增持久修复队列或崩溃恢复承诺。回退仅撤销 S08 代码、装配、规则和文档，不涉及数据库/缓存格式降级，保留前阶段与其他任务资料。
