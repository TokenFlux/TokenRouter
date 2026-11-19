# 前端模块与依赖

本文说明浏览器应用的目录归属、页面私有模块与共享组件划分、类型所有权、依赖检查和启动约束。控件外观与交互约定见[前端 UI 规范](frontend_ui_conventions.md)，检查命令和验证层次见[开发流程](../operations/development_workflow.md)。

## 章节导航

- [目录归属](#目录归属)：`frontend/src/` 下各一级目录职责。
- [页面私有实现与共享组件](#页面私有实现与共享组件)：跨页面复用组件与各业务页面私有边界。
- [类型所有权与依赖方向](#dependency_ownership)：API 类型归属、允许循环、静态依赖门禁与插件管理页规则。
- [插件页面与共享运行时](#插件页面与共享运行时)：插件菜单、宿主运行时与 Web SDK 构建约定。
- [启动与验证约束](#启动与验证约束)：Vite/TS 配置、启动时序、路由顺序与测试规范。

## 目录归属

以下路径相对 `frontend/src/`。领域内源码、测试和私有辅助实现共同维护，页面入口通过路由组合模块：

| 路径 | 职责 |
| --- | --- |
| `main.ts`、`App.vue` | 安装应用依赖、等待初次导航、组织全局布局和通知 |
| `router/` | 路由分组、守卫安装、导航进度、标题和首次设置跳转 |
| `api/`、`api/admin/` | HTTP 客户端、领域请求与响应类型；管理员专有接口位于 `admin` |
| `stores/` | 认证、应用设置、公告、订阅、支付等跨页面状态 |
| `pluginHost/` | 共享 Vue 运行时、插件资源加载、页面能力与 SDK 控件适配 |
| `views/` | 路由页面及按业务归组的页面私有模块（管理端 `providers/`、`groups/`、`settings/`、`plugins/`、`ops/`、`orders/`、`affiliates/`、`risk-control/`、`mcp/`、`proxies/`，用户端 `payment/`、`keys/`，首页 `home/`） |
| `components/common/` | 基础控件与显示组件；表格及其列类型、列顺序逻辑共置于 `table` |
| `components/layout/` | 应用外壳、导航和页面布局 |
| `components/plugin/` | manifest 字段表单、权限展示和配置数值校验，供插件管理与领域表单复用 |
| `components/<业务>/` | 跨页面复用的领域组件（`admin/`、`announcements/`、`auth/`、`charts/`、`creative/`、`Guide/`、`keys/`、`marketplace/`、`ops/`、`payment/`、`provider/`、`requests/`、`team/`、`usage/`、`user/`）；角色权限由路由和 API 决定 |
| `composables/` | 跨页面组合式状态和生命周期逻辑（含表格列持久化、断点共享、剪贴板、步进验证等）；引导步骤与控制器位于 `onboarding` |
| `utils/` | 共用转换、格式化、下载、离线国旗、存储和浏览器辅助；支付纯规则位于 `payment` |
| `i18n/` | 语言目录、错误文案、运营内容翻译和语言切换编排 |
| `types/` | 跨领域 UI 类型和环境声明 |
| `style.css`、`styles/`、`components/icons/` | 全局设计变量、附属样式和统一图标接口 |

## 页面私有实现与共享组件

| 领域 | 页面私有归属 | 共享组件或工具归属 |
| --- | --- | --- |
| 范围切换 | 各列表页按需接入 | `components/common/ScopeSelector.vue`（桌面显示图标+当前范围，窄屏带无障碍名称的图标按钮）；`components/team/ScopeDropdown.vue` 维护个人/团队密钥 URL 状态，MCP 管理页复用同控件切换手工令牌与已授权应用 |
| 导航与模型索引 | 侧栏仅负责渲染 | `components/layout/navigationItems.ts` 唯一定义角色入口、功能开关、自定义菜单和插件菜单，`components/layout/search/` 负责搜索排序；模型广场和全局搜索共用 `utils/marketplaceCatalog.ts` 的分组排序、品牌来源与按模型去重索引（见[全局搜索](frontend_ui_conventions.md#global_search)） |
| 系统设置 | `views/admin/SettingsView.vue` 为入口，`views/admin/settings/` 持领域面板、控制器、草稿、校验与保存编排 | 根页 setup 建领域控制器，bulk 分区共享 `form`；联网搜索持独立草稿按序保存；管理员 API Key 与支付提供商走即时接口；面板隐藏与卸载遵循[设置表单](frontend_ui_conventions.md#settings_form) |
| 提供商 | `views/admin/providers/` 持列表、选择、用量、缓存、创建、编辑和操作（创建与编辑各自独立授权或草稿生命周期） | `components/provider/` 放共享字段与编辑器；模型白名单选择器同时被风控页复用 |
| 支付 | `views/user/payment/` 放私有选择和结账逻辑 | `utils/payment/` 放纯支付规则，`components/payment/` 放用户结账组件，`components/admin/payment/` 放管理端配置、密钥字段和操作 |
| 代理、分组与模型属性 | `views/admin/proxies/`（过期推算与详情弹窗）、`views/admin/groups/`（分组草稿与弹窗） | `components/admin/proxy/`（代理选择器）、`components/admin/group/`（共享分组表单规则）、`components/admin/model-attributes/`（模型属性组件） |
| 运维、系统及其他页面 | `views/admin/{ops,orders,affiliates,risk-control,mcp}/`、`views/user/keys/`、`views/home/` | `components/ops/` 放被请求查询、管理端用量和运维看板复用的错误日志表格与详情弹窗；`components/admin/system/` 放系统版本操作，`components/announcements/` 放公告，`components/auth/` 放验证码 |

<a id="dependency_ownership"></a>
## 类型所有权与依赖方向

类型归属与循环约定：

- API 记录按领域维护在 `api/*.types.ts` 或 `api/admin/*.types.ts`（含 `settings.types.ts`、`ops.types.ts`、`providers.types.ts`），对应客户端文件导出或透传；已定义在实现文件中的局部类型跟随实现，调用方直接导入实际所有者。
- 公共响应和分页类型位于 `api/common.types.ts`，纯 UI 记录位于 `types/ui.ts`。模型属性常量与类型共置，运行时消费者使用普通 import，仅类型消费者使用 `import type`。
- 允许的类型循环（须用 `import type`，保留 HTTP 字段与可空性）：`api/identity.types.ts` 与 `api/subscriptions.types.ts`（`User`、`UserSubscription`），以及 `api/admin/providers.types.ts` 与 `api/admin/proxies.types.ts`（代理记录、提供商平台与类型）。

静态依赖门禁（`frontend/scripts/check-imports.mjs`）：

- `api/` 不得依赖 `views/`、`components/`、`stores/`、`router/`、`composables/` 或 `pluginHost/`。
- `components/` 不得依赖 `views/` 或 `pluginHost/` 实现；共享 `composables/` 不得依赖组件实现。
- `pluginHost/` 可组合 API、store 和共享组件，不得导入 `views/`。
- 13 个页面私有目录（`views/admin/{plugins,providers,groups,settings,risk-control,ops,orders,affiliates,mcp,proxies}/`、`views/user/{payment,keys}/`、`views/home/`）仅允许目录内部文件、登记的页面所有者及准确路由入口导入；跨模块测试例外按准确文件与目标登记。

运行时动态依赖与工具边界：

- `i18n/index.ts` 发布语言后动态加载 `router/title`、`router/index`、`stores/app`、`stores/auth`、`stores/adminSettings`、`stores/plugins` 刷新标题并派发事件；持久化账户语言时按需加载 `api/client` 串行写入偏好（按语言代次与用户身份）。此动态时序不属 API 向页面依赖例外。
- 共享工具中含响应式缓存与浏览器 I/O，提取纯规则前需先阅读调用方。

插件管理目录 `views/admin/plugins/` 规则：

- 路由入口：`PluginsView.vue`（`/admin/plugins`，`query.view` 区分已安装、可更新、发现）、`PluginDetailView.vue`（`/admin/plugins/:pluginId`，`query.tab` 区分概览、配置、额度、日志、版本）、`PluginSourcesView.vue`（`/admin/plugins/sources`）。
- 历史参数兼容：旧 `?tab=market` 和 `?tab=indexes` 由路由守卫重定向至发现视图和来源页。
- 来源与详情判定：仅当安装记录属于 `query.source` 指定来源时视为已安装；无 `source` 时按安装记录来源读取目录条目，上传和开发目录安装无目录条目。共享字段渲染与权限展示保留在 `components/plugin/`。
- 写操作与表单：管理器按写操作串行触发 step-up，成败后均回读安装与菜单状态，避免将网络错误误判为未写入。配置和额度为详情页独立标签，各自保存并在失败时保留草稿；安装审阅固定索引身份和版本，切换版本清除既有权限确认；更新失败保留目标与恢复诊断。

## 插件页面与共享运行时

- 菜单与路由：`stores/plugins.ts` 按账号、角色、语言缓存 UI 描述并每 5 分钟刷新；侧栏在自定义菜单后追加对应 audience 的插件菜单（沿用 SVG 清洗），标题按插件 ID、audience 和完整静态路径匹配。路径为 `/ext/:pluginId`（普通）和 `/admin/ext/:pluginId`（管理），子路径无开头斜杠，根页面为空字符串。
- 宿主加载：`pluginHost/` 首次加载前发布共享 Vue 和 SDK，按资源 URL 与完整性摘要复用加载。`views/plugin/PluginPageView.vue` 管当前选择、单次重试和错误边界，每挂载实例提供独立 HostApi 和确认弹窗，退出页面拒绝新调用并取消待确认决定。
- 资源清理：资源保留订阅独立于页面生命周期，登出、禁用和描述变化时仍清理旧资源，详见[插件前端宿主](plugin_system.md#frontend_host)。
- SDK 打包与 Docker 上下文：宿主按源码路径引用本地 Web SDK 类型，Vite 仅将共享错误类和常量映射到运行时源码；生产插件外置 SDK 和 Vue。根目录及 deploy Docker 构建在前端安装依赖前复制 Web SDK 源码，后端编译前复制 Go SDK；独立 `backend/Dockerfile` 同样使用仓库根目录作为构建上下文。

## 启动与验证约束

Vite 与 TypeScript 配置：

- 开发、构建和预览命令显式指定 `vite.config.ts`，忽略旧检出目录可能残留的 `vite.config.js`。Node 配置编译产物输出至 `node_modules/.cache/tsconfig-node`，避免在源码旁遮蔽配置。
- Vite 配置中的相对 TS 导入显式写 `.ts` 后缀以兼容原生配置加载；`tsconfig.node.json` 启用 `rewriteRelativeImportExtensions`，编译输出时改写为对应 JS 扩展名。

应用启动顺序：

1. 设置主题和 viewport。
2. 安装 Pinia 并读取注入配置。
3. 完成语言初始化及公开设置语言匹配。
4. 创建浏览器 history 前清理 CLI 导入会话 fragment；安装路由（导航进度在鉴权守卫前安装）和 i18n。
5. 等待初次路由就绪后挂载。

路由与测试约定：

- 路由记录按公开 -> 用户 -> 管理员 -> catch-all 顺序匹配。守卫测试调用实际安装的守卫验证身份、功能开关、首次设置和重定向，不复制判断逻辑。
- 源码测试检查布局或编译期配置时解析模板、脚本和样式结构；接口提交、生命周期和权限行为通过真实组件或实际控制器加接口替身验证。格式化引入的换行与引号变化不作为业务断言，有意义的模板文本空格需保留。
