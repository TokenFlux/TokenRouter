# S01 验证结果

基线 HEAD：`20105ee8e6b6f995867ffc2e27977866ab5c7393`。项目工具链固定 Go 1.27.0，golangci-lint 2.13.2；本阶段未更新工具或依赖。测试均使用 `-count=1`，[工具记录](tools.json) 与每个 result.json 保存命令、环境、退出码、计数和完整事件日志链接；[测试索引](test-events.json) 汇总实际执行结果。

## 实际结果

计数包含顶层测试及子测试；跳过与无匹配不算行为通过。最后的兼容补验不与全量结果重复相加。

| 验证 | 退出码 | 通过 / 失败 / 跳过 | 耗时秒 | 证据 |
| --- | --- | --- | --- | --- |
| 全量普通 | 0 | 11006 / 0 / 5 | 234.9 | [命令与事件](final-normal.result.json)；[脱敏日志](logs/final-normal.log.gz) |
| 全量 unit | 0 | 18991 / 0 / 9 | 295.8 | [命令与事件](final-unit.result.json)；[脱敏日志](logs/final-unit.log.gz) |
| 全量 integration | 1 | 11637 / 37 / 6 | 244.58 | [命令与事件](final-integration.result.json)；[脱敏日志](logs/final-integration.log.gz) |
| 普通 lint | 1 | — | 82.87 | [命令与事件](final-lint-normal.result.json)；[脱敏日志](logs/final-lint-normal.log.gz) |
| unit lint | 1 | — | 75.41 | [命令与事件](final-lint-unit.result.json)；[脱敏日志](logs/final-lint-unit.log.gz) |
| integration lint | 1 | — | 39.73 | [命令与事件](final-lint-integration.result.json)；[脱敏日志](logs/final-lint-integration.log.gz) |
| 技术包 unit race | 0 | 150 / 0 / 3 | 6.44 | [命令与事件](s01-3-race-unit.result.json)；[脱敏日志](logs/s01-3-race-unit.log.gz) |
| 资金存储 integration race | 0 | 22 / 0 / 0 | 52.69 | [命令与事件](s01-3-race-storage.result.json)；[脱敏日志](logs/s01-3-race-storage.log.gz) |
| 限流/会话真实 Redis | 0 | 17 / 0 / 0 | 9.6 | [命令与事件](s01-4-integration.result.json)；[脱敏日志](logs/s01-4-integration.log.gz) |
| 限流/会话 integration race | 0 | 2 / 0 / 0 | 3.73 | [命令与事件](s01-4-race-integration.result.json)；[脱敏日志](logs/s01-4-race-integration.log.gz) |
| 末尾兼容普通补验 | 0 | 368 / 0 / 2 | 3.26 | [命令与事件](final-compatibility-normal.result.json)；[脱敏日志](logs/final-compatibility-normal.log.gz) |
| 末尾兼容 unit 补验 | 0 | 415 / 0 / 2 | 3.48 | [命令与事件](final-compatibility-unit.result.json)；[脱敏日志](logs/final-compatibility-unit.log.gz) |
| 最终时钟普通补验 | 0 | 13 / 0 / 0 | 0.87 | [命令与事件](final-clock-normal.result.json)；[脱敏日志](logs/final-clock-normal.log.gz) |
| 最终时钟 unit 补验 | 0 | 13 / 0 / 0 | 0.61 | [命令与事件](final-clock-unit.result.json)；[脱敏日志](logs/final-clock-unit.log.gz) |
| 末尾兼容 lint | 0 | — | 0.53 | [命令与事件](final-compatibility-lint-verified.result.json)；[脱敏日志](logs/final-compatibility-lint-verified.log.gz) |
| 最终普通构建 | 0 | — | 10.08 | [命令与事件](build-normal-final.result.json)；[脱敏日志](logs/build-normal-final.log.gz) |
| 真实前端构建 | 0 | — | 25.41 | [命令与事件](build-frontend.result.json)；[脱敏日志](logs/build-frontend.log.gz) |
| 最终 embed 构建 | 0 | — | 9.94 | [命令与事件](build-embed-final.result.json)；[脱敏日志](logs/build-embed-final.log.gz) |
| 最终 Linux 构建 | 0 | — | 10.17 | [命令与事件](build-linux-final.result.json)；[脱敏日志](logs/build-linux-final.log.gz) |

