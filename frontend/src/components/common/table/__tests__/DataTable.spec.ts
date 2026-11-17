import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import DataTable from '../DataTable.vue'

// DataTable.spec.ts checks responsive rendering, sorting, selection, and virtual
// measurement ownership. Mounted tables and local browser stubs reproduce
// viewport changes and replaced datasets, while direct virtualizer observations
// verify which cached row identities remain reusable.

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key,
  }),
}))

// stubDesktopMatchMedia installs a media query stub reporting the desktop
// branch.
const stubDesktopMatchMedia = () => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: true,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  })
}

// stubMobileMatchMedia installs a media query stub reporting the mobile branch.
const stubMobileMatchMedia = () => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  })
}

describe('DataTable', () => {
  beforeEach(() => {
    stubDesktopMatchMedia()
    localStorage.clear()
  })

  // The table mounts under the desktop media stub. It must request the shared
  // 1024-pixel media query so responsive rendering follows the application
  // breakpoint.
  it('uses the shared 1024px desktop breakpoint', () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [{ id: 1, name: 'Key' }],
      },
    })

    expect(window.matchMedia).toHaveBeenCalledWith('(min-width: 1024px)')
    wrapper.unmount()
  })

  // A pending mobile table shares field headings with its loaded cards. Values
  // and interactive selection only appear once the parent supplies the result.
  it('keeps the mobile card labels visible while row values load', async () => {
    stubMobileMatchMedia()
    const wrapper = mount(DataTable, {
      props: {
        columns: [
          { key: 'name', label: 'Name' },
          { key: 'status', label: 'Status' },
        ],
        data: [],
        loading: true,
        selectable: true,
      },
    })

    const placeholder = wrapper.get('[data-loading-skeleton]')
    expect(placeholder.text()).toBe('NameStatus')
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('empty.noData')

    await wrapper.setProps({ data: [{ id: 1, name: 'Example', status: 'Ready' }], loading: false })

    expect(wrapper.find('[data-loading-skeleton]').exists()).toBe(false)
    expect(wrapper.get('[data-field="name"]').text()).toBe('NameExample')
    expect(wrapper.get('[data-test="select-row"]').exists()).toBe(true)
    wrapper.unmount()
  })

  // A sortable column starts ascending and is clicked once. Both direction
  // arrows must remain visible while active color and accessible sort direction
  // switch to descending.
  it('renders paired sort arrows and highlights the active direction', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [
          { key: 'name', label: 'Name', sortable: true },
          { key: 'created_at', label: 'Created', sortable: true },
        ],
        data: [
          { id: 1, name: 'Beta', created_at: '2026-01-02T00:00:00Z' },
          { id: 2, name: 'Alpha', created_at: '2026-01-01T00:00:00Z' },
        ],
        defaultSortKey: 'name',
        defaultSortOrder: 'asc',
      },
      slots: {
        'header-name': '<span data-test="custom-name-header">Name</span>',
      },
    })

    await wrapper.vm.$nextTick()

    const nameHeader = wrapper.findAll('th')[0]
    expect(nameHeader.find('[data-test="custom-name-header"]').exists()).toBe(true)
    expect(nameHeader.attributes('aria-sort')).toBe('ascending')
    expect(nameHeader.findAll('svg')).toHaveLength(2)
    expect(nameHeader.findAll('svg')[0].classes()).toContain('text-primary-600')
    expect(nameHeader.findAll('svg')[1].classes()).toContain('text-gray-300')

    await nameHeader.trigger('click')
    await wrapper.vm.$nextTick()

    expect(nameHeader.attributes('aria-sort')).toBe('descending')
    expect(nameHeader.findAll('svg')[0].classes()).toContain('text-gray-300')
    expect(nameHeader.findAll('svg')[1].classes()).toContain('text-primary-600')
  })

  // A small dataset remains below the virtualization threshold. Every record
  // must render in the document and no virtual padding spacer may replace
  // ordinary rows.
  it('renders every row with no virtual padding spacer for small datasets (virtualization off)', async () => {
    const data = Array.from({ length: 8 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
      },
    })

    await wrapper.vm.$nextTick()

    // Small lists use ordinary row rendering.
    expect((wrapper.vm as any).shouldVirtualize).toBe(false)
    // Every data record has a rendered row.
    expect(wrapper.findAll('tbody tr[data-index]')).toHaveLength(data.length)
    // Ordinary rows need no virtual spacer.
    expect(wrapper.findAll('tbody tr[aria-hidden="true"]')).toHaveLength(0)
  })

  // Asynchronous contents widen the table while its wrapper width remains fixed.
  // Observing the inner table must update overflow styling after the measurement
  // callback.
  it('rechecks horizontal overflow when asynchronous content changes the table width', async () => {
    const observers: Array<{
      callback: ResizeObserverCallback // Captured resize observation callback.
      elements: Element[] // Targets registered for observation.
    }> = []
    // TrackingResizeObserver records observed elements and exposes callbacks to
    // reproduce table content resizing.
    class TrackingResizeObserver {
      callback: ResizeObserverCallback // Captured resize observation callback.
      elements: Element[] = [] // Targets registered for observation.

      // constructor records the resize callback and registers this observation fixture.
      constructor(callback: ResizeObserverCallback) {
        this.callback = callback
        observers.push(this)
      }

      observe = vi.fn((element: Element) => this.elements.push(element)) // Observed-target registration spy.
      disconnect = vi.fn() // Observer teardown call spy.
      unobserve = vi.fn() // Target removal call spy.
    }
    const originalResizeObserver = globalThis.ResizeObserver
    globalThis.ResizeObserver = TrackingResizeObserver as unknown as typeof ResizeObserver

    try {
      const wrapper = mount(DataTable, {
        props: {
          columns: [
            { key: 'name', label: 'Name' },
            { key: 'actions', label: 'Actions' },
          ],
          data: [{ id: 1, name: 'Key' }],
        },
      })
      await wrapper.vm.$nextTick()

      const tableWrapper = wrapper.get('.table-wrapper').element
      const table = wrapper.get('table').element
      const tableObserver = observers.find(
        (observer) => observer.elements.includes(tableWrapper) && observer.elements.includes(table),
      )
      expect(tableObserver).toBeDefined()

      // Keep wrapper width fixed while increasing content width so the callback
      // reproduces asynchronous cells changing overflow independently of outer
      // layout.
      Object.defineProperty(tableWrapper, 'clientWidth', { configurable: true, value: 800 })
      Object.defineProperty(tableWrapper, 'scrollWidth', { configurable: true, value: 1200 })
      tableObserver?.callback([], tableObserver as unknown as ResizeObserver)
      await wrapper.vm.$nextTick()

      expect(wrapper.get('.table-wrapper').classes()).toContain('is-scrollable')
      wrapper.unmount()
    } finally {
      globalThis.ResizeObserver = originalResizeObserver
    }
  })

  // A table mounts with the default sticky configuration. Its wrapper must use
  // the thin separator class and omit the separate shadow-style separator class.
  it('uses a line boundary for sticky columns by default', () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [{ id: 1, name: 'Key' }],
      },
    })

    expect(wrapper.get('.table-wrapper').classes()).toContain('sticky-boundary-line')
    expect(wrapper.get('.table-wrapper').classes()).not.toContain('sticky-boundary-shadow')
  })

  // The record count exceeds an explicitly small virtualization threshold.
  // Windowed rendering must activate and the virtualizer must receive the
  // complete current dataset count.
  it('switches to windowed rendering once row count exceeds virtualizeThreshold', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        virtualizeThreshold: 3,
      },
    })

    await wrapper.vm.$nextTick()

    // The configured threshold enables windowed rendering.
    expect((wrapper.vm as any).shouldVirtualize).toBe(true)
    // The virtualizer receives the complete row count.
    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    expect(instance.options.count).toBe(data.length)
  })

  // Record IDs differ from their array positions in a virtualized dataset. The
  // virtualizer key callback must use those IDs so measurements follow record
  // identity.
  it('keys the virtualizer size cache by row identity, not index (avoids stale heights on sort/filter)', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ id: 100 + i, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        rowKey: 'id',
        virtualizeThreshold: 3,
      },
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    // Measurement keys use stable record identifiers.
    expect(instance.options.getItemKey(0)).toBe(100)
    expect(instance.options.getItemKey(5)).toBe(105)
  })

  // Pagination replaces every row ID with a different page of equal length. Old
  // element and height entries must be removed from the virtualizer caches.
  it('clears stale row and element caches when pagination replaces the row ID set', async () => {
    const firstPage = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, name: `First ${i + 1}` }))
    const secondPage = Array.from({ length: 100 }, (_, i) => ({
      id: i + 101,
      name: `Second ${i + 1}`,
    }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        rowKey: 'id',
        virtualizeThreshold: 1,
      },
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const firstPageIDs = firstPage.map((row) => row.id)
    ;(instance as any).itemSizeCache = new Map(firstPageIDs.map((id) => [id, 156]))
    instance.elementsCache.clear()
    for (const id of firstPageIDs) {
      instance.elementsCache.set(id, document.createElement('tr'))
    }
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    const sizeCache = (instance as any).itemSizeCache as Map<number, number>
    expect(sizeCache.size).toBeLessThanOrEqual(secondPage.length)
    expect(instance.elementsCache.size).toBeLessThanOrEqual(secondPage.length)
    expect(firstPageIDs.some((id) => sizeCache.has(id))).toBe(false)
    expect(firstPageIDs.some((id) => instance.elementsCache.has(id))).toBe(false)
    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  // Equal-length pages replace object records that have no explicit stable keys.
  // The table must clear retained elements because the new object identities
  // represent different rows.
  it('clears stale caches when equal-length pages replace rows without stable keys', async () => {
    const firstPage = Array.from({ length: 12 }, (_, i) => ({ name: `First ${i + 1}` }))
    const secondPage = Array.from({ length: 12 }, (_, i) => ({ name: `Second ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        virtualizeThreshold: 1,
      },
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  // Two pages have the same deduplicated keys but different duplicate
  // multiplicities. The table must invalidate measurements because each cached
  // key cannot identify one distinct row.
  it('conservatively clears caches when duplicate row-key multiplicity changes', async () => {
    const firstPage = [
      { id: 1, name: 'First A' },
      { id: 1, name: 'First B' },
      { id: 2, name: 'First C' },
    ]
    const secondPage = [
      { id: 1, name: 'Second A' },
      { id: 2, name: 'Second B' },
      { id: 2, name: 'Second C' },
    ]
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: firstPage,
        rowKey: 'id',
        virtualizeThreshold: 1,
      },
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureElementSpy = vi.spyOn(instance, 'measureElement')

    await wrapper.setProps({ data: secondPage })
    await wrapper.vm.$nextTick()

    expect(measureElementSpy.mock.calls.some(([node]) => node === null)).toBe(true)
  })

  // The same object records are reversed without explicit row keys. The
  // identity-set comparison must skip the virtualizer's full measurement reset
  // when those object references remain present.
  it('preserves cache when rows without stable keys only reorder the same objects', async () => {
    const data = Array.from({ length: 12 }, (_, i) => ({ name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        virtualizeThreshold: 1,
      },
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    const measureSpy = vi.spyOn(instance, 'measure')

    await wrapper.setProps({ data: [...data].reverse() })
    await wrapper.vm.$nextTick()

    expect(measureSpy).not.toHaveBeenCalled()
  })

  // A dataset with stable IDs is reordered while measured heights exist.
  // Measurement cache entries must survive the reordering and the virtualizer
  // must avoid a full reset.
  it('preserves stable row height cache when the same row IDs are only reordered', async () => {
    const data = Array.from({ length: 100 }, (_, i) => ({ id: i + 1, name: `Row ${i + 1}` }))
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data,
        rowKey: 'id',
        virtualizeThreshold: 1,
      },
    })

    await wrapper.vm.$nextTick()

    const exposed = (wrapper.vm as any).virtualizer
    const instance = exposed?.value ?? exposed
    ;(instance as any).itemSizeCache = new Map(data.map((row) => [row.id, 156]))
    const measureSpy = vi.spyOn(instance, 'measure')

    await wrapper.setProps({ data: [...data].reverse() })
    await wrapper.vm.$nextTick()

    const sizeCache = (instance as any).itemSizeCache as Map<number, number>
    expect(measureSpy).not.toHaveBeenCalled()
    expect(sizeCache.size).toBe(100)
  })

  // Current-page selection begins with a key belonging to another page.
  // Selecting all and then clearing one row must retain the off-page key in both
  // emitted events.
  it('emits controlled current-page selection while preserving off-page keys', async () => {
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [
          { id: 1, name: 'One' },
          { id: 2, name: 'Two' },
        ],
        rowKey: 'id',
        selectable: true,
        selectedKeys: [99],
      },
    })

    await wrapper.get('[data-test="select-all"]').setValue(true)

    const selectedAll = wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]
    expect(selectedAll).toEqual([99, 1, 2])

    await wrapper.setProps({ selectedKeys: selectedAll as number[] })
    const rowCheckboxes = wrapper.findAll<HTMLInputElement>('[data-test="select-row"]')
    expect(rowCheckboxes.every((checkbox) => checkbox.element.checked)).toBe(true)

    await rowCheckboxes[0].setValue(false)

    expect(wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]).toEqual([99, 2])
    expect(wrapper.emitted('selectionChange')?.at(-1)?.[0]).toEqual([99, 2])
  })

  // A mobile card is mounted in a 320-pixel host with a custom usage slot. The
  // field and its content must retain shrinkable width classes and render once.
  it('keeps the single usage field shrinkable in a 320px mobile card', () => {
    stubMobileMatchMedia()
    const viewport = document.createElement('div')
    viewport.style.width = '320px'
    document.body.appendChild(viewport)
    const wrapper = mount(DataTable, {
      attachTo: viewport,
      props: {
        columns: [{ key: 'usage', label: 'Usage' }],
        data: [{ id: 1, usage: 'snapshot' }],
        rowKey: 'id',
      },
      slots: {
        'cell-usage': '<div data-test="usage-cell">snapshot</div>',
      },
    })

    expect(viewport.style.width).toBe('320px')
    expect(wrapper.findAll('[data-field="usage"]')).toHaveLength(1)
    expect(wrapper.find('[data-field="ollama_cloud_usage"]').exists()).toBe(false)
    const field = wrapper.get('[data-field="usage"]')
    expect(field.classes()).toContain('min-w-0')
    expect(field.get('div').classes()).toEqual(expect.arrayContaining(['min-w-0', 'max-w-full']))
    expect(wrapper.findAll('[data-test="usage-cell"]')).toHaveLength(1)

    wrapper.unmount()
    viewport.remove()
  })

  // Mobile selection starts with a key outside the current page. The select-all
  // card control must append visible record keys while retaining that existing
  // selection.
  it('offers current-page select all in the mobile card layout', async () => {
    stubMobileMatchMedia()
    const wrapper = mount(DataTable, {
      props: {
        columns: [{ key: 'name', label: 'Name' }],
        data: [
          { id: 1, name: 'One' },
          { id: 2, name: 'Two' },
        ],
        rowKey: 'id',
        selectable: true,
        selectedKeys: [99],
      },
    })

    await wrapper.get('[data-test="select-all-mobile"]').setValue(true)

    expect(wrapper.emitted('update:selectedKeys')?.at(-1)?.[0]).toEqual([99, 1, 2])
  })
})
