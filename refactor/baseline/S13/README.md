# S13 完成证据

S13 已完成，roadmap 为 **14 / 17**。完整原计划与执行记录见 [阶段文件](../../S13-creative-batchimage.md#s13_completion)。未提交、推送或使用 subagent。

| 验证 | 通过事件 | 跳过 | 证据 |
| --- | ---: | ---: | --- |
| 普通全量 | 11,757 | 4 | [结果](final-normal.result.json) |
| unit 全量 | 19,797 | 8 | [结果](final-unit-recheck.result.json) |
| integration 全量（-p=4） | 12,732 | 4 | [重验结果](final-integration-recheck.result.json) |
| 真实 PostgreSQL/Redis 任务 race | 25 | 0 | [结果](tasks-integration-race.result.json) |
| B04 processor 取消 race（三轮） | 12 | 0 | [结果](b04-worker-cancellation-race.result.json) |
| B05 独立交付契约 race | 7 | 0 | [结果](b05-delivery-contracts-race.result.json) |
| standard/simple 等真实进程回归 | 10 | 0 | [结果](process-modes-recheck.result.json) |
| 真实前端产物 embed 测试 | 106 | 0 | [结果](real-embed-test.result.json) |

事件包含父子测试，各集合不可相加。B05 的补充测试只增加验证，运行代码未变。所有[跳过及原因](skipped-tests.json)单独保留；真实收费供应商、外部 TLS/E2E 未执行。

- 三套 lint **1 / 283 / 17**，对 S12 完整消息逐项比对无新增；B05 测试移除一项旧 errcheck。见 [比对](final-lint-comparison.json)。
- 后端、前端验证/构建、embed、Linux 及两个维护命令均通过，命令和退出码汇总在 [完成证据](completion-evidence.json)。
- [51 项 depguard 夹具](depguard-fixtures.json)全部命中预期，无临时文件残留；[8 套文件选择](build-selection-results.json)覆盖普通/unit/integration/wireinject/embed/e2e/Darwin/Linux。
- [迁移清单](migration-files.json)、[源码事实](source-facts.json.gz)、[普通引用](final-symbol-references-normal.json.gz)、[unit 引用](final-symbol-references-unit.json.gz)、[integration 引用](final-symbol-references-integration.json.gz)记录真实符号与实现/消费者；引用分析诊断为零。
- [资金与生命周期账本](funds-and-lifecycle.md)、[16 组契约矩阵](contract-matrix.json)、[依赖许可](dependency-exceptions.json)、[兼容入口清理](compatibility-removals.json)、[资金符号改名](funding-symbol-map.json)。
- [保护文件核对](final-integrity.json)、[Wire 幂等](wire-idempotence-final.json)、[链接](final-doc-links.json)及 [diff 检查](final-diff-check.json)均通过。原计划正文、SQL/Ent、S00—S12 和其他任务文件保持。

中间迁移适配失败全部保留。最终 integration 首轮因新增任务屏障与 HTTPRequests 同处阶段 15 导致顺序断言不确定；改为阶段 16 后，原断言、定向进程及全量 integration 均通过。此为本次装配回归修复，不是新增历史问题。

后续 S14 处理维护/初始化，S15 处理 HTTP/设置聚合，S16 删除有据可查的旧签名/上下文投影。回退不得删除成功事实、资金去重或恢复记录；B05 回退前处理或登记尚未交付/结算的已确认成功任务。
