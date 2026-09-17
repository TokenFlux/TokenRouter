# S15 实施证据（未完成）

阶段计划与持续记录见 [S15-http-settings.md](../../S15-http-settings.md)。初始资料、规划复现与分批命令结果分别保留；失败日志不覆盖，修复后使用独立 recheck 结果。

- `planning/`：原实现、仓库外 overlay 与预期失败，不计通过。
- `initial-state.json`、`initial-checksums.json.gz`：实施输入及原计划正文摘要。
- `routes-baseline.json`、`routes-current.json`、`route-table-comparison.json`：按函数和源码作用域提取的路由声明，不能代替真实完整装配。
- `settings-field-inputs.json`、`remaining-setting-methods.json`：完整拆分的输入；目前尚未逐项完成归属。
- `admin-route-migration.json`、`user-route-migration.json`、`settings-route-migration.json`：已拆的路由拥有者，后续更新实际消费者。
- `dependency-transitions.json`、`route-dependency-transitions.json`：精确源文件的新增目标依赖和退出步骤，不是目录级豁免。
- `*.json` 与同名 `*.log`：实际命令、退出码与测试事件。完整阶段验收尚未执行，不能以当前定向通过结果代替。
- `integrity-checkpoint.json`：原计划正文、SQL/Ent/旧冻结资料及原有未跟踪文件的阶段中途核对。

尚需删除全局 handler/DTO/routes 聚合、迁完设置规则、实现生产注册表并完成全部门禁。roadmap 保持 15/17，S15 实施中。
