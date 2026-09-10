# S01 依赖门禁输入

[dependency-baseline.json](dependency-baseline.json) 保存路径分类、全部精确 import 账本及逐文件过渡条目；[inventory.json](inventory.json) 保存其余旧包声明与 import。

## 分类与准入

- 新业务根包、protocol、upstream、infra 及 Adapter 从首次引入即分别匹配最终职责规则。
- payment 是尚未解耦的旧核心；server/config/setup/web/testutil 是保留路径但角色不同，不能一律套用业务核心禁令。
- pagination/timezone 为最终保留纯工具；其余 27 个 pkg 按实际归属逐步迁移，禁止整个 pkg 通配放行或整体按纯工具误伤。
- Ent 54 个路径及 migrations 保留；生成依赖见包清单，不对生成文件套用手写核心规则。
- 账本记录所有实际 import，不代表每个 import 都应做例外。只把与目标规则冲突且有用途的具体条目转为过渡规则；标准库/合法供应商 SDK/测试库继续按角色正常准入。

## 已有 depguard

保留 service-no-repository 和 handler-no-repository。现有 service 文件排除：ops_aggregation_service.go、ops_alert_evaluator_service.go、ops_cleanup_service.go、ops_metrics_collector.go、ops_scheduled_report_service.go、wire.go。这些排除不能扩展到新核心，也不能吞掉 unit 标签现有失败。

## 已核实的主要过渡项

| 源文件/职责 | 具体依赖 | 退出 |
| --- | --- | --- |
| payment/load_balancer.go | ent、ent/paymentorder、ent/paymentproviderinstance | S12.2 移入支付存储 Adapter |
| payment/load_balancer_test.go | ent | S12.2 随存储契约测试迁移 |
| payment/wire.go、wire_test.go | ent/config/Wire 的实际组合见 JSON | S12.2 应用绑定移 app |
| pkg/websearch/manager.go | Redis、proxyutil、net/http | S10.4 分离搜索配额与 provider/cache |
| pkg/xai/oauth.go 及 Redis 测试 | Redis、redissession、logredact、urlvalidator | S09.7 上游输入与外层 Redis 适配分离 |
| testutil/fixtures.go、stubs.go、redis.go | service/repository 与测试 Redis | 随业务迁移，S16 第3项 收尾 |

## S01 必须实际验证的拒绝场景

已登记旧文件的具体旧 import 可通过；同目录新文件的非法 import 被拒绝；旧文件新增未登记禁止 import 被拒绝。普通/unit/integration 分别验证，迁出后按目标职责重新匹配，不继承旧路径例外。

本阶段未改 lint 配置，也没有将 S00 发现的原有 depguard 失败改为豁免。
