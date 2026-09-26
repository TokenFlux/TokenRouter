# 前端 UI 规范

> 上级目录：[架构文档目录](index.md)

本文记录前端设计 token 与组件样式的强制约定：圆角层级、间距网格、控件尺寸、菜单与浮层、弹窗、层级、断点、动画时长、表格密度、图表主题和字号下限。覆盖 `frontend/tailwind.config.js`、`frontend/src/style.css` 与全部 Vue 组件；不覆盖颜色主题、深色模式实现（图表除外，见「图表主题」节）和业务组件的局部布局。修改前端组件、样式或这两个文件前先读本文。

## 圆角层级

圆角只允许使用语义 token，数值的唯一来源是 `style.css` `:root` 的 `--radius-*` 变量，`tailwind.config.js` 的 `borderRadius` 只做 var() 引用：

| token | 值 | 用途 |
|---|---|---|
| `rounded-compact` | 6px | 徽章、chip、tab 项、骨架屏、行内代码、小图标块 |
| `rounded-control` | 8px | 按钮、输入框、下拉框、侧栏链接、浮层面板 |
| `rounded-surface` | 12px | 卡片、表格容器、toast、代码块 |
| `rounded-dialog` | 16px | 桌面端弹窗（移动端弹窗仍用 surface） |
| `rounded-full` / `rounded-none` | — | 胶囊、进度条、开关；需要直角时的覆盖 |

旧尺度名（`rounded-sm/md/lg/xl/2xl/3xl`）、裸 `rounded` 和 `rounded-[...]` 任意值一律禁用——旧 key 已从配置删除，写旧类名不会生成任何样式。裸 CSS 里的 `border-radius` 只允许 `var(--radius-*)`、`0` 或 `9999px`。

唯一例外：边长 ≤16px 的微型装饰元素（如用量热力图的 12px 格子），全局最小档 compact（6px）已达边长一半、视觉上近似椭圆，允许用组件级局部变量保持更小半径（如 `.heatmap-cell` 的 `--radius-cell: 4px`），不新增全局档位。

## 间距约定

- 全部间距落在 Tailwind 4px 网格上，禁止 `mt-[2px]`、`padding-left: 17px` 这类任意值。
- 卡片 padding 只有两档：独立卡片 `p-6`，嵌套面板、网格卡和统计卡 `p-4`。不再使用 `p-5`。
- 布局水平 padding 链在 header 与 main 之间完全一致：`px-4 md:px-6 lg:px-8`，保证两侧边缘在所有断点对齐。
- 布局尺寸 token 只在 `style.css` 的 `:root` 定义一份：`--header-h`（3.5rem，顶栏高度）、`--sidebar-w`（14rem，侧栏展开宽）、`--sidebar-w-collapsed`（4.5rem，侧栏折叠宽）。顶栏高度、主区 `padding-top`、侧栏遮罩 `top`、侧栏宽度与主区 `lg:ml-*` 偏移一律引用变量（如 `h-[var(--header-h)]`），不写 `h-14`、`top-14`、`w-56` 这类平行字面量。吸顶偏移与锚点 `scroll-margin-top` 同样以 `calc(var(--header-h) + 余量)` 组合（参考 SettingsView 的 tabs 吸顶），余量写构成注释。
- 垂直空间由 AppLayout 的 flex 链统一分配：wrapper（`flex-col`，普通模式 `min-h-screen` / 全屏模式 `h-full`）→ `.app-main`（`flex-1 flex-col`）→ 页头（自然高度）+ 页面内容。需要撑满剩余高度的页面容器（如 `TablePageLayout`、`CustomPageView` 根元素）自取 `flex-1 min-h-0`，禁止手写 `calc(100vh - …)` 视口差值、禁止负 margin 抵消父级内边距；`--main-pad-*`、`--page-heading-space` 这类镜像变量已删除，不得重新引入。全屏工作区（`full-viewport`）模式下 `.app-main` 无内边距，页面天然满幅。
- 表单内 `space-y-2/3/4/6` 按上下文自选，不归一。

