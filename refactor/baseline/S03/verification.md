# S03 验证摘要

所有项目命令均显式使用 `GOTOOLCHAIN=go1.27.0`。系统 Go 为 1.27.1，本地 golangci-lint 2.13.2 的编译工具链为 1.27.0，Docker client/server 29.5.2；详见 [工具记录](tools.json)。以下只使用收尾实际执行的结果，中间编译/测试失败保留用于追踪迁移修正，不当作旧基线。

## 全量结果

| 命令集合 | 退出码 | 通过测试事件 | 失败 | 跳过 | 实际证据 |
| --- | ---: | ---: | ---: | ---: | --- |
| go test normal | 0 | 11061 | 0 | 5 | [closeout-test-normal](closeout-test-normal.result.json) |
| go test unit | 0 | 19046 | 0 | 9 | [closeout-test-unit](closeout-test-unit.result.json) |
| go test integration | 0 | 11747 | 0 | 6 | [closeout-test-integration](closeout-test-integration.result.json) |

事件数含父测试和子测试，不含包级汇总；跳过不计行为通过。所有命令保留 `-count=1 -json`，没有只使用缓存结果。

| lint 集合（不截断诊断） | S02 | S03 | 新增 | 结果 |
| --- | ---: | ---: | ---: | --- |
| normal | 2 | 1 | 0 | [诊断](closeout-lint-normal.json) / [命令](closeout-lint-normal.result.json) |
| unit | 286 | 285 | 0 | [诊断](closeout-lint-unit.json) / [命令](closeout-lint-unit.result.json) |
| integration | 20 | 19 | 0 | [诊断](closeout-lint-integration.json) / [命令](closeout-lint-integration.result.json) |

lint 均以退出码 1 归档既有问题，没有宣称通过。按文件、规则、完整消息比较：[lint-comparison.json](lint-comparison.json)。原 domain 目录的 QF1003 随迁移改为等价 tagged switch 后消除；其余诊断逐项相同，包括六项旧 unit depguard。没有扩大忽略规则。

## 关键契约

| 验证面 | 实际测试与证据 |
| --- | --- |
| 三种文本协议双向非流/SSE、工具/unknown/usage/结束事件 | 原 apicompat 算法测试迁到 bridge；型号选择相关契约在旧兼容入口，原断言保留；新 `TestRequestOptionsAreIndependentOfModelName` 验证显式输入。普通/unit/integration 全量及 [options race](request-options-race.result.json) 实际运行。 |
| Gemini 方言/流及执行链 | 原 Gemini/Antigravity 直接消费者测试；新增 `TestNativeGeminiCompatIteratorPreservesPartialUsageAndLazyIDs`/`TestNativeGeminiMessagesIteratorKeepsUsageUpdateAfterOutput`验证提前结束与 usage 观测时序（精确名称见事件表）；保留 HTTP 写失败、取消、前导/真实输出、末尾 usage 原链。 |
| 请求体修复 | 旧 httputil 的 BOM、控制字符、转义、结构、边界和 MaxBytesError 测试，新增纯 BodyLimitError 包装识别和原字节不变测试；[边界测试](final-boundary-contracts.result.json)。 |
| 能力/目录 HTTP | 24 项完整 JSON 与 TypeScript fixture；空集合、认证模式和 fallback；`TestProtocolCatalogHTTPContract` 走真实管理员路由，检查 401/403、auth→audit→catalog 顺序及投影冻结。 |
| 纯定价/目录 | 原服务 Pricing/Billing/Cost/Marketplace/区间/账户成本测试继续命中唯一新实现；新增零价/缺价、倍率不修改输入、零时刻/DST 等契约。 |
| provider 共享状态与热更新 | `TestPricingConstructionAndLifecycle`、候选工厂单次快照、Snapshot 隔离、并发加载/读取；[新包 race](provider-protocol-race.result.json)、[旧消费者 race](pricing-compat-race.result.json) 均通过，未运行全仓 race/benchmark。 |
| 分组存储 | 四文件 45 处省略默认夹具修正，原 GroupRepoSuite、复制与 outbox 失败回滚在真实 PostgreSQL 实际通过；[S02 失败名单逐项比较](test-baseline-comparison.json)，本轮无新增分组生产故障。 |
| 定价进程启停 | [真实进程测试](pricing-process-integration.result.json) 覆盖 version、standard/simple SIGTERM、初始化/监听失败、CLI/Web/AUTO_SETUP；追加定价单实例、初始化先于 Start、停止先于 Redis 的断言。 |
| HTTP/frontend | [前端验证](frontend-tests.result.json)：lint/typecheck 与关键 Vitest，14 文件、199 测试通过；[真实前端构建](frontend-build.result.json) 后 [embed 注入测试](embed-contracts.result.json) 实际通过。 |

逐测试事件及命令来源见 [contract-events.json.gz](contract-events.json.gz)。其分类用于检索，可能一条测试支持多种契约；每个结果中的失败、跳过单独记录。

## 构建与依赖

- [closeout-build](closeout-build.result.json)：退出码 0，`make build`。
- [closeout-build-embed](closeout-build-embed.result.json)：退出码 0，`go build -tags=embed -o bin/server-embed ./cmd/server`。
- [closeout-build-linux](closeout-build-linux.result.json)：退出码 0，`env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/server-linux-amd64 ./cmd/server`。
- [closeout-maintenance](closeout-maintenance.result.json)：退出码 0，`go build ./cmd/jwtgen ./cmd/cleanup-ingress-reject-logs`。
- [final-wire](final-wire.result.json)：退出码 0，`go generate ./cmd/server`。
- [wire-reproducible](wire-reproducible.result.json)：退出码 0，`go generate ./cmd/server`。

Wire 再次生成摘要一致，只有 app 定价 provider 的可解释变更；没有运行 Ent 生成。[构建选择](build-selections.json)覆盖 normal/unit/integration/wireinject/embed/e2e 与 Linux；选中测试还以实际 JSON 事件核实，跨平台 build 只作为可构建证据。

[depguard 夹具](dependency-fixtures.json)使用完整现有配置的可丢弃模块，六组正例退出 0、六组反例退出 1，missing/unexpected 均为空；覆盖精确旧许可、同目录新文件、旧文件新禁止 import、迁出失去许可、非法子包、协议反向依赖与正常 Adapter。临时目录已删除，配置 SHA256 与交付账本一致。

## 已有验证限制

详见 [verification-limitations.json](verification-limitations.json)：真实 Qoder/OpenAI API 测试需要显式开关/密钥，本地 auth 导入测试保持关闭；既有 DingTalk sentinel 和非 response.create WS fixture 跳过保持原状，unit/integration 的其它 skip 也逐项列明。真实供应商 E2E 未配置服务与密钥，Makefile 的 e2e-test.sh 仍不存在；外部 TLS 捕获限制沿 S02，均不计通过，S09/S11/S16 按所属场景补验。

PostgreSQL/Redis 的行为测试使用 Testcontainers 隔离资源，日志中的实际镜像版本保留；未连接生产数据库。SQL checksum、Ent、Go 依赖文件、旧冻结资料与其他任务文件的完整性见 [integrity.json](integrity.json)。已检查现有 diff 和新增文本文件，无需改动真实暂存区。
