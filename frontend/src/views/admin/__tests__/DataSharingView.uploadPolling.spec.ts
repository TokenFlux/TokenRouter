import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import DataSharingView from '../DataSharingView.vue'

const adminDataSharingAPI = vi.hoisted(() => ({
  getNotice: vi.fn(),
  getSkipRules: vi.fn(),
  getStorageLimit: vi.fn(),
  getRuntimeSettings: vi.fn(),
  getExportRemoteConfig: vi.fn(),
  getFilterOptions: vi.fn(),
  listSessions: vi.fn(),
  listExportArtifacts: vi.fn(),
  getExportArtifact: vi.fn(),
  getStats: vi.fn(),
  updateNotice: vi.fn(),
  updateSkipRules: vi.fn(),
  updateStorageLimit: vi.fn(),
  updateRuntimeSettings: vi.fn(),
  updateExportRemoteConfig: vi.fn(),
  testExportRemoteConfig: vi.fn(),
  getSession: vi.fn(),
  deleteSession: vi.fn(),
  batchDeleteSessions: vi.fn(),
  createExportArtifact: vi.fn(),
  createSessionExportArtifact: vi.fn(),
  createExportArtifactDownloadTicket: vi.fn(),
  uploadExportArtifact: vi.fn(),
  cancelExportArtifactUpload: vi.fn(),
  getExportArtifactRemoteDownloadURL: vi.fn(),
  deleteExportArtifact: vi.fn()
}))

vi.mock('@/api/admin/dataSharing', () => ({
  adminDataSharingAPI,
  default: adminDataSharingAPI
}))

vi.mock('@/api/dataSharing', () => ({
  dataSharingAPI: { startTicketDownload: vi.fn() },
  default: { startTicketDownload: vi.fn() }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn()
  })
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: '余额',
    formatBalanceAmount: (value: number) => String(value)
  })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))

vi.mock('vue-i18n', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      te: () => false
    })
  }
})

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  CategoryScale: {},
  LinearScale: {},
  PointElement: {},
  LineElement: {},
  BarElement: {},
  ArcElement: {},
  Tooltip: {},
  Legend: {},
  Filler: {}
}))

