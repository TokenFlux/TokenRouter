import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import BackupView from '../BackupView.vue'

const {
  getStorageConfig,
  getContentConfig,
  getSchedule,
  listBackups,
  getDownloadURL,
  downloadBackupFile,
  showError,
} = vi.hoisted(() => ({
  getStorageConfig: vi.fn(),
  getContentConfig: vi.fn(),
  getSchedule: vi.fn(),
  listBackups: vi.fn(),
  getDownloadURL: vi.fn(),
  downloadBackupFile: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    backup: {
      getStorageConfig,
      updateStorageConfig: vi.fn(),
      testStorageConnection: vi.fn(),
      getContentConfig,
      updateContentConfig: vi.fn(),
      getSchedule,
      updateSchedule: vi.fn(),
      createBackup: vi.fn(),
      listBackups,
      getBackup: vi.fn(),
      deleteBackup: vi.fn(),
      getDownloadURL,
      downloadBackupFile,
      restoreBackup: vi.fn(),
    },
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/composables/useStepUp', () => ({
  useStepUp: () => ({ run: (fn: () => unknown) => fn() }),
  isStepUpBlocked: () => false,
  isStepUpCancelled: () => false,
  stepUpBlockReason: () => '',
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) =>
      params?.index === undefined ? key : `${key}:${params.index}`,
  }),
}))

function baseRecord(id: string, parts?: unknown[]) {
  return {
    id,
    status: 'completed',
    backup_type: 'postgres',
    file_name: `${id}.sql.gz`,
    s3_key: `backups/${id}.sql.gz`,
    parts,
    size_bytes: 10,
    triggered_by: 'manual',
    started_at: '2026-08-09T00:00:00Z',
  }
}

function mountBackupView() {
  return mount(BackupView, {
    global: {
      stubs: {
        TotpStepUpDialog: true,
        transition: false,
      },
    },
  })
}

function findDownloadButton(wrapper: ReturnType<typeof mountBackupView>) {
  return wrapper.findAll('button').find(button =>
    button.text() === 'admin.backup.actions.download',
  )
}

// 分卷记录即使缺少 fork 的 storage_type，也必须走远程分卷下载契约。
describe('admin BackupView 分卷备份', () => {
  beforeEach(() => {
    getStorageConfig.mockResolvedValue({
      type: 'local',
      local_path: '/data/backups',
      s3: {},
    })
    getContentConfig.mockResolvedValue({})
    getSchedule.mockResolvedValue({ enabled: false, cron_expr: '', retain_days: 14, retain_count: 10 })
    listBackups.mockReset()
    getDownloadURL.mockReset()
    downloadBackupFile.mockReset()
    showError.mockReset()
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('显示分卷数并按顺序列出每个分卷链接', async () => {
    listBackups.mockResolvedValue({
      items: [{ ...baseRecord('split', [{ index: 1 }, { index: 2 }, { index: 3 }]), s3_key: '' }],
    })
    getDownloadURL.mockResolvedValue({
      parts: [
        { index: 1, size_bytes: 5, url: 'https://example.test/part-1' },
        { index: 2, size_bytes: 6, url: 'https://example.test/part-2' },
        { index: 3, size_bytes: 7, url: 'https://example.test/part-3' },
      ],
    })

    const wrapper = mountBackupView()
    await flushPromises()
    const cells = wrapper.findAll('tbody tr').at(0)?.findAll('td') || []
    expect(cells.at(5)?.text()).toBe('3')

    const downloadButton = findDownloadButton(wrapper)
    expect(downloadButton).toBeDefined()
    await downloadButton!.trigger('click')
    await flushPromises()

    expect(downloadBackupFile).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('admin.backup.actions.partLabel:1')
    expect(document.body.textContent).toContain('admin.backup.actions.partLabel:3')
    expect(document.body.querySelector('a[href="https://example.test/part-2"]')).not.toBeNull()
  })

  it('远程单文件记录仍使用单个下载地址', async () => {
    listBackups.mockResolvedValue({ items: [baseRecord('legacy')] })
    getDownloadURL.mockResolvedValue({ url: 'https://example.test/legacy.sql.gz' })

    const wrapper = mountBackupView()
    await flushPromises()
    await findDownloadButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(getDownloadURL).toHaveBeenCalledWith('legacy')
    expect(downloadBackupFile).not.toHaveBeenCalled()
    expect(document.body.textContent).not.toContain('admin.backup.actions.downloadParts')
  })

  it('本地单文件记录继续走鉴权 Blob 下载', async () => {
    listBackups.mockResolvedValue({
      items: [{ ...baseRecord('local'), storage_type: 'local', s3_key: '' }],
    })
    downloadBackupFile.mockResolvedValue(new Blob(['backup']))
    const createObjectURL = vi.fn(() => 'blob:backup')
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })

    const wrapper = mountBackupView()
    await flushPromises()
    await findDownloadButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(downloadBackupFile).toHaveBeenCalledWith('local')
    expect(getDownloadURL).not.toHaveBeenCalled()
    expect(createObjectURL).toHaveBeenCalled()
  })

  it('下载响应缺少 URL 时显示明确错误', async () => {
    listBackups.mockResolvedValue({ items: [baseRecord('missing')] })
    getDownloadURL.mockResolvedValue({})

    const wrapper = mountBackupView()
    await flushPromises()
    await findDownloadButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.backup.actions.downloadFailed')
  })
})
