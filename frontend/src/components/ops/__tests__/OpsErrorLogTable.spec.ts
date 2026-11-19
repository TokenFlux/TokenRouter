import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import OpsErrorLogTable from '../OpsErrorLogTable.vue'
import zhLocale from '@/i18n/locales/zh'
import enLocale from '@/i18n/locales/en'
import type { OpsErrorLog } from '@/api/admin/ops'

// OpsErrorLogTable.spec.ts checks recorded user, API-key, and provider
// identities in error rows and verifies required locale keys. Mounted table
// output exercises deleted-key presentation, while direct locale inspection
// avoids depending on runtime message compilation in the test environment.

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

// Tooltip and pagination doubles leave rendered row identities visible while
// removing unrelated overlay and paging behavior from assertions.
const TooltipStub = { template: '<div><slot /></div>' }
const PaginationStub = { template: '<div class="pagination-stub" />' }

// mountTable builds a single error row from fixture defaults and caller
// overrides. It mounts the table with controlled pagination and stubbed chrome
// so tests inspect the recorded identities.
function mountTable(row: Partial<OpsErrorLog>) {
  const base = {
    id: 1,
    created_at: '2026-06-05T23:59:50Z',
    phase: 'upstream',
    type: '',
    error_owner: 'provider',
    error_source: 'upstream_http',
    severity: 'error',
    status_code: 529,
    platform: 'anthropic',
    model: 'claude-opus-4-8',
    resolved: false,
    client_request_id: '',
    request_id: 'req-1',
    message: 'boom',
    user_email: '',
    provider_name: '',
    group_name: '',
    ...row,
  } as OpsErrorLog

  return mount(OpsErrorLogTable, {
    props: { rows: [base], total: 1, loading: false, page: 1, pageSize: 20 },
    global: { stubs: { 'el-tooltip': TooltipStub, Pagination: PaginationStub } },
  })
}

describe('OpsErrorLogTable user/api-key/provider columns', () => {
  // An upstream error row includes caller, API-key, and provider identities.
  // Rendered content must expose each identity separately so a provider name
  // cannot replace the recorded caller or key.
  it('renders user, api key and provider in separate columns for an upstream row', () => {
    const wrapper = mountTable({
      user_id: 2,
      user_email: 'alice@test.com',
      api_key_id: 5,
      api_key_name: 'my-key',
      provider_id: 9,
      provider_name: 'acct-A',
    })

    const text = wrapper.text()
    expect(text).toContain('alice@test.com') // recorded caller identity
    expect(text).toContain('my-key') // recorded API-key identity
    expect(text).toContain('acct-A') // upstream provider identity
  })

  // A recorded API key has been soft deleted after the request. Its retained
  // name and deleted marker must both remain visible in the error table output.
  it('shows the deleted badge for a soft-deleted api key', () => {
    const wrapper = mountTable({
      api_key_id: 5,
      api_key_name: 'old-key',
      api_key_deleted: true,
    })

    expect(wrapper.text()).toContain('old-key')
    expect(wrapper.text()).toContain('admin.ops.errorLog.keyDeletedBadge')
  })
})

// The component reads the admin.ops.errorLog namespace, so misplaced keys can
// appear as untranslated paths in rendered labels. The test runtime lacks
// message compilation; direct locale inspection therefore verifies the catalog
// location independently of rendered translations. Inspect the locale objects at
// their owning namespace.
describe('OpsErrorLogTable i18n keys exist in the errorLog namespace', () => {
  const locales: Record<string, any> = { zh: zhLocale, en: enLocale }
  for (const [name, msgs] of Object.entries(locales)) {
    // The table uses API-key labels under the errorLog locale namespace. Each
    // tested language must provide both keys so rendering does not fall back to
    // untranslated paths.
    it(`has apiKey & keyDeletedBadge for ${name}`, () => {
      const errorLog = msgs?.admin?.ops?.errorLog
      expect(errorLog?.apiKey).toBeTruthy()
      expect(errorLog?.keyDeletedBadge).toBeTruthy()
    })
  }
})

beforeEach(() => setActivePinia(createPinia()))
