import assert from 'node:assert/strict'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { loadConfigFromFile } from 'vite'
import { frontendRoot } from './source-analysis.mjs'

// vite-config.test.mjs verifies configuration selection for frontend CLI scripts.
// Older checkouts can retain compiled JavaScript beside the TypeScript source.
// Each command must explicitly load the current source even when that obsolete
// artifact exists, rather than relying on Vite's default filename precedence.

// Development, build, and preview scripts run in a directory containing both
// config formats. Their configured paths must select TypeScript and ignore obsolete JavaScript.
test('Vite scripts select TypeScript despite stale JavaScript configuration', async (context) => {
  const directory = mkdtempSync(join(tmpdir(), 'tokenrouter-vite-config-'))
  context.after(() => rmSync(directory, { recursive: true, force: true }))
  writeFileSync(join(directory, 'package.json'), JSON.stringify({ type: 'module' }))
  writeFileSync(join(directory, 'vite.config.js'), 'throw new Error("Stale configuration loaded")')
  writeFileSync(
    join(directory, 'vite.config.ts'),
    'export default { define: { __CONFIG_SOURCE__: "true" } }',
  )
  const { scripts } = JSON.parse(readFileSync(join(frontendRoot, 'package.json'), 'utf8'))
  for (const name of ['dev', 'build', 'build:assets', 'preview']) {
    const command = scripts[name].split('&&').at(-1).trim().split(/\s+/)
    assert.equal(command[0], 'vite', name)
    const optionIndex = command.indexOf('--config')
    const configFile = optionIndex < 0 ? undefined : join(directory, command[optionIndex + 1])
    const loaded = await loadConfigFromFile(
      { command: command.includes('build') ? 'build' : 'serve', mode: 'production' },
      configFile,
      directory,
    )
    assert.equal(loaded.path, join(directory, 'vite.config.ts'), name)
    assert.equal(loaded.config.define.__CONFIG_SOURCE__, 'true', name)
  }
})
