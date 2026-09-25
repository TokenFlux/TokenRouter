import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const viewPath = resolve(dirname(fileURLToPath(import.meta.url)), '../CreativeStudioView.vue')
const viewSource = readFileSync(viewPath, 'utf8')

describe('CreativeStudioView 移动端视口布局', () => {
  it('使用 AppLayout 全屏视口模式', () => {
    expect(viewSource).toContain('<AppLayout full-viewport>')
  })

  it('stage 高度由 AppLayout flex 链分配,不再复制顶栏尺寸或抵消父级内边距', () => {
    expect(viewSource).toContain('class="relative h-full min-h-0"')
    // 禁止负 margin 抵消 app-main 内边距、禁止写死顶栏高度的视口差值。
    expect(viewSource).not.toMatch(/-m[txyblr]-/)
    expect(viewSource).not.toContain('h-[calc(100dvh-3.5rem)]')
    expect(viewSource).not.toContain('h-[calc(100vh-')
  })
})
