# 配置

本文描述进程配置、首次初始化、数据库运行时设置、领域配置和前端构建变量的来源和优先级。新增配置项或修改管理设置时，用本文判断该存在哪里、何时校验、是否需要重启。本文不逐个列出 `Config` 的字段，也不复制部署样例。

## 章节导航

- [配置分层](#配置分层)：先确定一个选项属于哪一层。
- [进程配置来源](#configuration_sources)：修改默认值或文件加载时读取。
- [配置版本](#config_versioning)：修改升级、备份或恢复行为时读取。
- [显式环境引用](#config_env_references)：修改变量引用或安装输入时读取。
- [首次初始化](#首次初始化)：修改 setup 和安全密钥引导时读取。
- [数据库运行时设置](#数据库运行时设置)：修改管理员设置和热更新时读取。
- [领域配置](#领域配置)：业务实体用专用表，不放进通用键值设置。
- [前端变量](#前端变量)：前端构建和开发变量与后端运行时配置的区别。
- [新增配置检查清单](#新增配置检查清单)：实现和测试时逐项核对。

## 配置分层

| 层 | 例子 | 来源 | 生效方式 |
| --- | --- | --- | --- |
| 进程基础设施配置 | server、database、Redis、日志、CORS、JWT、worker 和队列、连接池、硬性安全开关 | `config.Config`，从默认值和版本化 YAML 加载，环境变量需显式引用 | 通常在启动时读取，修改后需要重启 |
| 引导和持久安全密钥 | 初始的数据库和 Redis、创建管理员、JWT secret、安装锁 | setup 流程、`config.yaml`、`security_secrets` | 只在首次安装或启动引导阶段 |
| 数据库运行时设置 | 注册、OAuth、SMTP、面板限流、step-up、部分调度和超时参数、展示和功能开关 | `settings.Store` 加各模块的运行时读取器 | 通过 getter、缓存快照或更新回调，按各设置的实现热生效 |
| 领域配置 | 用户、团队、API Key、分组、价格配置、提供商、套餐、支付实例 | 对应的 Ent schema 和 service | 事务提交后让领域缓存失效 |
| 前端构建和开发配置 | API base、WS base、dev proxy、dev port | Vite 的 `VITE_*` | 构建或 dev server 启动时注入 |

同一个概念可能出现在多层，但哪一层说了算要写清楚。例如进程配置 `server.client_ip.trust_forwarded_headers` 提供启动时的默认值，数据库设置可以在运行时覆盖安全客户端 IP 策略；读取方使用运行时快照，启动时的 struct 字段在这之后就过时了。反过来，数据库地址和 Redis 连接池无法在管理后台热切换。

<a id="configuration_sources"></a>
## 进程配置来源

`config.load` 使用独立的 Viper 实例。配置文件中的值优先于代码默认值；环境变量只有通过文件里的 `${VAR}` 引用才参与常规加载，不再按大写下划线名称隐式覆盖字段。

配置文件查找顺序：

1. `CONFIG_FILE` 非空时只选该路径，包括文件尚不存在的情况。
2. 否则依次查找 `DATA_DIR`（非空时）、`/app/data`、当前目录、`./config`、`/etc/tokenrouter` 中的 `config.yaml`。
3. 只找到 `/etc/sub2api/config.yaml` 时拒绝启动，提示移动到新路径或设置 `CONFIG_FILE`。
4. 没有文件且没有可导入的旧环境变量时，使用代码默认值，不创建文件。没有文件但存在旧配置变量时，升级器在默认配置路径创建 v2 文件。

读取后先检查版本并执行升级，再检查未知键、解析显式引用、解码、规范化和校验。未知键会使启动失败，并在已知时给出改名或删除提示。密码、密钥和代理凭据不会写入迁移报告；报告只包含键名、变量名和处理原因。

<a id="config_versioning"></a>
### 配置版本与自动升级

顶层 `config_version` 是独立于应用版本的正整数，当前为 `2`。版本检测遵循 YAML 合并键和别名的有效值，显式键优先于合并值。缺少该字段的旧文件视为 v1；字符串、浮点数、零和负数会被拒绝。文件版本高于当前二进制支持的版本时，也会拒绝启动。

v1 升级先合并旧别名，再按原有优先级导入环境变量，执行时长和模式等语义转换，移除失效或原本被忽略的键，最后移动到 v2 结构。敏感环境值保存为 `${实际变量名}`；`DATABASE_HOST`、`REDIS_HOST` 也保存为引用，以便依赖容器更换地址后继续连接。原文件中的字面量引用语法会被转义。空的 OIDC 安全开关变量保留文件中的值，包括 `false`；只有文件未设置时才写入默认值并保留显式标记。后续版本的升级步骤只转换文件，不再导入隐式环境配置。

v1 中的 `null`、`~` 和留空标量继承默认值；空字符串、`0`、`false` 和 `[]` 保留各自含义。整段配置为 `null` 时，迁移保留旧加载器对环境输入的可见性，不会使原本被该配置段遮蔽的变量生效。YAML 合并先按字面键名选择有效值，再展开旧点分键。微信渠道等布尔值转换保留旧版的数字非零规则，非法文本会在写回前报错。

升级器保留键的注释和顺序，先将原始字节备份到 `<配置路径>.v1.bak`；同名备份已存在时改用 `.v1.<UTC 时间>.bak`。备份权限为 `0600`。写回会解析符号链接并替换实际目标，保留链接本身；备份仍位于传入的配置路径旁。临时文件在目标目录中使用唯一名称，进程中断留下的文件不会阻止相同 PID 的进程重试。写回沿用原配置权限；单文件挂载拒绝重命名时回退到直接写入并同步，原始备份用于恢复。当前版本的文件不会被重写。

不可写的旧配置会使启动失败，错误中给出离线迁移命令：

```bash
./tokenrouter -migrate-config -config /path/config.yaml -out /path/config.v2.yaml
```

使用与原部署相同的环境变量执行命令，再用输出文件替换挂载文件。`-out -` 输出到 stdout；源文件不变，也不创建备份。已有 v2 文件按原始字节输出，源和目标不能指向同一文件。迁移报告写入 stderr。

运行旧二进制前必须停止新进程，并恢复原配置备份。旧二进制不理解 v2 键名和 `${VAR}`。完整部署准备见[配置文件版本 2 升级说明](../operations/upgrade_notes.md#config_version_2)。

<a id="config_env_references"></a>
### 显式环境引用

引用只在字符串值和列表元素中展开，键名保持原样：

| 写法 | 行为 |
| --- | --- |
| `${NAME}` | 读取变量；未设置时报错，已设置为空时得到空字符串 |
| `${NAME:-default}` | 未设置或为空时使用默认文本，不递归展开默认文本 |
| `$${` | 输出字面量 `${` |

变量名只允许英文字母、数字和下划线，首字符不能是数字。一个值可以含多个引用或混合普通文本。展开后的节点保持字符串类型，再由字段解码器转换整数、布尔和逗号分隔列表，`0123` 这样的密码保持原文。错误只指出字段和变量名，不显示值。

```yaml
config_version: 2
database:
  password: ${DATABASE_PASSWORD}
```

文件没有引用的旧应用配置变量会在每次启动时合并告警，只列排序后的变量名，不阻止正常启动。显式引用的变量不告警；安装输入仅在与当前有效配置不一致时告警。`ADMIN_*` 和安装开关不参与告警。

自动安装会为非空的 `DATABASE_HOST`、`REDIS_HOST` 及安装凭据写入显式引用；每次启动都需传入这些变量。手动安装向导保存主机地址的字面值。需要固定地址时，可以将引用改为实际主机名或 IP。

仍由环境提供的输入：

- 读取配置前的路径：`CONFIG_FILE`、`DATA_DIR`。
- 安装开关：`AUTO_SETUP`、`SKIP_SETUP`。
- 首次自动安装：`DATABASE_HOST/PORT/USER/PASSWORD/DBNAME/SSLMODE`、`REDIS_HOST/PORT/USERNAME/PASSWORD/DB/ENABLE_TLS`、`ADMIN_EMAIL`、`ADMIN_PASSWORD`、`SERVER_HOST`、`SERVER_PORT`、`SERVER_MODE`、`JWT_SECRET`、`JWT_EXPIRE_HOUR`、`TOTP_ENCRYPTION_KEY`、`TZ`、`SETUP_MIGRATION_TIMEOUT_SECONDS`。安装向导监听地址也使用 `SERVER_HOST`、`SERVER_PORT`。
- Go、操作系统或依赖库的标准输入，例如 `HTTP_PROXY`、`HTTPS_PROXY`、`NO_PROXY`、`GIN_MODE`、`GOMEMLIMIT`、`SystemRoot` 和 `TZ`。
- 配置文件明确引用的变量。应用时区以 `timezone` 为准；`TIMEZONE` 不再用于常规加载或安装。

### 网关和时区

网关的静态参数（请求体大小、等待、切换上限和完成执行器参数），由 app 整理成单独的 Options 传给网关，核心代码不接收完整的 Config。HTTP 请求、平台尝试和完成队列使用同一个应用依赖图。用户提示词替换和错误规则，按各自的设置来源和生效时机读取，运行状态分别由 `gateway/promptpolicy` 和 `gateway/errorpolicy` 持有。错误规则的管理写入、回源和发布通过进程内协调器控制顺序，缓存加载与失效通知见[网关错误响应策略](gateway_error_policy.md#缓存一致性)。

应用时区由 `timezone` 决定，默认 `Asia/Shanghai`。`TZ` 是首次自动安装输入和运行时标准变量；v2 文件加载时不会隐式覆盖应用时区。无效的 IANA 名称在启动校验时失败。

文本 SSE 的下游空闲心跳使用 `gateway.streaming.keepalive_interval_seconds`，默认 10 秒，允许 5 至 30 秒，0 关闭统一文本保活。`gateway.streaming.data_idle_timeout_seconds` 默认 180 秒，允许 30 至 300 秒，0 关闭正文停滞上限；收到上游响应头后，统一组件按上游活动与 handler 字节写入中较晚的时间判断停滞，超过上限后停止发心跳。等待上游响应头期间由传输层响应头超时和首输出保护限制，心跳不会因正文停滞上限而中断，读取器仍执行自己的超时处理。排队心跳和图片专用配置分别保留，Compact 的专用 SSE 控制器继续复用文本间隔。提前提交 200 的条件、错误格式和心跳计数见[SSE 保活](../architecture/gateway_request_lifecycle.md#sse_keepalive)。

### 请求记录

`request_log.retention_days` 控制请求摘要保留天数，默认 30。内存批写、失败处理和关闭排空见[请求 ID 与请求查询](../operations/request_lookup.md#request_storage)。

### 插件运行时

`plugins` 是需要重启生效的进程配置。`enabled` 默认 true，`max_running` 默认 20；`dev_mode` 默认 false，只有显式开启后才读取 `dev_dirs`。开发目录中的代码按 manifest 授予权限，不使用正式安装的二进制摘要校验。`enabled=false` 不启动插件进程或 KV 维护，贡献 API、UI 描述和资源路由返回 503 `PLUGINS_DISABLED`；管理员仍可查看和卸载。

`dir` 显式值优先；缺省时取 `DATA_DIR/plugins`，没有 DATA_DIR 时取 `storage.dir/plugins`，两者都未提供时用 `./data/plugins`。默认官方索引是 `https://raw.githubusercontent.com/TokenFlux/tokenrouter-plugins/dist/v1/index.json`。`download_proxy` 只代理索引与包下载，`download_url_prefix` 供 GitHub 下载前缀使用，`plugin_http_proxy` 才传给子进程的 HTTP_PROXY/HTTPS_PROXY。分发 URL 必须使用 HTTPS，代理支持 HTTP、HTTPS 或 SOCKS5。内置来源保留管理员启用选择，宿主配置改变其 URL 时清除旧缓存；官方验签公钥由项目方配置到源码，空集合会拒绝内置索引。自定义来源的固定公钥通过管理接口保存，来源和缓存规则见[插件运维](../operations/plugin_operations.md#distribution)。

部署后在 `config.yaml` 中设置插件参数；凭据或代理地址可以使用显式环境引用。插件信任模型和生命周期见[插件系统](../architecture/plugin_system.md)。

### 模型目录与价格补充

模型目录使用 `model_catalog.catalog_url`（默认 `https://models.dev/catalog.json?type=all`）和 `model_catalog.check_interval_minutes`（默认 10 分钟），自动同步价格和模型属性。旧的键名和环境值只在 v1 升级时迁移。

已知的 Wei-Shaw、BerriAI 公共旧价格地址，在 v1 升级时转换为 models.dev 地址，并补上新的下载域名；自定义地址保持不变，需要返回 models.dev 的目录格式。升级结果写回配置文件。官方模型补充通过 `go:embed` 编进二进制，同一个型号条目包含价格字段和 `attributes`。`model_catalog.supplement_file` 默认为空，用于指定相同格式的自定义补充。旧的相对资源路径和 `/app/resources/` 打包路径，只在原文件不存在时，迁移到同目录的 `model_pricing_supplements.json`；已存在的文件和其他自定义路径保持不变。旧路径变换写入升级后的文件；目标文件也不存在时，使用内嵌的补充。旧的目录缓存不再加载，也不会被删除。

数据的优先级：目录里已有的字段（包括零价）最高，其次是自定义补充，最后由内嵌补充填上有明确来源的缺失模型价格、媒体单价、生图文本输出价和明确的商业规则。媒体尺寸表这类复合字段整体选择一个来源，不同来源的尺寸表不合并。补充文件要求是 JSON 对象；顶层 `null`、非对象条目和非法的价格字段会让整次更新被拒绝，已发布的目录保持不变，并记录错误。自定义文件不存在时，这一层为空，内嵌补充照常生效。修改和删除在下一次周期检查或管理员手动更新时生效，包括远程返回 304 或远程地址为空的情况。首次启动遇到损坏的补充文件时，仍然可以发布带内嵌补充的离线目录，并保留错误，等待修复。

补充文件里的 `_billing_defaults` 是操作价格的保留节点，可以包含 `web_search_price_per_call`、`search_price_per_1k`、`audio_realtime_price_per_min`、`audio_tts_price_per_million_chars` 和 `audio_stt_price_per_hour`，单位和价格配置里的同名设置一致。自定义补充按字段叠加到内嵌的操作默认价上，管理员的价格配置优先，填写零表示免费；这个节点不会出现在模型列表、价格候选和属性列表里。

本地 JSON 使用 `provider` 字段，读取时兼容 `litellm_provider`；同一条目两者都有时，以新字段为准，包括空值和 `null`。内部的价格类型和来源分类使用目录通用的名称，实际报价来自哪里，由 `source` 和 `price_sources` 区分。


模型属性档案的 rules JSONB 同时用于展示、客户端导出和请求参数处理。每条规则可增加可选的路线匹配条件，已有规则默认适用于所有路线。编辑器保存字段覆盖；停用档案或移除字段后恢复目录与平台默认值。完整解析顺序与预览接口见[模型属性配置](model_catalog_and_marketplace.md#model_attributes)。

精确配置为旧官方目录地址 `https://models.dev/catalog.json` 时，v1 升级器补上 `?type=all`。带查询参数的地址和自定义镜像按配置读取。

### 国产供应商用量监控

国产供应商的周期用量监控属于启动时的进程配置 `provider.usage_monitor`。`monitor_enabled` 默认关闭；开启后，默认每 10 分钟运行一次，并发 4，单个提供商的探测超时 20 秒，每轮预算 300 秒，余额临时停调阈值 `balance_threshold` 默认 `0.5`。对应的键是 `interval_minutes`、`concurrency`、`probe_timeout_seconds` 和 `round_timeout_seconds`，修改后需要重启。管理员手动查询不受监控开关影响；对自定义中继的自动监控，还要求开启并命中 `security.url_allowlist.upstream_hosts`。

### 创作台和批量图片

创作台（Creative Studio）属于启动时的进程配置 `creative`：功能和队列开关、临时数据 TTL（`transient_ttl_seconds`，默认 1800 秒）、上传和 prompt 限制（`max_asset_bytes` 默认 32 MiB，`max_total_input_bytes` 默认 64 MiB 且不小于单文件上限，`max_prompt_chars` 默认 8000）、上游执行参数（`execute_timeout_seconds`、`max_execute_attempts`），以及 `creative:queue:*` 的队列键和 TTL，都在启动时校验，修改后需要重启。

创作台每个任务固定生成一张图片，预占价格按所选尺寸的单价计算。创作台的 `enabled` 和 `queue_enabled` 默认开启；临时存储和队列依赖 Redis，Redis 不可用时拒绝创建任务。完整的键清单见 `deploy/config.example.yaml` 和[创作台](../domains/creative_studio.md)。

批量图片的请求准入由分组和提供商统一协议配置控制，worker、恢复和清理随应用启动。`batch_image` 保留请求、下载、留存、队列和 Vertex 存储参数；未配置 GCS 存储桶时，Vertex 不参与新建作业选择。填写存储桶后，启动时校验名称、区域和作业前缀。

两类任务的生产核心，由 app 在构造时传入各自固定的 Options，配置来源相同，业务核心不读取完整的 config。批量图片的 provider registry 同时用于提交、轮询、下载和清理。任务运行时停止后无法再次启动；动态扩容创作 worker 也不会重新打开已经停止的运行时。

创作台的 worker 数量不是进程配置，而是数据库运行时设置 `creative_worker_count`：默认 128，只允许大于 0 的整数，没有硬上限；缺失或历史上的脏值按 128 处理。管理员在"功能特性 - 创作台"保存后，本实例立即扩缩 worker 池；缩容时优雅排空，正在执行的上游请求不会被中断。这个设置不需要迁移，也不通过公开设置接口暴露。

### 产品名称兼容

新安装默认使用 `tokenrouter` 作为数据库名、日志服务名、日志文件名和仪表盘缓存前缀。明确配置的数据库连接、日志路径和缓存前缀原样保留，升级不会重命名数据库或搬迁数据。只依赖旧的默认数据库名的部署，升级前需要设置 `DATABASE_DBNAME=sub2api`；Compose 部署还要保留原来的 `POSTGRES_USER` 和 `POSTGRES_DB`。


迁移 283 只在站点名称、站点标题、发件人名称和支付商品前缀去掉首尾空白后恰好等于旧产品名时，把它们改成 TokenRouter。自定义的值、历史订单、邮件记录和已经绑定的 TOTP 密钥都保持不变。新绑定的 TOTP 显示 TokenRouter，旧的绑定继续用原来的密钥验证。

<a id="site_link_addresses"></a>
### 站点链接与客户端 API 地址

`frontend_url` 是邮件链接的公开站点地址；后端优先读取数据库中的非空值，未填写或读取失败时使用部署配置 `site.frontend_url`。退订链接每次生成时读取当前设置，保留地址中的路径前缀，再追加 `/api/v1/settings/email-unsubscribe`。公开入口需要把该请求转发到后端；两处前端地址都为空时保留相对链接。

OAuth 服务端回调必须接收发起阶段写入的状态 Cookie。微信登录和支付 OAuth 的自动回调使用当前请求的公开 origin 和固定的 `/api/v1/auth/oauth/...` 路径，不读取站点前端地址或模型 API 地址；反向代理继续负责提供可信的 Host 和协议转发头。微信登录的显式回调配置保持优先。这样前端站点与面板 API 分域，或前端站点自身带路径前缀时，不会把状态 Cookie 留在另一个域名或路径上。

管理页面为 LinuxDo、GitHub、Google、微信和 OIDC 提供的回调建议使用浏览器实际的面板 API 入口，与 OAuth 发起请求采用同一 `VITE_API_BASE_URL`，默认 `/api/v1`；相对入口按当前浏览器 origin 解析。建议不会自动覆盖已填写的回调字段。自定义反向代理 API 路径时，代理还需保持对外回调路径和 Cookie Path 一致。

`api_base_url` 用于客户端接入展示和配置导出，可以包含模型 API 的 `/v1` 前缀，不参与上述站点链接和 OAuth 回调生成。修改这些地址来源不会重写已发送的邮件或已保存的回调地址。MCP OAuth 的站点地址校验和读取失败处理另有下述约束。

### MCP OAuth

MCP OAuth 的公开地址使用现有 `frontend_url`，后台保存的非空值优先，部署 `site.frontend_url` 其次。仅接受不含路径、查询、片段或用户信息的 HTTPS origin，回环 HTTP 可用于本地开发。地址缺失或不适用时 OAuth 不可用，管理员可以在 MCP 接入配置中查看原因并前往原前端地址设置。`api_base_url` 继续用于客户端接入展示。

有效前端地址使 OAuth 自动可用。后台修改前端地址无需重启；每个请求读取当前值，数据库读取失败时拒绝 OAuth 访问。前端 origin 变化后，旧地址的 OAuth 连接需要重新授权。

启动时始终在 `security_secrets` 中生成或读取独立 OAuth 签名密钥，以支持后续通过后台配置前端地址直接启用。授权页面同样使用已校验的前端 origin。手工 MCP 令牌的固定 TOTP 加密密钥要求继续适用。具体端点、期限及客户端约束见 [MCP OAuth 应用授权](mcp_management.md#oauth_authorization)。

## 首次初始化

setup 使用与主服务相同的配置文件查找规则，包含 `CONFIG_FILE` 和 `/etc/tokenrouter`。新文件写入 `CONFIG_FILE` 或数据目录中的 `config.yaml`；`.installed` 仍放在数据目录。正常情况下，配置文件和安装锁只要有一个存在，就不会重新开放初始化；`SKIP_SETUP` 是部署者手动跳过的开关。修改这套判断时，要保证"删掉一个文件也无法远程强制重装"。

交互式或 `AUTO_SETUP` 流程会：测试 PostgreSQL 和 Redis、执行迁移、只在空数据库里创建初始管理员、以 `0600` 权限写入 v2 配置文件、创建安装锁。已经有管理员或普通用户时，不会覆盖密码。自动 setup 使用的 `DATABASE_*`、`REDIS_*`、`ADMIN_*`、`SERVER_*`、`JWT_*` 和时区变量，只用来生成初始文件；非空的数据库密码、Redis 密码、JWT 和加密密钥保存为对应环境引用；连接测试使用实际值。未提供 JWT 或加密密钥时生成并持久化随机值。HTTP 和 CLI 安装将输入保存为字面量，并生成稳定加密密钥。

setup 的数据库和 Redis 连接测试由精简版 bootstrap 执行，输入字段、DSN 生成、超时和文件写入的顺序保持不变；首次管理员由 identity 的初始化能力写入，初始化不会创建默认分组。

主服务使用 `LoadForBootstrap`，只在引导阶段允许 `auth.jwt.secret` 暂时为空。`app/bootstrap` 初始化时，从 `security_secrets` 读取已有的 JWT secret，或者原子地生成并持久化一个新的，然后重新执行完整的配置校验。重启和升级沿用这个持久化的 JWT key；明确配置的值和数据库里已有的 secret 不一致时，以数据库里已持久化的值为准，已有会话不会因重启时重新生成密钥而失效。

<a id="runtime_settings"></a>
## 数据库运行时设置

全局 `ttft_mode` 位于网关通用设置：`first_output` 表示首有效内容，`first_event` 表示首个非心跳上游事件，新安装默认 `first_output`。每个 HTTP 请求或 WS 逻辑回合在开始时固定当次有效配置，重试沿用该选择；修改设置按现有缓存发布和刷新规则影响新请求，不重算历史记录，也不改变 TPS。

`settings` 是一张 `key/value/updated_at` 表，删除某个键表示恢复它的 getter 的默认行为。`settings.Store` 和它的 PostgreSQL 适配层负责通用的读写、版本字段和更新通知。身份的注册、安全和验证码设置，OAuth 配置的解释，提供商冷却，推广开关，用量排行，审计保留期和网关策略，分别由所属模块读取和解释；面板限流的配置和缓存在 `server/runtimeconfig`。

运行时设置包括：注册和邮件验证、第三方登录、SMTP、TOTP、会话绑定、step-up、登录协议、面板限流、部分冷却和流超时、支付展示，以及各种功能开关。不同 getter 在缺键时的回退值，可能来自代码常量，也可能来自 app 传入的启动选项，所以缺失的键不一定等于 `false`。

usage、audit 和 ops 的静态参数，由 app 整理成各模块的 Options；动态的 Ops 设置和日志配置，由数据库里的键控制。统一的预聚合控制器在 `settings/preaggregation`，带十五秒缓存和更新通知。运行日志按"应用、持久化失败时回滚、清理 Reload"的顺序处理。

### 模块读取器

创作台的运行开关和模型列表，由 app 直接注入 `creative.RuntimeSettings`，每次即时读取；路由容量使用同一个 `provider.QuotaSettingsCache`。各个读取器共用已经装配好的缓存实例。`settings/composite` 负责综合设置的组合、准备顺序和提交后的应用；`settings/httpapi` 负责扁平的请求绑定、权限、审计和响应。

生产网关使用所属模块的设置读取器。执行适配器的 `RuntimeReaders` 只持有网关、提供商、配额、路由、审核和搜索的实例，以及调度的读取接口，它不解释设置，也不缓存或启动任务。提示词替换由 HTTP 和 WS 在构造时注入同一个 promptpolicy 实例。Antigravity 的日志和流预算由 app 传入静态值；身份补丁每个请求都读取，失败时默认开启，空提示词时使用回退值。HTTP 客户端版本和余额的展示单位，分别直接从 gateway 和 billing 读取。可选的指针为 nil 时，中间件、路由和存储按原有的认证规则处理。

网关的 backend mode 运行快照在 `gateway/admission`，正常时缓存六十秒，查询故障时缓存五秒，回源预算单独为五秒。管理端发布新值时，推进进程内的发布代次；之前开始的回源和它的等待者，无法用旧值覆盖新值。HTML 注入缓存也绑定失效代次：旧的渲染可以结束自己的响应，但不会把结果写回失效之后的缓存；响应的 ETag 和 HTML 来自同一次渲染。这两处的发布代次都保存在进程内，不写入数据库。

### 综合设置的保存

综合设置 `PUT /api/v1/admin/settings` 在读取旧值之前，先进入实例内的更新保护。app 为综合输入的字段静态登记了各自的业务所有者、持久化的键和顺序，构造时拒绝重复登记，装配测试检查遗漏和重复。系统设置、认证默认值、Fast 策略和支付设置，先完成校验和整理，再做一次原子的批量写入。提交之前任何一步失败，运行状态和成功通知都不会发布。提交之后，如果必要的运行时应用失败，返回 `SETTINGS_APPLY_FAILED`，metadata 里标明 `persisted=true` 和失败的模块；配置已经保存，系统不会假装回滚，也不会自动重写或重试。

专用的设置入口各有自己的写入范围和通知行为。业务更新按“校验、批量原子写入、刷新缓存、发送通知”的顺序执行。业务调用方在持久化和缓存刷新完成后调用 `Store.NotifyUpdated`。`Subscribe` 按登记顺序同步通知订阅者，并返回幂等的注销函数。注销会阻止尚未领取的回调，已领取的回调可以完成一次。版本字段按应用版本赋值，JSON 中可以省略。运营内容另有持久化 `revision`，保存时在事务内比较已读取的版本，冲突返回 409。综合设置读取同时保存数据库原始值，参与者从同一快照校验文案版本。设置更新通知在单个应用实例内发布。公开设置、CSP、search 配置运行时和动态 worker 的回调由 app 装配。公开 API、embed 注入和 CSP 由 site 统一提供，各自保持字段格式：公开来源一次批量查询，认证、团队和用量分别解释自己需要的字段，site 只把公开的键和安全处理过的数据交给渲染层；OAuth secret 不会进入 web。

<a id="bstats_reporting"></a>
### bStats 安装统计

管理员在“系统设置 → 功能开关”的 bStats 卡片管理安装统计。`bstats_enabled` 缺键时默认开启；`bstats_log_errors`、`bstats_log_sent_data`、`bstats_log_response_status_text` 缺键时默认关闭。四个布尔字段由综合设置 GET/PUT 读写，省略字段保留原值，保存后热生效。关闭时取消并等待在途上报；重新开启重新执行首次随机延迟。只修改日志选项不会重置调度，关闭统计也不会清除日志选项。

上报使用 bStats 的 `server-implementation` 平台，TokenRouter 项目 ID 固定为 `35120`。`bstats_server_uuid` 是首次启用时生成并持久保存的随机 UUIDv4，不属于管理或公开设置接口，重启和升级继续复用。数据库备份恢复也会恢复此标识。标识读取、校验或持久化失败时暂停统计，后续设置发布或进程重启会重试；错误不会阻止网关启动。启动时读取配置和初始化标识共用 5 秒预算；运行时首次初始化标识同样最多等待 5 秒，调用方更短的截止时间优先。

数据只包含随机实例标识、操作系统及版本、CPU 架构和核数、TokenRouter 与 Go 版本，以及 bStats 协议元数据。国家分布由 bStats 根据连接来源推导。版本通过 `tokenrouter_version`、`go_version` 两个 Simple pie 图表上报。账户、API Key、站点域名、提示词、请求记录、用量和支付数据不进入报文。开启上报内容日志会把含 UUID 的完整报文写入服务端日志。

调度和 gzip JSON 协议参考官方 [MetricsBase v3.2.1](https://github.com/Bastian/bstats-metrics/blob/v3.2.1/base/src/main/java/org/bstats/MetricsBase.java)，设置默认值和图表格式分别参考同版本 `MetricsConfig`、`SimplePie`。首次延迟为 `[3,6)` 分钟，第二次在首次计划时间之后再延迟 `[0,30)` 分钟，此后每 30 分钟固定执行。HTTP 请求最长 10 秒，失败不即时重试，不提供手动上报或自定义频率。实现保留官方协议和调度，并增加 Go context 取消、有界响应读取和禁止重定向；MIT 许可证位于 `backend/internal/bstats/LICENSE`。

### 用户可见文案

站点文案使用 `site_texts`，其他独立运营文案通过 `localized_settings` 提交，完整字段和存储规则见[用户侧国际化](user_localization.md#content_owners)。`default_locale` 初始为 `en`。管理接口拒绝站点名称、标题和副标题的 `_zh`、`_en` 写入，返回 `REMOVED_SETTING_FIELD`。译文版本、核对和删除操作由统一内容模型处理。

### 社交分享

后台站点设置的分享标题和简介通过 `site_texts.site_share_title`、`site_texts.site_share_description` 编辑，沿用译文核对与版本冲突规则。空白标题使用首页标题，再回退到站点名称；空白简介使用副标题。封面通过 `site_share_image` 提交 data URL，支持 PNG、JPEG、WebP，最大 2 MiB、每边最大 4096 像素。选图只改草稿，保存设置才发布。

站点模块校验完整图片后，将图片和派生的 `site_share_image_meta` 描述在同一个设置事务中保存。省略图片字段保留现值，空字符串移除图片。描述不是可写管理字段。图片随数据库备份恢复，不需要独立文件目录。公开设置与 HTML 仅包含文案、图片地址、格式和尺寸，不包含封面 Base64。

内嵌前端在原始 HTML 中输出 Twitter Card 与 Open Graph 标签。有封面时使用 `summary_large_image`，否则使用 `summary`。HTML 按语言缓存，响应时补齐绝对地址并计算包含地址差异的 ETag；保存后使缓存失效。地址优先使用数据库 `frontend_url`，其次使用进程配置 `site.frontend_url`，两者均为空时读取请求来源。只有显式配置的可信代理可以通过转发头改变来源。

Vite 开发服务器也注入分享标签，未配置前端地址时使用本地开发端口。独立静态托管需由托管服务器实现相同的 HTML 注入；浏览器运行 JavaScript 后添加标签不能替代此步骤。社交平台可能缓存旧预览，应用保存成功不保证平台立即刷新。图片接口见 [HTTP 接口](http_api.md#social_share_image)。

### 部分更新与敏感字段

运行时设置的部分更新要区分"省略"和"明确清空"。`UpdatePaymentConfig` 只写入请求里实际提供的字段：省略支付方式列表、可见支付路由、手续费 map 或其他指针字段时，保留已有的值；只有明确传入空切片、空 map、空字符串或 `false`，才按对应字段的规则清空或关闭。系统设置的 handler 即使分阶段写入同一批支付设置，第二阶段的缺省值也不会覆盖第一阶段已经持久化的配置。

设置写入失败时，内存快照保持原样；数据库写入成功、但通知失败时，记录可观测的错误，靠 TTL 或重新加载恢复。更新请求里省略敏感字段时，保留原值；管理页面拿到的是掩码或空字符串，提交回来时 secret 不会因此被清空。验证码设置一次批量读取提供方的开关和密钥，同一个请求在多个 getter 之间看到的配置因此一致。

### 热路径的读取策略

热路径上的设置，从下面几种策略里选择一种并写清楚：

- 原子快照，更新成功后立即替换；安全客户端 IP 策略属于这一类。
- 带 TTL 的进程缓存，或者 stale-while-revalidate，避免每个请求都访问数据库。
- 通过更新通知使相关缓存失效，再按模块规则重新加载设置。
- 只在启动时加载；这类设置要注明需要重启，界面上也不能让人以为会立即生效。

### 注册、邮箱和登录

`registration_email_domain_quota_enabled` 控制：邮箱白名单非空时，是否允许白名单以外的域名按可注册主域名限量注册。缺失或读取失败时按关闭处理，保持只认白名单的安全默认值。这个设置通过公开设置和 SSR 注入下发给注册前端，用于选择本地的白名单预检方式；最终是否允许注册，由服务端在注册事务里重新读取并判断。管理端的更新请求省略这个字段时，保留当前值。

`user_email_change_enabled` 控制已经有实际邮箱的用户能否在注册后换绑主邮箱，默认关闭；缺失、读取失败或认证服务没有接入设置服务时，都拒绝换绑。它不影响还没有实际邮箱的用户第一次绑定邮箱，也不影响验证并绑定与当前记录相同的邮箱。开关通过公开设置和 SSR 注入控制个人资料页的入口，服务端在发送换绑验证码和提交换绑时都会检查。管理端的更新请求省略这个字段时，保留当前值。

`google_one_tap_enabled` 是和 `google_oauth_enabled` 分开的运行时开关，默认关闭。部署者先在现有的 Web 类型 Google OAuth Client ID 下，登记每个前端的 Authorized JavaScript origin，再开启这个开关；生产环境的 Origin 需要使用 HTTPS，本地开发只允许 localhost 和 loopback 的 HTTP。只有 One Tap 开关和完整的 Google OAuth 配置都有效时，公开设置才返回 `google_one_tap_enabled=true` 和非敏感的 `google_oauth_client_id`；任一条件不满足时，按关闭处理并返回空的 Client ID。Client Secret 只留在服务端，以及有掩码保护的管理设置里。首页和登录页使用同一组公开设置，旧的 HTML 注入缓存缺少新字段时，按关闭处理。

### 验证码与 CSP

验证码也是数据库运行时设置。Turnstile、腾讯天御和阿里云验证码 2.0 三者同时只能启用一个。

- 腾讯天御：启用时需要正整数的 `CaptchaAppId`、`AppSecretKey`、腾讯云的 `SecretId` 和 `SecretKey`，并选择 `cn` 中国站或 `intl` 国际站。站点决定前端 SDK、构造函数的写法、控制台入口和服务端校验票据的 endpoint；`CaptchaAppId` 和云密钥需要来自同一个站点。站点缺失或非法时，按 `cn` 处理。
- 阿里云：启用时需要 Scene ID、Prefix、AccessKey ID、AccessKey Secret，以及 `cn` 或 `sgp` 地域。

公开设置只返回各提供方的启用状态、站点和渲染需要的非敏感参数；管理响应对 secret 只返回"已配置"标记，提交空白值时保留原值，审计只记录字段被写入过，不记录内容。腾讯和阿里云 Web SDK 需要的脚本、连接、iframe、worker 和样式来源，由默认 CSP 和运行时的 CSP 补全逻辑共同维护，覆盖自定义的旧策略时也要补全，其中阿里云的静态资源允许 `https://*.alicdn.com`。

Google GIS 同样由默认策略和对旧自定义策略的增强共同放行：`script-src` 只加入 `https://accounts.google.com/gsi/client`，`frame-src` 和 `connect-src` 加入 `https://accounts.google.com/gsi/`，`style-src` 加入 `https://accounts.google.com/gsi/style`。tf CLI 网页导入在 `connect-src` 里只允许 `http://127.0.0.1:43110` 到 `43119` 这十个精确的 Origin；代码里的默认策略、对旧自定义策略的增强和 `deploy/config.example.yaml` 需要同步修改，范围不能扩大到端口通配符或局域网。完整说明见 [tf CLI 网页导入](tf_cli_web_import.md)。

<a id="notification_delivery"></a>
### 通知与 SMTP

notification 接收已经确定的事件、收件人、语言、来源标识和模板变量，维护用户与管理员通知事件、模板覆盖、退订和投递去重。SMTP 在技术适配层里。完整的生命周期见[通知与邮件投递](../domains/notification_delivery.md)。identity 维护验证码、重置令牌和 Redis 里的凭据，各入口的"先存后发"或"先发后存"顺序各不相同。用户自定义模板缺失或无效时，使用选定语言的内置模板。SMTP 发送失败或结果不确定时，自动流程停止重发。

同一个投递 key 的读取、发送和成功标记，在一个服务实例内协调；不同的 key 可以并行。退订密钥的首次生成也在实例内协调，已有的密钥、HMAC 和令牌有效期保持不变。这些保证只在单个服务进程内成立；SMTP 已经接收、但成功标记写入失败时，投递结果仍然不确定，系统没有恰好一次的投递协议。

SMTP 的测试连接和实际发送共用同一条建连路径和超时：

- `smtp_use_tls=true`：先按隐式 TLS 连接；服务端回复明文的 SMTP 问候时，改用强制 STARTTLS；服务端不支持升级时，连接失败，认证信息不会以明文发送。
- `smtp_use_tls=false`：使用机会式 STARTTLS，服务端不支持这个扩展时，按明文发送。

两条路径在认证成功后都忽略不标准的 QUIT 响应，所以后台的连接测试和实际发信能力一致。SMTP 的发送和管理测试都传递 context，取消时中止拨号、TLS 和在途的 I/O，超时取请求截止时间和连接、I/O 上限中较早的一个。DATA 得到成功响应后，结果记为成功，之后的取消或不标准的 QUIT 都不会触发重发。

<a id="search_configuration"></a>
### 搜索配置发布

`search.ConfigService` 持有唯一的配置缓存、singleflight 和当前的 Manager 注册表。供应商的选择、额度和停机见[搜索编排](../domains/search_orchestration.md)。保存成功时，推进本进程的发布代次，之前开始的回源和 Manager 构建无法覆盖新保存的结果。这个代次不写入数据库或 Redis。保存、读取、展示和 provider 配置都会复制所有可变的指针和 slice，读取方修改副本不会影响运行中的快照。

空 API Key 可保留已存密钥。搜索配置的专用管理 GET/PUT 接口返回包含 `api_key` 的配置和用量；`SanitizeWebSearchConfig` 提供清空密钥的副本，需要脱敏的调用方自行选择。管理测试不占额度。代理无法解析时，跳过这个供应商，请求不会改走直连；提供商代理优先于供应商代理。搜索配置的变化只影响 Brave 和 Tavily，Grok 和 AlphaSearch 的原生搜索策略不受影响。

### 首页、创作台和用量排行

`home_featured_models` 保存首页「已支持的 AI 模型」板块的精选模型 ID 列表（JSON 数组，最多 12 个，按数组顺序展示），在管理端"系统设置 - 通用设置"的「首页模型展示」卡片里维护，候选来自公开模型广场的分组。它通过公开设置接口和 SSR 注入同时下发，首页按 ID 在市场分组里解析模型；列表为空或全部解析不到时，首页显示按服务商类别聚合的默认卡片。这个设置可以热更新，不需要迁移（读取时缺键按空列表处理）。管理端的更新请求省略这个字段时保留当前值；写入前去掉空白项、去重，并拒绝超长的列表。

`creative_model_settings` 保存创作台允许使用的全局生图模型和能力白名单（JSON 数组），每项包含 `group_id`、`model` 和 `operations`。通用能力值只允许 `generate`、`edit`、`inpaint`，各平台实际支持的是：OpenAI 三项，Gemini 和 Grok 的 `generate`、`edit`。默认值是 `[]`，空列表表示创作台没有可用的生图模型；不需要数据库迁移。

管理端的 PUT 省略这个字段时保留旧值，明确发送 `[]` 时清空。保存时校验分组 ID 是正整数、模型名非空、至少有一项能力、分组加模型唯一，并按当前可请求的候选模型的能力，移除不支持的操作；移除后没有能力的条目被删除；无法解析的历史模型配置暂时保留。读到损坏的 JSON 或读取失败时，按空列表处理并记录日志。设置没有外键，所以失效的分组或提供商配置会保留，恢复后重新生效。

`usage_ranking_enabled`、`usage_ranking_sort_by`、`usage_ranking_show_total_tokens`、`usage_ranking_show_requests`、`usage_ranking_show_actual_cost` 和已有的 `usage_ranking_limit` 共同控制用户侧的用量排行。排行行里的 `user_id` 是付款主体：团队 Key 的请求按 `billing_user_id` 归到团队 Owner，Usage 明细里的 `user_id` 仍是实际行为的成员。

这些键存在 `settings` 表里，不需要迁移或重启。排行请求在查询前一次读取这些键，所以保存后的新排行请求读取新值。缺少新键时，按升级兼容的默认值处理：排行开启，按 `total_tokens` 排序，三项都显示，名次上限 20。排序值只允许 `total_tokens`、`requests` 和 `actual_cost`；排序用的指标必须显示，其他字段可以单独关闭。

管理端"通用设置"的用量排行卡片和公开设置，都返回这组有效配置。关闭总开关后，用户侧的导航和路由不再提供入口，`GET /api/v1/usage/ranking` 在查询前返回 `403`；管理员仪表盘的消费排行不受影响。关闭某个显示字段后，用户排行的响应里省略对应的行字段和总计，关闭 Token 时还要省略输入、输出和缓存 Token 的明细；这些字段在服务端就被去掉，浏览器端隐藏是不够的。普通明细查询和预聚合查询，都按所选指标大于零入榜，并用其余指标和付款主体 ID 作为稳定的并列顺序。

### 高级调度参数

高级调度器的配置分两层：每个分组的 `scheduler_type` 是领域配置，明确选择 `basic` 或 `advanced`；网关通用设置保存高级模式的运行参数，包括 `advanced_scheduler_sticky_weighted_enabled`、`advanced_scheduler_subscription_priority_enabled`、`advanced_scheduler_lb_top_k`、各个 `advanced_scheduler_weight_*`、两个独立的 `advanced_scheduler_ewma_*_alpha`，以及 `advanced_scheduler_sticky_escape_*`。

这些参数在"网关设置 - 通用设置"里编辑，由 `scheduler.SettingsRuntime` 读取，带五秒 TTL 和 singleflight。app 绑定一个实例，设置更新发布到这个实例；每次读取返回一份独立的 map，批量读取失败时，逐个键降级读取。数值留空时，继承 `scheduling.advanced` 的进程默认值；sticky escape 的开关和两个阈值也支持热更新。系统没有 `advanced_scheduler_enabled` 这样的全局开关，参数缺失时只回退到进程配置的默认值，分组的模式保持不变。

管理端的分组 API 还接受稀疏对象 `advanced_scheduler_overrides`，只在 `scheduler_type=advanced` 的实际调度中使用。创建时默认为 `{}`；更新时省略这个字段，保留原对象；传 `{}` 清除全部覆盖；对象里没出现的字段，继续继承全局设置。`false` 和 `0` 都是有效的覆盖值，会被保存。合并后七项基础评分权重全为零也是有效配置，这时评分相同的候选按提供商全局优先级和提供商 ID 稳定排序，全局权重不会被悄悄恢复。

合并后的基础权重之和、完整权重之和都需要是有限数，写入时拒绝会导致溢出的覆盖；运行时读到历史遗留的异常对象时，权重回退到全局有效值。这个字段随认证快照缓存，修改它会提升快照版本；公开的用户分组接口不返回它，也不返回 `scheduler_type`。

管理端的高级调度评分诊断，逐项返回最终的参数和来源：`group_override` 优先于 `global_runtime`，后者缺失时为 `process_default`。诊断只解释当前的实时评分，不保存历史快照，也不会因此启用分组、高级调度器或任何平台专属的策略。

进程配置的默认参数在 `scheduling.advanced`，包括 `lb_top_k`、`score_weights`、`ewma_error_rate_alpha`、`ewma_ttft_alpha` 和粘性逃逸的阈值。两个 alpha 要求 `0 < alpha <= 1`；sticky escape 的 TTFT 阈值是正数，错误率阈值在 `0..1` 之间，错误率阈值设为 `0` 表示任何正的错误率都会触发逃逸。

旧的进程调度键只由 v1 升级器转换。管理设置请求里的 `openai_advanced_scheduler_*` 和旧的全局开关仍返回已弃用错误。OpenAI 配额自动暂停保持其独立的运行时设置。

### Responses 长连接参数

“网关设置 → OpenAI → 长连接与连接池”支持在线调整连接数量、超时、消息大小和会话记录期限。数据库 `responses_ws` 保存覆盖对象，未覆盖字段使用 `gateway.openai.responses_ws` 的部署默认值。配置来源和热更新时机见 [Responses 长连接与在线参数](openai_upstream.md#responses_ws_runtime)。

旧的 WS 总开关、认证类型开关、强制 HTTP、新旧模式选择和协议版本开关在 v1 升级时删除；v2 文件中出现这些键会被拒绝。客户端是否允许使用 WS，统一由分组协议控制。HTTP 转 WS 执行路径的预热生成、重试退避、批量刷新、存储恢复和日志采样参数也已退役。连接池自身的预热与逐轮恢复继续按在线参数执行。

### 平台相关设置

Grok 文本转发有两项数据库运行时设置：`grok_default_text_model` 和 `grok_default_base_url_mode`。`grok_default_text_model` 只用于允许省略模型的请求，其他请求里的模型名不会被改成默认型号；需要改写时，配置手动的模型映射。原来的 `grok_cross_client_model_map_enabled` 已经移除，管理设置请求里带这个字段时返回 400。

base URL 模式只在提供商没有保存手动端点时生效，可以选 CLI 代理、公共 API、`us-east-1`、`us-west-2` 和 `eu-west-1`。这些设置可以热更新；提供商手动配置的 URL 优先，媒体和 Voice 使用各自的官方端点。

Grok 的启动配置分为 `provider.grok` 和 `scheduling.grok_free_quota`。`password_auth_enabled` 默认关闭，控制从邮箱密码到 SSO 或 OAuth 的敏感入口；Free OAuth 的本地软门禁由 `free_quota_soft_gate_enabled`、`free_quota_token_limit`、`free_quota_soft_gate_percent`、`free_quota_window_hours` 和 `free_quota_stats_cache_seconds` 控制。所有数值在启动时校验，修改后需要重启。统计缓存未命中或查询故障时按 fail-open 处理，OAuth state 的一次性消费、凭据持久化和 URL 信任检查照常执行。

`provider_scheduling_thresholds` 是整体替换的 JSON map，允许 OpenAI、Anthropic、Grok、Kimi 和 Zhipu 的 1 到 100 的整数，100 表示关闭这个平台的自动停调；提供商可以在自己的凭据里覆盖。管理设置的部分更新省略这个字段时，保留数据库里的值和进程缓存，前端的初始默认值不会被当成一次更新。

## 领域配置

结构化的、有关联关系和独立生命周期的业务配置，使用专门的表：

- 分组：倍率、客户端协议、回退目标、路由策略（模型映射、白名单和功能）和主动可用性探测参数。
- 价格配置：价卡和计费设置。
- 提供商：上游凭据、代理和调度状态。
- 订阅计划、支付 provider 实例、API Key、团队和用户属性，各有自己的不变量和审计路径。
- Ops 和预聚合等同时有进程级硬开关和运行时开关的功能，以进程开关为上限：部署者手动关闭的能力，数据库设置无法重新开启。

需要唯一约束、外键、状态机、列表查询或原子计数的数据，放进专门的表；编码成一大段 settings JSON 会失去这些约束。反过来，只被单个进程组件在启动时读取的参数（例如连接池大小）留在进程配置里，不需要新建业务表。

支付配置只在 `payment.ConfigService` 实现，实例读取和批量用量查询在 `payment/postgres`，provider factory、加密键和 `payment.resume_signing_key` 由 app 提供。热刷新整体读取失败时保留旧的注册表；首次读取失败时允许之后重试。配置键、缺省值、旧的密文和续接回退密钥都保持兼容；订阅套餐通过 billing 读取。

备份的 Options 只接收数据库名、本地根目录、时钟、日志和加密配置标记，完整的连接凭据只传给归档的技术适配层；S3 凭据按运行时设置读取。

## 前端变量

Vite 在构建和 dev server 启动时读取 `VITE_API_BASE_URL`、`VITE_DEV_PROXY_TARGET` 和 `VITE_DEV_PORT`。默认的 API base 是 `/api/v1`，dev proxy target 是 `http://localhost:8080`，dev port 是 `3000`。

`VITE_*` 会被打包进客户端代码，所以里面只能放公开的值。生产环境的内嵌前端，动态的品牌、功能和公开认证配置通过后端设置注入或 API 获取，和 Vite 构建变量是两条通道。修改后端的公开 URL 时，还要核对 OAuth callback、邮件链接、CORS 和 CSP、反向代理路径，只改前端的 base 是不够的。

## 新增配置检查清单

- 判断它属于进程、bootstrap、数据库运行时、领域实体还是前端构建，并确定唯一的来源。
- 进程字段：添加 `mapstructure`、代码默认值、规范化、未知键检查覆盖和 `Validate`，并更新部署样例。
- 写清楚 YAML 点分键、单位和默认值。改名、移动、删除或改变语义时递增 `CurrentConfigVersion`，追加升级步骤、对照表和迁移测试；新增带默认值的键不需要增加版本。
- 写清楚是否热生效、运行时快照何时更新、缓存怎么失效、失败时 fail-open 还是 fail-close。
- secret 使用只写和掩码，并覆盖日志脱敏；它不能出现在 `VITE_*` 或普通的设置响应里。
- 更新升级、显式引用、未知键、校验、setup 往返、设置读取器和 HTTP，以及部署冒烟测试。

相关文档：[HTTP 接口](http_api.md)、[系统架构](../architecture/system_architecture.md)、[部署与迁移](../operations/index.md)、[接口目录](index.md)。
