import { describe, it, expect, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ImportDataModal from '@/components/admin/account/ImportDataModal.vue'
import { adminAPI } from '@/api/admin'
import zhMessages from '@/i18n/locales/zh'

const showError = vi.fn()
const showSuccess = vi.fn()
const showWarning = vi.fn()

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess,
    showWarning
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      importData: vi.fn()
    },
    settings: {
      getOpenAIOAuthImportDefaults: vi.fn(),
      updateOpenAIOAuthImportDefaults: vi.fn()
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => key
  })
}))

const makeJsonFile = (name: string, payload: unknown, type = 'application/json') => {
  const content = typeof payload === 'string' ? payload : JSON.stringify(payload)
  const file = new File([content], name, { type })
  Object.defineProperty(file, 'text', {
    value: () => Promise.resolve(content)
  })
  return file
}

const setInputFiles = (element: Element, files: File[]) => {
  Object.defineProperty(element, 'files', {
    value: files,
    configurable: true
  })
}

describe('ImportDataModal', () => {
  const importData = vi.mocked(adminAPI.accounts.importData)

  beforeEach(() => {
    showError.mockReset()
    showSuccess.mockReset()
    showWarning.mockReset()
    importData.mockReset()
  })

  const mountModal = () => {
    return mount(ImportDataModal, {
      props: { show: true },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })
  }

  const successResult = {
    proxy_created: 0,
    proxy_reused: 0,
    proxy_failed: 0,
    account_created: 1,
    account_failed: 0,
    errors: []
  }

  it('未提供导入来源时提示错误', async () => {
    const wrapper = mountModal()

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectSource')
    expect(importData).not.toHaveBeenCalled()
  })

  it('粘贴无效 JSON 时提示解析失败', async () => {
    const wrapper = mountModal()

    await wrapper.find('textarea').setValue('invalid json')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailed')
    expect(importData).not.toHaveBeenCalled()
  })

  it('选择文件中的无效 JSON 时按文件名提示解析失败', async () => {
    const wrapper = mountModal()

    const input = wrapper.find('input[type="file"]')
    setInputFiles(input.element, [makeJsonFile('data.json', 'invalid json')])

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportParseFailedFile')
    expect(importData).not.toHaveBeenCalled()
  })

  it('粘贴有效 JSON 时提交解析后的数据', async () => {
    const wrapper = mountModal()
    const payload = { accounts: [{ name: 'pasted-account' }], proxies: [] }
    importData.mockResolvedValue(successResult)

    await wrapper.find('textarea').setValue(JSON.stringify(payload))
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(importData).toHaveBeenCalledWith({
      data: payload,
      skip_default_group_bind: true
    })
    expect(showSuccess).toHaveBeenCalledWith('admin.accounts.dataImportSuccess')
  })

  it('同时存在文件和粘贴内容时优先使用粘贴内容', async () => {
    const wrapper = mountModal()
    const filePayload = { accounts: [{ name: 'file-account' }], proxies: [] }
    const pastedPayload = { accounts: [{ name: 'pasted-account' }], proxies: [] }
    importData.mockResolvedValue(successResult)

    const input = wrapper.find('input[type="file"]')
    const file = new File([JSON.stringify(filePayload)], 'data.json', { type: 'application/json' })
    Object.defineProperty(file, 'text', {
      value: () => Promise.resolve(JSON.stringify(filePayload))
    })
    Object.defineProperty(input.element, 'files', {
      value: [file]
    })

    await input.trigger('change')
    await wrapper.find('textarea').setValue(JSON.stringify(pastedPayload))
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(importData).toHaveBeenCalledWith({
      data: pastedPayload,
      skip_default_group_bind: true
    })
  })

  it('拒绝不是导出数据结构的 JSON 文件', async () => {
    const wrapper = mountModal()
    const input = wrapper.find('input[type="file"]')
    setInputFiles(input.element, [makeJsonFile('random.json', { name: 'invalid' })])

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportInvalidFile')
    expect(importData).not.toHaveBeenCalled()
  })

  it('无有效 JSON 的新选择不会清空已有文件', async () => {
    const wrapper = mountModal()
    const input = wrapper.find('input[type="file"]')
    const payload = { accounts: [{ name: 'kept-account' }], proxies: [] }
    importData.mockResolvedValue(successResult)

    setInputFiles(input.element, [makeJsonFile('valid.json', payload)])
    await input.trigger('change')
    setInputFiles(input.element, [makeJsonFile('notes.txt', 'hello', 'text/plain')])
    await input.trigger('change')

    expect(showError).toHaveBeenCalledWith('admin.accounts.dataImportSelectFile')

    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(importData).toHaveBeenCalledWith({
      data: payload,
      skip_default_group_bind: true
    })
  })

  it('选择多个 JSON 文件时合并账号、代理和跳过计数', async () => {
    const wrapper = mountModal()
    const input = wrapper.find('input[type="file"]')
    importData.mockResolvedValue({ ...successResult, account_created: 2 })

    setInputFiles(input.element, [
      makeJsonFile('first.json', {
        accounts: [{ name: 'account-a' }],
        proxies: [],
        skipped_shadows: 1
      }),
      makeJsonFile('second.json', {
        accounts: [{ name: 'account-b' }],
        proxies: [{ proxy_key: 'proxy-b' }],
        skipped_shadows: 2
      })
    ])

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(importData).toHaveBeenCalledWith({
      data: expect.objectContaining({
        accounts: [{ name: 'account-a' }, { name: 'account-b' }],
        proxies: [{ proxy_key: 'proxy-b' }],
        skipped_shadows: 3
      }),
      skip_default_group_bind: true
    })
  })

  it('拖入 JSON 文件后可以直接导入', async () => {
    const wrapper = mountModal()
    const payload = { accounts: [{ name: 'dropped-account' }], proxies: [] }
    importData.mockResolvedValue(successResult)

    await wrapper.find('.border-dashed').trigger('drop', {
      dataTransfer: { files: [makeJsonFile('dropped.json', payload)] }
    })
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(importData).toHaveBeenCalledWith({
      data: payload,
      skip_default_group_bind: true
    })
  })

  it('部分成功时在关闭弹窗后通知父组件刷新', async () => {
    const wrapper = mountModal()
    const input = wrapper.find('input[type="file"]')
    importData.mockResolvedValue({
      ...successResult,
      account_created: 1,
      account_failed: 1
    })
    setInputFiles(input.element, [
      makeJsonFile('mixed.json', {
        accounts: [{ name: 'created' }, { name: 'failed' }],
        proxies: []
      })
    ])

    await input.trigger('change')
    await wrapper.find('form').trigger('submit')
    await flushPromises()

    expect(wrapper.emitted('imported')).toBeUndefined()
    await wrapper.findAll('button.btn-secondary')[1]!.trigger('click')
    expect(wrapper.emitted('imported')).toHaveLength(1)
    expect(wrapper.emitted('close')).toHaveLength(1)
  })

  it('粘贴输入框的 JSON 占位符不经过 i18n 消息编译', async () => {
    const actualI18n = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
    const i18n = actualI18n.createI18n({
      legacy: false,
      locale: 'zh',
      messages: {
        zh: zhMessages
      }
    })

    const wrapper = mount(ImportDataModal, {
      props: { show: true },
      global: {
        plugins: [i18n],
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }
        }
      }
    })

    expect(wrapper.find('textarea').attributes('placeholder')).toContain('"accounts"')
  })
})
