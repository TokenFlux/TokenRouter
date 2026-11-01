import assert from 'node:assert/strict'
import { mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import vue from '@vitejs/plugin-vue'
import postcss from 'postcss'
import selectorParser from 'postcss-selector-parser'
import { build, mergeConfig } from 'vite'
import { tokenrouterPlugin } from '../dist/vite.js'

// vite.test.mjs builds an isolated author entry with the public SDK preset.
// The output contract is verified from emitted files and bundler module metadata,
// so a successful typecheck cannot hide a second Vue runtime or extra chunks.

// An entry with a lazy page and CSS must still produce exactly the two host
// assets. Vue and the SDK stay external, and every emitted ordinary selector
// starts with the manifest plugin identity's root attribute.
test('emits one IIFE and stylesheet while leaving Vue and SDK external', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'trplugin-vite-'))
  try {
    await writeFile(
      path.join(root, 'entry.ts'),
      `import { h } from 'vue'; import { definePlugin } from '@tokenflux/tokenrouter-plugin-sdk'; import './style.css'; definePlugin({pages:{home:{render:()=>h('p','home')},other:()=>import('./page.ts').then(m=>m.default)}})`,
    )
    await writeFile(
      path.join(root, 'page.ts'),
      `import { h } from 'vue'; export default {render:()=>h('p','other')}`,
    )
    await writeFile(path.join(root, 'style.css'), `.page { color: var(--tr-color-text) }`)
    const result = await build(
      mergeConfig(
        tokenrouterPlugin({
          pluginId: 'test.plugin',
          entry: path.join(root, 'entry.ts'),
          outDir: path.join(root, 'dist'),
        }),
        { root, configFile: false, logLevel: 'silent' },
      ),
    )
    assert.deepEqual((await readdir(path.join(root, 'dist'))).sort(), ['index.css', 'index.js'])
    const outputs = (Array.isArray(result) ? result : [result]).flatMap((item) => item.output)
    const chunk = outputs.find((item) => item.type === 'chunk')
    assert.ok(chunk)
    assert.ok(
      Object.keys(chunk.modules).every(
        (id) => !id.includes('node_modules/vue') && !id.includes('@vue/runtime'),
      ),
    )
    const script = await readFile(path.join(root, 'dist/index.js'), 'utf8')
    assert.match(script, /__TR_VUE__/)
    assert.match(script, /__TR_PLUGIN_SDK__/)
    const stylesheet = postcss.parse(await readFile(path.join(root, 'dist/index.css'), 'utf8'))
    stylesheet.walkRules((rule) => {
      selectorParser((selectors) => {
        selectors.each((selector) => {
          assert.equal(selector.first?.type, 'attribute')
          assert.equal(selector.first?.attribute, 'data-tr-plugin')
          assert.equal(selector.first?.value, 'test.plugin')
        })
      }).processSync(rule.selector)
    })
  } finally {
    // Remove only this test's isolated build tree after all output assertions,
    // including when bundling or an assertion fails midway through verification.
    await rm(root, { recursive: true, force: true })
  }
})

// A real Vue SFC consumer protects component compilation and dynamic imports.
// Its bundled output must use host globals, retain scoped styles, and contain
// no separate lazy chunk or bundled Vue runtime implementation.
test('builds Vue SFC consumers with generated SDK declarations', async () => {
  const root = await mkdtemp(path.join(tmpdir(), 'trplugin-sfc-'))
  try {
    const result = await build(
      mergeConfig(
        tokenrouterPlugin({
          pluginId: 'test.consumer',
          entry: fileURLToPath(new URL('./fixtures/consumer/entry.ts', import.meta.url)),
          outDir: root,
        }),
        { plugins: [vue()], configFile: false, logLevel: 'silent' },
      ),
    )
    assert.deepEqual((await readdir(root)).sort(), ['index.css', 'index.js'])
    const outputs = (Array.isArray(result) ? result : [result]).flatMap((item) => item.output)
    const chunk = outputs.find((item) => item.type === 'chunk')
    assert.ok(chunk)
    assert.ok(Object.keys(chunk.modules).some((id) => id.includes('LazyPage.vue')))
    assert.ok(Object.keys(chunk.modules).every((id) => !id.includes('node_modules/')))
    const script = await readFile(path.join(root, 'index.js'), 'utf8')
    assert.match(script, /__TR_VUE__/)
    assert.match(script, /__TR_PLUGIN_SDK__/)
    const css = postcss.parse(await readFile(path.join(root, 'index.css'), 'utf8'))
    css.walkRules((rule) => {
      selectorParser((selectors) => {
        selectors.each((selector) => {
          assert.equal(selector.first?.type, 'attribute')
          assert.equal(selector.first?.attribute, 'data-tr-plugin')
          assert.equal(selector.first?.value, 'test.consumer')
        })
      }).processSync(rule.selector)
    })
  } finally {
    // Keep emitted consumer assets outside the repository and remove only this
    // test's directory after success or failure, leaving source fixtures untouched.
    await rm(root, { recursive: true, force: true })
  }
})
