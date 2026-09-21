# 显式日期装配与用量 HTTP 测试

## 生产调用

| 位置 | 本批变化 | 保留契约 |
| --- | --- | --- |
| `internal/app/calendar.go` | bootstrap 取得 Ent 后构造唯一 Calendar；由 Wire 表达顺序 | 默认时区及 `time.Local` 初始化顺序不变 |
| `internal/app/usage_http.go`、`public_usage.go` | 将 Calendar 传给用户、管理员、仪表盘和公开用量 handler | 各入口默认范围、用户时区回退和取时点不统一 |
| `internal/app/payment_http.go`、`promotion.go`、`team_http.go` | 日期对象传给支付、推广、团队 HTTP | 支付范围校验、推广包含最后一纳秒、团队仅服务端时区 |
| `internal/app/billing.go`、`billing_quota.go`、`team.go`、`payment_refund.go` | 已有日期端口复用共享 Calendar | 原时钟函数与每次调用时机、资金和事务算法不变 |
| `internal/app/pricing.go`、`account_management.go`、`account_oauth_usage.go`、`routing_http.go` | 注入 Calendar 的原取时/日界方法 | 定价、账号统计、市场的既有时点不变 |
| `internal/handler/team_handler.go`、`admin/team_handler.go` | 删除；app 直接构造原生团队 HTTP | 用户与管理员路由仍使用同一 `TeamService` |
| `internal/handler/admin/usage_handler.go`、`dashboard_handler.go` | 消费者为零后删除 | 生产早已由 app 直接构造原生 handler |

`pkg/timezone.Init`、全局日期入口和其他存储消费者尚未清零，本批不能作为 S16.1 完成证据。

## 测试迁移

`usage-native-http-imports.json` 逐文件记录本批原生测试依赖；三个值为 null 的转接文件已删除，其断言由原测试直接调用新模块承接。原测试名称、构建标签和业务断言保留。

| 测试 | 当前装配与断言 |
| --- | --- |
| `usage/httpapi/usage_handler_daily_test.go` | 原生 UsageService 与 KeyReference 只读替身；跨用户拒绝、范围和日汇总不变 |
| `usage/httpapi/usage_handler_request_type_test.go` | 原生 UsageLog/Repository；请求类型、团队和模型过滤、字段裁剪不变 |
| `usage/httpapi/usage_ranking_settings_test.go` | RuntimeSettings 直接批量读取设置；禁用、排序和隐藏字段不变 |
| `usage/httpapi/admin/dashboard_handler_*_test.go` | 原生 DashboardService；缓存分桶、过滤、排名和维度参数不变 |
| `usage/httpapi/admin/usage_handler_request_type_test.go` | 原生 UsageService/OpsService；时延与过滤断言不变 |
| `usage/httpapi/admin/usage_handler_search_users_test.go` | UserReference/消费者接口；已删用户标记与 IncludeDeleted 不变 |
| `usage/httpapi/admin/usage_cleanup_handler_test.go` | 原生 Options/CleanupService；错误替身保留 SQL 缺失身份并携带模块缺失错误 |
| `usage/postgres/usage_cleanup_repo_ent_test.go` | 同时核对 `sql.ErrNoRows` 与 `usage.ErrCleanupTaskNotFound`，确认 Adapter 的实际映射 |
| `usage/httpapi/dto/s15_mappers_usage_test.go` | 直接使用 UsageLog 和 DTO；原并行测试及所有序列化断言保留 |

新增 Calendar 契约覆盖春季 23 小时、秋季 25 小时、无效时区回退、独立处理器时区、明确偏移时间、推广闭区间以及团队忽略用户时区参数的旧行为。

普通/unit/integration lint 均为零。迁出测试删除旧 service/config 许可，三个已删夹具删除独立规则与排除项；保留其他原拒绝项。清理 HTTP 测试的 `database/sql$` 仅保留原错误链输入，不取得连接。

## 当前证据

- `native-auth-runtime-recheck.json`：解除本地监听限制后补验通过 25 条 unit race 事件；SQLite/miniredis 不是实际 PostgreSQL/Redis 集成证据。
- `calendar-expanded-normal.json`、`calendar-expanded-unit.json`：普通 383、unit 420 条通过事件；这些是测试迁移前的日期装配检查点。
- `usage-native-http-tests.json`：测试迁移后 113 条通过事件，含原生存储错误链断言。
- `usage-native-http-race.json`：测试迁移后相关 HTTP 定向 race 120 条通过事件。
- `calendar-native-normal-lint-recheck.json`、`calendar-native-unit-lint.json`、`calendar-native-integration-lint.json`：全量 lint 各退出零。
- `calendar-checkpoint.json`：计划正文、342 个 SQL、Ent 表定义、依赖与 S00—S15 冻结资料未变；索引为空。
- `checkpoint-full-normal.json`：普通全量测试退出 0，11,809 条通过、4 项既有跳过，无失败。

上述事件有父子和集合重叠，不相加；无测试包单列，不冒充行为通过。完整阶段验收和剩余旧图清理仍待完成。

## 用量存储后续批次

- `Store` 与 `AggregationStore` 的构造器显式接收 Calendar；五处事务内派生继续携带原实例的日期对象。普通查询、聚合 SQL 和 raw 回退的执行顺序与条件不变。
- `usage.Options.Calendar` 供 Dashboard 按日缓存使用；app 将同一对象传入 Options、查询及聚合存储。团队日统计的两个查询参与函数由 team/postgres 显式传入自身日期对象。
- usage 生产目录的全局 timezone 日期函数调用已清零；旧 repository 的测试兼容构造仍提供默认 Calendar，不宣称所有旧图或全项目时区转接已删除。
- `usage-store-calendar-files.txt` 记录构造调用及日期函数的机械改绑文件；手写装配、Options 和团队查询的后续变更以实际 diff 为准。
- 原纽约 SQL 时区测试与 Honolulu PostgreSQL 查询测试改为独立 Calendar，保留原查询断言和业务日条件。`calendar_test.go` 新增春秋两种 DST 下的事务日期继承，核对四条聚合 SQL、提交和 23/25 小时边界。
- `usage-store-calendar-normal.json`：首批普通 729 条通过；后续完整日期批次 `usage-store-calendar-unit.json`：792 条 unit race 通过，无失败或实际跳过。真实存储 race 与门禁结果追加到阶段执行记录。
- `usage-store-calendar-integration.json`：1,625 条通过、1 项原业务日条件跳过；CLI setup 夹具及父测试报 race，整组未通过，详见 [待确认记录](cli-process-race-observation.md)。保留已执行的存储事件，不以其掩盖进程测试失败。
- `usage-store-normal-lint-recheck.json`、`usage-store-unit-lint.json`、`usage-store-integration-lint-recheck.json`：本批最终全量 lint 为 0/0/0；仅为 app/usage 和 Qoder 存储夹具补齐精确 timezone import。
- `usage-store-build.json`、`usage-store-wire-stable.json`、`usage-store-wire-hash.json`：全量构建通过，Wire 重生成摘要一致。`new-source-diff-check.json`：40 个新增源码/阶段 Markdown 的差异空白检查通过。
