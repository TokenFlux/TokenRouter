# S14 验证与交接证据

S14 已完成。原计划及执行记录位于 [阶段文件](../../S14-backup-setup-maintenance.md#s14_completion)，原实现复现保留在 [planning](planning/planning-summary.json)。未提交、未推送、未切换分支；未使用 subagent。

## 验证结果

| 集合 | 结果 |
| --- | --- |
| 普通全量 | 11,765 条通过事件，4 项既有跳过 |
| unit 全量 | 19,813 条通过事件，8 项既有跳过 |
| integration 全量 | 12,748 条通过事件，4 项既有跳过 |
| 最终原生模块定向 race | 143 条通过事件，无跳过 |
| 真实存储/初始化定向 race | 11 条通过事件，无跳过，含真实 pg_dump/gzip/本地归档/psql 演练 |
| 真实进程 | 11 条通过事件，含 standard/simple、SIGTERM、两个维护命令、CLI/AUTO_SETUP 和初始化/监听失败 |
| 系统维护 HTTP race | 11 条通过事件；恢复密码 HTTP race 8 条通过事件 |
| lint 普通/unit/integration | 1 / 283 / 17；按文件映射、规则和完整消息与 S13 比较，无新增、无消失 |
| 门禁与构建选择 | 42 个夹具全部符合预期；8 个构建集合均取得文件选择结果 |
| 构建 | 后端、前端检查/构建、embed、Linux amd64、两个维护命令全部成功；真实 embed 106 条通过事件 |
| Wire | 再生成无差异 |

事件包含父子测试，各集合重叠，不能相加。全量之后补充的局部契约由独立结果记录；没有把原复现的预期失败或跳过计为通过。

- [命令与完成摘要](completion-evidence.json)
- [契约矩阵](contract-matrix.json)
- [完整 lint 对照](final-lint-comparison.json)
- [门禁夹具](depguard-fixtures.json)、[构建选择](build-selection-results.json)
- [文件与符号归属](migration-files.json)、[源码事实](source-facts.json.gz)
- [普通引用](references-normal.json.gz)、[unit 引用](references-unit.json.gz)、[integration 引用](references-integration.json.gz)
- [维护写权限与生命周期](funds-and-lifecycle.md)
- [依赖许可](dependency-exceptions.json)、[兼容退出事项](handoff.md)
- [冻结资料校验](integrity-checkpoint.json)、[Wire 校验](wire-idempotence.json)、[跳过记录](skipped-tests.json)

每个命令对应独立 result.json、压缩日志和适用的测试事件文件。早期编译或迁移回归失败日志保留；验收使用 completion-evidence.json 选定的成功结果，不只依据日志文件数量。

## 验证边界

SQL、取消与所有者比较使用隔离 PostgreSQL；S3/GitHub 使用本地协议夹具，未访问生产资源。真实进程验证运行于 Darwin；Linux 取得交叉构建证据，重启平台分支另有参数化生命周期测试。未新增真实云存储、Linux 部署演练或完整灾备承诺。
