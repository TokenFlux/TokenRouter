#!/usr/bin/env node
// UI token 防回归检查:扫描前端源码,拦截违反设计 token 规范的写法。
// 规则与 docs/architecture/frontend_ui_conventions.md 保持一致:
//   1. 禁止旧圆角尺度类名(rounded-sm/md/lg/xl/2xl/3xl/4xl 及其方向变体)
//   2. 禁止裸 rounded 与 rounded-[...] 任意值(圆角只能用语义 token)
//   3. 禁止小于 12px 的任意字号(text-[9px]/[10px]/[11px] 等)
//   4. 禁止 border-radius 字面量(只允许 var(--radius-*)、0、9999px 与 CSS 关键字)
//   5. 禁止 btn/input 与 h-9 冗余共现(基线已 36px;btn-sm/md/lg 显式提挡除外)
//   6. 禁止手写开关轨道尺寸组合(h-6 w-11 / h-5 w-9),开关只用 <Toggle>
//   7. 禁止 max-h-[Npx] 任意像素值(面板高度用 max-h-menu-sm/menu/panel 三档;
//      vh/dvh/%/calc 等非像素局部值不匹配本规则)
//   8. 禁止非响应式暗色判断 documentElement.classList.contains('dark')
//      (主题状态走 useTheme/useChartTheme;检测原语与一次性命令式 DOM 除外)
//   9. 禁止 z-[...] 任意层级值(层级用语义 z-* 工具类;局部堆叠上下文加 check-ui-allow)
//  10. 禁止 z-index/zIndex 数字字面量(CSS 用 var(--z-*),JS 用 Z_INDEX 常量;
//      局部堆叠与第三方覆盖加 check-ui-allow)
//  11. 禁止 JS 里的宽度断点字面量(useMediaQuery/matchMedia/innerWidth 比较;
//      断点唯一来源 constants/layout.ts;prefers-* 特性查询不命中)
//  12. tailwind.config.js 不得重新引入旧圆角 key;maxHeight 块只含 var() 引用
// 特殊行可在行尾加 `check-ui-allow` 注释豁免(仅限测试负断言等合法场景)。
// 迁移期间本脚本用于统计剩余待改数量;迁移完成后接入 lint:check 作为门禁。

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

const frontendRoot = fileURLToPath(new URL('..', import.meta.url))
const srcRoot = join(frontendRoot, 'src')

// 扫描 src 下的源码与样式文件,以及根 index.html(启动页可能含工具类)
const SCAN_EXTENSIONS = new Set(['.vue', '.css', '.ts', '.tsx', '.js', '.jsx'])
const EXTRA_FILES = [join(frontendRoot, 'index.html')]

// i18n 文案里内嵌的导览 HTML 属于内容字符串(含历史 inline style),不按组件样式校验
const EXCLUDED_DIRS = new Set(['i18n'])

/** 递归收集目录下匹配扩展名的文件(自带遍历,避免依赖 Node 版本的 recursive 选项)。 */
function collectFiles(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry.startsWith('.') || EXCLUDED_DIRS.has(entry)) continue
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      collectFiles(full, out)
    } else if (SCAN_EXTENSIONS.has(entry.slice(entry.lastIndexOf('.')))) {
      out.push(full)
    }
  }
  return out
}

