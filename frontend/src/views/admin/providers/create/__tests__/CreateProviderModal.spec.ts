import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { setSwitch } from '@/__tests__/helpers/switches'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CreateProviderModal from '../CreateProviderModal.vue'

// CreateProviderModal.spec.ts exercises provider draft selection, validation,
// and creation payloads through rendered controls. Shared field stubs expose
// policy updates while API spies verify native protocols, credential ownership,
// optional metadata, and import-specific payload behavior.

// Hoisted API spies isolate transport side effects and retain each submitted
// payload for assertions; beforeEach restores defaults between independent provider scenarios.
const { createProviderMock, importCodexSessionMock, createOpenAICodexPATMock } = vi.hoisted(() => ({
  createProviderMock: vi.fn(),
  importCodexSessionMock: vi.fn(),
  createOpenAICodexPATMock: vi.fn(),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    providers: {
      create: createProviderMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      importCodexSession: importCodexSessionMock,
      createOpenAICodexPAT: createOpenAICodexPATMock,
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({}),
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([]),
    },
    tlsFingerprintRouters: {
      list: vi.fn().mockResolvedValue([]),
    },
  },
}))

vi.mock('@/api/admin/providers', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
  getUpstreamBalanceAlertStatus: vi.fn(),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

// BaseDialogStub renders conditional dialog content and footer slots so tests
// can drive the same parent visibility and completion actions.
const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    // show mirrors parent visibility so hidden dialog content does not expose
    // controls that the creation test could accidentally interact with while closed.
    show: { type: Boolean, default: false },
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

// ModelWhitelistSelectorStub emits explicit model selections from visible test
// buttons, allowing payload assertions to distinguish untouched state from
// edited or cleared policy.
const ModelWhitelistSelectorStub = defineComponent({
  name: 'ModelWhitelistSelector',
  props: {
    // modelValue receives the parent selection and defaults to an empty array; emitted
    // replacements are applied by the parent.
    modelValue: {
      type: Array,
      default: () => [],
    },
    // platform identifies the upstream passed to the selector; omission leaves an empty string
    // available to component prop assertions.
    platform: {
      type: String,
      default: '',
    },
    // syncCredentials carries the parent discovery credentials; its undefined default
    // represents the absence of credentials supplied for an upstream preview.
    syncCredentials: {
      type: Object,
      default: undefined,
    },
  },
  emits: ['update:modelValue', 'synced'],
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

// OAuthAuthorizationFlowStub exposes the authorization draft and public events
// consumed by the parent, keeping transport behavior in the production
// controller under test.
const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: {
    showManualOption: Boolean, // manual method visibility
    showCodexSessionImportOption: Boolean, // session import visibility
    showAgentIdentityOption: Boolean, // Agent Identity visibility
    showCodexPatOption: Boolean, // PAT import visibility
    initialInputMethod: String, // initial authorization method
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

// SelectStub maps native test selections to model updates while retaining
// supplied options, so tests change provider settings without browser-specific
// dropdown mechanics.
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

// mountModal creates a dialog with isolated administrator APIs and shared
// control stubs. Callers drive its rendered form and own unmount cleanup after
// inspecting payloads or pending authorization behavior.
function mountModal() {
  return mount(CreateProviderModal, {
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

// selectButtonByText clicks the matching visible button and flushes pending Vue
// updates.
async function selectButtonByText(wrapper: ReturnType<typeof mountModal>, text: string) {
  const button = wrapper.findAll('button').find((candidate) => candidate.text().includes(text))
  expect(button).toBeDefined()
  await button?.trigger('click')
}

// submitApiKeyProvider creates and submits a minimally populated API Key draft
// for the requested platform. The returned wrapper remains available for payload
// assertions and belongs to the calling test for cleanup.
async function submitApiKeyProvider(platform: 'openai' | 'anthropic') {
  const wrapper = mountModal()
  await selectButtonByText(
    wrapper,
    platform === 'openai' ? 'OpenAI' : 'admin.providers.claudeConsole',
  )
  if (platform === 'openai') {
    await selectButtonByText(wrapper, 'API Key')
  }
  await wrapper.get('form#create-provider-form input[type="text"]').setValue(`${platform} provider`)
  await wrapper.get('form#create-provider-form input[type="password"]').setValue('test-api-key')
  await wrapper.get('form#create-provider-form').trigger('submit.prevent')
  await flushPromises()
  return wrapper
}

// openCodexImportStep mounts an OpenAI draft and enters authorization with a
// name. Callers trigger the desired import event afterward and retain the
// wrapper for request assertions or cleanup.
async function openCodexImportStep() {
  const wrapper = mountModal()
  await selectButtonByText(wrapper, 'OpenAI')
  await wrapper.get('form#create-provider-form input[type="text"]').setValue('Codex import')
  await wrapper.get('form#create-provider-form').trigger('submit.prevent')
  return wrapper
}

describe('CreateProviderModal OpenAI provider options', () => {
  beforeEach(() => {
    createProviderMock.mockReset().mockResolvedValue({ id: 42, platform: 'openai', type: 'apikey' })
    importCodexSessionMock.mockReset().mockResolvedValue({
      created: 1,
      updated: 0,
      skipped: 0,
      failed: 0,
      errors: [],
      warnings: [],
    })
    createOpenAICodexPATMock.mockReset().mockResolvedValue({})
  })

  // A monthly quota is configured with a fixed date, hour, timezone, and
  // notification threshold. The creation payload must preserve all of those
  // settings together.
  it('creates a provider with a fixed monthly spending limit', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Monthly provider')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('[data-settings-tab-button="quota"]').trigger('click')
    const card = wrapper.findComponent({ name: 'QuotaLimitCard' })
    card.vm.$emit('update:monthlyLimit', 100)
    card.vm.$emit('update:monthlyResetMode', 'fixed')
    card.vm.$emit('update:monthlyResetDay', 31)
    card.vm.$emit('update:monthlyResetHour', 9)
    card.vm.$emit('update:resetTimezone', 'Asia/Shanghai')
    card.vm.$emit('update:quotaNotifyMonthlyEnabled', true)
    card.vm.$emit('update:quotaNotifyMonthlyThreshold', 10)
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock).toHaveBeenCalledWith(
      expect.objectContaining({
        extra: expect.objectContaining({
          quota_monthly_limit: 100,
          quota_monthly_reset_mode: 'fixed',
          quota_monthly_reset_day: 31,
          quota_monthly_reset_hour: 9,
          quota_reset_timezone: 'Asia/Shanghai',
          quota_notify_monthly_enabled: true,
          quota_notify_monthly_threshold: 10,
        }),
      }),
    )
    wrapper.unmount()
  })

  // The draft switches from API Key configuration to Antigravity. Credential
  // visibility must follow the new platform instead of retaining the previous
  // authentication category controls.
  it('shows only OAuth credentials for Antigravity after switching from API Key', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await selectButtonByText(wrapper, 'Antigravity')
    await flushPromises()
    expect(wrapper.find('#create-antigravity-project-id').exists()).toBe(true)
    expect(wrapper.find('#create-upstream-base-url').exists()).toBe(false)
    expect(wrapper.find('#create-upstream-api-key').exists()).toBe(false)
    expect(wrapper.findAll('button').some((button) => button.text().trim() === 'API Key')).toBe(
      false,
    )
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Antigravity OAuth')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock).not.toHaveBeenCalled()
    expect(wrapper.findComponent(OAuthAuthorizationFlowStub).exists()).toBe(true)
  })

  // An untouched OpenAI API Key draft submits the catalog defaults. The
  // payload contains native protocols and current Extra fields while obsolete
  // protocol selectors remain absent.
  it('submits the explicit OpenAI text protocol defaults with the new configuration shape', async () => {
    await submitApiKeyProvider('openai')

    expect(createProviderMock).toHaveBeenCalledTimes(1)
    const payload = createProviderMock.mock.calls[0]?.[0]
    expect(payload?.credentials?.upstream_protocols).toEqual([
      'openai_responses',
      'openai_chat_completions',
      'openai_embeddings',
      'openai_images_generations',
      'openai_images_edits',
      'openai_responses_websocket',
      'openai_responses_compact',
      'openai_alpha_search',
    ])
    expect(payload?.credentials).not.toHaveProperty('openai_capabilities')
    expect(payload?.extra?.openai_text_route_mode).toBeUndefined()
    expect(payload?.extra?.openai_responses_probe_status).toBeUndefined()
    expect(payload?.extra?.openai_responses_continuation_supported).toBe(false)
    expect(payload?.extra).not.toHaveProperty('openai_responses_mode')
    expect(payload?.extra).not.toHaveProperty('openai_responses_supported')
  })

  // The diagnostic header override remains empty during creation. Extra must
  // omit the override so backend request tracing can continue using its
  // configured default header.
  it('omits the upstream request id header from extra when left empty', async () => {
    await submitApiKeyProvider('openai')

    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(createProviderMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty(
      'upstream_request_id_header',
    )
  })

  // The administrator supplies a header name with surrounding whitespace. The
  // submitted Extra must retain the trimmed override rather than the
  // unnormalized form input.
  it('sends the trimmed upstream request id header in extra when filled', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('openai provider')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('test-api-key')
    await wrapper
      .get('[data-testid="upstream-request-id-header"]')
      .setValue('  X-Oneapi-Request-Id  ')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(createProviderMock.mock.calls[0]?.[0]?.extra?.upstream_request_id_header).toBe(
      'X-Oneapi-Request-Id',
    )
  })

  // An API-key provider created with reminders on sends the full policy in Extra,
  // with the recipient trimmed and the numeric fields kept as numbers.
  // The balance reminder editor enables a threshold for a supported provider.
  // Creation must persist that enabled policy and amount in the provider
  // metadata.
  it('saves an enabled upstream balance reminder', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Wallet provider')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('test-api-key')
    await setSwitch(wrapper.get('[data-testid="balance-alert-enabled"]'), true)
    await wrapper.get('[data-testid="balance-alert-threshold"]').setValue('25.5')
    await wrapper.get('[data-testid="balance-alert-interval"]').setValue('30')
    await wrapper.get('[data-testid="balance-alert-email"]').setValue(' ops@example.com ')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(createProviderMock.mock.calls[0]?.[0]?.extra?.upstream_balance_alert).toEqual({
      enabled: true,
      threshold: 25.5,
      interval_minutes: 30,
      recipient_email: 'ops@example.com',
    })
  })

  // Leaving reminders off keeps the key out of Extra; the backend reads a missing policy as disabled.
  // A supported provider leaves its balance reminder disabled. Creation must
  // omit the reminder object instead of materializing an unused policy from
  // the editor defaults.
  it('omits the balance reminder when it stays off', async () => {
    await submitApiKeyProvider('openai')

    expect(createProviderMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty(
      'upstream_balance_alert',
    )
  })

  // Creating an API Key provider selects derouter through the shared editor.
  // The request stores the adapter in Extra and leaves its default query root implicit.
  // The upstream usage editor selects the derouter Account Key adapter.
  // Creation must retain that adapter and its entered key in the expected
  // request fields.
  it('creates a provider with the derouter Account Key adapter', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    await wrapper.get('[data-testid="upstream-usage-adapter"]').setValue('derouter')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Derouter account')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('account-key')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(createProviderMock.mock.calls[0][0].extra.upstream_usage_query).toEqual({
      enabled: true,
      adapter: 'derouter',
    })
    wrapper.unmount()
  })

  // A New API wallet query receives optional user credentials. The access
  // token and user identity must stay inside Credentials while Extra carries
  // the usage-query configuration.
  it('stores the optional New API wallet token in credentials, never in Extra', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')

    await wrapper.get('[data-testid="upstream-usage-adapter"]').setValue('new_api')
    await wrapper
      .get('[data-testid="upstream-usage-base-url"]')
      .setValue('https://usage.example.test')
    await wrapper.get('[data-testid="upstream-usage-wallet-access-token"]').setValue('wallet-pat')
    await wrapper.get('[data-testid="upstream-usage-wallet-user-id"]').setValue('42')
    await wrapper
      .get('form#create-provider-form input[type="text"]')
      .setValue('OpenAI wallet provider')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('relay-api-key')
    // The generic selector targets the API Key input; use the wallet-specific test ID so its separate PAT draft receives the intended credential.
    await wrapper.get('[data-testid="upstream-usage-wallet-access-token"]').setValue('wallet-pat')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()

    const payload = createProviderMock.mock.calls[0]?.[0]
    expect(payload?.credentials?.new_api_user_access_token).toBe('wallet-pat')
    expect(payload?.credentials?.new_api_user_id).toBe('42')
    expect(payload?.extra?.upstream_usage_query).toEqual({
      enabled: true,
      adapter: 'new_api',
      base_url: 'https://usage.example.test',
    })
    expect(payload?.extra?.upstream_usage_query?.new_api_user_access_token).toBeUndefined()
  })

  // OpenAI creation exposes administrator-selected native protocols and
  // workload controls. The rendered controls must remain independent of the
  // removed provider capability probe state.
  it('renders workload and administrator protocols without probe state', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')

    expect(wrapper.text()).toContain('admin.protocols.nativeTitle')
    expect(wrapper.find('[data-native-protocol="anthropic_messages"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.providers.openai.responsesContinuationSupported')
    expect(wrapper.text()).not.toContain('admin.providers.openai.responsesProbeStatus')
    expect(
      wrapper.get('[data-testid="create-openai-continuation-supported"]').attributes('role'),
    ).toBe('switch')
  })

  // Explicit toggle selection persists the boolean policy in the creation request.
  // The image URL backfill toggle is explicitly enabled for an OpenAI API Key
  // draft. The creation payload must include its opt-in Extra flag.
  it('opts into image URL backfill for OpenAI API-key providers', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    const toggle = wrapper.get('[data-testid="create-openai-images-url-to-b64-json"]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Images provider')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock.mock.calls[0]?.[0]?.extra?.images_url_to_b64_json).toBe(true)
  })

  // Every native protocol is deselected before submission. The resulting
  // credentials must retain an empty array instead of restoring the platform
  // defaults at creation time.
  it('allows an explicitly empty native protocol set', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')
    for (const toggle of wrapper.findAll('[data-native-protocol]')) await setSwitch(toggle, false)
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('OpenAI provider')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('test-api-key')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials?.upstream_protocols).toEqual([])
  })

  // The OpenAI form is inspected and submitted without the removed long-
  // context option. Neither the rendered controls nor persisted Extra may
  // revive that obsolete provider setting.
  it('does not render or submit the removed provider-level long-context setting', async () => {
    const wrapper = await submitApiKeyProvider('openai')

    expect(wrapper.find('[data-testid="openai-long-context-billing-toggle"]').exists()).toBe(false)
    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(
      createProviderMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled,
    ).toBeUndefined()
  })

  // The form no longer offers an upstream billing probe setting. Submission
  // must also omit the obsolete metadata so hidden defaults cannot restore
  // removed behavior.
  it('does not render or submit the removed upstream billing probe setting', async () => {
    const wrapper = await submitApiKeyProvider('openai')

    expect(wrapper.find('[data-testid="upstream-billing-auto-probe"]').exists()).toBe(false)
    expect(createProviderMock.mock.calls[0]?.[0]?.upstream_billing_probe_enabled).toBeUndefined()
  })

  // Namespace flattening belongs to the OAuth compatibility controls and remains hidden for API Key creation.
  // OpenAI authentication switches from OAuth to API Key within one draft.
  // Namespace flattening must disappear when the selected provider category no
  // longer supports that policy.
  it('shows the Codex namespace flatten toggle only for OpenAI OAuth providers', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      true,
    )

    await selectButtonByText(wrapper, 'API Key')
    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(
      false,
    )
  })

  // Each CN platform begins with its complete adaptive endpoint family.
  // Creation must preserve every configured native endpoint while using the supported protocol set for that platform.
  it.each([
    {
      label: 'Kimi',
      platform: 'kimi',
      base: 'https://api.moonshot.cn/v1',
      anthropic: 'https://api.moonshot.cn/anthropic',
      responses: 'https://api.moonshot.cn/v1',
    },
    {
      label: 'GLM',
      platform: 'zhipu',
      base: 'https://open.bigmodel.cn/api/paas/v4',
      anthropic: 'https://open.bigmodel.cn/api/anthropic',
      responses: undefined,
    },
    {
      label: 'DeepSeek',
      platform: 'deepseek',
      base: 'https://api.deepseek.com',
      anthropic: 'https://api.deepseek.com/anthropic',
      responses: 'https://api.deepseek.com',
    },
  ])(
    '$label 默认提交完整 adaptive 端点',
    async ({ label, platform, base, anthropic, responses }) => {
      const wrapper = mountModal()
      await selectButtonByText(wrapper, label)
      await wrapper
        .get('form#create-provider-form input[type="text"]')
        .setValue(`${label} adaptive`)
      await wrapper.get('form#create-provider-form input[type="password"]').setValue('sk-cn')

      await wrapper.get('form#create-provider-form').trigger('submit.prevent')
      await flushPromises()

      expect(createProviderMock).toHaveBeenCalledTimes(1)
      expect(createProviderMock.mock.calls[0]?.[0]).toMatchObject({ platform, type: 'apikey' })
      expect(createProviderMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
        provider_mode: 'payg',
        upstream_protocols: expect.arrayContaining([
          'anthropic_messages',
          'openai_chat_completions',
        ]),
        base_url: base,
      })
      expect(createProviderMock.mock.calls[0]?.[0]?.credentials.api_base_urls).toEqual({
        chat_completions: base,
        anthropic,
        ...(responses ? { responses } : {}),
      })
    },
  )

  // Both Kimi billing modes permit native Responses selection. The submitted
  // protocol set and endpoint must follow the selected mode without restoring deselected native protocols.
  it.each(['payg', 'coding'])('Kimi %s 可选择并提交原生 Responses', async (mode) => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    if (mode === 'coding')
      await selectButtonByText(wrapper, 'admin.providers.cnProviders.providerMode.coding')
    for (const toggle of wrapper.findAll('[data-native-protocol]')) {
      if (toggle.attributes('data-native-protocol') !== 'openai_responses')
        await setSwitch(toggle, false)
    }
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Kimi Responses')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('sk-cn')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      provider_mode: mode,
      upstream_protocols: ['openai_responses'],
      base_url: mode === 'coding' ? 'https://api.kimi.com/coding/v1' : 'https://api.moonshot.cn/v1',
    })
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials).toHaveProperty('api_base_urls')
  })

  // Kimi Coding Plan is selected with adaptive endpoint configuration. The
  // submitted native Responses endpoint must use the Coding Plan URL alongside
  // the remaining protocol URLs.
  it('submits adaptive Kimi Coding Plan Responses endpoint', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await selectButtonByText(wrapper, 'admin.providers.cnProviders.providerMode.coding')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Kimi coding')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('sk-kimi-coding')

    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      provider_mode: 'coding',
      upstream_protocols: expect.arrayContaining(['anthropic_messages', 'openai_chat_completions']),
      base_url: 'https://api.kimi.com/coding/v1',
      api_base_urls: {
        chat_completions: 'https://api.kimi.com/coding/v1',
        anthropic: 'https://api.kimi.com/coding',
        responses: 'https://api.kimi.com/coding/v1',
      },
    })
  })

  // The administrator edits the adaptive Chat endpoint before model preview.
  // Preview credentials must use that edited endpoint rather than a stale
  // shared base URL.
  it('uses the edited adaptive Chat endpoint when previewing upstream models', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Kimi')
    await wrapper
      .get('[data-testid="provider-endpoint-chat_completions"]')
      .setValue('https://relay.example.com/v1')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('sk-relay')

    expect(wrapper.getComponent(ModelWhitelistSelectorStub).props('syncCredentials')).toMatchObject(
      {
        platform: 'kimi',
        type: 'apikey',
        base_url: 'https://relay.example.com/v1',
        api_key: 'sk-relay',
      },
    )
  })

  // Zhipu pay-as-you-go keys have no usage endpoint, while Coding Plan keys use a fixed
  // vendor adapter. The usage editor follows the resolved adapter: hidden for payg, and
  // shown without adapter or address inputs for coding.
  it('shows the usage editor only when the drafted CN mode has an adapter', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'Zhipu GLM')
    expect(wrapper.find('[data-testid="upstream-usage-config"]').exists()).toBe(false)
    await wrapper.get('[data-testid="provider-mode-coding"]').trigger('click')
    expect(wrapper.find('[data-testid="upstream-usage-config"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="upstream-usage-adapter"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="upstream-usage-base-url"]').exists()).toBe(false)
  })

  // An OpenAI draft enters authorization through the public form action. The
  // child flow must expose Agent Identity as an available import method for
  // that provider.
  it('exposes Agent Identity in the OpenAI authorization methods', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('OpenAI provider')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')

    const flow = wrapper.getComponent(OAuthAuthorizationFlowStub)
    expect(flow.props('showManualOption')).toBe(true)
    expect(flow.props('showCodexSessionImportOption')).toBe(true)
    expect(flow.props('showAgentIdentityOption')).toBe(true)
    expect(flow.props('showCodexPatOption')).toBe(true)
    expect(flow.props('initialInputMethod')).toBe('manual')
  })

  // Agent Identity accepts camelCase and nested identity payloads supported by
  // the backend. Each fixture must reach session import after the administrator selects that method.
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

  // An Anthropic provider is created through the shared submission path.
  // OpenAI-specific Extra settings must remain absent despite their defaults
  // existing in the common dialog.
  it('omits the OpenAI setting for non-OpenAI provider creation', async () => {
    await submitApiKeyProvider('anthropic')

    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(
      createProviderMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled,
    ).toBeUndefined()
  })

  // A Codex session import is submitted through its dedicated endpoint. The
  // request must omit the obsolete provider billing override and leave billing
  // policy to the backend.
  it('leaves Codex session import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock).toHaveBeenCalledTimes(1)
    expect(
      importCodexSessionMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled,
    ).toBeUndefined()
  })

  // Codex session creation carries the image driver selected in the shared form.
  // The import payload must preserve this override alongside credential metadata.
  it('includes the selected image Responses driver when importing a Codex session', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Image provider')
    await wrapper.get('[data-testid="create-image-responses-model-input"]').setValue('gpt-6.1-sol')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.codex_image_responses_model).toBe(
      'gpt-6.1-sol',
    )
    wrapper.unmount()
  })

  // A personal access token import uses the dedicated creation endpoint. The
  // submitted request must omit the obsolete provider billing override just
  // like session import.
  it('leaves Codex PAT import billing ownership to the backend', async () => {
    const wrapper = await openCodexImportStep()
    await wrapper.get('[data-testid="import-codex-pat"]').trigger('click')
    await flushPromises()

    expect(createOpenAICodexPATMock).toHaveBeenCalledTimes(1)
    expect(
      createOpenAICodexPATMock.mock.calls[0]?.[0]?.extra?.openai_long_context_billing_enabled,
    ).toBeUndefined()
  })

  // Session and PAT import share explicit model policy drafts. Each endpoint
  // must receive the selected final whitelist separately from optional request-side mapping entries.
  it.each([
    ['Session', 'import-codex-session', importCodexSessionMock],
    ['PAT', 'import-codex-pat', createOpenAICodexPATMock],
  ])('为 Codex %s 导入独立保存最终模型白名单', async (_name, triggerTestId, apiMock) => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="set-openai-model-whitelist"]').trigger('click')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await wrapper.get(`[data-testid="${triggerTestId}"]`).trigger('click')
    await flushPromises()

    expect(apiMock).toHaveBeenCalledTimes(1)
    expect(apiMock.mock.calls[0]?.[0]?.credential_extras).toMatchObject({
      model_whitelist: ['gpt-5.4'],
    })
    expect(apiMock.mock.calls[0]?.[0]?.credential_extras).not.toHaveProperty('model_mapping')
  })

  // The administrator explicitly clears the whitelist before session import.
  // Credential supplements must preserve an empty model_whitelist array rather
  // than omitting that configured value.
  it('为 Codex Session 导入显式保存空模型白名单', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('[data-testid="clear-openai-model-whitelist"]').trigger('click')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.credential_extras).toMatchObject({
      model_whitelist: [],
    })
    expect(importCodexSessionMock.mock.calls[0]?.[0]?.credential_extras).not.toHaveProperty(
      'model_mapping',
    )
  })

  // An OAuth import leaves fingerprint convergence at its default off state.
  // The submitted metadata must omit the optional mode instead of persisting a
  // default override.
  it('defaults Codex fingerprint convergence to off for OAuth imports', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    const modeSelect = wrapper.get<HTMLSelectElement>(
      '[data-testid="create-codex-fingerprint-mode-select"]',
    )
    expect(modeSelect.element.value).toBe('off')

    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty(
      'codex_fingerprint_mode',
    )
  })

  // The administrator selects a fingerprint convergence mode before importing
  // OAuth credentials. The import request must preserve that explicit
  // nondefault mode in its metadata.
  it('persists an explicit Codex fingerprint convergence mode for OAuth imports', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')

    await wrapper
      .get<HTMLSelectElement>('[data-testid="create-codex-fingerprint-mode-select"]')
      .setValue('session')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Codex import')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await wrapper.get('[data-testid="import-codex-session"]').trigger('click')
    await flushPromises()

    expect(importCodexSessionMock.mock.calls[0]?.[0]?.extra?.codex_fingerprint_mode).toBe('session')
  })

  // Platform and authentication selections change the available settings
  // categories. The tab list must hide categories without applicable controls
  // while keeping the basic configuration tab available.
  it('按平台与类型隐藏没有内容的页签', async () => {
    // tabKeys reads rendered tab identities after the selected platform updates.
    const tabKeys = (wrapper: ReturnType<typeof mountModal>) =>
      wrapper
        .findAll('[data-settings-tab-button]')
        .map((tab) => tab.attributes('data-settings-tab-button'))
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    expect(tabKeys(wrapper)).toEqual(['basic', 'models', 'scheduling', 'request'])
    await selectButtonByText(wrapper, 'API Key')
    expect(tabKeys(wrapper)).toEqual(['basic', 'models', 'scheduling', 'quota', 'request'])
    wrapper.unmount()
  })

  // Enabled temporary-unschedulable rules are invalid when the form attempts
  // authorization. The dialog must remain on configuration and activate the
  // tab containing the invalid editor.
  it('进入授权步骤前校验临时不可调度规则并切到所在页签', async () => {
    const wrapper = mountModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Codex')
    await wrapper.get('[data-settings-tab-button="scheduling"]').trigger('click')
    await wrapper.get('[data-testid="temp-unsched-toggle"]').trigger('click')
    await wrapper.get('[data-settings-tab-button="request"]').trigger('click')

    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(wrapper.find('form#create-provider-form').exists()).toBe(true)
    expect(wrapper.get('[data-settings-tab-button="scheduling"]').attributes('aria-selected')).toBe(
      'true',
    )
    wrapper.unmount()
  })
})

