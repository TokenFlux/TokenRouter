# S11 实施与验收证据

S11 已完成，roadmap 为 **12 / 17**；下一步编写 S12 子计划。没有自动提交。

- [完整验收说明](verification.md)、[最终命令/退出码/日志](final-validation.json)。普通/unit/integration 分别 11,749 / 19,773 / 12,698 条通过、0 失败；4 / 8 / 5 项跳过[单列](final-skips.json)，不计通过。
- [完整 lint 基线比较](final-lint-comparison.json)：1 / 284 / 17 项，与 S10 按文件、规则、完整消息完全一致，无新增或删除诊断。
- [原冻结状态](initial-state.json)、[原源码摘要](initial-file-checksums.json.gz)、[最终保护核对](protected-final.json)。
- [固定问题原证据](planning/fixed-issues.json)、[修复后实际事件](fixed-regression-evidence.json)。迁移新增 CN 投影回归的原失败/修复另见验收说明，不扩大 B01—B06。
- [迁移与交接账本](migration-ledger.md)、[入口契约矩阵](entry-contract-matrix.md)、[578 个 Go 文件清单](final-migration-files.json.gz)、[符号引用汇总](final-symbol-summary.json)、[八组构建选择](build-selection-results.json)。
- [最终 57 项门禁夹具](depguard-fixtures.json)、[夹具清理/配置摘要](depguard-fixture-checkpoint.json)、[最后一轮许可清理](final-gate-cleanup.json)。
- [standard/simple 真实进程关闭](gateway-process-shutdown.result.json)、[Qoder/规则真实 PostgreSQL/Redis 链](fixed-execute-storage-race.result.json)、[统一请求等待 race](gateway-scope-race-reconciled.result.json)。
- [前端测试](frontend-test.result.json)、[前端构建](frontend-build.result.json)、[真实产物 embed 行为](embed-ui-test.result.json)、[Wire 幂等](wire-idempotence.json)。后端和维护命令构建见最终命令汇总。
- [并行交接与原始日志索引](parallel-evidence-index.json)、[文档链接](final-doc-links.json)、[diff 检查](diff-check.json)。

日志是 logs/ 下的 gzip，JSON 测试事件保留真实结果。迁移过程中暂态编译、夹具或门禁失败和后续通过均保留；working-*、parallel-checkpoints/ 是中间快照，不作为最终通过证明。事件包含父子测试，重叠集合不能相加。真实供应商、硬件、外部 TLS/E2E 及 S07 outbox 恢复限制未改变。