## 与 S00 比较

[lint 对齐](lint-comparison.json) 按原文件归属、规则和消息比较，普通 2、unit 68、integration 10，新增项均为零。TLS 测试的既有 G402 随原测试迁到 infra/httpclient/tlsfingerprint，未添加忽略项；六个旧 service/handler depguard 违规仍保留。

[integration 对齐](integration-comparison.json) 的 37 个失败条目与 S00 相同，仍集中于分组零值违反 groups_protocol_policy_v1。outbox 回滚注入未被到达，不能声称该场景通过；归 S03/S06 处理。真实 PostgreSQL 的退款审计失败回滚与并发只扣一次仍通过，资金相关定向 race 22 项通过。

## 末尾兼容补验

审查补齐了初始化前 Now 的单调时钟保留：显式 Calendar 已设置时区时继续转换，未初始化/用户时区回退时保留原 time.Now 语义；也恢复了随机字节辅助函数失败时返回 nil 的旧约定。补验覆盖 timezone、oauthpkce 及五个旧平台包，最后的时钟测试和 lint 均通过。普通、embed、Linux 二进制已按最终运行源码重新构建。全量结果记录的是补验前的完整回归，局部补验单独保留，不伪称所有测试在同一批次执行。

## 依赖与构建条件

[合法依赖证据](depguard-positive.json) 覆盖普通/unit/integration；unit/integration 的正例范围排除了已有 service/handler 违规，但包含 payment、待迁 pkg、保留路径与临时合法 Adapter。[拒绝夹具](depguard-fixtures.json) 同时覆盖新文件、测试文件、嵌套目录、跨平台依赖、迁出文件、精确旧文件新增 import 和待迁 pkg 的 SQL/Redis 增量；夹具已删除。

[完整构建选择](build-selections.json) 与 [阶段文件选择](file-build-selections.json) 覆盖普通、unit、integration、e2e、embed、wireinject 和 Linux。没有 go list 包错误。Wire 与 Ent 生成物未变化；Linux 仅作为可构建性证据。

## 环境与执行记录

[跳过和环境](skips-and-environment.json) 保留原始事件。真实供应商 E2E 沿用 S00 的地址/密钥及脚本缺失限制；本次 integration 的公开 TLS 探测实际执行通过，不能把它扩大为真实供应商 E2E 全面通过。

各子步骤中的机械提取编译错误、两个已删除的未使用 PKCE 辅助函数、一次 lint 并行锁拒绝，以及末尾单调时钟测试的静态检查修正，均与原有基线失败分开记录。最终新增 lint 项与 integration 失败均为零，未修改业务或扩大忽略规则。日志采用 gzip 归档，原文和归档摘要见 [log-archives.json](log-archives.json)。

## 交接

[迁移与剩余消费者](migration.md)、[契约矩阵](contracts.md) 和 [最终核对](final-checks.json) 为 S02 提供实际输入。阶段不修改既有 SQL migration、资金写入规则、缓存格式、公共 HTTP 响应或运行模式。

最终文件匹配证据见 [rule-coverage.json](rule-coverage.json)。新增的旧平台适配测试仅保留一个准确的 service/profile 许可，目标 TLS import 已改为新实现；[37 项定向回归](final-legacy-pool-test.result.json) 与 [实际准入](final-legacy-pool-gate.result.json) 通过，[三组 config 拒绝夹具](contract-test-gate-fixtures.json) 证明没有放宽该旧目录的新文件。
