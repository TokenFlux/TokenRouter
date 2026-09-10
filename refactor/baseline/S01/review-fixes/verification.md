# S01 review 修复验证

本次基于 `a8b9d1f0b`，修复请求重定向策略被缓存覆盖及 protocol 标准库 I/O 准入漏口。原 S00/S01 冻结证据不改写，当前增量源码摘要、规则范围和不变量见 [checks.json](checks.json)。

## 修复与契约

- `UpstreamPool.Do` 先复制缓存客户端、应用本次 `CheckRedirect`，再调用 `PrepareClient`。nil 恢复默认行为，适配器可覆盖本次策略，transport 与池 key 继续复用。失败及 Body.Close 的占用释放顺序不变。
- 回归测试使用本地 HTTP 重定向，覆盖默认→禁止→清空、适配覆盖顺序、目标未被访问、并发策略隔离和失败后的单条目池逐出。旧隔离测试改为核对 transport 身份；客户端本身是请求级对象。
- 仅收紧 protocol 六组生产/测试规则的标准库白名单；其余 509 条规则与历史例外保持原样。测试仅额外准入 testing，io 只表示流能力，具体调用和间接副作用仍需代码审查。

## 行为验证

项目工具链固定 `GOTOOLCHAIN=go1.27.0`。每行计数为 JSON 测试事件中的通过 / 失败 / 跳过，包含子测试，不跨行求和。

| 验证 | 退出码 | 通过 / 失败 / 跳过 |
| --- | --- | --- |
| [redirect-before-contract](redirect-before-contract.result.json) | 1 | 1 / 6 / 0 |
| [redirect-after](redirect-after.result.json) | 0 | 7 / 0 / 0 |
| [pool-normal](pool-normal.result.json) | 0 | 46 / 0 / 0 |
| [pool-unit](pool-unit.result.json) | 0 | 46 / 0 / 0 |
| [pool-integration](pool-integration.result.json) | 0 | 46 / 0 / 0 |
| [adapter-normal](adapter-normal.result.json) | 0 | 65 / 0 / 0 |
| [adapter-unit](adapter-unit.result.json) | 0 | 71 / 0 / 0 |
| [adapter-integration](adapter-integration.result.json) | 0 | 66 / 0 / 0 |
| [pool-adapter-race](pool-adapter-race.result.json) | 0 | 87 / 0 / 0 |

`redirect-before-contract` 是修复前的预期失败，用于证明测试能捕获问题。修复后所有新增测试通过。定向旧适配回归覆盖 OpenAI H2 回退/TLS 隔离、Grok CLI 403 边界、代理使用和逐跳安全校验；没有访问真实供应商。此次没有重跑全仓测试、前端构建或真实 E2E；原有环境限制继续见上级 S01 验证报告。

## 依赖夹具

[完整夹具及诊断](protocol-fixtures.json) 保存生成源码、命令、实际构建选择和预期诊断。使用实际 `.golangci.yml` 的副本，在可丢弃的同模块路径夹具中运行真实 golangci-lint。夹具已删除。

- 覆盖六个 protocol 职责路径、未知嵌套子目录，分别验证生产文件和测试文件；另外验证 unit、integration、wireinject、embed 和 Darwin/Linux 文件选择。
- 合法的 bytes / encoding/json / io / strings / testing 组合在六个构建集合中全部通过。
- 非法依赖包含 os、os/exec、net、net/smtp、net/http、net/rpc、database/sql、syscall、plugin、log、log/slog、io/fs，以及生产文件中的 testing。io/fs 用于验证 io 的精确许可不会扩大到子包。
- 修复前普通/unit/integration 仅分别报告 42 项拒绝；修复后普通/Linux 176 项，其余四组 177 项，均与选中文件的全部预期诊断完全一致，没有缺失或额外诊断。拒绝夹具退出码为 1 是预期成功条件。

[规则清单](protocol-rules.json) 记录本次准确准入包；[构建选择](build-selections.json) 确认新回归测试在项目普通、unit、integration、wireinject、embed 集合中均被选中。

## 全量 lint 与收尾

普通/unit/integration 全量 lint 退出码均为 1，诊断仍为 2 / 68 / 10。按准确文件、规则及消息多重集合与 S01 对齐后没有新增或移除项；仅归一化诊断文本首尾空白。见 [逐项对比](lint-comparison.json) 和各命令的 result.json / 原始 JSON 诊断。未新增忽略或历史许可。

相关 Project Doc 已同步请求级重定向顺序与 protocol 标准库边界。原计划正文、SQL migration、Ent/Wire 生成物及模块依赖文件未变；工作区其他任务保持原状。
