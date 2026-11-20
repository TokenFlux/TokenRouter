import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { isSwitchOn, setSwitch } from '@/__tests__/helpers/switches'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import EditProviderModal from '../EditProviderModal.vue'

// EditProviderModal.spec.ts exercises mounted provider editing across platforms
// and authentication types. API replacements capture submitted credentials and
// extras while interactive control stubs preserve user events, allowing the tests
// to distinguish hydration defaults, masked secrets, explicit empty collections,
// and field validation from changes made only by opening a panel.

// Hoisted API replacements capture updates and provide deterministic settings,
// TLS profiles, and balance-alert responses for every mounted provider scenario.
const {
  updateProviderMock,
  checkMixedChannelRiskMock,
  getWebSearchEmulationConfigMock,
  getSettingsMock,
  listTLSProfilesMock,
  getBalanceAlertStatusMock,
} = vi.hoisted(() => ({
  getBalanceAlertStatusMock: vi.fn(),
  updateProviderMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  getWebSearchEmulationConfigMock: vi.fn(),
  getSettingsMock: vi.fn(),
  listTLSProfilesMock: vi.fn(),
}))

// coerceSelectStubValue restores the option value type from a native select string.
// Unknown strings pass through unchanged; matching options return their original number,
// boolean, null, or string value for component events.
function coerceSelectStubValue(
  value: string,
  options: unknown[],
): string | number | boolean | null {
  const option = (options as Array<Record<string, unknown>>).find(
    (item) => String(item.value ?? '') === value,
  )
  return option ? (option.value as string | number | boolean | null) : value
}

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    settings: {
      getSettings: getSettingsMock,
      getWebSearchEmulationConfig: getWebSearchEmulationConfigMock,
    },
    providers: {
      update: updateProviderMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock,
    },
    tlsFingerprintProfiles: {
      list: listTLSProfilesMock,
    },
  },
}))

vi.mock('@/api/admin/providers', () => ({
  getAntigravityDefaultModelMapping: vi.fn(),
  getUpstreamBalanceAlertStatus: getBalanceAlertStatusMock,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
      n: (value: number) => value.toLocaleString('en-US'),
    }),
  }
})

// BaseDialogStub renders the form and footer slots only while show is true. This
// reproduces reopen behavior while avoiding the production dialog transition and teleport
// lifecycle in form tests.
const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    // show hides the stub slots when false; its false default keeps omitted visibility from
    // exposing dialog controls.
    show: {
      type: Boolean,
      default: false,
    },
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

// ModelWhitelistSelectorStub exposes deterministic whitelist edit buttons and the current
// selected models. Emitted arrays represent deliberate user edits, letting tests
// distinguish selection changes from merely opening the editor.
const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    // modelValue receives the parent selection and defaults to an empty array; emitted
    // replacements are applied by the parent.
    modelValue: {
      type: Array,
      default: () => [],
    },
    // providerId is the saved identity offered for upstream sync; undefined withholds it.
    providerId: {
      type: Number,
      default: undefined,
    },
  },
  emits: ['update:modelValue', 'synced'],
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
  `,
})

// SelectStub preserves model values and change events through a native select. Option
// lookup restores non-string values before emitting both events, matching the provider
// form controls used by these tests.
const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    // modelValue receives the parent selection and defaults to empty text; the select stub
    // emits edits to update that parent.
    modelValue: {
      type: [String, Number, Boolean, null],
      default: '',
    },
    // options supplies the parent records rendered as select options; its empty default
    // produces no candidate options.
    options: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue', 'change'],
  methods: {
    coerceSelectStubValue,
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
  `,
})

