# 前端 UI 规范

> 上级目录：[架构文档目录](index.md)

本文约定前端设计 token、全局样式与基础组件规范，适用范围为 `frontend/tailwind.config.js`、`frontend/src/style.css` 及全部 Vue 组件。浅色配色主题和业务组件内部的局部布局由各组件自行决定。修改前端组件、样式或上述配置文件前，先读本文。

## 章节导航

- [圆角层级](#圆角层级)、[间距约定](#layout_spacing)、[控件尺寸](#控件尺寸)、[开关](#开关)：调整基础样式、布局容器与表单控件时读取。
- [菜单与浮层](#菜单与浮层)、[筛选面板](#filter_panel)、[层级 z-index](#层级-z-index)、[弹窗](#弹窗)：调整浮层、日期选择、筛选与遮罩时读取。
- [全局搜索](#global_search)、[设置表单](#settings_form)、[行列表编辑器](#rule_list_editor)：调整顶栏搜索、设置页或规则列表时读取。
- [断点](#断点)、[加载反馈](#loading_feedback)、[通用图标](#通用图标)、[动画与时长](#ui_motion)、[表格密度](#表格密度)：调整响应式布局、骨架屏、图标、动效与表格时读取。
- [深色配色](#dark_colors)、[图表主题](#图表主题)、[字体和字号](#typography)：调整颜色、图表与排版时读取。
- [合法例外](#合法例外)、[校验](#校验)：确认局部例外和 `check:ui` 验证规则时读取。

## 圆角层级

圆角数值定义在 `style.css` `:root` 的 `--radius-*` 变量中，`tailwind.config.js` 的 `borderRadius` 只通过 `var()` 引用这些变量：

| token | 值 | 用途 |
| --- | --- | --- |
| `rounded-compact` | 6px | 徽章、chip、tab 项、骨架屏、行内代码、小图标块 |
| `rounded-control` | 8px | 按钮、输入框、下拉框、侧栏链接、浮层面板 |
| `rounded-surface` | 12px | 卡片、表格容器、toast、代码块 |
| `rounded-dialog` | 16px | 桌面端弹窗（移动端弹窗用 `rounded-surface`） |
| `rounded-full` / `rounded-none` | 无 | 胶囊、进度条、开关；需要直角时覆盖 |

门禁拦截旧尺度名（`rounded-sm/md/lg/xl/2xl/3xl`）、裸 `rounded` 和 `rounded-[...]` 任意值，这些旧键已从配置移除。裸 CSS 的 `border-radius` 只允许写 `var(--radius-*)`、`0` 或 `9999px`。

全局档位有两类局部例外：

- 微型装饰元素（边长不超过 16px，如用量热力图 12px 格子）：最小档 `compact` 的 6px 已达边长一半，允许用组件级局部变量设更小半径（如 `.heatmap-cell` 的 `--radius-cell: 4px`）。
- 创作台输入框 `CreativeComposer` 外壳：`--composer-radius` 取 `dialog` 与 `control` 之和（24px），用于底部悬浮输入区。

<a id="layout_spacing"></a>
## 间距约定

### 全局网格与容器布局

所有间距落在 Tailwind 的 4px 网格上，`mt-[2px]`、`padding-left: 17px` 这类任意值会被门禁拦截。表单内的 `space-y-2/3/4/6` 按上下文选择，不设统一档位。

| 场景 | 档位 | 说明 |
| --- | --- | --- |
| 卡片与模块之间 | 16px（`gap-4` / `space-y-4`） | 独立大卡片、统计卡、图表卡、移动端数据卡之间，工具栏/控件组与卡片之间，移动端数据卡与分页器之间 |
| 工具栏与控件组内部 | 8px（`gap-2` / `space-y-2`） | 搜索、筛选、刷新、批量操作工具栏外层及左右分组；窄屏换行行距、共用组件和骨架一致 |
| `TablePageLayout` 区间 | 组间 8px，末组到表格 16px | API 密钥页和 MCP 管理页的端点条例外，到上方工具栏和到表格均为 8px |
| `RuleListEditor` 区间 | 卡片 16px，线形 8px | 标题操作区到卡片列表及卡片行之间 16px；线形行到标题操作区 8px |
| 卡片内边距 | `p-6` / `p-4` | 独立卡片 `p-6`；嵌套面板、网格卡和统计卡 `p-4` |

其他通用布局规则：

- 筛选与正文间距：筛选弹层内带标签字段按表单间距排列；分页摘要、正文信息组和卡片区块各自按上下文设间距。
- 标题行底部对齐：页面或卡片标题带说明文字时，同行操作区与整块标题说明底部对齐（`AppLayout` 页头用 `items-end`，自绘页头和带说明卡片标题行同此）。补充信息放进可选 `page-heading-meta` 插槽（说明下方 8px，如用户仪表盘实时状态条），窄屏纵向堆叠时只在横排断点开启底部对齐。
- 控制台水平内边距：控制台的 header 与 main 共用 `px-4 md:px-6 lg:px-8`，各断点边缘对齐。
- 公共顶栏 `AppHeader`：默认首页和控制台共用。左侧为品牌与返回仪表盘图标；右侧依次为[全局搜索](#global_search)入口、文字导航（模型广场、公告、文档、联系我们、当前语言）、访客主题按钮、分隔线、余额与头像。
- 顶栏导航与首页模式：文字导航末尾不加箭头或下拉标记，公告未读时在文字右上角显示红点。登录用户在头像菜单切主题，访客用顶栏按钮。首页通过 `public-page` 隐藏侧栏开关并预留顶栏高度；自定义 HTML 或 iframe 首页使用全页模式。

全局布局尺寸 token 定义在 `style.css` 的 `:root`。顶栏高度、主区 `padding-top`、侧栏遮罩 `top`、侧栏宽度和主区 `lg:ml-*` 偏移都必须引用这些变量，禁止写 `h-14`、`top-14`、`w-56` 字面量，也不要引入 `--main-pad-*`、`--page-heading-space` 等重复变量：

| CSS 变量 | 值 | 用途 |
| --- | --- | --- |
| `--header-h` | `3.5rem` | 顶栏高度；吸顶偏移与 `scroll-margin-top` 写 `calc(var(--header-h) + 余量)` 并在注释说明余量来源 |
| `--sidebar-w` | `14rem` | 侧栏展开宽度 |
| `--sidebar-w-collapsed` | `4.5rem` | 侧栏折叠宽度 |

垂直空间由 `AppLayout` 的 flex 链分配：`wrapper`（`flex-col`）-> `.app-main`（`flex-1 flex-col`）-> 页头 + 页面内容。撑满剩余高度的容器（如 `TablePageLayout`、`CustomPageView`）加 `flex-1 min-h-0`，禁止手写 `calc(100vh - ...)` 或负 margin 抵消父级内边距。

| 视口模式 | 外壳高度 | 滚动与内边距行为 |
| --- | --- | --- |
| 普通模式 | `min-h-screen` | 保留页头与标准内边距，页面随内容滚动 |
| `full-viewport` | 满幅锁定 | `.app-main` 无内边距，用于全屏工作区 |
| `fit-viewport` | `lg` 起 `h-full min-h-0` | 保留页头与内边距，根容器用 `lg:flex-1 lg:min-h-0` 由卡片内部滚动（如兑换页历史列表）；窄屏自然滚动 |
| `fit-viewport="all"` | 全断点动态视口 | 保留页头与内边距，用于自定义页面；iframe 和 Markdown 在卡片内滚动，避免仅设 `min-h-screen` 导致 iframe 退回 150px |

### 页面布局约定

各业务页面的特定布局规则集中如下：

- 默认首页：正文用 `px-6 md:px-8`，页脚用 `px-6 md:px-12`。首屏两按钮在手机各占一行并满宽，640px 起并排居中。四项统计全断点同排，标签在列内换行。
- 用户仪表盘：页头含标题、实时状态和筛选工具。手机指标卡收紧内间距，环比保留“较上期”；请求数、Token 与消费涨跌用中性色，缓存命中率升降用状态色。快捷操作在窄屏和文档顺序中位于公告之前，桌面排公告右侧。
- 仪表盘联系卡片与公告摘要：“联系我们”卡片与顶栏共用 `utils/contactInfo.ts` 解析（链接新标签打开，其余点击复制），桌面撑满右列与公告底对齐。公告标题最多两行，摘要与顶栏共用 `components/announcements/announcementDisplay.ts`（跳过表格和代码块取首段，顶栏单行）。
- 使用记录与支付概览统计卡：横排时图标与文字区顶对齐，图标底块固定 `h-9 w-9` 不收缩，内图标 `h-5 w-5`；说明、多币种金额或单位换行时图标高度保持一致。
- 购买页与确认订阅卡：购买页 `xl` 起双栏（左栏金额/套餐、支付方式、账单信息，右栏费用、实付、付款，窄屏同序纵向），充值档位固定三列显币种，支付方式窄屏单列且手续费可换行。确认订阅卡 `sm` 起左名右价、窄屏价移下方，底部分隔线区分配额并整组换行。
- 系统设置页容器与吸顶导航：内容容器用 `w-full min-w-0` 填满主区，不设居中外边距和最大宽度。吸顶导航放 `.settings-tabs-sticky`（距顶栏 `1rem`，页面底色铺底并用 `box-shadow` 向上覆盖顶栏缝隙），外壳不透明加下投影，深色用 `dark-500` 边线。
- 系统设置页胶囊样式：外壳、一级标签和网关二级标签用 `rounded-full`（选中态为淡品牌青底、边框与图标文字着色）；网关页出现二级标签时外壳改用 `rounded-dialog`。
- 价格管理与属性管理：页签栏到工具栏 16px，工具栏 `gap-2`。搜索框复用输入框图标布局，`sm` 起 `w-64`、窄屏伸缩，占位提示写明可搜配置名或模型名。配置页状态筛选框 `w-32 shrink-0`，默认目录页两筛选进 `FilterDropdown`，目录信息共用 `ModelCatalogInfo`（辅助字号展示来源、短版本号、更新时间，窄屏换行）。
- 模型展示与白名单选择：模型测试、定时测试、白名单及配置选择器显示实际模型 ID；模型广场和首页推荐卡片显示展示名称并提供 ID 复制。白名单候选经统一属性目录分页搜索，保留已选项和手动输入；显式传入候选范围直接使用。真实上游同步合并提供商返回 ID，由管理员保存生效。

## 控件尺寸

### 按钮、输入与原生控件

- 36px 基线：按钮、输入框和下拉触发器统一 36px（`.btn` 和 `.input` 均为 `min-h-9`）；分页器为 28px 例外，见[表格密度](#表格密度)。在基线类上重复写 `h-9` 会被门禁拦截；紧凑档需提到 36px 时写 `btn-sm/md/lg + h-9`。
- 图标按钮与行内按钮：`.btn-icon`（`h-9 w-9`）和 `.btn-icon-sm`（`h-8 w-8`）自带 `rounded-control` 与居中布局，调用处只补 hover 和颜色类。表格行内等紧凑场景用 `.btn-sm`。
- 操作配色与原生表单控件：主操作用 `.btn-primary`，普通编辑、查询和筛选用 `primary-*`；状态提示、业务分类和第三方品牌用各自配色。radio 与 range 全局 `accent-color` 用 `primary-600`。
- 焦点指示：全站控件不画外圈光晕（`ring`），规则由 `focusIndication.spec.ts` 锁定。
  1. `.btn`、`Toggle` 与下拉触发器（`.input-trigger`）只在 `:focus-visible` 时显示 2px 实线 outline（offset 2px，浅色 `primary-600`、深色 `primary-500`，不参与过渡）。鼠标点击不出外环，按钮按下不缩放，鼠标反馈只靠悬停底色或边框。
  2. 文本输入框（`.input`）聚焦时只加深边框（浅色 `black/30`、深色 `dark-400`）。该样式挂在 `:focus-visible` 上，文本框任何聚焦都会命中，按钮形态的触发器只在键盘聚焦时命中。标签输入外壳用 `focus-within` 套同一组边框色。
  3. 下拉展开态（`Select`、`DateRangePicker`、`DateTimePicker`、`ProxySelector`）保持悬停边框（浅色 `black/20`、深色 `dark-500`），靠箭头翻转和面板表明展开。
  4. 校验态用 `.input-error`（自带深色和聚焦变体），调用处不加 `focus:ring*` 或 offset 底色类。
- 复选框：保持原生 `<input type="checkbox">`，由 `style.css` 统一样式（浅色填充 `primary-700`，深色填充 `primary-600`，白色勾选路径描边 1.75，`--motion-fast` 缩放透明过渡，半选显横线）；系统强制配色时回退浏览器原生外观。
- 文本链接：跳转文字用 `.text-link`（始终下划线；浅色默认黑、悬停/聚焦 `primary-600`，深色默认白、悬停/聚焦 `primary-500`）。整块可点击链接外层加 `.text-link-area`，Markdown 容器用 `.text-link-content`，首页底栏链接默认灰、悬停正文色。
- 文字选中与拖拽：原生按钮、`role="button"` 与 `.btn` 禁止文字选中，按钮内图片禁止拖拽；正文、表格数据和输入内容允许选中复制。

### 选择框、分段切换与输入框图标

- 选择框与下拉触发器：统一使用 `frontend/src/components/common/Select.vue`，禁用原生 `<select>`（`check:ui` 无法自动检测，需代码评审确认）。`Select`、`DateRangePicker`、`DateTimePicker` 触发器组合 `input input-trigger` 与状态类。
- 分段切换：2 到 5 个互斥选项用 `.segmented`、`.segmented-item`、`.segmented-item-active` 加 `v-segmented`（`directives/segmented.ts`），放进 36px 工具栏加 `h-9 items-stretch`。页面级大页签用 `.tabs`。
- 分段视觉细节：选中项保留 1px 描边以保证浅色辨识度；圆角取 `--segmented-item-radius`（`control` 减 1px 边框与 2px 内边距，调整轨道边框或内边距时同步更新）。
- 输入框图标与前后缀：使用 `style.css` 的 `input-icon-wrap`、`input-icon` / `input-icon-right`（可点操作加 `input-icon-action`）与 `input-has-icon` / `input-has-icon-right`。留白由 `inset + slot` 推算：

| 档位 | 类名或变量 | inset | 文本留白 | 用途 |
| --- | --- | --- | --- | --- |
| 默认 | `input-icon-wrap` | `0.75rem` | `2.5rem` | 常规带图标输入框 |
| 大号 | `input-icon-lg` | `0.875rem` | `2.75rem` | 登录注册表单 |
| 窄字符 | `input-icon-text` | 无 | `2rem` | `$` 等字符前缀 |
| 紧凑搜索 | 内联 `--input-icon-slot:1.5rem` | `0.75rem` | `2.25rem` | 紧凑搜索框 |

## 开关

全站开关统一使用 `components/common/Toggle.vue`。手写轨道和滑块组合（`h-6 w-11`、`h-5 w-9`）会被门禁拦截。

- 几何变量：尺寸由 scoped CSS 变量 `--toggle-track-w/h`、`--toggle-thumb`、`--toggle-inset` 控制，开启位移动态计算为 `calc(轨道宽 - 滑块 - 2×边距)`。
- 尺寸与形态：`size="md"`（44×24）和 `size="sm"`（36×20）；`variant="inset"`（默认，滑块内嵌）和 `variant="flush"`（大滑块贴边）。
- 配色：开态默认 `toggle-active`（等同 `bg-primary-600`）；关态通过 `off-tone` 选 `default`（`gray-300`）或 `soft`（`gray-200`）。特殊亮色开态（如 `bg-primary-500`）或 hover 配色用 `on-class` / `off-class` 传入整串类名。
- 异步保存：采用受控模式（`:model-value` + `@update:model-value`），由处理函数确认后写回（参考 `ProvidersView` 可调度开关）。

## 菜单与浮层

### 通用菜单、提示与画布浮层

- 下拉容器：菜单和搜索建议列表用 `.dropdown`（含圆角、边框、阴影、深色背景，默认绝对定位与 `py-1`；需固定定位补 `fixed`，自带留白补 `py-0`）。行内 4 个 `*ActionMenu` 用 `.action-menu`（fixed 定位与面板样式），宽度类（`w-48`/`w-52`）与 `action-menu-content` 写在调用处。
- 菜单项与导航浮层：常规项分 `.dropdown-item`（`px-4`）和 `.dropdown-item-sm`（`px-3`）。用户菜单、联系我们、语言切换等导航浮层用内缩结构：`.dropdown py-0`、`.menu-section`（`p-1.5` 带自动分隔线）、`.menu-heading`、`.menu-item`（`rounded-control` 悬停色同 `.sidebar-link`）与 `.menu-item-danger`。
- 顶栏导航按钮：文字入口用 `.header-nav-link`（`md` 起显文字，配色同 `.text-link` 无下划线，悬停/聚焦/展开变品牌色且无底色；`md` 以下收为 36px 图标按钮）。侧栏开关 `lg` 以下保持 36px 方形点击区；纯图标按钮用 `.header-status-icon-button`。
- 信息提示浮层：深底提示（`HelpTooltip`、表格悬停明细等）统一用 `.tooltip-panel`（浅色 `gray-900` 白字；深色 `dark-900` 底、`dark-100` 字、`dark-600` 描边），内部分隔线用 `dark-600`。
- 提示箭头：箭头用 `.tooltip-caret` 旋转方块，调用处补定位与朝外两边（向下 `border-b border-r`、向上 `border-l border-t`、向左 `border-b border-l`、向右 `border-r border-t`）。
- 创作台画布浮层：顶部工具条、设置、历史、输入框和空画布胶囊用 `.canvas-island`（85% 不透明白底或 `dark-900`、淡描边、背景模糊、柔和阴影）；32px 按钮用 `.canvas-tool-btn` / `.canvas-tool-btn-active`，分隔线 `.canvas-tool-divider`；展开菜单用实底 `.dropdown`。
- 叠图 GIF 控件：叠在图片上的 GIF 播放/停止按钮不用 `.canvas-island`，双主题统一 55% 黑半透明圆底、白色实心图标与 15% 白细描边。

### 遮罩、最大高度与统一浮层定位

| 类别 | token / 常量 | 取值与规则 |
| --- | --- | --- |
| 常规遮罩 | `--overlay-bg` | 浅色 `black/50`，深色 `black/70`；模板写 `bg-[var(--overlay-bg)]`，拦截 `bg-black/50` 字面值 |
| 强遮罩 | `--overlay-bg-strong` | 浅色 `black/70`，深色 `black/85`（用于媒体灯箱等） |
| 浮层最大高度 | `max-h-menu-sm` / `max-h-menu` / `max-h-panel` | 对应 `:root` 的 `15rem` / `20rem` / `26.25rem`；拦截 `max-h-[Npx]`（特例加 `check-ui-allow`），不拦截 `vh`/`dvh`/`calc` |
| 浮层定位 | `utils/floatingPanel.ts` (`getFloatingPanelPosition`)、`composables/useAnchoredPanel.ts` | 浮层定位统一由 `getFloatingPanelPosition` 按触发器对齐并在各断点视口边距内夹取，通过 `fixedHeight`、`align: 'left'` 控制；相对定位下拉菜单用 `useAnchoredPanel` 复用该计算并响应窗口缩放；JS 高度常量见 `constants/overlay.ts`（`SELECT_PANEL_MAX_HEIGHT`、`MIN_COMFORTABLE_PANEL_HEIGHT`） |

### 日期与日期时间选择器

原生 `type="date"` 和 `type="datetime-local"` 配色与深色主题不匹配，统一用自绘组件：

- `DateRangePicker`：左侧分组快捷范围，右侧单月日历。两次点击定起止（反向自动对调），起止同主按钮色，中间连淡品牌青色带，今日圆点标记，最晚可选明天；`dateOnly` 可隐藏子日快捷项。
- `DateRangePicker` 关闭与定位：未应用直接取消、点外部或按 Esc 丢弃草稿，弹层打开时 Esc 只关弹层不关外层弹窗；定位由 `getFloatingPanelPosition` 按触发器所在视口左右半边对齐。
- 本地“今天”与跨日更新：使用记录页与无已存范围的仪表盘默认本地“今天”（选“今天”后刷新重算当天边界）。用量排行与 `DateRangePicker` 共用 `useLocalDay`（按下一本地零点调度，兼容夏令时），零点、回焦、后台返回或手动刷新跨日重查；未编辑选区跟随更新，已编辑草稿保留，早发迟到请求不覆盖新请求。
- `DateTimePicker`：值格式为 `YYYY-MM-DDTHH:mm`（空串未设）。左侧单月日历（可选未来、`min` 前置灰），右侧与日历等高时分双列滚动；`placeholder` 说明空值含义，有值右侧显 `×` 清空。
- `DateTimePicker` 写回与快捷项：点确定才写回，“此刻”直写当前时间；`presets` 增左侧快捷列点击直写（公告结束时间为 1、3、7、30 天，从开始时间或现在起算）。Esc 与定位规则同 `DateRangePicker`。

<a id="filter_panel"></a>
## 筛选面板

列表工具栏的漏斗筛选统一使用 `FilterDropdown`，面板呈可折叠字段列表，单次点击即可切换条件。

- 面板外壳：宽 `18rem`，样式同 `DateRangePicker`（`rounded-surface`、淡描边、柔和阴影、6px 内边距），由 `getFloatingPanelPosition` 左对齐并防右侧溢出。激活态按钮显品牌色描边与数量角标，底部追加「重置筛选」（分隔线同 `.menu-section`）；支持 `keep-mounted` 与 `trigger-class`。
- 字段行（`FilterField`）：传 `label` 与 `icon`，标题行高 36px（悬停中性灰）。收起且有值时箭头左侧显示品牌色摘要，展开后隐藏。打开面板时有值字段自动展开，总数不超过 3 个时全展开，支持多字段同时展开。
- 选项行（`FilterOptions`）：props/事件同 `Select`，行高 32px，`.check-mark` 与标题图标同列；选中淡品牌青底与字，悬停中性灰。
- 选项品牌图标：`icon` 插槽渲染 16px 图标（平台用 `PlatformIcon` + `platformIconClass`，品牌/厂商/分组用 `ProviderIcon` 取 `display_brand`/`brand`，模型用 `ModelIcon`），均传 `hide-fallback` 保留列宽对齐。
- 单选语义：后端筛选均为单选，点另项替换、再点已选项取消。首项值为空或 `'all'` 作“全部”不渲染；具体默认值（如运维看板 `1h`）传 `empty-value`，取消选择回到默认值。
- 列表与远程形态：超 6 项静态列表默认显前 5 项加「更多/收起」（选中在后自动展开）；动态列表传 `searchable`（顶部搜索框、`max-h-menu-sm` 滚动）及可选 `creatable`；远程搜索传 `remote` 发 `search`，当前值不在结果中时顶部补 `selected-label` 行。
- 文本筛选与键盘：请求 ID、审计操作者/动作/IP 等用 `FilterTextInput`（有值显 `×`，调用方控制生效并传 `value-text`）。选项组为 `listbox` 仅一项在 Tab 序内；`↑`/`↓`/`Home`/`End` 移焦，回车/空格选中，Esc 关面板回焦按钮，「重置筛选」后焦点移至首个字段标题。

## 断点

- 断点常量：`constants/layout.ts` 定义 `BREAKPOINT_SM/MD/LG`（640/768/1024，与 Tailwind 默认 screens 一致）、`MEDIA_MIN_*`、`MEDIA_MAX_*` 及 `TABLE_DESKTOP_MEDIA_QUERY`（`lg` 别名，供表格、分页器及 Ops 五个表格共用），由 `breakpointTheme.spec.ts` 校验一致。
- 区间无重叠：`max` 变体约定为 `max = min - 1px`。JS 使用 `MEDIA_MAX_SM`，避免 `<= 640` 与 CSS 断点错位 1px。
- 门禁与 CSS 例外：JS 断点判断（`useMediaQuery`、`matchMedia`、`innerWidth`）必须引用常量，拦截字面量。CSS `@media` 不支持 `var()`，例外（如 `CustomPageView` 目录抽屉 639px）写字面量加注释并由测试锁定。

## 层级 z-index

层级数值在三处保持一致：`style.css` `:root` 的 `--z-*`、`tailwind.config.js` 的 `zIndex`（仅 `var()` 引用，供 `z-modal` 等工具类）和 `constants/overlay.ts` 的 `Z_INDEX`。`src/__tests__/zIndexTheme.spec.ts` 校验三处同步，门禁拦截 `z-[...]` 与数字字面量。

| 值 | token | 用途 |
| --- | --- | --- |
| 30 | `sidebar-overlay` | 侧栏遮罩 |
| 40 | `sidebar` | 侧栏 |
| 45 | `chart-tooltip` | 图表 tooltip（高于侧栏、低于顶栏和弹窗；弹窗内图表挂在弹窗层） |
| 50 | `header` / `modal` | 顶栏 / 弹窗遮罩（同层由 DOM 顺序和 teleport 决定先后） |
| 60 | `modal-nested` | 嵌套弹窗、筛选面板 |
| 100 | `tooltip` / `announcement` | 弹窗内 tooltip、下拉面板、灯箱 / 公告底层 |
| 120 / 140 | `announcement-raised` / `announcement-top` | 公告层级（递增） |
| 9998 | `menu-overlay` | ActionMenu 点击捕获层 |
| 9999 | `toast` / `action-menu` / `teleport-tooltip` | 通知 / 行内操作菜单 / teleport 提示浮层 |
| 99999 | `help-tooltip` | HelpTooltip（teleport） |
| 100000000 | `tour` | driver.js 引导层（仅登记数值，无工具类；`onboarding.css` 保留字面值与 `!important`） |
| 100000020 | `teleport-dropdown` | Select 等 teleport 下拉（压过引导层） |

局部堆叠上下文不入全局表：`DataTable` 内部（0/20/200/210/220）、`CreativeCanvas` 内部、`CustomPageView` 目录抽屉、弹窗内 sticky 表头（`z-[1]`）加 `check-ui-allow` 豁免。表格内局部 dropdown 直接用 `z-50`。

## 弹窗

- 默认基类 `BaseDialog`：支持 `narrow`、`normal`、`wide`、`extra-wide`、`full` 五档，内置 Esc、外部点击、焦点与滚动锁。输入法组词期间按 Esc 不关闭；面板内含全部控件时可开 `trapFocus`（默认关，版本更新日志启用并在关闭后回焦版本徽标）。
- 头部插槽与滚动：`subtitle`（`text-xs`）、`header-icon`、`header-actions`（关闭按钮前）组成头部。弹窗及内部滚动容器经 `.modal-content` 隐藏原生滚动条。分页表单可设 `bodyScroll=false` 固定头、页签与底栏（分组 `GroupSettingsForm`、提供商及属性配置均采用此模式）。
- 贴边分栏工作区：设 `flush` + `bodyScroll=false` 各栏自滚动；侧栏用浅底（`gray-50/70` / `dark-950`）加单侧分隔线，底栏配 `rounded-b-surface sm:rounded-b-dialog`。窄屏上下堆叠时 `min-h-0 flex-1` 仅在 `md:` 生效，避免内容被压扁。
- 提供商连接测试参考实现：共用标题、侧栏宽度、三列指标与 `ProviderTestResultView`（决策测试用 `SystemOneTestAnswers`，格式选择在标签旁，问题标识及条件折叠；批量测试详情复用同一结果组件）。
- 嵌套层级与安全凭证：嵌套弹窗传 `z-index="modal-nested"`（`Z_INDEX.MODAL_NESTED`），同层按挂载顺序叠放（如余额记录开充值框）。TOTP、登录验证、提权用 `AuthCardDialog`（居中图标头、无关闭按钮、`p-6`、原地渲染、`z-index` 控嵌套）。
- 公告弹窗：正文均由 `AnnouncementArticle` 渲染（标题、分钟时间、上下渐隐 Markdown）。`AnnouncementBell` 固定高 `36rem`（列表区 `p-2`、未读加粗配品牌青点、7 天内相对时间/更早月日），详情层覆盖列表且列表 `inert`，Esc 先返回再关。`AnnouncementPopup` 随内容高度，自动弹出带“知道了”，预览仅显关闭按钮。
- 迁移原则：标准“标题头、内容、按钮行”弹窗统一用 `BaseDialog`；特殊视觉结构保留手写并登记在[合法例外](#合法例外)。

<a id="global_search"></a>
## 全局搜索

- 入口：`AppHeader` 与未登录模型广场顶栏把 `GlobalSearchTrigger` 放右侧最前，顶栏变窄时仅它收缩。`sm` 起为 36px 拟输入框按钮（`w-44` 至 `w-72`，最小 `min-w-32`），`lg` 起显 `.kbd`（`⌘K` / `Ctrl K`）；`sm` 以下用 `.header-nav-link` 图标。
- 面板与快捷键：`GlobalSearchPalette` 在 `App.vue` 挂载一次，状态在 `components/layout/search/globalSearchState.ts`。仅入口挂载且无其他弹窗时响应 `⌘K`/`Ctrl K` 与输入框外 `/`；顶边 `12vh`、最宽 `max-w-2xl`，手机全屏并用“取消”关闭。
- 键盘导航：焦点留在输入框，`aria-activedescendant` 标示当前行。`↑`/`↓` 移动，回车打开，`⌘↵`/`Ctrl↵` 复制模型 ID 不关面板，`Tab`/`Shift+Tab` 切“全部/模型/页面/操作”，Esc 先清查询再关闭；输入法组词期间忽略。
- 结果行与数据源：当前行同 `.menu-item` 悬停态，命中字符用链接品牌色加粗。模型来自模型广场接口经 `utils/marketplaceCatalog.ts` 合并报价，显图标、展示名、等宽 ID 与最低价（多分组加“低至”）。
- 页面、操作与别名：页面来自 `components/layout/navigationItems.ts`（规则同侧栏；管理员标“管理”或“我的账户”，访客仅模型广场/登录/注册/文档）；操作含建 API 密钥、切主题/语言、复制 API 地址、退出；中英文别名在 `components/layout/search/searchCatalog.ts`。
- 空查询与跳转：空查询依次显最近访问（按账号本地最多 5 条）、推荐模型（`home_featured_models`）和常用页面；无结果提供“在模型广场中搜索”。选模型跳 `/models?q=<模型 ID>`（填搜索、清筛选、闪品牌色描边）；建密钥跳 `/keys?create=1`，弹窗后移参。

<a id="settings_form"></a>
## 设置表单

设置项较多的弹窗与整页表单，使用 `components/common/settings/` 下的组件搭建：

| 组件 | 用途与关键约束 |
| --- | --- |
| `SettingsTabs` | 接收 `tabs`（`key`/`label`/`hidden`）提供同名插槽；面板常驻挂载保留草稿，`hidden` 隐藏当前页签时回首个可见项；切页滚顶，支持方向键/`Home`/`End` |
| `SettingsSection` | 提供 `text-sm font-semibold` 标题、`.input-hint` 说明与 `actions` 插槽；相邻分区自动加 `border-t pt-6`，分区间距 24px、区内 16px；`.input-label` 专用于字段标签 |
| `SettingToggleRow` / `SettingRow` | 布尔项用 `SettingToggleRow`（左标题/说明/可选 `HelpTooltip`、右 `Toggle size="md"` inset；风险提示放 `hint` 插槽）；选择框或短输入框用 `SettingRow` + `field`（宽 `sm:w-56`，窄屏上下排）；无说明时垂直居中 |
| `SettingsSubpanel` | 依赖开关的条件字段放进 `Collapse` 包裹的 `SettingsSubpanel`（`rounded-surface`、淡边框、浅底、`p-4`） |
| `SettingsSegmented` / `SettingsNotice` | 2-5 互斥项用 `SettingsSegmented`（传 `:ariaLabel` 输出 `aria-label`），带图标说明的类型选卡选中显品牌色描边浅底；提示用 `SettingsNotice`（`info`/`warning`/`error`） |
| `SettingsCard` | 整页设置分组容器：`text-lg` 头部、`p-6` 内容、分区间隔 24px；卡内不放保存按钮，工具按钮和总开关放 `actions` 插槽 |
| `LocalizedEditor` / `LocalizedFieldsEditor` | 运营多语言文案：表单内仅编原文，标签行右侧 `text-xs` 译文入口（过期或待选原文语言变琥珀色）；点击开 `modal-nested` 贴边分栏翻译弹窗，支持 `placeholder`/`inputClass`/`layout` |
| `SettingsTagInput` | 逐条录入字符串列表（邮箱后缀、转发 IP 头、模型媒体参数）；组件负责展示与转发事件，分隔、去重和规范化由调用方处理 |
| `BulkApplyField` | 批量编辑包装器：左侧 `${id}-enabled` 复选框控制是否提交，未勾选时内容区加 `inert` 并置灰；布尔项放 `control` 插槽 |

表单校验、字段布局与整页保存规则：

- 校验定位：表单加 `novalidate`，提交先调 `validate()`，自动切到无效字段页签再触发原生校验；业务校验用 toast 并调 `revealField()` 定位（提供商弹窗用 `data-provider-field`），新手引导 `onboarding-reveal` 走同一流程。
- 长短字段布局：开关、选择框和短数字用 `SettingRow` 左右排；URL、密钥、长文本和多行文本用上下布局（`.input-label` -> 满宽输入 -> `.input-hint`），两个以上或同类短字段组（平台阈值、权重、默认值）可用 `md:grid-cols-2` 网格。
- 整页保存栏（`SettingsSaveBar`）：有未保存修改时视口底部居中浮出深色条（浅色 `gray-900`，深色 `dark-900` + `dark-500` 边线；`rounded-dialog` 16px 配 8px 内边距，与内部 `rounded-control` 8px 按钮同心），含“放弃”与“保存设置”。
- 脏状态追踪：每块用 `useDirtyTracker` 记快照（去除只读展示字段如联网搜索已用额度），加载/保存后调 `markClean(key)`；页面用 `provideSettingsSaveRegistry` 建表，子组件用 `useSettingsSaveTarget` 登记保存与 `discard` 重载函数。
- 提交与放弃语义：保存按序只提交脏块，单块失败不影响其他块，全成功提示一次；放弃时等待全部脏块重载完成（含失败块）再解除恢复态，通用设置图片编辑器恢复期间禁用并取消待完成读取。

<a id="dark_colors"></a>
## 深色配色

文字与表面色阶定义在 `tailwind.config.js` 的 `colors.dark`，边框定义在同文件的 `darkEdges`：

| 档位 | 值 | 角色 |
| --- | --- | --- |
| `dark-50` | `#FAFAFA` | 标题和强调文字 |
| `dark-100` | `#DEE0E2` | 正文文字、侧栏导航和顶栏图标默认色 |
| `dark-200` | `#D4D4D8` | 次强文字、占位文字底色 |
| `dark-300` | `#A1A1AA` | 次要文字 |
| `dark-400` | `#8B8B94` | 辅助文字、表头文字 |
| `dark-500` | `#5F5F67` | 图标、禁用文字 |
| `dark-600` | `#3D3D42` | 较强中性填充 |
| `dark-700` | `#27272A` | 中性填充：chip、禁用控件 |
| `dark-800` | `#17171A` | 弱填充：表格行 hover、嵌套面板 |
| `dark-900` | `#0F0F10` | 卡片、弹窗、下拉面板、表头 |
| `dark-950` | `#141416` | 控件底：输入框、次级按钮、Tab 轨道、行内代码（略亮于卡片） |

`borderColor.dark`、`divideColor.dark`、`ringColor.dark` 共用 `darkEdges`（基于 `#FCFCFE` 透明度，与文字背景解耦）：

| 边线档位 | 透明度 | 用途 |
| --- | --- | --- |
| `dark-400` | 30% | 控件焦点 |
| `dark-500` | 12.5% | 控件 hover、较强边线 |
| `dark-600` | 7.8% | 卡片、输入框、弹层默认边框 |
| `dark-700` | 4% | 表格内分隔线 |
| `dark-800` | 3.1% | 顶栏、侧栏和页面分割线，以及其他弱分隔线 |
| `dark-900` | 2% | 最弱边线 |

深色样式关键约束：

- 透明度叠加与中性边框：修饰符与档位透明度相乘（如 `dark:border-dark-600/70` 为 5.46%）；裸 CSS 用 `theme('borderColor.dark.600')` 引用。深色中性边框统一用 `dark-*`，禁用 `gray`/`slate` 实色描边。
- 页面与导航底色：`--page-bg` 浅色 `#FCFCFE`、深色 `#0A0A0B`，`html`/`body`/主题壳/滚动条轨道均用该纯色（无渐变）。`.site-header` 浅色 65% 白、深色 80% `--page-bg` 加模糊；`.sidebar` 不透明（白 / `--page-bg`）；顶栏与侧栏边线用 `dark-800`，控件与卡片用 `dark-600`。
- 选中、悬停与焦点：选中行用 `primary-500/8` 底 + `primary-500` 字（带框 `primary-500/15`）；未选中项 hover 用 `dark-800` + 品牌青字，已选中项 hover 保持底色。控件焦点规则见[焦点指示](#控件尺寸)。
- 新手引导与遮罩：深色 `--overlay-bg` 为 `0.7`、`--overlay-bg-strong` 为 `0.85`。新手引导通过 `styles/onboarding.css` 的 `--tour-*` 变量取色（深色提示框 12% 状态色底、操作提示品牌青、辅助文字 `dark-300`）。
- Vue Scoped 规则：`<style scoped>` 中深色后代规则必须写 `.dark .component-class`（含滚动条伪元素）。禁止写 `:global(.dark) .component-class`，避免编译为全局 `.dark` 污染根元素。

## 图表主题

- 响应式主题：`composables/useChartTheme.ts` 提供响应式 `colors`（`text`/`muted`/`grid`，zinc 色系）和 `onThemeChange` 钩子。门禁拦截非响应式 `document.documentElement.classList.contains('dark')`；命令式组件（如 Stripe Elements）用 `watch` 调 `elements.update({ appearance })`。
- 调色板与字号：分布图统一用 `CHART_PALETTE`（12 色按序取色），“Others”用 `CHART_OTHER_COLOR`，token 趋势序列用 `CHART_SERIES_COLORS`；刻度字号 `CHART_TICK_FONT_SIZE`（10），图例字号 `CHART_LEGEND_FONT_SIZE`（11）。
- 饼图与环图：扇区按原始值绘制，不做对数压缩、不设最小占比、零值不占区，“其他”按实际值计算。分组、端点、成员消费与运维请求时长分布用环形饼图（分组与端点旁留明细表，成员与时长用图例 + tooltip）。
- 分布表悬停：模型、分组与端点分布表名称与箭头默认 `gray-900`/`white`，可展开行整行悬停变 `primary-600`/`primary-500`；关闭明细或分组 ID `<= 0` 保持普通文字色。
- 用户仪表盘趋势图（`UserDashboardUsageChart`）：纯折线无填充无辉光，单指标叠加 `colors.muted` 虚线上期对比线，本地插件绘制峰值环、均值虚线、标签胶囊和呼吸点，未结束时段画虚线。
- 业务配色保留：`TeamMemberUsageCharts` 成员固定配色、`OpsSwitchRateTrendChart` 与 `DashboardView`（深色刻度 `#D4D4D8`、网格 `#27272A`）及 `DailyRevenueChart` 留在组件内。
- 数值格式：token 格式化用 `utils/format.ts` 的 `formatTokens`（两位小数千分位）和 `formatTokensK`（一位小数）；`ProviderTodayStatsCell` 保留 K1/M2 混合精度。

<a id="loading_feedback"></a>
## 加载反馈

### 路由导航进度

路由切换由 `NavigationProgress` 在顶部展示 2px 品牌青细线（`pointer-events: none`），不反映完成百分比。`router/navigationLoading.ts` 在鉴权守卫前注册并按导航编号隔离，防止旧导航淡出关闭新导航。结束时铺满淡出并由 `animationend` 隐藏（减少动画为静态短退出）；全局指示器只管路由，不管页内请求。

### 骨架屏与操作反馈

数据尚未返回时，在内容位置渲染骨架，保留页头、筛选工具栏、卡片外框和已知文字（表头、移动卡字段名、设置页签），避免加载期间展示误导性的零值或“暂无数据”。通用骨架用 `Skeleton.vue` 与 `.skeleton`（减少动画关闭脉动），装饰块加 `aria-hidden`，容器标明加载标签与 `aria-busy`。

| 组件 / 场景 | 加载策略 |
| --- | --- |
| 整页与通用组件 | `DashboardSkeleton`、`SettingsSkeleton`、`ModelMarketplaceSkeleton`、`ChartSkeleton`、`ContentSkeleton`、`DataTable` 骨架、`TableSkeletonBody`（保留实际表头并同步可见列数） |
| 公告与热力图 | 仪表盘公告按时间线占位，热力图用日期格子；顶栏公告列表仅在尚无数据时占位，刷新或全标已读保留现有行 |
| 用户仪表盘 | 用量卡、趋势图与 5 行排行仅首次取数显示骨架，刷新或切筛选保留已有数据；实时状态条首次行内骨架，轮询失败显 `` `—` `` 且不弹错误 |
| 汇总、分页与表单 | 区分首次加载、失败与成功零值/空态；用量汇总换筛选时等待新范围，同范围刷新可保留旧值；加载更多仅末尾追加；邮件模板切事件/语言后待读取成功才开放编辑 |
| 多布局与支付 | 多布局页面按当前入口显示对应骨架（如直达购买页订阅目录）；支付页首次读单和初始化用骨架，渠道已受理或待外部支付用状态提示 |

提交、刷新、授权跳转、连接测试、任务执行和支付处理等动作使用操作反馈，不用骨架。加载样式与请求、轮询、支付 SDK 及 iframe 挂载时机保持独立。

## 通用图标

通用图标统一使用 `components/icons/Icon.vue`，名称与 `IconName` 类型由同目录 `registry.ts` 管理（Lucide 风格 + Lucide Animated `motion-v` 动效；无官方动画的图形悬停做 400ms 轻缩放；许可与版本见同目录 README/LICENSE）。

- 接口与默认属性：保留 `name`、`size`、`strokeWidth`（默认 1.75、继承 `currentColor`），根节点为单个 SVG 并透传属性与事件。
- 尺寸档位：`xs` 12px（表格微型操作）、`sm` 16px（普通按钮、纯图标操作按钮、分页、选择框箭头、弹窗关闭、侧栏子项、刷新/创建/编辑）、`md` 18px（侧栏一级导航含自定义 SVG、顶栏工具区）、`lg` 24px（快捷入口卡片主图、灯箱关闭）、`xl` 32px。图标尺寸不影响按钮点击区。英文导航省去冗余的 Management、Records 等词。
- 悬停与时间轴动效：`animateOnHover` 默认开启，绑定最近交互控件或 `data-icon-trigger`（独立图标响应自身），键盘聚焦同效；每次进入播一次，离开复位，首挂不播。演示时间轴用 `animationActive`（Key 重定向演示由 `animationstart` 触发并重播重置）。
- 停止条件与旋转图标：`disabled`、`aria-disabled`、父级 `inert`、加载转圈与减少动态效果立即停播，状态结束以动画事件为准，不用定时器估算。`animate-spin` 与 `.spinner` 在减少动画模式下静止。展开、排序、选中指示图标设 `:animate-on-hover="false"`。
- 无障碍与焦点：默认 `aria-hidden`、`tabindex="-1"`、`focusable="false"`（含内部图形），`useIconAnimation` 交回或清除点击焦点避免焦点方框；明细提示按钮焦点环用 `focus-visible`。
- 防选中与拖拽：通用和模型品牌图标加 `select-none`。可拖拽行与手柄内的装饰图标加 `pointer-events-none`，避免 Chromium 阻止原生拖拽（如分组排序弹窗与支付提供商列表）。
- 品牌与模型图标：供应商、平台品牌与分组展示品牌用 `ProviderIcon`（`brand`），具体模型用 `ModelIcon`（`model`），未配品牌图形的平台用 `PlatformIcon`。OpenAI 默认浅黑深白单色；Google 默认彩色 G，传 `color` 用单色（如网关页签 `currentColor`）。新增通用图标入统一映射，用户上传 SVG 经净化流程。

<a id="ui_motion"></a>
## 动画与时长

普通交互使用 `style.css` 的动效变量，Tailwind 的 `duration-fast/normal/layout` 仅引用这些变量：

| CSS 变量 | 值 | 用途 |
| --- | --- | --- |
| `--motion-fast` | `150ms` | 按钮、提示、内容淡入（含 `fade-slow` 兼容名） |
| `--motion-normal` | `200ms` | 菜单、开关、短列表、分段背景移动 |
| `--motion-layout` | `220ms` | 折叠、侧栏、弹窗进入、购买页横向翻页 |
| `--motion-exit` | `150ms` | 浮层退出 |
| `--motion-ease` / `--motion-ease-exit` | 公共缓动 | 进入与退出曲线；菜单位移 `--motion-shift`（`4px`） |

核心动效原语与生命周期规则：

- 全局过渡类：写在 Tailwind `@layer` 外防裁剪，含 `fade`/`fade-slow`（纯透明）、`dropdown-fade`/`pop-float`（透明+位移，向上用 `--dropdown-shift`）、`modal`/`pop-fade`（遮罩+面板 `0.98` 缩放）。`top`/`left`/`bottom` 不参与过渡，带定位 `transform` 的提示仅用 `fade`。
- `MotionTransition`：透传 Vue Transition 属性，退出时给旧节点设 `inert`；仅暂禁校验失败的表单控件防打断原生校验，避免其他控件闪灰。`v-show` 须传 `persisted`。菜单点击捕获层关闭时即删，面板等退出完再删。
- 浮层跟随与外部点击：`Select`、`HelpTooltip` 等经 `useFloatingMotion` 跟随折叠/隐藏/`inert` 祖先自动关闭。`Select`、日期选择器和 `FilterDropdown` 在 `document` 捕获阶段监听外部点击，兼容弹窗内停止冒泡的场景。
- 折叠与展开：`Collapse`（`open`/`unmountOnHide`/`animate=false`/`appear`/`after-enter`/`after-leave`）以 Grid 自适应高度，收起设 `inert`。表格明细用 `ExpandableTableRow`，原生 `<details>` 改用 `Disclosure`。`form-field-reveal` 与 `onboarding-reveal` 跳过动画即刻定位。
- 分段与内容显现：`vSegmented` 驱动轨道 `::before` 平滑过渡，经 `ResizeObserver` 响应尺寸变化，不增聚焦节点。`vContentReveal` 对已有容器做 150ms 淡入（`AppLayout` main、`AuthLayout` 内容区及公开页），按 `route.path` 触发，忽略 query/hash 变化。
- 弹窗与短列表：`useDialogLifecycle` 保持滚动锁到退出完成，防重入计数和焦点抢占，Esc 只关最上层 `BaseDialog`；安全凭证弹窗用 `useLeavingPresence` 等 `after-leave` 卸载。Toast 和用户增删短列表用 `motion-list`（稳定业务 key）；分页表、虚拟列表和轮询结果不做逐行动效。
- 减少动态效果：开启后普通变量缩为 `1ms`、位移归零，动态切换偏好取消进行中的淡入；完成与清理依赖动画事件，不用定时器。主题切换时临时关闭过渡。
- 常量与连播：同动效 JS/CSS 共享常量（如 `KeyUsageView` 的 `RING_ANIMATION_MS`）；`constants/ui.ts` 的 `COPY_FEEDBACK_MS`（2000）与 `SEARCH_DEBOUNCE_MS`（300）非过渡档位。公告连播由 `after-leave` 推进。

页面与专用动效：

- 购买页：`MotionTransition` 按 `activeTab` 横向 220ms 翻页（Subscribe 向左、Pay-as-you-go 向右），动画期裁视窗、结束后恢复吸顶与浮层；Stripe 账单与手续费用 `Collapse`（退出留上一帧值）；切充值金额时 `useCountUp` 500ms 过渡，读屏与支付按钮始终用实际金额。
- 兑换成功（`RedeemCelebration`）：2 秒彩纸由动画事件移除，提示留 3 秒按 `fade` 退出，按独立序号触发，礼貌播报辅助技术；减少动画仅显静态提示。
- 用户仪表盘：时长集中在 `components/user/dashboard/dashboardMotion.ts` 并注入 `--dash-*`，含指标 `useCountUp`（四次方缓出、读屏读终值）、趋势图裁剪描线、迷你走势线、热力图按列错峰、模型占比条伸展及区块依次上移淡入（结束后清 `transform`）。
- 其他专用几何动效：`CreativeCanvas` 工具条、`CreativeRunHistory` 侧栏与条目详情、`CustomPageView` 目录抽屉、公告详情层沿用普通时长缓动；通用图标、加载反馈、计时进度与公开页数字滚动各自维护节奏并响应减少动态效果。

<a id="rule_list_editor"></a>
## 行列表编辑器

逐条添加的映射与规则列表使用 `components/common/RuleListEditor.vue`。模型映射使用基于它的 `ModelMappingEditor.vue`，提供商弹窗再封装为 `components/provider/ProviderModelMappingEditor.vue`。

- 展示与事件边界：外壳只管展示，增删排序经 `add`、`remove(index)`、`move(from, to)` 交给父级。添加按钮用带加号的 `btn btn-secondary`（达 `max` 禁用），无独立标题用 `add-placement="footer"`。
- 标题与帮助插槽：头部标题与操作按钮垂直居中、说明另起一行；帮助入口放 `title-suffix` 插槽（如 Key 模型重定向的点击式 `HelpTooltip` 动画演示，关闭卸载、减少动画直显终态）。
- 形态与行操作：空态为 `rounded-control` 虚线框（传 `empty-text` 显示）。`line` 形态用分隔线配 `btn-icon` 对齐 36px 输入；`card` 形态用 `rounded-surface` 浅底卡配 `item-label`（“规则 #n”）与 `btn-icon-sm`。操作图标 `arrowUp`/`arrowDown`/`trash`（首尾及 `<= min` 自动禁用）。
- 稳定 key 与焦点：默认用 `createStableObjectKeyResolver` 按对象身份生成 key；基本类型数组（如协议回退目标）传 `:animated="false"`。点击添加聚焦新行首控件，预设和 JSON 导入不移焦点。
- 删除动效与不可变更新：删除非末行绝对定位补位，删除末行留流淡出防与下方重叠，全退出后显空态。行对象原地编辑保身份，结构变化发新数组；不可变场景（如公告定向）用 `WeakMap` 继承展示 key 且不提交。
- 校验与提交格式：字段错用 `.input-error`/`.input-error-text`，列表汇总错经 `error` 以 `role="alert"` 显示。`utils/modelMappingRules.ts` 的 `KEY_REDIRECT_RULES`/`WILDCARD_ONLY_RULES` 按需传入；后端提交形状不变（提供商经 `buildModelMappingObject`）。

已接入与独立组件范围：

- 密钥、分组与提供商：API Key 模型重定向与复合分组绑定；分组模型映射、模型路由、推理强度映射、协议回退；提供商模型映射、紧凑模型映射、请求头覆写、临时不可调度与 TLS 指纹路由规则。
- 系统设置与定价运维：系统设置的整流器签名/Beta/OpenAI Fast 规则与模型模式、默认与认证来源订阅、提示词替换、搜索服务商、自定义端点、菜单、首页推荐模型、页脚分组与链接、登录协议文档、创作模型、配额通知邮箱；定价页模型定价、提供商统计规则与定价条目、定价卡区间/按次与媒体层级/时间段；EasyPay 自定义支付方式、公告定向 OR/AND 条件、运维告警静默条目、用户属性下拉选项。
- 独立组件场景：标签输入（`ModelTagInput`、`ModelWhitelistSelector`、收件人 chip）与带搜索分页的倍率表各自维护。价格和属性配置的模型规则共用 `ModelTagInput`（回车添加、粘贴批量导入、逐项删除、提交模型数组）；属性配置空规则需补全或删除后保存；价格卡片删除按钮放自身标题栏并隐藏添加说明，外层列表关删除且不留操作列。

## 表格密度

全站表格仅有一套密度：表头 `px-4 py-2 text-xs font-medium tracking-wider`，数据格 `px-4 py-3 text-sm`。`.table`、`TablePageLayout` 深度样式和 `DataTable` 三处保持同步。

- 表体与分页容器：表体和 `Pagination` 共用 `rounded-surface` + `overflow-hidden` 外框，直连无外边距；固定高表格用 flex 分配表体高度且分页不收缩。独立卡片/弹窗嵌入 `UsageTable` 传 `flat` 避免双层边框。
- 分页栏尺寸与状态：分页栏总高 36px（控件 28px，上下留白 4px，局部样式压低 `.input` 基线），浅色底 `gray-50/80`、深色底 `dark-900`；窄屏同高显上/下页与页数；空结果保留空页，范围 0 至 0 且翻页禁用。
- 移动端卡片基线对齐：左 `text-xs` 标签、右内容用 `items-baseline` 对齐首行文字。首元素为图标、头像或色条时，给首行文字加 `self-baseline`（裸文本包 `<span>`，参考 `GroupBadge`、`UsageTable`、`PlatformBadge`、`CapacityBadge`）。
- 移动端基线补充：同行更高文字元素（如带内边距倍率标签）同加 `self-baseline`；开头图形高于文字时（如 32px 头像）给文字加等高行高 `leading-8`（参考 `UsersView` 邮箱列）。
- 合法密度例外：`DataTable` 按列数自动调横向内边距（`px-2/3/4/6`）防宽表溢出；选择列 `.table-selection-cell` 用 `w-11 min-w-11 px-3 text-center` 配 `h-4 w-4` 复选框，外层通用单元格样式排除该列。
- 固定列规则：启用左侧固定列时，选择列随滚动移出，首个数据列到达左缘后固定；右侧操作列按 `stickyActionsColumn` 固定，自定义 `select` 列放首位。
- 列顺序调整：`DataTable` 经 `columnOrderStorageKey`（稳定独立、同组件多表含类型）开启手柄（`h-5 w-4`）拖拽与左右键调序，支持边缘自动滚动。选择与操作列固定，其余列顺序同步应用于表头、单元格、骨架和移动卡并存本地（隐藏列留位、新增列追加，存储不可用仍支持当页调序），手柄/排序/筛选事件互不干扰。

<a id="typography"></a>
## 字体和字号

- 字体栈与自托管：`tailwind.config.js` 的 `fontFamily.sans` 依次为 Plus Jakarta Sans、`"PingFang SC"`、`"Noto Sans SC Variable"` 与系统字体，等宽为 Geist Mono。网页字体自托管，在 `main.ts` 先于 `style.css` 引入，由 `fontTheme.spec.js` 校验。
- 中文与 Emoji 回退：苹方限苹果设备，思源黑体切约 100 个 woff2 按需加载。`postcss.config.js` 的 `adjust-noto-sans-sc-font-face` 将思源黑体字重上限改为 600（对齐苹方 Semibold）并剔除彩色 emoji 码位。
- 禁加 Emoji 字体：字体栈不放 `"Apple Color Emoji"`，避免 Jakarta 未加载时 Chrome 用其渲染数字和空格导致变宽。
- 字号层级与下限：全站最小字号 `text-xs`（12px，含徽章与辅助数字），门禁拦截 `text-[9px]`、`text-[10px]`、`text-[11px]`。正文与表格用 `text-sm`，说明文字用 `text-xs`，页面标题用 `.page-title`（`text-2xl font-bold`），`h1` 不单独设字号。

## 合法例外

- 营销页和落地页（`HomeView`、`KeyUsageView` 等公开页）的 hero 标题允许使用展示级字号。
- `onboarding.css` 覆盖 driver.js 第三方样式时允许使用 `!important`。
- `src/i18n/**` 导览文案 HTML 的内联样式属内容字符串，不参与 token 校验。
- 测试文件里的负断言加 `check-ui-allow` 注释豁免。
- 保留手写外壳的弹窗：`BackupSettings` 的 R2Guide、`SubscriptionsView` 指南弹窗（`max-w-2xl`）、`AnnouncementPopup` 与 `AnnouncementBell`（复用 `.modal-overlay`/`.modal-content` 与 `z-announcement` 系列）、`ProviderTestModal` 图片灯箱（强遮罩）、`GlobalSearchPalette`（无标题栏、顶定位、手机全屏，仍用 `useDialogLifecycle`）。
- `RiskControlView` 搜索框的 `pl-9` 图标留白局部保留。
- `ProvidersView` 跟随鼠标的操作菜单不走 `getFloatingPanelPosition`。
- `textarea` 自适应高度、`GroupBadge` 方角、`OpsDashboard` 250ms 路由同步防抖、`CreativeCanvas` 工具条与 `CreativeRunHistory` 条目详情展开动画保持局部写法。

## 校验

在 `frontend/` 运行 `npm run check:ui`（`scripts/check-ui-tokens.mjs`），`lint:check`、推送钩子和 CI 均自动执行。规则覆盖旧圆角、任意值圆角/字号/z 值/max-h 像素、36px 冗余、手写开关、非响应式暗色判断、z-index/JS 断点字面量及 `tailwind.config` 键约束。

豁免标记 `check-ui-allow` 规则：

- 必须写在真实注释中并附英文理由，字符串中的标记无效；父元素豁免不继承给子元素。
- HTML 注释放起始标签后绑定该标签，放同级元素间优先绑定后一个元素。
- JS 语句间块注释优先绑定后一句；豁免前一句使用行尾 `// check-ui-allow: ...`，避免格式化移位改变作用对象。
- CSS 豁免放目标声明行尾或紧前方。新增回归测试须断言具体被报语句或元素，不能只核对违规总数。

## 相关文档

- [系统架构](system_architecture.md)：前端在整体部署中的位置和静态资源的交付方式。
- [开发、验证与上游同步](../operations/development_workflow.md)：前端工具链和验证分层。
