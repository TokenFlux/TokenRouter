import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import type { PluginUI } from '@/api/plugins.types'
import { loadPlugin, retainPluginBundles } from '../loader'
import { definePlugin } from '../registry'

// loader.spec.ts drives resource events against actual script and link elements.
// It verifies stylesheet ordering, shared loading, integrity attributes, cache
// replacement, and failed-load cleanup without fetching or executing arbitrary
// remote scripts in the DOM test environment.

// Pending promises remain observed through cleanup so a failed assertion cannot
// leave canceled resource work as an unrelated unhandled rejection in later cases.
const pending: Promise<unknown>[] = []

/** descriptor constructs one accepted frontend contribution with a chosen content identity. */
function descriptor(integrity = 'sha384-first'): PluginUI {
  return {
    id: 'test.loader',
    version: '1.0.0',
    name: 'Loader',
    script: { url: '/api/v1/plugins/test.loader/assets/1.0.0/frontend/index.js', integrity },
    styles: [
      {
        url: '/api/v1/plugins/test.loader/assets/1.0.0/frontend/index.css',
        integrity: `${integrity}-style`,
      },
    ],
    pages: [],
    menus: [],
  }
}

/** observe retains a promise while preserving its original outcome for the test. */
function observe<T>(promise: Promise<T>): Promise<T> {
  pending.push(promise.catch(() => undefined))
  return promise
}

/** register completes the exact admitted script's synchronous definition and load event.
 * The temporary currentScript getter models classic IIFE execution. Restoration
 * happens before returning, so unrelated tests cannot inherit that script identity.
 */
function register(script: HTMLScriptElement): void {
  const current = vi.spyOn(document, 'currentScript', 'get').mockReturnValue(script)
  definePlugin({ pages: { home: { name: 'FixturePage' } } })
  current.mockRestore()
  script.dispatchEvent(new Event('load'))
}

afterEach(async () => {
  retainPluginBundles([])
  await Promise.all(pending.splice(0))
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('plugin bundle loader', () => {
  // Two callers request the same accepted release before its stylesheet finishes.
  // They share one load, and the script appears only after the required CSS succeeds.
  it('loads styles first and shares one integrity-checked script', async () => {
    const plugin = descriptor()
    const first = observe(loadPlugin(plugin))
    const second = observe(loadPlugin(plugin))
    expect(first).toBe(second)
    const style = document.head.querySelector<HTMLLinkElement>(
      'link[data-plugin-id="test.loader"]',
    )!
    expect(style.integrity).toBe('sha384-first-style')
    expect(style.crossOrigin).toBe('anonymous')
    expect(document.head.querySelector('script[data-plugin-id="test.loader"]')).toBeNull()
    style.dispatchEvent(new Event('load'))
    await flushPromises()
    const script = document.head.querySelector<HTMLScriptElement>(
      'script[data-plugin-id="test.loader"]',
    )!
    expect(script.integrity).toBe('sha384-first')
    expect(script.crossOrigin).toBe('anonymous')
    expect(new URL(script.src).searchParams.get('v')).toBe(plugin.script.integrity)
    register(script)
    expect((await first).pages.home).toBeDefined()
    expect(await second).toBe(await first)
    expect(await loadPlugin(plugin)).toBe(await first)
  })

  // A failed script leaves no successful registration or stylesheet behind.
  // Retrying the same accepted identity creates fresh elements and can complete normally.
  it('cleans failed resources before allowing a retry', async () => {
    const plugin = descriptor()
    const first = observe(loadPlugin(plugin))
    document.head
      .querySelector('link[data-plugin-id="test.loader"]')!
      .dispatchEvent(new Event('load'))
    await flushPromises()
    document.head
      .querySelector('script[data-plugin-id="test.loader"]')!
      .dispatchEvent(new Event('error'))
    await expect(first).rejects.toThrow('could not be loaded')
    expect(document.head.querySelector('[data-plugin-id="test.loader"]')).toBeNull()
    const retry = observe(loadPlugin(plugin))
    document.head
      .querySelector('link[data-plugin-id="test.loader"]')!
      .dispatchEvent(new Event('load'))
    await flushPromises()
    register(
      document.head.querySelector<HTMLScriptElement>('script[data-plugin-id="test.loader"]')!,
    )
    expect((await retry).pages.home).toBeDefined()
  })

  // Repair changes content hashes while retaining the release version and URL path.
  // A fresh cache-busted request replaces the earlier style and script only after successful registration.
  it('replaces same-version content after repair', async () => {
    const first = observe(loadPlugin(descriptor()))
    document.head
      .querySelector('link[data-plugin-id="test.loader"]')!
      .dispatchEvent(new Event('load'))
    await flushPromises()
    const oldScript = document.head.querySelector<HTMLScriptElement>(
      'script[data-plugin-id="test.loader"]',
    )!
    register(oldScript)
    await first
    const replacement = observe(loadPlugin(descriptor('sha384-second')))
    const styles = document.head.querySelectorAll<HTMLLinkElement>(
      'link[data-plugin-id="test.loader"]',
    )
    expect(styles).toHaveLength(2)
    styles[1].dispatchEvent(new Event('load'))
    await flushPromises()
    const scripts = document.head.querySelectorAll<HTMLScriptElement>(
      'script[data-plugin-id="test.loader"]',
    )
    expect(scripts[1].src).not.toBe(oldScript.src)
    register(scripts[1])
    await replacement
    expect(oldScript.isConnected).toBe(false)
    expect(document.head.querySelectorAll('link[data-plugin-id="test.loader"]')).toHaveLength(1)
  })

  // No resource event arrives before the complete load budget expires.
  // The promise rejects and cleanup removes the pending style without inserting a script afterward.
  it('times out incomplete resources after fifteen seconds', async () => {
    vi.useFakeTimers()
    const promise = observe(loadPlugin(descriptor()))
    await vi.advanceTimersByTimeAsync(15000)
    await expect(promise).rejects.toThrow('timed out')
    expect(document.head.querySelector('[data-plugin-id="test.loader"]')).toBeNull()
  })

  // An author invokes registration outside an executing host-created script.
  // The runtime rejects the call rather than assigning it to whichever plugin loaded most recently.
  it('rejects registration without a plugin identity', () => {
    expect(() => definePlugin({ pages: {} })).toThrow('host-loaded')
  })
})

// Discovery changes a visible installation before its replacement resource succeeds.
// Existing ready styles survive a failed replacement, and removing the installation still releases every retained resource.
it('retains ready styles through discovery replacement failures', async () => {
  const original = descriptor()
  const first = observe(loadPlugin(original))
  document.head
    .querySelector('link[data-plugin-id="test.loader"]')!
    .dispatchEvent(new Event('load'))
  await flushPromises()
  register(document.head.querySelector<HTMLScriptElement>('script[data-plugin-id="test.loader"]')!)
  await first
  const oldStyle = document.head.querySelector('link[data-plugin-id="test.loader"]')!
  const replacement = descriptor('sha384-replacement')
  retainPluginBundles([replacement])
  expect(oldStyle.isConnected).toBe(true)
  const attempt = observe(loadPlugin(replacement))
  const newStyle = [...document.head.querySelectorAll('link[data-plugin-id="test.loader"]')].find(
    (node) => node !== oldStyle,
  )!
  newStyle.dispatchEvent(new Event('error'))
  await expect(attempt).rejects.toThrow('could not be loaded')
  expect(oldStyle.isConnected).toBe(true)
  retainPluginBundles([])
  expect(oldStyle.isConnected).toBe(false)
})
