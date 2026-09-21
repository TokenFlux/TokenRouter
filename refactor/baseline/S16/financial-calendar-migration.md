# 显式日期与平台额度存储清理

## 所有权

- 删除 `pkg/timezone/timezone.go`，其中唯一的全局初始化与日志迁至 `app/bootstrap/timezone.go`。默认上海时区、非法值错误文本、写入 `time.Local` 的顺序和 UTC 偏移格式保持。
- `Calendar` 保留日期计算和显式时刻的偏移格式化；门禁只允许实际使用的 `fmt`、`strconv`、`strings`、`time`，不再允许日期包导入日志或其他 I/O。
- app 注入同一 Calendar 到平台额度存储、普通结算、任务成员预占、Key 按日计数和站点公开投影。订阅日窗口与展示接收日历参数；原周/月滚动、一次性日额度和尾段规则未合并。
- `site` 分别接收日期对象和配置展示名称，保留 `Local` 标签与实际解析地区的区别。API 与 embed 仍各自在原位置计算当前 UTC 偏移。
- 旧 service/handler 的剩余兼容入口显式捕获 `time.Local`；它们尚待随旧应用图删除。本记录不将这些消费者称为已完成原生装配。

机械改绑清单：`financial-calendar-callers.json`、`timezone-global-callers.json`。手写修改包括上述原生 Store/Options、app provider、bootstrap 初始化、日期测试和门禁。

## 平台额度兼容入口

- app 直接提供 `billing.UserPlatformQuotaRepository`，删除 repository ProviderSet 中旧构造器。
- 删除 `repository/user_platform_quota_repo.go` 及恒等 `user_platform_quota_service_adapter.go`；消费者见 `platform-quota-native-callers.json`。相关集成测试直接构造 billing/postgres，并继续用原事务与数据库夹具。
- `user_platform_quota_adapter_test.go` 的四项测试只证明同一个 fake 经恒等函数返回后能收到参数与错误，随恒等函数删除。真实写入、参数及错误仍由原 `TestUserPlatformQuotaRepository_*`、`TestUpsertForUser_*` 与 S04 协调测试验证，未删除存储业务断言。
- 两项无消费者的 NextDailyReset/NextWeeklyReset 兼容函数删除，实际预检已使用注入日历；只在模块内部使用的过期窗口归一化恢复包内可见。

## 日期测试归属

- `TestInit`、`TestInitInvalidTimezone`、`TestTimeNowAffected` 从日期包迁至 bootstrap，保留有效时区、非法输入和 time.Now 偏移断言；另验证默认值和失败不改已有时区。
- 原 Today、StartOfDay、Truncate、DST、StartOfWeek 测试仍在日期包，使用独立 Calendar。原初始化前单调时钟断言改为直接检查零配置 Calendar。
- 原 billing HTTP 北京日/周边界测试改为显式日历，保持断言；订阅测试覆盖 DST 23/25 小时日界。
- `billing/postgres/calendar_test.go` 验证普通结算和成员重置的 SQL 参数；真实 PostgreSQL 的 `tests/integration/billing_calendar_test.go` 验证日窗口、周/月累计及外层回滚。
- Key 测试验证原计数 key 与 24 小时 TTL；site 测试同时核对 API 与 HTML 注入的时区标签和偏移。

## 已取得验证

- 全量 `go build ./...` 通过，见 `financial-calendar-build-recheck.json`。最初漏传自由函数参数、旧测试调用及重复 import 的编译日志保留，均属迁移接线问题，已经修正。
- 日期及直接消费者 unit：13,989 条通过、2 项既有跳过，见 `financial-calendar-unit-final.json`。两项为真实 OpenAI token 比较与既有 WS 分支，不计行为通过；此检查点先于四项恒等转接测试删除。
- 原生日期定向 unit race：351 条通过，无实际跳过，见 `financial-calendar-race.json`。
- 真实 PostgreSQL/Redis 与进程 integration race：74 条通过，无失败或跳过，见 `financial-calendar-integration-race.json`。包含 CLI setup、standard/simple、SIGTERM、监听失败、维护命令与新增平台额度日期事务契约。
- CLI 夹具修复获用户单独授权，原 race 资料及授权记录见 `cli-process-race-observation.md`。未延伸历史问题排查。

后续公开标签、兼容入口删除、完整 lint、生成稳定性及完整性复核结果按实际执行追加。S16 保持实施中，不把本批证据视为最终验收。
