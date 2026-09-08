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
    },
    tlsFingerprintProfiles: {
      list: vi.fn()
    },
    tlsFingerprintRouters: {
      list: vi.fn()
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

function coerceSelectStubValue(value: string, options: unknown[]): string | number | boolean | null {
  const option = (options as Array<Record<string, unknown>>).find((item) => String(item.value ?? '') === value)
  return option ? (option.value as string | number | boolean | null) : value
}

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
          emits: ['update:modelValue', 'change'],
          methods: {
            coerceSelectStubValue
          },
          template: `
            <select
              v-bind="$attrs"
              :value="modelValue"
              @change="
                (event) => {
                  const value = coerceSelectStubValue(event.target.value, options)
                  const option = options.find((item) => String(item.value ?? '') === event.target.value) ?? null
                  $emit('update:modelValue', value)
                  $emit('change', value, option)
                }
              "
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
    vi.mocked(adminAPI.tlsFingerprintProfiles.list).mockReset()
    vi.mocked(adminAPI.tlsFingerprintRouters.list).mockReset()

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
    vi.mocked(adminAPI.tlsFingerprintProfiles.list).mockResolvedValue([
      { id: 7, name: 'Profile 7' }
    ] as any)
    vi.mocked(adminAPI.tlsFingerprintRouters.list).mockResolvedValue([])
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

  it.each(['kimi', 'zhipu', 'deepseek'])('全部目标为 %s API Key 时展示请求头覆写', (platform) => {
    const wrapper = mountModal({
      selectedPlatforms: [platform],
      selectedTypes: ['apikey']
    })

    expect(wrapper.find('#bulk-edit-header-override-enabled').exists()).toBe(true)
  })

  it.each(['kimi', 'zhipu', 'deepseek'])('目标为 %s OAuth 时不展示请求头覆写', (platform) => {
    const wrapper = mountModal({
      selectedPlatforms: [platform],
      selectedTypes: ['oauth']
    })

    expect(wrapper.find('#bulk-edit-header-override-enabled').exists()).toBe(false)
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

  it('Qoder 批量编辑应把旧 self mapping 保留为显式映射', async () => {
    vi.mocked(adminAPI.accounts.getById)
      .mockResolvedValueOnce(createAccount({
        id: 1,
        platform: 'qoder',
        type: 'cosy',
        credentials: {
          model_mapping: {
            ultimate: 'ultimate'
          }
        }
      }))
      .mockResolvedValueOnce(createAccount({
        id: 2,
        platform: 'qoder',
        type: 'cosy',
        credentials: {
          model_mapping: {
            ultimate: 'ultimate'
          }
        }
      }))

    const wrapper = mountModal({
      show: false,
      selectedPlatforms: ['qoder'],
      selectedTypes: ['cosy']
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {
          ultimate: 'ultimate'
        },
        model_whitelist: []
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

  it('全部目标为 Grok OAuth 时，官方主机 base_url 作为手动端点切换正常提交', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-base-url-enabled').setValue(true)
    await wrapper.get('#bulk-edit-base-url').setValue('https://api.x.ai/v1')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://api.x.ai/v1'
      }
    })
  })

  it('所选全为 grok 时展示快捷端点，点击后填入并自动勾选 base_url', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['oauth']
    })

    const presets = wrapper.findAll('[data-testid="grok-base-url-preset"]')
    expect(presets.length).toBe(5)

    // 第三个预设为区域 API (us-east-1.api.x.ai/v1)
    await presets[2].trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://us-east-1.api.x.ai/v1'
      }
    })
  })

  it('所选含非 grok 平台时不展示快捷端点', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok', 'anthropic'],
      selectedTypes: ['apikey']
    })

    expect(wrapper.findAll('[data-testid="grok-base-url-preset"]').length).toBe(0)
  })

  it('全部目标为 Grok OAuth 时，第三方 base_url 正常提交', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-base-url-enabled').setValue(true)
    await wrapper.get('#bulk-edit-base-url').setValue('https://relay.example.com/v1')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://relay.example.com/v1'
      }
    })
  })

  it('混合类型选择（含 apikey）时官方主机 base_url 不拦截', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['apikey', 'oauth']
    })

    await wrapper.get('#bulk-edit-base-url-enabled').setValue(true)
    await wrapper.get('#bulk-edit-base-url').setValue('https://api.x.ai/v1')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://api.x.ai/v1'
      }
    })
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

  it('OpenAI OAuth 批量编辑可开启 namespace 摊平兼容开关', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-flatten-namespaces-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-flatten-namespaces-toggle').trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: { openai_responses_flatten_namespaces: true }
    })
  })

  it('namespace 摊平开关不对非 OAuth 选择展示', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth', 'apikey']
    })

    expect(wrapper.find('#bulk-edit-openai-flatten-namespaces-enabled').exists()).toBe(false)
  })

  it('OpenAI API Key 批量编辑提交默认工作负载能力与文本路由', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-openai-endpoint-capabilities-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-responses-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="openai-text-protocol-chat_completions"]').setValue(false)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: { openai_workload_capabilities: null },
      extra: { openai_text_route_mode: 'force_responses' }
    })
  })

  it('OpenAI API Key 批量编辑可显式开启 HTTP continuation', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    const applyContinuationCheckbox = wrapper.get<HTMLInputElement>('#bulk-edit-openai-continuation-supported-enabled')
    expect(applyContinuationCheckbox.element.tagName).toBe('INPUT')
    expect(applyContinuationCheckbox.attributes('type')).toBe('checkbox')
    expect(wrapper.get('#bulk-edit-openai-continuation-supported-body').text()).toBe('')
    expect(wrapper.get('[data-testid="bulk-edit-openai-continuation-supported"]').attributes('role')).toBe('switch')
    await applyContinuationCheckbox.setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-continuation-supported"]').trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: { openai_responses_continuation_supported: true }
    })
  })

  it('仅保留 embeddings 时自动清除强制文本路由', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-openai-endpoint-capabilities-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-responses-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="openai-text-protocol-responses"]').setValue(false)
    await wrapper.get('[data-testid="bulk-edit-openai-endpoint-capability-chat_completions"]').setValue(false)

    expect(wrapper.find('[data-testid="bulk-edit-openai-responses-mode-not-applicable"]').exists()).toBe(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: { openai_workload_capabilities: ['embeddings'] },
      extra: { openai_text_route_mode: null }
    })
  })

  it('至少保留一个 OpenAI 工作负载能力', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-openai-endpoint-capabilities-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-endpoint-capability-chat_completions"]').setValue(false)
    await wrapper.get('[data-testid="bulk-edit-openai-endpoint-capability-embeddings"]').setValue(false)

    expect(
      (wrapper.get('[data-testid="bulk-edit-openai-endpoint-capability-embeddings"]').element as HTMLInputElement)
        .checked
    ).toBe(true)
  })

  it('工作负载设置隐藏后不随其他批量字段提交', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-openai-endpoint-capabilities-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-responses-mode-enabled').setValue(true)
    await wrapper.setProps({ selectedPlatforms: ['anthropic'], selectedTypes: ['apikey'] })
    await wrapper.get('#bulk-edit-status-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      status: 'active'
    })
  })

  it.each([
    ['inherit', {
      codex_image_generation_bridge: null,
      codex_image_generation_bridge_enabled: null,
      codex_image_generation_explicit_tool_policy: null
    }],
    ['enabled', {
      codex_image_generation_bridge: true,
      codex_image_generation_bridge_enabled: null,
      codex_image_generation_explicit_tool_policy: null
    }],
    ['disabled', {
      codex_image_generation_bridge: false,
      codex_image_generation_bridge_enabled: null,
      codex_image_generation_explicit_tool_policy: null
    }],
    ['block', {
      codex_image_generation_bridge: null,
      codex_image_generation_bridge_enabled: null,
      codex_image_generation_explicit_tool_policy: 'strip'
    }]
  ])('OpenAI OAuth 批量编辑提交 Codex 图片工具 %s 策略', async (mode, expectedExtra) => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    const applyCheckbox = wrapper.get<HTMLInputElement>('#bulk-edit-codex-image-tool-enabled')
    expect(applyCheckbox.element.checked).toBe(false)
    await applyCheckbox.setValue(true)
    await wrapper.get(`[data-testid="bulk-edit-codex-image-tool-${mode}"]`).trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: expectedExtra
    })
  })

  it('OpenAI API Key 批量编辑显示并提交 Codex 图片工具策略', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    expect(wrapper.find('#bulk-edit-codex-image-tool-enabled').exists()).toBe(true)
    await wrapper.get('#bulk-edit-codex-image-tool-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-codex-image-tool-enabled"]').trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_image_generation_bridge: true,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: null
      }
    })
  })

  it('筛选 OpenAI OAuth/API Key 账号批量编辑提交 Codex 图片工具策略', async () => {
    const wrapper = mountModal({
      accountIds: [],
      selectedPlatforms: [],
      selectedTypes: [],
      target: {
        mode: 'filtered',
        filters: { platform: 'openai', status: 'active' },
        previewCount: 12,
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth', 'apikey']
      }
    })

    await wrapper.get('#bulk-edit-codex-image-tool-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-codex-image-tool-block"]').trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith({
      filters: { platform: 'openai', status: 'active' },
      extra: {
        codex_image_generation_bridge: null,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: 'strip'
      }
    })
  })

  it('非 OpenAI 目标不显示 Codex 图片工具批量字段', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['oauth']
    })

    expect(wrapper.find('#bulk-edit-codex-image-tool-enabled').exists()).toBe(false)
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

  it('OpenAI OAuth 批量编辑应提交客户端访问策略字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-codex-cli-only-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-client-policy-select"]').setValue('codex_only')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_oauth_client_policy: 'codex_only',
        codex_cli_only: true
      }
    })
  })

  it('OpenAI OAuth 批量编辑应显式覆盖 Codex 指纹收敛模式', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-codex-fingerprint-mode-select"]').setValue('session')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'session'
      }
    })
  })

  it('OpenAI OAuth 批量编辑可显式关闭已有 Codex 指纹收敛模式', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    expect((wrapper.get('[data-testid="bulk-codex-fingerprint-mode-select"]').element as HTMLSelectElement).value).toBe('off')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'off'
      }
    })
  })

  it('OpenAI OAuth 批量编辑可启用 TLS 指纹伪装', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })
    await flushPromises()

    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-toggle').trigger('click')
    await wrapper.get('[data-testid="bulk-edit-tls-fingerprint-profile"]').setValue('-1')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: true,
        tls_fingerprint_profile_id: -1,
        tls_fingerprint_router_id: 0
      }
    })
  })

  it('OpenAI OAuth 和 Anthropic OAuth 混选时可批量启用 TLS 指纹伪装', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai', 'anthropic'],
      selectedTypes: ['oauth', 'setup-token']
    })
    await flushPromises()

    expect(wrapper.find('#bulk-edit-tls-fingerprint-enabled').exists()).toBe(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-toggle').trigger('click')
    await wrapper.get('[data-testid="bulk-edit-tls-fingerprint-profile"]').setValue('-1')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: true,
        tls_fingerprint_profile_id: -1
      }
    })
  })

  it('Qoder COSY 批量编辑可启用 TLS 指纹伪装且不写入 OpenAI TLS 路由器', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['qoder'],
      selectedTypes: ['cosy']
    })
    await flushPromises()

    expect(wrapper.find('#bulk-edit-tls-fingerprint-enabled').exists()).toBe(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-toggle').trigger('click')
    await wrapper.get('[data-testid="bulk-edit-tls-fingerprint-profile"]').setValue('-1')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: true,
        tls_fingerprint_profile_id: -1
      }
    })
  })

  it('OpenAI OAuth 批量编辑应提交 codex_cli_only_allowed_clients 字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-codex-allow-claude-code-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-codex-allow-claude-code-toggle').trigger('click')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_cli_only_allowed_clients: ['claude_code']
      }
    })
  })

  it('OpenAI OAuth 批量编辑应提交 5h/7d 自动暂停字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-auto-pause-5h-threshold-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-auto-pause-5h-threshold').setValue('95')
    await wrapper.get('#bulk-edit-openai-auto-pause-7d-threshold-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-auto-pause-7d-threshold').setValue('90')
    await wrapper.get('#bulk-edit-openai-auto-pause-5h-disabled-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-auto-pause-5h-disabled-toggle').trigger('click')
    await wrapper.get('#bulk-edit-openai-auto-pause-7d-disabled-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        auto_pause_5h_threshold: 0.95,
        auto_pause_7d_threshold: 0.9,
        auto_pause_5h_disabled: true,
        auto_pause_7d_disabled: false
      }
    })
  })

  it('OpenAI OAuth 批量编辑可关闭 TLS 指纹伪装', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: false,
        tls_fingerprint_profile_id: 0,
        tls_fingerprint_router_id: 0
      }
    })
  })

  it('OpenAI API Key 批量编辑应提交 API Key 专属 WS mode 字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    await wrapper.get('#bulk-edit-openai-apikey-ws-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-apikey-ws-mode-select"]').setValue('ctx_pool')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_apikey_responses_websockets_v2_mode: 'ctx_pool',
        openai_apikey_responses_websockets_v2_enabled: true
      }
    })
  })

  it('OpenAI API Key 批量编辑不再显示上游倍率自动探测设置', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey']
    })

    expect(wrapper.find('#bulk-edit-upstream-billing-auto-probe-enabled').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bulk-edit-upstream-billing-auto-probe-select"]').exists()).toBe(false)
  })

  it('筛选 OpenAI 账号批量编辑应提交原生 V2、旧版 Compact 模式和专属模型映射', async () => {
    const wrapper = mountModal({
      accountIds: [],
      selectedPlatforms: [],
      selectedTypes: [],
      target: {
        mode: 'filtered',
        filters: { platform: 'openai' },
        previewCount: 12,
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth', 'apikey']
      }
    })

    await wrapper.get('#bulk-edit-openai-native-compaction-v2-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-native-compaction-v2-mode-select"] input').setValue(true)
    await wrapper.get('#bulk-edit-openai-compact-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-compact-mode-select"] input').setValue(true)
    await wrapper.get('#bulk-edit-openai-compact-model-mapping-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-compact-model-mapping-add"]').trigger('click')
    const inputs = wrapper.findAll('[data-testid="bulk-edit-openai-compact-model-mapping-input"]')
    await inputs[0].setValue('gpt-5.4')
    await inputs[1].setValue('gpt-5.4-openai-compact')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith({
      filters: { platform: 'openai' },
      extra: {
        openai_native_compaction_v2_mode: 'force_on',
        openai_compact_mode: 'force_on'
      },
      credentials: {
        compact_model_mapping: {
          'gpt-5.4': 'gpt-5.4-openai-compact'
        }
      }
    })
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

  it('filtered-results 模式下应提交 filters 而不是 account_ids', async () => {
    const wrapper = mountModal({
      accountIds: [],
      target: {
        mode: 'filtered',
        filters: {
          platform: 'openai',
          type: 'oauth',
          status: 'active',
          group: '12',
          search: 'bulk-target',
          privacy_mode: 'training_set_cf_blocked'
        },
        previewCount: 5,
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth']
      }
    })

    await wrapper.get('#bulk-edit-status-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith({
      filters: {
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        group: '12',
        search: 'bulk-target',
        privacy_mode: 'training_set_cf_blocked'
      },
      status: 'active'
    })
  })
  // issue #6327：批量编辑无法把 Codex 指纹收敛关掉。
  //
  // 批量更新走 JSONB 顶层合并（extra = COALESCE(extra,'{}') || payload），删掉 payload
  // 里的键只表示「本次不更新该键」，清不掉账号已有的 device/session/full；而且只删不写会让
  // payload 退化成 {extra:{}}，被后端 len(req.Extra) > 0 判为空更新直接 400
  // "No updates provided"。Create/Edit 那两个表单能删键，是因为它们提交完整 extra 对象、
  // 后端整体 SetExtra 覆盖——两种持久化语义不能共用同一套写法。
  it('OpenAI OAuth 批量编辑选择「关闭」时应显式提交 codex_fingerprint_mode=off（issue #6327）', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    // 下拉框默认就是 off，用户只勾选「编辑该项」即提交——正是 issue 描述的操作路径。
    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'off'
      }
    })

    // 缺陷时期的形状：extra 为空对象，后端必然回 400。显式钉死不得回退。
    const payload = vi.mocked(adminAPI.accounts.bulkUpdate).mock.calls[0][1] as {
      extra: Record<string, unknown>
    }
    expect(Object.keys(payload.extra).length).toBeGreaterThan(0)
  })

  // 与兄弟字段 codex_cli_only 的写法对齐：关闭态同样落显式值，不靠省略表达。
  it('OpenAI OAuth 批量编辑显式 opt-in 模式仍原样提交', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    await wrapper
      .get('[data-testid="bulk-codex-fingerprint-mode-select"]')
      .setValue('session')
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.accounts.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'session'
      }
    })
  })

  // 未勾选「编辑该项」时不得写入该键，否则批量编辑别的字段会顺手清掉账号的收敛设置。
  it('未勾选编辑该项时不写入 codex_fingerprint_mode', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth']
    })

    await wrapper.get('#bulk-edit-openai-codex-cli-only-enabled').setValue(true)
    await wrapper.get('#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = vi.mocked(adminAPI.accounts.bulkUpdate).mock.calls[0][1] as {
      extra: Record<string, unknown>
    }
    expect(payload.extra).not.toHaveProperty('codex_fingerprint_mode')
  })
})
