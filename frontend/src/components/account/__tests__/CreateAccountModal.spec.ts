import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const {
  createAccountMock,
  importCodexSessionMock,
  createOpenAICodexPATMock,
  getOpenAIOAuthImportDefaultsMock,
} = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  importCodexSessionMock: vi.fn(),
  createOpenAICodexPATMock: vi.fn(),
  getOpenAIOAuthImportDefaultsMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      importCodexSession: importCodexSessionMock,
      createOpenAICodexPAT: createOpenAICodexPATMock,
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({}),
      getOpenAIOAuthImportDefaults: getOpenAIOAuthImportDefaultsMock,
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([]),
    },
  },
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    modelValue: {
      type: Array,
      default: () => [],
    },
    platform: {
      type: String,
      default: '',
    },
    syncCredentials: {
      type: Object,
      default: undefined,
    },
  },
  emits: ['update:modelValue'],
  template: `
    <div>
      <button
        type="button"
        data-testid="set-openai-model-whitelist"
        @click="$emit('update:modelValue', ['gpt-5.4'])"
      >
        set whitelist
      </button>
      <button
        type="button"
        data-testid="clear-openai-model-whitelist"
        @click="$emit('update:modelValue', [])"
      >
        clear whitelist
      </button>
    </div>
  `,
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: {
    showManualOption: Boolean,
    showCodexSessionImportOption: Boolean,
    showAgentIdentityOption: Boolean,
    showCodexPatOption: Boolean,
    initialInputMethod: String,
  },
  data: () => ({ inputMethod: 'manual' }),
  emits: ['import-codex-session', 'import-codex-pat'],
  template: `
    <div>
      <button data-testid="import-codex-session" @click="$emit('import-codex-session', 'session-json')">session</button>
      <button data-testid="import-codex-pat" @click="$emit('import-codex-pat', 'pat-token')">pat</button>
    </div>
  `,
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: {
      type: [String, Number, Boolean, null],
      default: '',
    },
    options: {
      type: Array,
      default: () => [],
    },
  },
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
  `,
})

function mountModal() {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        ConfirmDialog: true,
        Select: SelectStub,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
        QuotaLimitCard: true,
      },
    },
  })
}

async function selectButtonByText(wrapper: ReturnType<typeof mountModal>, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button).toBeDefined()
  await button?.trigger('click')
}

async function submitApiKeyAccount(platform: 'openai' | 'anthropic') {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, platform === 'openai' ? 'OpenAI' : 'admin.accounts.claudeConsole')
  if (platform === 'openai') {
    await selectButtonByText(wrapper, 'API Key')
  }
  await wrapper.get('form#create-account-form input[type="text"]').setValue(`${platform} account`)
  await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
}

async function openCodexImportStep() {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'OpenAI')
  await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
  await wrapper.get('form#create-account-form').trigger('submit.prevent')
  return wrapper
}

// 用于验证异步默认配置加载与导入请求之间的时序。
function createDeferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

describe('CreateAccountModal OpenAI account options', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({ id: 42, platform: 'openai', type: 'apikey' })
    importCodexSessionMock.mockReset().mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      failed: 0,
      errors: [],
      warnings: [],
    })
    createOpenAICodexPATMock.mockReset().mockResolvedValue({})
    getOpenAIOAuthImportDefaultsMock
      .mockReset()
      .mockResolvedValue({ credentials: { model_whitelist: [] }, extra: {} })
  })

  it('submits the explicit OpenAI text protocol defaults with the new configuration shape', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload?.credentials?.upstream_protocols).toEqual(['openai_responses','openai_chat_completions','openai_embeddings','openai_images_generations','openai_images_edits','openai_responses_websocket','openai_responses_compact','openai_alpha_search'])
    expect(payload?.credentials).not.toHaveProperty('openai_capabilities')
    expect(payload?.extra?.openai_text_route_mode).toBeUndefined()
    expect(payload?.extra?.openai_responses_probe_status).toBeUndefined()
    expect(payload?.extra?.openai_responses_continuation_supported).toBe(false)
    expect(payload?.extra).not.toHaveProperty('openai_responses_mode')
    expect(payload?.extra).not.toHaveProperty('openai_responses_supported')
  })

  it('omits the upstream request id header from extra when left empty', async () => {
    await submitApiKeyAccount('openai')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('upstream_request_id_header')
  })

  it('sends the trimmed upstream request id header in extra when filled', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('openai account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue('  X-Oneapi-Request-Id  ')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.upstream_request_id_header).toBe('X-Oneapi-Request-Id')
  })

  it('stores the optional New API wallet token in credentials, never in Extra', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')

    await wrapper.get('[data-testid="upstream-usage-adapter"]').setValue('new_api')
    await wrapper.get('[data-testid="upstream-usage-base-url"]').setValue('https://usage.example.test')
    await wrapper.get('[data-testid="upstream-usage-wallet-access-token"]').setValue('wallet-pat')
    await wrapper.get('[data-testid="upstream-usage-wallet-user-id"]').setValue('42')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenAI wallet account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('relay-api-key')
    // 上面的选择器命中 API Key 输入框；钱包 PAT 单独使用 data-testid 覆盖其值。
    await wrapper.get('[data-testid="upstream-usage-wallet-access-token"]').setValue('wallet-pat')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload?.credentials?.new_api_user_access_token).toBe('wallet-pat')
    expect(payload?.credentials?.new_api_user_id).toBe('42')
    expect(payload?.extra?.upstream_usage_query).toEqual({
      enabled: true,
      adapter: 'new_api',
      base_url: 'https://usage.example.test'
    })
    expect(payload?.extra?.upstream_usage_query?.new_api_user_access_token).toBeUndefined()
  })

  it('renders workload and administrator protocols without probe state', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')

    expect(wrapper.text()).toContain('admin.protocols.nativeTitle')
    expect(wrapper.find('[data-native-protocol="anthropic_messages"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.accounts.openai.responsesContinuationSupported')
    expect(wrapper.text()).not.toContain('admin.accounts.openai.responsesProbeStatus')
    expect(wrapper.get('[data-testid="create-openai-continuation-supported"]').attributes('role')).toBe('switch')
  })

  // 开关需由管理员显式启用，创建请求持久化为布尔值。
  it('opts into image URL backfill for OpenAI API-key accounts', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    const toggle = wrapper.get('[data-testid="create-openai-images-url-to-b64-json"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Images account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.images_url_to_b64_json).toBe(true)
  })

  it('allows an explicitly empty native protocol set', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    for (const checkbox of wrapper.findAll('[data-native-protocol]')) await checkbox.setValue(false)
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenAI account')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials?.upstream_protocols).toEqual([])
  })

  it('does not render or submit the removed account-level long-context setting', async () => {
    const wrapper = await submitApiKeyAccount('openai')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)
    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('does not render or submit the removed upstream billing probe setting', async () => {
    const wrapper = await submitApiKeyAccount('openai')

    expect(wrapper.find('[data-testid="upstream-billing-auto-probe"]').exists()).toBe(false)
    expect(createAccountMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBeUndefined()
  })

  // namespace 摊平是 OAuth 专属兼容开关，API Key 由自己的协议桥处理。
  it('shows the Codex namespace flatten toggle only for OpenAI OAuth accounts', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(true)

    await selectButtonByText(wrapper, 'API Key')
    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(false)
  })

  // 默认提交协议须与后端保存矩阵一致，三个平台都不能漏测。
  it.each([
    { label: 'Kimi', platform: 'kimi', base: 'https://api.moonshot.cn/v1', anthropic: 'https://api.moonshot.cn/anthropic', responses: 'https://api.moonshot.cn/v1' },
    { label: 'GLM', platform: 'zhipu', base: 'https://open.bigmodel.cn/api/paas/v4', anthropic: 'https://open.bigmodel.cn/api/anthropic', responses: undefined },
    { label: 'DeepSeek', platform: 'deepseek', base: 'https://api.deepseek.com', anthropic: 'https://api.deepseek.com/anthropic', responses: 'https://api.deepseek.com' }
  ])('$label 默认提交完整 adaptive 端点', async ({ label, platform, base, anthropic, responses }) => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, label)
    await wrapper.get('form#create-account-form input[type="text"]').setValue(`${label} adaptive`)
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-cn')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]).toMatchObject({ platform, type: 'apikey' })
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      account_mode: 'payg',
      upstream_protocols: expect.arrayContaining(['anthropic_messages', 'openai_chat_completions']),
      base_url: base
    })
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials.api_base_urls).toEqual({
      chat_completions: base,
      anthropic,
      ...(responses ? { responses } : {})
    })
  })

  it.each(['payg', 'coding'])('Kimi %s 可选择并提交原生 Responses', async mode => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    if (mode === 'coding') await selectButtonByText(wrapper, 'admin.accounts.cnProviders.accountMode.coding')
    for (const checkbox of wrapper.findAll('[data-native-protocol]')) { if (checkbox.attributes('data-native-protocol') !== 'openai_responses') await checkbox.setValue(false) }
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Kimi Responses')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-cn')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      account_mode: mode,
      upstream_protocols: ['openai_responses'],
      base_url: mode === 'coding' ? 'https://api.kimi.com/coding/v1' : 'https://api.moonshot.cn/v1'
    })
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toHaveProperty('api_base_urls')
  })

  it('submits adaptive Kimi Coding Plan Responses endpoint', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await selectButtonByText(wrapper, 'admin.accounts.cnProviders.accountMode.coding')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Kimi coding')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-kimi-coding')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      account_mode: 'coding',
      upstream_protocols: expect.arrayContaining(['anthropic_messages', 'openai_chat_completions']),
      base_url: 'https://api.kimi.com/coding/v1',
      api_base_urls: {
        chat_completions: 'https://api.kimi.com/coding/v1',
        anthropic: 'https://api.kimi.com/coding',
        responses: 'https://api.kimi.com/coding/v1'
      }
    })
  })

  it('uses the edited adaptive Chat endpoint when previewing upstream models', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper
      .get('[data-testid="cn-adaptive-base-url-chat_completions"]')
      .setValue('https://relay.example.com/v1')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-relay')

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('syncCredentials')).toMatchObject({
      platform: 'kimi',
      type: 'apikey',
      base_url: 'https://relay.example.com/v1',
      api_key: 'sk-relay'
    })
  })

  it('exposes Agent Identity in the OpenAI authorization methods', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('OpenAI account')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props('showManualOption')).toBe(true)
    expect(flow.props('showCodexSessionImportOption')).toBe(true)
    expect(flow.props('showAgentIdentityOption')).toBe(true)
    expect(flow.props('showCodexPatOption')).toBe(true)
    expect(flow.props('initialInputMethod')).toBe('manual')
  })

  it.each([
    ['camelCase', { authMode: 'agentIdentity', agentIdentity: { agentRuntimeId: 'runtime' } }],
    ['nested identity without auth_mode', { agent_identity: { agent_runtime_id: 'runtime' } }],
  ])('accepts backend-compatible %s Agent Identity imports', async (_name, content) => {
    const wrapper = await openCodexImportStep()
    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    flow.vm.inputMethod = 'agent_identity'

    flow.vm.$emit('import-codex-session', JSON.stringify(content))
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
  })

  it('omits the OpenAI setting for non-OpenAI account creation', async () => {
    await submitApiKeyAccount('anthropic')

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('leaves Codex session import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it('leaves Codex PAT import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock).toHaveBeenCalledTimes(1)
    expect(createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled).toBeUndefined()
  })

  it.each([
    ['Session', 'import-codex-session', importCodexSessionMock],
    ['PAT', 'import-codex-pat', createOpenAICodexPATMock],
  ])('为 Codex %s 导入独立保存最终模型白名单', async (_name, triggerTestId, apiMock) => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="set-openai-model-whitelist"]').trigger('click')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await wrapper.get(`[data-testid="${triggerTestId}"]`).trigger('click')
    await flushPromises()

    expect(apiMock).toHaveBeenCalledTimes(1)
    expect(apiMock.mock.calls[0]?.[0]?.credential_extras).toMatchObject({
      model_whitelist: ['gpt-5.4'],
    })
    expect(apiMock.mock.calls[0]?.[0]?.credential_extras).not.toHaveProperty('model_mapping')
  })

  it('为 Codex Session 导入显式保存空模型白名单', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="clear-openai-model-whitelist"]').trigger('click')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.credential_extras).toMatchObject({
      model_whitelist: [],
    })
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.credential_extras).not.toHaveProperty('model_mapping')
  })

  it.each([
    ['Session', 'import-codex-session', importCodexSessionMock],
    ['PAT', 'import-codex-pat', createOpenAICodexPATMock],
  ])('等待 OpenAI OAuth 默认配置后再构造 Codex %s 凭据', async (_name, triggerTestId, apiMock) => {
    const deferred = createDeferred<{
      credentials: { model_whitelist: string[] }
      extra: Record<string, unknown>
    }>()
    getOpenAIOAuthImportDefaultsMock.mockImplementation(() => deferred.promise)

    const wrapper = await openCodexImportStep()
    await wrapper.get(`[data-testid="${triggerTestId}"]`).trigger('click')
    await flushPromises()

    expect(apiMock).not.toHaveBeenCalled()

    deferred.resolve({ credentials: { model_whitelist: ['gpt-5.2'] }, extra: {} })
    await flushPromises()

    expect(apiMock).toHaveBeenCalledTimes(1)
    expect(apiMock.mock.calls[0]?.[0]?.credential_extras?.model_whitelist).toEqual(['gpt-5.2'])
  })

  it('defaults Codex fingerprint convergence to off for OAuth imports', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    const modeSelect = wrapper.get<HTMLSelectElement>(
      '[data-testid="create-codex-fingerprint-mode-select"]'
    )
    expect(modeSelect.element.value).toBe('off')

    await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('codex_fingerprint_mode')
  })

  it('persists an explicit Codex fingerprint convergence mode for OAuth imports', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    await wrapper.get<HTMLSelectElement>('[data-testid="create-codex-fingerprint-mode-select"]').setValue('session')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.codex_fingerprint_mode).toBe('session')
  })

})

describe('CreateAccountModal Gemini API Key provider source', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({ id: 43, platform: 'gemini', type: 'apikey' })
  })

  it('creates a third-party Gemini API Key without an official tier', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="create-account-platform-gemini"]').trigger('click')
    await wrapper.get('[data-testid="create-gemini-apikey-type"]').trigger('click')

    expect(wrapper.findAll('[data-testid="create-gemini-tier"]').length).toBe(1)

    await wrapper.get<HTMLSelectElement>('[data-testid="create-gemini-provider-type"]').setValue('third_party')
    expect(wrapper.find('[data-testid="create-gemini-tier"]').exists()).toBe(false)

    await wrapper.get('form#create-account-form input[type="text"]').setValue('Third-party Gemini')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('provider-key')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).not.toHaveBeenCalled()

    await wrapper.get<HTMLInputElement>('[data-testid="create-account-base-url"]').setValue('https://provider.example.test')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      provider_type: 'third_party',
      base_url: 'https://provider.example.test',
      api_key: 'provider-key'
    })
    expect(createAccountMock.mock.calls[0]?.[0]?.credentials).not.toHaveProperty('tier_id')
  })
})
