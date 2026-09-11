# S02 验证结果

项目工具链固定 `GOTOOLCHAIN=go1.27.0`；系统 Go 为 1.27.1，golangci-lint 2.13.2（Go 1.27.0 编译），Docker 29.5.2。普通、unit、integration 分别执行，每条命令有独立结果。pass/fail/skip 按带 Test 名的 JSON 事件计数，包含子例，不含只编译的包；构建与 lint 的零测试计数不表示行为通过。

## 最终结果

| 验证 | 退出码 | pass/fail/skip | 证据 |
| --- | --- | --- | --- |
| 普通全仓测试 | 0 | 11049/0/5 | [closeout-normal.result.json](closeout-normal.result.json) |
| unit 全仓测试 | 0 | 19034/0/9 | [closeout-unit.result.json](closeout-unit.result.json) |
| integration 全仓测试 | 1 | 11698/37/6 | [closeout-integration.result.json](closeout-integration.result.json) |
| 普通 lint（默认报告） | 1 | 0/0/0 | [delivery-lint-normal.result.json](delivery-lint-normal.result.json) |
| unit lint（默认报告） | 1 | 0/0/0 | [delivery-lint-unit.result.json](delivery-lint-unit.result.json) |
| integration lint（默认报告） | 1 | 0/0/0 | [delivery-lint-integration.result.json](delivery-lint-integration.result.json) |
| 普通 lint（完整报告） | 1 | — | [uncapped-lint-current-normal.result.json](uncapped-lint-current-normal.result.json) |
| unit lint（完整报告） | 1 | — | [uncapped-lint-current-unit.result.json](uncapped-lint-current-unit.result.json) |
| integration lint（完整报告） | 1 | — | [uncapped-lint-current-integration.result.json](uncapped-lint-current-integration.result.json) |
| 新模块/时间轮/lifecycle race | 0 | 67/0/0 | [lifecycle-contract-race-final2.result.json](lifecycle-contract-race-final2.result.json) |
| 旧队列/缓存/Live race | 0 | 79/0/0 | [runtime-race-second.result.json](runtime-race-second.result.json) |
| WS/错误透传/最终写回 race | 0 | 80/0/0 | [owned-resources-race-second.result.json](owned-resources-race-second.result.json) |
| HTTP 运维队列与 helper race | 0 | 66/0/0 | [handler-owner-race.result.json](handler-owner-race.result.json) |
| HTTP 日志/QPS 最终 drain race | 0 | 2/0/0 | [handler-drain-race-second.result.json](handler-drain-race-second.result.json) |
| 手动和定时清理等待 race | 0 | 32/0/0 | [usage-cleanup-race-second.result.json](usage-cleanup-race-second.result.json) |
| 基础库/会话/HTTP/logging race | 0 | 382/0/0 | [infra-race.result.json](infra-race.result.json) |
| 真实 Redis 会话/限流 race | 0 | 15/0/0 | [redis-race-integration.result.json](redis-race-integration.result.json) |
| 真实 Redis 订阅等待 race | 0 | 4/0/0 | [subscription-race-integration.result.json](subscription-race-integration.result.json) |
| 生命周期原文件副本 goleak/race | 0 | 19/0/0 | [lifecycle-goleak-fixture.result.json](lifecycle-goleak-fixture.result.json) |
| 真实存储、初始化与进程契约 | 0 | 69/0/0 | [process-storage-final.result.json](process-storage-final.result.json) |
| 完整图日志等级及进程补验 | 0 | 69/0/0 | [final-process-observability.result.json](final-process-observability.result.json) |
| 真实产物 embed 注入/HTTP 契约 | 0 | 101/0/0 | [delivery-embed-contracts.result.json](delivery-embed-contracts.result.json) |
| 前端实际产物 | 0 | 0/0/0 | [frontend-build.result.json](frontend-build.result.json) |
| 普通 Make 构建 | 0 | 0/0/0 | [closeout-build.result.json](closeout-build.result.json) |
| embed 构建 | 0 | 0/0/0 | [closeout-embed.result.json](closeout-embed.result.json) |
| Linux amd64 构建 | 0 | 0/0/0 | [closeout-linux.result.json](closeout-linux.result.json) |
| Linux arm64 构建 | 0 | 0/0/0 | [closeout-linux-arm64.result.json](closeout-linux-arm64.result.json) |
| 两个维护命令构建 | 0 | 0/0/0 | [delivery-maintenance.result.json](delivery-maintenance.result.json) |
| Wire 二次生成 | 0 | 0/0/0 | [delivery-wire-repeat.result.json](delivery-wire-repeat.result.json) |
| Linux 真实重启/重放/SIGTERM | 0 | — | [linux-process.result.json](linux-process.result.json) |