## 控件尺寸

- 按钮、输入框、下拉触发器共用 36px 基线（`.btn` / `.input` 均为 `min-h-9`），分页器控件同为 36px——表格页脚不再压缩分页尺寸。基线之上再写 `h-9` 属冗余（门禁拦截）；紧凑档要 36px 时用 `btn-sm/md/lg + h-9` 显式提挡。
- 图标按钮两档：`.btn-icon`（h-9 w-9）与 `.btn-icon-sm`（h-8 w-8），自带 `rounded-control` 与居中布局，站点只补 hover/颜色类；`.btn-sm` 用于表格行内等紧凑场景。
- 下拉触发器（Select、DateRangePicker）模板组合 `input input-trigger` + 各自状态类，不复制基线配方。
- 输入框图标/字符前后缀统一走 `input-icon-*` 机制（`style.css`）：容器 `input-icon-wrap`，图标位 `input-icon` / `input-icon-right`（可点击内容加 `input-icon-action`），输入框按侧加 `input-has-icon` / `input-has-icon-right`；文本留白由变量推导（`留白 = inset + slot`）。档位：默认（inset 0.75rem、留白 2.5rem）、`input-icon-lg`（auth 表单，inset 0.875rem、留白 2.75rem）、`input-icon-text`（`$` 等窄字符前缀，留白 2rem），紧凑搜索框内联 `--input-icon-slot:1.5rem`（留白 2.25rem）。

## 开关

全站开关只有 `components/common/Toggle.vue` 一个实现，禁止手写轨道/滑块（门禁拦截 `h-6 w-11` / `h-5 w-9` 组合）。

- 几何单一来源是 Toggle  scoped 样式里的 CSS 变量（`--toggle-track-w/h`、`--toggle-thumb`、`--toggle-inset`），开态位移由 `calc(轨道宽 − 滑块 − 2×边距)` 推导，改档位只调变量。
- 档位：`size="md"`（44×24）/ `size="sm"`（36×20），`variant="inset"`（滑块内嵌，默认）/ `variant="flush"`（大滑块贴边，原 Headless 手写风）。
- 配色：开态默认 `toggle-active`（≡ `bg-primary-600`）；关态由 `off-tone` 选档——`default`（gray-300）/ `soft`（gray-200，手写迁移站点的原色）。个别站点的亮色开态（`bg-primary-500`）或 hover 配色用 `on-class` / `off-class` 整串透传，不新增档位。
- 异步保存场景用 `:model-value` + `@update:model-value` 受控写法，值由处理器写回（参考 AccountsView 的可调度开关）。

## 菜单与浮层

- 下拉容器统一用 `.dropdown` 纯容器配方（定位、圆角、阴影、暗色）；箭头定位与入场动效不进门配方，由调用点自补（目前仅 AppHeader 一处）。
- 菜单项两档：`.dropdown-item`（px-4）与紧凑档 `.dropdown-item-sm`（px-3），配色、hover、过渡都在配方里，站点只补 `gap-*`、`rounded-control` 这类布局增量。配方基线是中性色；品牌色场景（顶栏用户菜单）加 `.dropdown-item-brand` 修饰类，它定义在基线之后，同层同优先级时后定义生效。
- 表格行内操作菜单（4 个 `*ActionMenu`）的浮层容器统一 `.action-menu` 类（fixed 定位 + 层级 + 面板样式），宽度类（w-48/w-52）与 `action-menu-content` 钩子类留在调用点。
- 遮罩透明度只有两档，唯一来源是 `:root` 变量：常规 `--overlay-bg`(black/50)、媒体灯箱等强遮罩 `--overlay-bg-strong`(black/70)。模板写 `bg-[var(--overlay-bg)]`，禁止手写 `bg-black/50` 这类字面值——原 /55、/60 漂移值已就近归并入标准档。
- 浮层面板最大高度三档，唯一来源是 `:root` 的 `--max-h-menu-sm`(15rem)、`--max-h-menu`(20rem)、`--max-h-panel`(26.25rem)，模板对应 `max-h-menu-sm/menu/panel`；像素任意值 `max-h-[Npx]` 由门禁拦截，局部特例（如告警表 520px）加 `check-ui-allow` 说明。vh/dvh/calc 等视口相对值语义不同，不入档也不拦截。
- 挂载到 body 的浮层定位只有一份实现：`utils/floatingPanel.ts` 的 `getFloatingPanelPosition`（翻转、对齐、夹取、窄屏行为全部由 options 表达：固定高菜单用 `fixedHeight`，左对齐面板用 `align: 'left'`，菜单类传 `pinLeftOnMobile: false`)。禁止在组件里重写 rect/spaceBelow 翻转几何。JS 侧面板尺寸常量在 `constants/overlay.ts`(`SELECT_PANEL_MAX_HEIGHT`、`MIN_COMFORTABLE_PANEL_HEIGHT`)，与样式档位同源。

