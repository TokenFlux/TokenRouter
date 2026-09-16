# S09 证据入口

- [原计划及完成记录](../../S09-upstream-gateway.md#s09_completion)
- [完成门禁与命令结果](completion.json)
- [契约矩阵](acceptance-matrix.md)
- [迁移账本](migration-ledger.md)、[后续职责与回退](handoff.md)
- [文件/符号摘要](final-file-summary.json)、[静态消费者摘要](final-consumers-summary.json)
- [全部测试结果](final-test-results.json)、[跳过](final-skipped-tests.json)、[保留的失败](final-failed-events.json)
- [lint逐项对照](final-lint-comparison.json)、[Wire一致性](final-wire-idempotence.json)、[工作区保护](final-integrity-check.json)
- [验收阻塞例外与偶发观测](incidental-observations.json)、[容器环境失败和补验](environment-observations.json)

较大的声明、引用、测试事件和日志以 gzip 保存。每个 result 的命令、退出码与事件数量独立有效，父子事件与重复集合不能相加为独立测试数。规划失败资料保存在 planning，未改写 S00—S08 冻结资料；本阶段没有自动提交。
