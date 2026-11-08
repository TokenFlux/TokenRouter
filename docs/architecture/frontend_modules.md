# 前端模块与依赖

本文说明浏览器应用的目录归属、跨页面共享方式和启动约束。控件外观与交互约定见[前端 UI 规范](frontend_ui_conventions.md)，格式化、检查命令和验证层次见[开发流程](../operations/development_workflow.md)。

## 目录归属

下表中的路径相对 `frontend/src/`。领域内的源码、测试和私有辅助实现共同维护；页面入口通过路由组合这些模块。

| 路径 | 职责 |
| --- | --- |
| `main.ts`、`App.vue` | 安装应用依赖、等待初次导航、组织全局布局和通知 |
| `router/` | 路由分组、守卫安装、导航进度、标题和首次设置跳转 |
| `api/`、`api/admin/` | HTTP 客户端、领域请求和响应类型；管理员专有接口位于 admin |
| `stores/` | 认证、应用设置、公告、订阅、支付等跨页面状态 |
| `pluginHost/` | 共享 Vue 运行时、插件资源加载、页面能力与 SDK 控件适配 |
| `views/` | 路由页面，以及按业务归组的页面私有控制器和组件 |
| `components/common/` | 基础控件与显示组件；表格及其列类型、列顺序逻辑共同放在 table |
| `components/layout/` | 应用外壳、导航和页面布局 |
| `components/plugin/` | manifest 字段表单、权限展示和配置数值校验，供插件管理与后续领域表单复用 |
| `components/<业务>/` | 多个页面共同使用的领域组件；调用方的角色由路由和 API 决定 |
| `composables/` | 跨页面组合式状态和生命周期逻辑；引导步骤与其控制器放在 onboarding |
| `utils/` | 共用的转换、格式、存储和浏览器辅助实现；支付规则放在 payment |
| `i18n/` | 语言目录、错误文案、运营内容翻译和语言切换编排 |
| `types/` | 跨领域 UI 类型和环境声明 |
| `style.css`、`styles/`、`components/icons/` | 全局设计变量、附属样式和统一图标接口 |

## 页面私有实现与共享组件

列表工具栏的范围下拉使用 `components/common/ScopeSelector.vue`，桌面显示图标和当前范围，窄屏显示带无障碍名称的图标按钮。`components/team/ScopeDropdown.vue` 负责个人和团队密钥的 URL 状态，MCP 管理页复用相同控件切换手工令牌和已授权应用。