## 断点

- 断点数值的唯一来源是 `constants/layout.ts`:`BREAKPOINT_SM/MD/LG`(640/768/1024，与 Tailwind 默认 screens 对齐，tailwind.config.js 不得自定义 screens)、`MEDIA_MIN_*`/`MEDIA_MAX_*` 媒体查询串、`TABLE_DESKTOP_MEDIA_QUERY`(lg 别名，表格与分页器共用切换点）。契约测试 `breakpointTheme.spec.ts` 锁定对齐。
- max 变体约定 `max = min - 1px`(与 Tailwind max-* 语义一致）,min/max 区间互斥、没有 1px 重叠带；JS 侧写 `MEDIA_MAX_SM` 而不是 `<= 640`。
- JS 断点判断（useMediaQuery/matchMedia/innerWidth 比较）一律引用常量，字面量由门禁拦截；CSS @media 不支持 var()，唯一例外落点（如 CustomPageView 目录抽屉的 639px）写字面量 + 推导注释，由契约测试锁定。
- Ops 五个表格组件曾用 768 并自称「与 DataTable 一致」，实际 DataTable 是 1024——已统一修正为 `TABLE_DESKTOP_MEDIA_QUERY`，修掉 768–1023px 区间桌面表格配移动分页器的混排。

## 层级 z-index

层级梯队三轨同源：`style.css` `:root` 的 `--z-*` 变量承载唯一数值 → `tailwind.config.js` 的 `zIndex` 扩展只做 var() 引用（模板用 `z-modal`、`z-toast` 这类语义工具类）→ `constants/overlay.ts` 的 `Z_INDEX` 常量供 JS 内联。契约测试 `src/__tests__/zIndexTheme.spec.ts` 锁定三轨一致；任意值 `z-[...]` 与数字字面量由门禁拦截。梯队（只命名不调层级，数值即现状）:

| 值 | token | 用途 |
|---|---|---|
| 30 | `chart-tooltip` / `sidebar-overlay` | 图表 tooltip（有意低于导航）/ 侧栏遮罩 |
| 40 | `sidebar` | 侧栏 |
| 50 | `header` / `modal` | 顶栏 / 弹窗遮罩（同层靠 DOM 顺序与 teleport 决胜） |
| 60 | `modal-nested` | 嵌套弹窗、筛选面板 |
| 100 | `tooltip` / `announcement` | 弹窗内 tooltip、下拉面板、灯箱 / 公告底层 |
| 120 / 140 | `announcement-raised` / `announcement-top` | 公告梯队（递增有意） |
| 9998 | `menu-overlay` | ActionMenu 点击捕获层 |
| 9999 | `toast` / `action-menu` / `teleport-tooltip` | 通知 / 行内操作菜单 / teleported 提示浮层 |
| 99999 | `help-tooltip` | HelpTooltip(teleported) |
| 100000000 | `tour` | driver.js 引导层，外部约束，仅登记不暴露工具类；onboarding.css 保持字面值 !important |
| 100000020 | `teleport-dropdown` | Select 等 teleported 下拉（必须压过引导层） |

局部堆叠上下文不入阶梯、不加全局 token:DataTable 内部（0/20/200/210/220)、CreativeCanvas 画布内、CustomPageView 目录抽屉、弹窗内 sticky 表头（z-[1])，均加 `check-ui-allow` 豁免。表内局部 dropdown 用普通 `z-50` 即可（局部上下文，不占语义档）。

