# 缓存及构造转接收敛

本批仅清理已核对消费者的转接，并迁移 INTERNAL 500 计数的唯一实现。未改变 Redis key、TTL、Lua、错误、发布订阅或生命周期行为。

| 原 repository 文件 | 最终实现及消费者 |
| --- | --- |
| `gemini_token_cache.go` | `account/rediscache.NewOAuthTokenCache`；app 原装配不变，7 个测试文件直接调用 |
| `rpm_cache.go` | `scheduler/rediscache.NewRPMCache`；旧入口无消费者 |
| `user_rpm_cache.go` | `scheduler/rediscache.NewUserRPMCache`；旧入口无消费者 |
| `openai_403_counter_cache.go` | `account/rediscache.NewOpenAI403CounterCache`；健康 Redis 测试直接调用 |
| `timeout_counter_cache.go` | `account/rediscache.NewTimeoutCounterCache`；健康 Redis 测试直接调用 |
| `temp_unsched_cache.go` | `account/rediscache.NewTempUnschedCache`；健康 Redis 测试直接调用 |
| `internal500_counter_cache.go` | 实现原样迁入 `account/rediscache/internal500_counter_cache.go`，app 的 `cacheProviders` 直接构造 |
| `leader_lock_cache.go` | `infra/redis.NewLeaderLockCache`，app 对剩余 `service.LeaderLockCache` 消费者做 Wire 接口绑定 |
| `aliyun_captcha_verifier.go` | `identity/provider.NewAliyunCaptchaVerifier`；旧入口无消费者 |
| `tencent_captcha_service.go` | `identity/provider.NewTencentCaptchaVerifier`；旧入口无消费者 |
| `turnstile_service.go` | `identity/provider.NewTurnstileVerifier`；旧入口无消费者 |
| `passkey_session_store.go` | `identity/rediscache.NewPasskeySessionStore`；旧入口无消费者 |
| `totp_cache.go` | `identity/rediscache.NewTotpCache` 与所属具体类型；旧入口无消费者 |
| `refresh_token_cache.go` | `identity/rediscache.NewRefreshTokenCache`；原轮换、失败和 OAuth 补偿测试直接调用 |
| `proxy_latency_cache.go` | `egress/rediscache.NewProxyLatencyCache`；旧入口无消费者 |
| `tls_fingerprint_profile_cache.go` | `egress/rediscache.NewTLSFingerprintProfileCache`；旧入口无消费者 |
| `tls_fingerprint_router_cache.go` | `egress/rediscache.NewTLSFingerprintRouterCache`；旧入口无消费者 |

原测试名称和行为断言保留。健康测试用两个所属模块的实例继续验证共享旧 Redis 状态，新增 INTERNAL 500 迁移契约验证原 key、24 小时初始 TTL、后续递增不续期及重置。跨平台刷新 CAS 和取消测试继续使用真实 PostgreSQL/Redis。

## 已执行证据

- `cache-direct-unit.json`：定向 unit，441 条通过事件。
- `cache-cleanup-normal.json`：定向普通，450 条通过事件。
- `cache-direct-complete-integration-race.json`：token 与健康兼容，21 条通过事件。
- `health-leader-direct-integration-race.json`：INTERNAL 500、健康与 leader lock，9 条通过事件。
- `identity-cache-direct-integration-race.json`：身份 refresh 轮换与补偿，3 条通过事件。
- 上述集合无失败或实际跳过，集合重叠且包含父子测试，不相加。
- 首次 `oauth-cache-direct-integration-race.json` 的选择表达式未覆盖 Vertex/Antigravity 名称，后续 complete 集合已补全；首次结果不作为全部平台验收。
- `retired-bridge-fixture-{normal,unit,integration}.log`：每组均命中两条预期 depguard 拒绝，覆盖原许可文件名与新文件下的非法子包。夹具位于仓库外，验证后已删除。
- `cache-cleanup-full-integration-lint.log` 保存本批首次接线诊断；修复 Wire 构建标签及角色规则重叠后，结果另存 `cache-cleanup-final-*-lint.log`，不覆盖初次日志。
- `cache-cleanup-final-{normal,unit,integration}-lint.log` 三组完整 lint 均退出 0。
- `cache-cleanup-wireinject-final-lint.log` 专项退出 0；原手写 Wire 对已迁模块缺少的 7 项许可按文件/import 补齐。首次互斥未执行与重试诊断分别保留，不计为通过。
- `go build ./...` 退出 0；Wire 再生成与本批生成结果一致，`git diff --check` 通过。

legacybridge 的 17 条失效规则、已删除目录排除和 import 许可已移除；`retired-legacybridge` 全局规则拒绝旧包及子包。app 新缓存绑定只有单文件精确许可，不向其他文件开放 service 依赖。

本批不代表 S16 完成。旧实体、业务 context、其他 repository/service/handler 转接及最终应用图仍需按原计划逐项收敛。
