import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { updateAccountMock, checkMixedChannelRiskMock, getWebSearchEmulationConfigMock, getSettingsMock, listTLSProfilesMock, authIsSimpleMode } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  getWebSearchEmulationConfigMock: vi.fn(),
  getSettingsMock: vi.fn(),
  listTLSProfilesMock: vi.fn(),
  authIsSimpleMode: { value: true }
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

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    }
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    settings: {
      getSettings: getSettingsMock,
      getWebSearchEmulationConfig: getWebSearchEmulationConfigMock
    },
    accounts: {
      update: updateAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock
    },
    tlsFingerprintProfiles: {
      list: listTLSProfilesMock
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

import EditAccountModal from '../EditAccountModal.vue'

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

function buildAccount() {
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

function buildOpenAISparkShadowAccount() {
  const account = buildAccount()
  return {
    ...account,
    id: 4,
    name: 'OpenAI Spark Shadow',
    type: 'oauth',
    parent_account_id: 1,
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

function buildOpenAIOAuthAccount() {
  const account = buildAccount()
  return {
    ...account,
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

function buildVertexAccount() {
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

function buildQoderAccount() {
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

function buildGrokOAuthAccount() {
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

function buildGrokAPIKeyAccount() {
  return {
    ...buildAccount(),
    id: 6,
    name: 'Grok API Key',
    platform: 'grok',
    credentials: {},
    credentials_status: { has_api_key: true },
    concurrency: 2
  } as any
}
function mountModal(account = buildAccount()) {
  return mount(EditAccountModal, {
    props: {
      show: true,
      account,
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

describe('EditAccountModal', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    getWebSearchEmulationConfigMock.mockReset()
    getSettingsMock.mockReset()
    listTLSProfilesMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    getSettingsMock.mockResolvedValue({ account_quota_notify_enabled: false })
    getWebSearchEmulationConfigMock.mockResolvedValue({ enabled: false, providers: [] })
    listTLSProfilesMock.mockResolvedValue([])
  })

  it('renders the shared account model rule copy', async () => {
    const account = buildAccount()
    account.credentials.model_whitelist = []
    const wrapper = mountModal(account)

    expect(wrapper.text()).toContain('admin.accounts.modelRestriction')
    expect(wrapper.text()).toContain('admin.accounts.modelWhitelist')
    expect(wrapper.text()).toContain('admin.accounts.modelRestrictionCombinedHint')
    expect(wrapper.text()).toContain('admin.accounts.supportsAllModels')

    const mappingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.modelMapping'))
    expect(mappingButton).toBeTruthy()
    await mappingButton!.trigger('click')
    expect(wrapper.text()).toContain('admin.accounts.mapRequestModels')
  })

  it('allows concurrency and load factor to be cleared before entering replacement values', async () => {
    const account = {
      ...buildAccount(),
      concurrency: 6,
      load_factor: 6
    }
    updateAccountMock.mockResolvedValue(account)
    const wrapper = mountModal(account)
    const concurrencyInput = wrapper.get<HTMLInputElement>(
      '[data-testid="edit-account-concurrency"]'
    )
    const loadFactorInput = wrapper.get<HTMLInputElement>(
      '[data-testid="edit-account-load-factor"]'
    )

    await concurrencyInput.setValue('')
    await loadFactorInput.setValue('')
    expect(concurrencyInput.element.value).toBe('')
    expect(loadFactorInput.element.value).toBe('')

    await concurrencyInput.setValue('12')
    await loadFactorInput.setValue('9')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]).toMatchObject({
      concurrency: 12,
      load_factor: 9
    })
  })

  it('reopening the same account rehydrates the OpenAI whitelist from props', async () => {
    const account = buildAccount()
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2-2025-12-11')

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })

    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual(['gpt-5.2'])
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
  })

  // 回显与保存都应保留管理员配置的分协议地址，不能被默认端点覆盖。
  it.each(['kimi', 'zhipu', 'deepseek'])('%s adaptive 账号回显并保存自定义端点', async platform => {
    const account = buildAccount()
    account.platform = platform
    const endpoints = {
      chat_completions: 'https://relay.example.test/v1',
      anthropic: 'https://relay.example.test/anthropic',
      ...(platform !== 'zhipu' ? { responses: 'https://relay.example.test/responses' } : {})
    }
    account.credentials = {
      api_key: 'sk-cn',
      account_mode: 'payg',
      upstream_protocols: platform === 'zhipu' ? ['anthropic_messages','openai_chat_completions'] : ['anthropic_messages','openai_responses','openai_chat_completions'],
      base_url: endpoints.chat_completions,
      api_base_urls: endpoints
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    const inputValues = wrapper.findAll<HTMLInputElement>('input[type="text"]').map(input => input.element.value)
    for (const endpoint of Object.values(endpoints)) expect(inputValues).toContain(endpoint)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      account_mode: 'payg',
      upstream_protocols: platform === 'zhipu' ? ['anthropic_messages','openai_chat_completions'] : ['anthropic_messages','openai_responses','openai_chat_completions'],
      base_url: endpoints.chat_completions,
      api_base_urls: endpoints
    })
  })

  it.each(['payg', 'coding'])('Kimi %s 原生 Responses 回显和保存不回退协议', async mode => {
    const account = buildAccount()
    account.platform = 'kimi'
    account.credentials = {
      api_key: 'sk-cn', account_mode: mode, upstream_protocols: ['openai_responses'], api_base_urls: { responses: 'https://relay.example.test/responses' },
      base_url: 'https://relay.example.test/responses'
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(account)
    expect(wrapper.findAll<HTMLInputElement>('input[type="text"]').map(input => input.element.value))
      .toContain('https://relay.example.test/responses')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      account_mode: mode, upstream_protocols: ['openai_responses'], api_base_urls: { responses: 'https://relay.example.test/responses' }, base_url: 'https://relay.example.test/responses'
    })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toHaveProperty('api_base_urls')
  })

  it('preserves adaptive GLM endpoints on submit', async () => {
    const account = buildAccount()
    account.platform = 'zhipu'
    account.credentials = {
      api_key: 'sk-glm',
      account_mode: 'coding',
      upstream_protocols: ['anthropic_messages','openai_chat_completions'],
      base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
      api_base_urls: {
        chat_completions: 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropic: 'https://open.bigmodel.cn/api/anthropic'
      }
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      account_mode: 'coding',
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
  ])('preserves a custom CN relay for %s accounts', async (_name, storedProtocol) => {
    const account = buildAccount()
    account.platform = 'zhipu'
    account.credentials = {
      api_key: 'sk-glm',
      account_mode: 'payg',
      base_url: 'https://relay.example.com/v1'
    }
    if (storedProtocol) {
      account.credentials.api_protocol = storedProtocol
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const submittedCredentials = updateAccountMock.mock.calls[0]?.[1]?.credentials
    expect(submittedCredentials).toMatchObject({
      account_mode: 'payg',
      upstream_protocols: expect.arrayContaining(['openai_chat_completions']),
      base_url: 'https://relay.example.com/v1'
    })
    expect(submittedCredentials).toHaveProperty('upstream_protocols')
  })

  it('uses the legacy base_url when adaptive endpoints are missing', async () => {
    const account = buildAccount()
    account.platform = 'zhipu'
    account.credentials = {
      api_key: 'sk-glm',
      account_mode: 'payg',
      upstream_protocols: ['anthropic_messages','openai_chat_completions'],
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: '   '
      }
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
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
    const account = buildAccount()
    account.platform = testCase.platform
    account.credentials = {
      api_key: 'sk-cn',
      account_mode: 'payg',
      upstream_protocols: [testCase.protocol === 'anthropic' ? 'anthropic_messages' : 'openai_responses'],
      api_base_urls: testCase.expectedProtocolUrls,
      base_url: testCase.expectedBaseUrl
    }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    for (const checkbox of wrapper.findAll('[data-native-protocol]')) await checkbox.setValue(true)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      upstream_protocols: testCase.platform === 'zhipu' ? ['anthropic_messages','openai_chat_completions'] : ['anthropic_messages','openai_responses','openai_chat_completions'],
      base_url: testCase.expectedBaseUrl,
      api_base_urls: testCase.expectedProtocolUrls
    })
  })

  it('preserves model mappings when editing the whitelist', async () => {
    const account = buildAccount()
    account.credentials = {
      ...account.credentials,
      model_whitelist: ['gpt-5.2'],
      model_mapping: {
        'gpt-latest': 'gpt-5.2'
      }
    }
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const whitelistButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.modelWhitelist'))
    expect(whitelistButton).toBeTruthy()

    await whitelistButton!.trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.2')

    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual(['gpt-5.2-2025-12-11'])
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-latest': 'gpt-5.2'
    })
  })

  it('压缩开关独立控制，旧版关闭时隐藏映射，协议至少保留一项', async () => {
    const wrapper = mountModal(buildAccount())
    const responses = wrapper.get<HTMLInputElement>('[data-native-protocol="openai_responses"]')
    const chat = wrapper.get<HTMLInputElement>('[data-native-protocol="openai_chat_completions"]')
    await chat.setValue(false)
    expect(responses.element.checked).toBe(true)
    expect(responses.element.disabled).toBe(false)
    await chat.setValue(true)
    expect(responses.element.disabled).toBe(false)
    await wrapper.get('[data-testid="edit-openai-compact-mode"]').setValue(false)
    expect(wrapper.text()).not.toContain('admin.accounts.openai.compactModelMapping')
    expect(wrapper.get<HTMLInputElement>('[data-testid="edit-openai-native-compaction-v2-mode"]').element.checked).toBe(true)
    await wrapper.get('[data-testid="edit-openai-compact-mode"]').setValue(true)
    expect(wrapper.find('[data-testid="openai-responses-probe-status"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('submits independent OpenAI native V2 and legacy compact settings', async () => {
    const account = buildAccount()
    account.extra = {
      openai_compact_mode: 'force_on',
      openai_native_compaction_v2_mode: 'force_off'
    }
    account.credentials = {
      ...account.credentials,
      compact_model_mapping: {
        'gpt-5.4': 'gpt-5.4-openai-compact'
      }
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_compact_mode).toBe('force_on')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_native_compaction_v2_mode).toBe('force_off')
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.compact_model_mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-openai-compact'
    })
  })

  it('does not render or resubmit the removed account-level long-context setting', async () => {
    const account = buildAccount()
    account.extra = {
      openai_long_context_billing_enabled: true,
      preserved: 'value'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_long_context_billing_enabled')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.preserved).toBe('value')
  })

  it('loads and clears the OAuth-only Codex namespace flatten toggle', async () => {
    const account = buildAccount()
    account.type = 'oauth'
    account.extra = { openai_responses_flatten_namespaces: true }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('[data-testid="edit-openai-flatten-namespaces-toggle"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_responses_flatten_namespaces'
    )
  })

  it('submits the Codex namespace flatten toggle when switched on', async () => {
    const account = buildAccount()
    account.type = 'oauth'
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)
    await wrapper.get('[data-testid="edit-openai-flatten-namespaces-toggle"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_flatten_namespaces).toBe(true)
  })

  it('writes the upstream request id header into extra only when it changes', async () => {
    const account = buildAccount()
    account.extra = { openai_compact_mode: 'force_on' }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const untouched = mountModal(account)
    await untouched.get('form#edit-account-form').trigger('submit.prevent')
    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.upstream_request_id_header).toBeUndefined()

    updateAccountMock.mockClear()
    const wrapper = mountModal(account)
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue(' X-Oneapi-Request-Id ')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toMatchObject({
      openai_compact_mode: 'force_on',
      upstream_request_id_header: 'X-Oneapi-Request-Id'
    })
  })

  it('removes the upstream request id header from extra when cleared', async () => {
    const account = buildAccount()
    account.extra = { upstream_request_id_header: 'X-Request-ID' }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    expect((wrapper.get('[data-testid="upstream-request-id-header"]').element as HTMLInputElement).value).toBe('X-Request-ID')
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue('')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toBeDefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('upstream_request_id_header')
  })

  it('hides the Codex namespace flatten toggle for non-OAuth OpenAI accounts', async () => {
    const account = buildAccount()
    const wrapper = mountModal(account)

    expect(wrapper.find('[data-testid="edit-openai-flatten-namespaces-toggle"]').exists()).toBe(false)
  })

  it('loads and submits Grok OAuth model mapping edits', async () => {
    const account = buildGrokOAuthAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    expect(wrapper.text()).toContain('Imagine Image')
    expect(wrapper.text()).toContain('Imagine Video')

    const inputWithValue = (value: string) => {
      const input = wrapper
        .findAll('input')
        .find((input) => (input.element as HTMLInputElement).value === value)
      expect(input).toBeTruthy()
      return input!
    }

    await inputWithValue('grok-latest').setValue('grok')
    await inputWithValue('grok-4.3').setValue('grok-build-0.1')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      grok: 'grok-build-0.1'
    })
  })

  it('uses the official xAI base URL when a Grok API-key account omits base_url', async () => {
    const account = buildGrokAPIKeyAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect((wrapper.get('input[placeholder="https://api.x.ai/v1"]').element as HTMLInputElement).value)
      .toBe('https://api.x.ai/v1')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.base_url).toBe('https://api.x.ai/v1')
  })

  it('only submits model mapping credentials when saving an OpenAI spark shadow account', async () => {
    authIsSimpleMode.value = false
    const account = buildOpenAISparkShadowAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.find('[data-testid="openai-plan-type-select"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="edit-codex-fingerprint-mode-select"]').exists()).toBe(false)

    await wrapper.get('[data-testid="set-shadow-group"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const payload = updateAccountMock.mock.calls[0]?.[1]
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

  it('loads and submits a manual plan type for a non-shadow OpenAI OAuth account', async () => {
    const account = buildOpenAIOAuthAccount()
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const planTypeSelect = wrapper.get<HTMLSelectElement>('[data-testid="openai-plan-type-select"]')

    expect(planTypeSelect.element.value).toBe('chatgptpro')

    await planTypeSelect.setValue('free')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      email: 'oauth@example.com',
      plan_type: 'free',
      model_whitelist: ['gpt-5.4']
    })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
  })

  it('OpenAI OAuth 编辑白名单时提交独立 model_whitelist', async () => {
    const account = buildOpenAIOAuthAccount()
    account.credentials = {
      ...account.credentials,
      model_mapping: {
        'codex-alias': 'gpt-5.4'
      },
      model_whitelist: ['gpt-5.4']
    }
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const whitelistButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.modelWhitelist'))
    expect(whitelistButton).toBeTruthy()

    await whitelistButton!.trigger('click')
    expect(wrapper.get('[data-testid="model-whitelist-value"]').text()).toBe('gpt-5.4')
    await wrapper.get('[data-testid="rewrite-to-snapshot"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'codex-alias': 'gpt-5.4'
    })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([
      'gpt-5.2-2025-12-11'
    ])
  })

  it('OpenAI OAuth 在独立白名单格式中回填自映射规则', async () => {
    const account = buildOpenAIOAuthAccount()
    account.credentials = {
      ...account.credentials,
      model_mapping: {
        'gpt-5.6-sol': 'gpt-5.6-sol'
      },
      model_whitelist: []
    }
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const mappingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.modelMapping'))
    expect(mappingButton).toBeTruthy()

    await mappingButton!.trigger('click')

    const requestInput = wrapper
      .findAll('input')
      .find((input) => input.attributes('placeholder') === 'admin.accounts.requestModel')
    const targetInput = wrapper
      .findAll('input')
      .find((input) => input.attributes('placeholder') === 'admin.accounts.actualModel')
    expect(requestInput).toBeTruthy()
    expect(targetInput).toBeTruthy()
    expect((requestInput!.element as HTMLInputElement).value).toBe('gpt-5.6-sol')
    expect((targetInput!.element as HTMLInputElement).value).toBe('gpt-5.6-sol')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-5.6-sol': 'gpt-5.6-sol'
    })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
  })

  it('loads and submits the Codex fingerprint mode for OpenAI OAuth accounts', async () => {
    const account = buildOpenAIOAuthAccount()
    account.extra = { codex_fingerprint_mode: 'device' }
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const modeSelect = wrapper.get<HTMLSelectElement>(
      '[data-testid="edit-codex-fingerprint-mode-select"]'
    )

    expect(modeSelect.element.value).toBe('device')
    await modeSelect.setValue('full')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_fingerprint_mode).toBe('full')
  })

  it('does not show the plan type override for OpenAI API-key accounts', () => {
    const wrapper = mountModal(buildAccount())

    expect(wrapper.find('[data-testid="openai-plan-type-select"]').exists()).toBe(false)
  })

  it('renders the account scheduling threshold override as a switch', () => {
    const wrapper = mountModal(buildAccount())

    expect(wrapper.get('[data-testid="account-scheduling-threshold-override-enabled"]').attributes('role')).toBe('switch')
  })

  // 关闭开关时删除显式 opt-in，并保留其他账号配置。
  it('hydrates and disables image URL backfill independently', async () => {
    const account = buildAccount()
    account.extra = { images_url_to_b64_json: true, retained: 'value' }
    updateAccountMock.mockReset().mockResolvedValue(account)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(account)
    const toggle = wrapper.get('[data-testid="edit-openai-images-url-to-b64-json"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()
    const extra = updateAccountMock.mock.calls[0]?.[1]?.extra
    expect(extra).not.toHaveProperty('images_url_to_b64_json')
    expect(extra.retained).toBe('value')
  })

  it('submits administrator text protocols and removes probe state', async () => {
    const account = buildAccount()
    account.extra = {
      openai_text_route_mode: 'force_chat_completions',
      openai_responses_probe_status: 'unsupported',
      openai_responses_continuation_supported: true,
      images_url_to_b64_json: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.get('[data-testid="edit-openai-continuation-supported"]').attributes('role')).toBe('switch')
    await wrapper.get('[data-native-protocol="openai_responses"]').setValue(true)
    await wrapper.get('[data-native-protocol="openai_chat_completions"]').setValue(false)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_text_route_mode).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_probe_status).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_continuation_supported).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.images_url_to_b64_json).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_supported')
  })

  it('does not render the removed upstream billing auto-probe setting', () => {
    const wrapper = mountModal(buildAccount())

    expect(wrapper.find('[data-testid="upstream-billing-auto-probe"]').exists()).toBe(false)
  })

  it('hydrates the native set and submits only the unified shape', async () => {
    const account = buildAccount()
    account.credentials.upstream_protocols = ['openai_chat_completions']
    account.extra = {
      openai_responses_mode: 'force_chat_completions',
      openai_responses_supported: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    const responses = wrapper.get<HTMLInputElement>('[data-native-protocol="openai_responses"]')
    expect(responses.element.checked).toBe(false)
    expect(wrapper.find('[data-testid="openai-responses-probe-status"]').exists()).toBe(false)
    await responses.setValue(true)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_text_route_mode).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_probe_status).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.openai_responses_continuation_supported).toBe(false)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_supported')
  })

  it('submits OpenAI APIKey endpoint capabilities from credentials', async () => {
    const account = buildAccount()
    account.credentials.upstream_protocols = ['openai_responses', 'openai_chat_completions']
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.findAll('input[type="checkbox"]').some((input) => (input.element as HTMLInputElement).checked)).toBe(true)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.upstream_protocols).toEqual(['openai_responses', 'openai_chat_completions'])
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('openai_capabilities')
  })

	it('submits OpenAI quota auto-pause thresholds in extra', async () => {
	  const account = buildAccount()
	  account.extra = {
		auto_pause_5h_threshold: 0.9,
		auto_pause_7d_threshold: 0.8
	  }
	  updateAccountMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateAccountMock.mockResolvedValue(account)

	  const wrapper = mountModal(account)

	  await wrapper.get('[data-testid="auto-pause-5h-threshold"]').setValue('95')
	  await wrapper.get('[data-testid="auto-pause-7d-threshold"]').setValue('96')
	  await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

	  expect(updateAccountMock).toHaveBeenCalledTimes(1)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_threshold).toBe(0.95)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_threshold).toBe(0.96)
	})

	it('submits OpenAI quota auto-pause disable flag in extra', async () => {
	  // 切换账号级禁用标记时必须持久化为 auto_pause_5h_disabled，
	  // 这样即使配置了全局默认阈值，管理员也能让单个账号豁免自动暂停；
	  // 否则阈值留空会静默回退到全局默认值。
	  const account = buildAccount()
	  updateAccountMock.mockReset()
	  checkMixedChannelRiskMock.mockReset()
	  checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
	  updateAccountMock.mockResolvedValue(account)

	  const wrapper = mountModal(account)

	  await wrapper.get('[data-testid="auto-pause-5h-disabled"]').trigger('click')
	  await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

	  expect(updateAccountMock).toHaveBeenCalledTimes(1)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_5h_disabled).toBe(true)
	  expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.auto_pause_7d_disabled).toBeUndefined()
	})

  it('preserves an explicitly empty native protocol set', async () => {
    const account = buildAccount()
    account.credentials = { ...account.credentials, upstream_protocols: [] }
    const wrapper = mountModal(account)
    await flushPromises()
    expect(wrapper.findAll('[data-native-protocol]').every(input => !(input.element as HTMLInputElement).checked)).toBe(true)
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.upstream_protocols).toEqual([])
  })

  it('submits Codex image tool force-inject mode as bridge override', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_bridge: false,
      codex_image_generation_bridge_enabled: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageTool')
    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolDesc')
    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolEnabledDesc')

    await wrapper.get('button[data-testid="codex-image-tool-enabled"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge_enabled')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool no-injection mode without strip policy', async () => {
    const account = buildAccount()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-disabled"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_bridge).toBe(false)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
  })

  it('submits Codex image tool block mode as strip policy and clears bridge override', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_bridge: true
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolBlock')
    expect(wrapper.text()).toContain('admin.accounts.openai.codexImageToolBlockDesc')

    await wrapper.get('button[data-testid="codex-image-tool-block"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_explicit_tool_policy).toBe('strip')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('loads strip policy as block mode and clears both keys when reset to inherit', async () => {
    const account = buildAccount()
    account.extra = {
      codex_image_generation_explicit_tool_policy: 'strip'
    }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('button[data-testid="codex-image-tool-inherit"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_explicit_tool_policy')
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('codex_image_generation_bridge')
  })

  it('loads and submits OpenAI OAuth TLS fingerprint settings', async () => {
    const account = buildAccount()
    account.type = 'oauth'
    account.name = 'OpenAI OAuth'
    account.credentials = {
      access_token: 'oauth-token',
      chatgpt_account_id: 'chatgpt-acc'
    }
    account.extra = {
      enable_tls_fingerprint: true,
      tls_fingerprint_profile_id: -1
    }
    account.enable_tls_fingerprint = true
    account.tls_fingerprint_profile_id = -1
    listTLSProfilesMock.mockResolvedValue([{ id: 7, name: 'Profile 7' }])
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-openai-tls-fingerprint-profile"]').exists()).toBe(true)
    const profileSelect = wrapper.get('[data-testid="edit-openai-tls-fingerprint-profile"]')
    expect((profileSelect.element as HTMLSelectElement).value).toBe('-1')

    await profileSelect.setValue('7')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.enable_tls_fingerprint).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.tls_fingerprint_profile_id).toBe(7)
  })

  it('loads and submits Qoder COSY model mappings', async () => {
    const account = buildQoderAccount()
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'claude-opus-4-6': 'ultimate',
      auto: 'auto'
    })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.security_oauth_token).toBe('redacted')
  })

  it('switches Qoder site without deleting credentials or model mappings', async () => {
    const account = buildQoderAccount()
    updateAccountMock.mockResolvedValue(account)
    const wrapper = mountModal(account)

    await wrapper.get('[data-testid="edit-qoder-site-cn"]').trigger('click')
    expect(wrapper.text()).toContain('admin.accounts.qoder.site.changeWarning')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const credentials = updateAccountMock.mock.calls[0]?.[1]?.credentials
    expect(credentials.site).toBe('cn')
    expect(credentials.refresh_mode).toBe('cosy')
    expect(credentials.security_oauth_token).toBe('redacted')
    expect(credentials.model_mapping).toEqual({
      'claude-opus-4-6': 'ultimate',
      auto: 'auto'
    })
  })

  it('does not persist generated Qoder model mappings on unrelated edits', async () => {
    const account = buildQoderAccount()
    delete account.credentials.model_mapping
    delete account.credentials.model_whitelist
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.security_oauth_token).toBe('redacted')
  })

  it('does not persist generated Qoder model mappings after only viewing the mapping tab', async () => {
    const account = buildQoderAccount()
    delete account.credentials.model_mapping
    delete account.credentials.model_whitelist
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    const mappingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.accounts.modelMapping'))
    expect(mappingButton).toBeTruthy()

    await mappingButton!.trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toBeUndefined()
  })

  it('persists Qoder model_mapping after explicit mapping edit', async () => {
    const account = buildQoderAccount()
    delete account.credentials.model_mapping
    delete account.credentials.model_whitelist
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    const addMappingButton = wrapper.findAll('button').find(button => button.text().includes('admin.accounts.addMapping'))
    expect(addMappingButton).toBeTruthy()
    await addMappingButton!.trigger('click')

    const requestInput = wrapper.findAll('input').find(input => input.attributes('placeholder') === 'admin.accounts.requestModel')
    const targetInput = wrapper.findAll('input').find(input => input.attributes('placeholder') === 'admin.accounts.actualModel')
    expect(requestInput).toBeTruthy()
    expect(targetInput).toBeTruthy()
    await requestInput!.setValue('glm-5.2')
    await targetInput!.setValue('gm51model')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({ 'glm-5.2': 'gm51model' })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
  })

  it('loads and submits Qoder COSY TLS fingerprint settings', async () => {
    const account = buildQoderAccount()
    account.extra = {
      enable_tls_fingerprint: true,
      tls_fingerprint_profile_id: -1,
      tls_fingerprint_router_id: 9
    }
    listTLSProfilesMock.mockResolvedValue([{ id: 7, name: 'Profile 7' }])
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-openai-tls-fingerprint-profile"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="edit-openai-tls-fingerprint-router"]').exists()).toBe(false)
    const profileSelect = wrapper.get('[data-testid="edit-openai-tls-fingerprint-profile"]')
    expect((profileSelect.element as HTMLSelectElement).value).toBe('-1')

    await profileSelect.setValue('7')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.enable_tls_fingerprint).toBe(true)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.tls_fingerprint_profile_id).toBe(7)
    expect(updateAccountMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('tls_fingerprint_router_id')
  })

  it('allows saving apikey account when backend redacted api_key but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 api_key，credentials_status.has_api_key=true。
    const account = buildAccount()
    account.credentials = {
      base_url: 'https://api.openai.com',
      model_mapping: { 'gpt-5.2': 'gpt-5.2' }
    }
    account.credentials_status = { has_api_key: true }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    // 用户未输入新 key 时，payload 不带 api_key，由后端合并保留旧值。
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('api_key')
  })

  it('updates a Gemini API Key to a third-party provider and clears its tier', async () => {
    const account = {
      ...buildAccount(),
      platform: 'gemini',
      credentials: {
        api_key: 'AIza-test',
        base_url: 'https://generativelanguage.googleapis.com',
        tier_id: 'aistudio_free'
      }
    }
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-gemini-tier"]').exists()).toBe(true)
    await wrapper.get<HTMLSelectElement>('[data-testid="edit-gemini-provider-type"]').setValue('third_party')
    expect(wrapper.find('[data-testid="edit-gemini-tier"]').exists()).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateAccountMock).not.toHaveBeenCalled()

    await wrapper.get<HTMLInputElement>('[data-testid="edit-account-base-url"]').setValue('https://provider.example.test')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_type: 'third_party',
      base_url: 'https://provider.example.test'
    })
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('tier_id')
  })

  it('treats historical Gemini API Key accounts as official when switching back from a third-party provider', async () => {
    const account = {
      ...buildAccount(),
      platform: 'gemini',
      credentials: {
        api_key: 'provider-key',
        base_url: 'https://provider.example.test'
      }
    }
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)
    await flushPromises()

    const providerType = wrapper.get<HTMLSelectElement>('[data-testid="edit-gemini-provider-type"]')
    expect(providerType.element.value).toBe('official')
    await providerType.setValue('third_party')
    await providerType.setValue('official')
    await wrapper.get<HTMLSelectElement>('[data-testid="edit-gemini-tier-select"]').setValue('aistudio_paid')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_type: 'official',
      tier_id: 'aistudio_paid'
    })
  })

  it('allows saving apikey account against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.api_key 仍是明文，应允许保存。
    const account = buildAccount()
    // 显式确保没有 credentials_status。
    expect(account.credentials_status).toBeUndefined()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    // 旧后端响应未脱敏，原 api_key 会随 currentCredentials 一起传回去。
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.api_key).toBe('sk-test')
  })

  it('blocks apikey save when neither credentials_status nor legacy api_key indicates existence', async () => {
    const account = buildAccount()
    account.credentials = {
      base_url: 'https://api.openai.com'
    }
    // 既没有 credentials_status 也没有旧的 api_key。
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).not.toHaveBeenCalled()
  })

  it('allows saving Vertex SA account when backend redacted service_account_json but credentials_status reports it exists', async () => {
    // 新前端 + 新后端：响应已脱敏，credentials 里没有 service_account_json，credentials_status.has_service_account_json=true。
    const account = buildVertexAccount()
    account.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    account.credentials_status = { has_service_account_json: true }
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.project_id).toBe('demo-project')
  })

  it('allows saving Vertex SA account against legacy backend without credentials_status', async () => {
    // 新前端 + 旧后端：credentials_status 缺失，但 credentials.service_account_json 仍是明文，应允许保存。
    const account = buildVertexAccount()
    expect(account.credentials_status).toBeUndefined()
    expect(account.credentials.service_account_json).toBeTruthy()
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateAccountMock.mockResolvedValue(account)

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
  })

  it('blocks Vertex SA save when neither credentials_status nor legacy json indicates existence', async () => {
    const account = buildVertexAccount()
    account.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex'
    }
    // 既没有 credentials_status 也没有旧的 service_account_json。
    updateAccountMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(account)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).not.toHaveBeenCalled()
  })
})