## 弹窗

- 默认入口是 `BaseDialog`：宽度档位 narrow/normal/wide/extra-wide/full，Escape 关闭、点击外部关闭、焦点管理与背景滚动锁定全部内置，新弹窗不要再手写 `fixed inset-0` 外壳。
- 安全凭证流程（TOTP 设置/禁用/登录验证/提权）走 `AuthCardDialog`：居中图标头、无右上角关闭按钮、整卡 p-6，是与 BaseDialog 并存的独立风格族。它不 teleport、保持内联渲染，嵌套层级由 `z-index` prop 决胜。
- 分诊标准：结构同构（标题头 + 内容 + 按钮行）的手写弹窗迁 BaseDialog；有定制视觉结构的保留并登记在下面的例外清单。

## 图表主题

- 图表主题的唯一入口是 `composables/useChartTheme.ts`：响应式 `colors`（text/muted/grid 三档语义，zinc 体系）+ `onThemeChange` 重绘钩子。禁止 `document.documentElement.classList.contains('dark')` 快照判断（门禁拦截）——它没有响应式依赖，切主题不重算，曾导致 8 处图表切主题不换色。vue-chartjs 场景 colors 变响应式即自动重绘；Stripe Elements 等命令式场景用 watch + `elements.update({ appearance })` 重应用。
- 分布图调色板只有一份 `CHART_PALETTE`（12 色，按切片排名取色），"Others" 聚合切片用 `CHART_OTHER_COLOR`;token 趋势序列色用 `CHART_SERIES_COLORS`。刻度字号 `CHART_TICK_FONT_SIZE`(10)、图例字号 `CHART_LEGEND_FONT_SIZE`(11)。
- 业务色例外留在本地：TeamMemberUsageCharts 成员固定配色（跨图表按成员稳定取色）、OpsSwitchRateTrendChart 与 DashboardView 的品牌调网格/刻度色（dark-200/primary-900 字面值）、DailyRevenueChart 的线/填充色对。
- token 数量格式化统一 `utils/format.ts` 的 `formatTokens`（两位小数 + 千分位）与 `formatTokensK`（一位小数），语义不同不混用；AccountTodayStatsCell 的 K1/M2 混合精度是有意的本地变体。

## 动画与时长

