# 错误所有者与团队 SQL 测试迁移

- 范围：仅删除直接转接原生错误的旧 service 变量，不重建错误、改变错误链或返回消息。
- `service-error-alias-owners.json` 记录 387 个旧符号和目标；`service-error-alias-reassignments.json` 为空，清点未发现对这些旧变量的重新赋值或取地址。
- `service-error-owner-reassignments.json` 也为空：改绑后的原生错误所有者同样没有重新赋值或取地址，不依赖旧转接变量保留某个历史错误对象。
- `service-error-local-consumers.json` 记录 133 个旧 service 文件、573 处引用改绑；`service-error-external-consumers.json` 记录 67 个外部文件、267 处改绑。改绑使用语法树和普通/unit/integration 类型信息。
- `service-error-alias-deletions.json` 记录各声明的删除及六个空文件；没有被删除的 Project Doc 锚点。默认使用原包名，存在遮蔽时使用职责别名，不增加 native 前缀。
- app 调度和四个 middleware 测试的六条新原生 import 按文件精确登记，保留原拒绝规则。

## 本阶段回归

`checkpoint-full-unit.json`：19,857 条通过事件、1 项失败。失败是 L01 添加关闭错误断言后未登记 sqlmock 的 Close 预期；不是旧业务失败。保留原失败日志 `checkpoint-full-unit.log.gz`。

`team-close-regression.json`：原位置的定向 unit race 通过。随后原 `TestTransferTeamOwnershipUsesTwoOrderedUpdates` 连同原事务顺序断言迁入 `internal/team/postgres/team_ownership_test.go`，删除 `repository/team_repo_s05_compat_unit_test.go`。仅供旧包装访问的 SQL 函数恢复为该包私有函数，生产 SQL、参数和事务未变。

## 验证

- `error-native-build.json`：全量构建通过。
- `error-native-unit-lint.json`：首次六项精确 import 诊断保留，规则已随消费者改绑。
- 完整 unit、integration、三组 lint 及后续迁移验收结果追加到阶段文件；上述构建结果不替代行为验证。
- 错误改绑之后的完整 unit 为 19,858 条通过、8 项既有跳过，完整 integration 为 12,790 条通过、4 项既有跳过，均退出 0。后续日期批次的进程夹具 race 单独记录，不改写这两个先前检查点的结果。
