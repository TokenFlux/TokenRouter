import { flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, reactive } from 'vue'
import type { MarketplaceGroup } from '@/api/marketplace.types'
import { useMarketplaceModelIndex } from '../useMarketplaceModelIndex'

// useMarketplaceModelIndex.spec.ts checks the on-demand model index used by global search.
// The marketplace API is replaced with controllable promises, and the account is a
// reactive fake so tests can switch identity while a request is still running.

const api = vi.hoisted(() => ({ getPublic: vi.fn(), getAvailable: vi.fn() }))
const auth = reactive({ isAuthenticated: true, user: { id: 1 } as { id: number } | null })

vi.mock('@/api/marketplace', () => ({
  getMarketplaceModels: api.getPublic,
  getAvailableMarketplaceModels: api.getAvailable,
}))
vi.mock('@/stores', () => ({ useAuthStore: () => auth }))

// catalog returns one group offering the given model IDs.
function catalog(...ids: string[]): MarketplaceGroup[] {
  return [
    {
      id: 1,
      name: 'g',
      description: '',
      display_brand: '',
      sort_order: 0,
      rate_multiplier: 1,
      models: ids.map((id) => ({
        id,
        display_name: id,
        pricing: { pricing_mode: 'token', price_status: 'unpriced' },
      })),
    },
  ] as MarketplaceGroup[]
}

// deferred returns a promise with its resolve function exposed to the test.
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

let scope = effectScope()

// createIndex creates the composable inside a disposable effect scope.
function createIndex() {
  return scope.run(() => useMarketplaceModelIndex())!
}

beforeEach(() => {
  scope = effectScope()
  auth.isAuthenticated = true
  auth.user = { id: 1 }
})

afterEach(() => {
  scope.stop()
  vi.resetAllMocks()
  vi.restoreAllMocks()
})

describe('useMarketplaceModelIndex', () => {
  // Nothing is requested before the palette opens, and repeated opens for the same
  // account reuse the loaded catalog instead of sending another request.
  it('loads once per account on demand', async () => {
    api.getAvailable.mockResolvedValue(catalog('a'))
    const index = createIndex()
    expect(api.getAvailable).not.toHaveBeenCalled()
    await index.ensureLoaded()
    await index.ensureLoaded()
    expect(api.getAvailable).toHaveBeenCalledOnce()
    expect(index.status.value).toBe('ready')
    expect(index.entries.value.map((entry) => entry.id)).toEqual(['a'])
  })

  // An account signs out while its private catalog is still loading. The late response
  // must not publish the previous account's models, and the index returns to idle so the
  // next open loads the public catalog.
  it('drops a late response after the account changes', async () => {
    const pending = deferred<MarketplaceGroup[]>()
    api.getAvailable.mockReturnValue(pending.promise)
    api.getPublic.mockResolvedValue(catalog('public'))
    const index = createIndex()
    void index.ensureLoaded()
    auth.isAuthenticated = false
    auth.user = null
    await nextTick()
    pending.resolve(catalog('private'))
    await flushPromises()
    expect(index.entries.value).toEqual([])
    expect(index.status.value).toBe('idle')
    await index.ensureLoaded()
    expect(index.entries.value.map((entry) => entry.id)).toEqual(['public'])
  })

  // A failed request stays failed when the palette reopens, so an outage is not retried on
  // every open; retry sends a new request.
  it('waits for retry after a failure', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    api.getAvailable.mockRejectedValueOnce(new Error('offline')).mockResolvedValue(catalog('a'))
    const index = createIndex()
    await index.ensureLoaded()
    await index.ensureLoaded()
    expect(index.status.value).toBe('error')
    expect(api.getAvailable).toHaveBeenCalledOnce()
    await index.retry()
    expect(index.status.value).toBe('ready')
  })
})
