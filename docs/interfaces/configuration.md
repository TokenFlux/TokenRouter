# 配置

本文说明进程配置、首次初始化、数据库运行时设置、领域配置和前端构建变量的来源与优先级。新增配置项或调整管理设置时，据此判断存储位置、校验时机及是否需要重启。本文不逐一列举 `Config` 字段，也不复制部署样例。

## 章节导航

- [配置分层](#配置分层)：确定配置项所属层级和生效方式。
- [进程配置来源](#configuration_sources)：文件查找顺序、加载管线和各模块启动参数。
  - [配置版本与自动升级](#config_versioning)：v1 到 v2 升级、备份写回与离线迁移命令。
  - [显式环境引用](#config_env_references)：`${VAR}` 语法、告警规则和环境变量清单。
  - [站点链接与客户端 API 地址](#site_link_addresses)：`frontend_url`、OAuth 回调与 `api_base_url` 的分工。
  - [已退役和改名的键](#retired_and_renamed_keys)：v2 移除或改名的配置项速查。
- [首次初始化](#首次初始化)：setup 锁定规则、自动/交互安装和 JWT secret 引导。
- [数据库运行时设置](#runtime_settings)：热更新策略、保存事务和各功能域运行时参数。
  - [bStats 安装统计](#bstats_reporting)、[社交分享](#社交分享)、[通知与 SMTP](#notification_delivery)、[搜索配置发布](#search_configuration)、[高级调度参数](#高级调度参数)。
- [领域配置](#领域配置)：业务实体专用表与进程/运行时设置的边界。
- [前端变量](#前端变量)：`VITE_*` 构建变量与后端公开配置的区分。
- [新增配置检查清单](#新增配置检查清单)：新增或迁移配置时的核对项。

## 配置分层

| 层 | 典型示例 | 来源 | 生效方式 |
| --- | --- | --- | --- |
| 进程基础设施配置 | server、database、Redis、日志、CORS、JWT、worker 与队列、连接池、硬性安全开关 | `config.Config`（默认值加版本化 YAML，环境变量须显式引用） | 启动时读取，修改需重启 |
| 引导和持久安全密钥 | 初始数据库与 Redis、初始管理员、JWT secret、安装锁 | setup 流程、`config.yaml`、`security_secrets` | 仅在首次安装或启动引导阶段 |
| 数据库运行时设置 | 注册、OAuth、SMTP、面板限流、step-up、部分调度和超时参数、展示与功能开关 | `settings.Store` 加各模块运行时读取器 | 通过 getter、缓存快照或更新回调按实现热生效 |
| 领域配置 | 用户、团队、API Key、分组、价格配置、提供商、套餐、支付实例 | 对应 Ent schema 与 service | 事务提交后使领域缓存失效 |
| 前端构建和开发配置 | API base、WS base、dev proxy、dev port | Vite `VITE_*` | 构建或 dev server 启动时注入 |

同一概念跨多层出现时须明确最终权威。例如进程配置 `server.client_ip.trust_forwarded_headers` 仅提供启动默认值，数据库设置可在运行时覆盖安全客户端 IP 策略，读取方使用运行时快照。相反，数据库地址与 Redis 连接池不支持后台热切换。

<a id="configuration_sources"></a>
## 进程配置来源

`config.load` 使用独立的 Viper 实例：配置文件值优先于代码默认值，环境变量只有通过 `${VAR}` 显式引用才参与常规加载，不按大写下划线名称隐式覆盖。

配置文件查找顺序：

1. `CONFIG_FILE` 非空时只选该路径，即使文件尚不存在。
2. 否则依次查找 `DATA_DIR`（非空时）、`/app/data`、当前目录、`./config`、`/etc/tokenrouter` 下的 `config.yaml`。
3. 仅找到 `/etc/sub2api/config.yaml` 时拒绝启动，提示迁移至新路径或设置 `CONFIG_FILE`。
4. 无配置文件且无旧环境变量时使用代码默认值，不创建文件；无配置文件但存在旧配置变量时，升级器在默认路径创建 v2 文件。

加载管线依次执行：版本检测与自动升级 -> 未知键检查 -> 显式引用展开 -> 字段解码 -> 规范化 -> 校验。未知键导致启动失败，并在已知时给出改名或删除提示。密码、密钥和代理凭据不写入迁移报告，报告只记录键名、变量名和处理原因。

<a id="config_versioning"></a>
### 配置版本与自动升级

顶层 `config_version` 为独立于应用版本的正整数，当前版本为 `2`。版本检测遵循 YAML 合并键和别名的有效值，显式键优先于合并值；缺少该字段的旧文件视为 v1；字符串、浮点数、零、负数或高于当前支持的版本均拒绝启动。

v1 自动升级行为：

1. 合并旧别名，按原优先级导入环境变量，执行时长和模式等语义转换，移除失效或原被忽略的键，最后迁至 v2 结构。
2. 敏感环境值以及 `DATABASE_HOST`、`REDIS_HOST` 保存为 `${实际变量名}`，原字面量引用语法会被转义。
3. 空的 OIDC 安全开关变量保留文件值（含 `false`）；仅文件未设置时写入默认值并保留显式标记。后续版本升级只转换文件，不再导入隐式环境配置。

v1 标量与写回约束：

- 标量语义：v1 中的 `null`、`~` 和留空标量继承默认值，`""`、`0`、`false` 和 `[]` 保留原义。整段配置为 `null` 时保留旧加载器对环境变量的遮蔽语义。YAML 合并先按字面键名选有效值再展开旧点分键；微信渠道等布尔转换保留非零数字规则，非法文本在写回前报错。
- 备份与写回：保留注释和键顺序，先以 `0600` 备份原始字节至 `<配置路径>.v1.bak`（同名已存在则用 `.v1.<UTC 时间>.bak`）。写回解析符号链接并替换实际目标，保留链接本身，备份仍位于传入路径旁；目标目录使用唯一临时文件名并沿用原权限，单文件挂载拒绝重命名时回退直接写入并同步。当前版本文件不会重写。
- 只读文件与离线迁移：不可写的旧配置导致启动失败，并在错误中给出离线命令：

```bash
./tokenrouter -migrate-config -config /path/config.yaml -out /path/config.v2.yaml
```

使用与原部署相同的环境变量执行该命令，再用输出文件替换挂载文件。`-out -` 输出到 stdout；源文件不变且不建备份；已有 v2 文件按原始字节输出；源和目标不可指向同一文件；迁移报告写入 stderr。

回退到旧二进制前必须先停止新进程并恢复原配置备份，旧二进制无法识别 v2 键名和 `${VAR}`。完整部署准备见[配置文件版本 2 升级说明](../operations/upgrade_notes.md#config_version_2)。

<a id="config_env_references"></a>
### 显式环境引用

引用仅在字符串值和列表元素中展开，键名保持原样：

| 写法 | 行为 |
| --- | --- |
| `${NAME}` | 读取变量；未设置时报错，已设为空时得到空字符串 |
| `${NAME:-default}` | 未设置或为空时使用默认文本，不递归展开默认文本 |
| `$${` | 输出字面量 `${` |

变量名只允许英文字母、数字和下划线，且首字符不能为数字；单值可含多个引用或混合普通文本。展开后保持字符串类型，再交由字段解码器转换整数、布尔和逗号分隔列表（如 `0123` 等密码保持原文）。报错只提示字段和变量名，不输出值。

```yaml
config_version: 2
database:
  password: ${DATABASE_PASSWORD}
```

告警与安装引用规则：

- 遗留变量告警：文件未引用的旧应用配置变量在每次启动时合并告警，仅列出排序后的变量名，不阻止启动。已引用变量不告警，安装输入仅在与当前有效配置不一致时告警，`ADMIN_*` 和安装开关不参与告警。
- 安装期引用：自动安装为非空的 `DATABASE_HOST`、`REDIS_HOST` 及安装凭据写入显式引用，每次启动须传入对应变量；手动安装向导写入主机字面值，可自行改为固定主机名或 IP。

仍由环境变量提供的输入：

- 启动前置路径：`CONFIG_FILE`、`DATA_DIR`。
- 安装开关：`AUTO_SETUP`、`SKIP_SETUP`。
- 首次自动安装（存储）：`DATABASE_HOST/PORT/USER/PASSWORD/DBNAME/SSLMODE`、`REDIS_HOST/PORT/USERNAME/PASSWORD/DB/ENABLE_TLS`。
- 首次自动安装（服务与密钥）：`ADMIN_EMAIL`、`ADMIN_PASSWORD`、`SERVER_HOST`、`SERVER_PORT`、`SERVER_MODE`、`JWT_SECRET`、`JWT_EXPIRE_HOUR`、`TOTP_ENCRYPTION_KEY`、`TZ`、`SETUP_MIGRATION_TIMEOUT_SECONDS`（安装向导监听地址同样使用 `SERVER_HOST`、`SERVER_PORT`）。
- 运行时标准输入：`HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`、`GIN_MODE`、`GOMEMLIMIT`、`SystemRoot`、`TZ`。
- 配置文件显式引用的变量。应用时区以 `timezone` 为准，`TIMEZONE` 不再用于常规加载或安装。

### 网关、时区与流保活

- 静态参数与策略边界：网关请求体上限、等待、切换上限和完成执行器参数在启动时加载。用户提示词替换由 `gateway/promptpolicy` 持有，错误规则由 `gateway/errorpolicy` 持有；错误规则写入、回源和发布在进程内有序协调，缓存一致性见[网关错误响应策略](gateway_error_policy.md#缓存一致性)。
- 时区：应用时区由 `timezone` 决定（默认 `Asia/Shanghai`），无效 IANA 名称导致启动失败。`TZ` 仅作为首次自动安装输入和标准运行时变量，v2 文件加载不隐式覆盖 `timezone`。
- 文本 SSE 保活：

| 配置键 | 默认值与范围 | 行为说明 |
| --- | --- | --- |
| `gateway.streaming.keepalive_interval_seconds` | 默认 `10` 秒，允许 `5` 到 `30` 秒，`0` 关闭 | 统一控制文本 SSE 下游空闲心跳间隔；Compact 专用 SSE 控制器复用此间隔。 |
| `gateway.streaming.data_idle_timeout_seconds` | 默认 `180` 秒，允许 `30` 到 `300` 秒，`0` 关闭 | 收到上游响应头后，按上游活动与 handler 字节写入中较晚时间判断正文停滞，超限后停发心跳。等待上游响应头期间受传输层响应头超时和首输出保护限制，不因该上限中断心跳（读取器自行处理超时）。 |

排队心跳和图片专用配置各自保留。提前提交 200 的条件、错误格式和心跳计数见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)。

### 请求记录与使用记录导出

- 请求记录：`request_log.retention_days` 控制请求摘要保留天数，默认 `30`。内存批写、失败处理和关闭排空见[请求 ID 与请求查询](../operations/request_lookup.md#request_storage)。
- 使用记录导出：`usage.export_tasks` 为重启生效的进程配置，控制后台 CSV 导出：

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `enabled` | `true` | 是否启用后台使用记录导出 |
| `retention_hours` | `24` | 本地导出文件保留小时数 |
| `max_active_per_user` / `max_active_per_admin` | `2` / `5` | 普通用户与管理员的活动任务（`pending`、`running`）上限 |
| `batch_size` | `2000` | 单批游标扫描记录数 |
| `worker_concurrency` / `worker_interval_seconds` | `2` / `10` | 后台导出并发数与轮询间隔（秒） |
| `task_timeout_seconds` | `3600` | 单任务执行超时（秒） |
| `dir` | 留空自动推导 | 显式值优先；留空依次取 `DATA_DIR/exports/usage`、`storage.dir/exports/usage`、`./data/exports/usage` |

### 插件运行时

`plugins` 为重启生效的进程配置（凭据或代理地址可用显式环境引用），插件信任模型和生命周期见[插件系统](../architecture/plugin_system.md)：

- 运行开关：`enabled` 默认 `true`，`max_running` 默认 `20`；`dev_mode` 默认 `false`，开启后才读取 `dev_dirs`（开发目录按 manifest 授权，不做安装包摘要校验）。
- 禁用行为：`enabled=false` 时不启动插件进程和 KV 维护，贡献 API、UI 描述和资源返回 `503 PLUGINS_DISABLED`，管理员仍可查看和卸载。
- 目录与索引：`dir` 显式值优先，缺省依次取 `DATA_DIR/plugins`、`storage.dir/plugins`、`./data/plugins`。默认官方索引为 `https://raw.githubusercontent.com/TokenFlux/tokenrouter-plugins/dist/v1/index.json`。
- 下载与代理：`download_proxy` 仅代理索引和包下载，`download_url_prefix` 用于 GitHub 下载前缀，`plugin_http_proxy` 传给子进程 `HTTP_PROXY`/`HTTPS_PROXY`。分发 URL 须为 HTTPS，代理支持 HTTP、HTTPS 或 SOCKS5。
- 来源与验签：内置来源保留管理员启用选择，宿主修改其 URL 时清除旧缓存。官方验签公钥内嵌于源码（空集合会拒绝内置索引），自定义来源固定公钥经管理接口保存，详见[插件运维](../operations/plugin_operations.md#distribution)。

### 模型目录与价格补充

- 目录同步：`model_catalog.catalog_url` 默认 `https://models.dev/experimental/v2.0/api.json`，`model_catalog.check_interval_minutes` 默认 `10` 分钟，自动同步价格和模型属性。加载时自动将 models.dev 官方旧路径 `/catalog.json`、`/api.json` 映射至 v2 并保留原模型类型筛选；自定义镜像地址原样保留，须返回 v2 提供商目录。
- v1 目录迁移：v1 升级自动将已知 Wei-Shaw、BerriAI 旧公共价格地址迁至 models.dev 并补上新下载域名，自定义地址不变；旧的相对资源路径和 `/app/resources/` 路径仅在原文件不存在时迁至同目录 `model_pricing_supplements.json`；旧目录缓存不再加载也不删除。
- 补充文件：官方补充经 `go:embed` 编入二进制（同条目含价格字段和 `attributes`）；`model_catalog.supplement_file` 默认空，指定同格式自定义补充。
- 字段覆盖优先级：
  1. 目录已有字段（包含零价）最高。
  2. 自定义补充文件次之。
  3. 内嵌官方补充兜底，填补有明确来源的缺失模型价格、媒体单价、生图文本输出价和明确商业规则。媒体尺寸表等复合字段整块择源，不跨来源合并。
- 校验与容错：补充文件须为 JSON 对象，顶层 `null`、非对象条目或非法价格字段会拒绝整次更新并保留已发布目录。自定义文件不存在时跳过该层；修改和删除在下一次周期检查或手动更新生效（含远程 304 或远程地址为空）。首次启动遇到损坏文件时仍发布带内嵌补充的离线目录并记录错误。
- `_billing_defaults`：保留节点，支持 `web_search_price_per_call`、`search_price_per_1k`、`audio_realtime_price_per_min`、`audio_tts_price_per_million_chars`、`audio_stt_price_per_hour`。自定义按字段叠加到内嵌默认价上，管理员价格配置优先（`0` 表示免费），该节点不出现在模型、价格候选和属性列表中。
- 字段兼容：本地 JSON 使用 `provider` 并兼容读取 `litellm_provider`（并存时以 `provider` 为准，含空值与 `null`）；报价来源由 `source` 和 `price_sources` 区分。
- 模型属性档案：`rules` JSONB 同时用于展示、客户端导出和请求参数处理，支持可选路线匹配条件，完整解析顺序见[模型属性配置](model_catalog_and_marketplace.md#model_attributes)。

### 国产供应商用量监控

`provider.usage_monitor` 为重启生效的进程配置：

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `enabled` | `false` | 是否开启周期用量监控（不影响管理员手动查询） |
| `interval_minutes` | `10` | 巡检周期（分钟） |
| `concurrency` | `4` | 探测并发数 |
| `probe_timeout_seconds` | `20` | 单个提供商探测超时（秒） |
| `round_timeout_seconds` | `300` | 单轮总时间预算（秒） |
| `pause_below_balance` | `0.5` | 低于该余额阈值时临时停调 |

对自定义中继启用自动监控时，还须开启并命中 `security.url_allowlist.upstream_hosts`。

### 创作台和批量图片

创作台进程配置（`creative`，重启生效、启动校验；完整键见 `deploy/config.example.yaml` 和[创作台](../domains/creative_studio.md)）：

| 配置键 | 默认值 | 说明 |
| --- | --- | --- |
| `enabled`、`queue_worker_enabled` | `true` | 创作台功能与队列 worker 开关 |
| `transient_ttl_seconds` | `1800` | 临时数据（图片和 prompt）TTL（秒） |
| `max_asset_bytes`、`max_total_input_bytes` | `32 MiB` / `64 MiB` | 单文件与总输入上限（总上限不小于单文件上限） |
| `max_prompt_chars` | `8000` | 提示词最大字符数 |
| `execute_timeout_seconds`、`max_execute_attempts`、`creative.queue.*` | 见部署样例 | 上游执行超时、重试上限及 Redis 队列键与 TTL |

运行与并发约定：

- 创作台任务与并发：每任务固定生成一张图并按所选尺寸单价预占；临时存储和队列依赖 Redis，不可用时拒绝创建任务。并发数由数据库运行时设置 `creative_worker_count` 控制（默认 `128`，正整数无硬上限，缺失或脏值按 `128` 处理，不经公开接口暴露），保存后本实例热扩缩 worker 池，缩容优雅排空不中断在途请求。
- 批量图片（`batch_image`）：请求准入由分组和提供商统一协议配置控制，worker、恢复和清理随应用启动。`batch_image` 保留请求、下载、留存、队列和 Vertex 存储参数；未配置 GCS 存储桶时 Vertex 不参与新建作业选择，配置后启动校验桶名、区域和作业前缀。
- 运行时生命周期：创作台与批量图片运行时停止后不可再次启动，动态扩容创作 worker 也不会重开已停止的运行时。

### 产品名称兼容

- 默认标识：新安装默认使用 `tokenrouter` 作为数据库名、日志服务名、日志文件名和仪表盘缓存前缀。显式配置的数据库连接、日志路径和缓存前缀原样保留，升级不会重命名库或搬迁数据。依赖旧默认库名的部署升级前须设置 `DATABASE_DBNAME=sub2api`，Compose 部署还须保留原 `POSTGRES_USER` 和 `POSTGRES_DB`。
- 迁移 283：仅当站点名称、站点标题、发件人名称和支付商品前缀去掉首尾空白后恰好等于旧产品名时，改为 TokenRouter。自定义值、历史订单、邮件记录和已绑定 TOTP 密钥保持不变；新绑定 TOTP 显示 TokenRouter，旧绑定继续用原密钥验证。

<a id="site_link_addresses"></a>
### 站点链接与客户端 API 地址

| 地址来源 | 适用场景 | 解析与回退规则 |
| --- | --- | --- |
| `frontend_url` | 邮件链接、退订链接 | 数据库非空值优先，其次部署配置 `site.frontend_url`。退订链接每次生成时保留路径前缀并追加 `/api/v1/settings/email-unsubscribe`（公开入口须转发至后端）；两处均空时保留相对链接。 |
| 请求公开 origin | 微信登录与支付 OAuth 自动回调 | 使用当前请求公开 origin 拼接固定 `/api/v1/auth/oauth/...`，不读 `frontend_url` 或 `api_base_url`，反向代理负责可信 Host 与协议头；微信登录显式回调配置优先。此设计避免分域或带路径前缀时状态 Cookie 错位。 |
| 浏览器 `VITE_API_BASE_URL` | 管理页 LinuxDo/GitHub/Google/微信/OIDC 回调建议 | 默认 `/api/v1`，相对路径按当前浏览器 origin 解析；不覆盖已填回调字段。自定义反代路径时需保持对外回调路径和 Cookie Path 一致。 |
| `api_base_url` | 客户端接入展示与配置导出 | 可含模型 API 的 `/v1` 前缀，不参与站点链接和 OAuth 回调生成。修改上述地址不重写已发邮件或已存回调。 |

### MCP OAuth

MCP OAuth 公开地址沿用 `frontend_url`（数据库非空优先，其次 `site.frontend_url`），仅接受不含路径、查询、片段或用户信息的 HTTPS origin（本地开发允许回环 HTTP）。

- 生效与热更新：配置有效地址即自动启用，后台修改无需重启；每请求实时读取当前值，数据库读取失败时拒绝 OAuth 访问。origin 变更后旧连接须重新授权；地址缺失或不适用时可在 MCP 接入配置查看原因。
- 密钥引导：启动时始终在 `security_secrets` 生成或读取独立 OAuth 签名密钥，支持后续后台配好地址即刻启用；授权页使用已校验前端 origin。手工 MCP 令牌的固定 TOTP 加密密钥要求保持不变。端点和期限见 [MCP OAuth 应用授权](mcp_management.md#oauth_authorization)。

<a id="retired_and_renamed_keys"></a>
### 已退役和改名的键

| 旧键、旧路径或旧机制 | 当前状态与替代方案 |
| --- | --- |
| 环境变量隐式覆盖与 `TIMEZONE` | v2 仅支持 YAML 中 `${VAR}` 显式引用；应用时区改用 `timezone`（`TZ` 仅作自动安装输入和标准运行时变量） |
| `/etc/sub2api/config.yaml` 默认查找路径 | 仅存在该文件时拒绝启动，须移至 `/etc/tokenrouter/config.yaml` 等新路径或设置 `CONFIG_FILE` |
| 模型目录旧键与 Wei-Shaw/BerriAI 公共旧地址、`/app/resources/` 打包路径、`litellm_provider` | v1 自动迁至 `model_catalog.catalog_url`（models.dev v2）和 `model_pricing_supplements.json`；本地 JSON 改用 `provider` |
| 高级调度旧进程键、`openai_advanced_scheduler_*`、`advanced_scheduler_enabled` | 进程默认迁至 `scheduling.advanced`，运行时改用 `advanced_scheduler_*`；旧管理字段返回已弃用错误，无全局高级调度开关 |
| Responses WS 旧总开关、认证类型开关、强制 HTTP、新旧模式选择、协议版本开关，以及 HTTP 转 WS 预热/重试退避/批量刷新/存储恢复/日志采样 | v1 升级删除，v2 出现即拒绝；客户端 WS 准入改由分组协议控制，连接池在线参数归 `responses_ws` / `gateway.openai.responses_ws` |
| `batch_image` 旧启用开关 | 请求准入改由分组与提供商统一协议配置控制，worker、恢复和清理随应用启动；仅保留请求、下载、留存、队列和 Vertex 存储参数 |
| `grok_cross_client_model_map_enabled` | 已移除，管理设置传入返回 400（改用手动模型映射） |
| 站点名称、标题和副标题的 `_zh`、`_en` 字段 | 已移除，管理接口写入返回 `REMOVED_SETTING_FIELD`（改用 `site_texts`） |

## 首次初始化

setup 使用与主服务一致的配置文件查找规则（含 `CONFIG_FILE` 和 `/etc/tokenrouter`）。新文件写往 `CONFIG_FILE` 或数据目录 `config.yaml`，安装锁 `.installed` 位于数据目录。配置文件或安装锁只要存在其一即关闭初始化入口（`SKIP_SETUP` 为部署者手动跳过开关），确保删除单个文件无法远程强制重装。

交互式或 `AUTO_SETUP` 流程依次执行：

1. 测试 PostgreSQL 和 Redis 连接（使用实际值）。
2. 执行数据库迁移。
3. 仅在空数据库中创建初始管理员（不创建默认分组；已有管理员或普通用户时不覆盖密码）。
4. 以 `0600` 权限写入 v2 配置文件并创建 `.installed` 锁。

凭据写入与 JWT 密钥引导：

- 安装凭据格式：`AUTO_SETUP` 使用的 `DATABASE_*`、`REDIS_*`、`ADMIN_*`、`SERVER_*`、`JWT_*` 和 `TZ` 仅用于生成初始文件，非空数据库密码、Redis 密码、JWT 和加密密钥写为环境引用，未提供的 JWT 或加密密钥随机生成并持久化。HTTP 和 CLI 安装将输入存为字面量并生成稳定加密密钥。
- JWT secret 持久化：主服务通过 `LoadForBootstrap` 仅在引导阶段允许 `auth.jwt.secret` 暂空，引导时从 `security_secrets` 读取已有密钥或原子生成新密钥，再执行完整配置校验。配置文件值与库内持久化密钥不一致时以数据库为准，避免重启或升级导致既有会话失效。

<a id="runtime_settings"></a>
## 数据库运行时设置

`settings` 是一张 `key/value/updated_at` 表，删除键即恢复对应 getter 默认值。缺键时的回退值可能来自代码常量或启动选项，不一定等于 `false`。

运行时读取与热路径约定：

- 策略选型：热路径设置须明确采用以下之一：原子快照（如安全客户端 IP 策略）、带 TTL/stale-while-revalidate 进程缓存、更新通知失效重载，或仅启动加载（须注明需重启）。
- 全局 `ttft_mode`：位于网关通用设置，支持 `first_output`（首有效内容，新安装默认）和 `first_event`（首个非心跳上游事件）。每个 HTTP 请求或 WS 逻辑回合开始时固定有效值并沿用于重试；热更新只影响新请求，不重算历史也不改变 TPS。
- 预聚合与日志：预聚合控制器带 15 秒缓存和更新通知；运行日志按"应用 -> 持久化失败回滚 -> 清理 Reload"处理。
- 代次快照：网关 backend mode 快照正常缓存 60 秒、故障缓存 5 秒、回源预算 5 秒，发布时推进进程内代次阻止旧回源覆盖。HTML 注入缓存同样绑定进程内失效代次，响应 ETag 与 HTML 来自同一次渲染。
- 即时读取项：Antigravity 身份补丁每次请求读取，失败默认开启，空提示词使用回退值。

### 综合设置保存与部分更新

- 原子保存：`PUT /api/v1/admin/settings` 先进入实例内更新保护，再校验整理系统设置、认证默认值、Fast 策略和支付设置并执行单次原子批量写入。提交前失败不发布任何状态或通知。
- 提交后应用失败：若必要运行时应用失败，返回 `SETTINGS_APPLY_FAILED` 且 metadata 标明 `persisted=true` 与失败模块，不假回滚也不自动重写重试。
- 专用入口与通知：专用设置按"校验 -> 批量原子写入 -> 刷新缓存 -> 调用 `Store.NotifyUpdated`"执行。单实例内同步通知订阅者，注销函数幂等（阻止未领取回调，已领取可完成一次）。
- 版本与公开读取：版本字段按应用版本赋值且 JSON 可省略；运营内容维护持久化 `revision`，事务内冲突返回 409（综合设置读取保存原始快照供各模块校验）。公开 API、HTML 注入和 CSP 一次批量查询，OAuth secret 不进入前端。
- 部分更新：`UpdatePaymentConfig` 区分"省略（保留原值）"与"显式清空（空切片、空 map、空串或 `false`）"，分阶段写入时后阶段缺省值不覆盖前阶段已持久化配置。
- 敏感字段与容错：敏感字段在管理页返回掩码或空串，提交省略或空白保留原值。写库失败保持内存快照不变；写库成功但通知失败记录可观测错误，靠 TTL 或重载恢复。验证码设置一次批量读取，保证同请求一致。

<a id="bstats_reporting"></a>
### bStats 安装统计

- 开关与热生效：位于"系统设置 -> 功能开关"bStats 卡片，由综合设置 GET/PUT 读写，省略保留原值，保存后热生效。`bstats_enabled` 默认开启，`bstats_log_errors`、`bstats_log_sent_data`、`bstats_log_response_status_text` 默认关闭。
- 启停行为：关闭时取消并等待在途上报，重开后重新执行首次随机延迟；仅改日志选项不重置调度，关闭统计也不清空日志选项。
- 标识与预算：使用 bStats `server-implementation` 平台，项目 ID 固定为 `35120`。`bstats_server_uuid` 在首次启用时生成随机 UUIDv4 并持久化（不经管理或公开接口暴露，重启升级复用，随备份恢复）。读写或校验失败时暂停统计，不阻塞启动，启动与首次初始化各自最多等待 5 秒（调用方更短截止时间优先）。
- 上报范围：仅含随机实例标识、操作系统及版本、CPU 架构与核数、TokenRouter 与 Go 版本（`tokenrouter_version`、`go_version` 两个 Simple pie 图表）及协议元数据，国家分布由 bStats 按连接来源推导。账户、API Key、域名、提示词、请求记录、用量和支付数据均不上报；开启报文日志会把含 UUID 的完整报文写入服务端日志。
- 调度时序：参考官方 [MetricsBase v3.2.1](https://github.com/Bastian/bstats-metrics/blob/v3.2.1/base/src/main/java/org/bstats/MetricsBase.java)（含同版本 `MetricsConfig`、`SimplePie`，MIT 许可见 `backend/internal/bstats/LICENSE`）。首次延迟 `[3,6)` 分钟，第二次再延迟 `[0,30)` 分钟，此后每 30 分钟执行。
- 传输约束：HTTP 请求超时 10 秒，失败不即时重试，无手动触发或自定义频率；实现带 Go context 取消、有界响应读取和禁止重定向。

### 用户可见文案

站点文案使用 `site_texts`，其他独立运营文案通过 `localized_settings` 提交，详见[用户侧国际化](user_localization.md#content_owners)。`default_locale` 初始为 `en`；管理接口拒绝站点名称、标题和副标题的 `_zh`、`_en` 写入，返回 `REMOVED_SETTING_FIELD`。

### 社交分享

- 文案与回退：分享标题和简介通过 `site_texts.site_share_title`、`site_texts.site_share_description` 维护（沿用译文核对和版本冲突规则）。标题留空依次回退首页标题、站点名称，简介留空回退副标题。
- 封面上传：`site_share_image` 接受 data URL，支持 PNG、JPEG、WebP，上限 2 MiB、每边上限 4096 像素；选图仅改草稿，保存设置才发布。
- 存储与脱敏：校验后与派生只读 `site_share_image_meta` 同事务保存；省略保留现值，空字符串移除。封面随数据库备份恢复，无需独立文件目录；公开设置与 HTML 仅含文案、图片地址、格式和尺寸，不含 Base64。图片接口见 [HTTP 接口](http_api.md#social_share_image)。
- HTML 元标签：内嵌前端在原始 HTML 输出 Twitter Card（有封面用 `summary_large_image`，否则 `summary`）与 Open Graph 标签，按语言缓存并在响应时补齐绝对地址和含地址差异的 ETag，保存后失效缓存。地址优先取数据库 `frontend_url`，其次 `site.frontend_url`，均空时取请求来源（仅显式可信代理可经转发头改来源）。
- 开发与托管：Vite 开发服务器同样注入（未配前端地址时用本地开发端口）；独立静态托管须由服务端完成相同注入，前端 JS 补写无法替代。社交平台缓存旧预览不随保存立即刷新。

### 注册、邮箱和登录

| 设置键 | 默认值与故障语义 | 行为与暴露范围 |
| --- | --- | --- |
| `registration_email_domain_quota_enabled` | 默认关闭；缺失或读失败按关闭处理 | 邮箱白名单非空时，控制是否允许白名单外域名按可注册主域名限量注册。经公开设置和 SSR 注入供前端预检，最终由服务端在注册事务内重读判定；更新省略保留原值。 |
| `user_email_change_enabled` | 默认关闭；缺失、读失败或未接设置服务均拒绝 | 控制已有实际邮箱的用户能否换绑主邮箱（不影响首次绑定邮箱或验证绑定相同邮箱）。经公开设置和 SSR 控制资料页入口，服务端在发验证码和提交换绑时均校验；更新省略保留原值。 |
| `google_one_tap_enabled` | 默认关闭 | 独立于 `google_oauth_enabled`，须先在 Web 类型 Google OAuth Client ID 下登记 Authorized JavaScript origin（生产须 HTTPS，本地仅允许 localhost/loopback HTTP）。仅当 One Tap 与完整 Google OAuth 均有效时，公开设置才返回 `google_one_tap_enabled=true` 和 `google_oauth_client_id`，否则按关闭处理且返回空 Client ID；Client Secret 仅留服务端与脱敏管理设置。旧 HTML 注入缓存缺新字段时按关闭处理。 |

### 验证码与 CSP

Turnstile、腾讯天御和阿里云验证码 2.0 三者互斥，同时只能启用一个：

- 腾讯天御：启用须配置正整数 `CaptchaAppId`、`AppSecretKey`、腾讯云 `SecretId` 和 `SecretKey`，以及 `cn`（中国站，缺失或非法时的默认值）或 `intl`（国际站）。站点决定前端 SDK、构造函数、控制台入口与服务端验票端点，AppId 与云密钥须同站。
- 阿里云验证码 2.0：启用须配置 Scene ID、Prefix、AccessKey ID、AccessKey Secret 及 `cn` 或 `sgp` 地域。
- 脱敏与审计：公开设置只返回启用状态、站点和渲染所需非敏感参数；管理响应对 secret 只返回"已配置"标记，提交空白保留原值；审计只记写入行为，不记密钥内容。
- CSP 自动补全：默认 CSP 与运行时对自定义旧策略的增强共同维护以下来源：
  - 腾讯与阿里云 Web SDK 所需脚本、连接、iframe、worker 和样式来源（阿里云静态资源允许 `https://*.alicdn.com`）。
  - Google GIS：`script-src` 仅加 `https://accounts.google.com/gsi/client`，`frame-src` 和 `connect-src` 加 `https://accounts.google.com/gsi/`，`style-src` 加 `https://accounts.google.com/gsi/style`。
  - tf CLI 网页导入：`connect-src` 仅放行 `http://127.0.0.1:43110` 到 `43119` 这十个精确 Origin，禁止扩大为端口通配符或局域网；默认策略、旧策略增强和 `deploy/config.example.yaml` 须同步更新，详见 [tf CLI 网页导入](tf_cli_web_import.md)。

<a id="notification_delivery"></a>
### 通知与 SMTP

- 模块边界：`notification` 维护用户和管理员通知事件、模板覆盖、退订和投递去重（生命周期见[通知与邮件投递](../domains/notification_delivery.md)）；`identity` 维护验证码、重置令牌和 Redis 凭据，各入口自行决定"先存后发"或"先发后存"。
- 模板与失败：自定义模板缺失或无效时回退选定语言内置模板；SMTP 失败或结果不确定时自动流程停止重发。
- 单实例协调：同一投递 key 的读取、发送和成功标记在单实例内串行协调（不同 key 可并行），退订密钥首次生成同样在实例内协调。系统不提供跨进程恰好一次投递：SMTP 已接收但成功标记落库失败时，投递状态仍属不确定。
- SMTP 建连与超时：连接测试与实际发信共用建连路径和超时：
  - `smtp_use_tls=true`：优先隐式 TLS，若服务端回明文问候则切强制 STARTTLS；不支持升级直接失败，绝不明文发凭据。
  - `smtp_use_tls=false`：机会式 STARTTLS，不支持扩展时按明文发送。
  - 认证成功后忽略不标准 `QUIT` 响应。全程传递 context，取消即中止拨号、TLS 与在途 I/O，超时取请求截止时间与连接/I/O 上限较早者；`DATA` 成功后即记成功，后续取消或异常 `QUIT` 不触发重发。

<a id="search_configuration"></a>
### 搜索配置发布

`search.ConfigService` 持有唯一配置缓存、singleflight 和当前 Manager 注册表（编排与额度规则见[搜索编排](../domains/search_orchestration.md)）。

- 发布一致性：保存成功推进进程内发布代次（不落库、不写 Redis），阻止较早开始的回源或 Manager 构建覆盖新配置。保存、读取、展示和 provider 配置均深拷贝可变指针和 slice，隔离调用方修改。
- 密钥与代理：空 `api_key` 保留已存密钥；专用管理 GET/PUT 返回含 `api_key` 的配置与用量，`SanitizeWebSearchConfig` 生成去密钥副本。管理测试不扣额度；代理无法解析时跳过该供应商，不回退直连；提供商代理优先于搜索供应商代理。配置变更只影响 Brave 和 Tavily，不影响 Grok 和 AlphaSearch 原生搜索。

### 首页、创作台和用量排行

- `home_featured_models`：首页"已支持的 AI 模型"精选模型 ID 列表（JSON 数组，最多 12 项，按顺序展示），候选来自公开模型广场分组。经公开设置和 SSR 下发，为空或全解析失败时回退按服务商类别聚合的默认卡片。热更新、无需迁移（缺键视为空列表），更新省略保留原值，写入前去空白、去重并拒绝超长列表。
- `creative_model_settings` 格式：创作台全局生图模型白名单 JSON 数组（`{group_id, model, operations}`），允许 `generate`、`edit`、`inpaint`（OpenAI 三项，Gemini 和 Grok 仅 `generate`、`edit`），默认 `[]`，无需迁移。
- `creative_model_settings` 校验：PUT 省略保留旧值，传 `[]` 清空；校验正整数 `group_id`、非空 `model`、至少一项能力及 `(group_id, model)` 唯一，按当前可调度候选裁剪不支持操作（无剩余能力则删条目，无法解析的历史配置暂留）。损坏 JSON 或读失败按 `[]` 处理并记日志；无外键，失效分组或提供商恢复后重新生效。
- 用量排行键与归属：由 `usage_ranking_enabled`、`usage_ranking_sort_by`、`usage_ranking_show_total_tokens`、`usage_ranking_show_requests`、`usage_ranking_show_actual_cost` 和 `usage_ranking_limit` 控制（存 `settings` 表，每次查询前批量读取，无需迁移重启）。排行 `user_id` 为付款主体（团队 Key 按 `billing_user_id` 归属 Team Owner，用量明细 `user_id` 仍为实际成员）。
- 用量排行默认与约束：缺键默认开启、按 `total_tokens` 排序、三项全显、上限 `20`。排序键限 `total_tokens`、`requests`、`actual_cost`，且排序指标必须开启展示。
- 用量排行裁剪与入榜：关闭总开关后隐藏用户入口且 `GET /api/v1/usage/ranking` 查询前返回 `403`（管理员消费排行不受影响）。关闭某显示字段时服务端直接省略对应行字段与总计（关 Token 时一并省略输入、输出和缓存明细）。明细与预聚合查询均按排序指标大于零入榜，其余指标与付款主体 ID 作稳定并列序。

### 高级调度参数

高级调度配置分三层，优先级固定为 `group_override > global_runtime > process_default`：

1. 领域分组配置：分组的 `scheduler_type` 明确选 `basic` 或 `advanced`；另可配稀疏对象 `advanced_scheduler_overrides`（仅在 `scheduler_type=advanced` 生效）。
2. 数据库全局运行时参数（"网关设置 - 通用设置"）：由 `scheduler.SettingsRuntime` 以 5 秒 TTL 和 singleflight 读取，返回独立 map，批量读取失败降级逐键读取。数值留空继承进程默认值；无 `advanced_scheduler_enabled` 全局开关，缺参不改变分组模式。包含以下键：
   - `advanced_scheduler_sticky_weighted_enabled`、`advanced_scheduler_subscription_priority_enabled`、`advanced_scheduler_lb_top_k`。
   - `advanced_scheduler_weight_*`、`advanced_scheduler_ewma_*_alpha`、`advanced_scheduler_sticky_escape_*`（含开关和阈值，支持热更新）。
3. 进程配置默认值（`scheduling.advanced`）：含 `lb_top_k`、`score_weights`、`ewma_error_rate_alpha`、`ewma_ttft_alpha` 和粘性逃逸阈值。要求 `0 < alpha <= 1`，TTFT 阈值为正数，错误率阈值在 `0` 到 `1` 之间（`0` 表示任何正错误率均触发逃逸）。

分组稀疏覆盖（`advanced_scheduler_overrides`）与诊断规则：

- 写入语义：创建默认 `{}`；更新省略保留原对象，传 `{}` 清空全部覆盖；未出现字段继承全局设置，`false` 和 `0` 均为有效覆盖值。合并后七项基础权重全零有效，同分候选按提供商全局优先级和提供商 ID 稳定排序。
- 校验与缓存：合并后的基础权重之和与完整权重之和均须有限，溢出覆盖写入时拒绝，运行时读到历史异常对象回退全局有效值。该字段随认证快照缓存并推进快照版本；公开用户分组接口不返回它和 `scheduler_type`。
- 评分诊断：管理端高级调度评分诊断逐项报告最终参数及来源（`group_override`、`global_runtime`、`process_default`），仅解释实时评分，不落盘快照，也不改变分组或平台策略状态。OpenAI 配额自动暂停保持独立运行时设置。

### Responses 长连接参数

"网关设置 -> OpenAI -> 长连接与连接池"支持在线调整连接数、超时、消息大小和会话记录期限。数据库 `responses_ws` 保存覆盖对象，未覆盖字段回退进程配置 `gateway.openai.responses_ws`，来源和热更新时机见 [Responses 长连接与在线参数](openai_upstream.md#responses_ws_runtime)。

客户端 WS 准入由分组协议统一控制，连接池自身的预热与逐轮恢复继续按在线参数执行。

### 平台相关设置

- Grok 运行时设置：`grok_default_text_model` 仅用于允许省略模型的请求，不改写已有模型名；`grok_default_base_url_mode`（可选 CLI 代理、公共 API、`us-east-1`、`us-west-2`、`eu-west-1`）仅在提供商未配手动端点时生效，支持热更新，手动 URL 优先，媒体和 Voice 使用各自官方端点。
- Grok 密码登录开关：`provider.grok.password_auth_enabled` 为重启生效的进程配置，默认关闭，控制邮箱密码转 SSO 或 OAuth 的敏感入口。
- Grok 免费额度软门禁：`scheduling.grok_free_quota`（重启生效、启动校验）含 `free_quota_soft_gate_enabled`、`free_quota_token_limit`、`free_quota_soft_gate_percent`、`free_quota_window_hours` 和 `free_quota_stats_cache_seconds`。统计缓存未命中或查询故障按 fail-open 处理，OAuth state 一次性消费、凭据持久化和 URL 信任检查照常执行。
- `provider_scheduling_thresholds`：整体替换的 JSON map，允许 OpenAI、Anthropic、Grok、Kimi 和 Zhipu 的 `1` 到 `100` 整数（`100` 关闭该平台自动停调，提供商可在自身凭据覆盖）。管理设置部分更新省略该字段时保留数据库值和进程缓存，前端初始默认值不当作更新提交。

## 领域配置

具备关联关系、唯一约束、外键、状态机、列表查询或原子计数的结构化业务配置存放在专门表，不塞入大段 `settings` JSON；仅由单个进程组件启动时读取的参数（如连接池大小）留在进程配置中：

- 分组：倍率、客户端协议、回退目标、路由策略（模型映射、白名单和功能）和主动可用性探测参数。
- 价格配置：价卡和计费设置。
- 提供商：上游凭据、代理和调度状态。
- 订阅计划、支付 provider 实例、API Key、团队和用户属性：各自维护独立不变量与审计路径。
- 硬开关上限：Ops 和预聚合等同时具备进程级硬开关与运行时开关的功能，以进程开关为上限；部署者关闭后，数据库设置无法重新开启。

子系统配置边界：

- 支付配置：热刷新整体读取失败时保留旧注册表，首次读取失败允许后续重试；保持配置键、缺省值、旧密文和续接回退密钥兼容，订阅套餐经 billing 读取。
- 备份配置：完整数据库连接凭据只传递给归档适配层，不扩散到通用配置对象；S3 凭据按运行时设置读取。

## 前端变量

Vite 在构建和 dev server 启动时读取：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `VITE_API_BASE_URL` | `/api/v1` | 面板 API 基础路径 |
| `VITE_DEV_PROXY_TARGET` | `http://localhost:8080` | 本地开发代理目标 |
| `VITE_DEV_PORT` | `3000` | 本地开发服务器端口 |

`VITE_*` 会打包进客户端代码，只能放公开值。生产内嵌前端的动态品牌、功能和公开认证配置由后端 HTML 注入或公开 API 提供。调整后端公开 URL 时须同步核对 OAuth callback、邮件链接、CORS、CSP 与反向代理路径。

## 新增配置检查清单

1. 确定唯一权威层级：进程配置、bootstrap、数据库运行时设置、领域实体或前端构建变量。
2. 进程字段须补齐 `mapstructure`、代码默认值、规范化、未知键检查覆盖、`Validate` 和部署样例。
3. 明确 YAML 点分键、单位和默认值；改名、移动、删除或变更语义时递增 `CurrentConfigVersion` 并补充升级步骤、对照表与迁移测试（新增带默认值的键无需递增版本）。
4. 写明是否热生效、运行时快照更新时机、缓存失效方式及故障策略（fail-open 或 fail-close）。
5. 敏感字段须实现只写、掩码和日志脱敏，禁止进入 `VITE_*` 或普通设置响应。
6. 同步更新升级、显式引用、未知键、校验、setup 往返、设置读取器、HTTP 及部署冒烟测试。

相关文档：[HTTP 接口](http_api.md)、[系统架构](../architecture/system_architecture.md)、[部署与迁移](../operations/index.md)、[接口目录](index.md)。
