import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'

// ModelWhitelistSelector.spec.ts tests model selection, clipboard actions, upstream
// synchronization, and paginated catalog suggestions. Controlled promises and fake
// timers reproduce changed searches while explicit candidate tests distinguish omitted
// model data from a deliberately empty list.

// The dependency mocks control catalog and upstream model results, clipboard writes,
// and notification feedback. Each case resets their call history before scheduling its
// request responses.
const {
  catalogDefaults,
  syncUpstreamModels,
  syncUpstreamModelsPreview,
  showError,
  showInfo,
  showSuccess,
  copyToClipboard,
} = vi.hoisted(() => ({
  catalogDefaults: vi.fn(),
  syncUpstreamModels: vi.fn(),
  syncUpstreamModelsPreview: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
  showSuccess: vi.fn(),
  copyToClipboard: vi.fn().mockResolvedValue(true),
}))

vi.mock('@/api/admin/providers', () => ({
  providersAPI: {
    syncUpstreamModels,
    syncUpstreamModelsPreview,
  },
}))

vi.mock('@/api/admin/modelAttributes', () => ({
  modelAttributesAPI: { defaults: catalogDefaults },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showInfo,
    showSuccess,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

// mountSelector mounts the selector with default props and caller overrides for the
// current case. Dependency mocks remain controlled by the test, and callers own
// unmounting and any timer cleanup.
function mountSelector(props: Record<string, unknown> = {}) {
  return mount(ModelWhitelistSelector, {
    props: {
      modelValue: [],
      platform: 'openai',
      models: ['gpt-5.6-sol'],
      ...props,
    },
    global: {
      stubs: {
        ModelIcon: true,
        Icon: true,
      },
    },
  })
}

// syncButton finds the upstream synchronization action rendered for the mounted selector.
function syncButton(wrapper: ReturnType<typeof mountSelector>) {
  const button = wrapper
    .findAll('button')
    .find((item) => item.text().includes('admin.providers.syncUpstreamModels'))
  expect(button).toBeTruthy()
  return button!
}

describe('ModelWhitelistSelector', () => {
  // Restore real timers after each catalog scenario so a failing assertion cannot leave
  // later tests running under this case fake clock.
  afterEach(() => {
    vi.useRealTimers()
  })
  beforeEach(() => {
    catalogDefaults.mockReset()
    syncUpstreamModels.mockReset()
    syncUpstreamModelsPreview.mockReset()
    showError.mockReset()
    showInfo.mockReset()
    showSuccess.mockReset()
    copyToClipboard.mockReset()
    copyToClipboard.mockResolvedValue(true)
  })

  // A model suggestion exposes a dedicated copy action beside its selection control.
  // Copying must pass the exact ID to the clipboard without emitting a selection update.
  it('复制模型 ID 时不会选中模型', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = wrapper
      .findAll('[data-testid="model-option"]')
      .find((candidate) => candidate.text().includes('gpt-5.6-sol'))
    expect(row).toBeTruthy()

    const copyButton = row!.get('[data-testid="copy-model-id"]')
    expect(copyButton.attributes('aria-label')).toBe('common.copy gpt-5.6-sol')
    await copyButton.trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.6-sol')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  // The administrator activates a model suggestion through its selection control. The
  // component must emit that selected ID and leave the clipboard action unused in this
  // interaction.
  it('模型选择行为保持不变', async () => {
    const wrapper = mountSelector()
    await wrapper.get('div.cursor-pointer').trigger('click')

    const row = wrapper
      .findAll('[data-testid="model-option"]')
      .find((candidate) => candidate.text().includes('gpt-5.6-sol'))
    expect(row).toBeTruthy()
    await row!.get('[data-testid="select-model"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[['gpt-5.6-sol']]])
    expect(copyToClipboard).not.toHaveBeenCalled()
  })

  // A create form supplies preview credentials and the upstream response repeats a model
  // ID. Synchronization must use the preview endpoint and emit a deduplicated selection
  // with success feedback.
  it('创建提供商时使用临时凭证同步上游模型', async () => {
    syncUpstreamModelsPreview.mockResolvedValue({ models: ['gpt-5.1', 'o3', 'gpt-5.1'] })
    const syncCredentials = {
      platform: 'openai',
      type: 'apikey',
      base_url: 'https://openai.example.com/v1',
      api_key: 'openai-key',
    }
    const wrapper = mountSelector({ syncCredentials })

    const button = wrapper
      .findAll('button')
      .find((item) => item.text().includes('admin.providers.syncUpstreamModels'))
    expect(button).toBeTruthy()
    await button!.trigger('click')

    expect(syncUpstreamModelsPreview).toHaveBeenCalledWith(syncCredentials, expect.any(AbortSignal))
    expect(syncUpstreamModels).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual(['gpt-5.1', 'o3'])
    expect(showSuccess).toHaveBeenCalled()
  })

  // An edit form supplies both a saved provider ID and preview credentials.
  // Synchronization must prefer the saved-provider endpoint and merge its model result
  // into the emitted selection.
  it('编辑提供商时仍使用提供商 ID 同步上游模型', async () => {
    syncUpstreamModels.mockResolvedValue({ models: ['claude-sonnet-4-5'] })
    const wrapper = mountSelector({
      platform: 'anthropic',
      providerId: 7,
      syncCredentials: {
        platform: 'anthropic',
        type: 'apikey',
        api_key: 'should-not-use',
      },
    })

    const button = wrapper
      .findAll('button')
      .find((item) => item.text().includes('admin.providers.syncUpstreamModels'))
    expect(button).toBeTruthy()
    await button!.trigger('click')

    expect(syncUpstreamModels).toHaveBeenCalledWith(7, expect.any(AbortSignal))
    expect(syncUpstreamModelsPreview).not.toHaveBeenCalled()
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual(['claude-sonnet-4-5'])
  })

  // Manual Qoder sync adds every new supported ID once and retains custom selections.
  // The accepted observation also reaches the parent for rendering account capabilities.
  it('merges manually synchronized Qoder candidates into the whitelist', async () => {
    const result = {
      models: ['qwen4-max', 'cantus'],
      sync_status: {
        state: 'fresh',
        model_details: [
          { key: 'qmodel_latest', alias: 'qwen4-max', enabled: true },
          { key: 'cmodel', alias: 'cantus', enabled: true },
        ],
      },
    }
    syncUpstreamModels.mockResolvedValue(result)
    const wrapper = mountSelector({
      platform: 'qoder',
      providerId: 8,
      modelValue: ['retired-model', 'qwen4-max', 'qmodel_latest'],
    })
    await syncButton(wrapper).trigger('click')
    await flushPromises()
    expect(syncUpstreamModels).toHaveBeenCalledWith(8, expect.any(AbortSignal))
    expect(wrapper.emitted('update:modelValue')).toEqual([
      [['retired-model', 'qwen4-max', 'cantus']],
    ])
    expect(wrapper.emitted('synced')).toEqual([[result]])
    expect(wrapper.text()).toContain('admin.providers.qoder.modelsNotObserved')
    expect(showSuccess).toHaveBeenCalled()
    wrapper.unmount()
  })

  // An OpenCode create form previews models with its drafted service mode. The sync action
  // must be offered for that platform, forward provider_mode, and report the response's
  // protocol suggestions through the synced event before merging the model IDs.
  it('reports synchronized protocol suggestions for OpenCode previews', async () => {
    const result = {
      models: ['custom', 'new'],
      model_protocols: { custom: 'openai_chat_completions', new: 'anthropic_messages' },
    }
    syncUpstreamModelsPreview.mockResolvedValue(result)
    const syncCredentials = {
      platform: 'opencode',
      type: 'apikey',
      api_key: 'oc-key',
      provider_mode: 'go',
    }
    const wrapper = mountSelector({ platform: 'opencode', syncCredentials })

    await syncButton(wrapper).trigger('click')
    await flushPromises()

    expect(syncUpstreamModelsPreview).toHaveBeenCalledWith(syncCredentials, expect.any(AbortSignal))
    expect(wrapper.emitted('synced')).toEqual([[result]])
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual(['custom', 'new'])
  })

  // Preview credentials change and then return to their first value while the first
  // request is pending. Cancellation must abort that request and drop its response, even
  // though the final credentials match the ones it was sent with.
  it('discards a synchronization whose credentials changed while pending', async () => {
    let finish!: (value: unknown) => void
    syncUpstreamModelsPreview.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const first = { platform: 'openai', type: 'apikey', api_key: 'first' }
    const wrapper = mountSelector({ syncCredentials: first })

    await syncButton(wrapper).trigger('click')
    const signal = syncUpstreamModelsPreview.mock.calls[0]![1] as AbortSignal
    await wrapper.setProps({ syncCredentials: { ...first, api_key: 'second' } })
    await wrapper.setProps({ syncCredentials: { ...first } })
    expect(signal.aborted).toBe(true)
    finish({ models: ['stale'], model_protocols: { stale: 'systemone' } })
    await flushPromises()

    expect(wrapper.emitted('synced')).toBeUndefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(showError).not.toHaveBeenCalled()
    expect(syncButton(wrapper).attributes('disabled')).toBeUndefined()
  })

  // A saved-provider synchronization completes after the form closes. The response must
  // not reach the parent, so a closed dialog cannot receive models or suggestions.
  it('discards synchronization results after unmount', async () => {
    let finish!: (value: unknown) => void
    syncUpstreamModels.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const wrapper = mountSelector({ platform: 'opencode', providerId: 44 })

    await syncButton(wrapper).trigger('click')
    const signal = syncUpstreamModels.mock.calls[0]![1] as AbortSignal
    wrapper.unmount()
    finish({ models: ['stale'] })
    await flushPromises()

    expect(signal.aborted).toBe(true)
    expect(wrapper.emitted('synced')).toBeUndefined()
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  // The caller supplies a model unknown to the catalog and later supplies an empty list.
  // Explicit candidates must display as supplied and must never trigger catalog loading.
  it('显式候选包含目录未知型号，空候选保持为空', async () => {
    const wrapper = mountSelector({ models: ['custom/unknown-model'] })
    await wrapper.get('div.cursor-pointer').trigger('click')
    expect(wrapper.get('[data-testid="model-option"]').text()).toContain('custom/unknown-model')
    expect(catalogDefaults).not.toHaveBeenCalled()
    await wrapper.setProps({ models: [] })
    expect(wrapper.findAll('[data-testid="model-option"]')).toHaveLength(0)
    wrapper.unmount()
  })

  // Bulk editing can select Qoder alongside another platform without supplying a
  // single account catalog. Shared catalog suggestions must still be searchable.
  it('loads catalog suggestions for a mixed Qoder and OpenAI bulk selection', async () => {
    vi.useFakeTimers()
    catalogDefaults.mockResolvedValue({ items: [{ model: 'gpt-example' }], total: 1 })
    const wrapper = mountSelector({ models: undefined, platforms: ['qoder', 'openai'] })
    await wrapper.get('div.cursor-pointer').trigger('click')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(wrapper.get('[data-testid="model-option"]').text()).toBe('gpt-example')
    wrapper.unmount()
  })

  // A search changes while the previous catalog request remains unresolved, then another
  // page is loaded. Only current results may populate suggestions while the saved
  // selected ID remains untouched.
  it('目录分页只加载候选，搜索以最新响应为准', async () => {
    vi.useFakeTimers()
    let resolveOld!: (value: unknown) => void
    catalogDefaults.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOld = resolve
      }),
    )
    const wrapper = mountSelector({ models: undefined, modelValue: ['saved-custom'] })
    await wrapper.get('div.cursor-pointer').trigger('click')
    await vi.advanceTimersByTimeAsync(300)
    catalogDefaults.mockResolvedValueOnce({ items: [{ model: 'new-model' }], total: 2 })
    await wrapper.get('input[placeholder="admin.providers.searchModels"]').setValue('new')
    await vi.advanceTimersByTimeAsync(300)
    resolveOld({ items: [{ model: 'stale-model' }], total: 1 })
    await flushPromises()
    expect(wrapper.findAll('[data-testid="model-option"]').map((row) => row.text())).toEqual([
      'new-model',
    ])
    catalogDefaults.mockResolvedValueOnce({ items: [{ model: 'new-second' }], total: 2 })
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.providers.loadMoreModels')!
      .trigger('click')
    await flushPromises()
    expect(catalogDefaults).toHaveBeenLastCalledWith({ search: 'new', page: 2, page_size: 50 })
    expect(wrapper.findAll('[data-testid="model-option"]')).toHaveLength(2)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(wrapper.text()).toContain('saved-custom')
    wrapper.unmount()
  })

  // The first catalog lookup fails while a manual identifier is available to add. The
  // editor must allow that addition and recover its suggestions when the retry succeeds.
  it('目录失败可以重试，仍可手动添加型号', async () => {
    vi.useFakeTimers()
    catalogDefaults.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mountSelector({ models: undefined })
    await wrapper.get('div.cursor-pointer').trigger('click')
    await vi.advanceTimersByTimeAsync(300)
    const retry = wrapper.findAll('button').find((button) => button.text() === 'common.retry')
    expect(retry).toBeTruthy()
    await wrapper
      .get('input[placeholder="admin.providers.enterCustomModelName"]')
      .setValue('custom-model')
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.providers.addModel')!
      .trigger('click')
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toEqual(['custom-model'])
    catalogDefaults.mockResolvedValueOnce({ items: [{ model: 'recovered' }], total: 1 })
    await retry!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="model-option"]').text()).toContain('recovered')
    wrapper.unmount()
  })
})
