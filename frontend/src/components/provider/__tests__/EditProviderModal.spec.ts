import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { isSwitchOn, setSwitch } from '@/__tests__/helpers/switches'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { updateProviderMock, checkMixedChannelRiskMock, getWebSearchEmulationConfigMock, getSettingsMock, listTLSProfilesMock } = vi.hoisted(() => ({
  updateProviderMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  getWebSearchEmulationConfigMock: vi.fn(),
  getSettingsMock: vi.fn(),
  listTLSProfilesMock: vi.fn(),
}))

function coerceSelectStubValue(value: string, options: unknown[]): string | number | boolean | null {
  const option = (options as Array<Record<string, unknown>>).find((item) => String(item.value ?? '') === value)
  return option ? (option.value as string | number | boolean | null) : value
}

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    settings: {
      getSettings: getSettingsMock,
      getWebSearchEmulationConfig: getWebSearchEmulationConfigMock
    },
    providers: {
      update: updateProviderMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock
    },
    tlsFingerprintProfiles: {
      list: listTLSProfilesMock
    }
  }
}))

vi.mock('@/api/admin/providers', () => ({
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

import EditProviderModal from '../EditProviderModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div>
      <button
        type="button"
        data-testid="rewrite-to-snapshot"
        @click="$emit('update:modelValue', ['gpt-5.2-2025-12-11'])"
      >
        rewrite
      </button>
      <button
        type="button"
        data-testid="rewrite-to-qoder-defaults"
        @click="$emit('update:modelValue', ['claude-opus-4-6', 'auto'])"
      >
        rewrite qoder
      </button>
      <span data-testid="model-whitelist-value">
        {{ Array.isArray(modelValue) ? modelValue.join(',') : '' }}
      </span>
    </div>
  `
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: {
      type: [String, Number, Boolean, null],
      default: ''
    },
    options: {
      type: Array,
      default: () => []
    }
  },
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
})

const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div data-testid="group-selector">
      <button
        type="button"
        data-testid="set-shadow-group"
        @click="$emit('update:modelValue', [7])"
      >
        group
      </button>
    </div>
  `
})

function buildProvider() {
  return {
    id: 1,
    name: 'OpenAI Key',
    notes: '',
    platform: 'openai',
    type: 'apikey',
    credentials: {
      api_key: 'sk-test',
      base_url: 'https://api.openai.com',
      model_whitelist: ['gpt-5.2']
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildOpenAISparkShadowProvider() {
  const provider = buildProvider()
  return {
    ...provider,
    id: 4,
    name: 'OpenAI Spark Shadow',
    type: 'oauth',
    parent_provider_id: 1,
    credentials: {
      access_token: 'parent-access-token',
      refresh_token: 'parent-refresh-token',
      api_key: 'sk-parent',
      base_url: 'https://api.openai.com',
      model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark'
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact'
      }
    },
    group_ids: []
  } as any
}

function buildOpenAIOAuthProvider() {
  const provider = buildProvider()
  return {
    ...provider,
    id: 7,
    name: 'OpenAI OAuth',
    type: 'oauth',
    credentials: {
      email: 'oauth@example.com',
      plan_type: 'chatgptpro',
      model_mapping: {
        'gpt-5.4': 'gpt-5.4'
      }
    }
  } as any
}

function buildVertexProvider() {
  return {
    id: 2,
    name: 'Vertex SA',
    notes: '',
    platform: 'gemini',
    type: 'service_account',
    credentials: {
      service_account_json: '{"type":"service_account","client_email":"sa@example.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\\nMIIE\\n-----END PRIVATE KEY-----\\n"}',
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildQoderProvider() {
  return {
    id: 3,
    name: 'Qoder COSY',
    notes: '',
    platform: 'qoder',
    type: 'cosy',
    credentials: {
      security_oauth_token: 'redacted',
      machine_id: 'machine',
      site: 'global',
      refresh_mode: 'cosy',
      model_mapping: {
        'claude-opus-4-6': 'ultimate',
        auto: 'auto'
      },
      model_whitelist: []
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildGrokOAuthProvider() {
  return {
    id: 5,
    name: 'Grok OAuth',
    notes: '',
    platform: 'grok',
    type: 'oauth',
    credentials: {
      refresh_token: 'grok-rt',
      base_url: 'https://api.x.ai/v1',
      model_mapping: {
        'grok-latest': 'grok-4.3'
      }
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function buildGrokAPIKeyProvider() {
  return {
    ...buildProvider(),
    id: 6,
    name: 'Grok API Key',
    platform: 'grok',
    credentials: {},
    credentials_status: { has_api_key: true },
    concurrency: 2
  } as any
}
function mountModal(provider = buildProvider()) {
  return mount(EditProviderModal, {
    props: {
      show: true,
      provider,
      proxies: [],
      groups: []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        Icon: true,
        ProxySelector: true,
        GroupSelector: GroupSelectorStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub
      }
    }
  })
}

describe('EditProviderModal', () => {
  beforeEach(() => {
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    getWebSearchEmulationConfigMock.mockReset()
    getSettingsMock.mockReset()
    listTLSProfilesMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    getSettingsMock.mockResolvedValue({ provider_quota_notify_enabled: false })
    getWebSearchEmulationConfigMock.mockResolvedValue({ enabled: false, providers: [] })
    listTLSProfilesMock.mockResolvedValue([])
  })

  it.each([undefined, ''])('Jev 地址为 %s 时加载官方默认值', async (baseUrl) => {
    const provider = { ...buildProvider(), platform: 'jev' as const, credentials: { api_key: 'test-jev-key', base_url: baseUrl, upstream_protocols: ['systemone'] } }
    const wrapper = mountModal(provider)
    await flushPromises()
    expect((wrapper.get('#edit-provider-base-url').element as HTMLInputElement).value).toBe('https://api.typesafe.ai')
    wrapper.unmount()
  })

  it('清空 Jev 地址后保存官方默认值', async () => {
    const provider = { ...buildProvider(), platform: 'jev' as const, credentials: { api_key: 'test-jev-key', base_url: 'https://custom.example', upstream_protocols: ['systemone'] } }
    const wrapper = mountModal(provider)
    await flushPromises()
    await wrapper.get('#edit-provider-base-url').setValue('')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials.base_url).toBe('https://api.typesafe.ai')
    wrapper.unmount()
  })

  it('renders the shared provider model rule copy', async () => {
    const provider = buildProvider()
    provider.credentials.model_whitelist = []
    const wrapper = mountModal(provider)

    expect(wrapper.text()).toContain('admin.providers.modelRestriction')
    expect(wrapper.text()).toContain('admin.providers.modelWhitelist')
    expect(wrapper.text()).toContain('admin.providers.modelRestrictionCombinedHint')
    expect(wrapper.text()).toContain('admin.providers.supportsAllModels')

    const mappingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.modelMapping'))
    expect(mappingButton).toBeTruthy()
    await mappingButton!.trigger('click')
    expect(wrapper.text()).toContain('admin.providers.mapRequestModels')
  })

  it('allows concurrency and load factor to be cleared before entering replacement values', async () => {
    const provider = {
      ...buildProvider(),
      concurrency: 6,
      load_factor: 6
    }
    updateProviderMock.mockResolvedValue(provider)
    const wrapper = mountModal(provider)
    const concurrencyInput = wrapper.get<HTMLInputElement>(
      '[data-testid="edit-provider-concurrency"]'
    )
    const loadFactorInput = wrapper.get<HTMLInputElement>(
      '[data-testid="edit-provider-load-factor"]'
    )

    await concurrencyInput.setValue('')
    await loadFactorInput.setValue('')
    expect(concurrencyInput.element.value).toBe('')
    expect(loadFactorInput.element.value).toBe('')

    await concurrencyInput.setValue('12')
    await loadFactorInput.setValue('9')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]).toMatchObject({
      concurrency: 12,
      load_factor: 9
    })
  })

  it('reopening the same provider rehydrates the OpenAI whitelist from props', async () => {
    const provider = buildProvider()
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2-2025-12-11')

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual(['gpt-5.2'])
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
  })

  // 回显与保存都应保留管理员配置的分协议地址，不能被默认端点覆盖。
  it.each(['kimi', 'zhipu', 'deepseek'])('%s adaptive 提供商回显并保存自定义端点', async platform => {
    const provider = buildProvider()
    provider.platform = platform
    const endpoints = {
      chat_completions: 'https://relay.example.test/v1',
      anthropic: 'https://relay.example.test/anthropic',
      ...(platform !== 'zhipu' ? { responses: 'https://relay.example.test/responses' } : {})
    }
    provider.credentials = {
      api_key: 'sk-cn',
      provider_mode: 'payg',
      upstream_protocols: platform === 'zhipu' ? ['anthropic_messages','openai_chat_completions'] : ['anthropic_messages','openai_responses','openai_chat_completions'],
      base_url: endpoints.chat_completions,
      api_base_urls: endpoints
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    const inputValues = wrapper.findAll<HTMLInputElement>('input[type="text"]').map(input => input.element.value)
    for (const endpoint of Object.values(endpoints)) expect(inputValues).toContain(endpoint)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_mode: 'payg',
      upstream_protocols: platform === 'zhipu' ? ['anthropic_messages','openai_chat_completions'] : ['anthropic_messages','openai_responses','openai_chat_completions'],
      base_url: endpoints.chat_completions,
      api_base_urls: endpoints
    })
  })

  it.each(['payg', 'coding'])('Kimi %s 原生 Responses 回显和保存不回退协议', async mode => {
    const provider = buildProvider()
    provider.platform = 'kimi'
    provider.credentials = {
      api_key: 'sk-cn', provider_mode: mode, upstream_protocols: ['openai_responses'], api_base_urls: { responses: 'https://relay.example.test/responses' },
      base_url: 'https://relay.example.test/responses'
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(provider)
    expect(wrapper.findAll<HTMLInputElement>('input[type="text"]').map(input => input.element.value))
      .toContain('https://relay.example.test/responses')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_mode: mode, upstream_protocols: ['openai_responses'], api_base_urls: { responses: 'https://relay.example.test/responses' }, base_url: 'https://relay.example.test/responses'
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toHaveProperty('api_base_urls')
  })

  it('preserves adaptive GLM endpoints on submit', async () => {
    const provider = buildProvider()
    provider.platform = 'zhipu'
    provider.credentials = {
      api_key: 'sk-glm',
      provider_mode: 'coding',
      upstream_protocols: ['anthropic_messages','openai_chat_completions'],
      base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
      api_base_urls: {
        chat_completions: 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropic: 'https://open.bigmodel.cn/api/anthropic'
      }
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_mode: 'coding',
      upstream_protocols: ['anthropic_messages','openai_chat_completions'],
      base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
      api_base_urls: {
        chat_completions: 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropic: 'https://open.bigmodel.cn/api/anthropic'
      }
    })
  })

  it.each([
    ['explicit Chat Completions', 'chat_completions'],
    ['legacy missing protocol', undefined]
  ])('preserves a custom CN relay for %s providers', async (_name, storedProtocol) => {
    const provider = buildProvider()
    provider.platform = 'zhipu'
    provider.credentials = {
      api_key: 'sk-glm',
      provider_mode: 'payg',
      base_url: 'https://relay.example.com/v1'
    }
    if (storedProtocol) {
      provider.credentials.api_protocol = storedProtocol
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    const submittedCredentials = updateProviderMock.mock.calls[0]?.[1]?.credentials
    expect(submittedCredentials).toMatchObject({
      provider_mode: 'payg',
      upstream_protocols: expect.arrayContaining(['openai_chat_completions']),
      base_url: 'https://relay.example.com/v1'
    })
    expect(submittedCredentials).toHaveProperty('upstream_protocols')
  })

  it('uses the legacy base_url when adaptive endpoints are missing', async () => {
    const provider = buildProvider()
    provider.platform = 'zhipu'
    provider.credentials = {
      api_key: 'sk-glm',
      provider_mode: 'payg',
      upstream_protocols: ['anthropic_messages','openai_chat_completions'],
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: '   '
      }
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      upstream_protocols: ['anthropic_messages','openai_chat_completions'],
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: 'https://relay.example.com/v1',
        anthropic: 'https://open.bigmodel.cn/api/anthropic'
      }
    })
  })

  it.each([
    {
      name: 'Anthropic',
      platform: 'zhipu',
      protocol: 'anthropic',
      baseUrl: 'https://relay.example.com/anthropic',
      expectedBaseUrl: 'https://open.bigmodel.cn/api/paas/v4',
      expectedProtocolUrls: {
        chat_completions: 'https://open.bigmodel.cn/api/paas/v4',
        anthropic: 'https://relay.example.com/anthropic'
      }
    },
    {
      name: 'Responses',
      platform: 'deepseek',
      protocol: 'responses',
      baseUrl: 'https://relay.example.com/responses',
      expectedBaseUrl: 'https://api.deepseek.com',
      expectedProtocolUrls: {
        chat_completions: 'https://api.deepseek.com',
        anthropic: 'https://api.deepseek.com/anthropic',
        responses: 'https://relay.example.com/responses'
      }
    }
  ])('keeps a fixed $name relay in its protocol slot when switching to Adaptive', async (testCase) => {
    const provider = buildProvider()
    provider.platform = testCase.platform
    provider.credentials = {
      api_key: 'sk-cn',
      provider_mode: 'payg',
      upstream_protocols: [testCase.protocol === 'anthropic' ? 'anthropic_messages' : 'openai_responses'],
      api_base_urls: testCase.expectedProtocolUrls,
      base_url: testCase.expectedBaseUrl
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    for (const toggle of wrapper.findAll('[data-native-protocol]')) await setSwitch(toggle, true)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      upstream_protocols: testCase.platform === 'zhipu' ? ['anthropic_messages','openai_chat_completions'] : ['anthropic_messages','openai_responses','openai_chat_completions'],
      base_url: testCase.expectedBaseUrl,
      api_base_urls: testCase.expectedProtocolUrls
    })
  })

  it('preserves model mappings when editing the whitelist', async () => {
    const provider = buildProvider()
    provider.credentials = {
      ...provider.credentials,
      model_whitelist: ['gpt-5.2'],
      model_mapping: {
        'gpt-latest': 'gpt-5.2'
      }
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    const whitelistButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.modelWhitelist'))
    expect(whitelistButton).toBeTruthy()

    await whitelistButton!.trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual(['gpt-5.2-2025-12-11'])
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-latest': 'gpt-5.2'
    })
  })

  it('压缩开关独立控制，旧版关闭时隐藏映射，协议至少保留一项', async () => {
    const wrapper = mountModal(buildProvider())
    const responses = wrapper.get('[data-native-protocol="openai_responses"]')
    const chat = wrapper.get('[data-native-protocol="openai_chat_completions"]')
    await setSwitch(chat, false)
    expect(isSwitchOn(responses)).toBe(true)
    expect(responses.attributes('disabled')).toBeUndefined()
    await setSwitch(chat, true)
    expect(responses.attributes('disabled')).toBeUndefined()
    await setSwitch(wrapper.get('[data-testid="edit-openai-compact-mode"]'), false)
    expect(wrapper.text()).not.toContain('admin.providers.openai.compactModelMapping')
    expect(isSwitchOn(wrapper.get('[data-testid="edit-openai-native-compaction-v2-mode"]'))).toBe(true)
    await setSwitch(wrapper.get('[data-testid="edit-openai-compact-mode"]'), true)
    expect(wrapper.find('[data-testid="openai-responses-probe-status"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('submits independent OpenAI native V2 and legacy compact settings', async () => {
    const provider = buildProvider()
    provider.extra = {
      openai_compact_mode: 'force_on',
      openai_native_compaction_v2_mode: 'force_off'
    }
    provider.credentials = {
      ...provider.credentials,
      compact_model_mapping: {
        'gpt-5.4': 'gpt-5.4-openai-compact'
      }
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_compact_mode).toBe('force_on')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_native_compaction_v2_mode).toBe('force_off')
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.compact_model_mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-openai-compact'
    })
  })

  it('does not render or resubmit the removed provider-level long-context setting', async () => {
    const provider = buildProvider()
    provider.extra = {
      openai_long_context_billing_enabled: true,
      preserved: 'value'
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_long_context_billing_enabled')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.preserved).toBe('value')
  })

  it('loads and clears the OAuth-only Codex namespace flatten toggle', async () => {
    const provider = buildProvider()
    provider.type = 'oauth'
    provider.extra = { openai_responses_flatten_namespaces: true }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('[data-testid="edit-openai-flatten-namespaces-toggle"]').trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_responses_flatten_namespaces'
    )
  })

  it('submits the Codex namespace flatten toggle when switched on', async () => {
    const provider = buildProvider()
    provider.type = 'oauth'
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('[data-testid="edit-openai-flatten-namespaces-toggle"]').trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_flatten_namespaces).toBe(true)
  })

  it('writes the upstream request id header into extra only when it changes', async () => {
    const provider = buildProvider()
    provider.extra = { openai_compact_mode: 'force_on' }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const untouched = mountModal(provider)
    await untouched.get('form#edit-provider-form').trigger('submit.prevent')
    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.upstream_request_id_header).toBeUndefined()

    updateProviderMock.mockClear()
    const wrapper = mountModal(provider)
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue(' X-Oneapi-Request-Id ')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).toMatchObject({
      openai_compact_mode: 'force_on',
      upstream_request_id_header: 'X-Oneapi-Request-Id'
    })
  })

  it('removes the upstream request id header from extra when cleared', async () => {
    const provider = buildProvider()
    provider.extra = { upstream_request_id_header: 'X-Request-ID' }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    expect((wrapper.get('[data-testid="upstream-request-id-header"]').element as HTMLInputElement).value).toBe('X-Request-ID')
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue('')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).toBeDefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('upstream_request_id_header')
  })

  it('hides the Codex namespace flatten toggle for non-OAuth OpenAI providers', async () => {
    const provider = buildProvider()
    const wrapper = mountModal(provider)

    expect(wrapper.find('[data-testid="edit-openai-flatten-namespaces-toggle"]').exists()).toBe(false)
  })

  it('loads and submits Grok OAuth model mapping edits', async () => {
    const provider = buildGrokOAuthProvider()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    const inputWithValue = (value: string) => {
      const input = wrapper
        .findAll('input')
        .find((input) => (input.element as HTMLInputElement).value === value)
      expect(input).toBeTruthy()
      return input!
    }

    await inputWithValue('grok-latest').setValue('grok')
    await inputWithValue('grok-4.3').setValue('grok-build-0.1')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      grok: 'grok-build-0.1'
    })
  })

  it('uses the official xAI base URL when a Grok API-key provider omits base_url', async () => {
    const provider = buildGrokAPIKeyProvider()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect((wrapper.get('input[placeholder="https://api.x.ai/v1"]').element as HTMLInputElement).value)
      .toBe('https://api.x.ai/v1')

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.base_url).toBe('https://api.x.ai/v1')
  })

  it('only submits model mapping credentials when saving an OpenAI spark shadow provider', async () => {
    const provider = buildOpenAISparkShadowProvider()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(wrapper.find('[data-testid="openai-plan-type-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="edit-codex-fingerprint-mode-select"]').exists()).toBe(false)

    await wrapper.get('[data-testid="set-shadow-group"]').trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    const payload = updateProviderMock.mock.calls[0]?.[1]
    expect(payload?.group_ids).toEqual([7])
    expect(payload?.credentials).toEqual({
      model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark'
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact'
      }
    })
  })

  it('loads and submits a manual plan type for a non-shadow OpenAI OAuth provider', async () => {
    const provider = buildOpenAIOAuthProvider()
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    const planTypeSelect = wrapper.get<HTMLSelectElement>('[data-testid="openai-plan-type-select"]')

    expect(planTypeSelect.element.value).toBe('chatgptpro')

    await planTypeSelect.setValue('free')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      email: 'oauth@example.com',
      plan_type: 'free',
      model_whitelist: ['gpt-5.4']
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
  })

  it('OpenAI OAuth 编辑白名单时提交独立 model_whitelist', async () => {
    const provider = buildOpenAIOAuthProvider()
    provider.credentials = {
      ...provider.credentials,
      model_mapping: {
        'codex-alias': 'gpt-5.4'
      },
      model_whitelist: ['gpt-5.4']
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    const whitelistButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.modelWhitelist'))
    expect(whitelistButton).toBeTruthy()

    await whitelistButton!.trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.4')
    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'codex-alias': 'gpt-5.4'
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([
      'gpt-5.2-2025-12-11'
    ])
  })

  it('OpenAI OAuth 在独立白名单格式中回填自映射规则', async () => {
    const provider = buildOpenAIOAuthProvider()
    provider.credentials = {
      ...provider.credentials,
      model_mapping: {
        'gpt-5.6-sol': 'gpt-5.6-sol'
      },
      model_whitelist: []
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    const mappingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.modelMapping'))
    expect(mappingButton).toBeTruthy()

    await mappingButton!.trigger('click')

    const requestInput = wrapper
      .findAll('input')
      .find((input) => input.attributes('placeholder') === 'admin.providers.requestModel')
    const targetInput = wrapper
      .findAll('input')
      .find((input) => input.attributes('placeholder') === 'admin.providers.actualModel')
    expect(requestInput).toBeTruthy()
    expect(targetInput).toBeTruthy()
    expect((requestInput!.element as HTMLInputElement).value).toBe('gpt-5.6-sol')
    expect((targetInput!.element as HTMLInputElement).value).toBe('gpt-5.6-sol')

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.6-sol': 'gpt-5.6-sol'
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
  })

  it('loads and submits the Codex fingerprint mode for OpenAI OAuth providers', async () => {
    const provider = buildOpenAIOAuthProvider()
    provider.extra = { codex_fingerprint_mode: 'device' }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    const modeSelect = wrapper.get<HTMLSelectElement>(
      '[data-testid="edit-codex-fingerprint-mode-select"]'
    )

    expect(modeSelect.element.value).toBe('device')
    await modeSelect.setValue('full')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.codex_fingerprint_mode).toBe('full')
  })

  it('does not show the plan type override for OpenAI API-key providers', () => {
    const wrapper = mountModal(buildProvider())

    expect(wrapper.find('[data-testid="openai-plan-type-select"]').exists()).toBe(false)
  })

  it('renders the provider scheduling threshold override as a switch', () => {
    const wrapper = mountModal(buildProvider())

    expect(wrapper.get('[data-testid="provider-scheduling-threshold-override-enabled"]').attributes('role')).toBe('switch')
  })

  // 关闭开关时删除显式 opt-in，并保留其他提供商配置。
  it('hydrates and disables image URL backfill independently', async () => {
    const provider = buildProvider()
    provider.extra = { images_url_to_b64_json: true, retained: 'value' }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(provider)
    const toggle = wrapper.get('[data-testid="edit-openai-images-url-to-b64-json"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()
    const extra = updateProviderMock.mock.calls[0]?.[1]?.extra
    expect(extra).not.toHaveProperty('images_url_to_b64_json')
    expect(extra.retained).toBe('value')
  })

  it('submits administrator text protocols and removes probe state', async () => {
    const provider = buildProvider()
    provider.extra = {
      openai_text_route_mode: 'force_chat_completions',
      openai_responses_probe_status: 'unsupported',
      openai_responses_continuation_supported: true,
      images_url_to_b64_json: true
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(wrapper.get('[data-testid="edit-openai-continuation-supported"]').attributes('role')).toBe('switch')
    await setSwitch(wrapper.get('[data-native-protocol="openai_responses"]'), true)
    await setSwitch(wrapper.get('[data-native-protocol="openai_chat_completions"]'), false)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_text_route_mode).toBeUndefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_probe_status).toBeUndefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_continuation_supported).toBe(true)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.images_url_to_b64_json).toBe(true)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_supported')
  })

  it('does not render the removed upstream billing auto-probe setting', () => {
    const wrapper = mountModal(buildProvider())

    expect(wrapper.find('[data-testid="upstream-billing-auto-probe"]').exists()).toBe(false)
  })

  it('hydrates the native set and submits only the unified shape', async () => {
    const provider = buildProvider()
    provider.credentials.upstream_protocols = ['openai_chat_completions']
    provider.extra = {
      openai_responses_mode: 'force_chat_completions',
      openai_responses_supported: true
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    const responses = wrapper.get('[data-native-protocol="openai_responses"]')
    expect(isSwitchOn(responses)).toBe(false)
    expect(wrapper.find('[data-testid="openai-responses-probe-status"]').exists()).toBe(false)
    await setSwitch(responses, true)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_text_route_mode).toBeUndefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_probe_status).toBeUndefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_continuation_supported).toBe(false)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_supported')
  })

  it('submits OpenAI APIKey endpoint capabilities from credentials', async () => {
    const provider = buildProvider()
    provider.credentials.upstream_protocols = ['openai_responses', 'openai_chat_completions']
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(wrapper.findAll('[data-native-protocol]').some(isSwitchOn)).toBe(true)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.upstream_protocols).toEqual(['openai_responses', 'openai_chat_completions'])
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('openai_capabilities')
  })

	it('submits OpenAI quota auto-pause thresholds in extra', async () => {
	  const provider = buildProvider()
	  provider.extra = {
		auto_pause_5h_threshold: 0.9,
		auto_pause_7d_threshold: 0.8
	  }
	  updateProviderMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateProviderMock.mockResolvedValue(provider)

	  const wrapper = mountModal(provider)

	  await wrapper.get('[data-testid="auto-pause-5h-threshold"]').setValue('95')
	  await wrapper.get('[data-testid="auto-pause-7d-threshold"]').setValue('96')
	  await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

	  expect(updateProviderMock).toHaveBeenCalledTimes(1)
	  expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_threshold).toBe(0.95)
	  expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_threshold).toBe(0.96)
	})

	it('submits OpenAI quota auto-pause disable flag in extra', async () => {
	  // 切换提供商级禁用标记时必须持久化为 auto_pause_5h_disabled，
	  // 这样即使配置了全局默认阈值，管理员也能让单个提供商豁免自动暂停；
	  // 否则阈值留空会静默回退到全局默认值。
	  const provider = buildProvider()
	  updateProviderMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateProviderMock.mockResolvedValue(provider)

	  const wrapper = mountModal(provider)

	  await wrapper.get('[data-testid="auto-pause-5h-disabled"]').trigger('click')
	  await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

	  expect(updateProviderMock).toHaveBeenCalledTimes(1)
	  expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_disabled).toBe(true)
	  expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_disabled).toBeUndefined()
	})

  it('preserves an explicitly empty native protocol set', async () => {
    const provider = buildProvider()
    provider.credentials = { ...provider.credentials, upstream_protocols: [] }
    const wrapper = mountModal(provider)
    await flushPromises()
    expect(wrapper.findAll('[data-native-protocol]').every(toggle => !isSwitchOn(toggle))).toBe(true)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.upstream_protocols).toEqual([])
  })

  it('submits Codex image tool force-inject mode as bridge override', async () => {
    const provider = buildProvider()
    provider.extra = {
      codex_image_generation_bridge: false,
      codex_image_generation_bridge_enabled: true
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(wrapper.text()).toContain('admin.protocols.imagePolicy')
    expect(wrapper.text()).toContain('admin.providers.openai.codexImageToolDesc')

    await wrapper.get('[data-testid="codex-image-tool-select"]').setValue('enabled')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(true)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge_enabled')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool no-injection mode without strip policy', async () => {
    const provider = buildProvider()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('[data-testid="codex-image-tool-select"]').setValue('disabled')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(false)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool block mode as strip policy and clears bridge override', async () => {
    const provider = buildProvider()
    provider.extra = {
      codex_image_generation_bridge: true
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(wrapper.text()).toContain('admin.protocols.imagePolicyOptions.block.label')

    await wrapper.get('[data-testid="codex-image-tool-select"]').setValue('block')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_explicit_tool_policy).toBe('strip')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('loads strip policy as block mode and clears both keys when reset to inherit', async () => {
    const provider = buildProvider()
    provider.extra = {
      codex_image_generation_explicit_tool_policy: 'strip'
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('[data-testid="codex-image-tool-select"]').setValue('inherit')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('loads and submits OpenAI OAuth TLS fingerprint settings', async () => {
    const provider = buildProvider()
    provider.type = 'oauth'
    provider.name = 'OpenAI OAuth'
    provider.credentials = {
      access_token: 'oauth-token',
      chatgpt_account_id: 'chatgpt-acc'
    }
    provider.extra = {
      enable_tls_fingerprint: true,
      tls_fingerprint_profile_id: -1
    }
    provider.enable_tls_fingerprint = true
    provider.tls_fingerprint_profile_id = -1
    listTLSProfilesMock.mockResolvedValue([{ id: 7, name: 'Profile 7' }])
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-openai-tls-fingerprint-profile"]').exists()).toBe(true)
    const profileSelect = wrapper.get('[data-testid="edit-openai-tls-fingerprint-profile"]')
    expect((profileSelect.element as HTMLSelectElement).value).toBe('-1')

    await profileSelect.setValue('7')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.enable_tls_fingerprint).toBe(true)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.tls_fingerprint_profile_id).toBe(7)
  })

  it('loads and submits Qoder COSY model mappings', async () => {
    const provider = buildQoderProvider()
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'claude-opus-4-6': 'ultimate',
      auto: 'auto'
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.security_oauth_token).toBe('redacted')
  })

  it('switches Qoder site without deleting credentials or model mappings', async () => {
    const provider = buildQoderProvider()
    updateProviderMock.mockResolvedValue(provider)
    const wrapper = mountModal(provider)

    await wrapper.get('[data-testid="edit-qoder-site-cn"]').trigger('click')
    expect(wrapper.text()).toContain('admin.providers.qoder.site.changeWarning')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    const credentials = updateProviderMock.mock.calls[0]?.[1]?.credentials
    expect(credentials.site).toBe('cn')
    expect(credentials.refresh_mode).toBe('cosy')
    expect(credentials.security_oauth_token).toBe('redacted')
    expect(credentials.model_mapping).toEqual({
      'claude-opus-4-6': 'ultimate',
      auto: 'auto'
    })
  })

  it('does not persist generated Qoder model mappings on unrelated edits', async () => {
    const provider = buildQoderProvider()
    delete provider.credentials.model_mapping
    delete provider.credentials.model_whitelist
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toBeUndefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.security_oauth_token).toBe('redacted')
  })

  it('does not persist generated Qoder model mappings after only viewing the mapping tab', async () => {
    const provider = buildQoderProvider()
    delete provider.credentials.model_mapping
    delete provider.credentials.model_whitelist
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    const mappingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.modelMapping'))
    expect(mappingButton).toBeTruthy()

    await mappingButton!.trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toBeUndefined()
  })

  it('persists Qoder model_mapping after explicit mapping edit', async () => {
    const provider = buildQoderProvider()
    delete provider.credentials.model_mapping
    delete provider.credentials.model_whitelist
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    const addMappingButton = wrapper.findAll('button').find(button => button.text().includes('admin.providers.addMapping'))
    expect(addMappingButton).toBeTruthy()
    await addMappingButton!.trigger('click')

    const requestInput = wrapper.findAll('input').find(input => input.attributes('placeholder') === 'admin.providers.requestModel')
    const targetInput = wrapper.findAll('input').find(input => input.attributes('placeholder') === 'admin.providers.actualModel')
    expect(requestInput).toBeTruthy()
    expect(targetInput).toBeTruthy()
    await requestInput!.setValue('glm-5.2')
    await targetInput!.setValue('gm51model')

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({ 'glm-5.2': 'gm51model' })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
  })

  it('loads and submits Qoder COSY TLS fingerprint settings', async () => {
    const provider = buildQoderProvider()
    provider.extra = {
      enable_tls_fingerprint: true,
      tls_fingerprint_profile_id: -1,
      tls_fingerprint_router_id: 9
    }
    listTLSProfilesMock.mockResolvedValue([{ id: 7, name: 'Profile 7' }])
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-openai-tls-fingerprint-profile"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="edit-openai-tls-fingerprint-router"]').exists()).toBe(false)
    const profileSelect = wrapper.get('[data-testid="edit-openai-tls-fingerprint-profile"]')
    expect((profileSelect.element as HTMLSelectElement).value).toBe('-1')

    await profileSelect.setValue('7')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.enable_tls_fingerprint).toBe(true)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.tls_fingerprint_profile_id).toBe(7)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('tls_fingerprint_router_id')
  })

  it('allows saving apikey provider when backend redacted api_key but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 api_key，credentials_status.has_api_key=true。
    const provider = buildProvider()
    provider.credentials = {
      base_url: 'https://api.openai.com',
      model_mapping: { 'gpt-5.2': 'gpt-5.2' }
    }
    provider.credentials_status = { has_api_key: true }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    // 用户未输入新 key 时，payload 不带 api_key，由后端合并保留旧值。
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('api_key')
  })

  it('updates a Gemini API Key to a third-party provider and clears its tier', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'gemini',
      credentials: {
        api_key: 'AIza-test',
        base_url: 'https://generativelanguage.googleapis.com',
        tier_id: 'aistudio_free'
      }
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-gemini-tier"]').exists()).toBe(true)
    await wrapper.get<HTMLSelectElement>('[data-testid="edit-gemini-provider-type"]').setValue('third_party')
    expect(wrapper.find('[data-testid="edit-gemini-tier"]').exists()).toBe(false)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()

    await wrapper.get<HTMLInputElement>('[data-testid="edit-provider-base-url"]').setValue('https://provider.example.test')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_type: 'third_party',
      base_url: 'https://provider.example.test'
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('tier_id')
  })

  it('treats historical Gemini API Key providers as official when switching back from a third-party provider', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'gemini',
      credentials: {
        api_key: 'provider-key',
        base_url: 'https://provider.example.test'
      }
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await flushPromises()

    const providerType = wrapper.get<HTMLSelectElement>('[data-testid="edit-gemini-provider-type"]')
    expect(providerType.element.value).toBe('official')
    await providerType.setValue('third_party')
    await providerType.setValue('official')
    await wrapper.get<HTMLSelectElement>('[data-testid="edit-gemini-tier-select"]').setValue('aistudio_paid')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_type: 'official',
      tier_id: 'aistudio_paid'
    })
  })

  it('allows saving apikey provider against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.api_key 仍是明文，应允许保存。
    const provider = buildProvider()
    // 构造缺少 credentials_status 的响应。
    expect(provider.credentials_status).toBeUndefined()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    // 旧后端响应未脱敏，原 api_key 会随 currentCredentials 一起传回去。
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.api_key).toBe('sk-test')
  })

  it('blocks apikey save when neither credentials_status nor legacy api_key indicates existence', async () => {
    const provider = buildProvider()
    provider.credentials = {
      base_url: 'https://api.openai.com'
    }
    // 既没有 credentials_status 也没有旧的 api_key。
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()
  })

  it('allows saving Vertex SA provider when backend redacted service_account_json but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 service_account_json，credentials_status.has_service_account_json=true。
    const provider = buildVertexProvider()
    provider.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    provider.credentials_status = { has_service_account_json: true }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.project_id).toBe('demo-project')
  })

  it('allows saving Vertex SA provider against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.service_account_json 仍是明文，应允许保存。
    const provider = buildVertexProvider()
    expect(provider.credentials_status).toBeUndefined()
    expect(provider.credentials.service_account_json).toBeTruthy()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
  })

  it('blocks Vertex SA save when neither credentials_status nor legacy json indicates existence', async () => {
    const provider = buildVertexProvider()
    provider.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    // 既没有 credentials_status 也没有旧的 service_account_json。
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()
  })

  it('按账号类型隐藏没有内容的页签', async () => {
    const tabKeys = (wrapper: ReturnType<typeof mountModal>) =>
      wrapper.findAll('[data-settings-tab-button]').map(tab => tab.attributes('data-settings-tab-button'))
    const apiKeyWrapper = mountModal(buildProvider())
    await flushPromises()
    expect(tabKeys(apiKeyWrapper)).toEqual(['basic', 'models', 'scheduling', 'quota', 'request'])
    apiKeyWrapper.unmount()

    const oauthWrapper = mountModal({ ...buildProvider(), type: 'oauth', credentials: {} })
    await flushPromises()
    expect(tabKeys(oauthWrapper)).toEqual(['basic', 'models', 'scheduling', 'request'])
    oauthWrapper.unmount()
  })

  it('业务校验失败时切回字段所在页签', async () => {
    const provider = buildProvider()
    provider.credentials = { base_url: 'https://api.openai.com' }
    provider.credentials_status = { has_api_key: false }
    updateProviderMock.mockReset()
    const wrapper = mountModal(provider)
    await flushPromises()

    await wrapper.get('[data-settings-tab-button="request"]').trigger('click')
    expect(wrapper.get('[data-settings-tab-button="basic"]').attributes('aria-selected')).toBe('false')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-settings-tab-button="basic"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-provider-field="api-key"]').element.closest('[data-settings-tab]')?.getAttribute('data-settings-tab')).toBe('basic')
    wrapper.unmount()
  })
})

useProtocolCatalogFixture()
