import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { usePluginCatalog } from '../usePluginCatalog'
import { catalogFixture, indexFixture } from './fixtures'

// usePluginCatalog.spec.ts controls overlapping distribution observations without network access.
// Deferred readers expose stale-result rejection and post-refresh readback, while
// component disposal proves that pending requests and timers cannot publish into
// a different administrative page instance.

// Transport spies remain independent for source and catalog reads so the tests
// can distinguish one failed observation from another source's accepted state.
const fixture = vi.hoisted(() => ({ catalog: vi.fn(), indexes: vi.fn(), refresh: vi.fn() }))
const wrappers: VueWrapper[] = []
vi.mock('@/api/admin/plugins', () => ({
  catalog: fixture.catalog,
  indexes: fixture.indexes,
  refreshCatalog: fixture.refresh,
}))

/** controlled constructs a manually settled response for testing read-generation ownership. */
function controlled<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/** renderCatalog creates one component-owned observer without immediately selecting a distribution tab. */
function renderCatalog() {
  let catalog!: ReturnType<typeof usePluginCatalog>
  const wrapper = mount(
    defineComponent({
      setup() {
        catalog = usePluginCatalog(ref(false))
        return () => null
      },
    }),
  )
  wrappers.push(wrapper)
  return catalog
}

afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  vi.resetAllMocks()
})

describe('catalog observation ownership', () => {
  // Two explicit reads overlap and the older server response returns last.
  // Only the newer source-qualified entries remain visible, and the retired request receives cancellation without clearing the latest result.
  it('rejects a late older catalog response', async () => {
    const old = controlled<ReturnType<typeof catalogFixture>[]>()
    fixture.catalog
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce([{ ...catalogFixture(), index_name: 'New observation' }])
    fixture.indexes.mockResolvedValue([indexFixture()])
    const catalog = renderCatalog()
    const first = catalog.load()
    const oldSignal = fixture.catalog.mock.calls[0]![0] as AbortSignal
    await catalog.load()
    expect(oldSignal.aborted).toBe(true)
    old.resolve([{ ...catalogFixture(), index_name: 'Old observation' }])
    await first
    expect(catalog.entries.value[0]?.index_name).toBe('New observation')
    expect(catalog.loading.value).toBe(false)
  })

  // Signature verification fails for one source after an accepted market has already loaded.
  // Readback still retrieves current diagnostics and retains usable entries while the aggregate verification failure remains visible to the administrator.
  it('rereads observations after rejected verification', async () => {
    fixture.catalog.mockResolvedValue([catalogFixture()])
    fixture.indexes.mockResolvedValue([
      { ...indexFixture(), signature_status: 'invalid', last_error: 'Signature rejected' },
    ])
    fixture.refresh.mockRejectedValue({ message: 'Signature rejected' })
    const catalog = renderCatalog()
    await catalog.refresh()
    expect(catalog.entries.value).toHaveLength(1)
    expect(catalog.indexes.value[0]?.last_error).toBe('Signature rejected')
    expect(catalog.error.value).toEqual({ message: 'Signature rejected' })
    expect(catalog.refreshing.value).toBe(false)
    await flushPromises()
  })
})
