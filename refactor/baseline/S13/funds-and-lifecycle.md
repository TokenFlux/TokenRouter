# 任务资金与运行拥有关系

本文件只登记 S13 已接入的调用与阶段边界。完整符号和调用方见 `source-facts.json.gz` 与 `final-symbol-references-*.json.gz`；后者按构建集合生成。

| 入口 | 资金/存储拥有者 | 事务与后置行为 |
| --- | --- | --- |
| `creative.Public.CreateRun` | `creative.Funding.Reserve` → app 唯一 `billing.Funds` | 任务建行保持原独立阶段。预占的去重、付款用户、订阅/余额、Key/成员预记及任务分配由一次 SQL Tx 提交；随后保存临时输入、入队并推进原 provisioning/outbox。 |
| 创作供应商成功 | `creative/postgres.RecordProviderOutcome` | 在一次 Ent Tx 中记录成功时间、账号、输出元数据与 settle outbox；不写图片或 prompt。Redis 保存有独立短预算，不重新推理。 |
| 创作完成/释放 | `creative.Results` → `creative.Funding.Capture/Release` | 先完成幂等资金操作，再完成交付终态及原用量记录。交付丢失与供应商成功分开；取消状态保持取消，已确认服务仍捕获一次。日志失败不重扣。 |
| `batchimage.Public.Submit` | `batchimage.Funding.Reserve` → app 唯一 `billing.Funds` | 沿用建行、预占、条目、上传/提交、保存供应商引用、入队的原阶段，不持 SQL 事务调用供应商。预占投影参与同一资金 Tx。 |
| 批量索引/完成/取消 | `ProviderProcessor` / `Settlement` / `Funding` | 固定任务账号与 provider，读取历史价格及分配快照。索引与最终资金效果依旧分阶段；capture/release 重放保持原请求 ID，恢复不重新提交供应商任务。 |

## 通用资金参与

- `billing.TaskReference` 使用注册的 scope、任务 ID、显式原预占 ID。未知 scope 在 Begin 前拒绝。
- `app/billing.go` 分别创建 `creative/postgres.FundingParticipant` 与 `batchimage/postgres.FundingParticipant`；两者只操作收到的本次 `*sql.Tx`，不 Begin/Commit/Rollback，不发布失效。
- `billing/postgres` 保留整个事务的死锁重试；每次尝试重新取得 Tx 与参与者。内部函数改用 Task 名称，原重试日志字符串、savepoint、指纹与 request ID 字符串保持，映射见 `funding-symbol-map.json`。
- 生产任务直接取得 app 的 `billing.Funds`，不再经过旧 `CreativeEntity` 命令分支。旧零值/命令兼容仅为已有接口及测试保留，S15/S16 清理，不能新增生产消费者。
- 普通结算、支付/推广、身份、Key/成员配置等其余资金入口沿用前阶段所有权；S13 不重新迁移或扩大这些原子范围。

## 生命周期

| 资源 | 唯一运行拥有者 | 退出责任 |
| --- | --- | --- |
| 任务提交、下载与管理 HTTP | app `TaskRequestsAndDownloads` | 在 HTTP 退出后封闭新调用，等待完整调用/输出；其 StopOrder 为 16，先于 task worker 和共享存储。 |
| 创作任务、扩缩容、outbox/transient 恢复 | `creative.CreativeWorkerRuntime` | 构造不启动；停止不可逆，封闭领取/扩容，取消运行 context 并等待在途。生成阶段用户/账号槽由 scheduler.Lease 逆序释放，不占用结果保存/结算阶段。 |
| 批量领取、delayed、stale 与资金恢复 | `batchimage.Runtime` + `BatchImageWorker` | 重复 Stop 共用完成/首次结果；失去或无法确认 token 时取消处理。ACK、心跳、重排及清理只通过拥有者句柄。 |
| 批量清理 | `batchimage.Runtime` + `Cleanup` | 闭包持有固定运行信号；立即停止不能清空后台仍使用的 done。 |
| provider 注册表 | app 构造，四类批量消费者共享 | 不再由提交、下载、清理、worker 各建一份生产表。供应商实现无独立的第二套任务状态机。 |
| SQL、Redis、日志、HTTP 池 | 既有 app lifecycle | 任务阶段未完成时不提前报告共享依赖关闭成功。HTTP 五秒与后台总计三十秒预算保持独立。 |

## 固定修复及证据

- B01—B03：`runtime-fixed-race`、`fixed-regressions` 与后续任务 race；反复启动、先停止后启动、并发停止和清理立即停止。
- B04：`tasks-integration-race` 的真实 Redis 旧 token/接管场景；不会删除继任者 active。
- B05：`s13-fixed-integration`、`tasks-integration-race` 与结果用例测试；成功事实/outbox 回滚、输出保存失败、结果丢失及资金重放。临时输出故障和永久缺失分别处理。
- T01：并行仓储替身补齐同步/副本，B05 新增输出读取与写入共用同一替身屏障；`creative-executor-race-recheck` 三轮 270 条通过。
- 真实 HTTP 屏障：`task-activity-race-recheck`；超时后不停止 worker/Redis，也不接纳新调用。
- 冒烟替身新增成功事实端口：`task-core-unit-recheck`，238 条通过；原主动终止的缺端口执行单独保留，不算通过。

## 保留边界与回退

Redis 和 PostgreSQL 仍分阶段恢复，不保证跨存储原子性或多个服务进程协调。未持久化成功事实前的进程崩溃不承诺恢复已发生的供应商结果；正常停止超时也不能称为 drain 成功。真实收费供应商未连接，协议/认证/对象流采用本地夹具。

回退 B05 前必须处理或登记成功已确认但尚未交付/结算的任务；不得删除成功事实、资金去重或 outbox。撤销 B01—B04 会恢复相应重开、漏等、信号清空与旧 token 风险。无需数据库或缓存格式降级。