// GroupSelectorStub emits a fixed group selection for shadow-provider submission
// assertions. Its visible button avoids unrelated group loading while exercising the same
// model-update event consumed by the dialog.
const GroupSelectorStub = defineComponent({
  name: 'GroupSelector',
  props: {
    // modelValue receives the parent selection and defaults to an empty array; emitted
    // replacements are applied by the parent.
    modelValue: {
      type: Array,
      default: () => [],
    },
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
  `,
})

// buildProvider builds an editable OpenAI API-key response with a saved whitelist.
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
      model_whitelist: ['gpt-5.2'],
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
  } as any
}

// buildOpenAISparkShadowProvider builds a shadow response containing parent secrets for
// omission assertions.
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
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark',
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact',
      },
    },
    group_ids: [],
  } as any
}

// buildOpenAIOAuthProvider builds an OAuth response with a subscription tier and self-
// mapping.
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
        'gpt-5.4': 'gpt-5.4',
      },
    },
  } as any
}

// buildVertexProvider builds a Vertex service-account response with valid project
// metadata.
function buildVertexProvider() {
  return {
    id: 2,
    name: 'Vertex SA',
    notes: '',
    platform: 'gemini',
    type: 'service_account',
    credentials: {
      service_account_json:
        '{"type":"service_account","client_email":"sa@example.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\\nMIIE\\n-----END PRIVATE KEY-----\\n"}',
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex',
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
  } as any
}

// buildQoderProvider builds configured COSY mappings with an explicit empty final
// whitelist.
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
        auto: 'auto',
      },
      model_whitelist: [],
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
  } as any
}

// buildGrokOAuthProvider builds a Grok OAuth response with a request model alias.
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
        'grok-latest': 'grok-4.3',
      },
    },
    extra: {},
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
  } as any
}

// buildGrokAPIKeyProvider builds a redacted Grok API-key response without an explicit
// endpoint.
function buildGrokAPIKeyProvider() {
  return {
    ...buildProvider(),
    id: 6,
    name: 'Grok API Key',
    platform: 'grok',
    credentials: {},
    credentials_status: { has_api_key: true },
    concurrency: 2,
  } as any
}
// mountModal mounts the provider editor with deterministic interactive control stubs. The
// default provider is an OpenAI API-key account; callers supply other responses to
// exercise platform-specific hydration and submission.
function mountModal(provider = buildProvider()) {
  return mount(EditProviderModal, {
    props: {
      show: true,
      provider,
      proxies: [],
      groups: [],
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        Icon: true,
        ProxySelector: true,
        GroupSelector: GroupSelectorStub,
        ModelWhitelistSelector: ModelWhitelistSelectorStub,
      },
    },
  })
}

describe('EditProviderModal', () => {
  // Stored modes hydrate independently, and a saved provider can reopen with the
  // same capabilities. A disabled Images protocol keeps its mode without enabling itself.
  it('round-trips independent image modes while retaining disabled protocols', async () => {
    const provider = buildProvider()
    provider.credentials = {
      ...provider.credentials,
      upstream_protocols: ['openai_images_generations'],
      images_generations_mode: 'non_streaming',
      images_edits_mode: 'streaming',
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    await wrapper.get('[data-settings-tab-button="request"]').trigger('click')
    const row = wrapper.get('[data-setting-row="image-mode-openai_images_generations"]')
    expect(row.get('select').element).toHaveProperty('value', 'non_streaming')
    updateProviderMock.mockImplementation(async (_id, patch) => ({ ...provider, ...patch }))
    await wrapper.get('form').trigger('submit.prevent')
    await flushPromises()
    const saved = updateProviderMock.mock.calls.at(-1)?.[1]
    expect(saved.credentials).toMatchObject({
      images_generations_mode: 'non_streaming',
      images_edits_mode: 'streaming',
      upstream_protocols: ['openai_images_generations'],
    })
    wrapper.unmount()
    const reopened = mountModal({ ...provider, ...saved })
    await flushPromises()
    await reopened.get('[data-settings-tab-button="request"]').trigger('click')
    expect(
      reopened.get('[data-setting-row="image-mode-openai_images_generations"] select').element,
    ).toHaveProperty('value', 'non_streaming')
    reopened.unmount()
  })

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
    getBalanceAlertStatusMock.mockReset().mockResolvedValue({ provider_id: 1 })
  })

  // A monthly-only quota is restored when the provider opens. Editing its amount retains
  // usage and its fixed schedule, while disabling the quota removes schedule fields.
  it('round-trips a monthly quota and clears its schedule when disabled', async () => {
    const provider = buildProvider()
    provider.extra = {
      quota_monthly_limit: 100,
      quota_monthly_used: 25,
      quota_monthly_start: '2026-10-01T00:00:00Z',
      quota_monthly_reset_mode: 'fixed',
      quota_monthly_reset_day: 31,
      quota_monthly_reset_hour: 9,
      quota_reset_timezone: 'Asia/Shanghai',
      quota_notify_monthly_enabled: true,
      quota_notify_monthly_threshold: 10,
      quota_notify_monthly_threshold_type: 'percentage',
    }
    updateProviderMock.mockResolvedValue(provider)
    const wrapper = mountModal(provider)
    await flushPromises()
    await wrapper.get('[data-settings-tab-button="quota"]').trigger('click')
    const monthlyRow = wrapper
      .findAllComponents({ name: 'QuotaDimensionRow' })
      .find((row) => row.props('dim') === 'monthly')!
    expect(monthlyRow.exists()).toBe(true)
    expect(monthlyRow.props('limit')).toBe(100)
    await monthlyRow.get('input[type="number"]').setValue('150')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0][1].extra).toMatchObject({
      ...provider.extra,
      quota_monthly_limit: 150,
    })
    await setSwitch(wrapper.get('[data-testid="quota-limit-toggle"]'), false)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    const cleared = updateProviderMock.mock.calls[1][1].extra
    expect(cleared).not.toHaveProperty('quota_monthly_limit')
    expect(cleared).not.toHaveProperty('quota_monthly_reset_mode')
    expect(cleared).not.toHaveProperty('quota_monthly_reset_day')
    wrapper.unmount()
  })

  // Derouter usage queries have their own management endpoint. Saving the mounted Anthropic
  // editor retains forwarding credentials separately and sends query settings under the
  // usage configuration.
  it('round-trips derouter query settings independently of the Anthropic forwarding URL', async () => {
    const provider = buildProvider()
    provider.platform = 'anthropic'
    provider.credentials.base_url = 'https://forward.example.test/anthropic'
    provider.extra = {
      upstream_usage_query: {
        enabled: true,
        adapter: 'derouter',
        base_url: 'https://old-management.example.test',
      },
    }
    updateProviderMock.mockResolvedValue(provider)
    const wrapper = mountModal(provider)
    await flushPromises()
    await wrapper.get('[data-settings-tab-button="quota"]').trigger('click')
    expect(
      (wrapper.get('[data-testid="upstream-usage-adapter"]').element as HTMLSelectElement).value,
    ).toBe('derouter')
    await wrapper
      .get('[data-testid="upstream-usage-base-url"]')
      .setValue('https://management.example.test')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    const payload = updateProviderMock.mock.calls[0][1]
    expect(payload.credentials.base_url).toBe('https://forward.example.test/anthropic')
    expect(payload.extra.upstream_usage_query).toEqual({
      enabled: true,
      adapter: 'derouter',
      base_url: 'https://management.example.test',
    })
    const saved = {
      ...provider,
      credentials: payload.credentials,
      extra: payload.extra,
      credentials_status: { has_api_key: true },
    }
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ provider: saved, show: true })
    await flushPromises()
    await wrapper.get('[data-settings-tab-button="quota"]').trigger('click')
    expect(
      (wrapper.get('[data-testid="upstream-usage-adapter"]').element as HTMLSelectElement).value,
    ).toBe('derouter')
    expect((wrapper.get('#edit-provider-base-url').element as HTMLInputElement).value).toBe(
      'https://forward.example.test/anthropic',
    )
    await wrapper.get('[data-testid="upstream-usage-base-url"]').setValue('')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock).toHaveBeenCalledTimes(2)
    expect(updateProviderMock.mock.calls[1][1].credentials.base_url).toBe(
      'https://forward.example.test/anthropic',
    )
    expect(updateProviderMock.mock.calls[1][1].extra.upstream_usage_query).toEqual({
      enabled: true,
      adapter: 'derouter',
    })
    wrapper.unmount()
  })

  // Jev credentials can omit the forwarding address or store an empty string. Both forms
  // hydrate the official endpoint rather than selecting another platform's default.
  it.each([undefined, ''])('Jev 地址为 %s 时加载官方默认值', async (baseUrl) => {
    const provider = {
      ...buildProvider(),
      platform: 'jev' as const,
      credentials: {
        api_key: 'test-jev-key',
        base_url: baseUrl,
        upstream_protocols: ['systemone'],
      },
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    expect((wrapper.get('#edit-provider-base-url').element as HTMLInputElement).value).toBe(
      'https://api.typesafe.ai',
    )
    wrapper.unmount()
  })

  // An administrator clears an existing Jev forwarding address before saving. The submitted
  // credential copy restores the Jev default endpoint and preserves the provider's remaining
  // configuration.
  it('清空 Jev 地址后保存官方默认值', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'jev' as const,
      credentials: {
        api_key: 'test-jev-key',
        base_url: 'https://custom.example',
        upstream_protocols: ['systemone'],
      },
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    await wrapper.get('#edit-provider-base-url').setValue('')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials.base_url).toBe(
      'https://api.typesafe.ai',
    )
    wrapper.unmount()
  })

  // The mounted model editor uses shared labels for whitelist and mapping controls. These
  // assertions keep platform-specific form composition from substituting inconsistent copy
  // for those choices.
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

  // Administrators clear numeric inputs before entering replacement values. Concurrency and
  // load-factor drafts allow that intermediate state and submit the entered replacements
  // through the provider update.
  it('allows concurrency and load factor to be cleared before entering replacement values', async () => {
    const provider = {
      ...buildProvider(),
      concurrency: 6,
      load_factor: 6,
    }
    updateProviderMock.mockResolvedValue(provider)
    const wrapper = mountModal(provider)
    const concurrencyInput = wrapper.get<HTMLInputElement>(
      '[data-testid="edit-provider-concurrency"]',
    )
    const loadFactorInput = wrapper.get<HTMLInputElement>(
      '[data-testid="edit-provider-load-factor"]',
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
      load_factor: 9,
    })
  })

  // Closing and reopening the same provider object starts a fresh edit. The whitelist
  // returns to the response value instead of retaining the previous unsaved selection.
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

  // CN providers store independent custom addresses for their supported protocols. Hydration
  // and submission retain those addresses for Kimi, Zhipu, and DeepSeek across the mounted
  // editor.
  it.each(['kimi', 'zhipu', 'deepseek'])(
    '%s adaptive 提供商回显并保存自定义端点',
    async (platform) => {
      const provider = buildProvider()
      provider.platform = platform
      const endpoints = {
        chat_completions: 'https://relay.example.test/v1',
        anthropic: 'https://relay.example.test/anthropic',
        ...(platform !== 'zhipu' ? { responses: 'https://relay.example.test/responses' } : {}),
      }
      provider.credentials = {
        api_key: 'sk-cn',
        provider_mode: 'payg',
        upstream_protocols:
          platform === 'zhipu'
            ? ['anthropic_messages', 'openai_chat_completions']
            : ['anthropic_messages', 'openai_responses', 'openai_chat_completions'],
        base_url: endpoints.chat_completions,
        api_base_urls: endpoints,
      }
      updateProviderMock.mockReset().mockResolvedValue(provider)
      checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

      const wrapper = mountModal(provider)
      const inputValues = wrapper
        .findAll<HTMLInputElement>('input[type="text"]')
        .map((input) => input.element.value)
      for (const endpoint of Object.values(endpoints)) expect(inputValues).toContain(endpoint)
      await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
      await flushPromises()

      expect(updateProviderMock).toHaveBeenCalledTimes(1)
      expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
        provider_mode: 'payg',
        upstream_protocols:
          platform === 'zhipu'
            ? ['anthropic_messages', 'openai_chat_completions']
            : ['anthropic_messages', 'openai_responses', 'openai_chat_completions'],
        base_url: endpoints.chat_completions,
        api_base_urls: endpoints,
      })
    },
  )

  // Kimi native Responses configuration is exercised in both billing modes. Saving keeps its
  // protocol selection and endpoint instead of replacing them with a different native
  // protocol.
  it.each(['payg', 'coding'])('Kimi %s 原生 Responses 回显和保存不回退协议', async (mode) => {
    const provider = buildProvider()
    provider.platform = 'kimi'
    provider.credentials = {
      api_key: 'sk-cn',
      provider_mode: mode,
      upstream_protocols: ['openai_responses'],
      api_base_urls: { responses: 'https://relay.example.test/responses' },
      base_url: 'https://relay.example.test/responses',
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    const wrapper = mountModal(provider)
    expect(
      wrapper.findAll<HTMLInputElement>('input[type="text"]').map((input) => input.element.value),
    ).toContain('https://relay.example.test/responses')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_mode: mode,
      upstream_protocols: ['openai_responses'],
      api_base_urls: { responses: 'https://relay.example.test/responses' },
      base_url: 'https://relay.example.test/responses',
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toHaveProperty('api_base_urls')
  })

  // An adaptive GLM provider has separate Chat Completions and Anthropic relay addresses.
  // Submitting its unchanged form preserves both protocol entries and the shared forwarding
  // address.
  it('preserves adaptive GLM endpoints on submit', async () => {
    const provider = buildProvider()
    provider.platform = 'zhipu'
    provider.credentials = {
      api_key: 'sk-glm',
      provider_mode: 'coding',
      upstream_protocols: ['anthropic_messages', 'openai_chat_completions'],
      base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
      api_base_urls: {
        chat_completions: 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropic: 'https://open.bigmodel.cn/api/anthropic',
      },
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_mode: 'coding',
      upstream_protocols: ['anthropic_messages', 'openai_chat_completions'],
      base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
      api_base_urls: {
        chat_completions: 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropic: 'https://open.bigmodel.cn/api/anthropic',
      },
    })
  })

  // A custom CN relay can accompany an explicit Chat Completions selector or legacy missing
  // protocol data. Both responses retain the relay through hydration and update submission.
  it.each([
    ['explicit Chat Completions', 'chat_completions'],
    ['legacy missing protocol', undefined],
  ])('preserves a custom CN relay for %s providers', async (_name, storedProtocol) => {
    const provider = buildProvider()
    provider.platform = 'zhipu'
    provider.credentials = {
      api_key: 'sk-glm',
      provider_mode: 'payg',
      base_url: 'https://relay.example.com/v1',
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
      base_url: 'https://relay.example.com/v1',
    })
    expect(submittedCredentials).toHaveProperty('upstream_protocols')
  })

  // Legacy CN credentials have a shared base URL but no adaptive endpoint map. Hydration
  // uses that address for its protocol slot while filling other slots from presets.
  it('uses the legacy base_url when adaptive endpoints are missing', async () => {
    const provider = buildProvider()
    provider.platform = 'zhipu'
    provider.credentials = {
      api_key: 'sk-glm',
      provider_mode: 'payg',
      upstream_protocols: ['anthropic_messages', 'openai_chat_completions'],
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: '   ',
      },
    }
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      upstream_protocols: ['anthropic_messages', 'openai_chat_completions'],
      base_url: 'https://relay.example.com/v1',
      api_base_urls: {
        chat_completions: 'https://relay.example.com/v1',
        anthropic: 'https://open.bigmodel.cn/api/anthropic',
      },
    })
  })

  // A CN provider stores a relay in one native protocol slot. Enabling additional protocols
  // retains that relay while saving each protocol-specific endpoint and the selected native
  // set.
  it.each([
    {
      name: 'Anthropic',
      platform: 'zhipu',
      protocol: 'anthropic',
      baseUrl: 'https://relay.example.com/anthropic',
      expectedBaseUrl: 'https://open.bigmodel.cn/api/paas/v4',
      expectedProtocolUrls: {
        chat_completions: 'https://open.bigmodel.cn/api/paas/v4',
        anthropic: 'https://relay.example.com/anthropic',
      },
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
        responses: 'https://relay.example.com/responses',
      },
    },
  ])(
    'keeps a fixed $name relay in its protocol slot when switching to Adaptive',
    async (testCase) => {
      const provider = buildProvider()
      provider.platform = testCase.platform
      provider.credentials = {
        api_key: 'sk-cn',
        provider_mode: 'payg',
        upstream_protocols: [
          testCase.protocol === 'anthropic' ? 'anthropic_messages' : 'openai_responses',
        ],
        api_base_urls: testCase.expectedProtocolUrls,
        base_url: testCase.expectedBaseUrl,
      }
      updateProviderMock.mockReset().mockResolvedValue(provider)
      checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

      const wrapper = mountModal(provider)
      for (const toggle of wrapper.findAll('[data-native-protocol]')) await setSwitch(toggle, true)
      await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
      await flushPromises()

      expect(updateProviderMock).toHaveBeenCalledTimes(1)
      expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
        upstream_protocols:
          testCase.platform === 'zhipu'
            ? ['anthropic_messages', 'openai_chat_completions']
            : ['anthropic_messages', 'openai_responses', 'openai_chat_completions'],
        base_url: testCase.expectedBaseUrl,
        api_base_urls: testCase.expectedProtocolUrls,
      })
    },
  )

  // An administrator changes the final whitelist while request mappings already exist.
  // Submission writes the new whitelist and keeps the independent alias-to-model mapping
  // unchanged.
  it('preserves model mappings when editing the whitelist', async () => {
    const provider = buildProvider()
    provider.credentials = {
      ...provider.credentials,
      model_whitelist: ['gpt-5.2'],
      model_mapping: {
        'gpt-latest': 'gpt-5.2',
      },
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([
      'gpt-5.2-2025-12-11',
    ])
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-latest': 'gpt-5.2',
    })
  })

  // Legacy and native compaction have separate switches in the mounted editor. Disabling
  // legacy compaction hides its mapping controls while native compaction remains enabled and
  // protocols remain selectable.
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
    expect(isSwitchOn(wrapper.get('[data-testid="edit-openai-native-compaction-v2-mode"]'))).toBe(
      true,
    )
    await setSwitch(wrapper.get('[data-testid="edit-openai-compact-mode"]'), true)
    expect(wrapper.find('[data-testid="openai-responses-probe-status"]').exists()).toBe(false)
    wrapper.unmount()
  })

  // An OpenAI provider stores different native and legacy compaction settings.
  // Submitting the untouched form retains both independent values and its saved
  // compact model mapping in the update payload.
  it('submits independent OpenAI native V2 and legacy compact settings', async () => {
    const provider = buildProvider()
    provider.extra = {
      openai_compact_mode: 'force_on',
      openai_native_compaction_v2_mode: 'force_off',
    }
    provider.credentials = {
      ...provider.credentials,
      compact_model_mapping: {
        'gpt-5.4': 'gpt-5.4-openai-compact',
      },
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_native_compaction_v2_mode).toBe(
      'force_off',
    )
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.compact_model_mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-openai-compact',
    })
  })

  // Stored extras contain a retired provider-level long-context billing flag. The form
  // exposes no matching control and removes that setting when constructing the current
  // update payload.
  it('does not render or resubmit the removed provider-level long-context setting', async () => {
    const provider = buildProvider()
    provider.extra = {
      openai_long_context_billing_enabled: true,
      preserved: 'value',
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_long_context_billing_enabled',
    )
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.preserved).toBe('value')
  })

  // An OAuth provider starts with namespace flattening enabled. The switch reflects that
  // saved state, and disabling it removes the field from submitted extras.
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
      'openai_responses_flatten_namespaces',
    )
  })

  // An OAuth provider starts without namespace flattening enabled. Switching the control on
  // writes its explicit true value into the provider's submitted extra settings.
  it('submits the Codex namespace flatten toggle when switched on', async () => {
    const provider = buildProvider()
    provider.type = 'oauth'
    updateProviderMock.mockReset().mockResolvedValue(provider)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)
    await wrapper.get('[data-testid="edit-openai-flatten-namespaces-toggle"]').trigger('click')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_flatten_namespaces).toBe(
      true,
    )
  })

  // The provider has compaction metadata and no upstream request-ID header override.
  // An untouched submission keeps the header absent, while entering a header trims
  // its value and retains the existing compaction setting.
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
    await wrapper
      .get('[data-testid="upstream-request-id-header"]')
      .setValue(' X-Oneapi-Request-Id ')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).toMatchObject({
      openai_compact_mode: 'force_on',
      upstream_request_id_header: 'X-Oneapi-Request-Id',
    })
  })

  // An administrator clears a saved upstream request-ID header override. Submission removes
  // that key from the copied extras so subsequent requests use the default header behavior.
  it('removes the upstream request id header from extra when cleared', async () => {
    const provider = buildProvider()
    provider.extra = { upstream_request_id_header: 'X-Request-ID' }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    expect(
      (wrapper.get('[data-testid="upstream-request-id-header"]').element as HTMLInputElement).value,
    ).toBe('X-Request-ID')
    await wrapper.get('[data-testid="upstream-request-id-header"]').setValue('')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).toBeDefined()
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'upstream_request_id_header',
    )
  })

  // An existing balance reminder is loaded without user changes. Saving unrelated provider
  // fields retains its saved policy instead of replacing it with a default alert object.
  it('retains an unchanged balance reminder in extra', async () => {
    const provider = buildProvider()
    provider.extra = {
      upstream_balance_alert: {
        enabled: true,
        threshold: 50,
        interval_minutes: 30,
        recipient_email: 'ops@example.com',
      },
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await flushPromises()
    expect(getBalanceAlertStatusMock).toHaveBeenCalledWith(1)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.upstream_balance_alert).toEqual(
      provider.extra.upstream_balance_alert,
    )
  })

  // An administrator disables an existing balance reminder in the mounted form. Submission
  // writes the disabled policy while retaining its other saved alert configuration values.
  it('submits a disabled balance reminder with its saved fields', async () => {
    const provider = buildProvider()
    provider.extra = {
      upstream_balance_alert: {
        enabled: true,
        threshold: 50,
        interval_minutes: 30,
        recipient_email: 'ops@example.com',
      },
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await setSwitch(wrapper.get('[data-testid="balance-alert-enabled"]'), false)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.upstream_balance_alert).toEqual({
      enabled: false,
      threshold: 50,
      interval_minutes: 30,
      recipient_email: 'ops@example.com',
    })
  })

  // An OpenAI API-key provider uses request controls without OAuth namespace normalization.
  // The mounted form hides the OAuth-only flattening toggle for that authentication type.
  it('hides the Codex namespace flatten toggle for non-OAuth OpenAI providers', async () => {
    const provider = buildProvider()
    const wrapper = mountModal(provider)

    expect(wrapper.find('[data-testid="edit-openai-flatten-namespaces-toggle"]').exists()).toBe(
      false,
    )
  })

  // A Grok OAuth account opens with stored model rewrites. Editing the mapping submits the
  // updated request-side rules through credentials while retaining the account's other
  // settings.
  it('loads and submits Grok OAuth model mapping edits', async () => {
    const provider = buildGrokOAuthProvider()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    // inputWithValue locates an existing mapping input by its current text.
    // The assertion fails immediately when hydration did not render the expected
    // row, before the test attempts to edit that field.
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
      grok: 'grok-build-0.1',
    })
  })

  // Grok API-key credentials can omit the forwarding address. The mounted editor chooses the
  // official xAI endpoint and saves that platform-specific fallback with the update.
  it('uses the official xAI base URL when a Grok API-key provider omits base_url', async () => {
    const provider = buildGrokAPIKeyProvider()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(
      (wrapper.get('input[placeholder="https://api.x.ai/v1"]').element as HTMLInputElement).value,
    ).toBe('https://api.x.ai/v1')

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.base_url).toBe('https://api.x.ai/v1')
  })

  // A Spark shadow displays credentials inherited from its parent. Saving a group edit
  // retains only its allowed mapping fields and omits inherited secrets and protocol normalization.
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
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark',
      },
      compact_model_mapping: {
        'gpt-5.3-codex-spark': 'gpt-5.3-codex-spark-compact',
      },
    })
  })

  // A non-shadow OpenAI OAuth account exposes a manual subscription tier. The mounted
  // selector restores the saved tier and submits the administrator's updated override in
  // credentials.
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
      model_whitelist: ['gpt-5.4'],
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toBeUndefined()
  })

  // An OpenAI OAuth whitelist edit changes final model eligibility. Submission keeps that
  // whitelist in its dedicated field rather than encoding it as request self-mappings.
  it('OpenAI OAuth 编辑白名单时提交独立 model_whitelist', async () => {
    const provider = buildOpenAIOAuthProvider()
    provider.credentials = {
      ...provider.credentials,
      model_mapping: {
        'codex-alias': 'gpt-5.4',
      },
      model_whitelist: ['gpt-5.4'],
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
      'codex-alias': 'gpt-5.4',
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([
      'gpt-5.2-2025-12-11',
    ])
  })

  // OpenAI OAuth credentials contain separate whitelist and mapping fields with self-
  // mappings. Hydration keeps those mappings visible and submission retains their distinct
  // persisted representations.
  it('OpenAI OAuth 在独立白名单格式中回填自映射规则', async () => {
    const provider = buildOpenAIOAuthProvider()
    provider.credentials = {
      ...provider.credentials,
      model_mapping: {
        'gpt-5.6-sol': 'gpt-5.6-sol',
      },
      model_whitelist: [],
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
      'gpt-5.6-sol': 'gpt-5.6-sol',
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
  })

  // An OpenAI OAuth account stores a Codex fingerprint selection. The mounted editor
  // restores that selection and submits the selected mode through its extra configuration.
  it('loads and submits the Codex fingerprint mode for OpenAI OAuth providers', async () => {
    const provider = buildOpenAIOAuthProvider()
    provider.extra = { codex_fingerprint_mode: 'device' }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    const modeSelect = wrapper.get<HTMLSelectElement>(
      '[data-testid="edit-codex-fingerprint-mode-select"]',
    )

    expect(modeSelect.element.value).toBe('device')
    await modeSelect.setValue('full')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.codex_fingerprint_mode).toBe('full')
  })

  // Subscription overrides apply to OpenAI OAuth accounts. Opening an API-key provider hides
  // the manual plan-type selector so that control cannot submit an irrelevant override.
  it('does not show the plan type override for OpenAI API-key providers', () => {
    const wrapper = mountModal(buildProvider())

    expect(wrapper.find('[data-testid="openai-plan-type-select"]').exists()).toBe(false)
  })

  // The provider editor renders a control for enabling its scheduling threshold
  // override. The located control exposes the switch role required for its
  // accessible interaction.
  it('renders the provider scheduling threshold override as a switch', () => {
    const wrapper = mountModal(buildProvider())

    expect(
      wrapper
        .get('[data-testid="provider-scheduling-threshold-override-enabled"]')
        .attributes('role'),
    ).toBe('switch')
  })

  // An OpenAI API-key provider enables image URL backfill in saved extras. Disabling its
  // switch removes only that setting and retains unrelated extra fields.
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

  // OpenAI credentials use administrator-selected native protocols while extras contain
  // historical probe settings. Submission preserves continuation and image options while
  // removing retired route and capability fields.
  it('submits administrator text protocols and removes probe state', async () => {
    const provider = buildProvider()
    provider.extra = {
      openai_text_route_mode: 'force_chat_completions',
      openai_responses_probe_status: 'unsupported',
      openai_responses_continuation_supported: true,
      images_url_to_b64_json: true,
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    expect(
      wrapper.get('[data-testid="edit-openai-continuation-supported"]').attributes('role'),
    ).toBe('switch')
    await setSwitch(wrapper.get('[data-native-protocol="openai_responses"]'), true)
    await setSwitch(wrapper.get('[data-native-protocol="openai_chat_completions"]'), false)
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_text_route_mode).toBeUndefined()
    expect(
      updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_probe_status,
    ).toBeUndefined()
    expect(
      updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_continuation_supported,
    ).toBe(true)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra?.images_url_to_b64_json).toBe(true)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_responses_supported',
    )
  })

  // The provider editor uses administrator-selected capabilities. Its mounted controls omit
  // the retired upstream billing auto-probe setting instead of inviting edits to an
  // unsupported field.
  it('does not render the removed upstream billing auto-probe setting', () => {
    const wrapper = mountModal(buildProvider())

    expect(wrapper.find('[data-testid="upstream-billing-auto-probe"]').exists()).toBe(false)
  })

  // A stored native protocol set coexists with legacy OpenAI routing extras. The mounted
  // selector restores the native set and submission removes the retired extra
  // representation.
  it('hydrates the native set and submits only the unified shape', async () => {
    const provider = buildProvider()
    provider.credentials.upstream_protocols = ['openai_chat_completions']
    provider.extra = {
      openai_responses_mode: 'force_chat_completions',
      openai_responses_supported: true,
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
    expect(
      updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_probe_status,
    ).toBeUndefined()
    expect(
      updateProviderMock.mock.calls[0]?.[1]?.extra?.openai_responses_continuation_supported,
    ).toBe(false)
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty('openai_responses_mode')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'openai_responses_supported',
    )
  })

  // An OpenAI API-key provider stores an explicit endpoint protocol list. Saving the mounted
  // editor keeps that list in credentials without adding the retired capability object.
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.upstream_protocols).toEqual([
      'openai_responses',
      'openai_chat_completions',
    ])
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty(
      'openai_capabilities',
    )
  })

  // OpenAI utilization thresholds are edited as percentages. The update converts those
  // values to fractions under their separate five-hour and seven-day extra fields.
  it('submits OpenAI quota auto-pause thresholds in extra', async () => {
    const provider = buildProvider()
    provider.extra = {
      auto_pause_5h_threshold: 0.9,
      auto_pause_7d_threshold: 0.8,
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

  // An OpenAI provider has an automatic-pause exemption switch. Editing it writes the
  // corresponding disabled flag into extras rather than changing the independent threshold
  // amount.
  it('submits OpenAI quota auto-pause disable flag in extra', async () => {
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

  // Stored credentials contain an explicit empty native protocol array. Saving preserves
  // that empty selection instead of replacing it with the platform catalog's default
  // protocols.
  it('preserves an explicitly empty native protocol set', async () => {
    const provider = buildProvider()
    provider.credentials = { ...provider.credentials, upstream_protocols: [] }
    const wrapper = mountModal(provider)
    await flushPromises()
    expect(wrapper.findAll('[data-native-protocol]').every((toggle) => !isSwitchOn(toggle))).toBe(
      true,
    )
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.upstream_protocols).toEqual([])
  })

  // An administrator selects forced Codex image-tool injection. The resulting extra settings
  // express a bridge override and keep independent policy fields consistent with that
  // choice.
  it('submits Codex image tool force-inject mode as bridge override', async () => {
    const provider = buildProvider()
    provider.extra = {
      codex_image_generation_bridge: false,
      codex_image_generation_bridge_enabled: true,
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'codex_image_generation_bridge_enabled',
    )
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'codex_image_generation_explicit_tool_policy',
    )
  })

  // An administrator selects the Codex image mode that avoids injection. Submission writes
  // its bridge behavior without introducing a strip policy that would block the tool.
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'codex_image_generation_explicit_tool_policy',
    )
  })

  // An administrator selects the Codex image-tool blocking mode. Submission writes the strip
  // policy and removes a conflicting bridge override from the provider update.
  it('submits Codex image tool block mode as strip policy and clears bridge override', async () => {
    const provider = buildProvider()
    provider.extra = {
      codex_image_generation_bridge: true,
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
    expect(
      updateProviderMock.mock.calls[0]?.[1]?.extra?.codex_image_generation_explicit_tool_policy,
    ).toBe('strip')
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'codex_image_generation_bridge',
    )
  })

  // A stored strip policy hydrates the blocked image-tool selection. Resetting it to inherit
  // removes both policy representations so the provider returns to inherited behavior.
  it('loads strip policy as block mode and clears both keys when reset to inherit', async () => {
    const provider = buildProvider()
    provider.extra = {
      codex_image_generation_explicit_tool_policy: 'strip',
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'codex_image_generation_explicit_tool_policy',
    )
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'codex_image_generation_bridge',
    )
  })

  // An OpenAI OAuth response contains TLS simulation settings. The mounted profile controls
  // restore them and submit the selected fingerprint configuration with other extra fields.
  it('loads and submits OpenAI OAuth TLS fingerprint settings', async () => {
    const provider = buildProvider()
    provider.type = 'oauth'
    provider.name = 'OpenAI OAuth'
    provider.credentials = {
      access_token: 'oauth-token',
      chatgpt_account_id: 'chatgpt-acc',
    }
    provider.extra = {
      enable_tls_fingerprint: true,
      tls_fingerprint_profile_id: -1,
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

  // A Qoder COSY provider opens with persisted model mappings. Saving the mounted mapping
  // editor retains those rules and its independent explicit whitelist representation.
  it('loads and submits Qoder COSY model mappings', async () => {
    const provider = buildQoderProvider()
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'claude-opus-4-6': 'ultimate',
      auto: 'auto',
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.security_oauth_token).toBe(
      'redacted',
    )
  })

  // An administrator switches a Qoder COSY provider's routing site. The update writes the
  // new site while retaining credentials and the provider's existing request model mappings.
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
      auto: 'auto',
    })
  })

  // Reading the saved observation displays disabled routes too. A manual sync replaces
  // those properties immediately, while the whitelist change is saved with the form.
  it('shows Qoder model properties and saves the synchronized whitelist on update', async () => {
    const provider = buildQoderProvider()
    delete provider.credentials.model_mapping
    const previous = {
      key: 'old-route',
      name: 'Previous model',
      enabled: false,
      default: false,
      reasoning: false,
      can_disable: false,
      default_thinking: false,
      efforts: null,
      input_limit: 1000,
      context_windows: null,
      vision: false,
    }
    provider.qoder_model_catalog_status = { state: 'fresh', models: [], model_details: [previous] }
    updateProviderMock.mockResolvedValue(provider)
    const wrapper = mountModal(provider)
    await flushPromises()
    expect(wrapper.text()).toContain('Previous model')
    expect(wrapper.text()).toContain('common.disabled')
    const selector = wrapper.findComponent(ModelWhitelistSelectorStub)
    selector.vm.$emit('synced', {
      models: ['qwen4-max'],
      sync_status: {
        state: 'fresh',
        model_details: [
          {
            ...previous,
            key: 'qmodel_latest',
            name: 'Qwen4-Max',
            alias: 'qwen4-max',
            enabled: true,
            reasoning: true,
            default_thinking: true,
            default_effort: 'high',
            efforts: ['low', 'high'],
            input_limit: 1000000,
            output_limit: 65536,
            context_windows: [200000, 1000000],
          },
        ],
      },
    })
    selector.vm.$emit('update:modelValue', ['qwen4-max'])
    await flushPromises()
    expect(wrapper.text()).not.toContain('Previous model')
    expect(wrapper.text()).toContain('Qwen4-Max')
    expect(wrapper.text()).toContain('1,000,000 / 65,536')
    expect(wrapper.text()).toContain('low / high')
    expect(updateProviderMock).not.toHaveBeenCalled()
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials.model_whitelist).toEqual([
      'qwen4-max',
    ])
    wrapper.unmount()
  })

  // A Qoder provider has no persisted model restrictions. Saving unrelated fields leaves
  // editor-generated defaults out of credentials so viewing the provider cannot create
  // restrictions.
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.security_oauth_token).toBe(
      'redacted',
    )
  })

  // A Qoder administrator opens the mapping tab without changing its rows. Saving still
  // omits generated model mappings because panel visibility does not count as an edit.
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

  // A Qoder administrator explicitly changes a mapping row. The touched state permits
  // submission of that mapping instead of treating it as an unconfigured editor default.
  it('persists Qoder model_mapping after explicit mapping edit', async () => {
    const provider = buildQoderProvider()
    delete provider.credentials.model_mapping
    delete provider.credentials.model_whitelist
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    const addMappingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('admin.providers.addMapping'))
    expect(addMappingButton).toBeTruthy()
    await addMappingButton!.trigger('click')

    const requestInput = wrapper
      .findAll('input')
      .find((input) => input.attributes('placeholder') === 'admin.providers.requestModel')
    const targetInput = wrapper
      .findAll('input')
      .find((input) => input.attributes('placeholder') === 'admin.providers.actualModel')
    expect(requestInput).toBeTruthy()
    expect(targetInput).toBeTruthy()
    await requestInput!.setValue('glm-5.2')
    await targetInput!.setValue('gm51model')

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'glm-5.2': 'gm51model',
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.model_whitelist).toEqual([])
  })

  // Qoder COSY supports client TLS simulation with saved profile settings. Hydration and
  // submission preserve those fields independently of the account's model restriction
  // editing.
  it('loads and submits Qoder COSY TLS fingerprint settings', async () => {
    const provider = buildQoderProvider()
    provider.extra = {
      enable_tls_fingerprint: true,
      tls_fingerprint_profile_id: -1,
      tls_fingerprint_router_id: 9,
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.extra).not.toHaveProperty(
      'tls_fingerprint_router_id',
    )
  })

  // A current server redacts the API key but reports its presence. Saving succeeds without
  // inserting a replacement secret into the outgoing credential copy.
  it('allows saving apikey provider when backend redacted api_key but credentials_status reports it exists', async () => {
    const provider = buildProvider()
    provider.credentials = {
      base_url: 'https://api.openai.com',
      model_mapping: { 'gpt-5.2': 'gpt-5.2' },
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
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('api_key')
  })

  // A Gemini API-key account switches from the official endpoint to a third-party relay.
  // Submission writes the relay source and removes its inapplicable AI Studio tier.
  it('updates a Gemini API Key to a third-party provider and clears its tier', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'gemini',
      credentials: {
        api_key: 'AIza-test',
        base_url: 'https://generativelanguage.googleapis.com',
        tier_id: 'aistudio_free',
      },
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await flushPromises()

    expect(wrapper.find('[data-testid="edit-gemini-tier"]').exists()).toBe(true)
    await wrapper
      .get<HTMLSelectElement>('[data-testid="edit-gemini-provider-type"]')
      .setValue('third_party')
    expect(wrapper.find('[data-testid="edit-gemini-tier"]').exists()).toBe(false)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()

    await wrapper
      .get<HTMLInputElement>('[data-testid="edit-provider-base-url"]')
      .setValue('https://provider.example.test')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_type: 'third_party',
      base_url: 'https://provider.example.test',
    })
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).not.toHaveProperty('tier_id')
  })

  // A historical Gemini account has no explicit source marker and opens as official.
  // Switching its source to third-party and back saves the selected official tier.
  it('treats historical Gemini API Key providers as official when switching back from a third-party provider', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'gemini',
      credentials: {
        api_key: 'provider-key',
        base_url: 'https://provider.example.test',
      },
    }
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)
    await flushPromises()

    const providerType = wrapper.get<HTMLSelectElement>('[data-testid="edit-gemini-provider-type"]')
    expect(providerType.element.value).toBe('official')
    await providerType.setValue('third_party')
    await providerType.setValue('official')
    await wrapper
      .get<HTMLSelectElement>('[data-testid="edit-gemini-tier-select"]')
      .setValue('aistudio_paid')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    await flushPromises()

    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toMatchObject({
      provider_type: 'official',
      tier_id: 'aistudio_paid',
    })
  })

  // A legacy server returns API-key text without credentials_status flags. The editor
  // accepts that stored credential and submits unrelated changes without requiring a
  // replacement key.
  it('allows saving apikey provider against legacy backend without credentials_status', async () => {
    const provider = buildProvider()
    expect(provider.credentials_status).toBeUndefined()
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })
    updateProviderMock.mockResolvedValue(provider)

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).toHaveBeenCalledTimes(1)
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials?.api_key).toBe('sk-test')
  })

  // Neither a presence flag nor legacy credential text indicates an API key. Submission
  // stops before the update API instead of saving an unusable account.
  it('blocks apikey save when neither credentials_status nor legacy api_key indicates existence', async () => {
    const provider = buildProvider()
    provider.credentials = {
      base_url: 'https://api.openai.com',
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()
  })

  // A current server redacts service-account JSON but supplies a presence flag. The Vertex
  // form saves project and location metadata without requiring the hidden secret again.
  it('allows saving Vertex SA provider when backend redacted service_account_json but credentials_status reports it exists', async () => {
    const provider = buildVertexProvider()
    provider.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex',
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

  // A legacy server returns service-account JSON without presence flags. Vertex validation
  // accepts that saved credential and permits the administration update to proceed normally.
  it('allows saving Vertex SA provider against legacy backend without credentials_status', async () => {
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

  // A Vertex provider has no presence flag or saved service-account JSON. Validation
  // prevents the update request and leaves the credential fields available for correction.
  it('blocks Vertex SA save when neither credentials_status nor legacy json indicates existence', async () => {
    const provider = buildVertexProvider()
    provider.credentials = {
      project_id: 'demo-project',
      client_email: 'sa@example.iam.gserviceaccount.com',
      location: 'us-central1',
      tier_id: 'vertex',
    }
    updateProviderMock.mockReset()
    checkMixedChannelRiskMock.mockReset()
    checkMixedChannelRiskMock.mockResolvedValue({ has_risk: false })

    const wrapper = mountModal(provider)

    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()
  })

  // Providers expose only settings categories relevant to their authentication type. API-key
  // and OAuth forms therefore show different quota tabs while retaining their common
  // categories.
  it('按账号类型隐藏没有内容的页签', async () => {
    // tabKeys reads visible category identifiers from the mounted settings-tab buttons.
    const tabKeys = (wrapper: ReturnType<typeof mountModal>) =>
      wrapper
        .findAll('[data-settings-tab-button]')
        .map((tab) => tab.attributes('data-settings-tab-button'))
    const apiKeyWrapper = mountModal(buildProvider())
    await flushPromises()
    expect(tabKeys(apiKeyWrapper)).toEqual(['basic', 'models', 'scheduling', 'quota', 'request'])
    apiKeyWrapper.unmount()

    const oauthWrapper = mountModal({ ...buildProvider(), type: 'oauth', credentials: {} })
    await flushPromises()
    expect(tabKeys(oauthWrapper)).toEqual(['basic', 'models', 'scheduling', 'request'])
    oauthWrapper.unmount()
  })

  // Submission starts from the request tab while an API key is missing. Validation prevents
  // the update and reveals the basic tab containing the failing credential field.
  it('业务校验失败时切回字段所在页签', async () => {
    const provider = buildProvider()
    provider.credentials = { base_url: 'https://api.openai.com' }
    provider.credentials_status = { has_api_key: false }
    updateProviderMock.mockReset()
    const wrapper = mountModal(provider)
    await flushPromises()

    await wrapper.get('[data-settings-tab-button="request"]').trigger('click')
    expect(wrapper.get('[data-settings-tab-button="basic"]').attributes('aria-selected')).toBe(
      'false',
    )
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateProviderMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-settings-tab-button="basic"]').attributes('aria-selected')).toBe(
      'true',
    )
    expect(
      wrapper
        .get('[data-provider-field="api-key"]')
        .element.closest('[data-settings-tab]')
        ?.getAttribute('data-settings-tab'),
    ).toBe('basic')
    wrapper.unmount()
  })

  // An OpenCode Go provider stores independent route overrides for both modes.
  // Saving a cleared custom root retains its Go identity and both protocol maps.
  it('preserves OpenCode mode and protocol overrides when editing credentials', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'opencode' as const,
      credentials: {
        api_key: 'oc_sk_test',
        provider_mode: 'go',
        base_url: 'https://relay.example/inference/go',
        upstream_protocols: ['anthropic_messages', 'openai_chat_completions'],
        opencode_model_protocols: {
          payg: { 'minimax-m3': 'openai_chat_completions' },
          go: { 'minimax-m3': 'anthropic_messages' },
        },
      },
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    await wrapper.get('#edit-provider-base-url').setValue('')
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toEqual(
      expect.objectContaining({
        base_url: '',
        provider_mode: 'go',
        opencode_model_protocols: provider.credentials.opencode_model_protocols,
      }),
    )
    wrapper.unmount()
  })
  // A saved OpenCode provider deletes an override, then the shared selector reports newly
  // discovered models. Saving persists the reported catalog routes separately and leaves
  // the manual deletion intact.
  it('saves synchronized OpenCode catalog routes after deleting an override', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'opencode',
      credentials: {
        api_key: 'saved',
        provider_mode: 'go',
        base_url: 'https://saved.example',
        opencode_model_protocols: { go: { custom: 'systemone' } },
      },
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    await wrapper.get('[data-settings-tab-button="models"]').trigger('click')
    const selector = wrapper.getComponent(ModelWhitelistSelectorStub)
    expect(selector.props('providerId')).toBe(1)
    await wrapper
      .get('[data-testid="opencode-fields"] button[aria-label="common.delete"]')
      .trigger('click')
    selector.vm.$emit('synced', {
      models: ['custom', 'new'],
      model_protocols: { custom: 'anthropic_messages', new: 'openai_responses' },
    })
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toEqual(
      expect.objectContaining({
        base_url: 'https://saved.example',
        provider_mode: 'go',
        opencode_model_protocols: { payg: {}, go: {} },
        opencode_catalog_protocols: {
          payg: {},
          go: { custom: 'anthropic_messages', new: 'openai_responses' },
        },
      }),
    )
    wrapper.unmount()
  })

  // A sync response either clears the current mode's accepted suggestions with an empty
  // table or omits suggestions entirely. Saving preserves manual overrides and the other
  // mode's accepted table, and omission keeps the current mode's table as well.
  it.each([
    { result: 'empty', suggestions: {} },
    { result: 'omitted', suggestions: undefined },
  ])('saves $result OpenCode protocol suggestions with mode isolation', async ({ suggestions }) => {
    const provider = {
      ...buildProvider(),
      platform: 'opencode',
      credentials: {
        api_key: 'saved',
        provider_mode: 'go',
        opencode_model_protocols: { go: { manual: 'systemone' } },
        opencode_catalog_protocols: {
          payg: { other: 'openai_chat_completions' },
          go: { 'retired-model': 'openai_responses' },
        },
      },
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    const selector = wrapper.getComponent(ModelWhitelistSelectorStub)
    selector.vm.$emit('synced', {
      models: ['retired-model'],
      ...(suggestions === undefined ? {} : { model_protocols: suggestions }),
    })
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toEqual(
      expect.objectContaining({
        opencode_model_protocols: { payg: {}, go: { manual: 'systemone' } },
        opencode_catalog_protocols: {
          payg: { other: 'openai_chat_completions' },
          go: suggestions ?? provider.credentials.opencode_catalog_protocols.go,
        },
      }),
    )
    expect(provider.credentials.opencode_catalog_protocols.go).toEqual({
      'retired-model': 'openai_responses',
    })
    wrapper.unmount()
  })

  // A saved OpenCode provider with a blank root and one protocol override is opened. The
  // root stays blank with the Go placeholder, the override is restored into its slot, and
  // saving keeps both without filling in another platform's default address.
  it('round-trips a blank OpenCode root and its protocol address overrides', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'opencode',
      credentials: {
        api_key: 'saved',
        provider_mode: 'go',
        api_base_urls: { gemini: 'https://relay.example/google/v1beta' },
      },
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    const baseUrl = wrapper.get('#edit-provider-base-url')
    expect((baseUrl.element as HTMLInputElement).value).toBe('')
    expect(baseUrl.attributes('placeholder')).toBe('https://opencode.ai/inference/go')
    expect((wrapper.get('#edit-provider-endpoint-gemini').element as HTMLInputElement).value).toBe(
      'https://relay.example/google/v1beta',
    )
    expect(wrapper.get('#edit-provider-endpoint-responses').attributes('placeholder')).toBe(
      'https://opencode.ai/inference/go/openai/v1',
    )
    await wrapper.get('form#edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateProviderMock.mock.calls[0]?.[1]?.credentials).toEqual(
      expect.objectContaining({
        base_url: '',
        provider_mode: 'go',
        api_base_urls: { gemini: 'https://relay.example/google/v1beta' },
      }),
    )
    wrapper.unmount()
  })

  // Saved-provider sync reads stored credentials. Editing the forwarding address or
  // entering a replacement key must withhold the saved identity from the selector and
  // explain that saving comes first, for any API-key platform.
  it.each(['base-url', 'api-key'])(
    'withholds saved-provider sync after an unsaved %s change',
    async (field) => {
      const wrapper = mountModal()
      await flushPromises()
      await wrapper.get('[data-settings-tab-button="models"]').trigger('click')
      expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('providerId')).toBe(1)
      expect(wrapper.find('[data-testid="model-sync-save-first"]').exists()).toBe(false)

      await wrapper
        .get(field === 'base-url' ? '#edit-provider-base-url' : '#edit-provider-api-key')
        .setValue(field === 'base-url' ? 'https://unsaved.example' : 'sk-replacement')

      expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('providerId')).toBeUndefined()
      expect(wrapper.get('[data-testid="model-sync-save-first"]').text()).toContain(
        'admin.providers.saveBeforeSync',
      )
      wrapper.unmount()
    },
  )

  // An OpenCode provider switches service mode before saving. The mode is part of the
  // sync destination, so the saved identity is withheld until the new mode is saved.
  it('withholds saved-provider sync after an unsaved OpenCode mode change', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'opencode',
      credentials: { api_key: 'saved', provider_mode: 'payg', base_url: '' },
    }
    const wrapper = mountModal(provider)
    await flushPromises()
    wrapper.getComponent({ name: 'SettingsSegmented' }).vm.$emit('update:modelValue', 'go')
    await flushPromises()
    await wrapper.get('[data-settings-tab-button="models"]').trigger('click')
    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('providerId')).toBeUndefined()
    wrapper.unmount()
  })

  // Antigravity AI Credits overage forwarding governs outbound request behavior rather
  // than group membership or proxy routing. The toggle mounts inside the request tab
  // panel instead of the basic tab panel.
  it('places Antigravity allow-overages toggle in the request tab', async () => {
    const provider = {
      ...buildProvider(),
      platform: 'antigravity' as const,
      type: 'oauth' as const,
      credentials: { project_id: 'demo-project' },
      extra: { allow_overages: true },
    }
    const wrapper = mountModal(provider)
    await flushPromises()

    expect(
      wrapper
        .get('#edit-provider-allow-overages')
        .element.closest('[data-settings-tab]')
        ?.getAttribute('data-settings-tab'),
    ).toBe('request')
    wrapper.unmount()
  })
})

useProtocolCatalogFixture()