describe('CreateProviderModal Gemini API Key provider source', () => {
  beforeEach(() => {
    createProviderMock.mockReset().mockResolvedValue({ id: 43, platform: 'gemini', type: 'apikey' })
  })

  // Gemini switches to a third-party API Key source. Creation must require a
  // custom endpoint and omit official tier metadata after a valid third-party
  // URL is supplied.
  it('creates a third-party Gemini API Key without an official tier', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="create-provider-platform-gemini"]').trigger('click')
    await wrapper.get('[data-testid="create-gemini-apikey-type"]').trigger('click')

    expect(wrapper.findAll('[data-testid="create-gemini-tier"]').length).toBe(1)

    await wrapper
      .get<HTMLSelectElement>('[data-testid="create-gemini-provider-type"]')
      .setValue('third_party')
    expect(wrapper.find('[data-testid="create-gemini-tier"]').exists()).toBe(false)

    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Third-party Gemini')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('provider-key')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(createProviderMock).not.toHaveBeenCalled()

    await wrapper
      .get<HTMLInputElement>('[data-testid="create-provider-base-url"]')
      .setValue('https://provider.example.test')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(createProviderMock).toHaveBeenCalledTimes(1)
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials).toMatchObject({
      provider_type: 'third_party',
      base_url: 'https://provider.example.test',
      api_key: 'provider-key',
    })
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials).not.toHaveProperty('tier_id')
  })
})

