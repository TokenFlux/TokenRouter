# SDK 参考

Go 入口是 `sdk.Serve(plugin)`，前端入口是 `definePlugin({pages})`。完整类型以 [`plugin-sdk/go/sdk`](../../../plugin-sdk/go/sdk/) 和 [`plugin-sdk/web/src/types.ts`](../../../plugin-sdk/web/src/types.ts) 为准，传输消息见[插件契约](../../interfaces/plugin_contracts.md#process_protocol)。

## 生命周期与接口

| Go 接口 | 用途 |
| --- | --- |
| `Plugin.Init(ctx, env)` | 接收插件/宿主版本、配置、宿主客户端与状态目录；成功后才可接收调用 |
| `HTTPPlugin.Handler()` | 提供 manifest 声明的 HTTP 处理器 |
| `TaskPlugin.RunTask(ctx, name, scheduled)` | 处理宿主时区调度的任务；同名任务不重叠 |
| `ConfigWatcher.ConfigChanged(ctx, cfg)` | 应用已保存的配置；未实现时宿主重启进程 |
| `HealthChecker.Health(ctx)` | 返回健康状态；应尽快响应取消 |
| `Shutdowner.Shutdown(ctx)` | 在有限预算内释放插件资源 |

SDK 处理握手、回调连接、panic 恢复和父进程观察。后台 goroutine 仍需作者自己管理；请求 context 取消不代表 goroutine 自动结束。不要在没有退出条件的任务里持有宿主调用或锁。

Config 提供 String、Float、Int、Bool、Secret 等读取器。secret 在宿主数据库中加密，Init 收到已解密配置；日志、响应和前端资源不得反射 secret。新增、删除或改变字段时，考虑上一版本配置的迁移及回滚。

## 宿主能力

| 客户端 | 主要方法与边界 |
| --- | --- |
| Site | `Info`；站点名称、时区与余额单位 |
| Users | `Get`、`LookupIDs`、`LookupUsername`、`LookupEmail`、`Identities`；邮箱读取另需 user.email |
| Wallet | `Get`、`Credit`、`Debit`；金额为十进制字符串，Debit 必须提供本人 Caller |
| Redeem | `Issue`；单次使用的余额、并发或订阅码 |
| Subscriptions | `Plans`、`ListForUser`、`Grant`；发放受套餐白名单和天数限制 |
| Usage | `Self`、`Summary`、`Stats`、`Ranking`；Self 必须提供本人 Caller |
| Orders | `ListForUser`；仅返回协议声明字段 |
| Catalog | `Models`、`GroupsForUser`；复用宿主可见目录 |
| Email | `Send`；持久化认领后投递，不自动重发结果未知的邮件 |
| Captcha | `Verify(ctx, caller, token)`；使用受验证 Caller 中的客户端地址 |
| KV | `Get`、`Put`、`Delete`、`List`、`Incr`；命名空间固定到当前插件 |

`CallerFrom(request.Context())` 返回宿主已经验证的用户信息，Locale 是宿主协商后的请求语言。需要本人权限的 SDK 方法接收 Caller，不接受作者自行构造的 grant 字符串。不要缓存 Caller 作为后台长期授权；票据五分钟后失效，且绑定插件与用户。

资金、兑换、订阅和邮件操作需要稳定的幂等键。同一插件跨能力共享键空间，应使用 `capability:business-id` 等可区分格式；重试同一业务时保持键与业务参数不变。余额金额使用字符串并通过 `ParseAmount` 验证，不要先转换为浮点数再计算。

`EMAIL_DELIVERY_UNKNOWN` 表示宿主无法确定投递结果。应向管理员展示待核对状态，不换新键自动再发。其他宿主哨兵错误可用 `errors.Is` 判断；HTTP 处理器可使用 `sdk.WriteJSON` 和 `sdk.WriteError`。

## KV 与数据兼容

Put 默认无条件写；`IfAbsent()` 只创建不存在或已过期的逻辑键，`IfVersion(v)` 使用正版本 CAS。Delete 的 `ifVersion=0` 无条件，正数要求匹配。TTL 为零时不过期；过期键逻辑上不存在，清理前重建仍会延续物理版本号。

JSON 值最多 64 KiB，key 最多 256 字节并满足字符约束。每插件软配额为 50000 个键、64 MiB；达到配额后已有有效键仍可修改。List 使用字面 prefix 和排他 cursor，不把百分号或下划线解释为模式。分页同时受条数和 8 MiB key/value 总字节预算限制，收到不足请求条数的一页时也要根据 next_cursor 判断是否继续。精确边界见[KV 契约](../../interfaces/plugin_contracts.md#kv_api)。

代码回滚保留业务 KV 和资金台账。数据格式应向前兼容，或为新字段使用版本化记录；不要假设安装、升级或回滚会还原已经执行的外部效果。

## 前端页面

在模块同步执行期间调用 `definePlugin`，pages 的键必须对应 manifest 页面 ID，值可以是 Vue 对象组件、函数式组件或返回 Promise 的加载函数。Vue 和 SDK 由宿主注入，正式 IIFE 不再打包一份 Vue。`tokenrouterPlugin` 预设处理外部依赖、固定文件名、CSS 选择器作用域和动画名称；不要依赖宿主私有组件路径。动画名称的变量限制见[Web SDK 契约](../../interfaces/plugin_contracts.md#web_sdk)。

`useHost()` 在页面 setup 或函数式组件的同步渲染中调用。它提供只读 user、locale、theme、site、route，以及 request、navigate、refreshAccount、toast、confirm。user 仅有 id、username、role。request 只访问当前插件 API，路径以 `/` 开始，query 单独传递；navigate 只接受当前 audience 已声明的插件页面，根路径为空字符串。

`HostRequestError` 保留 status 与稳定 code。插件返回的 401 不触发宿主登录刷新或登出；原生宿主鉴权失败仍使用正常会话恢复。组件卸载或账号变更后，旧 HostApi 不再接受新请求。

控件包括 TrButton、TrCard、TrInput、TrTextarea、TrSelect、TrToggle、TrDialog、TrTable、TrPagination、TrEmpty、TrSkeleton、TrIcon、TrBadge、TrCaptcha、TrAmount。使用公开 props 和宿主主题变量；品牌色使用 `--tr-color-primary`。TrCaptcha 输出不透明票据，服务端仍需调用 Captcha.Verify；在宿主确认关闭验证码时票据可以为空。

`useMessages` 按完整语言标签、基础语言、英文依次回退。文本插值不解释 HTML；需要 Markdown 时由作者明确清洗内容，不把用户或索引提供的 HTML 直接注入页面。
