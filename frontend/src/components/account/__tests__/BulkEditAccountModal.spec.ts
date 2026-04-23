import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BulkEditAccountModal from '../BulkEditAccountModal.vue'
import ModelWhitelistSelector from '../ModelWhitelistSelector.vue'
import { adminAPI } from '@/api/admin'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getById: vi.fn(),
      bulkUpdate: vi.fn(),
      checkMixedChannelRisk: vi.fn()
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

function mountModal(extraProps: Record<string, unknown> = {}) {
  return mount(BulkEditAccountModal, {
    props: {
      show: true,
      accountIds: [1, 2],
      selectedPlatforms: ['antigravity'],
      selectedTypes: ['apikey'],
      proxies: [],
      groups: [],
      ...extraProps
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        ConfirmDialog: true,
        Select: {
          props: ['modelValue', 'options'],
          emits: ['update:modelValue'],
          template: `
            <select
              v-bind="$attrs"
              :value="modelValue"
              @change="$emit('update:modelValue', $event.target.value)"
            >
              <option v-for="option in options" :key="option.value" :value="option.value">
                {{ option.label }}
              </option>
            </select>
          `
        },
        ProxySelector: true,
        GroupSelector: true,
        Icon: true
      }
    }
  })
}

function createAccount(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    name: 'test-account',
    platform: 'anthropic',
    type: 'apikey',
    credentials: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides
  } as any
}

describe('BulkEditAccountModal', () => {
  beforeEach(() => {
    vi.mocked(adminAPI.accounts.getById).mockReset()
    vi.mocked(adminAPI.accounts.bulkUpdate).mockReset()
    vi.mocked(adminAPI.accounts.checkMixedChannelRisk).mockReset()

    vi.mocked(adminAPI.accounts.getById).mockImplementation(async (id: number) =>
      createAccount({ id })
    )
    vi.mocked(adminAPI.accounts.bulkUpdate).mockResolvedValue({
      success: 2,
      failed: 0,
      results: []
    } as any)
    vi.mocked(adminAPI.accounts.checkMixedChannelRisk).mockResolvedValue({
      has_risk: false
    } as any)
  })

  it('批量编辑打开时，相同模型白名单会回填到选择器', async () => {
    vi.mocked(adminAPI.accounts.getById)
      .mockResolvedValueOnce(createAccount({
        id: 1,
        credentials: {
          model_whitelist: ['claude-sonnet-4-5', 'claude-opus-4-1']
        }
      }))
      .mockResolvedValueOnce(createAccount({
        id: 2,
        credentials: {
          model_whitelist: ['claude-opus-4-1', 'claude-sonnet-4-5']
        }
      }))

    const wrapper = mountModal({
      show: false,
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey']
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    const selector = wrapper.findComponent(ModelWhitelistSelector)
    expect(selector.exists()).toBe(true)
    expect(selector.props('modelValue')).toEqual(['claude-opus-4-1', 'claude-sonnet-4-5'])
    expect((wrapper.get('#bulk-edit-model-restriction-enabled').element as HTMLInputElement).checked).toBe(false)
  })

  it('批量编辑打开时，不同模型限制配置不会误回填', async () => {
    vi.mocked(adminAPI.accounts.getById)
      .mockResolvedValueOnce(createAccount({
        id: 1,
        credentials: {
          model_whitelist: ['claude-sonnet-4-5']
        }
      }))
      .mockResolvedValueOnce(createAccount({
        id: 2,
        credentials: {
          model_whitelist: ['claude-opus-4-1']
        }
      }))

    const wrapper = mountModal({
      show: false,
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey']
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    const selector = wrapper.findComponent(ModelWhitelistSelector)
    expect(selector.exists()).toBe(true)
    expect(selector.props('modelValue')).toEqual([])
  })

  it('antigravity 白名单包含 Gemini 图片模型且过滤掉普通 GPT 模型', async () => {
    const wrapper = mountModal()
    const selector = wrapper.findComponent(ModelWhitelistSelector)
    expect(selector.exists()).toBe(true)

    await selector.find('div.cursor-pointer').trigger('click')

    expect(wrapper.text()).toContain('gemini-3.1-flash-image')
    expect(wrapper.text()).toContain('gemini-2.5-flash-image')
    expect(wrapper.text()).not.toContain('gpt-5.3-codex')
  })

  it('antigravity 映射预设包含图片映射并过滤 OpenAI 预设', async () => {
    const wrapper = mountModal()

    const mappingTab = wrapper.findAll('button').find((btn) => btn.text().includes('admin.accounts.modelMapping'))
    expect(mappingTab).toBeTruthy()
    await mappingTab!.trigger('click')

    expect(wrapper.text()).toContain('3.1-Flash-Image透传')
    expect(wrapper.text()).toContain('3-Pro-Image→3.1')
    expect(wrapper.text()).not.toContain('GPT-5.3 Codex Spark')
  })

  it('仅勾选模型限制且白名单留空时，应清空 model_mapping 和 model_whitelist 以支持所有模型', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {},
        model_whitelist: []
      }
    })
  })

  it('Antigravity 批量修改白名单模式仍应写入 mapping-only 结构', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['antigravity'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    const selector = wrapper.findComponent(ModelWhitelistSelector)
    await selector.vm.$emit('update:modelValue', ['claude-sonnet-4-5'])
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {
          'claude-sonnet-4-5': 'claude-sonnet-4-5'
        }
      }
    })
  })

  it('包含 Antigravity 的跨平台批量修改不允许提交模型限制', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['antigravity', 'openai'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).not.toHaveBeenCalled()
  })

  it('OpenAI 账号批量编辑可开启自动透传', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-passthrough-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-passthrough-toggle').trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_passthrough: true
      }
    })
  })

  it('OpenAI OAuth 批量编辑应提交 OAuth 专属 WS mode 字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-ws-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-ws-mode-select"]').setValue('passthrough')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_oauth_responses_websockets_v2_mode: 'passthrough',
        openai_oauth_responses_websockets_v2_enabled: true
      }
    })
  })

  it('OpenAI API Key 批量编辑不显示 WS mode 入口', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    expect(wrapper.find('#bulk-edit-openai-ws-mode-enabled').exists()).toBe(false)
  })

  it('OpenAI 账号批量编辑可关闭自动透传', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-openai-passthrough-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_passthrough: false,
        openai_oauth_passthrough: false
      }
    })
  })

  it('开启 OpenAI 自动透传时不再同时提交模型限制', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-passthrough-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-passthrough-toggle').trigger('click')
    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_passthrough: true
      }
    })
    expect(wrapper.text()).toContain('admin.accounts.openai.modelRestrictionDisabledByPassthrough')
  })
})