useProtocolCatalogFixture()

describe('Jev 提供商', () => {
  beforeEach(() => {
    createProviderMock.mockReset()
    createProviderMock.mockResolvedValue({ id: 101 })
  })

  // Jev creation uses its API Key category and official endpoint. The payload
  // must select SystemOne as its native protocol and leave upstream balance
  // queries disabled.
  it('使用 API Key、SystemOne 和官方地址创建，余额查询默认关闭', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="create-provider-platform-jev"]').trigger('click')
    await flushPromises()
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('Jev test')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('test-key')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock).toHaveBeenCalledWith(
      expect.objectContaining({
        platform: 'jev',
        type: 'apikey',
        credentials: expect.objectContaining({
          api_key: 'test-key',
          base_url: 'https://api.typesafe.ai',
          upstream_protocols: ['systemone'],
        }),
        extra: expect.objectContaining({
          upstream_usage_query: expect.objectContaining({ enabled: false }),
        }),
      }),
    )
    wrapper.unmount()
  })
})

describe('OpenCode provider creation', () => {
  beforeEach(() => {
    createProviderMock.mockReset()
    createProviderMock.mockResolvedValue({ id: 102 })
  })

  // Selecting OpenCode forces API-key credentials and the payg mode card. Submission
  // stores an empty root, no address overrides, and every enabled protocol for model-aware
  // routing.
  it('creates an independent OpenCode API-key provider', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="create-provider-platform-opencode"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="provider-mode-payg"]').attributes('aria-checked')).toBe(
      'true',
    )
    expect(wrapper.get('[data-testid="create-provider-base-url"]').attributes('placeholder')).toBe(
      'https://opencode.ai/inference',
    )
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('OpenCode test')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('oc_sk_test')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock).toHaveBeenCalledWith(
      expect.objectContaining({
        platform: 'opencode',
        type: 'apikey',
        credentials: expect.objectContaining({
          api_key: 'oc_sk_test',
          base_url: '',
          provider_mode: 'payg',
          api_base_urls: {},
          opencode_model_protocols: { payg: {}, go: {} },
          upstream_protocols: [
            'anthropic_messages',
            'openai_responses',
            'openai_chat_completions',
            'gemini_generate_content',
            'systemone',
          ],
        }),
      }),
    )
    wrapper.unmount()
  })

  // The administrator picks the Go card and overrides one protocol address below the root.
  // Placeholders follow the Go root, and the payload keeps only the entered override.
  it('saves the Go mode and a single protocol address override', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="create-provider-platform-opencode"]').trigger('click')
    await wrapper.get('[data-testid="provider-mode-go"]').trigger('click')
    expect(wrapper.get('[data-testid="provider-endpoint-gemini"]').attributes('placeholder')).toBe(
      'https://opencode.ai/inference/go/google/v1beta',
    )
    await wrapper
      .get('[data-testid="provider-endpoint-responses"]')
      .setValue(' https://relay.example/v1 ')
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('OpenCode Go')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('oc_sk_go')
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials).toEqual(
      expect.objectContaining({
        provider_mode: 'go',
        api_base_urls: { responses: 'https://relay.example/v1' },
      }),
    )
    wrapper.unmount()
  })

  // The shared model selector previews a model absent from bundled defaults with the
  // entered key and mode. The suggestions it reports are saved as the accepted catalog
  // table, separately from manual overrides.
  it('persists synchronized catalog suggestions for a newly discovered model', async () => {
    const wrapper = mountModal()
    await wrapper.get('[data-testid="create-provider-platform-opencode"]').trigger('click')
    await flushPromises()
    await wrapper.get('form#create-provider-form input[type="text"]').setValue('OpenCode preview')
    await wrapper.get('form#create-provider-form input[type="password"]').setValue('preview-key')
    const selector = wrapper.getComponent(ModelWhitelistSelectorStub)
    expect(selector.props('syncCredentials')).toMatchObject({
      platform: 'opencode',
      api_key: 'preview-key',
      provider_mode: 'payg',
    })
    selector.vm.$emit('synced', {
      models: ['new-model'],
      model_protocols: { 'new-model': 'anthropic_messages' },
    })
    await wrapper.get('form#create-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(createProviderMock.mock.calls[0]?.[0]?.credentials).toEqual(
      expect.objectContaining({
        opencode_model_protocols: { payg: {}, go: {} },
        opencode_catalog_protocols: { payg: { 'new-model': 'anthropic_messages' }, go: {} },
      }),
    )
    wrapper.unmount()
  })

  // Mounting the create dialog in its closed state on the provider list page must
  // defer global settings lookups until the administrator actually opens the modal.
  it('defers global feature settings reads while the modal stays closed', async () => {
    const { adminAPI } = await import('@/api/admin')
    vi.mocked(adminAPI.settings.getWebSearchEmulationConfig).mockClear()
    vi.mocked(adminAPI.settings.getSettings).mockClear()

    const wrapper = mount(CreateProviderModal, {
      props: { show: false, proxies: [], groups: [] },
      global: {
        stubs: {
          BaseDialog: BaseDialogStub,
          Select: SelectStub,
          ModelWhitelistSelector: ModelWhitelistSelectorStub,
          OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
          ProxySelector: true,
          GroupSelector: true,
          Icon: true,
          PlatformIcon: true,
        },
      },
    })
    await flushPromises()
    expect(adminAPI.settings.getWebSearchEmulationConfig).not.toHaveBeenCalled()
    expect(adminAPI.settings.getSettings).not.toHaveBeenCalled()

    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(adminAPI.settings.getWebSearchEmulationConfig).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
