# 插件契约

本文记录 `plugin-sdk/` 中的插件声明、安装包、索引和进程协议。SDK 使用 MIT 许可证，与宿主的 LGPL-3.0-or-later 许可证分开。声明、发布工具、进程协议和上传安装管理已接入源码，宿主钱包、直接订阅发放和只读能力已接入，其余宿主业务能力和页面代理仍在实现中。

## 章节导航

- [插件声明](#manifest)：字段、权限与兼容性。
- [安装包](#packages)：内容、路径与完整性。
- [签名索引](#catalog)：来源与分发记录。
- [安装管理](#administration)：上传、配置与运行控制。
- [进程协议](#process_protocol)：宿主能力与 SDK。
- [KV 接口](#kv_api)：版本、JSON、TTL 与分页。
- [Web SDK](#web_sdk)：构建预设与示例。

<a id="manifest"></a>
## 插件声明

`plugin.json` 由 `plugin-sdk/go/manifest` 统一解析。宿主、发布工具和索引构建复用同一套规则：拒绝未知字段，插件 ID 使用 `<publisher>.<name>`，发布版本和宿主版本范围使用完整 SemVer，本地化文本必须有 `en`。

声明至少包含后端或前端贡献之一。后端声明可执行文件、按顺序匹配的 HTTP 路由和定时任务；前端声明 IIFE 入口、样式、按用户或管理员区分的页面和菜单。原始 manifest 字节用于计算摘要，解析和配置规范化不改写发布资产。

配置支持 string、textarea、number、integer、boolean、select 和 secret。缺失值可以填入默认值，显式空字符串、false 和 0 不被默认值覆盖。secret 不允许声明默认值；宿主负责在规范化前合并未提交的旧密钥值，并在持久化时加密。`Normalize` 返回新的配置映射，不返回部分成功结果。

兼容性检查同时核对 API 主版本、宿主版本上下界、已实现的权限和平台包。`0.0.0-dev` 或不合法的宿主 SemVer 仅跳过宿主版本范围；权限和平台检查仍然执行。只有没有后端的插件可以使用 `any` 包。

<a id="packages"></a>
## 安装包与完整性

安装包命名为 `<id>-<version>-<os>-<arch>.tar.gz`，纯前端插件使用 `<id>-<version>-any.tar.gz`。根目录直接包含 `plugin.json` 和声明的文件，不再套一层目录。支持 Linux amd64/arm64、macOS amd64/arm64 和 Windows amd64，Windows 的声明可执行路径追加 `.exe`。

`plugin-sdk/go/pkgfmt` 限制压缩包 64 MiB、展开文件总量 256 MiB、前端文件总量 8 MiB、2000 个条目和 10 层目录。只允许普通文件和目录，拒绝链接、设备、重复路径、绝对路径、反斜杠和路径穿越。解压目的地必须尚不存在，失败时移除本次创建的目录。

解压校验完成后，仅声明的二进制设置为 0755，其余文件为 0644。包图标最多 64 KiB，菜单 SVG 最多 10 KiB。SHA-256 分别标识压缩包、原始 manifest 和二进制；前端文件另计算 SHA-384 SRI。发布器固定文件顺序和归档元数据，相同输入可以生成相同归档字节。

`trplugin validate` 校验目录或归档。如果归档旁存在 `checksums.txt`，还会核对其中的摘要；没有这个文件时，独立校验只能证明结构和声明有效，不能证明发行者身份。

<a id="catalog"></a>
## 签名索引

`plugin-sdk/go/index` 定义 schemaVersion 1 的目录。每个插件最多保留 30 个版本，按 SemVer 降序排列；每个版本带完整 manifest、原始 manifest 摘要以及各平台归档的 HTTPS 地址、SHA-256 和大小。嵌入对象的 JSON 排版可以变化，`manifestSha256` 始终指向 Release 中原始 `plugin.json` 的字节。

签名文件是 `base64(ed25519.Sign(privateKey, index.json 原始字节))`，与 JSON 分开保存。验签在解析和替换缓存之前完成。多个受信公钥支持轮换，空公钥列表不能通过验签。

发布工具读取 `publishers/` 和 `plugins/` 的 YAML 登记，抓取 Release 并逐个下载、校验安装包；有后端的官方登记必须同时提供两个 Linux 架构。已经成功收录的仓库与 tag 保留首次摘要，失败版本记录原因并允许后续构建重试。提交权限检查使用可信基线中的发布者账号，新发布者需要维护者明确审核。

<a id="administration"></a>
## 安装管理接口

`/api/v1/admin/plugins` 提供已安装列表和上传安装；`/uploads` 接收 multipart 的 `file` 字段，返回 `upload_token`、过期时间、manifest、摘要和平台。安装请求使用 `{source: "upload", upload_token, accept_permissions, enable}`，响应包含 installation、needs_configuration 和 repaired。需要确认 manifest 中的全部权限；同版本上传只允许修复缺失或无效的已安装包。

`/:id` 返回详情或卸载；DELETE 的 `purge_data=true` 清除 KV，缺省保留，台账始终保留。`/:id/enable`、`disable`、`restart` 控制运行意图和本地进程；`/:id/logs` 返回最近 500 行。安装列表和详情的可选 `tasks` 字段返回已登记任务的内存状态及最近执行结果；`next_run_at` 为零值时表示暂停。状态字段见[定时任务](../architecture/plugin_system.md#task_scheduling)。所有路由登记到 adminapi，执行代码、配置和额度变更通过实际 HTTP step-up guard；multipart 上传在 MCP 中使用浏览器入口。

`/:id/config` 的 GET 和 PUT 使用 `{values: {...}}`。普通字段按提交内容替换并补默认值；secret 省略保留、空字符串清除，返回值为 `{set: boolean}`。`/:id/limits` 接受 wallet、redeem、subscription、email 分组，未给出的字段使用默认值，显式 0 禁止对应操作，null 和未知字段被拒绝。金额额度采用精确十进制 JSON 数字。

<a id="process_protocol"></a>
## 进程协议与 SDK

`plugin-sdk/go/proto/tokenrouter/plugin/v1/plugin.proto` 是协议源，`make -C plugin-sdk/go proto` 用 go.mod 固定的工具生成 `pluginpb/v1`。Plugin 是必需服务，HTTPHandler 和 TaskRunner 对应可选贡献；Host 由宿主经 GRPCBroker 提供。

SDK 的 `Serve` 处理握手、日志、请求转换、配置更新、panic 恢复和父进程看门狗。HTTP 处理器 panic 返回 HTTP 500，生命周期 panic 转为 gRPC Internal；插件自行创建的 goroutine 需要自行处理 panic。忽略取消且仍未返回的 HTTP 或任务回调会让 SDK 健康检查失败。

宿主能力按站点、用户、钱包、兑换码、订阅、用量、订单、模型、邮件、验证码和 KV 分组。金额使用十进制字符串，扣款和本人用量调用从 `Caller` 中取得私有 grant。SDK 将 ErrorInfo reason 映射为可用 `errors.Is` 判断的错误，包括投递结果不明的 `ErrEmailDeliveryUnknown`；该结果不得通过更换幂等键盲目重发邮件。

`SendEmail` 要求 `notify.email`，主题 1 至 120 字符、纯文本正文 1 至 8000 字符。主题首尾空白和正文换行形式先规范化，再比较幂等内容；主题内部控制字符、无效 UTF-8、NUL 及无效用户 ID 被拒绝。默认额度为每用户每天 5 次、插件每天 500 次。无邮箱和退订返回 `delivered=false`，仍占用一次额度。认领后的已知发送前错误以 `USER_NOT_FOUND` 或 `HOST_UNAVAILABLE` 持久化，同键重放该错误。结果不明返回 `EMAIL_DELIVERY_UNKNOWN`；同键重放只读取已保存结果，不重新发信。


`VerifyCaptcha` 要求 `captcha.verify`。站点未启用验证码时返回 `enabled=false, success=true`；启用状态与验证使用同一份配置。Turnstile 与阿里云保留原始 token，腾讯使用包含 `ticket`、`randstr` 的 JSON 字符串。SDK 从 `Caller` 传递原 HTTP 请求的客户端 IP。现有验证服务判为失败的挑战返回 `success=false`；配置或未分类服务错误返回 `HOST_UNAVAILABLE`。原认证入口继续执行其启动配置中的必需验证码约束。

`plugintest` 提供进程内 gRPC 假宿主、余额和 KV，可注入协议错误。它不替代宿主的权限、额度、审计及 PostgreSQL 事务测试。签到示例使用 pending 记录固定奖励金额，再调用钱包幂等加款，最后 CAS 标记完成，允许恢复在 KV 认领后失败的加款。

<a id="kv_api"></a>
## KV 接口

`KVGet/Put/Delete/List/Incr` 无需额外权限，只能操作当前插件数据。collection 匹配 `^[a-z0-9_.-]{1,64}$`，key 是 1 至 256 字节的可打印 UTF-8。value 必须是有效 UTF-8 JSON，输入和 JSONB 规范化后的紧凑表示均不超过 64 KiB；数据库无法表示的 NUL 转义、数值越界等返回 `INVALID_ARGUMENT`。

`if_version=0` 无条件，`-1` 要求键不存在或已过期，正值要求未过期版本匹配；其他负值无效。条件不满足返回 `VERSION_CONFLICT`。删除不存在的键返回 `deleted=false`；正版本删除仍要求存在，`-1` 可用于断言不存在。每次成功写入递增物理行版本，物理删除后重建从 1 开始。

`ttl_seconds=0` 永不过期，正数为相对秒数，上限与 Go `time.Duration` 的整秒范围一致；负数拒绝。自增从不存在或过期的 0 值开始，接受 `1.0`、`1e2` 等数学上为整数的 JSON 数值，拒绝小数和其他 JSON 类型。数值或版本超出 int64 时整次写入回滚。

列表的 prefix 是字面前缀，不解释 `%`、`_`；cursor 是上一页最后一个 key 的排他游标，使用字节排序。limit 非正时为 100，超过 500 时取 500，只有存在下一页才返回 next_cursor。`KV_QUOTA_EXCEEDED` 拒绝超过采样额度后的新增逻辑键，统计和过期清理规则见[KV 存储与维护](../architecture/plugin_system.md#kv_storage)。

<a id="web_sdk"></a>
## Web SDK 与示例

`plugin-sdk/web` 提供 `@tokenrouter/plugin-sdk` 类型与 Vite 预设。正式构建把 Vue 和 SDK 分别外置为 `__TR_VUE__`、`__TR_PLUGIN_SDK__`，输出单个 IIFE `index.js` 和导入样式对应的 `index.css`。动态页面导入合入同一个脚本，CSS 选择器使用 `data-tr-plugin` 前缀，动画名和引用一起加插件前缀。

本地 Vite 开发服务器将 SDK 导入替换为 mock。mock 使用原生控件、内存账户与可替换的请求夹具，刷新页面会重置数据；它不证明正式宿主的持久化、权限或视觉一致性。发布构建不包含 mock。

`plugin-sdk/go/examples/checkin/web` 包含用户签到页和管理员记录页，页面通过 HostApi 发请求和刷新账户，不读取宿主 token。用户页确认签到后即保留已完成状态，余额显示刷新失败时只提供刷新余额的操作，不重新发起加款。

相关文档：[接口目录](index.md)、[开发与验证](../operations/development_workflow.md)。