const RULES = [
  {
    id: 'legacy-radius',
    pattern:
      /(?<![\w-])rounded(?:-(?:t|b|l|r|tl|tr|bl|br|ss|se|es|ee))?-(?:sm|md|lg|xl|2xl|3xl|4xl)(?![\w-])/g,
    message: '旧圆角尺度类名,改用 rounded-compact/control/surface/dialog'
  },
  {
    id: 'bare-rounded',
    pattern: /(?<![\w-])rounded(?![-\w])/g,
    message: '裸 rounded 已废弃,改用 rounded-compact'
  },
  {
    id: 'arbitrary-radius',
    pattern: /(?<![\w-])rounded(?:-(?:t|b|l|r|tl|tr|bl|br|ss|se|es|ee))?-\[/g,
    message: '圆角不允许任意值,改用语义 token 或 rounded-full/none'
  },
  {
    id: 'tiny-text',
    pattern: /text-\[(?:\d|1[01])(?:\.\d+)?px\]/g,
    message: '字号下限 12px,改用 text-xs'
  },
  // 36px 是 .btn/.input 的基线高度,叠写 h-9 属冗余;btn-sm/md/lg 配 h-9 是合法的显式提挡。
  // h-9 前后排除连字符,避免误伤 min-h-9/max-h-9。
  {
    id: 'btn-exact-h9',
    pattern:
      /class=(["'])(?=[^"']*(?<![\w-])h-9(?![\w-]))(?=[^"']*(?<![\w-])btn(?![\w-]))(?![^"']*(?<![\w-])btn-(?:sm|md|lg)(?![\w-]))[^"']*\1/g,
    message: '.btn 基线已是 36px(min-h-9),去掉冗余 h-9(紧凑档提挡请用 btn-sm/md/lg + h-9)'
  },
  {
    id: 'input-exact-h9',
    pattern: /class=(["'])(?=[^"']*(?<![\w-])h-9(?![\w-]))(?=[^"']*(?<![\w-])input(?![\w-]))[^"']*\1/g,
    message: '.input 基线已是 36px(min-h-9),去掉冗余 h-9'
  },
  // 开关轨道只由 common/Toggle.vue 的 CSS 变量生成,禁止手写轨道/滑块尺寸组合重新引入第二套实现。
  {
    id: 'manual-toggle-track',
    pattern:
      /class=(["'])(?=[^"']*(?<![\w-])h-6(?![\w-]))(?=[^"']*(?<![\w-])w-11(?![\w-]))[^"']*\1|class=(["'])(?=[^"']*(?<![\w-])h-5(?![\w-]))(?=[^"']*(?<![\w-])w-9(?![\w-]))[^"']*\2/g,
    message: '开关轨道请用 <Toggle>(size/variant 选档),不手写 h-6 w-11 / h-5 w-9 组合'
  },
  // 面板最大高度的唯一来源是 :root 的 --max-h-menu-sm/menu/panel;只匹配像素任意值,
  // vh/dvh/%/calc/min() 等视口相对局部值不拦截(语义不同,不入档)。
  {
    id: 'arbitrary-max-h',
    pattern: /(?<![\w-])max-h-\[\d+(?:\.\d+)?px\]/g,
    message: '面板高度用 max-h-menu-sm(15rem)/menu(20rem)/panel(26.25rem) 三档,局部特例加 check-ui-allow'
  },
  // classList 快照没有响应式依赖,切主题不重算;组件里一律 useTheme/useChartTheme。
  // 豁免:useTheme.ts(响应式原语)、embedded-url.ts(SSR 安全的一次性探测)、
  // useSwipeSelect.ts(拖拽框选覆盖层,生命周期一次拖拽,重建即重读)。
  {
    id: 'nonreactive-dark',
    pattern: /documentElement\.classList\.contains\((['"])dark\1\)/g,
    message: '暗色判断请用 useTheme/useChartTheme 的响应式 isDark,不要快照 classList'
  },
  // 层级梯队唯一来源是 style.css :root 的 --z-* 变量(契约测试三轨锁定);
  // 表内局部 dropdown 用普通 z-50 不命中本规则,只有任意值被拦。
  {
    id: 'arbitrary-z',
    pattern: /(?<![\w-])z-\[\d+\]/g,
    message: '层级用语义 z-* 工具类(sidebar/header/modal/modal-nested/tooltip/toast 等),局部堆叠加 check-ui-allow'
  },
  // CSS z-index 与 JS zIndex 的数字字面量;z-index: var(--z-*) 不命中。
   {
    id: 'z-index-literal',
    pattern: /\bz-index\s*:\s*\d|\bzIndex\s*[:=]\s*['"]?\d/g,
    message: '层级字面值:CSS 用 var(--z-*),JS 用 constants/overlay.ts 的 Z_INDEX;局部堆叠/第三方覆盖加 check-ui-allow'
  },
  // 断点数值唯一来源是 constants/layout.ts;prefers-color-scheme/reduced-motion 是特性查询,不命中。
  {
    id: 'literal-width-breakpoint',
    pattern:
      /(?:useMediaQuery|matchMedia)\(\s*['"]\((?:min|max)-width:\s*\d+px|\binnerWidth\s*[<>=]=?\s*\d{3,}|\b\d{3,}\s*[<]=\s*innerWidth\b/g,
    message: '宽度断点请用 constants/layout.ts 的 BREAKPOINT_*/MEDIA_* 常量,不写 px 字面量'
  }
]

// border-radius 声明单独按行解析:剔除 var(...) 后,只允许 0、9999px、斜杠、!important 与 CSS 全局关键字
const BORDER_RADIUS_DECL = /border-radius\s*:\s*([^;]+);/g
const ALLOWED_RADIUS_VALUE = /^([\s/]|0|9999px|inherit|initial|unset|revert|!important)*$/

/** 收集一个文件内全部违规,返回 [{ruleId, line, column, snippet, message}] */
function checkFile(file) {
  const text = readFileSync(file, 'utf8')
  const lines = text.split('\n')
  const violations = []

  // 带 check-ui-allow 的行整行豁免(用于测试负断言等合法引用)
  const allowed = new Set(lines.map((l, i) => (l.includes('check-ui-allow') ? i : -1)).filter(i => i >= 0))

  for (const rule of RULES) {
    for (const match of text.matchAll(rule.pattern)) {
      const offset = match.index
      const line = text.slice(0, offset).split('\n').length - 1
      if (allowed.has(line)) continue
      const column = offset - text.lastIndexOf('\n', offset - 1) - 1
      violations.push({ ruleId: rule.id, line: line + 1, column, snippet: match[0], message: rule.message })
    }
  }

  for (const match of text.matchAll(BORDER_RADIUS_DECL)) {
    const value = match[1].replace(/var\([^)]*\)/g, '').trim()
    if (ALLOWED_RADIUS_VALUE.test(value)) continue
    const offset = match.index
    const line = text.slice(0, offset).split('\n').length - 1
    if (allowed.has(line)) continue
    const column = offset - text.lastIndexOf('\n', offset - 1) - 1
    violations.push({
      ruleId: 'raw-border-radius',
      line: line + 1,
      column,
      snippet: match[0],
      message: 'border-radius 只允许 var(--radius-*)、0、9999px 或 CSS 全局关键字'
    })
  }

  return violations
}

/** 检查 tailwind.config.js 的 borderRadius 块是否重新引入旧 key、maxHeight 块是否只含 var() 引用。 */
function checkTailwindConfig() {
  const configPath = join(frontendRoot, 'tailwind.config.js')
  const text = readFileSync(configPath, 'utf8')
  const violations = []

  const block = text.match(/borderRadius\s*:\s*\{([\s\S]*?)\}/)
  if (block) {
    const legacyKey = block[1].match(/(?:^|[\s,])(?:sm|md|lg|xl|DEFAULT|['"]?2xl['"]?|['"]?3xl['"]?|['"]?4xl['"]?)\s*:/)
    if (legacyKey) {
      violations.push({
        ruleId: 'legacy-radius-key',
        line: text.slice(0, text.indexOf(block[0])).split('\n').length,
        column: 0,
        snippet: legacyKey[0].trim(),
        message: 'tailwind.config.js 不得定义旧圆角 key(旧类名会随之复活)'
      })
    }
  }

  // maxHeight 三档必须 var() 引用 :root 变量,字面量会造成第二处数值来源。
  const maxHeightBlock = text.match(/maxHeight\s*:\s*\{([\s\S]*?)\}/)
  if (maxHeightBlock) {
    for (const pair of maxHeightBlock[1].matchAll(/(['"]?[\w-]+['"]?)\s*:\s*([^,]+)/g)) {
      const value = pair[2].trim().replace(/^['"]|['"]$/g, '')
      if (value.startsWith('var(')) continue
      violations.push({
        ruleId: 'literal-max-height-key',
        line: text.slice(0, text.indexOf(maxHeightBlock[0])).split('\n').length,
        column: 0,
        snippet: pair[0].trim(),
        message: 'tailwind.config.js 的 maxHeight 只允许 var(--max-h-*) 引用,数值唯一来源在 style.css :root'
      })
      break
    }
  }

  return violations
}

const files = [...collectFiles(srcRoot), ...EXTRA_FILES]
const all = []
for (const file of files) {
  for (const v of checkFile(file)) {
    all.push({ file: relative(frontendRoot, file), ...v })
  }
}
for (const v of checkTailwindConfig()) {
  all.push({ file: 'tailwind.config.js', ...v })
}

if (all.length === 0) {
  console.log('check:ui 通过:未发现违反设计 token 规范的写法')
  process.exit(0)
}

// 按规则分组输出,每组最多展示 50 条,避免迁移期间刷屏
const MAX_PER_RULE = 50
const byRule = new Map()
for (const v of all) {
  if (!byRule.has(v.ruleId)) byRule.set(v.ruleId, [])
  byRule.get(v.ruleId).push(v)
}

let total = 0
for (const [ruleId, list] of byRule) {
  total += list.length
  console.error(`\n[${ruleId}] ${list.length} 处:${list[0].message}`)
  for (const v of list.slice(0, MAX_PER_RULE)) {
    console.error(`  ${v.file}:${v.line}:${v.column}  ${v.snippet}`)
  }
  if (list.length > MAX_PER_RULE) console.error(`  … 其余 ${list.length - MAX_PER_RULE} 处从略`)
}
console.error(`\ncheck:ui 失败:共 ${total} 处违规(特殊场景可在行尾加 check-ui-allow 豁免)`)
process.exit(1)
