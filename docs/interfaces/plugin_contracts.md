# 插件契约

本文说明 `plugin-sdk/` 的插件声明、安装包、签名索引、进程协议、宿主能力和管理 HTTP 接口。运行时归属与信任边界见[插件系统](../architecture/plugin_system.md)，安装与恢复见[插件运维](../operations/plugin_operations.md)。

SDK 使用 MIT 许可证，宿主使用 LGPL-3.0-or-later 许可证。

## 章节导航

- [插件声明](#manifest)：字段、权限与兼容性。
- [安装包与完整性](#packages)：内容、路径与完整性。
- [签名索引](#catalog)：来源与分发记录。
- [安装管理接口](#administration)：上传、配置与运行控制。
- [HTTP 贡献与公开资源](#http_contributions)：路由鉴权、响应过滤与公开资源。
- [进程协议与 SDK](#process_protocol)：宿主能力与 SDK。
- [KV 接口](#kv_api)：版本、JSON、TTL 与分页。
- [Web SDK 与示例](#web_sdk)：构建预设与示例。

<a id="manifest"></a>
## 插件声明

`plugin.json` 由 `plugin-sdk/go/manifest` 统一解析，宿主、发布工具和索引构建复用同一套规则。原始 manifest 字节用于计算摘要，解析和配置规范化不改写发布资产。

- 基础格式：拒绝未知字段；插件 ID 使用 `<publisher>.<name>`；发布版本和宿主版本范围使用完整 SemVer；本地化文本必须包含 `en`。
- 贡献要求：至少包含后端或前端贡献之一。后端声明可执行文件、按顺序匹配的 HTTP 路由和定时任务；前端声明 IIFE 入口、样式、按用户或管理员区分的页面和菜单。
- 兼容性检查：同时核对 API 主版本、宿主版本上下界、已实现权限和平台包。`0.0.0-dev` 或非法宿主 SemVer 仅跳过宿主版本范围，权限与平台检查照常执行。只有无后端插件可以使用 `any` 包。

配置字段支持 `string`、`textarea`、`number`、`integer`、`boolean`、`select` 和 `secret`：

- 默认值：缺失值填入默认值；显式空字符串、`false` 和 `0` 不被默认值覆盖。`Normalize` 返回新的配置映射，不返回部分成功结果。
- 密钥字段：`secret` 不允许声明默认值；宿主在规范化前合并未提交的旧密钥值，持久化时加密。
- 字段说明：可选 `description` 为本地化纯文本说明，每种语言最多 300 个 Unicode 字符，提供时须含英文回退。表单在字段名下纯文本显示，不属于管理员提交的配置值。旧版严格解析器会拒绝该字段，使用它的插件须提高 `minHostVersion` 并使用支持该字段的 SDK 打包工具。

<a id="packages"></a>
## 安装包与完整性

安装包命名为 `<id>-<version>-<os>-<arch>.tar.gz`，纯前端插件使用 `<id>-<version>-any.tar.gz`。归档根目录直接包含 `plugin.json` 和声明文件，无外层包装目录。支持 Linux amd64/arm64、macOS amd64/arm64 和 Windows amd64，Windows 声明的可执行路径追加 `.exe`。

`plugin-sdk/go/pkgfmt` 限制以下归档与资产上限：

| 限制项 | 上限 |
| --- | --- |
| 压缩包体积 | 64 MiB |
| 展开文件总量 | 256 MiB |
| 前端文件总量 | 8 MiB |
| 归档条目数 | 2000 |
| 目录深度 | 10 层 |
| 包图标 | 64 KiB |
| 菜单 SVG | 10 KiB |

解压与校验规则如下：

- 文件类型与路径：只允许普通文件和目录，拒绝链接、设备、重复路径、绝对路径、反斜杠和路径穿越。根目录 `.tr-installed` 为宿主保留名，包内禁止包含该文件或目录（含大小写或尾点别名）。
- 解压目标与权限：解压目的地必须尚不存在，失败时移除本次创建的目录。校验完成后，仅声明的二进制设为 `0755`，其余文件设为 `0644`。
- 摘要与确定性构建：SHA-256 分别标识压缩包、原始 manifest 和二进制，前端文件另计算 SHA-384 SRI。发布器固定文件顺序和归档元数据，相同输入生成相同归档字节。
- 独立校验：`trplugin validate` 校验目录或归档；归档旁存在 `checksums.txt` 时同时核对摘要，缺少该文件时只能证明结构和声明有效，不能证明发行者身份。

<a id="catalog"></a>
## 签名索引

`plugin-sdk/go/index` 定义 `schemaVersion: 1` 的目录，发布与传输共用 8 MiB 原始目录字节上限，超限时发布工具报错并保留已有输出。

- 版本条目：每个插件最多保留 30 个版本，按 SemVer 降序排列。每个版本含完整 manifest、原始 manifest 摘要以及各平台归档的 HTTPS 地址、SHA-256 和大小。嵌入 JSON 排版可以变化，`manifestSha256` 始终指向 Release 中原始 `plugin.json` 的字节。
- 前向兼容：`ParseForHost` 在解析嵌入声明前检查 `minHostVersion`。仅当已发布宿主版本低于最低要求时保留未知贡献并显示 `HOST_TOO_OLD`；当前宿主可执行的声明和发布工具仍严格拒绝未知字段。
- 签名验证：签名文件为 `base64(ed25519.Sign(privateKey, index.json 原始字节))`，与 JSON 分开保存。验签在解析和替换缓存前完成，支持多个受信公钥轮换，空公钥列表不能通过验签。
- 官方索引构建：发布工具读取 `publishers/` 和 `plugins/` 的 YAML 登记，抓取 Release 并逐个下载校验安装包，有后端的官方登记须同时提供两个 Linux 架构。已收录仓库与 tag 保留首次摘要；失败版本记录原因并允许后续重试。提交权限校验依据可信基线中的发布者账号，新发布者须维护者审核。
- 无签名自定义目录：`trplugin index build` 默认要求 `INDEX_SIGNING_KEY`，仅显式传入 `--unsigned` 时生成无签名目录并移除旧输出中的签名文件。

<a id="administration"></a>
## 安装管理接口

所有管理路由均登记到 `adminapi`，执行代码、配置和额度变更经过 HTTP step-up guard；multipart 上传在 MCP 中使用浏览器入口。

| 路由 | 用途与关键字段 |
| --- | --- |
| `GET /api/v1/admin/plugins` | 已安装列表；可选 `tasks` 字段返回内存任务状态与最近结果，`next_run_at` 零值表示暂停（字段见[定时任务](../architecture/plugin_system.md#task_scheduling)） |
| `POST /api/v1/admin/plugins/uploads` | 接收 multipart `file`，返回 `upload_token`、过期时间、manifest、摘要和平台 |
| `POST /api/v1/admin/plugins` | 安装：上传用 `{source: "upload", upload_token, accept_permissions, enable}`，索引用 `{source: "index", index_id, plugin_id, version, accept_permissions, enable}`；响应含 `installation`、`needs_configuration`、`repaired` |
| `GET /api/v1/admin/plugins/:id` | 返回安装详情（含可选 `tasks`） |
| `DELETE /api/v1/admin/plugins/:id` | 卸载；`purge_data=true` 清除 KV，缺省保留；台账始终保留 |
| `POST /api/v1/admin/plugins/:id/{enable,disable,restart}` | 控制运行意图和本地进程 |
| `GET /api/v1/admin/plugins/:id/logs` | 返回最近 500 行插件日志 |
| `POST /api/v1/admin/plugins/:id/update` | 更新，接受 `{version, accept_permissions}` 或 `{upload_token, accept_permissions}` |
| `POST /api/v1/admin/plugins/:id/rollback` | 交换完整版本快照 |
| `GET/PUT /api/v1/admin/plugins/:id/config` | 读写配置 `{values: {...}}` |
| `/api/v1/admin/plugins/:id/limits` | 配置 `wallet`、`redeem`、`subscription`、`email` 额度分组 |
| `GET /api/v1/admin/plugin-catalog`、`/:indexId/:pluginId`、`POST /refresh` | 读取来源限定的目录条目、全部保留版本及兼容性，或刷新所有启用来源 |
| `GET/POST /api/v1/admin/plugin-indexes`、`PUT/DELETE /:id` | 列表、新增、修改和删除索引来源 |

安装、更新与回滚遵循以下约束：

- 权限确认：安装和更新必须确认 manifest 中的全部权限。新增权限未确认时返回 `PLUGIN_PERMISSIONS_NOT_ACCEPTED`，`metadata` 同时包含完整 `permissions` 和 `new_permissions`。
- 忽略版本检查：安装和更新均接受可选 `ignore_host_version`（默认 `false`），仅跳过最低和最高网关版本要求。该决定随当前及上一包快照保存，重启和回滚继续沿用；新一次安装或更新默认重新检查。安装 DTO 同名字段返回当前决定。
- 强制覆盖：安装和更新均接受可选 `force`（默认 `false`），允许替换同 ID 持久化安装（含同版本）。`force` 仅作用于本次请求，不跳过完整性、协议、平台、权限确认或配置校验，也不能覆盖开发目录。
- 来源切换与修复：安装启用 `force` 时可显式切换来源；未开启 `force` 时，同版本上传仅用于修复缺失或无效的已安装包。更新接口的索引来源固定为当前安装来源，上传更新可替换分发来源。
- 更新失败响应：返回 `PLUGIN_UPDATE_FAILED`，`metadata` 包含 `version`、`start_error`、最多 50 行 `logs`，以及恢复成功时的 `restored_version` 或恢复失败时的 `rollback_error`。

索引来源管理输入为 `name`、`url`、`public_key`、`enabled`；更新省略字段时保留原值，`public_key` 传空字符串清除固定公钥。内置来源只能修改 `enabled`。来源 DTO 不返回缓存正文，包含最近验签状态、缓存接受时间、刷新中标记和错误。

配置与额度校验规则：

- 配置提交：普通字段按提交内容替换并补默认值；`secret` 省略保留、空字符串清除，GET 返回 `{set: boolean}`。停用安装可保存未填完整的必填项，启用前须通过完整校验。
- 额度提交：未给出的字段取默认值，显式 `0` 禁止对应操作，拒绝 `null` 和未知字段。金额额度使用精确十进制 JSON 数字，浏览器可能输出的指数形式先无损归一化，仍限制最多 7 位整数和 8 位小数；能力 RPC 的金额字符串继续要求普通十进制形式。
- 表单校验与分离保存：管理表单保留数值输入原文，提交前检查整数安全范围和金额实际小数位，避免 JavaScript 舍入将无效输入转为合法值。文本长度按 Unicode 字符计数；RE2 pattern 仅在服务端执行，字段问题经 `metadata.fields` 定位。配置与额度为两个独立保存动作，界面明确报告部分成功，重试时跳过已完成且未修改的配置。

<a id="http_contributions"></a>
## HTTP 贡献与公开资源

全局 `plugins.enabled: false` 时，下述插件路由统一返回 503 `PLUGINS_DISABLED`，管理端仍可查看和卸载。

### 动态 API 代理

`ANY /api/v1/plugins/:id/api/*path` 按 manifest 顺序匹配方法和精确或前缀路径，未声明端点返回 404。路径只解码一次，拒绝 dot segment、反斜杠、控制字符和重复分隔符，保留有意义的末尾 `/`。所有动态端点经过 Backend 模式门禁，不进入面板命令幂等协调器。

- 鉴权与限流：`public` 可匿名，提供 `Authorization` 时走现有 JWT 校验并保留其失败规则；`user` 用用户 JWT；`admin` 用管理员中间件。公开端点先走公网 IP 限流，已认证请求再走用户面板限流。
- 请求过滤：请求体按路由限额，超限返回 413。仅转发 `Accept`、`Accept-Language`、`Content-Type`、`If-None-Match`、`If-Modified-Since`、`User-Agent`、`X-Request-Id` 和 `X-Plugin-*`，不转发 `Authorization`、`Cookie`、管理密钥与代理地址头。
- 上下文与版本复核：`Caller` 传入已验证用户及当前用户名（匿名无 `Caller`）；`Locale` 原样传入宿主协商后的请求语言；`grant` 走私有 RPC 字段；客户端 IP 复用宿主安全地址解析器。取得租约后复核版本和 manifest 摘要，声明切换时返回 503 `PLUGIN_RESTARTING`。
- 响应体与 MIME：成功响应原样输出 body（不套宿主 envelope），状态须为 200 至 599，body 上限 4 MiB。必须提供可解析的 `Content-Type`，允许 `application/json`、`text/plain`、`text/csv`、`image/png`、`image/jpeg`、`image/webp`、`image/gif`、`application/octet-stream` 及其参数。
- 响应头过滤：仅保留 `Content-Type`、`Cache-Control`、`ETag`、`Last-Modified`、`Content-Disposition`、`Retry-After`、`X-Plugin-*`（拒绝大小写重复的允许头），并附加 `nosniff`。

宿主生成的失败响应使用正常面板错误 envelope：

| 失败情形 | HTTP 状态与错误码 |
| --- | --- |
| HTML、SVG、SSE、无效响应或 RPC 失败 | 502 `PLUGIN_UNAVAILABLE` |
| 插件处理超时 | 504 `PLUGIN_TIMEOUT` |
| 未取得租约时进程不可用 | 503 |
| 取得租约后声明已切换 | 503 `PLUGIN_RESTARTING` |

### 公开静态资源

`GET /api/v1/plugins/:id/assets/:version/*file` 无需登录并经过公网 IP 限流，只读取本地已有版本，不下载缺失包。

- 开放范围：`frontend/` 下的 `.js`、`.css`、`.svg`、`.png`、`.jpg`、`.webp`、`.json`、`.map`，根目录 `icon.svg`/`icon.png`，以及该版本 `manifest.icon` 指定的单个 SVG/PNG（声明图标及固定根图标均限 64 KiB，其他目录不开放）。
- 完整性校验：路径越界、未登记文件、符号链接或尺寸变化返回 404；文件句柄可读范围固定在已接受尺寸。
- 缓存与安全头：ETag 取安装时 SHA-256 前 16 字节，`Cache-Control` 为 `public, max-age=31536000, immutable`；SVG 另附 `Content-Security-Policy: sandbox` 和 `Content-Disposition: inline`。

### UI 发现

`GET /api/v1/plugins/ui` 要求登录并遵循 Backend 模式门禁，使用标准 envelope，`data.plugins` 为按 ID 排序的数组。响应不缓存，不包含配置、密文或本地路径。

- 收录条件：仅包含已启用且所需脚本与样式仍在本地的贡献，后端进程状态不影响页面发现。缺少必要 bundle 时省略该插件，缺少可选图标仍保留菜单。
- 返回内容：版本化资源 URL 和 SHA-384 SRI、按请求语言解析的名称和标题，以及按角色过滤的页面和菜单（管理员同时可见 `user`/`admin` audience）。菜单继承目标页面 audience；用户路由为 `/ext/:id[/path]`，管理员为 `/admin/ext/:id[/path]`；SVG 由前端 sanitizer 处理。

<a id="process_protocol"></a>
## 进程协议与 SDK

协议源为 `plugin-sdk/go/proto/tokenrouter/plugin/v1/plugin.proto`，通过 `make -C plugin-sdk/go proto` 用 `go.mod` 固定工具生成 `pluginpb/v1`。

`Plugin` 为必需服务，`HTTPHandler` 和 `TaskRunner` 对应可选贡献，`Host` 由宿主经 `GRPCBroker` 提供。

SDK 的 `Serve` 负责握手、日志、请求转换、配置更新、panic 恢复和父进程看门狗。HTTP 处理器 panic 返回 HTTP 500，生命周期 panic 转为 gRPC `Internal`；插件自建 goroutine 需自行处理 panic。忽略取消且仍未返回的 HTTP 或任务回调会使 SDK 健康检查失败。

宿主能力按站点、用户、钱包、兑换码、订阅、用量、订单、模型、邮件、验证码和 KV 分组。金额使用十进制字符串，扣款和本人用量调用从 `Caller` 取私有 grant。SDK 将 `ErrorInfo` reason 映射为可用 `errors.Is` 判断的错误，包括投递结果不明的 `ErrEmailDeliveryUnknown`（不得更换幂等键盲目重发邮件）。

### 兑换码签发（`IssueRedeemCode`）

要求 `redeem.issue` 权限，返回 `PL` 开头的 26 位单次码。未使用与已使用状态由普通兑换入口管理，余额码兑换按原规则累计充值并执行适用返利。审计记录类型、数量、目标套餐和重放状态，不记录码值。

| 兑换码类型 | `value` 与附加字段约束 |
| --- | --- |
| `balance` | `value` 遵循钱包正金额格式，禁止携带 `plan_id` 或天数 |
| `concurrency` | 无小数、无前导零的正整数字符串，限原生并发 `INTEGER` 范围，禁止携带 `plan_id` 或天数 |
| `subscription` | 须提供有效 `plan_id`，`value` 为空或 `0`；`validity_days` 非负，`0` 在新发码时读套餐默认值并按原生订阅规则规范化后固定保存 |

- 过期时间：`expires_unix` 为 `0` 表示不过期；非零须晚于新发码时刻且不超过 365×24 小时。已提交重放不重新检查相对期限或当前套餐默认值。
- 额度与返回：发行额度覆盖单码面值、当日余额码面值、并发面值、所有类型的每日数量，以及订阅白名单和天数。订阅码的实际天数经管理和兑换记录接口的可选 `validity_days` 字段返回。

### 邮件发送（`SendEmail`）

要求 `notify.email` 权限。默认额度为每用户每天 5 次、插件每天 500 次；无邮箱和退订返回 `delivered=false`，仍占用一次额度。

- 参数校验：主题 1 至 120 字符，纯文本正文 1 至 8000 字符。先规范化主题首尾空白与正文换行，再比较幂等内容；拒绝主题内控制字符、无效 UTF-8、NUL 及无效用户 ID。
- 失败与重放：认领后已知发送前错误以 `USER_NOT_FOUND` 或 `HOST_UNAVAILABLE` 持久化，同键重放该错误。投递结果不明返回 `EMAIL_DELIVERY_UNKNOWN`，同键重放只读已保存结果，不重新发信。

### 验证码校验（`VerifyCaptcha`）

要求 `captcha.verify` 权限。站点未启用验证码时返回 `enabled=false, success=true`，启用状态与验证使用同一份配置；原认证入口继续执行启动配置中的必需验证码约束。

- Token 与 IP：Turnstile 与阿里云传原始 token，腾讯传包含 `ticket`、`randstr` 的 JSON 字符串。SDK 从 `Caller` 传递原 HTTP 请求的客户端 IP。
- 结果映射：挑战失败返回 `success=false`，配置或未分类服务错误返回 `HOST_UNAVAILABLE`。

### 测试工具

`plugintest` 提供进程内 gRPC 假宿主、余额和 KV，并可注入协议错误，但不替代宿主的权限、额度、审计及 PostgreSQL 事务测试。签到示例先用 pending 记录固定奖励金额，再调用钱包幂等加款，最后 CAS 标记完成，以恢复 KV 认领后失败的加款。

<a id="kv_api"></a>
## KV 接口

`KVGet/Put/Delete/List/Incr` 无需额外权限，只能操作当前插件数据。

- 键与值格式：`collection` 匹配 `^[a-z0-9_.-]{1,64}$`，`key` 为 1 至 256 字节可打印 UTF-8。`value` 须为有效 UTF-8 JSON，输入和 JSONB 规范化后的紧凑表示均不超过 64 KiB；数据库无法表示的 NUL 转义、数值越界等返回 `INVALID_ARGUMENT`。
- 版本控制（`if_version`）：`0` 为无条件；`-1` 要求键不存在或已过期（删除时可用于断言不存在）；正值要求未过期且版本匹配（删除时仍要求存在）；其他负值无效。条件不满足返回 `VERSION_CONFLICT`。删除不存在的键返回 `deleted=false`。每次成功写入递增物理行版本，物理删除后重建从 1 开始。
- TTL 与自增：`ttl_seconds=0` 永不过期，正数为相对秒数（上限与 Go `time.Duration` 整秒范围一致），负数拒绝。自增从不存在或过期的 0 值开始，接受 `1.0`、`1e2` 等数学上为整数的 JSON 数值，拒绝小数及其他 JSON 类型；数值或版本超出 int64 时整次写入回滚。
- 列表分页：`prefix` 为字面前缀（不解释 `%`、`_`）；`cursor` 为上一页最后 key 的排他游标，按字节排序；`limit` 非正取 100，超 500 取 500。每页 key 与紧凑 JSON value 总字节最多 8 MiB（为 16 MiB RPC 上限留元数据空间），可能在达到请求条数前结束。
- 翻页与配额：仅存在下一页时返回 `next_cursor`，调用方须按游标继续读取，不能仅按返回条数判断结束。超过采样额度后新增逻辑键返回 `KV_QUOTA_EXCEEDED`，统计和过期清理见[KV 存储与维护](../architecture/plugin_system.md#kv_storage)。

<a id="web_sdk"></a>
## Web SDK 与示例

`plugin-sdk/web` 提供 `@tokenflux/tokenrouter-plugin-sdk` 类型与 Vite 预设。正式构建将 Vue 和 SDK 分别外置为 `__TR_VUE__`、`__TR_PLUGIN_SDK__`，输出单个 IIFE `index.js` 和导入样式对应的 `index.css`，动态页面导入合入同一脚本。

### 样式与动画作用域

- 选择器隔离：CSS 选择器添加 `data-tr-plugin` 前缀，嵌套规则继承外层作用域并保留相对关系。
- 动画隔离：动画定义及引用同步加插件前缀，shorthand 按 CSS 语法识别名称，不改写同名 easing、direction 等关键字。
- 动态变量限制：存在本地 keyframes 时动画名称必须可静态确定。`fade var(--duration) ease` 可保留动态时长，变量只能提供名称以外分量，不能展开额外动画名称或逗号分隔列表；完全由变量决定名称或产生歧义的声明会使构建失败。需要动态属性时可单独设置字面量 `animation-name` 与动态 `animation-duration`。

### 宿主 API 与内置组件

`useHost()` 返回当前页面范围内的 `HostApi`，浏览器资源通过 API 基址加载，分离部署须在前端 CSP 中允许该来源的脚本和样式。

- `request`：路径以 `/` 开始，禁止绝对 URL、查询片段和路径穿越，查询使用独立参数；响应保留插件自有 `code`、`data` 等字段；HTTP 失败映射为带 `status` 和稳定 `code` 的 `HostRequestError`。
- 401 处理边界：宿主对已校验的插件响应添加 `X-TokenRouter-Plugin-Response: 1` 并经 CORS 暴露；插件自身 401 不触发宿主登录刷新、重放或登出。鉴权中间件前置返回的 401 没有该标记，继续走原登录恢复流程。
- `navigate`：使用 manifest 页面路径，根页面传空字符串，其余页面不带开头斜杠，仅能进入当前 audience 已声明页面。
- 国际化与响应式状态：`useMessages` 依次选择完整语言标签、主语言、英文词典，缺失 key 直接显示 key。用户、语言、主题和路由以只读响应式引用提供，`site` 读取当前公开设置。
- 组件约定：`TrDialog` 实际面板保留 `data-tr-plugin`；`TrTable` 使用 `cell-<key>` 单元格插槽；`TrCaptcha` 将腾讯双字段编码为 JSON，其余提供商票据原样传递。

### 本地 Mock 与示例

本地 Vite 开发服务器将 SDK 导入替换为 mock，SDK 根入口与 `/dev` 的 `configureMock` 共享同一模块实例（普通 npm 安装和本地链接共用请求夹具状态）。mock 使用原生控件、内存账户与可替换请求夹具，刷新页面会重置数据，不证明正式宿主的持久化、权限或视觉一致性；发布构建不包含 mock。

[独立签到插件](https://github.com/TokenFlux/tr-plugin-checkin)包含用户签到页和管理员记录页，经 `HostApi` 发请求和刷新账户，不读取宿主 token。用户页确认签到后保留已完成状态，余额刷新失败时仅提供刷新余额操作，不重发加款。

主仓库使用 Go 内部进程夹具和 Web 最小 Vue SFC 消费夹具验证 SDK，测试不依赖外部仓库或 submodule，Web 夹具不进入 npm 包。

相关文档：[接口目录](index.md)、[开发与验证](../operations/development_workflow.md)。
