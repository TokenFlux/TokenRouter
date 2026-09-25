#!/usr/bin/env node
// UI token 防回归检查:扫描前端源码,拦截违反设计 token 规范的写法。
// 规则与 docs/architecture/frontend_ui_conventions.md 保持一致:
//   1. 禁止旧圆角尺度类名(rounded-sm/md/lg/xl/2xl/3xl/4xl 及其方向变体)
//   2. 禁止裸 rounded 与 rounded-[...] 任意值(圆角只能用语义 token)
//   3. 禁止小于 12px 的任意字号(text-[9px]/[10px]/[11px] 等)
//   4. 禁止 border-radius 字面量(只允许 var(--radius-*)、0、9999px 与 CSS 关键字)
//   5. tailwind.config.js 不得重新引入旧圆角 key
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

/** 检查 tailwind.config.js 的 borderRadius 块是否重新引入旧 key。 */
function checkTailwindConfig() {
  const configPath = join(frontendRoot, 'tailwind.config.js')
  const text = readFileSync(configPath, 'utf8')
  const block = text.match(/borderRadius\s*:\s*\{([\s\S]*?)\}/)
  if (!block) return []
  const legacyKey = block[1].match(/(?:^|[\s,])(?:sm|md|lg|xl|DEFAULT|['"]?2xl['"]?|['"]?3xl['"]?|['"]?4xl['"]?)\s*:/)
  if (!legacyKey) return []
  return [
    {
      ruleId: 'legacy-radius-key',
      line: text.slice(0, text.indexOf(block[0])).split('\n').length,
      column: 0,
      snippet: legacyKey[0].trim(),
      message: 'tailwind.config.js 不得定义旧圆角 key(旧类名会随之复活)'
    }
  ]
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
