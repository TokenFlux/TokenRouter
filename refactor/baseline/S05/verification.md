# S05 验证与交接

本阶段已完成。所有后端命令使用 `GOTOOLCHAIN=go1.27.0`；golangci-lint 2.13.2。完整命令、退出码、日志位置见对应 `*.result.json`，日志已脱敏压缩到 `logs/`。

| 验证 | 结果 | 主证据 |
| --- | --- | --- |
| 普通全量测试 | 11,125 pass / 0 fail / 4 skip | delivery-test-normal.result.json |
| unit 全量测试 | 19,120 pass / 0 fail / 8 skip | delivery-test-unit.result.json |
| integration 全量测试 | 11,851 pass / 0 fail / 5 skip | delivery-test-integration.result.json |
| 身份/Key/团队定向 race | 通过 | delivery-auth-race.result.json |
| S05 真实 PostgreSQL/Redis integration race | 通过 | delivery-storage-race.result.json |
| 普通 / unit / integration lint | 既有 1 / 285 / 19 项；无新增或删除 | lint-comparison.json |
| 依赖门禁正反夹具 | 12 个构建集合/场景全部符合预期，夹具已删除 | dependency-fixtures.json |
| 普通、embed、Linux 服务构建 | 通过 | delivery-build*.result.json |
| 两个维护命令 | 构建通过；jwtgen 进程级参数/签名/无 worker 验证通过 | delivery-build-jwtgen、delivery-build-cleanup；integration TestS02ProcessModes/jwtgen-minimal |
| 前端测试 / 构建 | 通过 | final-test-frontend、final-build-frontend.result.json |
| 真实前端产物 embed 测试 | 101 条通过事件，无跳过 | delivery-test-embed.result.json |
| Wire 再生成 | 无差异；未生成 Ent | wire-reproducible.json |

父测试和子测试分别计事件，不能把上表数量理解为相互独立的业务契约。实际关键测试按契约归集于 [contract-events.json.gz](contract-events.json.gz)。`go list` 的普通、unit、integration、wireinject、embed、e2e 与 Darwin/Linux 选择见 [build-selections.json.gz](build-selections.json.gz)。跳过原因、外部供应商/硬件/TLS 限制见 [verification-limitations.json](verification-limitations.json)，跳过不计行为通过。

三个 lint 退出码均为 1，原因是冻结的既有诊断。已按源文件、规则、完整消息和源码上下文逐项匹配，并保留原六项 unit depguard 违规；没有扩大忽略规则。全量最终测试包含禁用钉钉登录的实际契约、Google 官方验证器的本地 JWKS/签名、软件 WebAuthn 认证器的真实 SDK/数据库/Redis 链，以及 standard/simple 的 SIGTERM 关闭顺序。

## 相关历史修复

| 编号 | 原问题 | 本阶段最小修复与验证 |
| --- | --- | --- |
| H01 | 认证快照浅复制，来源、缓存和请求共享可变状态 | 独立复制 map/slice/指针与嵌套策略；原 HEAD 隔离测试失败，修复后及 race 通过 |
| H02 | 复合分组快照遗漏已有 OpenAIFastPolicy | 补齐 v40 现有字段的双向投影，不升级版本或新增缓存协议 |
| H03 | 管理 Key 的请求先重置消费，再验证/更新分组 | 先验证；配置、授权与重置参加原同一事务；真实 PG 失败回滚验证 |
| H04 | refresh 轮换忽略 DEL 结果/错误，竞争或删除失败仍签发 | 原 Redis key 取得唯一消费权后再签发；保持先校验用户/version/binding 的时机 |
| H05 | pending 事务读取后无条件消费，两个事务都可成功 | UPDATE 增加 consumed_at IS NULL；竞争失败返回原 consumed 错误；真实 PG 交错验证 |
| H06 | 邮箱 OAuth 已创建用户，后续 Begin 失败缺少补偿 | 将原尽力删除补偿覆盖 Begin 失败；不扩大原事务、不改变其他补偿/副作用策略 |

原 HEAD 的非测试生产源码保持不变，复现来源及测试摘要见 [original-auth-reproduction-sources.json](original-auth-reproduction-sources.json) 与各 `original-*.result.json`。本阶段机械搬迁、测试夹具和时钟装配中出现的失败也单独保留，最终结果不以旧试验失败或仅编译作为行为结论。

## 交接资料

- [文件、声明、构建条件、消费者与 Wire](file-ownership.json.gz)：544 个文件条目，含旧兼容入口和未迁消费者；混合文件按声明记录。
- [事务及字段写权限](transactions-and-fields.md)、[十二组资金写入](funding-writes.json)：明确资金操作和业务编排归属，未迁闭合事务仍保留原边界。
- [生命周期与后续阶段](lifecycle-and-handoff.md)：S06—S16 的剩余职责和兼容退出项。
- [精确依赖许可](dependency-exceptions.json)、[规则差异](dependency-rules.json)：无悬空文件许可，原 service/handler 规则保持。
- [私有入口处置](private-entry-dispositions.json)：无消费者删除，只有测试消费者的转接留在对应标签的测试文件。
- [冻结项核对](invariants-final.json)、[差异检查](diff-check.json)、[日志清单](archive-manifest.json)：HEAD/index、SQL/Ent、原阶段和其他任务内容的核对证据。

本阶段未提交或推送。回退按原子子步骤恢复代码、Wire、规则与文档；回退 H01—H06 会恢复相应历史风险。没有数据库或缓存格式降级步骤。平台额度协调仍只覆盖单服务进程，认证 pubsub 的双实例兼容验证不扩大该范围。
