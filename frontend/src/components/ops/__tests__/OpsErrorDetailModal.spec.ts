import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpsErrorDetailModal from '../OpsErrorDetailModal.vue'

// OpsErrorDetailModal.spec.ts exercises evidence disclosure, local recovery, and
// selection changes through the dialog's public properties and controls.
// Deferred API results reproduce the stale-record race independently for detail
// and upstream queries, while clipboard assertions protect the exact stored
// text.

// Mock API boundaries let each scenario independently control detail,
// pagination, and clipboard completion while leaving presentation logic in the
// real component.
const mocks = vi.hoisted(() => ({
  getRequestErrorDetail: vi.fn(),
  getUpstreamErrorDetail: vi.fn(),
  listRequestErrorUpstreamErrors: vi.fn(),
  copyToClipboard: vi.fn(),
}))

vi.mock('@/api/admin/ops', () => ({ opsAPI: mocks }))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: mocks.copyToClipboard }),
}))
vi.mock('@/stores', () => ({ useAuthStore: () => ({ isAdmin: false }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key }),
}))

// errorRecord builds a visible error identity with overridable response
// evidence. The default identifier and fields support ordinary request details,
// while overrides create upstream, stale-selection, and copied-payload
// scenarios.
function errorRecord(id = 1, fields: Record<string, unknown> = {}) {
  return {
    id,
    created_at: '2026-10-19T00:00:00Z',
    phase: 'request',
    status_code: 502,
    upstream_status_code: 403,
    platform: 'openai',
    model: 'gpt-5.6',
    request_id: `rid-${id}`,
    message: 'All available providers exhausted',
    error_body: '{"error":"same"}',
    upstream_error_message: 'provider rate limit exhausted',
    upstream_error_detail: '{"error":"same"}',
    upstream_errors: '[]',
    provider_name: 'provider',
    group_name: 'group',
    ...fields,
  }
}

// mountDialog mounts a visible request-error dialog with overridable public
// props. Stubbed dialog slots stay accessible, while disclosure bodies mount
// only after expansion so tests can inspect their initial absence.
function mountDialog(props: Record<string, unknown> = {}) {
  return shallowMount(OpsErrorDetailModal, {
    props: {
      show: true, // dialog fixture visibility flag
      errorId: 1,
      errorType: 'request',
      ...props,
    },
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        Collapse: { props: ['open'], template: '<div v-if="open"><slot /></div>' },
        Icon: true,
      },
    },
  })
}

// deferred exposes a promise and its resolver for controlled response ordering.
// Tests choose when to complete older requests so generation guards can be
// observed after a newer selection loads.
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((complete) => {
    resolve = complete
  })
  return { promise, resolve }
}

