# 前端 UI 规范

> 上级目录：[架构文档目录](index.md)

本文记录前端设计 token 与组件样式的强制约定：圆角层级、间距网格、控件尺寸、表格密度和字号下限。覆盖 `frontend/tailwind.config.js`、`frontend/src/style.css` 与全部 Vue 组件；不覆盖颜色主题、深色模式实现和业务组件的局部布局。修改前端组件、样式或这两个文件前先读本文。

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

## 间距约定

- 全部间距落在 Tailwind 4px 网格上，禁止 `mt-[2px]`、`padding-left: 17px` 这类任意值。
- 卡片 padding 只有两档：独立卡片 `p-6`，嵌套面板、网格卡和统计卡 `p-4`。不再使用 `p-5`。
- 布局水平 padding 链在 header 与 main 之间完全一致：`px-4 md:px-6 lg:px-8`，保证两侧边缘在所有断点对齐。
- 顶栏高度由 `--header-h`（3.5rem）单一来源驱动；表格页高度计算引用该变量，不手写 `100vh - 64px` 之类的硬编码和。
- 表单内 `space-y-2/3/4/6` 按上下文自选，不归一。

## 控件尺寸

- 按钮、输入框、下拉触发器共用 36px 基线（`min-h-9` / `h-9`），分页器控件同为 36px——表格页脚不再压缩分页尺寸。
- 图标按钮 `h-9 w-9`；`.btn-sm` 用于表格行内等紧凑场景。

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
- 测试文件里的负断言（断言某类名不存在）会命中扫描，行尾加 `check-ui-allow` 豁免。

## 校验

`frontend/` 下运行 `npm run check:ui`（`scripts/check-ui-tokens.mjs`）扫描上述规则，随 `lint:check` 一起作为提交前检查。新增组件样式前对照本文；确需偏离时在 PR 中说明理由，并考虑补充为例外条款。

## 相关文档

- [系统架构](system_architecture.md)：前端在整体部署中的组成与静态资源交付。
- [开发、验证与上游同步](../operations/development_workflow.md)：前端工具链与验证分层。