普通与 unit 全量通过；integration 原有 `groups_protocol_policy_v1` 分组约束失败独立保留。最终名单和每个测试的具体数据库错误与 S01 核对，见 [integration-comparison.json](integration-comparison.json)。未改资金业务或扩大忽略规则。另一次额度释放测试的时钟前提波动已经通过同源时间固定测试数据，全部金额断言保留，证据和限定结论见 [clock-fixture.md](clock-fixture.md)。

## lint 的截断与完整比较

默认报告仍为普通 2、unit 68、integration 10 条，但同文诊断的默认上限可能使文件名单随返回顺序变化；不能以这些数量或单次默认文件集合证明无回归。因此在原始 HEAD 隔离副本及当前工作区分别使用 `--max-same-issues=0 --max-issues-per-linter=0` 再跑三组，保留完整 JSON，见 [uncapped-lint-comparison.json](uncapped-lint-comparison.json)。

- normal：HEAD 2 条 → 当前 2 条；新增 0 条，移除 0 条。
- unit：HEAD 289 条 → 当前 286 条；新增 0 条，移除 3 条。
- integration：HEAD 21 条 → 当前 20 条；新增 0 条，移除 1 条。

移除项来自 AES 构造返回具体类型和旧设置测试不再强转私有仓储/替身，都是迁移后的类型检查结果；原六项 unit depguard 违规仍保留。完整比较没有新增诊断。S01 的原始冻结报告不改写，新取得的未截断 HEAD 报告仅放在 S02。

## 进程、存储与构建选择

PostgreSQL 测试使用 `postgres:18.1-alpine3.23`，Redis 使用 `redis:8.4-alpine`，均为隔离容器及临时配置。普通图验证 standard/simple、setup 三条入口、CLI 文件权限/安装锁、版本注入、初始化失败连接回收、监听失败、SIGTERM 及关闭顺序。Linux 容器通过真实管理员 API 发起重启，两次请求相同结果且第二次带重放 Header；约 600ms 响应窗口后按同一清理链退出 0，重开容器后 SIGTERM 也退出 0。

首次尝试取得 Linux amd64 runner 镜像超时，随后用当前 Docker 原生 Linux arm64 执行进程验证；amd64 单独构建通过。容器重开可能改变动态映射端口，验证脚本已重新查询端口。失败尝试有原始结果，不算行为通过；最终脚本 [linux-process.py](linux-process.py) 和 [linux-process.result.json](linux-process.result.json)可复查，资源已删除。

[build-selections.json](build-selections.json)保存普通/unit/integration/e2e/embed/wireinject 和 Darwin/Linux 的 go list 结果及文件选择；e2e 仅清点选择，不宣称执行。源码级构造候选三项均为请求内流对象，完整 Wire 构造无后台 Start。goleak 在与原文件摘要一致的可丢弃 lifecycle 副本中验证，没有修改仓库模块依赖。

Wire 通过原 `go generate ./cmd/server` 入口生成，并再次生成比较摘要一致；只修改 Wire，没有运行 Ent 生成。前端真实产物生成后做 embed 构建和注入测试。`make build`、Linux amd64/arm64 及两个维护命令构建都有结果。

## 限制、文档及差异

真实供应商 E2E 缺少地址/密钥、既有 Makefile 引用脚本缺失，归 S16；外部 TLS capture 环境限制沿用 S01。单元中的本地 TLS 收集与取消验证不冒充外部供应商兼容验证。未运行全仓 race 或 benchmark。lumberjack 的原第三方维护循环保留进程生命周期，应用持有的文件句柄按序关闭。

[契约矩阵](contracts.md)、[实际事件](contract-events.json)、[迁移清单](migration.md)、[生命周期](lifecycle.md)、[精确依赖例外](dependency-exceptions.json)提供后续阶段输入。中间构建/测试诊断、时间源夹具修正、测试替身竞争和默认 lint 截断都保留为执行记录，不冒充 S01 既有失败。

[checks.json](checks.json)验证 309 个 SQL 文件、379 个 Ent 文件、go.mod/go.sum、49 个原有其他任务文件、计划正文、索引和原 depguard/protocol 规则；[日志目录清单](log-manifest.json)列出脱敏压缩日志。最终 `git diff --check` 必须为零。
