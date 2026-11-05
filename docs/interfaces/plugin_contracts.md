# 插件契约

本文说明 `plugin-sdk/` 的插件声明、安装包、签名索引、进程协议、宿主能力和管理 HTTP 接口。运行时归属与信任边界见[插件系统](../architecture/plugin_system.md)，安装、升级和恢复步骤见[插件运维](../operations/plugin_operations.md)。SDK 使用 MIT 许可证，宿主使用 LGPL-3.0-or-later 许可证。

## 章节导航

- [插件声明](#manifest)：字段、权限与兼容性。
- [安装包](#packages)：内容、路径与完整性。
- [签名索引](#catalog)：来源与分发记录。
- [安装管理](#administration)：上传、配置与运行控制。
- [HTTP 贡献](#http_contributions)：路由鉴权、响应过滤与公开资源。
- [进程协议](#process_protocol)：宿主能力与 SDK。
- [KV 接口](#kv_api)：版本、JSON、TTL 与分页。
- [Web SDK](#web_sdk)：构建预设与示例。

<a id="manifest"></a>
## 插件声明

`plugin.json` 由 `plugin-sdk/go/manifest` 统一解析。宿主、发布工具和索引构建复用同一套规则：拒绝未知字段，插件 ID 使用 `<publisher>.<name>`，发布版本和宿主版本范围使用完整 SemVer，本地化文本必须有 `en`。

声明至少包含后端或前端贡献之一。后端声明可执行文件、按顺序匹配的 HTTP 路由和定时任务；前端声明 IIFE 入口、样式、按用户或管理员区分的页面和菜单。原始 manifest 字节用于计算摘要，解析和配置规范化不改写发布资产。

配置支持 string、textarea、number、integer、boolean、select 和 secret。缺失值可以填入默认值，显式空字符串、false 和 0 不被默认值覆盖。secret 不允许声明默认值；宿主负责在规范化前合并未提交的旧密钥值，并在持久化时加密。`Normalize` 返回新的配置映射，不返回部分成功结果。

配置字段的可选 `description` 是本地化说明，每种语言最多 300 个 Unicode 字符，提供时必须包含英文回退。表单在字段名下以纯文本显示说明；它不属于管理员提交的配置值。旧版严格解析器会拒绝这一新增字段，因此使用字段说明的插件需要提高 `minHostVersion`，并使用支持该字段的 SDK 打包工具。

兼容性检查同时核对 API 主版本、宿主版本上下界、已实现的权限和平台包。`0.0.0-dev` 或不合法的宿主 SemVer 仅跳过宿主版本范围；权限和平台检查仍然执行。只有没有后端的插件可以使用 `any` 包。

<a id="packages"></a>
## 安装包与完整性

安装包命名为 `<id>-<version>-<os>-<arch>.tar.gz`，纯前端插件使用 `<id>-<version>-any.tar.gz`。根目录直接包含 `plugin.json` 和声明的文件，不再套一层目录。支持 Linux amd64/arm64、macOS amd64/arm64 和 Windows amd64，Windows 的声明可执行路径追加 `.exe`。

`plugin-sdk/go/pkgfmt` 限制压缩包 64 MiB、展开文件总量 256 MiB、前端文件总量 8 MiB、2000 个条目和 10 层目录。只允许普通文件和目录，拒绝链接、设备、重复路径、绝对路径、反斜杠和路径穿越。根目录的 `.tr-installed` 属于宿主保留名，包内不能包含该文件或目录，也不能使用其大小写或尾点别名。解压目的地必须尚不存在，失败时移除本次创建的目录。

解压校验完成后，仅声明的二进制设置为 0755，其余文件为 0644。包图标最多 64 KiB，菜单 SVG 最多 10 KiB。SHA-256 分别标识压缩包、原始 manifest 和二进制；前端文件另计算 SHA-384 SRI。发布器固定文件顺序和归档元数据，相同输入可以生成相同归档字节。

`trplugin validate` 校验目录或归档。如果归档旁存在 `checksums.txt`，还会核对其中的摘要；没有这个文件时，独立校验只能证明结构和声明有效，不能证明发行者身份。

<a id="catalog"></a>
## 签名索引

`plugin-sdk/go/index` 定义 schemaVersion 1 的目录。每个插件最多保留 30 个版本，按 SemVer 降序排列；每个版本带完整 manifest、原始 manifest 摘要以及各平台归档的 HTTPS 地址、SHA-256 和大小。嵌入对象的 JSON 排版可以变化，`manifestSha256` 始终指向 Release 中原始 `plugin.json` 的字节。

`ParseForHost` 在解析嵌入声明前检查 `minHostVersion`。仅当已发布宿主版本低于最低要求时，才保留未知贡献并显示 `HOST_TOO_OLD`；当前宿主可执行的声明和发布工具仍严格拒绝未知字段。

签名文件是 `base64(ed25519.Sign(privateKey, index.json 原始字节))`，与 JSON 分开保存。发布和传输使用同一套 8 MiB 原始目录字节上限，超限时发布工具报错并保留已有输出。验签在解析和替换缓存之前完成。多个受信公钥支持轮换，空公钥列表不能通过验签。

发布工具读取 `publishers/` 和 `plugins/` 的 YAML 登记，抓取 Release 并逐个下载、校验安装包；有后端的官方登记必须同时提供两个 Linux 架构。已经成功收录的仓库与 tag 保留首次摘要，失败版本记录原因并允许后续构建重试。提交权限检查使用可信基线中的发布者账号，新发布者需要维护者明确审核。`trplugin index build` 默认要求 `INDEX_SIGNING_KEY`；只有显式传入 `--unsigned` 才生成不带签名的自定义目录，并移除旧输出中的签名文件。

<a id="administration"></a>
## 安装管理接口

`/api/v1/admin/plugins` 提供已安装列表和安装操作；`/uploads` 接收 multipart 的 `file` 字段，返回 `upload_token`、过期时间、manifest、摘要和平台。上传安装使用 `{source: "upload", upload_token, accept_permissions, enable}`，索引安装使用 `{source: "index", index_id, plugin_id, version, accept_permissions, enable}`，响应包含 installation、needs_configuration 和 repaired。需要确认 manifest 中的全部权限。安装和更新都接受可选的 `ignore_host_version`、`force`，默认均为 false；前者仅跳过最低和最高网关版本要求，后者允许替换同 ID 的持久化安装，包括相同版本。安装接口启用 force 时可显式替换来源；更新接口的索引来源仍固定为当前来源。未开启 force 时，同版本上传仍只用于修复缺失或无效的已安装包。

`/:id` 返回详情或卸载；DELETE 的 `purge_data=true` 清除 KV，缺省保留，台账始终保留。`/:id/enable`、`disable`、`restart` 控制运行意图和本地进程；`/:id/logs` 返回最近 500 行。安装列表和详情的可选 `tasks` 字段返回已登记任务的内存状态及最近执行结果；`next_run_at` 为零值时表示暂停。状态字段见[定时任务](../architecture/plugin_system.md#task_scheduling)。所有路由登记到 adminapi，执行代码、配置和额度变更通过实际 HTTP step-up guard；multipart 上传在 MCP 中使用浏览器入口。

`/:id/update` 接受 `{version, accept_permissions}` 或 `{upload_token, accept_permissions}`。索引更新只能来自当前安装的来源；上传更新可以替换当前分发来源。新增权限未确认时，`PLUGIN_PERMISSIONS_NOT_ACCEPTED` 的 metadata 同时包含完整 permissions 与 new_permissions。`ignore_host_version` 随当前及上一包快照保存，重启和回滚继续使用对应决定；新一次安装或更新缺省重新执行版本检查。安装 DTO 的同名字段返回当前决定。`force` 只作用于本次请求，不跳过完整性、协议、平台、权限确认或配置校验，也不能覆盖开发目录。`/:id/rollback` 交换完整版本快照；失败更新返回 `PLUGIN_UPDATE_FAILED`，metadata 包含 version、start_error、最多 50 行 logs，以及成功恢复时的 restored_version 或恢复失败时的 rollback_error。

`GET /api/v1/admin/plugin-catalog` 返回来源限定的条目、全部保留版本及兼容性；`/:indexId/:pluginId` 读取单条详情，`POST /refresh` 刷新所有启用来源。`/api/v1/admin/plugin-indexes` 的 GET/POST 用于列表和新增，`/:id` 的 PUT/DELETE 用于修改和删除。输入为 name、url、public_key、enabled；更新省略字段时保留，public_key 空字符串清除固定公钥。内置来源只能修改 enabled。来源 DTO 不返回缓存正文，包含最近验签状态、缓存接受时间、刷新中标记和错误。

`/:id/config` 的 GET 和 PUT 使用 `{values: {...}}`。普通字段按提交内容替换并补默认值；secret 省略保留、空字符串清除，返回值为 `{set: boolean}`。`/:id/limits` 接受 wallet、redeem、subscription、email 分组，未给出的字段使用默认值，显式 0 禁止对应操作，null 和未知字段被拒绝。金额额度采用精确十进制 JSON 数字；浏览器可能输出的指数形式会先无损归一化，仍限制为最多七位整数和八位小数。能力 RPC 的金额字符串继续要求普通十进制形式。

管理表单保留数值输入原文，提交前检查整数安全范围与金额的实际小数位，避免 JavaScript 舍入把无效输入变成可接受的值。文本长度按 Unicode 字符计数；RE2 pattern 只由服务器执行，字段失败通过 metadata.fields 中的结构化问题定位。停用安装可保存未填完整的必填项，启用前仍需通过完整验证。配置与额度是两个独立保存动作，界面明确报告部分成功，并在重试时跳过已经完成且未再修改的配置。

<a id="http_contributions"></a>
## HTTP 贡献与公开资源

`ANY /api/v1/plugins/:id/api/*path` 按 manifest 顺序匹配方法和精确或前缀路径，未声明端点返回 404。路径只解码一次，拒绝 dot segment、反斜杠、控制字符和重复分隔符，保留有意义的末尾 `/`。`public` 可匿名；提供 Authorization 时使用现有 JWT 校验并保留其失败规则。`user` 使用用户 JWT，`admin` 使用管理员中间件；所有动态端点经过现有 Backend 模式门禁。公开端点先经过公网 IP 限流，已认证请求再经过用户面板限流。插件 HTTP 不进入面板命令幂等协调器。

请求体按路由限制，超过返回 413。只转发 Accept、Accept-Language、Content-Type、If-None-Match、If-Modified-Since、User-Agent、X-Request-Id 和 X-Plugin-*；Authorization、Cookie、管理密钥与代理地址头均不转发。Caller 取已验证用户及当前用户名，Locale 使用宿主协商后的请求语言并原样传入 SDK，grant 放在私有 RPC 字段；匿名请求没有 Caller。客户端 IP 复用宿主的安全地址解析器。取得租约后复核版本和 manifest 摘要，声明切换时返回 503 `PLUGIN_RESTARTING`，避免旧鉴权规则作用到新端点。

插件成功返回的 HTTP body 原样发送，不套宿主 envelope。状态必须为 200 至 599，body 最多 4 MiB；必须提供可解析的 Content-Type，允许 application/json、text/plain、text/csv、image/png、image/jpeg、image/webp、image/gif、application/octet-stream 及其参数。HTML、SVG、SSE 和无效响应返回 502 `PLUGIN_UNAVAILABLE`。RPC 失败也返回 502，超时返回 504 `PLUGIN_TIMEOUT`；尚未取得租约时的进程不可用仍为 503。响应只保留 Content-Type、Cache-Control、ETag、Last-Modified、Content-Disposition、Retry-After、X-Plugin-*，并加 nosniff；大小写重复的允许头被拒绝。宿主生成的失败响应使用正常面板错误 envelope。

`GET /api/v1/plugins/:id/assets/:version/*file` 无需登录，并经过公网 IP 限流。它只读取本地已有的版本，不下载缺失包；只公开 frontend/ 下的 .js、.css、.svg、.png、.jpg、.webp、.json、.map，以及根目录 icon.svg/icon.png。对应版本 manifest.icon 明确指定的单个 SVG/PNG 也可公开，声明图标及固定根图标均限 64 KiB；其他目录中的文件不因此开放。路径越界、未登记文件、符号链接或尺寸变化返回 404。响应使用安装时的 SHA-256 前 16 字节作为 ETag，Cache-Control 为 `public, max-age=31536000, immutable`；SVG 另有 `Content-Security-Policy: sandbox` 和 `Content-Disposition: inline`。文件句柄的可读范围固定在已接受的尺寸，避免文件随后增长扩大响应。

`GET /api/v1/plugins/ui` 要求登录并遵循 Backend 模式门禁，使用标准 envelope，data.plugins 为按 ID 排序的数组。只包含已启用、所需脚本与样式仍在本地的贡献；后端进程是否可用不影响页面发现。返回版本化资源 URL 和 SHA-384 SRI、按请求语言解析的名称和标题，以及按角色过滤的页面和菜单；管理员同时可见 user/admin audience。菜单继承目标页面的 audience，用户路由为 `/ext/:id[/path]`，管理员为 `/admin/ext/:id[/path]`；SVG 留给现有前端 sanitizer 处理。缺少可选图标时仍保留菜单，缺少必要 bundle 时省略该插件。响应不缓存，不包含配置、密文或本地路径。

全局 `plugins.enabled: false` 时，上述插件路由统一返回 503 `PLUGINS_DISABLED`。管理端仍可查看和卸载。

<a id="process_protocol"></a>
## 进程协议与 SDK

`plugin-sdk/go/proto/tokenrouter/plugin/v1/plugin.proto` 是协议源，`make -C plugin-sdk/go proto` 用 go.mod 固定的工具生成 `pluginpb/v1`。Plugin 是必需服务，HTTPHandler 和 TaskRunner 对应可选贡献；Host 由宿主经 GRPCBroker 提供。

SDK 的 `Serve` 处理握手、日志、请求转换、配置更新、panic 恢复和父进程看门狗。HTTP 处理器 panic 返回 HTTP 500，生命周期 panic 转为 gRPC Internal；插件自行创建的 goroutine 需要自行处理 panic。忽略取消且仍未返回的 HTTP 或任务回调会让 SDK 健康检查失败。

宿主能力按站点、用户、钱包、兑换码、订阅、用量、订单、模型、邮件、验证码和 KV 分组。金额使用十进制字符串，扣款和本人用量调用从 `Caller` 中取得私有 grant。SDK 将 ErrorInfo reason 映射为可用 `errors.Is` 判断的错误，包括投递结果不明的 `ErrEmailDeliveryUnknown`；该结果不得通过更换幂等键盲目重发邮件。

`IssueRedeemCode` 要求 `redeem.issue`。balance 的 value 遵循钱包正金额格式；concurrency 使用无小数、无前导零的正整数字符串，并限制在原生并发 INTEGER 范围内。subscription 要求有效 plan_id，value 为空或 `0`；validity_days 非负，0 在新发码时读取套餐默认值，按原生订阅规则规范化并固定保存。其他类型不能携带 plan_id 或天数。expires_unix 为 0 时码不过期；非零必须在新发码时刻之后且不超过 365×24 小时。已提交重放不重新检查相对期限或当前套餐默认值。

发行额度覆盖单码面值、当日余额码面值、并发面值、所有类型的每日数量及订阅白名单和天数。返回 PL 开头的 26 位单次码，未使用与已使用状态由普通兑换入口管理；余额码兑换会按原规则累计充值并执行适用返利。审计记录类型、数量、目标套餐和重放状态，不记录返回的码值。订阅码的实际天数通过管理和兑换记录接口中的可选 validity_days 字段返回。

`SendEmail` 要求 `notify.email`，主题 1 至 120 字符、纯文本正文 1 至 8000 字符。主题首尾空白和正文换行形式先规范化，再比较幂等内容；主题内部控制字符、无效 UTF-8、NUL 及无效用户 ID 被拒绝。默认额度为每用户每天 5 次、插件每天 500 次。无邮箱和退订返回 `delivered=false`，仍占用一次额度。认领后的已知发送前错误以 `USER_NOT_FOUND` 或 `HOST_UNAVAILABLE` 持久化，同键重放该错误。结果不明返回 `EMAIL_DELIVERY_UNKNOWN`；同键重放只读取已保存结果，不重新发信。


`VerifyCaptcha` 要求 `captcha.verify`。站点未启用验证码时返回 `enabled=false, success=true`；启用状态与验证使用同一份配置。Turnstile 与阿里云保留原始 token，腾讯使用包含 `ticket`、`randstr` 的 JSON 字符串。SDK 从 `Caller` 传递原 HTTP 请求的客户端 IP。现有验证服务判为失败的挑战返回 `success=false`；配置或未分类服务错误返回 `HOST_UNAVAILABLE`。原认证入口继续执行其启动配置中的必需验证码约束。

`plugintest` 提供进程内 gRPC 假宿主、余额和 KV，可注入协议错误。它不替代宿主的权限、额度、审计及 PostgreSQL 事务测试。签到示例使用 pending 记录固定奖励金额，再调用钱包幂等加款，最后 CAS 标记完成，允许恢复在 KV 认领后失败的加款。

<a id="kv_api"></a>
## KV 接口

`KVGet/Put/Delete/List/Incr` 无需额外权限，只能操作当前插件数据。collection 匹配 `^[a-z0-9_.-]{1,64}$`，key 是 1 至 256 字节的可打印 UTF-8。value 必须是有效 UTF-8 JSON，输入和 JSONB 规范化后的紧凑表示均不超过 64 KiB；数据库无法表示的 NUL 转义、数值越界等返回 `INVALID_ARGUMENT`。

`if_version=0` 无条件，`-1` 要求键不存在或已过期，正值要求未过期版本匹配；其他负值无效。条件不满足返回 `VERSION_CONFLICT`。删除不存在的键返回 `deleted=false`；正版本删除仍要求存在，`-1` 可用于断言不存在。每次成功写入递增物理行版本，物理删除后重建从 1 开始。

`ttl_seconds=0` 永不过期，正数为相对秒数，上限与 Go `time.Duration` 的整秒范围一致；负数拒绝。自增从不存在或过期的 0 值开始，接受 `1.0`、`1e2` 等数学上为整数的 JSON 数值，拒绝小数和其他 JSON 类型。数值或版本超出 int64 时整次写入回滚。

列表的 prefix 是字面前缀，不解释 `%`、`_`；cursor 是上一页最后一个 key 的排他游标，使用字节排序。limit 非正时为 100，超过 500 时取 500。每页 key 与紧凑 JSON value 的总字节数最多 8 MiB，因此可能在达到请求条数前结束，为 16 MiB 的 RPC 上限留出元数据空间。只有存在下一页才返回 next_cursor，调用方根据游标继续读取，不能仅按返回条数判断结束。`KV_QUOTA_EXCEEDED` 拒绝超过采样额度后的新增逻辑键，统计和过期清理规则见[KV 存储与维护](../architecture/plugin_system.md#kv_storage)。

<a id="web_sdk"></a>
## Web SDK 与示例

`plugin-sdk/web` 提供 `@tokenflux/tokenrouter-plugin-sdk` 类型与 Vite 预设。正式构建把 Vue 和 SDK 分别外置为 `__TR_VUE__`、`__TR_PLUGIN_SDK__`，输出单个 IIFE `index.js` 和导入样式对应的 `index.css`。动态页面导入合入同一个脚本，CSS 选择器使用 `data-tr-plugin` 前缀；嵌套样式规则继承外层作用域，保留相对关系。

动画定义及引用一起加插件前缀，shorthand 按 CSS 语法识别名称，不改写同名的 easing、direction 等关键字。存在本地 keyframes 时，动画名称必须能静态确定；`fade var(--duration) ease` 可以保留动态时长，变量只能提供名称以外的分量，不能展开额外的动画名称或逗号分隔列表。完全由变量决定名称或产生名称歧义的声明会使构建失败。需要动态时长等属性时，可以单独设置字面量 `animation-name` 和动态 `animation-duration`。

宿主的 `useHost()` 返回当前页面范围内的 HostApi。`request` 路径以 `/` 开始，禁止绝对 URL、查询片段和路径穿越，查询使用独立参数；响应保留插件自有 `code`、`data` 等字段。HTTP 失败映射为带 status 和稳定 code 的 HostRequestError。宿主为已校验的插件响应添加 `X-TokenRouter-Plugin-Response: 1`，并通过 CORS 暴露；插件自身的 401 不触发宿主登录刷新、重放或登出。鉴权中间件在调用插件前返回的 401 没有该标记，继续使用原登录恢复流程。`navigate` 使用 manifest 页面路径，根页面传空字符串，其他页面不带开头斜杠，只能进入当前 audience 已声明的页面。

`useMessages` 依次选择完整语言标签、主语言、英文词典，缺失 key 直接显示 key。用户、语言、主题和路由通过只读响应式引用提供，site 属性读取当前公开设置。TrDialog 的实际面板保留 `data-tr-plugin`；TrTable 使用 `cell-<key>` 单元格插槽；TrCaptcha 将腾讯双字段编码为 JSON，其余提供商票据原样传递。浏览器资源通过 API 基址加载，分离部署需在前端 CSP 中允许该来源的脚本和样式。

本地 Vite 开发服务器将 SDK 导入替换为 mock。SDK 根入口与 `/dev` 的 `configureMock` 共享同一个模块实例，普通 npm 安装和本地链接使用相同的请求夹具状态。mock 使用原生控件、内存账户与可替换的请求夹具，刷新页面会重置数据；它不证明正式宿主的持久化、权限或视觉一致性。发布构建不包含 mock。

[独立签到插件](https://github.com/TokenFlux/tr-plugin-checkin)包含用户签到页和管理员记录页，页面通过 HostApi 发请求和刷新账户，不读取宿主 token。用户页确认签到后即保留已完成状态，余额显示刷新失败时只提供刷新余额的操作，不重新发起加款。主仓库使用 Go 内部进程夹具和 Web 最小 Vue SFC 消费夹具验证 SDK；测试不依赖外部仓库或 submodule，Web 夹具不进入 npm 包。

相关文档：[接口目录](index.md)、[开发与验证](../operations/development_workflow.md)。