- 配对双写的「CSS 动画 + JS 定时器」一律改事件驱动：节点移除挂 `@animationend`/`after-leave` 等事件，不用 `setTimeout` 猜时长。已收敛四处：CreativeStudioView 提交飞信（`@animationend` 移除节点，删掉多留 100ms 的缓冲定时器）、AppSidebar 移动抽屉（路由变化事件收起，同路由点击立即收起）、公告连播（`dismissPopup` 后由弹层 after-leave 回调 `onPopupClosed` 推进队列；动画途中被卸载时由组件 onBeforeUnmount 补推）。
- 同一次动效的 JS/CSS 两处时长必须同源：KeyUsageView 圆环用 `RING_ANIMATION_MS` 常量内联注入 `transitionDuration`，数字滚动共用同一常量（修复历史上 1000ms 对 1.2s 的漂移，数字比圆环先停 200ms）。
- 共享时长常量在 `constants/ui.ts`:`COPY_FEEDBACK_MS`(2000，「已复制」反馈；EndpointPopover 1800 与 KeysView 800 已就近归并）与 `SEARCH_DEBOUNCE_MS`(300，搜索/筛选防抖；OpsDashboard 的 250ms 路由同步防抖语义不同，保留）。
- 全局过渡配方在 `style.css`（自带 reduced-motion 收敛）：`fade`（默认 0.2s，调用点用 `--fade-duration-enter/leave` 覆盖，如侧栏遮罩 200/150ms)、`fade-slow`(0.3s+上移，auth 页区块）、`pop-fade`（公告类浮层，内层 section 缩放）、`dropdown-fade`（下拉面板，只动画透明度与位移，定位由 JS 计算不参与过渡）、`pop-float`（锚定弹层，调用点用 `--pop-origin`/`--pop-shift` 表达锚点方向）。迁入后不得再写 scoped 副本。
- 结构性展开动画不入全局配方，保留本地：CreativeCanvas 工具条 max-width/max-height 扩展、CreativeRunHistory 条目详情的 grid-template-rows 折叠。

## 表格密度

全站只有一套密度：表头 `px-4 py-2 text-xs font-medium tracking-wider`，数据单元格 `px-4 py-3 text-sm`。`.table` 组件类、`TablePageLayout` 深度样式和 `DataTable` 必须保持一致。

两个合法例外：

- `DataTable` 按列数自适应横向 padding（`px-2/3/4/6`），宽表格不至于溢出；
- 选择列宽固定 `--select-col-width: 52px`。

## 字号

- 下限 `text-xs`（12px），禁止 `text-[9px]/[10px]/[11px]`；徽章和辅助数字也不例外。
- 正文与表格 `text-sm`，说明文字 `text-xs`，页面标题统一 `.page-title`（`text-2xl font-bold`），不散装 `h1` 字号。

## 合法例外

- 营销与落地页（`HomeView`、`KeyUsageView` 等公开页）的 hero 标题可用展示级字号。
- `onboarding.css` 覆盖 driver.js 第三方样式时的 `!important`。
- i18n 文案中内嵌的导览 HTML（`src/i18n/**`）属于内容字符串，其 inline style 不参与 token 校验。
- 测试文件里的负断言（断言某类名不存在）会命中扫描，行尾加 `check-ui-allow` 豁免。
- 弹窗分诊保留的手写外壳：RedeemView 成功结果弹窗（成功图标头 + 着色 footer）、BackupView R2Guide 与 SubscriptionsView 指南弹窗（max-w-2xl 无 BaseDialog 对应档位）、AnnouncementPopup 与 AnnouncementBell 弹窗（独立层级梯队 + 定制过渡）、两个 AccountTestModal 的图片灯箱（媒体覆盖层，用强遮罩档）、KeysView 列设置面板（p-2/shadow-xl 漂移值保留）。新增弹窗默认走 BaseDialog，不复刻这些结构。
- RiskControlView 搜索框的 `pl-9` 图标留白（与 `input-icon-*` 档位值都不重合，局部保留）。
- AccountsView 的鼠标跟随操作菜单（定位语义独特，不走 `getFloatingPanelPosition`）。
- textarea 内容驱动高度、GroupBadge 方角造型、OpsDashboard 的 250ms 路由同步防抖（语义不同于搜索防抖）、CreativeCanvas 工具条与 CreativeRunHistory 条目详情的结构性展开动画，均属局部语义，不强行入档。

## 校验

`frontend/` 下运行 `npm run check:ui`（`scripts/check-ui-tokens.mjs`）扫描上述规则，随 `lint:check` 一起作为提交前检查；具体拦截规则与正则见脚本头部注释（旧圆角、任意圆角/字号/z 值、36px 冗余、手写开关、max-h 任意像素、非响应式暗色判断、z-index 字面量、JS 断点字面量、tailwind.config 键约束）。新增组件样式前对照本文；确需偏离时在 PR 中说明理由，并考虑补充为例外条款。

## 相关文档

- [系统架构](system_architecture.md)：前端在整体部署中的组成与静态资源交付。
- [开发、验证与上游同步](../operations/development_workflow.md)：前端工具链与验证分层。
