# S01 迁移与交接清单

[逐文件声明、import、hash 与消费者](inventory.json)；[各构建集合](file-build-selections.json)；[精确旧入口消费者](remaining-consumers.json)。

本次共有 167 个受影响 Go 文件（含删除项），最终 2717 个 Go 文件、127 个 Go 目录。Ent 与资金业务的所有权仍沿用 S00；本阶段只完成通用基础提取。

| 旧能力 | 当前实现 | 剩余职责/退出 |
| --- | --- | --- |
| `internal/util/logredact` | internal/pkg/logredact | 随消费者迁移，最迟 S16；直接消费者 8 个文件 |
| `internal/pkg/errors` | internal/pkg/apperror + server/httpx | S15/S16；新增核心不能使用数值 HTTP 错误入口；直接消费者 198 个文件 |
| `internal/pkg/response` | internal/server/httpx | 随 HTTP 消费者迁移，S15/S16 清理；直接消费者 86 个文件 |
| `internal/pkg/httputil` | internal/server/httpx；JSON 宽容修复仍在旧包 | S03 迁移协议规范化，S15/S16 清理转接；直接消费者 9 个文件 |
| `internal/pkg/ip` | internal/pkg/ipmatch + server/clientip；ACL 裁决仍在旧包 | S05 归入 apikey，HTTP 转接最迟 S15/S16 清理；直接消费者 28 个文件 |
| `internal/pkg/logger` | internal/infra/telemetry/logging | S02 迁配置转换；其余转接随消费者迁移，最迟 S16；直接消费者 163 个文件 |
| `internal/pkg/servertiming` | internal/infra/telemetry/timing | 随消费者迁移，最迟 S16；直接消费者 12 个文件 |
| `internal/pkg/httpclient` | internal/infra/httpclient | S06/S09 消费者迁移，最迟 S16；直接消费者 16 个文件 |
| `internal/pkg/proxyurl` | internal/infra/httpclient/proxy | S06/S09 消费者迁移，最迟 S16；直接消费者 4 个文件 |
| `internal/pkg/proxyutil` | internal/infra/httpclient/proxy | S06/S09 消费者迁移，最迟 S16；直接消费者 3 个文件 |
| `internal/pkg/tlsfingerprint` | internal/infra/httpclient/tlsfingerprint | S06/S09 消费者迁移，最迟 S16；直接消费者 62 个文件 |
| `internal/pkg/redissession` | internal/infra/redis/session | S09 授权用例迁移，最迟 S16；直接消费者 1 个文件 |
| `internal/util/urlvalidator` | internal/egress + infra/httpclient | S06/S09 替换使用方，最迟 S16；直接消费者 13 个文件 |

其他混合入口：

- `repository/http_upstream.go` 只保留旧配置与平台策略适配；OpenAI HTTP/2 回退状态在 S06 迁入策略所有者，Grok CLI Header/403 回退在 S09 迁入上游所有者。唯一连接池和释放机制在 infra/httpclient。
- PostgreSQL 连接、扫描、timing、错误识别和死锁重试已提取；InitEnt 的时区/迁移/密钥/simple 编排留 S02，Ent 事务 context 和业务错误翻译仍在存储适配层。
- AES 旧构造器保留十六进制配置解释与错误文本；infra/crypto 接收解码后的密钥。Redis 旧构造器保留配置投影。
- logger/config_adapter.go 与 timezone.Init 的应用装配归 S02；Calendar 先保持初始化前后时区和单调时钟兼容。
- `internal/middleware` 已无消费者并删除；路由装配中两个固定窗口构造 import 精确登记为 S02 退出项。HTTP 计数接口返回标量，避免 HTTP 引用 Redis 实现或另建通用 DTO 包。
- Wire provider 签名和生成结果未变；PanelRateLimiter 的调用点是普通路由装配，因此本次不执行 Wire/Ent 生成。

## 验证语义调整

- 平台 transport 测试通过正式 Do 契约观察实际客户端和 Options；不导出内部缓存条目。LRU/TTL 机制测试移入 infra，增加公开执行中的池满、失败释放及并发重复关闭测试。
- 旧限流 Allow 的前缀/剩余时间回退场景移为真实 Lua + 单条 PTTL 故障的测试；HTTP 测试改为计数接口替身；真实 Redis 继续验证 TTL、并发和 session 单次消费。
- 保留旧 PKCE 编码差异、错误类型身份、metadata 复制与请求体 64 MiB 截断语义，未夹带业务修复。

## S02 输入

优先接手配置投影、InitEnt 的初始化编排、logger 配置转换、timezone 全局初始化及路由中的技术实例构造。沿用现有资源实例和关闭顺序；不能重新创建第二套连接池、logger sink、timing key 或会话命名空间。对每个被迁文件同步删除其依赖例外，并保留 S00 的分组协议失败归属。
