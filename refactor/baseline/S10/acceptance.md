# S10 验收与限制

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 全量普通 | 11,505 通过 / 0 失败 / 4 既有跳过 | [命令与结果](final-normal.result.json) |
| 全量 unit | 19,543 通过 / 0 失败 / 8 既有跳过 | [命令与结果](final-unit.result.json) |
| 全量 integration，包并发 4 | 12,449 通过 / 0 失败 / 4 既有跳过 | [命令与结果](final-integration.result.json) |
| 定向共享状态/存储 race | 四个模块、SMTP、队列、配置发布、Redis、PostgreSQL 全部通过 | [分组记录](final-test-results.json) |
| 真实进程 | standard/simple、SIGTERM、版本、setup/维护命令；13 个通过事件 | [进程与事务](final-process-storage-contracts.result.json) |
| 最后补充契约 | SMTP ACK、页面 HTTP、搜索清理预算；Cyber warning/媒体/用户状态提交回滚 | [B01—B07](fixed-issues-validation.json)、[媒体事务](final-moderation-media-transaction.result.json) |
| 全量 lint | 1 / 284 / 17，与 S09 逐文件映射、规则、消息相同，无新增 | [完整对比](final-lint-comparison.json) |
| depguard | 57 项普通/unit/integration 夹具均符合预期，临时文件已删除 | [命令和诊断](depguard-fixtures.json) |
| 前端 | 14 个测试文件、199 项测试通过；真实产物构建通过 | [前端测试](final-frontend-test.result.json)、[构建](final-frontend-build.result.json) |
| 编译与注入 | 普通、embed、Linux amd64、两个维护命令通过；真实产物 embed 101 个通过事件 | [构建汇总](final-build-results.json) |
| Wire | 手写组合根改绑；再次生成 SHA 相同 | [摘要](final-wire-idempotence.json) |
| 构建选择 | 普通/unit/integration/wireinject/embed/e2e/Darwin/Linux 无加载错误 | [go list](buildsets.json) |

数字是包含父子测试的事件数，集合重叠，不相加。全量测试后只补充约定契约测试、清除无消费者的兼容符号、把 unit 私有委托移入相应测试文件以及修正门禁和格式；这些最终差异另经直接消费者普通 7,875、unit 589 和对应定向测试验收，不冒充重新全跑结果。

原 SMTP 测试迁到 notification/smtp 后，其三条历史 lint 按路径映射保留，未更改断言或扩大排除。首次收尾发现的未使用包装、标签辅助函数、精确许可和格式诊断均为本阶段迁移残留，清理后完整 lint 对齐。[中间记录](intermediate-results.json)保留失败退出码；原问题失败另见 planning，不计入通过。

既有跳过逐条保存在 [final-skips.json](final-skips.json)：真实 API、开发机授权文件、外部 TLS 指纹、既有事务/WS 条件测试均未伪称通过。e2e 的 go list/编译选择不等于真实供应商行为验证；硬件、生产账号、外部 TLS 捕获和真实供应商 E2E 未执行。

未发现并修复清单外历史问题。notification 的协调和搜索发布代次仅覆盖单服务进程；SMTP 接收成功但标记失败仍可能不确定，不保证恰好一次。搜索取消最多三秒尽力退额，清理超时明确保留额度及失败观测；不增加持久修复队列或跨实例协调。

[完整性结果](final-integrity-check.json)校验 SQL、Ent、S00—S09、其他任务文件及原计划前缀；[文档和锚点](final-doc-links.json)核对受影响链接。无自动提交、推送或分支切换，索引保持原状态。
