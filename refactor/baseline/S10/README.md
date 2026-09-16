# S10 验证证据

状态：**已完成**。原计划及执行记录见 [S10 阶段文件](../../S10-notification-site-moderation-search.md#s10_completion)。

- [验收与限制](acceptance.md)、[固定问题行为证据](fixed-issues-validation.json)、[测试结果](final-test-results.json)、[既有跳过](final-skips.json)。
- [迁移账本](migration-ledger.md)、[逐符号消费者](final-symbol-links.json.gz)、[生产 Wire](final-wire-module-bindings.json)、[后续职责与回退](handoff.md)。
- [依赖许可](dependency-exceptions.json)、[57 项门禁夹具](depguard-fixtures.json)、[完整 lint 对比](final-lint-comparison.json)、[构建条件](buildsets.json)。
- [原计划摘要](plan-original.json)、[实施基线](initial-state.json)、[初始文件摘要](initial-file-checksums.json.gz)、[规划原失败](planning/)。
- [构建/维护/进程](final-build-results.json)、[生成幂等](final-wire-idempotence.json)、[文件完整性](final-integrity-check.json)、[文档锚点](final-doc-links.json)。

每份 `*.result.json` 保存实际命令、工具链、退出码、时间和事件计数；日志在 `logs/`。普通/unit/integration 的 lint 完整 JSON 以 gzip 保存。中间迁移失败与预期历史失败不计作通过；未执行的真实外部环境验证单独保留。tools 是本阶段可重放清点/夹具的证据源，不替代仓库 depguard。
