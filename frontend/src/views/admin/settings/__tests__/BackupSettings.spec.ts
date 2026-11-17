import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import BackupSettings from '../BackupSettings.vue'
import {
  createSettingsSaveRegistry,
  settingsSaveRegistryKey,
} from '@/composables/useSettingsSaveRegistry'

/**
 * BackupSettings.spec.ts checks backup download routing and independent
 * schedule persistence through the mounted settings panel. Mocked backup
 * endpoints distinguish split archives, remote single files, and authenticated
 * local Blobs, while an injected save registry verifies that changing
 * retention submits the schedule target alone.
 */

// Hoisted backup API mocks distinguish download paths and independent save
// endpoints while keeping tests isolated from server and storage operations.
const {
  getStorageConfig,
  getContentConfig,
  getSchedule,
  listBackups,
  getDownloadURL,
  downloadBackupFile,
  showError,
  updateSchedule,
  updateStorageConfig,
  updateContentConfig,
} = vi.hoisted(() => ({
  getStorageConfig: vi.fn(),
  getContentConfig: vi.fn(),
  getSchedule: vi.fn(),
  listBackups: vi.fn(),
  getDownloadURL: vi.fn(),
  downloadBackupFile: vi.fn(),
  showError: vi.fn(),
  updateSchedule: vi.fn(),
  updateStorageConfig: vi.fn(),
  updateContentConfig: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAPI: {
    backup: {
      getStorageConfig,
      updateStorageConfig,
      testStorageConnection: vi.fn(),
      getContentConfig,
      updateContentConfig,
      getSchedule,
      updateSchedule,
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

/**
 * baseRecord creates a completed remote backup fixture with an optional parts
 * list. It omits storage_type to exercise older record responses, and callers
 * override selected fields to represent local storage or split archives.
 */
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

/**
 * mountBackupSettings mounts the panel with the step-up dialog stubbed and
 * transitions disabled. The returned wrapper belongs to the test, while API
 * and translation mocks supply deterministic record state and action labels.
 */
function mountBackupSettings() {
  return mount(BackupSettings, {
    global: {
      stubs: {
        TotpStepUpDialog: true,
        transition: false,
      },
    },
  })
}

/**
 * findDownloadButton locates the first download action by its translation-key
 * label. It returns undefined when no matching button exists, allowing callers
 * to assert presence before clicking a populated backup row.
 */
function findDownloadButton(wrapper: ReturnType<typeof mountBackupSettings>) {
  return wrapper
    .findAll('button')
    .find((button) => button.text() === 'admin.backup.actions.download')
}

// Split archive metadata selects remote part URLs even when older records
// omit storage_type and do not expose an S3 key.
describe('admin BackupSettings 分卷备份', () => {
  beforeEach(() => {
    getStorageConfig.mockResolvedValue({
      type: 'local',
      local_path: '/data/backups',
      s3: {},
    })
    getContentConfig.mockResolvedValue({})
    getSchedule.mockResolvedValue({
      enabled: false,
      cron_expr: '',
      retain_days: 14,
      retain_count: 10,
    })
    listBackups.mockReset()
    getDownloadURL.mockReset()
    downloadBackupFile.mockReset()
    showError.mockReset()
    // Prevent browser navigation while preserving the component's anchor
    // creation and click path for assertions on download endpoint selection.
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
    // Clear teleported download dialogs between cases so a previous archive
    // part list cannot satisfy later DOM assertions by accident.
    document.body.innerHTML = ''
  })

  // Backup history can finish independently of configuration. Pending history
  // keeps the headers and reserves rows until a successful response confirms
  // there are no records to list.
  it('shows an empty backup history only after the first read succeeds', async () => {
    let completeHistory!: (response: { items: [] }) => void
    listBackups.mockReturnValueOnce(
      new Promise((resolve) => {
        completeHistory = resolve
      }),
    )
    const wrapper = mountBackupSettings()
    await flushPromises()

    const table = wrapper.get('table')
    expect(table.find('thead').exists()).toBe(true)
    expect(table.find('tbody[data-loading-skeleton]').exists()).toBe(true)
    expect(table.text()).not.toContain('admin.backup.empty')

    completeHistory({ items: [] })
    await flushPromises()
    expect(table.find('tbody[data-loading-skeleton]').exists()).toBe(false)
    expect(table.text()).toContain('admin.backup.empty')
    wrapper.unmount()
  })

  // A split record omits storage_type and its S3 key. Download must show the
  // part count and individual remote links while bypassing the authenticated
  // single-file endpoint.
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

    const wrapper = mountBackupSettings()
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

  // A legacy remote record has one archive and no parts list. Download must
  // request its signed URL and keep the multipart link dialog closed.
  it('远程单文件记录仍使用单个下载地址', async () => {
    listBackups.mockResolvedValue({ items: [baseRecord('legacy')] })
    getDownloadURL.mockResolvedValue({ url: 'https://example.test/legacy.sql.gz' })

    const wrapper = mountBackupSettings()
    await flushPromises()
    await findDownloadButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(getDownloadURL).toHaveBeenCalledWith('legacy')
    expect(downloadBackupFile).not.toHaveBeenCalled()
    expect(document.body.textContent).not.toContain('admin.backup.actions.downloadParts')
  })

  // An explicitly local record contains no remote key or archive parts.
  // Download must fetch an authenticated Blob and create its object URL
  // without requesting remote download links.
  it('本地单文件记录继续走鉴权 Blob 下载', async () => {
    listBackups.mockResolvedValue({
      items: [{ ...baseRecord('local'), storage_type: 'local', s3_key: '' }],
    })
    downloadBackupFile.mockResolvedValue(new Blob(['backup']))
    const createObjectURL = vi.fn(() => 'blob:backup')
    Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: createObjectURL })
    Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: vi.fn() })

    const wrapper = mountBackupSettings()
    await flushPromises()
    await findDownloadButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(downloadBackupFile).toHaveBeenCalledWith('local')
    expect(getDownloadURL).not.toHaveBeenCalled()
    expect(createObjectURL).toHaveBeenCalled()
  })

  // A remote download endpoint returns neither parts nor a URL. The mounted
  // action must report the download failure message rather than trying to
  // navigate an empty address.
  it('下载响应缺少 URL 时显示明确错误', async () => {
    listBackups.mockResolvedValue({ items: [baseRecord('missing')] })
    getDownloadURL.mockResolvedValue({})

    const wrapper = mountBackupSettings()
    await flushPromises()
    await findDownloadButton(wrapper)!.trigger('click')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.backup.actions.downloadFailed')
  })
})

// Storage, content, and schedule register separate dirty snapshots so the
// shared save bar submits only the changed configuration group.
describe('admin BackupSettings 保存', () => {
  beforeEach(() => {
    getStorageConfig.mockResolvedValue({ type: 'local', local_path: '/data/backups', s3: {} })
    getContentConfig.mockResolvedValue({})
    getSchedule.mockResolvedValue({
      enabled: true,
      cron_expr: '0 2 * * *',
      retain_days: 14,
      retain_count: 10,
    })
    listBackups.mockResolvedValue({ items: [] })
    updateSchedule.mockReset().mockResolvedValue(undefined)
    updateStorageConfig.mockReset()
    updateContentConfig.mockReset()
  })

  // Changing schedule retention marks the shared registry dirty after initial
  // configuration loads. Saving must call only the schedule endpoint and
  // clear the registry once that request succeeds.
  it('只修改定时备份时只提交定时备份配置', async () => {
    const saveRegistry = createSettingsSaveRegistry()
    const wrapper = mount(BackupSettings, {
      global: {
        stubs: { TotpStepUpDialog: true, transition: false },
        provide: { [settingsSaveRegistryKey as symbol]: saveRegistry.registry },
      },
    })
    await flushPromises()
    expect(saveRegistry.dirty.value).toBe(false)

    await wrapper.get('#backup-schedule-retain-days').setValue('30')
    expect(saveRegistry.dirty.value).toBe(true)

    await expect(saveRegistry.saveDirty()).resolves.toBe(true)

    expect(updateSchedule).toHaveBeenCalledWith(expect.objectContaining({ retain_days: 30 }))
    expect(updateStorageConfig).not.toHaveBeenCalled()
    expect(updateContentConfig).not.toHaveBeenCalled()
    expect(saveRegistry.dirty.value).toBe(false)
  })
})
