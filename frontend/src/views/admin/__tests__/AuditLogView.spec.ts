import { reactive } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AuditLogView from '../AuditLogView.vue'

// AuditLogView.spec.ts checks incoming audit links while Vue reuses the page.
// Route changes must update visible controls and query parameters together, and
// a slower previous request must not replace rows belonging to the new filter.

// Shared fixtures expose the current route and independently controlled API responses.
const api = vi.hoisted(() => ({ list: vi.fn(), get: vi.fn(), clear: vi.fn(), showError: vi.fn() }))
let route: { query: Record<string, string> }
vi.mock('@/api/admin', () => ({ adminAPI: { audit: api } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: api.showError }) }))
vi.mock('vue-router', async (original) => ({
  ...(await original<typeof import('vue-router')>()),
  useRoute: () => route,
}))
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

// page mounts real filter bindings with lightweight shell and selector fixtures.
function page() {
  return mount(AuditLogView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/></div>' },
        FilterDropdown: { template: '<div><slot /></div>' },
        FilterField: { template: '<div><slot /></div>' },
        DataTable: {
          props: ['data'],
          template: '<div data-test="rows"><span v-for="row in data">{{ row.id }}</span></div>',
        },
        BaseDialog: true,
        ConfirmDialog: true,
        Pagination: true,
        ContentSkeleton: true,
        RequestIdLink: true,
        Icon: true,
      },
    },
  })
}

describe('audit route filters', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    route = reactive({ query: { q: 'tr_mcp_first', auth_method: 'mcp_token' } })
    api.list.mockResolvedValue({ items: [], total: 0 })
  })

  // A token link supplies both a prefix and MCP authentication filter. Controls
  // must show those values, then follow another link without remounting the page.
  it('shows MCP authentication and synchronizes reused route controls', async () => {
    const wrapper = page()
    await flushPromises()
    // checkedAuthMethod returns the value of the checked authentication row, or null when
    // that field shows no condition.
    const authField = wrapper.get('[role="option"][data-value="mcp_token"]').element.parentElement!
    const checkedAuthMethod = () =>
      authField.querySelector('[aria-selected="true"]')?.getAttribute('data-value') ?? null
    expect(checkedAuthMethod()).toBe('mcp_token')
    expect(wrapper.get('[role="option"][data-value="mcp_token"]').text()).toBe(
      'admin.mcp.authMethod',
    )
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ q: 'tr_mcp_first', auth_method: 'mcp_token' }),
    )
    route.query = { q: 'tr_mcp_second', auth_method: 'jwt' }
    await flushPromises()
    expect(checkedAuthMethod()).toBe('jwt')
    expect(
      (wrapper.get('input[aria-label="admin.audit.filters.q"]').element as HTMLInputElement).value,
    ).toBe('tr_mcp_second')
    expect(api.list).toHaveBeenLastCalledWith(
      expect.objectContaining({ q: 'tr_mcp_second', auth_method: 'jwt' }),
    )
    route.query = {}
    await flushPromises()
    expect(checkedAuthMethod()).toBeNull()
    wrapper.unmount()
  })

  // The first token's response finishes after a later token's list is visible.
  // The newer rows remain displayed beneath the newer query controls.
  it('ignores an older filter response after navigation', async () => {
    let resolvePrevious!: (value: unknown) => void
    api.list.mockReturnValueOnce(
      new Promise((resolve) => {
        resolvePrevious = resolve
      }),
    )
    const wrapper = page()
    route.query = { q: 'tr_mcp_second' }
    api.list.mockResolvedValueOnce({ items: [{ id: 2 }], total: 1 })
    await flushPromises()
    resolvePrevious({ items: [{ id: 1 }], total: 1 })
    await flushPromises()
    expect(wrapper.get('[data-test="rows"]').text()).toBe('2')
    wrapper.unmount()
  })
})
