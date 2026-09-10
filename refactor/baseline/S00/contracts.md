# 契约测试与构建集合

完整实际入选及结果见 [contracts.json](contracts.json)，基线集合见 [build-selections.json](build-selections.json)，新增测试集合见 [increment-build-selections.json](increment-build-selections.json)。计数包含顶层测试及子测试，不按日志重复行累加。

| 契约 | 后续阶段 | 实际通过/失败/跳过证据 | 证据边界 |
| --- | --- | --- | --- |
| 资金精度与原指纹 | S04 | normal: 14/0/0；unit: 14/0/0；integration: 14/0/0 | 纯金额契约；普通/unit 已执行 |
| 普通请求幂等与订阅/余额分配 | S04 | integration: 8/0/0 | 真实 PostgreSQL |
| 任务预占、捕获、释放及并发限额 | S04、S13 | integration: 9/0/0 | 真实 PostgreSQL，包含订阅/余额和成员预占 |
| 退款审计失败、重试与并发 | S12.4 | refund-atomicity-final: 3/0/0；refund-atomicity-race: 3/0/0 | S00 新增；公开用例 + 真实 PostgreSQL/真实仓储，仅供应商 HTTP 替换；定向 race |
| 返利与外层事务 | S12.1 | integration: 3/0/0 | 真实 PostgreSQL；外层回滚和转账认领 |
| 平台额度镜像及窗口 | S04 | integration: 13/0/0 | 数据库和 Redis/替身层分开；通过事件对应文件判断 |
| 陈旧资料不能覆盖资金/额度 | S04—S06 | integration: 6/0/0 | 真实 PostgreSQL |
| 身份首次绑定与默认赠送 | S05 | unit: 7/0/0 | 现有 unit 身份/SQLite 路径；不充当 PostgreSQL 锁竞争证据 |
| 订阅时间链与 Key 改绑 | S04、S05 | normal: 15/0/0；unit: 23/0/0；integration: 16/0/0 | 按测试文件标注 unit 与 integration；跨模块真实回滚在迁移时继续覆盖 |
| 协议目录与单步转换 | S03、S06 | normal: 13/0/0；unit: 92/0/0；integration: 13/0/0 | 目录/路由规则及转换输出契约 |
| 真实输出、failover 与取消 | S09、S11 | normal: 9/0/1；unit: 10/0/1；integration: 9/0/1 | 区分前导事件与已输出；明确跳过子场景 |
| Qoder 断开后 usage 与释放 | S09.1 | normal: 7/0/0；unit: 7/0/0；integration: 7/0/0 | 生产请求链对应的现有接口测试 |
| 槽位释放与缓存快照 | S07 | normal: 32/0/0；unit: 37/0/0；integration: 33/0/0 | 取消释放/版本栅栏；非全量 race |
| 配置、setup、embed 与停机 | S02、S14、S15 | normal: 186/0/0；unit: 244/0/0；integration: 215/0/0 | 本地测试与构建；不宣称真实部署启动/恢复演练 |
| 分组存储与协议约束 | S03、S06 | integration: 2/37/0 | 已有失败：groups_protocol_policy_v1；不将未到达的 outbox 断言算作通过 |
| 禁用/移除功能的兼容响应 | S14、S15 | normal: 6/0/0；unit: 6/0/0；integration: 6/0/0 | 保留既有拒绝/不存在行为 |

## 关键测试定位

- TestUsageBillingRepositoryApply_DeduplicatesBalanceBilling、TestUsageBillingRepositoryApply_RequestFingerprintConflict：资金去重与冲突，integration 通过。
- TestUsageBillingRepositoryBatchImageMemberAllowanceSerializesConcurrentReserve：真实成员预占竞争，integration 通过。
- TestAffiliateRepository_AccrueQuota_ReusesOuterTransaction：外层事务回滚，integration 通过。
- TestPaymentRefundPostgresAtomicity：新增审计失败/重试、并发确认两个子场景，integration 与定向 race 通过。
- TestQoderGatewayStreamClientDisconnectStillCollectsUsage、TestQoderStreamReleaseDoesNotFireOnClientCancel：断开后的计费数据与释放约束，已执行通过。

## 标签与平台

| 集合 | 包数 | 测试文件数（原始基线） |
| --- | --- | --- |
| normal | 111 | 764 |
| unit | 111 | 1210 |
| integration | 111 | 847 |
| e2e | 112 | 767 |
| embed | 111 | 765 |
| wireinject | 111 | 764 |
| linux | 111 | 765 |

112 个磁盘目录与普通 go list 的 111 个包并不矛盾：internal/integration 仅在 e2e 标签下入选。S00 新增退款测试只进入 integration（当前增加 1 个测试文件），不改变普通/unit 编译集合。

Darwin 原生、Linux 非 Darwin 实现均完成服务构建；非 Darwin attestation 的运行行为仍需相应 OS 执行，不能从跨编译推断通过。wireinject 只核对源集合，没有重新生成 Wire/Ent。

## 已知覆盖限制

E2E 的 BASE_URL、CLAUDE_API_KEY、GEMINI_API_KEY 未提供；既有 backend/scripts/e2e-test.sh 缺失。供应商真实调用、本地凭据读取和 TLS 外部捕获服务的跳过项逐条保留在验证结果中。

search_truncate_test.go 复制截断实现而未调用实际 handler，不作为用户入口测试充分性证据；S05 迁移该入口时收紧。S00 优先补充了资金原子性的实际缺口，不复制现有测试套件。
