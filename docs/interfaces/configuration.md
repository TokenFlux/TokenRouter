# 配置边界

本文描述进程配置、首次初始化、数据库运行时设置、领域配置和前端构建变量的来源及优先级。它用于新增配置项或修改管理设置时判断存储位置、校验时机和是否需要重启，不枚举 `Config` 中的每个字段或复制部署样例。

## 章节导航

- [配置分层](#配置分层)：先确定一个选项属于哪一层。
- [进程配置来源](#进程配置来源)：修改默认值、YAML 或环境变量时读取。
- [首次初始化](#首次初始化)：修改 setup 和安全密钥引导时读取。
- [数据库运行时设置](#数据库运行时设置)：修改管理员设置和热更新时读取。
- [领域配置](#领域配置)：避免把业务实体塞入通用键值设置。
- [前端变量](#前端变量)：区分构建/开发变量与后端运行时配置。
- [新增配置检查](#新增配置检查)：实现与测试清单。

## 配置分层

| 层 | 例子 | 权威来源 | 生效方式 |
| --- | --- | --- | --- |
| 进程基础设施配置 | server、database、Redis、日志、CORS、JWT、worker/queue、连接池和硬安全开关 | `config.Config`，由默认值/YAML/环境变量加载 | 通常启动时读取，修改后重启 |
| 引导与持久安全密钥 | 初始 DB/Redis、管理员创建、JWT secret、安装锁 | setup 流程、`config.yaml`、`security_secrets` | 仅首次安装或启动引导阶段 |
| 数据库运行时设置 | 注册、OAuth、SMTP、面板限流、step-up、部分调度/超时、展示和功能开关 | `settings` 表 + `SettingService` | getter/cached snapshot/on-update，按设置实现热生效 |
| 领域配置 | 用户、团队、API Key、分组、渠道、账号、套餐、支付实例 | 对应 Ent schema/service | CRUD 事务后失效领域缓存 |
| 前端构建/开发配置 | API base、WS base、dev proxy、dev port | Vite `VITE_*` | 构建或 dev server 启动时注入 |

同名概念可能跨层，但要有明确接管语义。例如进程 `security.trust_forwarded_ip_for_api_key_acl` 提供启动默认值，数据库设置可以在运行时覆盖安全客户端 IP 策略；读取方必须使用运行时 snapshot，而不是持续读取旧的 struct 字段。相反，数据库地址和 Redis 连接池不能通过管理后台热切换。

<a id="configuration_sources"></a>
## 进程配置来源

`config.load` 使用 Viper，最终优先级为：

```text
环境变量
  > 选中的 config.yaml
  > setDefaults 注册的代码默认值
```

配置文件选择规则为：

1. `CONFIG_FILE` 非空时只使用该显式文件路径。
2. 否则按顺序搜索 `DATA_DIR`（若设置）、`/app/data`、当前目录、`./config`、`/etc/sub2api` 中的 `config.yaml`。
3. 文件不存在允许继续使用默认值和环境变量；文件存在但 YAML 无法读取/解析则启动失败。

环境变量把点分键转成大写下划线，例如 `database.host` 对应 `DATABASE_HOST`，`gateway.max_body_size` 对应 `GATEWAY_MAX_BODY_SIZE`。`setDefaults` 还负责把所有 struct 键注册进 Viper，使纯环境变量部署能被 `Unmarshal` 看到；新增字段不能只加 `mapstructure` tag 而不注册默认/可达键。定价进程配置中的 `pricing.override_file` 是可选本地 JSON 补丁，按字段浅合并覆盖远程目录和回退文件，修改后在重启或下一次目录下载时生效；文件缺失/非法只记录告警并保留原目录。少量变量有显式绑定或专用解析：`ENABLE_SERVER_TIMING`，逗号分隔的 `SERVER_TRUSTED_PROXIES` 和 `SECURITY_FORWARDED_CLIENT_IP_HEADERS`，以及受兼容条件约束的旧 WeChat 变量。

国产供应商周期用量监控属于启动时进程配置 `gateway.cn_providers`。`monitor_enabled` 默认关闭；开启后默认每 10 分钟运行一次、并发 4、单账号探测超时 20 秒、整轮预算 300 秒，余额临时停调阈值 `balance_threshold` 默认 `0.5`。对应键为 `interval_minutes`、`concurrency`、`probe_timeout_seconds` 和 `round_timeout_seconds`，修改后需要重启。管理员手动查询不受监控开关影响；自定义中继的自动监控还要求启用并命中 `security.url_allowlist.upstream_hosts`。

时区的优先级是标准 `TZ`、兼容 `TIMEZONE`、配置文件、默认 `Asia/Shanghai`。`TZ` 非空时必须显式覆盖 `TIMEZONE`，使容器运行时、应用本地日统计和 PostgreSQL 连接时区使用同一部署者选择；无效 IANA 名称仍在启动校验中失败。

创作台（Creative Studio）属于启动时进程配置 `creative`：功能与队列开关、临时数据 TTL（`transient_ttl_seconds`，默认 1800 秒）、上传与 prompt 限制（`max_asset_bytes` 默认 32 MiB、`max_total_input_bytes` 默认 64 MiB 且不得小于单文件上限、`max_prompt_chars` 默认 8000）、上游执行参数（`execute_timeout_seconds`、`max_execute_attempts`）和 `creative:queue:*` 队列键/TTL 都在启动校验，修改后需要重启。创作台每次任务固定生成一张图片，预占价格按所选尺寸单价计算。与 `batch_image` 不同，`enabled` 与 `queue_enabled` 默认开启，但临时存储与队列依赖 Redis，Redis 不可用时任务创建 fail-close。完整键清单见 `deploy/config.example.yaml` 和[创作台](../domains/creative_studio.md)。

创作台的 worker 数量不属于进程配置，而是数据库运行时设置 `creative_worker_count`：默认 128，仅允许大于 0 的整数，不设置硬上限；缺失或历史脏值按 128 处理。管理员在“功能特性 - 创作台”保存后，本实例立即扩缩 worker 池，缩容采用优雅排空，不中断正在执行的上游请求；该设置无需迁移，也不通过公开设置接口暴露。

加载完成后会做字符串规范化、枚举回退、派生默认、文件读取和完整 `Validate`。无效安全 header、URL、数值范围、模式组合或必要 secret 会让启动失败；不应等到某个请求首次使用时才发现。自动生成的 TOTP key 只适合开发，`EncryptionKeyConfigured=false` 会阻止后台把 TOTP 当成生产可用配置。

环境变量优先于 YAML，因此排查“文件修改不生效”时先检查容器环境。不得在日志、错误或管理响应中输出数据库密码、JWT/TOTP secret、OAuth secret、对象存储 secret 或账号凭据。

## 首次初始化

setup 使用 `DATA_DIR > 可写 /app/data > 当前目录` 选择 `config.yaml` 和 `.installed` 的位置。正常情况下，配置文件或安装锁任一存在就不会重新开放初始化；`SKIP_SETUP` 是部署者的显式旁路。修改这套判断必须保持“删除一个文件不能远程强制重装”的防重置边界。

交互或 `AUTO_SETUP` 流程测试 PostgreSQL/Redis、执行迁移、只在空数据库中创建初始管理员、以 `0600` 写入配置并创建安装锁。已有管理员或已有普通用户时不会覆盖密码。自动 setup 的 `DATABASE_*`、`REDIS_*`、`ADMIN_*`、`SERVER_*`、`JWT_*` 和时区变量是生成初始文件的输入；生成后常规启动仍走统一 config loader。

主服务使用 `LoadForBootstrap`，只在引导阶段允许 `jwt.secret` 暂时为空。数据库 repository 初始化会从 `security_secrets` 读取既有 JWT secret，或原子生成并持久化一个新 secret，然后重新执行完整配置校验。多个实例不能各自使用临时随机 JWT key；显式配置与数据库已有 secret 不一致时，以已持久化的安全边界处理，避免滚动部署让会话随机失效。

## 数据库运行时设置

`settings` 是 `key/value/updated_at` 表，删除键表示恢复该 getter 的默认语义。`SettingService` 负责类型解析、范围/组合校验、敏感值保留、批量原子写入和更新后的缓存通知；handler 只负责 HTTP binding、权限、审计和响应。

运行时设置包括注册与邮件验证、第三方登录、SMTP、TOTP/session binding/step-up、登录协议、面板限流、部分冷却与流超时、数据共享、支付展示以及各类功能开关。不同 getter 的回退可能来自代码常量或 `config.Config`，不能假设所有缺失键都等价于 `false`。

`registration_email_domain_quota_enabled` 控制邮箱白名单非空时是否允许非白名单域名按可注册主域名限量注册，缺失或读取失败均按关闭处理，以保持严格白名单的安全默认。该设置通过公开设置和 SSR 注入提供给注册前端用于选择本地白名单预检策略，但最终准入仍由服务端在注册事务内重新读取并判定；管理更新请求省略该字段时必须保留当前值，不能把兼容请求解释为显式关闭。

`user_email_change_enabled` 控制已有真实邮箱身份的用户能否在注册后换绑主邮箱，默认关闭；缺失、读取失败或认证服务未接入设置服务时都必须拒绝换绑。它不影响尚无真实邮箱用户的首次邮箱绑定，也不阻止用户验证并绑定与当前记录相同的邮箱。开关通过公开设置和 SSR 注入控制个人资料页入口，但安全边界由服务端同时在发送换绑验证码和提交换绑时执行；管理更新请求省略该字段时保留当前值。

`google_one_tap_enabled` 是独立于 `google_oauth_enabled` 的数据库运行时开关，默认关闭。部署者先为现有 Web 类型 Google OAuth Client ID 登记每个前端 Authorized JavaScript origin，再显式开启；生产 Origin 必须使用 HTTPS，本地开发只允许 localhost/loopback HTTP。公开设置只有在 One Tap 开关和完整 Google OAuth 配置同时有效时才返回 `google_one_tap_enabled=true` 及非敏感 `google_oauth_client_id`，任一条件不满足时按关闭并返回空 Client ID；Client Secret 始终只留在服务端和受掩码保护的管理设置中。首页与登录页共用这组公开设置，旧 HTML 注入缓存缺失新字段时按关闭处理。

`home_featured_models` 保存首页「已支持的 AI 模型」板块的精选模型 ID 列表（JSON 数组，最多 12 个，按数组顺序展示），在管理端“系统设置 - 通用设置”的「首页模型展示」卡片维护，选项来自公开模型广场分组。它通过公开设置接口和 SSR 注入同时下发，首页按 ID 在市场分组中解析模型；列表为空或全部解析不到时，首页回退到按服务商类别聚合的默认卡片。该设置可热更新、不需要迁移（读路径容忍缺键按空列表处理）；管理更新请求省略该字段时保留当前值，写入前会去掉空白项、去重并拒绝超长列表。

`creative_model_settings` 保存创作台允许使用的全局生图模型与能力白名单（JSON 数组），每项为 `group_id`、`model` 和 `operations`，通用能力值仅允许 `generate`、`edit`、`inpaint`，实际平台交集为 OpenAI 三项、Gemini/Grok 的 `generate`/`edit`。默认值为 `[]`，空列表表示创作台没有任何可用生图模型；不需要数据库迁移。管理 PUT 省略字段时保留旧值，显式发送 `[]` 时清空；保存校验正整数分组 ID、非空模型名、至少一项能力和分组+模型唯一性，并按可解析的实际分组平台移除 Gemini 的 `inpaint`，移除后无能力的条目删除；无法解析的历史分组暂时保留。读取损坏 JSON 或读取失败按空列表处理并记录日志，设置不建立外键，因此失效分组/账号配置会保留并在恢复后重新生效。

`usage_ranking_enabled`、`usage_ranking_sort_by`、`usage_ranking_show_total_tokens`、`usage_ranking_show_requests`、`usage_ranking_show_actual_cost` 与既有 `usage_ranking_limit` 共同控制用户侧用量排行。排行行的 `user_id` 表示付款主体；团队 Key 的请求按 `billing_user_id` 归到团队 Owner，Usage 明细中的 `user_id` 仍表示实际行为成员。它们保存在 `settings` 表，不需要迁移或重启；排行请求在查询前一次读取这些键，因此保存后立即作用于本实例，跨实例通过同一数据库读取最终一致。缺失新键按升级兼容默认：排行开启、按 `total_tokens` 排序、三项均显示、名次上限为 20。排序值只允许 `total_tokens`、`requests` 和 `actual_cost`；所选指标必须保持可见，其它字段可独立关闭。

管理端“通用设置”的用量排行卡片和公开设置都会返回这组有效配置。关闭总开关后，用户侧导航和路由不再提供入口，`GET /api/v1/usage/ranking` 也必须在查询前返回 `403`；它不影响管理员仪表盘的消费排行。关闭显示字段时，用户排行响应必须省略对应行字段及总计，关闭 Token 还要省略输入、输出和缓存 Token 明细，不能只由浏览器隐藏。普通明细和预聚合查询都按所选指标大于零入榜，并使用其余指标和付款主体 ID 作为稳定并列顺序。

高级调度器的归属分为两层：每个 Group 的 `scheduler_type` 是领域配置，明确选择 `basic` 或 `advanced`；网关通用设置保存高级模式的运行参数，包括 `advanced_scheduler_sticky_weighted_enabled`、`advanced_scheduler_subscription_priority_enabled`、`advanced_scheduler_lb_top_k`、各 `advanced_scheduler_weight_*`、两个独立的 `advanced_scheduler_ewma_*_alpha` 以及 `advanced_scheduler_sticky_escape_*`。它们在“网关设置 - 通用设置”编辑，使用短 TTL 的进程缓存读取。数值留空时继承 `gateway.advanced_scheduler` 的进程默认值；sticky escape 开关和两个阈值也支持热更新。不存在 `advanced_scheduler_enabled` 全局开关，缺失参数只回退到进程配置默认值，不能改变任意分组的模式。

管理 Group API 还接受 `advanced_scheduler_overrides` 作为稀疏对象，仅在 `scheduler_type=advanced` 的实际调度中使用。创建缺省为 `{}`；更新时省略字段保持原对象，传 `{}` 清除全部覆盖，字段内未出现的值继续继承全局设置。`false` 与 `0` 不等于未设置，都会作为显式覆盖保存；合并后的七项基础评分权重全部为零也是有效配置，此时评分相同的候选按账号全局优先级和账号 ID 稳定排序，不会静默恢复全局权重。合并后的基础权重和完整权重总和都必须是有限值，写入会拒绝导致溢出的稀疏覆盖；运行时若读到历史异常对象，权重回退到全局有效值。该字段随认证快照缓存并提升快照版本；公开用户分组接口不会返回它或 `scheduler_type`。

管理员账号高级调度评分诊断会逐项返回最终参数和来源：`group_override` 优先于 `global_runtime`，后者缺失时为 `process_default`。该返回只解释当前实时评分，不保存历史快照；它不会反向启用分组、高级调度器或任何平台专属策略。

进程配置的默认参数位于 `gateway.advanced_scheduler`，包含 `lb_top_k`、`score_weights`、`ewma_error_rate_alpha`、`ewma_ttft_alpha` 与粘性逃逸阈值。两个 alpha 要求 `0 < alpha <= 1`；sticky escape 的 TTFT 阈值必须为正数，错误率阈值必须在 `0..1`，显式错误率 `0` 表示任意正错误率即可触发逃逸。旧的 `gateway.openai_ws.lb_top_k`、`gateway.openai_ws.scheduler_score_weights.*` 和 `gateway.openai_scheduler.sticky_escape_*` 均不再兼容，启动校验会明确拒绝；管理设置请求中的 `openai_advanced_scheduler_*` 或旧全局开关也会返回弃用错误，而不是被静默忽略。OpenAI 配额自动暂停仍是 OpenAI 专属设置，不属于通用高级调度参数。

Grok 文本转发有三项数据库运行时设置：`grok_default_text_model`、`grok_cross_client_model_map_enabled` 和 `grok_default_base_url_mode`。默认模型与跨客户端开关共同发布进程级模型映射快照；当前开关只在 Grok 分组的 Anthropic Messages 派发阶段生效，将 Claude 模型 ID 映射到默认文本模型，不改写 Responses 或 Chat Completions 中的其他模型。base URL 模式只在账号未保存显式端点时生效，可选 CLI 代理、公共 API、`us-east-1`、`us-west-2` 和 `eu-west-1`。这些设置可热更新，不覆盖账号显式 URL，也不改变媒体/Voice 的官方端点选择。

`account_scheduling_thresholds` 是整体替换的 JSON map，只允许 OpenAI、Anthropic 和 Grok 的 1-100 整数，100 表示关闭对应平台自动停调；账号可在自身凭据中覆盖。管理设置的部分更新省略该字段时必须保留数据库值和进程缓存，不能把前端初始默认值当成显式更新。

`gateway.grok` 属于启动时进程配置。`password_auth_enabled` 默认关闭并控制邮箱密码到 SSO/OAuth 的敏感入口；Free OAuth 本地软门禁由 `free_quota_soft_gate_enabled`、`free_quota_token_limit`、`free_quota_soft_gate_percent`、`free_quota_window_hours` 和 `free_quota_stats_cache_seconds` 控制。所有数值在启动时校验，修改后需要重启；统计缓存 miss 或查询故障按 fail-open 处理，但不能放宽 OAuth state 一次性消费、凭据持久化或 URL 信任边界。

验证码同样属于数据库运行时设置。Turnstile、腾讯天御与阿里云验证码 2.0 三者互斥。腾讯天御启用时必须同时具备正整数 `CaptchaAppId`、`AppSecretKey`、腾讯云 `SecretId` 和 `SecretKey`，并选择 `cn` 中国站或 `intl` 国际站；站点决定前端 SDK、构造函数形式、控制台入口和服务端票据校验 endpoint，`CaptchaAppId` 与云密钥必须来自同一站点，缺失或非法站点按 `cn` 回退。阿里云启用时必须具备 Scene ID、Prefix、AccessKey ID、AccessKey Secret 及 `cn` 或 `sgp` 地域。公开设置只返回各提供方的启用状态、站点和渲染所需的非敏感参数；管理响应只返回 secret 的“已配置”标记，空白更新保留原值，审计仅记录字段发生写入而不记录内容。腾讯与阿里云 Web SDK 所需的脚本、连接、iframe、worker 和样式来源由默认 CSP 与运行时 CSP 补全逻辑共同维护，覆盖自定义旧策略时也不能遗漏，其中阿里云静态资源允许 `https://*.alicdn.com`。Google GIS 同样由默认策略与旧自定义策略增强共同允许：`script-src` 仅加入 `https://accounts.google.com/gsi/client`，`frame-src`/`connect-src` 加入 `https://accounts.google.com/gsi/`，`style-src` 加入 `https://accounts.google.com/gsi/style`。tf CLI 网页导入在 `connect-src` 中只允许 `http://127.0.0.1:43110` 到 `43119` 十个精确 Origin；代码默认策略、旧自定义策略增强和 `deploy/config.example.yaml` 必须同步，不能扩大为端口或局域网通配符。完整边界见 [tf CLI 网页导入](tf_cli_web_import.md)。

SMTP 的测试连接与实际发送共用同一建连路径和超时。`smtp_use_tls=true` 先按隐式 TLS 连接；仅当服务端以明文 SMTP 问候响应时改用强制 STARTTLS，服务端不支持升级时直接失败，不能明文发送认证。`smtp_use_tls=false` 保留机会式 STARTTLS，并在服务端不提供扩展时允许现有明文语义。两条路径都在认证成功后忽略非标准 QUIT 响应，因此后台连接测试与实际发信能力保持一致。

热路径设置必须使用以下一种明确策略：

- 原子 snapshot，更新成功后立即替换；安全客户端 IP 策略属于此类。
- 有 TTL 的进程缓存或 stale-while-revalidate，避免每请求访问数据库。
- Redis/跨实例失效通知，使多个进程最终看到同一设置。
- 仅在启动时加载；此类设置应记录需要重启，不能在 UI 暗示即时生效。

设置写入失败时不得先更新内存 snapshot；数据库成功后若通知失败，要保留可观测错误并允许 TTL/重载恢复。敏感字段在更新请求中省略表示保留原值，不能因管理页面返回掩码或空字符串而清空 secret。验证码设置读取应一次批量取得提供方开关和密钥，避免同一请求在多个 getter 间看到不一致配置。

运行时设置的部分更新必须区分“省略”与“显式清空”。`UpdatePaymentConfig` 只写入请求中实际提供的字段；省略支付方式列表、可见支付路由、手续费 map 或其它指针字段会保留已有值，显式传入空切片、空 map、空字符串或 `false` 才按对应字段规则清空或关闭。系统设置 handler 即使分阶段写入同一批支付设置，也不能用第二阶段的缺省值覆盖第一阶段已持久化的配置。

## 领域配置

结构化、有关联和独立生命周期的业务配置使用专用表：

- 分组拥有平台、倍率、能力、回退、策略和主动可用性探测参数；渠道拥有模型映射与价格；账号拥有上游凭据、代理和调度状态。
- Subscription Plan、支付 provider instance、API Key、团队和用户属性都有各自不变量与审计路径。
- Ops/pre-aggregation 等既有进程 hard switch 又有运行时开关时，进程开关是上限：数据库不能开启部署者显式硬关闭的能力。

不要把需要唯一约束、外键、状态机、列表查询或原子计数的数据编码成一个巨大 settings JSON。相反，只被单个进程组件启动时读取的连接池大小也不应新建业务表。

## 前端变量

Vite 在构建/dev server 启动时读取 `VITE_API_BASE_URL`、`VITE_WS_BASE_URL`、`VITE_DEV_PROXY_TARGET` 和 `VITE_DEV_PORT`。默认 API base 是 `/api/v1`，dev proxy target 是 `http://localhost:8080`，dev port 是 `3000`。

`VITE_*` 会进入客户端 bundle，绝不能放 secret。生产内嵌前端的动态品牌、功能和公开认证配置通过后端设置注入或 API 获取；它们与 Vite 构建变量是不同通道。改变后端 public URL 时，还要核对 OAuth callback、邮件链接、CORS/CSP 和反向代理路径，不能只改前端 base。

## 新增配置检查

- 判断它属于进程、bootstrap、数据库运行时、领域实体还是前端构建层，并选择唯一权威来源。
- 进程字段添加 `mapstructure`、代码默认值、环境变量可达性、规范化和 `Validate`；必要时更新部署样例。
- 明确环境变量名称、YAML 点分键和优先级；新增兼容旧键时规定弃用与新键优先条件。
- 明确是否热生效、是否多实例一致、缓存如何失效以及失败时 fail-open 还是 fail-close。
- secret 使用 write-only/掩码语义并覆盖日志脱敏；不能暴露到 `VITE_*` 或普通设置响应。
- 更新配置和环境变量可达性、校验、setup 往返、SettingService/handler 以及部署冒烟测试。

相关文档：[HTTP 接口边界](http_api.md)、[系统架构](../architecture/system_architecture.md)、[部署与迁移](../operations/index.md)、[接口目录](index.md)。