describe('OpsErrorDetailModal', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    mocks.getRequestErrorDetail.mockResolvedValue(errorRecord())
    mocks.getUpstreamErrorDetail.mockResolvedValue(errorRecord(1, { message: 'upstream record' }))
    mocks.listRequestErrorUpstreamErrors.mockResolvedValue({ items: [], total: 0 })
    mocks.copyToClipboard.mockResolvedValue(true)
  })

  // Repeated response text is grouped but retains both provenance labels. The
  // first screen shows one summary and copying returns exact original bytes.
  it('starts with collapsed evidence, preserves provenance, and copies the original', async () => {
    const raw = '  {"error":"same"}\n'
    mocks.getRequestErrorDetail.mockResolvedValue(
      errorRecord(1, { error_body: raw, upstream_error_detail: raw }),
    )
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.find('[data-testid="error-summary"]').text()).toContain(
      'provider rate limit exhausted',
    )
    expect(wrapper.findAll('pre')).toHaveLength(0)
    const evidence = wrapper.find('[data-testid="diagnostic-payloads"]')
    expect(evidence.text()).toContain(
      'admin.ops.errorDetail.payloads.client · admin.ops.errorDetail.payloads.upstream_detail',
    )
    const disclosure = evidence.find('button[aria-expanded]')
    await disclosure.trigger('click')
    expect(disclosure.attributes('aria-expanded')).toBe('true')
    expect(wrapper.findAll('pre')).toHaveLength(1)
    expect(wrapper.find('pre').text()).toContain('"error": "same"')
    await evidence.find('button[aria-label]').trigger('click')
    expect(mocks.copyToClipboard).toHaveBeenCalledWith(raw)
    wrapper.unmount()
  })

  // HTML should never dominate the summary or create executable DOM content.
  // Expanding its response section reveals the markup as escaped text for
  // inspection.
  it('keeps HTML out of the summary and renders it as text on expansion', async () => {
    const html = '<html><h1>Blocked</h1><script>danger()</script></html>'
    mocks.getRequestErrorDetail.mockResolvedValue(
      errorRecord(1, { upstream_error_message: html, upstream_error_detail: html }),
    )
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.find('[data-testid="error-summary"]').text()).toContain(
      'admin.ops.errorDetail.summaryFormats.html',
    )
    expect(wrapper.text()).not.toContain('<html>')
    await wrapper
      .findAll('[data-testid="diagnostic-payloads"] button[aria-expanded]')[1]
      .trigger('click')
    expect(wrapper.find('pre').text()).toBe(html)
    expect(wrapper.findAll('script, h1')).toHaveLength(0)
    wrapper.unmount()
  })

  // The attempt query can fail after the main record loads successfully. Its
  // local retry must preserve the summary and distinguish failure from
  // emptiness.
  it('shows and retries attempt failures without replacing the main detail', async () => {
    mocks.listRequestErrorUpstreamErrors.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountDialog()
    await flushPromises()
    const attempts = wrapper.find('[data-testid="upstream-attempts"]')
    expect(attempts.text()).toContain('admin.ops.errorDetail.attemptsLoadFailed')
    expect(attempts.text()).not.toContain('admin.ops.errorDetail.noUpstreamAttempts')
    expect(wrapper.find('[data-testid="error-summary"]').exists()).toBe(true)
    await attempts.find('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(attempts.text()).toContain('admin.ops.errorDetail.noUpstreamAttempts')
    expect(mocks.listRequestErrorUpstreamErrors).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  // A failed main-detail query leaves a recovery action in the same dialog.
  // Retrying retrieves the selected record instead of presenting a
  // missing-selection message.
  it('recovers from a detail failure inside the dialog', async () => {
    mocks.getRequestErrorDetail.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mountDialog()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.ops.failedToLoadErrorDetail')
    expect(wrapper.text()).not.toContain('admin.ops.errorDetail.noErrorSelected')
    await wrapper.find('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="error-summary"]').exists()).toBe(true)
    wrapper.unmount()
  })

  // An older selection can complete after the newly opened error has loaded.
  // Neither its detail nor its attempt list may replace the visible record.
  it('ignores late results after selecting a different error', async () => {
    const oldDetail = deferred<ReturnType<typeof errorRecord>>()
    const oldAttempts = deferred<{
      items: ReturnType<typeof errorRecord>[] // resolved upstream attempt fixtures
      total: number // total matching record count
    }>()
    mocks.getRequestErrorDetail.mockReturnValueOnce(oldDetail.promise)
    mocks.listRequestErrorUpstreamErrors.mockReturnValueOnce(oldAttempts.promise)
    const wrapper = mountDialog()
    mocks.getRequestErrorDetail.mockResolvedValue(
      errorRecord(2, { upstream_error_message: 'new selection' }),
    )
    mocks.listRequestErrorUpstreamErrors.mockResolvedValue({
      items: [errorRecord(20, { provider_name: 'new provider' })],
      total: 1,
    })
    await wrapper.setProps({ errorId: 2 })
    await flushPromises()
    oldDetail.resolve(errorRecord(1, { upstream_error_message: 'stale selection' }))
    oldAttempts.resolve({ items: [errorRecord(10, { provider_name: 'stale provider' })], total: 1 })
    await flushPromises()
    expect(wrapper.find('[data-testid="error-summary"]').text()).toContain('new selection')
    expect(wrapper.text()).toContain('new provider')
    expect(wrapper.text()).not.toContain('stale selection')
    expect(wrapper.text()).not.toContain('stale provider')
    wrapper.unmount()
  })

  // Reusing one record ID across request and upstream categories changes APIs.
  // Closing resets disclosures so reopening never inherits previously expanded
  // payloads.
  it('reloads category changes and resets disclosures on reopen', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.find('[data-testid="diagnostic-payloads"] button[aria-expanded]').trigger('click')
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true, errorType: 'upstream' })
    await flushPromises()
    expect(mocks.getUpstreamErrorDetail).toHaveBeenCalledWith(1)
    expect(wrapper.find('[data-testid="upstream-attempts"]').exists()).toBe(false)
    expect(wrapper.findAll('pre')).toHaveLength(0)
    wrapper.unmount()
  })

  // A failed later page must leave already loaded attempts available. Retrying
  // requests that same page in chronological order before appending results.
  it('retries a failed later page and keeps earlier attempts visible', async () => {
    mocks.listRequestErrorUpstreamErrors
      .mockResolvedValueOnce({
        items: [errorRecord(10, { provider_name: 'first provider' })],
        total: 2,
        pages: 2,
      })
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({
        items: [errorRecord(11, { provider_name: 'next provider' })],
        total: 2,
        pages: 2,
      })
    const wrapper = mountDialog()
    await flushPromises()
    const attempts = wrapper.find('[data-testid="upstream-attempts"]')
    await attempts
      .findAll('button')
      .find((button) => button.text().includes('loadMoreAttempts'))!
      .trigger('click')
    await flushPromises()
    expect(attempts.text()).toContain('first provider')
    await attempts.find('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(attempts.text()).toContain('next provider')
    expect(mocks.listRequestErrorUpstreamErrors).toHaveBeenLastCalledWith(
      1,
      expect.objectContaining({ page: 2, sort_by: 'created_at', sort_order: 'asc' }),
      { include_detail: true },
    )
    wrapper.unmount()
  })

  // Detail expansion may omit rows that disappeared after the server counted
  // them. Exhausted page metadata must stop pagination and explain the missing
  // records.
  it('stops at the final page when some attempt details are unavailable', async () => {
    mocks.listRequestErrorUpstreamErrors.mockResolvedValue({
      items: [],
      total: 2,
      pages: 1,
      page_size: 100,
    })
    const wrapper = mountDialog()
    await flushPromises()
    const attempts = wrapper.find('[data-testid="upstream-attempts"]')
    expect(attempts.text()).toContain('admin.ops.errorDetail.incompleteAttempts')
    expect(attempts.text()).not.toContain('admin.ops.errorDetail.noUpstreamAttempts')
    expect(attempts.text()).not.toContain('admin.ops.errorDetail.loadMoreAttempts')
    wrapper.unmount()
  })

  // Recovered attempts can have a successful client status and failed upstream
  // status. The attempt badge must identify the upstream failure being
  // inspected.
  it('shows the upstream status of recovered attempts', async () => {
    mocks.listRequestErrorUpstreamErrors.mockResolvedValue({
      items: [errorRecord(10, { status_code: 200, upstream_status_code: 429 })],
      total: 1,
    })
    const wrapper = mountDialog()
    await flushPromises()
    const attempt = wrapper.find('[data-testid="upstream-attempts"] li')
    expect(attempt.text()).toContain('429')
    expect(attempt.text()).not.toContain('200')
    wrapper.unmount()
  })
})
