# 前端 UI 规范

> 上级目录：[架构文档目录](index.md)

本文记录前端设计 token 和组件样式的约定：圆角层级、间距网格、控件尺寸、菜单与浮层、弹窗与设置表单、层级、断点、加载反馈、图标与动画时长、行列表编辑器、表格密度、深色配色、图表主题、字体栈和字号下限。适用范围是 `frontend/tailwind.config.js`、`frontend/src/style.css` 和全部 Vue 组件；浅色配色主题和业务组件内部的局部布局由各组件自行决定。修改前端组件、样式或这两个文件前，先读本文。

## 章节导航

- [圆角层级](#圆角层级)、[间距约定](#间距约定)、[控件尺寸](#控件尺寸)、[开关](#开关)：调整基础组件时读取。
- [菜单与浮层](#菜单与浮层)、[层级 z-index](#层级-z-index)、[弹窗](#弹窗)：调整浮层和遮罩时读取。
- [设置表单](#settings_form)：新增或调整分页设置弹窗、整页设置、设置项行和批量编辑项时读取。
- [通用图标](#通用图标)：选择图标、调整悬停动画、迁移内联 SVG 时读取。
- [断点](#断点)、[加载反馈](#loading_feedback)、[动画与时长](#动画与时长)、[表格密度](#表格密度)：调整响应式布局和交互时读取。
- [行列表编辑器](#rule_list_editor)：新增或修改逐条添加的映射、规则列表时读取。
- [深色配色](#dark_colors)、[图表主题](#图表主题)、[字体和字号](#typography)：调整颜色、字体和文字时读取。
- [合法例外](#合法例外)、[校验](#校验)：确认局部例外和验证命令时读取。

## 圆角层级

圆角使用下表的 token。数值写在 `style.css` `:root` 的 `--radius-*` 变量里，`tailwind.config.js` 的 `borderRadius` 只通过 var() 引用这些变量：

| token | 值 | 用途 |
|---|---|---|
| `rounded-compact` | 6px | 徽章、chip、tab 项、骨架屏、行内代码、小图标块 |
| `rounded-control` | 8px | 按钮、输入框、下拉框、侧栏链接、浮层面板 |
| `rounded-surface` | 12px | 卡片、表格容器、toast、代码块 |
| `rounded-dialog` | 16px | 桌面端弹窗（移动端弹窗用 surface） |
| `rounded-full` / `rounded-none` | 无 | 胶囊、进度条、开关；需要直角时覆盖 |

系统设置页的吸顶导航是胶囊分段样式：外壳、一级标签和网关二级标签都用 `rounded-full`。选中态由淡品牌青底、边框和图标文字着色组成。网关页多出一行二级标签时，外壳改用 `rounded-dialog`。外壳是不透明底色加一层下投影，深色模式用比卡片亮一档的 `dark-500` 边线。外壳放在 `.settings-tabs-sticky` 容器里，容器吸顶在顶栏下方 1rem，用页面底色铺底，并用 box-shadow 向上铺满到顶栏，盖住页签上方缝隙和圆角两侧的滚动内容。

旧尺度名（`rounded-sm/md/lg/xl/2xl/3xl`）、裸 `rounded` 和 `rounded-[...]` 任意值会被门禁拦截；这些旧 key 已经从配置里删除，写了也不会生成样式。裸 CSS 里的 `border-radius` 只能写 `var(--radius-*)`、`0` 或 `9999px`。

例外有两种。第一种是边长不超过 16px 的微型装饰元素（例如用量热力图 12px 的格子）。最小档 compact 的 6px 已经是这类元素边长的一半，看起来接近椭圆，所以允许用组件级的局部变量设更小的半径（例如 `.heatmap-cell` 的 `--radius-cell: 4px`）。第二种是创作台输入框 `CreativeComposer` 的外壳，它的 `--composer-radius` 取 `dialog` 和 `control` 之和（24px），让悬浮在画布底部的输入框更圆润。全局档位保持上表的几档。

<a id="layout_spacing"></a>
## 间距约定

- 所有间距都落在 Tailwind 的 4px 网格上。`mt-[2px]`、`padding-left: 17px` 这类任意值会被门禁拦截。
- 页面布局有两档间距。独立大卡片、统计卡、图表卡、移动端数据卡之间，以及工具栏、控件组和卡片之间，用 16px（`gap-4` / `space-y-4`）；工具栏和控件组内部用 8px（`gap-2` / `space-y-2`）。横向网格和纵向堆叠用同样的档位，加载骨架和实际内容的间距一致。`TablePageLayout` 里相邻的工具组之间 8px，最后一组工具到表格 16px。API 密钥页的端点条例外，它到表格也是 8px，和它到上方工具栏的间距相同。移动端数据卡和分页器之间也是 16px。`RuleListEditor` 的标题操作区到卡片列表 16px，卡片行之间 16px，线形行和它的标题操作区之间 8px。
- 列表的搜索、筛选、刷新和批量操作工具栏都用 `gap-2`（8px），包括工具栏外层、左侧筛选组和右侧操作组；共用组件和加载骨架也用这个间距。窄屏换行时行间距同样是 8px。筛选弹层里带标签的字段按表单间距排列；分页摘要、正文信息组和卡片区块各用自己的间距。
- 页面或卡片的标题带说明文字时，同一行的操作区和整块标题说明的底部对齐。`AppLayout` 页头用 `items-end`，自绘页头和带说明的卡片标题行也这样对齐。页头的补充信息放在可选的 `page-heading-meta` 插槽，位于说明下方 8px，参与同一个底部对齐，例如用户仪表盘的实时状态条。先纵向堆叠、宽屏再横排的布局，只在横排断点开启底部对齐，窄屏的控件正常换行。
- 卡片 padding 有两档：独立卡片 `p-6`，嵌套面板、网格卡和统计卡 `p-4`。
- header 和 main 的水平 padding 完全一致，都是 `px-4 md:px-6 lg:px-8`，两侧边缘在所有断点上对齐。
- 默认首页和控制台共用 `AppHeader`，品牌、工具按钮、余额和用户菜单都在这里。首页通过 `public-page` 隐藏侧栏开关，保留模型广场和访客登录入口，并为固定顶栏预留高度；自定义 HTML 或 iframe 首页使用全页模式。操作台的返回仪表盘图标在品牌右侧。
- 布局尺寸 token 在 `style.css` 的 `:root` 里定义，各有一份：`--header-h`（3.5rem，顶栏高度）、`--sidebar-w`（14rem，侧栏展开宽度）、`--sidebar-w-collapsed`（4.5rem，侧栏折叠宽度）。顶栏高度、主区 `padding-top`、侧栏遮罩 `top`、侧栏宽度和主区 `lg:ml-*` 偏移都引用这些变量（例如 `h-[var(--header-h)]`），`h-14`、`top-14`、`w-56` 这类字面量会和变量脱节。吸顶偏移和锚点 `scroll-margin-top` 写成 `calc(var(--header-h) + 余量)`（参考 SettingsView 的 tabs 吸顶），并在注释里写明余量由什么组成。
- 垂直空间由 AppLayout 的 flex 链分配：wrapper（`flex-col`，普通模式 `min-h-screen`，锁定模式 `h-full min-h-0`）→ `.app-main`（`flex-1 flex-col`）→ 页头（自然高度）+ 页面内容。需要撑满剩余高度的页面容器（例如 `TablePageLayout`、`CustomPageView` 的根元素）自己加 `flex-1 min-h-0`。手写 `calc(100vh - …)` 视口差值和用负 margin 抵消父级内边距都会破坏这条链；`--main-pad-*`、`--page-heading-space` 这类和布局重复的尺寸变量也不要引入。
- 系统设置页的内容容器用 `w-full min-w-0` 填满主区，各页签的卡片同宽。容器不设居中外边距和最大宽度，否则在纵向 flex 布局里会按内容收缩。
- 全屏工作区（`full-viewport`）模式下 `.app-main` 没有内边距，页面自然满幅。宽屏锁定（`fit-viewport`）模式只在 `lg` 及以上把外壳锁定为视口高度，页头和标准内边距照常保留，页面根容器用 `lg:flex-1 lg:min-h-0` 接住剩余高度，由卡片内部区域滚动（参考兑换页的历史列表和分页器）；窄屏随内容自然滚动。
- 自定义页面用 `fit-viewport="all"`，在所有屏幕尺寸下按动态视口分配高度，页头和标准内边距照常保留。iframe 和 Markdown 正文在卡片内滚动。只设 `min-h-screen` 时，后代的 `height: 100%` 拿不到明确的高度，iframe 会退回默认的 150px；需要百分比高度的页面要接入完整的高度链。
- 表单内的 `space-y-2/3/4/6` 按上下文选择，没有统一档位。

## 控件尺寸

- 按钮、输入框和下拉触发器共用 36px 基线（`.btn` 和 `.input` 都是 `min-h-9`），分页器控件也是 36px，表格页脚的分页尺寸和其他地方一样。在基线之上再写 `h-9` 是冗余写法，门禁会拦截；紧凑档需要 36px 时，用 `btn-sm/md/lg + h-9` 提到这一档。
- 主操作用 `.btn-primary`；普通的编辑、查询、筛选和链接操作用 `primary-*` 品牌色。状态提示、业务分类和第三方品牌使用各自的配色。radio 和 range 通过全局 `accent-color` 使用 `primary-600`。checkbox 是原生 input，勾选、半选、禁用和键盘行为都由浏览器提供，外观由 `style.css` 统一设置：浅色填充 `primary-700`，深色填充 `primary-600`，白色勾选标记复用通用的 check 路径，描边 1.75。选中和取消时用 `--motion-fast` 做缩放和透明度过渡，半选显示一条横线；系统强制配色时显示浏览器的原生外观。
- 原生按钮、`role="button"` 和 `.btn` 的文字不可选中，按钮内的图片不可拖拽；正文、表格数据和输入内容可以选中复制。
- 图标按钮有两档：`.btn-icon`（h-9 w-9）和 `.btn-icon-sm`（h-8 w-8），自带 `rounded-control` 和居中布局，调用处只需补 hover 和颜色类。`.btn-sm` 用在表格行内等紧凑场景。
- 需要选择框时使用 `frontend/src/components/common/Select.vue`。原生 `<select>` 的面板样式、深色配色和键盘交互与项目组件对不上；`check:ui` 检查不到它，评审时需要人工确认。
- 下拉触发器（Select、DateRangePicker、DateTimePicker）在模板里组合 `input input-trigger` 和各自的状态类，基线样式来自这两个类。
- 分段切换（两到五个互斥选项，例如指标、时间范围、数据来源）用 `style.css` 的 `.segmented` 轨道、`.segmented-item` 选项和 `.segmented-item-active` 选中态。轨道加 `v-segmented`（`directives/segmented.ts`），所有选项共用一个选中背景；内边距、字号和高度由调用方用工具类补充；放进 36px 工具栏时，给轨道加 `h-9 items-stretch`。选中项保留 1px 描边，因为浅色模式下只靠阴影和白底看不清选中项的轮廓。选项和选中背景的圆角取 `--segmented-item-radius`（`control` 减去 1px 边框和 2px 内边距），和轨道外缘同心；调整轨道的边框或内边距时，同步修改这个值。页面级的大页签用 `.tabs`。
- 输入框的图标和字符前后缀使用 `style.css` 里的 `input-icon-*` 机制：容器 `input-icon-wrap`，图标位 `input-icon` 或 `input-icon-right`（可点击的内容再加 `input-icon-action`），输入框按图标所在的一侧加 `input-has-icon` 或 `input-has-icon-right`。文本留白由变量推算：`留白 = inset + slot`。档位：默认（inset 0.75rem，留白 2.5rem）、`input-icon-lg`（登录注册表单，inset 0.875rem，留白 2.75rem）、`input-icon-text`（`$` 等窄字符前缀，留白 2rem）；紧凑搜索框内联 `--input-icon-slot:1.5rem`（留白 2.25rem）。
- 价格管理和属性管理的页签栏和下方工具栏之间 16px。搜索框使用同样的图标布局，`sm` 及以上固定 `w-64`，更窄时随工具栏剩余宽度伸缩，占位提示写明可以搜索配置名称或模型名称。工具栏相邻控件之间 `gap-2`；配置页的状态筛选框用 `w-32 shrink-0`，默认目录页的两个筛选条件收进 `FilterDropdown`（规则见[菜单与浮层](#菜单与浮层)）。两页的默认目录信息共用 `ModelCatalogInfo`，用辅助字号展示来源、短版本号和更新时间，窄屏自动换行。

## 开关

全站的开关只有 `components/common/Toggle.vue` 一个实现。手写的轨道和滑块（`h-6 w-11`、`h-5 w-9` 组合）会被门禁拦截。

- 几何尺寸来自 Toggle scoped 样式里的 CSS 变量（`--toggle-track-w/h`、`--toggle-thumb`、`--toggle-inset`），开启时的位移由 `calc(轨道宽 − 滑块 − 2×边距)` 算出，调整档位时只改变量。
- 档位：`size="md"`（44×24）和 `size="sm"`（36×20）；`variant="inset"`（滑块内嵌，默认）和 `variant="flush"`（大滑块贴边）。
- 配色：开启默认 `toggle-active`（等于 `bg-primary-600`）。关闭时用 `off-tone` 选择：`default`（gray-300）或 `soft`（gray-200）。个别位置需要亮色开态（`bg-primary-500`）或特殊的 hover 配色时，用 `on-class` 和 `off-class` 传入整串类名，档位保持现有几种。
- 异步保存的场景用受控写法：`:model-value` 加 `@update:model-value`，值由处理函数写回（参考 ProvidersView 的可调度开关）。

## 菜单与浮层

- 菜单和搜索建议列表用 `.dropdown` 容器样式，圆角、边框、阴影和深色背景都包含在内。默认绝对定位，上下留白 `py-1`；需要固定定位时补 `fixed`，内容自带留白时补 `py-0`。定位偏移、尺寸和箭头由调用处维护，展开和收起使用公共动效。
- 深底的信息提示（`HelpTooltip`、表格悬停明细、状态说明等）用 `.tooltip-panel`：浅色模式是 `gray-900` 深底白字；深色模式是 `dark-900` 底色、`dark-100` 文字和 `dark-600` 描边。箭头用 `.tooltip-caret` 旋转方块，一半嵌进面板边缘；调用处补定位和朝外的两条边（向下 `border-b border-r`，向上 `border-l border-t`，向左 `border-b border-l`，向右 `border-r border-t`）。手写的 `dark:bg-gray-*` 面板和边框三角箭头，在深色配色调整时容易漏改，统一用这两个类。浮层内的分隔线在深色模式下用 `dark-600`。
- 菜单项有两档：`.dropdown-item`（px-4）和紧凑档 `.dropdown-item-sm`（px-3）。配色、hover 和过渡都已包含，调用处只补 `gap-*`、`rounded-control` 这类布局类。基础配色是中性色。顶栏用户菜单、联系客服、语言切换这类导航型浮层，用内缩菜单样式：面板 `.dropdown py-0`，内容按 `.menu-section` 分区（`p-1.5` 留白，相邻分区之间自动加分隔线），分区标题 `.menu-heading`，菜单项 `.menu-item` 内缩并带 `rounded-control` 的悬停底色，悬停配色和 `.sidebar-link` 一致，图标继承文字颜色；危险操作再加 `.menu-item-danger`。顶栏工具区的图标按钮用全局的 `.header-status-icon-button`。
- 列表工具栏的漏斗筛选用 `FilterDropdown`，每个字段用 `FilterField` 包裹；按钮、面板和点击外部关闭的逻辑都由组件提供。
  - 面板外观和 `DateRangePicker` 一致：`rounded-surface`、淡描边和柔和阴影。头部、已选条件和字段区之间靠留白分层，中间没有分割线。
  - 头部是标题、条件数和「重置」，没有生效的条件时「重置」置灰。有生效条件时，头部下方列出已选条件标签（字段名、当前取值和 ×），顺序和字段一致，点 × 只移除这一项。再往下是可滚动的字段栅格。
  - `FilterField` 里的 `Select` 会自动上报当前选项：第一项的值为空或 `'all'` 时视为「全部」，其他情况按空值判断；默认值不是第一项时（例如运维看板时间范围的 `1h`），传 `empty-value`。文本输入、远程搜索这类非 `Select` 字段，由调用方传 `value-text` 并监听 `clear`。
  - 字段标签用中性灰 `text-xs`，和表单的 `.input-label` 区分开；占满一整行的字段传 `full`。
  - `columns` 取 1、2、3，对应面板宽 18、34、48rem，窄屏都退回单列：两三个条件用 1，四个左右用 2，更多用 3。
  - 面板默认左边缘对齐触发按钮，右侧放不下时向左平移，水平方向的夹取复用 `getFloatingPanelPosition`。有生效条件时，按钮换成品牌色描边并显示数量角标。
  - 面板里有输入框状态，或者测试需要直接访问字段时，传 `keep-mounted`；运维看板这类自定义按钮样式用 `trigger-class` 覆盖。
- 日期范围用 `DateRangePicker`：左侧是分组的快捷范围，右侧是自绘的单月日历，原生 `type="date"` 输入不再使用。在日历上点两次确定起止日期，反向点选时自动对调。起止日期和主按钮同色，中间的日期用淡品牌青色带连起来，今天用小圆点标出。最晚可以选到明天，用来兼容时区差异。没点应用就取消、点外部或按 Esc 关闭时，改动全部丢弃，触发器只显示已经生效的范围。弹层由 `getFloatingPanelPosition` 定位：触发器在视口右半边时右对齐，在左半边时左对齐。
- 单个日期时间（例如公告的开始和结束时间）用 `DateTimePicker`。值的格式和 `datetime-local` 相同（`YYYY-MM-DDTHH:mm`），空字符串表示未设置。原生 `type="datetime-local"` 的面板跟随浏览器和系统主题，深色模式下和项目控件的配色对不上。弹层左侧是和 `DateRangePicker` 同款的单月日历，可以选未来的日期。右侧是时、分两列滚动列表，列高等于日历网格。`placeholder` 写空值的含义，例如“立即生效”。有值时，触发器右侧显示 ×，点一下清空。传入 `min` 后，早于这个日期的日子置灰。点确定才写回，点取消、点外部或按 Esc 会丢弃草稿。弹层打开时按 Esc 只关闭弹层，外层弹窗保持打开。点“此刻”直接写入当前时间。传入 `presets` 后，弹层左侧多出一列快捷选项，点击后直接写回。公告结束时间的快捷选项是 1、3、7、30 天，从开始时间起算，没填开始时间时从现在起算。定位规则和 `DateRangePicker` 相同。
- 表格行内的操作菜单（4 个 `*ActionMenu`）的浮层容器用 `.action-menu` 类（fixed 定位、层级和面板样式），宽度类（w-48/w-52）和 `action-menu-content` 钩子类写在调用处。
- 创作台画布上的浮层（顶部工具条、设置、历史、输入框、空画布引导的胶囊）用 `.canvas-island`：85% 不透明的白底或 `dark-900` 底、淡描边、背景模糊和一档柔和阴影，深色模式减弱阴影。浮层里的 32px 图标按钮用 `.canvas-tool-btn`，选中态加 `.canvas-tool-btn-active`，按钮组之间用 `.canvas-tool-divider`。展开的菜单和弹层用实底的 `.dropdown` 或同等样式。
- 遮罩透明度有两档，都来自 CSS 变量：浅色模式在 `:root` 定义常规遮罩 `--overlay-bg`（black/50）和媒体灯箱等使用的强遮罩 `--overlay-bg-strong`（black/70）；深色模式在 `html.dark` 中整体加深为 black/70 和 black/85。模板写 `bg-[var(--overlay-bg)]`，`bg-black/50` 这类字面值会被门禁拦截。
- 浮层面板的最大高度有三档，来自 `:root` 的 `--max-h-menu-sm`（15rem）、`--max-h-menu`（20rem）、`--max-h-panel`（26.25rem），模板里对应 `max-h-menu-sm/menu/panel`。像素任意值 `max-h-[Npx]` 会被门禁拦截，局部特例（例如告警表的 520px）加 `check-ui-allow` 并写明原因。vh、dvh、calc 等相对视口的值含义不同，不归入这三档，门禁也不拦截。
- 挂载到 body 的浮层，定位只有一个实现：`utils/floatingPanel.ts` 的 `getFloatingPanelPosition`。翻转、对齐、夹取和窄屏行为都通过 options 控制：固定高度的菜单用 `fixedHeight`，左对齐面板用 `align: 'left'`，菜单类传 `pinLeftOnMobile: false`。组件里自己计算 rect 和 spaceBelow 做翻转，会和这个实现产生分歧。JS 侧的面板尺寸常量在 `constants/overlay.ts`（`SELECT_PANEL_MAX_HEIGHT`、`MIN_COMFORTABLE_PANEL_HEIGHT`），和样式档位取值一致。

## 断点

- 断点数值定义在 `constants/layout.ts`：`BREAKPOINT_SM/MD/LG`（640/768/1024，与 Tailwind 默认 screens 相同；`tailwind.config.js` 使用默认 screens）、`MEDIA_MIN_*` 和 `MEDIA_MAX_*` 媒体查询字符串，以及 `TABLE_DESKTOP_MEDIA_QUERY`（lg 的别名，表格和分页器共用这个切换点，Ops 的五个表格组件也用它）。接口测试 `breakpointTheme.spec.ts` 保证这些值和 Tailwind 一致。
- max 变体的约定是 `max = min - 1px`（和 Tailwind 的 max-* 含义相同），min 和 max 区间互不重叠，中间没有 1px 的重叠带。JS 里写 `MEDIA_MAX_SM`，`<= 640` 这样的比较会和 CSS 断点错开一个像素。
- JS 里的断点判断（useMediaQuery、matchMedia、innerWidth 比较）都引用这些常量，字面量会被门禁拦截。CSS 的 @media 不支持 var()，需要例外的位置（例如 CustomPageView 目录抽屉的 639px）写字面量，加注释说明数值的由来，并由接口测试锁定。

## 层级 z-index

层级的数值在三处保持一致：`style.css` `:root` 的 `--z-*` 变量存放数值；`tailwind.config.js` 的 `zIndex` 扩展只通过 var() 引用（模板里用 `z-modal`、`z-toast` 这类命名工具类）；`constants/overlay.ts` 的 `Z_INDEX` 常量供 JS 内联使用。接口测试 `src/__tests__/zIndexTheme.spec.ts` 检查三处一致；任意值 `z-[...]` 和数字字面量会被门禁拦截。下表只给现有层级命名，数值就是当前的取值：

| 值 | token | 用途 |
|---|---|---|
| 30 | `chart-tooltip` / `sidebar-overlay` | 图表 tooltip（有意低于导航）/ 侧栏遮罩 |
| 40 | `sidebar` | 侧栏 |
| 50 | `header` / `modal` | 顶栏 / 弹窗遮罩（同一层由 DOM 顺序和 teleport 决定先后） |
| 60 | `modal-nested` | 嵌套弹窗、筛选面板 |
| 100 | `tooltip` / `announcement` | 弹窗内的 tooltip、下拉面板、灯箱 / 公告底层 |
| 120 / 140 | `announcement-raised` / `announcement-top` | 公告层级（有意递增） |
| 9998 | `menu-overlay` | ActionMenu 点击捕获层 |
| 9999 | `toast` / `action-menu` / `teleport-tooltip` | 通知 / 行内操作菜单 / teleport 出去的提示浮层 |
| 99999 | `help-tooltip` | HelpTooltip（teleport） |
| 100000000 | `tour` | driver.js 引导层，数值由第三方决定，这里登记数值，没有对应的工具类；`onboarding.css` 保留字面值和 !important |
| 100000020 | `teleport-dropdown` | Select 等 teleport 出去的下拉（需要压过引导层） |

局部的堆叠上下文不进入这张表，也不加全局 token：DataTable 内部（0/20/200/210/220）、CreativeCanvas 画布内部、CustomPageView 目录抽屉、弹窗内的 sticky 表头（z-[1]）都加 `check-ui-allow` 豁免。表格内的局部 dropdown 用普通的 `z-50` 即可，它处在局部上下文里，不占用命名层级。

## 弹窗

- 默认使用 `BaseDialog`：宽度档位有 narrow、normal、wide、extra-wide、full；Escape 关闭、点击外部关闭、焦点管理和背景滚动锁定都已内置。新弹窗直接用它，手写 `fixed inset-0` 外壳会缺少这些行为。
- 标题需要说明或图标时，用 `subtitle` 在标题下加一行 `text-xs` 说明，用 `header-icon` 插槽在标题左侧放图标块。标题右侧的模式切换等控件放进 `header-actions` 插槽，排在关闭按钮之前。头部结构由这些插槽组成。
- 贴边分栏的工作区弹窗：传 `flush` 去掉内容区内边距，再配合 `bodyScroll=false`，由各栏自己滚动。侧栏用浅底（浅色 `gray-50/70`，深色 `dark-950`）和单侧分隔线贴住弹窗边缘，外面不再包卡片。底部操作区放在内容里，用同样的浅底和 `rounded-b-surface sm:rounded-b-dialog`，这样不会盖住弹窗的圆角。窄屏下分栏改成上下堆叠、由外层整体滚动时，各栏按内容撑高，`min-h-0 flex-1` 只在分栏断点（`md:`）生效；否则栏会被压缩，内容溢出后底部留白消失，内容贴住底栏。提供商连接测试弹窗是这种布局的参考实现。
- 提供商测试共用标题、侧栏宽度、三列指标和结果面板。`ProviderTestResultView` 管理复制、日志和外层滚动，决策测试的空态与答案由 `SystemOneTestAnswers` 按问题类型展示。测试类型决定输入字段和指标内容；决策输入的格式选择放在字段标签旁，问题标识及补充条件折叠显示。批量测试详情使用同一个结果组件。
- 分页表单可以设置 `BaseDialog` 的 `bodyScroll=false`，由表单内部管理滚动，标题、页签和底部操作区始终可见。默认由弹窗内容区滚动。分组的创建和编辑（`GroupSettingsForm`）、提供商的创建、编辑和批量编辑，都按[设置表单](#settings_form)的约定分页。
- 安全凭证流程（TOTP 设置、禁用、登录验证、提权）使用 `AuthCardDialog`：居中的图标头，右上角没有关闭按钮，整张卡片 p-6，是和 BaseDialog 并存的另一种风格。它不 teleport，在原位置渲染，嵌套时的层级由 `z-index` prop 决定。
- 手写弹窗的处理方式：结构是"标题头、内容、按钮行"的，迁移到 BaseDialog；有特殊视觉结构的继续手写，并登记在下面的[合法例外](#合法例外)里。

<a id="settings_form"></a>
## 设置表单

设置项较多的弹窗和整页设置，用 `components/common/settings/` 下的组件搭建分区和开关行。

- 分页：`SettingsTabs` 接收 `tabs`（`key`、`label`、`hidden`），并按 `key` 提供同名插槽。所有面板一直挂载，切页时编辑器内部的草稿不会丢失；`hidden` 只隐藏页签按钮，当前页签被隐藏时回到第一个可见页签。切页后滚动到顶部，支持方向键和 Home、End。表单加 `novalidate`，提交时先调用 `validate()`，组件会切到无效字段所在的页签，再显示浏览器的原生校验提示。业务校验用 toast 提示，同时调用 `revealField()` 定位字段（提供商弹窗用 `data-provider-field` 标记）。新手引导的 `onboarding-reveal` 也走这个定位流程。
- 分区：`SettingsSection` 提供 `text-sm font-semibold` 的标题、`.input-hint` 说明和 `actions` 插槽。相邻分区之间自动加 `border-t pt-6`，页内分区间距 24px，分区内 16px。分区标题用这个组件的标题，`.input-label` 留给字段标签；分隔线由组件自动添加。
- 设置行：布尔项用 `SettingToggleRow`（左侧是标题、说明和可选的 `HelpTooltip`，右侧是 `Toggle size="md"` 默认的 inset 变体）。右侧是选择框或输入框时用 `SettingRow` 并传 `field`，控件宽度固定 `sm:w-56`，窄屏改成上下排列。
- 依赖字段：开关打开后才需要的字段，放进用 `Collapse` 包裹的 `SettingsSubpanel`（`rounded-surface`、淡边框、浅底、`p-4`），展开时有过渡动画。
- 选择与提示：两到五个互斥选项用 `SettingsSegmented`（基于 `.segmented` 和 `v-segmented`），无障碍名称通过 `:ariaLabel` 传入，组件输出 `aria-label`；带图标和说明的类型选择用卡片，选中时显示品牌色描边和浅底。说明、风险提示和错误用 `SettingsNotice` 的 `info`、`warning`、`error` 三种语气，颜色由组件决定。字段说明用 `.input-hint`。
- 整页设置：系统设置页这类整页表单，每组设置放在 `SettingsCard` 里。卡片头部是 `text-lg` 标题和说明，内容区 `p-6`，`SettingsSection` 之间相隔 24px。卡片里不放保存按钮。刷新、测试连接这类工具按钮和控制整张卡片的总开关放在 `actions` 插槽，显示在标题右侧。设置行下方需要提醒权限或风险时，在 `SettingToggleRow` 的 `hint` 插槽里放 `SettingsNotice`。
- 字段布局：开关、选择框和短数字用 `SettingRow`，标题在左，控件在右。设置行没有说明文字时，标题和控件垂直居中。URL、密钥、长文本和多行文本用上下布局：`.input-label` 在上，输入框占满宽度，`.input-hint` 在输入框下方。两个以上这样的字段可以放进 `md:grid-cols-2` 网格。一组同类的短字段（各平台的阈值、调度权重、和提供商弹窗对应的默认值）也用上下布局的网格并排。
- 多语言字段：面向用户的运营文案用 `LocalizedEditor`（传 `label`）或 `LocalizedFieldsEditor`，表单里和普通输入框一样只编辑原文。翻译入口是标签行右侧的 `text-xs` 小链接，显示已有语言；有译文过期或需要选择原文语言时变成琥珀色。翻译弹窗是贴边分栏的工作区弹窗，左栏是语言列表，右栏编辑选中的语言；它用 `modal-nested` 层级，可以叠在公告、套餐等弹窗上。占位文字、等宽字体和栅格布局分别通过 `placeholder`、`inputClass` 和 `layout` 传入。
- 标签输入：逐个录入的字符串列表（邮箱后缀白名单、转发 IP 请求头）用 `SettingsTagInput`。组件负责展示标签和转发输入事件，分隔、去重和规范化由调用方处理。
- 整页保存：页面里所有设置都通过 `SettingsSaveBar` 保存。有未保存的修改时，视口底部居中浮出一条深色浮条（浅色模式 `gray-900` 底，深色模式 `dark-900` 底加 `dark-500` 边线），上面是提示文字和“放弃”“保存设置”两个标准尺寸按钮。浮条用 `rounded-dialog`（16px）和 8px 内边距，里面 `rounded-control`（8px）的按钮与外框弧度同心。每块设置用 `useDirtyTracker` 记录加载或保存后的快照，数据源各自加载完成后调用 `markClean(key)`。快照里去掉只读的展示数据，例如联网搜索的已用额度。页面用 `provideSettingsSaveRegistry` 创建登记表，本页的表单和子组件（子组件调用 `useSettingsSaveTarget`）把修改状态和返回是否成功的保存函数登记进去。每块登记时同时提供 `discard`，从服务器重新加载这块设置。点保存时按登记顺序只提交有修改的几块，某一块失败时其余几块照常保存，全部成功后提示一次。点放弃时，有修改的几块各自重新加载。
- 批量编辑：每个可以修改的项用 `BulkApplyField` 包裹，左侧的复选框（`${id}-enabled`）决定是否提交。未勾选时内容区加 `inert` 并置灰，键盘也进不去。布尔值放在 `control` 插槽的开关里，界面上只有这一个开关，左侧复选框负责是否提交。

<a id="dark_colors"></a>
## 深色配色

深色模式下的文字和表面色阶定义在 `tailwind.config.js` 的 `colors.dark`，边框另用同一文件的 `darkEdges`。按组件的角色选择 token，现有的 `dark-*` 档位名称保持不变：

| 档位 | 值 | 角色 |
|---|---|---|
| `dark-50` | `#FAFAFA` | 标题和强调文字 |
| `dark-100` | `#DEE0E2` | 正文文字、侧栏导航和顶栏图标的默认色 |
| `dark-200` | `#D4D4D8` | 次强文字、占位文字底色 |
| `dark-300` | `#A1A1AA` | 次要文字 |
| `dark-400` | `#8B8B94` | 辅助文字、表头文字 |
| `dark-500` | `#5F5F67` | 图标、禁用文字 |
| `dark-600` | `#3D3D42` | 较强的中性填充 |
| `dark-700` | `#27272A` | 中性填充：chip、禁用控件 |
| `dark-800` | `#17171A` | 弱填充：表格行 hover、嵌套面板 |
| `dark-900` | `#0F0F10` | 卡片、弹窗、下拉面板 |
| `dark-950` | `#141416` | 控件底：输入框、次级按钮、Tab 轨道、行内代码 |

页面底色引用 `--page-bg`：浅色 `#FCFCFE`，深色在 `html.dark` 里覆盖为 `#0A0A0B`。`html`、`body`、主题外壳和背景层都用这个纯色背景，深色页面的滚动条轨道也引用它；页面背景没有渐变。

深色模式下，侧栏和顶栏同样使用不透明的 `--page-bg`，和页面同色。控制台、首页和公开模型广场的顶栏共用 `.site-header`；深色模式关闭顶栏的背景模糊，滚动内容和移动端侧栏遮罩都不会改变它的底色。浅色模式下，顶栏是 80% 透明度的白色并带背景模糊，侧栏是白色。

深色模式的层次靠接近底色的表面和低透明度的边线区分。`borderColor.dark`、`divideColor.dark`、`ringColor.dark` 共用 `darkEdges`，和 `colors.dark` 的实色分开，调整边框时文字和背景不受影响。

| 边线档位 | 灰白 `#FCFCFE` 的透明度 | 用途 |
|---|---|---|
| `dark-400` | 30% | 控件焦点 |
| `dark-500` | 12.5% | 控件 hover、较强的边线 |
| `dark-600` | 7.8% | 卡片、输入框、弹层的默认边框 |
| `dark-700` | 4% | 表格内的分隔线 |
| `dark-800` | 3.1% | 顶栏、侧栏和页面之间的分割线，以及其他弱分隔线 |
| `dark-900` | 2% | 最弱的边线 |

边线的透明度会和 `/70` 这类修饰符相乘，例如 `dark:border-dark-600/70` 的最终透明度是 7.8% × 70% = 5.46%。裸 CSS 用 `theme('borderColor.dark.600')` 等写法引用同一来源。深色模式下的中性边框都用 `dark-*` 边线档位，`gray`、`slate` 的实色描边在深色下过亮。

顶栏底边和侧栏外侧边共用 `dark-800` 弱分割线，由 `.site-header` 和 `.sidebar` 的深色样式维护。控件和卡片用 `dark-600` 默认边框，可操作区域的轮廓因此比页面分区更清楚。

表头和卡片同为 `dark-900`，表格行 hover 用 `dark-800`。控件底 `dark-950` 比卡片略亮。

导航、分段控件和列表选中行使用 `primary-500/8` 的淡品牌青底和 `primary-500` 文字，带边框的选中控件用 `primary-500/15`。侧栏和 Select 未选中项的 hover 用 `dark-800` 弱填充和品牌青文字；已选中项在 hover 或获得键盘焦点时保持选中底色。主操作、链接、开关和图表使用品牌青；状态色和徽章使用各自的配色。

输入框焦点、次级按钮焦点和 Select 展开时，使用 `dark-400` 边线（灰白 30%）和 `white/6` 外圈。按钮在深色模式下的焦点环 offset 用 `dark-900`。深色遮罩由 `html.dark` 把 `--overlay-bg` 和 `--overlay-bg-strong` 加深到 0.7 和 0.85。

新手引导的中英文 HTML 通过 `styles/onboarding.css` 的 `--tour-*` 变量读取提示框背景、操作提示和辅助文字颜色。深色提示框用 12% 透明度的状态色背景，正文继承弹窗文字色，操作提示用品牌青，辅助文字用 `dark-300`。

## 图表主题

- 模型、分组和端点分布表的名称与展开箭头默认使用普通文字色（浅色 `gray-900`、深色 `white`）。可展开的行在整行悬停时将名称和箭头显示为品牌色（浅色 `primary-600`、深色 `primary-500`）。关闭明细或分组 ID 小于等于 0 时使用普通文字色。
- 饼图和圆环图按原始指标值画扇区，每项的占比是该项数值除以所有项之和，tooltip 用同一组数值计算百分比。扇区数据保持原值：不做对数压缩，也不设最小占比，零值不占扇区。消费排行的"其他"汇总项也按实际费用计算。
- 分组和端点分布的条形图、柱状图使用从零开始的线性数值轴，柱长和原始指标值成正比，不使用对数轴或数据压缩。
- 图表主题从 `composables/useChartTheme.ts` 取：它提供响应式的 `colors`（text、muted、grid 三档，zinc 色系）和 `onThemeChange` 重绘钩子。`document.documentElement.classList.contains('dark')` 这种快照判断没有响应式依赖，切换主题后不会重新计算，门禁会拦截。vue-chartjs 场景下，colors 是响应式的，切换主题时自动重绘；Stripe Elements 等命令式场景，用 watch 加 `elements.update({ appearance })` 重新应用。
- 分布图的调色板只有一份 `CHART_PALETTE`（12 色，按切片排名取色），"Others" 汇总切片用 `CHART_OTHER_COLOR`；token 趋势的序列色用 `CHART_SERIES_COLORS`。刻度字号 `CHART_TICK_FONT_SIZE`（10），图例字号 `CHART_LEGEND_FONT_SIZE`（11）。
- 以下业务配色留在组件内部：TeamMemberUsageCharts 的成员固定配色（同一成员在不同图表里颜色一致）；OpsSwitchRateTrendChart 和 DashboardView 的本地图表主题（深色刻度 `#D4D4D8`、网格 `#27272A`，浅色使用品牌色调的字面值）；DailyRevenueChart 的线条和填充色。
- 用户仪表盘趋势图（`UserDashboardUsageChart`）只画线，线下区域和辉光阴影都不画，保持扁平。单指标时叠加上一周期的对比线，用 `colors.muted` 虚线，按下标和本期对齐。峰值环、均值虚线、标签胶囊和末端的呼吸点画在同一个本地插件里，颜色取自 `useChartTheme` 和卡片底色。尚未结束的时段画成虚线。
- token 数量用 `utils/format.ts` 格式化：`formatTokens`（两位小数加千分位）和 `formatTokensK`（一位小数）用途不同，按场景选择。ProviderTodayStatsCell 的 K1/M2 混合精度是有意保留的本地写法。

<a id="loading_feedback"></a>
## 加载反馈

路由切换使用 `NavigationProgress`：页面顶部一条 2px 的品牌青细线，`pointer-events: none`，不挡点击。`router/navigationLoading.ts` 在鉴权守卫之前注册导航反馈，覆盖异步页面加载、重定向、取消和异常。每次导航都有编号，较早的导航和它的结束动画无法结束较新的导航。

导航开始时立即显示进度线；成功、取消或异常后，进度线铺满再淡出，由 `animationend` 通知状态层隐藏。它表示导航仍在进行，并不反映请求的完成百分比。减少动画模式下显示静态细线，结束动画也更短。页面内的数据请求和轮询由各自的数据区域显示加载状态，全局导航指示器只管路由。

数据还没取到时，在内容将要出现的位置显示骨架，页头、筛选工具栏、卡片外框和表格列结构都保留。加载期间显示业务零值或"暂无数据"会误导用户，应当显示骨架。通用骨架用 `Skeleton.vue` 和 `.skeleton` 的中性色和轻微脉动，减少动画模式下关闭脉动。装饰块加 `aria-hidden`，区域用加载标签和 `aria-busy` 标明状态。

现有的骨架组件包括管理员仪表盘的 `DashboardSkeleton`、设置页的 `SettingsSkeleton`、模型广场的 `ModelMarketplaceSkeleton`、图表的 `ChartSkeleton`，以及 DataTable 的表格行和移动端卡片。公告按时间线条目占位，热力图直接用日期格子占位。用户仪表盘的用量指标卡、趋势图和模型排行只在第一次取数时显示骨架，分别占位数值、绘图区和 5 行排行；刷新、切换范围或筛选时，已有数据保持显示。加载成功或失败后退出占位，进入页面的数据、错误或空状态分支。标题下方的实时状态条，第一次取数时用行内骨架占位数值；之后轮询失败时，数值显示为占位符 `—`，界面上没有错误弹窗。

页面、弹窗和局部数据区都用这套规则。列表、表单、字段详情和文档正文可以用 `ContentSkeleton`；统计页按实际的卡片网格组合 `Skeleton` 和 `ChartSkeleton`。原生表格用 `TableSkeletonBody`，保留实际的表头，并传入当前可见的列数，条件列变化时占位列同步调整。加载更多时只在列表末尾追加占位，已有内容保持显示。

骨架只用于内容尚未取得的情况。提交、刷新按钮、授权跳转、连接测试、任务执行和支付处理中，使用操作反馈。支付页面第一次读取订单和初始化表单时显示骨架；渠道已经受理或者在等待外部支付时，显示状态提示。加载样式和请求、轮询、支付 SDK、iframe 的挂载时机互相独立。

## 通用图标

通用界面图标使用 `components/icons/Icon.vue`，图标名称和 `IconName` 类型由同目录的 `registry.ts` 管理。图形是 Lucide 风格，逐元素的动效移植自 Lucide Animated，由 `motion-v` 运行；没有官方动画的图形，悬停时做一次 400ms 的轻微缩放。源码版本和许可见图标目录里的 README 和 LICENSE。

- 接口保持 `name`、`size`、`strokeWidth`。默认描边 1.75，颜色继承 `currentColor`。根节点是单个 SVG，调用处的样式、事件、标签和尺寸都透传给它。
- 尺寸档位：`xs` 12px、`sm` 16px、`md` 18px、`lg` 24px、`xl` 32px。侧栏一级导航和顶栏工具区用 `md`（18px）。普通按钮、纯图标操作按钮、分页、选择框箭头、弹窗关闭按钮和侧栏子项用 `sm`（16px），刷新、创建、编辑等操作也用这一档。表格里的微型操作可以用 `xs`，快捷入口卡片的主图和媒体灯箱的关闭图标用 `lg`。侧栏的自定义 SVG 和一级导航同尺寸。图标尺寸不影响按钮的点击区域。
- 英文导航用简短名称，省掉上下文已经说明的 Management、Records 等词；页面标题和说明可以用完整名称。
- `animateOnHover` 默认开启。动画绑定在最近的按钮、链接、菜单项等控件上，非标准的交互容器加 `data-icon-trigger`；独立的图标响应自己的悬停。键盘聚焦触发同样的反馈，每次进入只播放一次，鼠标和焦点都离开后复位，首次挂载时不自动播放。
- 演示时间轴通过 `animationActive` 触发图标的动画序列：变为 `true` 时播放一次，变回 `false` 时复位，和 `animateOnHover` 互不影响。Key 重定向演示用节点的 `animationstart` 事件触发，重播时先重置，同样遵守减少动态效果、禁用和卸载清理的规则。
- `disabled`、`aria-disabled`、父级 `inert`、加载转圈和系统的减少动态效果设置，都会停止装饰动画，状态变化立即生效。离开、换图形或卸载时取消动画序列；完成时机以动画事件为准，定时器估算的时间不可靠。
- 加载转圈由业务状态控制，`animate-spin` 和 `.spinner` 在减少动态效果模式下静止。展开、排序和选中指示图标设置 `:animate-on-hover="false"`，它们的状态由外层的 CSS 旋转表示。
- 图标默认 `aria-hidden`，本身不可聚焦。根 SVG 默认带 `tabindex="-1"` 和 `focusable="false"`，内部图形在挂载和更新后补上同样的属性，焦点监听因此不会把装饰节点加入 Tab 顺序。鼠标点击仍可能让这些 SVG 节点获得焦点，`useIconAnimation` 会把焦点交回外层控件，或交给 label 关联的表单控件；没有外层控件或控件无法接收焦点时，清除 SVG 的焦点，悬停和点击事件照常响应，独立图标和帮助提示因此不会出现浏览器默认的焦点方框。键盘焦点提示由外层控件提供，明细提示按钮的焦点环用 `focus-visible`。图标有独立含义时传入无障碍标签；图标按钮的名称和点击区域由按钮提供。
- 通用图标和模型品牌图标（包括模型图标的字母占位）加 `select-none`，拖选文字时图标不会出现选中高亮；图标的点击和悬停事件照常响应。
- 供应商、平台品牌和分组展示品牌使用 `ProviderIcon`，具体模型使用 `ModelIcon`。品牌名称传给 `brand`，模型 ID 传给 `model`。提供商测试弹窗中未配置品牌图形的平台使用 `PlatformIcon`。两个组件中的 OpenAI 图标默认使用浅色黑色、深色白色的单色配色。`ProviderIcon` 的 `color` 属性可以指定颜色，网关设置页签用 `currentColor` 跟随页签状态。
- 新增通用图标要加进统一映射。品牌标志、用户上传的 SVG、图表和业务插画使用各自的实现，自定义 SVG 照常经过现有的净化流程。

<a id="ui_motion"></a>
## 动画与时长

普通交互使用 `style.css` 里的动效变量，Tailwind 的 `duration-fast/normal/layout` 只引用这些变量。按钮、提示和内容淡入用 `--motion-fast`（150ms）；菜单、开关和短列表用 `--motion-normal`（200ms）；折叠、侧栏和弹窗进入用 `--motion-layout`（220ms）；浮层退出用 `--motion-exit`（150ms）。缓动用 `--motion-ease`，浮层退出用 `--motion-ease-exit`，菜单位移是 `--motion-shift`（4px）。调用方直接引用变量，普通动效的时长不在调用处重复写。

- Vue 在运行时添加过渡类，所以这些样式写在 Tailwind 的 `@layer` 之外，以免被静态类名扫描裁掉。全局显隐样式有：`fade` 和 `fade-slow`（只有透明度）、`dropdown-fade` 和 `pop-float`（透明度加短距离位移）、`modal` 和 `pop-fade`（遮罩和面板，面板从 0.98 缩放进入）。`fade-slow` 保留现有的调用名，使用内容淡入的档位。向上打开的菜单用 `--dropdown-shift` 调整方向。定位用的 top、left、bottom 不参与浮层过渡；带定位 transform 的提示只用 fade。调用方直接使用这些全局样式，组件里不再保留 scoped 副本。
- 显隐入口用 `MotionTransition`。它把 Vue Transition 的属性和事件原样传下去，并在退出时给旧节点设置 inert，旧节点不再响应操作。当前校验不通过的表单控件会被暂时禁用，以免打断浏览器的原生校验；其他控件保持原样，退出动画里不会闪出禁用状态的底色。`v-show` 的调用处要传 `persisted`。菜单的点击捕获层在关闭时立即移除，面板等退出动画结束再移除。Select、HelpTooltip 等通过 `useFloatingMotion` 跟随正在折叠的触发器，祖先变成 inert 时同步关闭 Teleport 浮层。
- 纵向展开用 `Collapse`：`open` 控制显隐，内容默认保留。`unmountOnHide` 让按需挂载的内容在退出后卸载，退出期间显示上一帧的 slot 数据。`animate=false` 跳过动效，`appear` 控制首次挂载时是否播放，`after-enter` 和 `after-leave` 通知动画完成。Grid 行高自动适应内容，过渡期间裁剪溢出，展开结束后恢复正常溢出；收起的内容设置 inert。表格明细用 `ExpandableTableRow`，保持 tr 和 td 结构，关闭后不留空行。
- 原生 details 的交互改用 `Disclosure`，折叠头是一个按钮，带 `aria-expanded` 和 `aria-controls`。校验和引导通过 `form-field-reveal`、`onboarding-reveal` 展开字段时，Collapse 跳过动画，下一次 DOM 更新后就能定位到字段。
- 分段切换用 `vSegmented` 定位轨道 `::before` 的背景，位置、宽度和高度以 `--motion-normal`（200ms）和公共缓动过渡。选中项由调用方的 `.segmented-item-active` 标记，点击、键盘和 ARIA 属性由调用方提供；指令不增加可聚焦节点。首屏显示和隐藏后重新出现时直接定位，没有选中项时移除背景。指令在 Vue 更新和 ResizeObserver 通知时重新测量，能处理宽度不同的文案、换行、字体和尺寸变化，卸载时释放观察器。快速连续切换时，CSS 从当前位置继续过渡；减少动态效果时，按公共时长变量立即完成。
- 页签和页面内容用 `vContentReveal`：默认对已有元素做 150ms 的透明度动画，不增加包装层，也不重新挂载组件。AppLayout 只处理 main，AuthLayout 只处理内容区，公开页面在各自的内容容器上接入。路由以 `route.path` 触发，同一路径下 query 或 hash 变化时不重播；快速换页时取消旧动画，导航进度条独立表示路由加载。页签用 v-if 还是 v-show，由各页面自己决定。
- 购买页用 `MotionTransition` 在同一个视窗内横向切换，面板只以 `activeTab` 为 key：进入 Subscribe 时向左翻页，返回 Pay-as-you-go 时向右翻页，时长 `--motion-layout`（220ms）。退出的面板绝对定位，由公共生命周期隔离交互；动画期间裁剪视窗，结束后恢复摘要吸顶和浮层布局。在同一个页签内填写表单或推进支付流程时，面板不重新挂载。首次加载显示骨架时不播放切换动画，减少动态效果时按公共时长变量直接切换。
- 两种弹窗共用 `useDialogLifecycle`：滚动锁保持到弹窗实际退出完成，快速重新打开不会重复计数，旧弹窗不会抢走新弹窗的焦点，Escape 只关闭最上层的 BaseDialog。父级按需挂载的安全凭证弹窗使用 `useLeavingPresence`，关闭后等外壳的 `after-leave` 再卸载。
- Toast 和用户直接增删的短列表用 `motion-list`，以稳定的业务 key 识别进入、退出和位置变化。分页表格、虚拟列表和轮询结果不做逐行动效。
- 系统开启减少动态效果时，普通过渡变量缩短到 1ms，位移归零，Collapse 和内容淡入直接切到最终状态；动态修改这个偏好会取消正在进行的内容淡入。完成和清理通过 Vue 生命周期和动画完成事件执行，业务代码里不用定时器估算结束时间。
- 专用动画保留自己的几何：CreativeCanvas 工具条展开、CreativeRunHistory 侧栏滑入和条目详情、CustomPageView 目录抽屉使用普通的时长和缓动；通用图标、加载反馈、计时进度和公开页的数字滚动使用各自的节奏，并各自处理减少动态效果。切换主题时临时关闭过渡。
- 用户兑换成功时，卡片内显示 `RedeemCelebration`：统计区保留占位，显示成功图标和权益信息；彩纸动画持续 2 秒，由动画完成事件移除；提示保留 3 秒后按公共 fade 退出。每次成功用独立的序号触发，再次提交或卸载时清理上一次的效果。减少动态效果时只显示静态的成功信息，播放过程中修改偏好也立即取消装饰。成功消息通过礼貌播报区域通知辅助技术。
- 用户仪表盘的专用动效时长集中在 `components/user/dashboard/dashboardMotion.ts`：脚本直接引用常量，样式读取页面根节点注入的 `--dash-*` 变量。包括这些动效：
  - 指标数字滚动（`useCountUp`，四次方缓出，读屏只读最终值）。
  - 趋势图在切换指标或重新取数时，按指标和数据版本重建，和首次打开一样从左到右裁剪描线。
  - 迷你走势线裁剪展开。
  - 热力图首次取数时按列错峰入场。
  - 模型占比条依次伸展。
  - 各区块首次挂载时依次上移淡入，结束后清除 transform。

  减少动态效果时，以上动效都直接显示最终状态。
- 同一个动效的 JS 时长和 CSS 时长来自同一个常量。KeyUsageView 的圆环和数字滚动共用 `RING_ANIMATION_MS`。`constants/ui.ts` 的 `COPY_FEEDBACK_MS`（2000）和 `SEARCH_DEBOUNCE_MS`（300）分别是反馈的停留时间和防抖时间，不属于过渡档位。公告连播由 after-leave 推进队列，组件中途卸载时由卸载回调完成清理。

<a id="rule_list_editor"></a>
## 行列表编辑器

逐条添加的映射和规则列表使用 `components/common/RuleListEditor.vue`。从来源模型到目标模型的映射使用基于它的 `ModelMappingEditor.vue`，提供商弹窗再包一层 `components/provider/ProviderModelMappingEditor.vue`，默认带上提供商映射的说明和占位。

- 外壳只负责展示。增删和排序通过 `add`、`remove(index)`、`move(from, to)` 事件交给父级执行，所以既能接入向上 emit 的组件，也能接入用不可变更新的组件。
- 头部标题和右侧操作按钮垂直居中，说明文字另起一行。添加按钮使用带加号的 `btn btn-secondary`，行数达到 `max` 时禁用。列表没有自己的标题（标题和开关在外层）时，使用 `add-placement="footer"`，按钮放到列表下方。
- 标题旁的帮助入口放进 `title-suffix` 插槽，`ModelMappingEditor` 会透传这个插槽。Key 模型重定向用点击展开的 `HelpTooltip`：请求卡片横向依次经过客户端、密钥规则和后续路由，在密钥节点展示模型名的替换。关闭时卸载演示，减少动态效果时直接显示最终结果。
- 空态是 `rounded-control` 的虚线框加居中说明，只在传入 `empty-text` 时显示。
- `line` 形态用分隔线分隔各行，适合一两个字段的短行，操作按钮用 `btn-icon`，和 36px 的输入框对齐。`card` 形态是 `rounded-surface` 的浅底卡片，适合多字段的规则；传入 `item-label` 时，卡片头显示"规则 #n"和紧凑的 `btn-icon-sm` 操作按钮。
- 行操作有三个图标：上移 `arrowUp`、下移 `arrowDown`、删除 `trash`，默认灰色，删除在悬停时变红。第一行不能上移，最后一行不能下移，行数不超过 `min` 时不能删除。
- 行的 key 默认用对象身份（`createStableObjectKeyResolver`），下标会在增删后错位。行内容是基本类型、只能靠下标区分时（例如协议回退目标），传 `:animated="false"` 关闭列表动效，否则退出动画会落在错误的行上。
- 点击添加按钮后，新行的第一个输入框或选择框获得焦点；预设和 JSON 导入追加的行保持当前焦点不变。
- 删除时，如果后面还有行，退出的行使用 `motion-list` 的绝对定位，由后面的行补上位置；删除的是最后一行时，它留在文档流里淡出，下方内容等淡出结束再上移，两者不会重叠。所有退出动画结束后，空态才出现。
- 行对象原地编辑，保持对象身份；结构变化（增删、排序）时发出新数组。需要不可变更新的位置，让克隆后的行继承原行的展示 key，例如公告定向编辑器用 WeakMap 保存组和条件的 key，编辑字段时不会重新挂载。展示 key 不写进提交的数据。
- 字段错误用 `.input-error` 和 `.input-error-text`，列表级的汇总错误通过 `error` 以 `role="alert"` 显示。映射校验规则在 `utils/modelMappingRules.ts`，默认不开启，由各位置按需传入 `KEY_REDIRECT_RULES`、`WILDCARD_ONLY_RULES` 等选项。统一样式时，各位置的校验范围保持原样。
- 编辑器提交给后端的数据形状保持原样：API Key 和分组提交映射对象，提供商通过 `buildModelMappingObject` 构造。

已经接入的编辑器：

- API Key 的模型重定向和复合分组绑定；分组的模型映射、模型路由、推理强度映射和协议回退。
- 提供商的模型映射、紧凑模型映射、请求头覆写、临时不可调度规则，以及 TLS 指纹路由规则。
- 系统设置里的整流器签名模式、Beta 模型模式、OpenAI Fast 规则和模型模式、默认订阅和认证来源订阅、提示词替换、搜索服务商、自定义端点、菜单、首页推荐模型、页脚分组和链接、登录协议文档、创作模型和配额通知邮箱。
- 定价页的模型定价条目、提供商统计规则和它的定价条目；定价卡片的上下文区间、按次和媒体计费层级、时间段。
- EasyPay 自定义支付方式、公告定向的 OR 组和 AND 条件、运维告警静默条目、用户属性的下拉选项。

标签式输入（例如 `ModelTagInput`、`ModelWhitelistSelector` 和收件人 chip），以及带搜索和分页的倍率表格，使用各自的组件。价格配置和属性配置的模型规则共用 `ModelTagInput`：回车添加标签，粘贴批量导入，逐项删除，保存时提交模型数组。属性配置里的空模型规则需要补全或删除后才能保存。

## 表格密度

全站只有一套密度：表头 `px-4 py-2 text-xs font-medium tracking-wider`，数据单元格 `px-4 py-3 text-sm`。`.table` 组件类、`TablePageLayout` 的深度样式和 `DataTable` 三处要保持一致。

分页表格的表体和 `Pagination` 共用一个 `rounded-surface` 外框，外框用 `overflow-hidden` 裁出底部圆角。分页放在表体的滚动区域之外，两者之间直接相接，没有 `space-y-*` 或外边距；固定高度的表格用 flex 分配表体高度，分页不收缩。`TablePageLayout` 已经包含这个结构；独立卡片和弹窗也按这个约定，嵌入 `UsageTable` 时传 `flat`，表格只保留外框这一层边框。桌面分页上下留白 8px，浅色底 `gray-50/80`，深色底 `dark-900`，控件高 36px；窄屏显示上一页、下一页和当前页数。

移动端卡片里，`DataTable` 的每一行左边是 `text-xs` 标签，右边是单元格内容，两边用 `items-baseline` 按首行文字对齐。单元格的首个元素是图标、头像或色条时，flex 容器会拿这个元素的底边当基线，标签就会偏下。这类单元格给第一行文字加 `self-baseline`，参考 `GroupBadge` 的分组名和 `UsageTable` 的 Token、延迟列。徽章组件也照这样处理，例如 `PlatformBadge`、`CapacityBadge`。文字是裸文本节点时，先包一层 `<span>`。

按基线对齐的元素会贴到 flex 容器顶部，由此有两条补充：

- 同一行比它高的文字元素（例如带内边距的倍率标签）也加 `self-baseline`，文字才在同一高度。
- 开头的图形比文字高时（例如 32px 头像），文字再加和图形等高的行高（`leading-8`），文字仍在图形的中线上。参考 `UsersView` 的邮箱列。

两个合法例外：

- `DataTable` 按列数自动调整横向 padding（`px-2/3/4/6`），宽表格因此不会溢出。
- 选择列通过 `DataTable` 的 `.table-selection-cell` 使用 `w-11 min-w-11 px-3 text-center`，复选框 `h-4 w-4`，内置的行选择和自定义的 `select` 列布局相同；外层页面的通用单元格样式要排除这一列。

启用左侧固定列时，选择列随横向滚动移出视野，第一个数据列到达左边缘后固定；右侧的操作列按 `stickyActionsColumn` 固定。自定义的 `select` 列放在列定义的第一位，宽度和固定偏移由 DataTable 决定。

`DataTable` 通过 `columnOrderStorageKey` 开启列顺序调整。每种表格用一个稳定、独立的存储键；同一个组件承载不同类型的表格时，键里要包含类型。用户拖动表头的手柄来换列，手柄获得焦点后也可以用左右方向键移动。手柄和排序指示器一样是 `h-5 w-4`，表头高度不变；拖到横向滚动区域的边缘时自动滚动。

选择列和操作列的位置固定，其余数据列都可以调整，左侧固定区域跟随调整后的第一个数据列。表头、单元格、加载占位和移动端卡片使用同一个列顺序。顺序保存在当前浏览器里，隐藏的列保留位置，新增的列追加到数据列末尾；浏览器存储不可用时，仍然可以在当前页面调整。拖拽手柄、升降序排序和表头里的筛选按钮各自处理自己的事件。

<a id="typography"></a>
## 字体和字号

- 正文字体栈在 `tailwind.config.js` 的 `fontFamily.sans`：英文用 Plus Jakarta Sans，中文先用苹方（`"PingFang SC"`），没有苹方的设备用思源黑体网页字体（`"Noto Sans SC Variable"`），再往后是系统字体。等宽字体用 Geist Mono。网页字体都自托管，在 `main.ts` 里先于 `style.css` 引入，`fontTheme.spec.js` 检查字体栈、引入顺序和下面的 PostCSS 改写。
- 苹方随苹果系统安装，许可限定在苹果设备上使用。思源黑体的字形和苹方接近，Windows 和 Android 上由它显示中文，各平台的中文看起来基本一致。思源黑体按字符切成约 100 个 woff2 文件，字体栈前面的字体缺少某个字符时，浏览器才下载这个字符所在的文件。苹果设备上苹方覆盖了常用汉字，通常一个文件也用不到。
- `postcss.config.js` 的 `adjust-noto-sans-sc-font-face` 插件改写思源黑体的 `@font-face`。字重范围上限改成 600，因为苹方最粗是 Semibold（600），`font-bold` 在两边一样粗。`unicode-range` 去掉默认按彩色显示的 emoji 码位，🍥 这类字符由系统的彩色 emoji 字体显示。
- emoji 由浏览器的系统回退显示，字体栈里没有 emoji 字体。加入 `"Apple Color Emoji"` 后，Jakarta 还没加载完或加载失败时，Chrome 会用它渲染数字和空格，数字明显变宽。
- 最小字号是 `text-xs`（12px），徽章和辅助数字也一样；`text-[9px]`、`text-[10px]`、`text-[11px]` 会被门禁拦截。
- 正文和表格用 `text-sm`，说明文字用 `text-xs`，页面标题用 `.page-title`（`text-2xl font-bold`），`h1` 不单独设字号。

## 合法例外

- 营销页和落地页（`HomeView`、`KeyUsageView` 等公开页）的 hero 标题可以用展示级字号。
- `onboarding.css` 覆盖 driver.js 第三方样式时使用的 `!important`。
- i18n 文案里内嵌的导览 HTML（`src/i18n/**`）属于内容字符串，它的 inline style 不参与 token 校验。
- 测试文件里断言"某个类名不存在"的负断言会被扫描命中，在行尾加 `check-ui-allow` 豁免。
- 保留手写外壳的弹窗：BackupView 的 R2Guide 和 SubscriptionsView 的指南弹窗（max-w-2xl，BaseDialog 没有对应档位）、AnnouncementPopup 和 AnnouncementBell 的弹窗（独立的层级和定制过渡）、ProviderTestModal 的图片灯箱（媒体覆盖层，用强遮罩档）。新增弹窗默认用 BaseDialog，不要照搬这些结构。
- RiskControlView 搜索框的 `pl-9` 图标留白（和 `input-icon-*` 的各档数值都不同，局部保留）。
- ProvidersView 跟随鼠标的操作菜单（定位方式特殊，不使用 `getFloatingPanelPosition`）。
- textarea 按内容决定高度、GroupBadge 的方角造型、OpsDashboard 250ms 的路由同步防抖（用途和搜索防抖不同）、CreativeCanvas 工具条和 CreativeRunHistory 条目详情的结构性展开动画，都是局部写法，保持原样。

## 校验

在 `frontend/` 下运行 `npm run check:ui`（`scripts/check-ui-tokens.mjs`）检查上述规则，它和 `lint:check` 一起作为提交前检查。具体的拦截规则和正则写在脚本头部的注释里，包括：旧圆角、任意值的圆角、字号和 z 值、36px 冗余、手写开关、max-h 任意像素、非响应式的暗色判断、z-index 字面量、JS 断点字面量和 `tailwind.config` 的键约束。新增组件样式前对照本文；确实需要偏离时，在 PR 里说明理由，并考虑把它补进例外清单。

## 相关文档

- [系统架构](system_architecture.md)：前端在整体部署中的位置和静态资源的交付方式。
- [开发、验证与上游同步](../operations/development_workflow.md)：前端工具链和验证分层。

模型测试、定时测试、白名单及配置选择器显示实际 ID；模型广场和首页推荐卡片使用展示名称，并提供 ID 和复制操作。白名单候选通过统一属性目录分页搜索，保留已选项和手动输入；明确传入的候选范围直接使用。真实上游同步合并提供商返回的 ID，由管理员保存后生效。
