# S12 实施证据

S12 迁移已提交为 `f7f970c3d`。用户授权 R01 针对性修复后，原普通全量失败已消除，roadmap 更新为 13 / 17；[修复及补验](r01-refresh-stop/README.md)独立保存，本次修复尚未提交。

- [原输入](initial-state.json)、[源码摘要](initial-file-checksums.json.gz)、[规划固定问题](planning/fixed-issues.json)。
- [迁移/事务账本](migration-ledger.md)、[实际文件与构建/消费者清单](final-migration-files.json.gz)、[测试兼容清理](test-only-compatibility.json)。
- [验证明细](verification.md)、[门禁夹具](depguard-fixtures.json)、[lint 逐项比较](final-lint-comparison.json)。
- [R01 账号刷新观察](unplanned-account-refresh-observation.json)、[R02 非 S12 测试误选观察](unplanned-creative-race-observation.json)。

日志与 JSON 事件保存原失败及后续通过，规划预期失败不计为通过。父子测试事件和重叠集合不相加；仅编译、跳过、外部环境限制不计行为通过。迁移提交已获用户授权；未推送、切换分支或使用 subagent。