vi.mock('vue-chartjs', () => ({
  Bar: { template: '<div />' },
  Doughnut: { template: '<div />' },
  Line: { template: '<div />' }
}))

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" data-test="data-table-row">
        <slot name="cell-remote_status" :row="row" :value="row.remote_status" />
        <slot name="cell-actions" :row="row" :value="row.actions" />
      </div>
    </div>
  `
}

function createArtifact(overrides: Record<string, unknown> = {}) {
  return {
    id: 42,
    status: 'completed',
    filename: 'export.jsonl.zst',
    encoding: 'zstd',
    session_count: 100,
    file_size: 100_000_000,
    sha256: 'abc123',
    error_message: '',
    remote_status: 'uploading',
    remote_bucket: '',
    remote_key: '',
    remote_error_message: '',
    remote_uploaded_at: null,
    remote_upload_bytes: 0,
    remote_upload_speed: 0,
    generate_progress_done: 100,
    generate_progress_total: 100,
    generate_progress_percent: 100,
    created_at: '2026-07-14T00:00:00Z',
    started_at: '2026-07-14T00:00:00Z',
    completed_at: '2026-07-14T00:01:00Z',
    deleted_at: null,
    updated_at: '2026-07-14T00:01:00Z',
    ...overrides
  }
}

function mountView() {
  return mount(DataSharingView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /><slot /></div>'
        },
        DataTable: DataTableStub,
        Pagination: true,
        Select: true,
        EmptyState: true,
        BaseDialog: true,
        ConfirmDialog: true,
        LoadingSpinner: true,
        Icon: true,
        Teleport: true
      }
    }
  })
}

function setDocumentHidden(hidden: boolean) {
  Object.defineProperty(document, 'hidden', {
    configurable: true,
    value: hidden
  })
}

describe('admin DataSharingView export upload polling', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setDocumentHidden(false)
    Object.values(adminDataSharingAPI).forEach(mock => mock.mockReset())

    adminDataSharingAPI.getNotice.mockResolvedValue({ content: '', version: 1 })
    adminDataSharingAPI.getSkipRules.mockResolvedValue([])
    adminDataSharingAPI.getStorageLimit.mockResolvedValue({ limit_bytes: 0 })
    adminDataSharingAPI.getRuntimeSettings.mockResolvedValue({
      worker_count: 1,
      queue_size: 1,
      flush_queue_size: 1,
      task_timeout_seconds: 1,
      compression_level: 'fastest',
      buffer_enabled: true,
      buffer_idle_flush_seconds: 30,
      buffer_max_sessions: 4096,
      buffer_max_pending_events: 65536,
      duration_window_size: 512,
      export_batch_size: 500,
      export_worker_count: 4
    })
    adminDataSharingAPI.getExportRemoteConfig.mockResolvedValue({
      endpoint: '',
      region: 'auto',
      bucket: '',
      access_key_id: '',
      prefix: 'data-sharing-exports',
      force_path_style: false,
      upload_concurrency: 4,
      upload_part_size_mb: 64
    })
    adminDataSharingAPI.getFilterOptions.mockResolvedValue({ models: [], request_paths: [], user_agents: [] })
    adminDataSharingAPI.listSessions.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 1 })
    adminDataSharingAPI.getStats.mockResolvedValue({
      storage_trend: [],
      group_storage_breakdown: [],
      request_path_breakdown: [],
      model_breakdown: [],
      user_agent_breakdown: [],
      quality_error_breakdown: [],
      invalid_user_breakdown: []
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    delete (document as Document & { hidden?: boolean }).hidden
  })

  it('starts polling after upload and refreshes until the terminal status is returned', async () => {
    adminDataSharingAPI.listExportArtifacts.mockResolvedValue({
      items: [createArtifact({ remote_status: 'not_uploaded' })],
      total: 1,
      page: 1,
      page_size: 10,
      pages: 1
    })
    adminDataSharingAPI.uploadExportArtifact.mockResolvedValue(createArtifact())
    adminDataSharingAPI.getExportArtifact
      .mockResolvedValueOnce(createArtifact({ remote_upload_bytes: 25_000_000, remote_upload_speed: 5_000_000 }))
      .mockResolvedValueOnce(createArtifact({ remote_status: 'uploaded', remote_bucket: 'bucket-a', remote_key: 'exports/file.zst' }))

    const wrapper = mountView()
    await flushPromises()

    const uploadButton = wrapper.findAll('button').find(button => button.text().trim() === '上传')
    expect(uploadButton).toBeDefined()
    await uploadButton!.trigger('click')
    await flushPromises()

    expect(adminDataSharingAPI.uploadExportArtifact).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('0.00% · 0.00 MB/s')

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(adminDataSharingAPI.getExportArtifact).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('25.0% · 5.00 MB/s')

    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(wrapper.text()).toContain('已上传')
    expect(adminDataSharingAPI.getExportArtifact).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(6000)
    expect(adminDataSharingAPI.getExportArtifact).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('does not overlap upload status requests when one poll is still pending', async () => {
    adminDataSharingAPI.listExportArtifacts.mockResolvedValue({
      items: [createArtifact()],
      total: 1,
      page: 1,
      page_size: 10,
      pages: 1
    })
    let resolveFirstPoll: (artifact: ReturnType<typeof createArtifact>) => void = () => {}
    adminDataSharingAPI.getExportArtifact
      .mockReturnValueOnce(new Promise(resolve => {
        resolveFirstPoll = resolve
      }))
      .mockResolvedValueOnce(createArtifact({ remote_status: 'uploaded' }))

    const wrapper = mountView()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(2000)
    await vi.advanceTimersByTimeAsync(6000)
    expect(adminDataSharingAPI.getExportArtifact).toHaveBeenCalledTimes(1)

    resolveFirstPoll(createArtifact({ remote_upload_bytes: 10_000_000, remote_upload_speed: 2_000_000 }))
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()

    expect(adminDataSharingAPI.getExportArtifact).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('pauses while hidden, resumes when visible, and stops after unmount', async () => {
    adminDataSharingAPI.listExportArtifacts.mockResolvedValue({
      items: [createArtifact()],
      total: 1,
      page: 1,
      page_size: 10,
      pages: 1
    })
    adminDataSharingAPI.getExportArtifact.mockResolvedValue(createArtifact({
      remote_upload_bytes: 10_000_000,
      remote_upload_speed: 2_000_000
    }))

    const wrapper = mountView()
    await flushPromises()

    setDocumentHidden(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(4000)
    expect(adminDataSharingAPI.getExportArtifact).not.toHaveBeenCalled()

    setDocumentHidden(false)
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    await vi.advanceTimersByTimeAsync(2000)
    await flushPromises()
    expect(adminDataSharingAPI.getExportArtifact).toHaveBeenCalledTimes(1)

    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(4000)
    expect(adminDataSharingAPI.getExportArtifact).toHaveBeenCalledTimes(1)
  })
})