设置页面保留 `views/admin/SettingsView.vue` 入口，`settings/` 持有领域面板、控制器、草稿、校验和持久化编排。根页面在 setup 时建立领域控制器；bulk 设置分区按字段范围访问同一份 `form`。联网搜索保留自己的草稿，由保存编排按顺序持久化；管理员 API Key 和支付提供商配置使用各自的即时接口。面板隐藏与卸载有不同效果，保存登记、轮询和草稿保留继续遵循 [设置表单](frontend_ui_conventions.md#settings_form) 的规则。

提供商页面的列表、选择、用量、缓存、创建、编辑和操作组件放在 `views/admin/providers/`。创建和编辑各自拥有授权或草稿生命周期，共同依赖 `components/provider/` 的字段与编辑器。模型白名单选择器也被风控页面使用，因此继续作为领域共享组件维护。

支付页面的私有选择和结账逻辑放在页面业务目录，纯支付规则放在 `utils/payment/`。用户结账组件位于 `components/payment/`；提供商配置、密钥字段和管理操作位于 `components/admin/payment/`。跨角色的规则共同维护，管理端配置保持自己的归属。

代理选择器归 `components/admin/proxy/`，系统版本操作归 `components/admin/system/`，公告归 `components/announcements/`，验证码归 `components/auth/`。分组表单规则与模型属性组件分别在 `components/admin/group/` 和 `components/admin/model-attributes/` 中维护，页面通过这些模块使用表单能力。

<a id="dependency_ownership"></a>

## 类型所有权与依赖方向

API 记录按所属领域维护在 `api/*.types.ts` 或 `api/admin/*.types.ts`。已经定义在 API 实现文件中的局部类型继续跟随实现；调用方直接导入实际所有者。公共响应和分页类型在 `api/common.types.ts`，纯 UI 记录在 `types/ui.ts`。模型属性的领域常量与其类型共同维护，运行时消费者使用普通 import，仅使用类型的消费者使用 `import type`。

类型循环反映实际响应关系：`api/identity.types.ts` 与 `api/subscriptions.types.ts` 通过 `User`、`UserSubscription` 互相引用；`api/admin/providers.types.ts` 与 `api/admin/proxies.types.ts` 通过代理记录、提供商平台和类型互相引用。这两组关系使用 type-only import，保留 HTTP 字段和可空性。

依赖检查集中在 `frontend/scripts/check-imports.mjs`：API 模块不能依赖页面、组件、store、router、composable 或 pluginHost；组件不能依赖 view 或 pluginHost 实现；pluginHost 可组合 API、store 和共享组件，但不能导入 view；共享 composable 不能依赖组件实现。页面私有目录只允许自己的文件、登记的页面所有者及准确的路由入口导入。跨模块行为测试的必要访问按准确的文件与目标记录例外。

`i18n/index.ts` 在发布语言后动态加载 `router/title`、`router/index`、`stores/app`、`stores/auth` 、`stores/adminSettings` 和 `stores/plugins`，刷新页面标题并发送语言变化事件。需要持久化账户语言时再加载 `api/client`，按语言代次和用户身份串行写入偏好。这些准确的运行时依赖保持原来的初始化时序；它们不属于 API 向页面依赖的例外。共享工具中存在响应式缓存和浏览器 I/O，阅读调用方后再决定是否提取纯规则。

插件管理目录 `views/admin/plugins/` 有三个路由入口：`PluginsView.vue` 是插件目录（`/admin/plugins`，query.view 区分已安装、可更新和发现），`PluginDetailView.vue` 是单个插件的详情页（`/admin/plugins/:pluginId`，query.tab 区分概览、配置、额度、日志和版本），`PluginSourcesView.vue` 管理索引来源（`/admin/plugins/sources`）。旧的 `?tab=market` 和 `?tab=indexes` 由路由守卫转到发现视图和来源页。详情页只在安装记录属于 query.source 指定的来源时把插件当作已安装；没有 source 时使用安装记录的来源读取目录条目，上传和开发目录安装没有目录条目。共享字段渲染与权限展示保留在 `components/plugin/`。管理器按写操作串行使用 step-up，成功和失败后都读回安装与菜单状态，避免把网络错误当成未写入的证明。配置和额度是详情页的两个标签，各自保存，失败时保留草稿。安装审阅固定索引身份和版本，换版本会清除之前的权限确认；更新失败保留目标和恢复诊断。

## 插件页面与共享运行时

`stores/plugins.ts` 按账号、角色、语言缓存 UI 描述，并每五分钟刷新。侧栏在自定义菜单之后追加对应 audience 的插件菜单，沿用 SVG 清洗；标题解析按插件 ID、audience 和完整静态路径匹配。普通路径为 `/ext/:pluginId`，管理路径为 `/admin/ext/:pluginId`，子路径与 manifest 一致，不含开头斜杠，根页面为空字符串。

`pluginHost/` 在首次加载前发布共享 Vue 与 SDK，按资源 URL 和完整性摘要复用加载。`views/plugin/PluginPageView.vue` 负责当前选择、一次资源失败重试与错误边界；每个挂载实例提供独立 HostApi 和确认弹窗。退出页面会拒绝该实例的新调用并取消待确认决定。资源保留订阅独立于页面生命周期，登出、禁用和描述变化仍能移除旧资源。完整流程见[插件前端宿主](plugin_system.md#frontend_host)。

宿主通过源码路径引用本地 Web SDK 类型；Vite 只将共享错误类和常量映射到运行时源码。生产插件仍把 SDK 和 Vue 外置，不携带宿主实现。根目录及 deploy Docker 构建在前端安装依赖前复制 Web SDK 源码，后端编译前复制 Go SDK；独立 `backend/Dockerfile` 同样使用仓库根目录作为构建上下文。

## 启动与验证约束

前端开发、构建和预览命令显式使用 `vite.config.ts`。旧检出目录可能保留此前 TypeScript 编译生成的 `vite.config.js`；即使该文件仍在，项目命令也不加载它。Node 配置编译产物位于 `node_modules/.cache/tsconfig-node`，避免在源码旁生成会遮蔽配置的文件。

Vite 配置中的相对 TypeScript 导入显式写 `.ts` 后缀，以兼容原生配置加载。`tsconfig.node.json` 启用 `rewriteRelativeImportExtensions`，编译输出时将这些导入改写为对应的 JavaScript 扩展名。

应用先设置主题和 viewport，再安装 Pinia 并读取注入配置。语言初始化及公开设置的语言匹配完成后安装路由和 i18n，等待初次路由就绪后挂载。CLI 导入会话的 fragment 在创建浏览器 history 前清理，导航进度在鉴权守卫前安装。

路由记录分为公开、用户和管理员三组，匹配顺序保持该顺序，catch-all 最后。守卫测试调用实际安装的守卫，验证身份、功能开关、首次设置和重定向，而不是复制另一份判断逻辑。

源码测试需要检查布局或编译期配置时，使用解析后的模板、脚本和样式结构。接口提交、生命周期和权限行为使用真实组件或实际控制器及接口替身验证。格式化引入的换行和引号变化不应成为业务断言的一部分；有意义的模板文本空格需要保留。
