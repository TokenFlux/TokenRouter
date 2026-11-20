import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { setSwitch } from '@/__tests__/helpers/switches'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BulkEditProviderModal from '../BulkEditProviderModal.vue'
import ModelWhitelistSelector from '../../../../../components/provider/ModelWhitelistSelector.vue'
import { adminAPI } from '@/api/admin'
import { modelAttributesAPI } from '@/api/admin/modelAttributes'

// BulkEditProviderModal.spec.ts checks bulk provider form hydration, field
// applicability, and update payloads. Selection metadata determines which
// controls are offered, and apply checkboxes distinguish omitted fields from
// explicit disabling, empty model rules, and filtered-target updates. Mock API
// responses supply provider defaults and capture submitted changes.
//
// Native select events contain strings, while these payloads distinguish
// numeric profile identifiers, boolean flags, and null choices. The select
// helper restores the first option whose string representation matches the
// event value. Null and empty-string options share an empty representation,
// so their order determines which identity survives that collision. Unmatched
// values remain strings for the component to consume.

vi.mock('@/api/admin/modelAttributes', () => ({ modelAttributesAPI: { defaults: vi.fn() } }))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    providers: {
      getById: vi.fn(),
      bulkUpdate: vi.fn(),
      checkMixedChannelRisk: vi.fn(),
    },
    tlsFingerprintProfiles: {
      list: vi.fn(),
    },
    tlsFingerprintRouters: {
      list: vi.fn(),
    },
  },
}))

vi.mock('@/api/admin/providers', () => ({
  getAntigravityDefaultModelMapping: vi.fn(),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

// coerceSelectStubValue restores an option value from the native select
// string so payload assertions receive the value type supplied by the form.
// It selects the first option whose string representation matches, recovering
// numeric, boolean, or null values when that option carries them.
//
// Null and empty-string options both match the native empty string, so option
// order resolves that collision. Other values with identical string forms
// likewise select the first match. Unmatched text returns unchanged for the
// stub caller to emit.
function coerceSelectStubValue(
  value: string,
  options: unknown[],
): string | number | boolean | null {
  // Null and empty-string options share the native empty value, while numbers
  // and booleans need their source types restored for payload assertions.
  const option = (options as Array<Record<string, unknown>>).find(
    (item) => String(item.value ?? '') === value,
  )
  return option ? (option.value as string | number | boolean | null) : value
}

// mountModal opens bulk editing for two Antigravity API Key providers by
// default. Extra props override those defaults, including initial visibility,
// and the select stub preserves typed option values during changes.
function mountModal(extraProps: Record<string, unknown> = {}) {
  return mount(BulkEditProviderModal, {
    props: {
      show: true,
      providerIds: [1, 2],
      selectedPlatforms: ['antigravity'],
      selectedTypes: ['apikey'],
      proxies: [],
      groups: [],
      ...extraProps,
    } as any,
    global: {
      stubs: {
        BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
        ConfirmDialog: true,
        Select: {
          props: [
            'modelValue', // current controlled input value
            'options', // available typed selection options
          ],
          emits: [
            'update:modelValue', // replacement controlled input value
            'change', // notify parent of input change
          ],
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
        },
        ProxySelector: true,
        GroupSelector: true,
        Icon: true,
      },
    },
  })
}

// createProvider builds an active Anthropic API Key record for hydration
// tests. Overrides are applied last and replace whole fields, so callers
// provide complete nested credential objects when testing shared model rules.
function createProvider(overrides: Record<string, unknown> = {}) {
  return {
    id: 1,
    name: 'test-provider',
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
    ...overrides,
  } as any
}

describe('BulkEditProviderModal', () => {
  beforeEach(() => {
    vi.mocked(adminAPI.providers.getById).mockReset()
    vi.mocked(adminAPI.providers.bulkUpdate).mockReset()
    vi.mocked(adminAPI.providers.checkMixedChannelRisk).mockReset()
    vi.mocked(adminAPI.tlsFingerprintProfiles.list).mockReset()
    vi.mocked(adminAPI.tlsFingerprintRouters.list).mockReset()

    vi.mocked(adminAPI.providers.getById).mockImplementation(async (id: number) =>
      createProvider({ id }),
    )
    vi.mocked(adminAPI.providers.bulkUpdate).mockResolvedValue({
      success: 2,
      failed: 0,
      results: [],
    } as any)
    vi.mocked(adminAPI.providers.checkMixedChannelRisk).mockResolvedValue({
      has_risk: false,
    } as any)
    vi.mocked(adminAPI.tlsFingerprintProfiles.list).mockResolvedValue([
      { id: 7, name: 'Profile 7' },
    ] as any)
    vi.mocked(adminAPI.tlsFingerprintRouters.list).mockResolvedValue([])
  })

  // Two providers store the same whitelist in different orders. Opening the
  // modal fills the sorted shared whitelist while leaving its apply checkbox
  // unchecked.
  it('批量编辑打开时，相同模型白名单会回填到选择器', async () => {
    vi.mocked(adminAPI.providers.getById)
      .mockResolvedValueOnce(
        createProvider({
          id: 1,
          credentials: {
            model_whitelist: ['claude-sonnet-4-5', 'claude-opus-4-1'],
          },
        }),
      )
      .mockResolvedValueOnce(
        createProvider({
          id: 2,
          credentials: {
            model_whitelist: ['claude-opus-4-1', 'claude-sonnet-4-5'],
          },
        }),
      )

    const wrapper = mountModal({
      show: false,
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey'],
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    const selector = wrapper.findComponent(ModelWhitelistSelector)
    expect(selector.exists()).toBe(true)
    expect(selector.props('modelValue')).toEqual(['claude-opus-4-1', 'claude-sonnet-4-5'])
    expect(
      (wrapper.get('#bulk-edit-model-restriction-enabled').element as HTMLInputElement).checked,
    ).toBe(false)
  })

  // The selected providers store different model whitelists. Opening the modal
  // leaves the selector empty so one provider's restriction is not presented
  // as a shared value.
  it('批量编辑打开时，不同模型限制配置不会误回填', async () => {
    vi.mocked(adminAPI.providers.getById)
      .mockResolvedValueOnce(
        createProvider({
          id: 1,
          credentials: {
            model_whitelist: ['claude-sonnet-4-5'],
          },
        }),
      )
      .mockResolvedValueOnce(
        createProvider({
          id: 2,
          credentials: {
            model_whitelist: ['claude-opus-4-1'],
          },
        }),
      )

    const wrapper = mountModal({
      show: false,
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey'],
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    const selector = wrapper.findComponent(ModelWhitelistSelector)
    expect(selector.exists()).toBe(true)
    expect(selector.props('modelValue')).toEqual([])
  })

  // Opening the whitelist selector loads models from the shared catalog after
  // its debounce. Catalog options become visible without automatically
  // selecting either model in the form.
  it('白名单从统一目录加载型号，选择不会自动填充表单', async () => {
    // Control the catalog debounce so opening the whitelist selector reaches
    // its delayed load before assertions inspect available models and the
    // untouched selection.
    vi.useFakeTimers()
    vi.mocked(modelAttributesAPI.defaults).mockResolvedValue({
      items: [{ model: 'gemini-3.1-flash-image' }, { model: 'custom-model' }],
      total: 2,
    } as any)
    const wrapper = mountModal()
    try {
      const selector = wrapper.findComponent(ModelWhitelistSelector)
      await selector.find('div.cursor-pointer').trigger('click')
      // Advance past the selector debounce before reading catalog options,
      // allowing the queued model request and its reactive display update to
      // finish.
      await vi.advanceTimersByTimeAsync(300)
      expect(wrapper.text()).toContain('gemini-3.1-flash-image')
      expect(wrapper.text()).toContain('custom-model')
      expect(selector.props('modelValue')).toEqual([])
    } finally {
      // Release the mounted selector before restoring real timers so the
      // catalog case does not leave its pending component work on the fake
      // clock.
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  // The administrator opens model mapping and adds a mapping row. The form
  // gains two inputs for the source and destination rather than populating
  // mappings from catalog choices.
  it('模型映射通过手动添加编辑', async () => {
    const wrapper = mountModal()
    const mappingTab = wrapper
      .findAll('button')
      .find((btn) => btn.text().includes('admin.providers.modelMapping'))
    await mappingTab!.trigger('click')
    const count = wrapper.findAll('input').length
    await wrapper
      .findAll('button')
      .find((btn) => btn.text().includes('admin.providers.addMapping'))!
      .trigger('click')
    expect(wrapper.findAll('input')).toHaveLength(count + 2)
    wrapper.unmount()
  })

  // All selected accounts use the same Chinese vendor and API Key
  // authentication. The header override apply control is visible for each
  // supported vendor in the parameter table.
  it.each(['kimi', 'zhipu', 'deepseek'])('全部目标为 %s API Key 时展示请求头覆写', (platform) => {
    const wrapper = mountModal({
      selectedPlatforms: [platform],
      selectedTypes: ['apikey'],
    })

    expect(wrapper.find('#bulk-edit-header-override-enabled').exists()).toBe(true)
  })

  // All selected accounts use a Chinese vendor with OAuth authentication. The
  // header override apply control is absent for each vendor despite matching
  // its supported platform name.
  it.each(['kimi', 'zhipu', 'deepseek'])('目标为 %s OAuth 时不展示请求头覆写', (platform) => {
    const wrapper = mountModal({
      selectedPlatforms: [platform],
      selectedTypes: ['oauth'],
    })

    expect(wrapper.find('#bulk-edit-header-override-enabled').exists()).toBe(false)
  })

  // Model restriction is selected for editing with an empty whitelist.
  // Submission includes both an empty mapping and an empty whitelist,
  // expressing removal of configured model restrictions.
  it('仅勾选模型限制且白名单留空时，应清空 model_mapping 和 model_whitelist 以支持所有模型', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey'],
    })

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {},
        effort_model_mapping: {},
        effort_model_mapping_strip_effort: false,
        model_whitelist: [],
      },
    })
  })

  // An Antigravity selection applies one whitelist entry through the shared
  // selector. Submission stores that independent whitelist alongside an empty
  // mapping instead of deriving identity mappings.
  it('Antigravity 批量修改保存独立白名单', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['antigravity'],
      selectedTypes: ['apikey'],
    })

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    const selector = wrapper.findComponent(ModelWhitelistSelector)
    await selector.vm.$emit('update:modelValue', ['claude-sonnet-4-5'])
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {},
        effort_model_mapping: {},
        effort_model_mapping_strip_effort: false,
        model_whitelist: ['claude-sonnet-4-5'],
      },
    })
  })

  // Both Qoder COSY accounts contain an identity mapping for ultimate. Opening
  // and submitting model restrictions preserves that explicit mapping and
  // sends an empty independent whitelist.
  it('Qoder 批量编辑应把旧 self mapping 保留为显式映射', async () => {
    vi.mocked(adminAPI.providers.getById)
      .mockResolvedValueOnce(
        createProvider({
          id: 1,
          platform: 'qoder',
          type: 'cosy',
          credentials: {
            model_mapping: {
              ultimate: 'ultimate',
            },
          },
        }),
      )
      .mockResolvedValueOnce(
        createProvider({
          id: 2,
          platform: 'qoder',
          type: 'cosy',
          credentials: {
            model_mapping: {
              ultimate: 'ultimate',
            },
          },
        }),
      )

    const wrapper = mountModal({
      show: false,
      selectedPlatforms: ['qoder'],
      selectedTypes: ['cosy'],
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {
          ultimate: 'ultimate',
        },
        effort_model_mapping: {},
        effort_model_mapping_strip_effort: false,
        model_whitelist: [],
      },
    })
  })

  // A mixed Antigravity and OpenAI selection applies empty model restrictions.
  // The bulk payload uses the shared mapping and whitelist fields for both
  // platforms without vendor-specific divergence.
  it('包含 Antigravity 的跨平台批量修改共用模型规则', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['antigravity', 'openai'],
      selectedTypes: ['apikey'],
    })

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {},
        effort_model_mapping: {},
        effort_model_mapping_strip_effort: false,
        model_whitelist: [],
      },
    })
  })

  // Both selected Anthropic API-key providers share identical effort_model_mapping
  // rules and wire-effort stripping settings. Bulk editing must hydrate those rules
  // and submit both effort_model_mapping and effort_model_mapping_strip_effort intact.
  it('hydrates and submits shared effort_model_mapping rules in bulk edit', async () => {
    const sharedCredentials = {
      model_whitelist: ['claude-opus-5-5-high'],
      effort_model_mapping: {
        'claude-opus-5-5': {
          high: 'claude-opus-5-5-high',
        },
      },
      effort_model_mapping_strip_effort: true,
    }
    vi.mocked(adminAPI.providers.getById)
      .mockResolvedValueOnce(
        createProvider({
          id: 1,
          platform: 'anthropic',
          type: 'apikey',
          credentials: sharedCredentials,
        }),
      )
      .mockResolvedValueOnce(
        createProvider({
          id: 2,
          platform: 'anthropic',
          type: 'apikey',
          credentials: sharedCredentials,
        }),
      )

    const wrapper = mountModal({
      show: false,
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey'],
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {},
        effort_model_mapping: {
          'claude-opus-5-5': {
            high: 'claude-opus-5-5-high',
          },
        },
        effort_model_mapping_strip_effort: true,
        model_whitelist: ['claude-opus-5-5-high'],
      },
    })
  })

  // All selected Grok accounts use OAuth and receive an official API URL. The
  // selected base URL is submitted as a credential update after the
  // administrator enables that field.
  it('全部目标为 Grok OAuth 时，官方主机 base_url 作为手动端点切换正常提交', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-base-url-enabled').setValue(true)
    await wrapper.get('#bulk-edit-base-url').setValue('https://api.x.ai/v1')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://api.x.ai/v1',
      },
    })
  })

  // A Grok-only selection exposes five endpoint presets. Choosing the regional
  // preset both enables base URL editing and submits that preset's endpoint
  // without another apply action.
  it('所选全为 grok 时展示快捷端点，点击后填入并自动勾选 base_url', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['oauth'],
    })

    const presets = wrapper.findAll('[data-testid="grok-base-url-preset"]')
    expect(presets.length).toBe(5)

    // The regional preset differs from the default host, so its submitted
    // value also proves that the preset click updated the field.
    await presets[2].trigger('click')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://us-east-1.api.x.ai/v1',
      },
    })
  })

  // Selected providers include Grok and Anthropic API Key accounts. Grok
  // endpoint presets remain absent because a vendor-specific shortcut would
  // not describe the whole target set.
  it('所选含非 grok 平台时不展示快捷端点', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok', 'anthropic'],
      selectedTypes: ['apikey'],
    })

    expect(wrapper.findAll('[data-testid="grok-base-url-preset"]').length).toBe(0)
  })

  // A Grok OAuth selection applies a third-party relay URL. Submission retains
  // the entered endpoint in credentials after the base URL apply checkbox is
  // enabled.
  it('全部目标为 Grok OAuth 时，第三方 base_url 正常提交', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-base-url-enabled').setValue(true)
    await wrapper.get('#bulk-edit-base-url').setValue('https://relay.example.com/v1')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://relay.example.com/v1',
      },
    })
  })

  // A Grok selection mixes API Key and OAuth account types. The official API
  // endpoint remains a valid base URL update for the mixed target set.
  it('混合类型选择（含 apikey）时官方主机 base_url 不拦截', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['grok'],
      selectedTypes: ['apikey', 'oauth'],
    })

    await wrapper.get('#bulk-edit-base-url-enabled').setValue(true)
    await wrapper.get('#bulk-edit-base-url').setValue('https://api.x.ai/v1')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        base_url: 'https://api.x.ai/v1',
      },
    })
  })

  // OpenAI OAuth providers enable automatic passthrough through its apply
  // control and switch. Submission writes the enabled value into extra without
  // requiring model-rule edits.
  it('OpenAI 提供商批量编辑可开启自动透传', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-passthrough-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-passthrough-toggle').trigger('click')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_passthrough: true,
      },
    })
  })

  // OpenAI OAuth providers enable namespace flattening through its apply
  // control and switch. Submission writes the compatibility flag into extra
  // for the selected provider IDs.
  it('OpenAI OAuth 批量编辑可开启 namespace 摊平兼容开关', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-flatten-namespaces-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-flatten-namespaces-toggle').trigger('click')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: { openai_responses_flatten_namespaces: true },
    })
  })

  // The OpenAI target set mixes OAuth and API Key accounts. Namespace
  // flattening controls are absent because this compatibility setting applies
  // to an OAuth-only selection.
  it('namespace 摊平开关不对非 OAuth 选择展示', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth', 'apikey'],
    })

    expect(wrapper.find('#bulk-edit-openai-flatten-namespaces-enabled').exists()).toBe(false)
  })

  // Native protocols are enabled for editing and every protocol except
  // embeddings is disabled. Submission sends exactly the remaining embeddings
  // protocol in the credentials array.
  it('批量编辑保存统一原生协议集合', async () => {
    const wrapper = mountModal({ selectedPlatforms: ['openai'], selectedTypes: ['apikey'] })
    await wrapper.get('#bulk-native-protocols-enabled').setValue(true)
    await flushPromises()
    for (const toggle of wrapper.findAll('[data-native-protocol]')) {
      if (toggle.attributes('data-native-protocol') !== 'openai_embeddings')
        await setSwitch(toggle, false)
    }
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: { upstream_protocols: ['openai_embeddings'] },
    })
  })

  // OpenAI API Key providers opt into HTTP continuation through separate apply
  // and value controls. Submission includes the enabled flag, and the value
  // control retains its switch role.
  it('OpenAI API Key 批量编辑可显式开启 HTTP continuation', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey'],
    })

    const applyContinuationCheckbox = wrapper.get<HTMLInputElement>(
      '#bulk-edit-openai-continuation-supported-enabled',
    )
    expect(applyContinuationCheckbox.element.tagName).toBe('INPUT')
    expect(applyContinuationCheckbox.attributes('type')).toBe('checkbox')
    expect(wrapper.get('#bulk-edit-openai-continuation-supported-body').text()).toBe('')
    expect(
      wrapper.get('[data-testid="bulk-edit-openai-continuation-supported"]').attributes('role'),
    ).toBe('switch')
    await applyContinuationCheckbox.setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-continuation-supported"]').trigger('click')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: { openai_responses_continuation_supported: true },
    })
  })

  // The administrator applies native protocols and turns every protocol off.
  // Submission carries an explicit empty array, preserving the distinction
  // between disabling all protocols and omitting the field.
  it('原生协议全部关闭后提交显式空数组', async () => {
    const wrapper = mountModal({ selectedPlatforms: ['openai'], selectedTypes: ['apikey'] })
    await wrapper.get('#bulk-native-protocols-enabled').setValue(true)
    await flushPromises()
    for (const toggle of wrapper.findAll('[data-native-protocol]')) await setSwitch(toggle, false)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: { upstream_protocols: [] },
    })
  })

  // The target platform changes after protocol editing has been enabled.
  // Submitting the form excludes the stale protocol selection and contains
  // only the applicable status update.
  it('切换批量目标后不提交旧协议配置', async () => {
    const wrapper = mountModal({ selectedPlatforms: ['openai'], selectedTypes: ['apikey'] })
    await wrapper.get('#bulk-native-protocols-enabled').setValue(true)
    await wrapper.setProps({ selectedPlatforms: ['anthropic'], selectedTypes: ['apikey'] })
    await wrapper.get('#bulk-edit-status-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], { status: 'active' })
  })

  // An OpenAI OAuth selection applies each Codex image policy in turn.
  // Payloads preserve null inheritance and explicit enable, disable, or strip
  // values with legacy fields cleared.
  it.each([
    [
      'inherit',
      {
        codex_image_generation_bridge: null,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: null,
      },
    ],
    [
      'enabled',
      {
        codex_image_generation_bridge: true,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: null,
      },
    ],
    [
      'disabled',
      {
        codex_image_generation_bridge: false,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: null,
      },
    ],
    [
      'block',
      {
        codex_image_generation_bridge: null,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: 'strip',
      },
    ],
  ])('OpenAI OAuth 批量编辑提交 Codex 图片工具 %s 策略', async (mode, expectedExtra) => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    const applyCheckbox = wrapper.get<HTMLInputElement>('#bulk-edit-codex-image-tool-enabled')
    expect(applyCheckbox.element.checked).toBe(false)
    await applyCheckbox.setValue(true)
    await wrapper.get('[data-testid="bulk-edit-codex-image-tool-select"]').setValue(mode)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: expectedExtra,
    })
  })

  // OpenAI API Key targets expose the Codex image policy control. Enabling the
  // bridge submits its true value and nulls the legacy bridge and
  // explicit-tool fields.
  it('OpenAI API Key 批量编辑显示并提交 Codex 图片工具策略', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey'],
    })

    expect(wrapper.find('#bulk-edit-codex-image-tool-enabled').exists()).toBe(true)
    await wrapper.get('#bulk-edit-codex-image-tool-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-codex-image-tool-select"]').setValue('enabled')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_image_generation_bridge: true,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: null,
      },
    })
  })

  // A filtered target mixes OpenAI OAuth and API Key accounts. Applying the
  // block image policy sends filters with the strip policy and clears bridge
  // overrides.
  it('筛选 OpenAI OAuth/API Key 提供商批量编辑提交 Codex 图片工具策略', async () => {
    const wrapper = mountModal({
      providerIds: [],
      selectedPlatforms: [],
      selectedTypes: [],
      target: {
        mode: 'filtered',
        filters: { platform: 'openai', status: 'active' },
        previewCount: 12,
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth', 'apikey'],
      },
    })

    await wrapper.get('#bulk-edit-codex-image-tool-enabled').setValue(true)
    await wrapper.get('[data-testid=\"bulk-edit-codex-image-tool-select\"]').setValue('block')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith({
      filters: { platform: 'openai', status: 'active' },
      extra: {
        codex_image_generation_bridge: null,
        codex_image_generation_bridge_enabled: null,
        codex_image_generation_explicit_tool_policy: 'strip',
      },
    })
  })

  // Anthropic OAuth targets do not support the OpenAI Codex image policy. The
  // corresponding bulk apply control is absent from the mounted form for that
  // selection.
  it('非 OpenAI 目标不显示 Codex 图片工具批量字段', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['oauth'],
    })

    expect(wrapper.find('#bulk-edit-codex-image-tool-enabled').exists()).toBe(false)
  })

  // OpenAI OAuth targets select a per-session WebSocket connection mode.
  // Submission writes the unified responses connection field with the selected
  // value in the extra patch.
  it('OpenAI OAuth 批量编辑提交统一连接字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-ws-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-ws-mode-select"]').setValue('per_session')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        responses_ws_connection_mode: 'per_session',
      },
    })
  })

  // OpenAI API Key targets mount the bulk form. The OAuth WebSocket mode apply
  // control is absent, preventing the OAuth-specific selector from appearing
  // for key credentials.
  it('OpenAI API Key 批量编辑不显示 WS mode 入口', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey'],
    })

    expect(wrapper.find('#bulk-edit-openai-ws-mode-enabled').exists()).toBe(false)
  })

  // OpenAI OAuth targets apply the Codex-only client policy. Submission
  // includes both the policy name and its legacy boolean representation for
  // the selected provider IDs.
  it('OpenAI OAuth 批量编辑应提交客户端访问策略字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-codex-cli-only-enabled').setValue(true)
    await wrapper
      .get('[data-testid="bulk-edit-openai-client-policy-select"]')
      .setValue('codex_only')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_oauth_client_policy: 'codex_only',
        codex_cli_only: true,
      },
    })
  })

  // OpenAI OAuth targets opt into session fingerprint mode. Submission
  // explicitly includes the selected mode so the bulk patch replaces an
  // existing fingerprint setting.
  it('OpenAI OAuth 批量编辑应显式覆盖 Codex 指纹收敛模式', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-codex-fingerprint-mode-select"]').setValue('session')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'session',
      },
    })
  })

  // Fingerprint editing is enabled while its default choice remains off.
  // Submission contains an explicit off value instead of omitting the selected
  // field from the patch.
  it('OpenAI OAuth 批量编辑可显式关闭已有 Codex 指纹收敛模式', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    expect(
      (
        wrapper.get('[data-testid="bulk-codex-fingerprint-mode-select"]')
          .element as HTMLSelectElement
      ).value,
    ).toBe('off')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'off',
      },
    })
  })

  // OpenAI OAuth targets enable TLS fingerprinting and choose the random
  // profile sentinel. Submission enables fingerprinting, carries that profile
  // choice, and clears the router binding.
  it('OpenAI OAuth 批量编辑可启用 TLS 指纹伪装', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })
    await flushPromises()

    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-toggle').trigger('click')
    await wrapper.get('[data-testid="bulk-edit-tls-fingerprint-profile"]').setValue('-1')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: true,
        tls_fingerprint_profile_id: -1,
        tls_fingerprint_router_id: 0,
      },
    })
  })

  // OpenAI and Anthropic token accounts share a TLS fingerprint update.
  // Submission includes the enabled profile configuration without writing an
  // OpenAI-only router field across the mixed selection.
  it('OpenAI OAuth 和 Anthropic OAuth 混选时可批量启用 TLS 指纹伪装', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai', 'anthropic'],
      selectedTypes: ['oauth', 'setup-token'],
    })
    await flushPromises()

    expect(wrapper.find('#bulk-edit-tls-fingerprint-enabled').exists()).toBe(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-toggle').trigger('click')
    await wrapper.get('[data-testid="bulk-edit-tls-fingerprint-profile"]').setValue('-1')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: true,
        tls_fingerprint_profile_id: -1,
      },
    })
  })

  // Qoder COSY targets enable TLS fingerprinting with the random profile
  // sentinel. Submission carries the shared profile fields and leaves the
  // OpenAI router field absent.
  it('Qoder COSY 批量编辑可启用 TLS 指纹伪装且不写入 OpenAI TLS 路由器', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['qoder'],
      selectedTypes: ['cosy'],
    })
    await flushPromises()

    expect(wrapper.find('#bulk-edit-tls-fingerprint-enabled').exists()).toBe(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-tls-fingerprint-toggle').trigger('click')
    await wrapper.get('[data-testid="bulk-edit-tls-fingerprint-profile"]').setValue('-1')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: true,
        tls_fingerprint_profile_id: -1,
      },
    })
  })

  // OpenAI OAuth targets enable the Claude Code client exception. Submission
  // stores the allowed client list in extra after both its apply checkbox and
  // value switch are selected.
  it('OpenAI OAuth 批量编辑应提交 codex_cli_only_allowed_clients 字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-codex-allow-claude-code-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-codex-allow-claude-code-toggle').trigger('click')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_cli_only_allowed_clients: ['claude_code'],
      },
    })
  })

  // OpenAI OAuth targets apply pause thresholds and independent disabled
  // flags. Submission converts the entered percentages to fractional
  // thresholds and preserves explicit true and false values.
  it('OpenAI OAuth 批量编辑应提交 5h/7d 自动暂停字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-auto-pause-5h-threshold-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-auto-pause-5h-threshold').setValue('95')
    await wrapper.get('#bulk-edit-openai-auto-pause-7d-threshold-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-auto-pause-7d-threshold').setValue('90')
    await wrapper.get('#bulk-edit-openai-auto-pause-5h-disabled-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-auto-pause-5h-disabled-toggle').trigger('click')
    await wrapper.get('#bulk-edit-openai-auto-pause-7d-disabled-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        auto_pause_5h_threshold: 0.95,
        auto_pause_7d_threshold: 0.9,
        auto_pause_5h_disabled: true,
        auto_pause_7d_disabled: false,
      },
    })
  })

  // TLS fingerprint editing is applied with the value switch left off.
  // Submission disables fingerprinting and clears both the profile and router
  // selections for OpenAI OAuth targets.
  it('OpenAI OAuth 批量编辑可关闭 TLS 指纹伪装', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-tls-fingerprint-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        enable_tls_fingerprint: false,
        tls_fingerprint_profile_id: 0,
        tls_fingerprint_router_id: 0,
      },
    })
  })

  // OpenAI API Key targets select pooled WebSocket connections through their
  // dedicated control. Submission writes the same unified connection-mode
  // field used by OAuth updates.
  it('OpenAI API Key 批量编辑提交统一连接字段', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey'],
    })

    await wrapper.get('#bulk-edit-openai-apikey-ws-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-apikey-ws-mode-select"]').setValue('pooled')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        responses_ws_connection_mode: 'pooled',
      },
    })
  })

  // OpenAI API Key targets mount the bulk settings form. Neither the upstream
  // billing auto-probe apply checkbox nor its selector is exposed to the
  // administrator.
  it('OpenAI API Key 批量编辑不再显示上游倍率自动探测设置', () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey'],
    })

    expect(wrapper.find('#bulk-edit-upstream-billing-auto-probe-enabled').exists()).toBe(false)
    expect(
      wrapper.find('[data-testid="bulk-edit-upstream-billing-auto-probe-select"]').exists(),
    ).toBe(false)
  })

  // Filtered OpenAI targets enable both compaction modes and add a compact
  // model mapping. Submission carries filters, force-on modes, and the
  // dedicated source-to-target mapping together.
  it('筛选 OpenAI 提供商批量编辑应提交原生 V2、旧版 Compact 模式和专属模型映射', async () => {
    const wrapper = mountModal({
      providerIds: [],
      selectedPlatforms: [],
      selectedTypes: [],
      target: {
        mode: 'filtered',
        filters: { platform: 'openai' },
        previewCount: 12,
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth', 'apikey'],
      },
    })

    await wrapper.get('#bulk-edit-openai-native-compaction-v2-mode-enabled').setValue(true)
    await setSwitch(
      wrapper.get('[data-testid="bulk-edit-openai-native-compaction-v2-mode-select"]'),
      true,
    )
    await wrapper.get('#bulk-edit-openai-compact-mode-enabled').setValue(true)
    await setSwitch(wrapper.get('[data-testid="bulk-edit-openai-compact-mode-select"]'), true)
    await wrapper.get('#bulk-edit-openai-compact-model-mapping-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-compact-model-mapping-add"]').trigger('click')
    await wrapper
      .get('[data-testid="bulk-edit-openai-compact-model-mapping-source-0"]')
      .setValue('gpt-5.4')
    await wrapper
      .get('[data-testid="bulk-edit-openai-compact-model-mapping-target-0"]')
      .setValue('gpt-5.4-openai-compact')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith({
      filters: { platform: 'openai' },
      extra: {
        openai_native_compaction_v2_mode: 'force_on',
        openai_compact_mode: 'force_on',
      },
      credentials: {
        compact_model_mapping: {
          'gpt-5.4': 'gpt-5.4-openai-compact',
        },
      },
    })
  })

  // OpenAI API Key targets apply passthrough with its value switch off.
  // Submission explicitly disables both the current passthrough field and the
  // OAuth legacy field.
  it('OpenAI 提供商批量编辑可关闭自动透传', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['apikey'],
    })

    await wrapper.get('#bulk-edit-openai-passthrough-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        openai_passthrough: false,
        openai_oauth_passthrough: false,
      },
    })
  })

  // OpenAI OAuth targets enable passthrough and apply empty model restrictions
  // together. Submission retains both model-rule fields, and the form
  // continues to expose restriction controls.
  it('OpenAI 自动透传仍保存模型限制', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-passthrough-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-passthrough-toggle').trigger('click')
    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {},
        effort_model_mapping: {},
        effort_model_mapping_strip_effort: false,
        model_whitelist: [],
      },
      extra: {
        openai_passthrough: true,
      },
    })
    expect(wrapper.text()).toContain('admin.providers.modelRestriction')
  })

  // Bulk editing targets a filtered provider set instead of explicit IDs.
  // Submission forwards the complete filter criteria with the applied status
  // value to the bulk endpoint.
  it('filtered-results 模式下应提交 filters 而不是 provider_ids', async () => {
    const wrapper = mountModal({
      providerIds: [],
      target: {
        mode: 'filtered',
        filters: {
          platform: 'openai',
          type: 'oauth',
          status: 'active',
          group: '12',
          search: 'bulk-target',
          privacy_mode: 'training_set_cf_blocked',
        },
        previewCount: 5,
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth'],
      },
    })

    await wrapper.get('#bulk-edit-status-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith({
      filters: {
        platform: 'openai',
        type: 'oauth',
        status: 'active',
        group: '12',
        search: 'bulk-target',
        privacy_mode: 'training_set_cf_blocked',
      },
      status: 'active',
    })
  })
  // Fingerprint editing is selected with the off choice left unchanged. The
  // update contains a nonempty extra patch with codex_fingerprint_mode set to
  // off, allowing replacement of stored modes.
  it('OpenAI OAuth 批量编辑选择「关闭」时应显式提交 codex_fingerprint_mode=off（issue #6327）', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    // Leave the value selector untouched so enabling the apply checkbox alone
    // exercises submission of its default off value.
    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'off',
      },
    })

    // A selected fingerprint update needs an actual extra member; an empty
    // object cannot replace the provider mode in a merge patch.
    const payload = vi.mocked(adminAPI.providers.bulkUpdate).mock.calls[0][1] as {
      // Extra fields included in the bulk patch are applied; omitted members
      // leave the provider values unchanged.
      extra: Record<string, unknown>
    }
    expect(Object.keys(payload.extra).length).toBeGreaterThan(0)
  })

  // Fingerprint editing selects an explicit session opt-in mode. The extra
  // patch forwards that value unchanged so bulk updates retain the
  // administrator's selected fingerprint behavior.
  it('OpenAI OAuth 批量编辑显式 opt-in 模式仍原样提交', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-codex-fingerprint-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-codex-fingerprint-mode-select"]').setValue('session')
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_fingerprint_mode: 'session',
      },
    })
  })

  // The administrator applies client policy without enabling fingerprint
  // editing. The resulting extra patch omits codex_fingerprint_mode, leaving
  // unrelated stored fingerprint choices outside this update.
  it('未勾选编辑该项时不写入 codex_fingerprint_mode', async () => {
    const wrapper = mountModal({
      selectedPlatforms: ['openai'],
      selectedTypes: ['oauth'],
    })

    await wrapper.get('#bulk-edit-openai-codex-cli-only-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    const payload = vi.mocked(adminAPI.providers.bulkUpdate).mock.calls[0][1] as {
      // Extra fields included in the bulk patch are applied; omitted members
      // leave the provider values unchanged.
      extra: Record<string, unknown>
    }
    expect(payload.extra).not.toHaveProperty('codex_fingerprint_mode')
  })

  // OpenAI OAuth and Anthropic API Key targets have different credential
  // capabilities. Base URL and warmup controls are absent for the former and
  // visible for the latter.
  it('Base URL 与预热拦截只对适用的账号类型显示', async () => {
    const openaiOAuth = mountModal({ selectedPlatforms: ['openai'], selectedTypes: ['oauth'] })
    expect(openaiOAuth.find('#bulk-edit-base-url-enabled').exists()).toBe(false)
    expect(openaiOAuth.find('#bulk-edit-intercept-warmup-enabled').exists()).toBe(false)
    openaiOAuth.unmount()

    const anthropicKey = mountModal({ selectedPlatforms: ['anthropic'], selectedTypes: ['apikey'] })
    expect(anthropicKey.find('#bulk-edit-base-url-enabled').exists()).toBe(true)
    expect(anthropicKey.find('#bulk-edit-intercept-warmup-enabled').exists()).toBe(true)
    anthropicKey.unmount()
  })

  // A throttle option is chosen before RPM editing is applied. Submission
  // initially omits extra, then includes the throttle mode after the RPM apply
  // checkbox is enabled.
  it('未勾选 RPM 限制时不提交用户消息限速', async () => {
    const wrapper = mountModal({ selectedPlatforms: ['anthropic'], selectedTypes: ['oauth'] })
    await wrapper.get('[data-settings-tab-button="quota"]').trigger('click')
    const throttle = wrapper.findAll('#bulk-edit-rpm-limit-body button[role="radio"]')[1]
    await throttle.trigger('click')
    await wrapper.get('#bulk-edit-status-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()

    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledTimes(1)
    expect(vi.mocked(adminAPI.providers.bulkUpdate).mock.calls[0]?.[1]).not.toHaveProperty('extra')

    vi.mocked(adminAPI.providers.bulkUpdate).mockClear()
    await wrapper.get('#bulk-edit-rpm-limit-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(vi.mocked(adminAPI.providers.bulkUpdate).mock.calls[0]?.[1]?.extra).toMatchObject({
      user_msg_queue_mode: 'throttle',
    })
    wrapper.unmount()
  })

  // A setting body begins with its apply checkbox unchecked. The body is inert
  // until that checkbox is selected, preventing interaction with values
  // excluded from submission.
  it('未勾选应用的设置项保持置灰且不可聚焦', async () => {
    const wrapper = mountModal({ selectedPlatforms: ['anthropic'], selectedTypes: ['apikey'] })
    const body = wrapper.get('#bulk-edit-proxy-body')
    expect(body.attributes('inert')).toBeDefined()
    await wrapper.get('#bulk-edit-proxy-enabled').setValue(true)
    expect(body.attributes('inert')).toBeUndefined()
    wrapper.unmount()
  })
  // A batch changes only generation mode and then encounters a save failure.
  // Editing mode remains absent from the payload, while the selected generation
  // mode and application checkbox remain available for retrying the same change.
  it('批量生图模式独立提交，保存失败后保留选择', async () => {
    const wrapper = mountModal({ selectedPlatforms: ['openai'], selectedTypes: ['apikey'] })
    await wrapper.get('[data-settings-tab-button="request"]').trigger('click')
    await wrapper.get('#bulk-images_generations_mode-enabled').setValue(true)
    await wrapper
      .get('#bulk-images_generations_mode-body')
      .findAll('[role="radio"]')[1]
      .trigger('click')
    vi.mocked(adminAPI.providers.bulkUpdate).mockRejectedValueOnce(new Error('save failed'))
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    const credentials = vi.mocked(adminAPI.providers.bulkUpdate).mock.calls.at(-1)?.[1]?.credentials
    expect(credentials).toEqual({ images_generations_mode: 'non_streaming' })
    expect(wrapper.get('#bulk-images_generations_mode-enabled').element).toHaveProperty(
      'checked',
      true,
    )
    expect(
      wrapper
        .get('#bulk-images_generations_mode-body')
        .findAll('[role="radio"]')[1]
        .attributes('aria-checked'),
    ).toBe('true')
    expect(wrapper.emitted('close')).toBeUndefined()
    wrapper.unmount()
  })
  // OAuth bulk editing submits a selected text driver and supports explicitly clearing it.
  // The opt-in checkbox controls persistence independently from the image tool policy.
  it.each(['gpt-6.1-sol', ''])('bulk updates the image Responses driver to %s', async (model) => {
    const wrapper = mountModal({ selectedPlatforms: ['openai'], selectedTypes: ['oauth'] })
    await wrapper.get('#bulk-edit-image-responses-model-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-image-responses-model-input"]').setValue(model)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: { codex_image_responses_model: model },
    })
    wrapper.unmount()
  })

  // Preloaded current-page rows satisfy model-restriction prefill without issuing
  // redundant getById requests, and filtered-target mode skips individual reads.
  it('reuses selectedProviders rows and skips getById in filtered mode', async () => {
    vi.mocked(adminAPI.providers.getById).mockClear()
    const wrapper = mountModal({
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey'],
      selectedProviders: [
        createProvider({ id: 1, credentials: { model_whitelist: ['claude-sonnet-4-5'] } }),
        createProvider({ id: 2, credentials: { model_whitelist: ['claude-sonnet-4-5'] } }),
      ],
    })
    await flushPromises()
    expect(adminAPI.providers.getById).not.toHaveBeenCalled()
    expect(wrapper.getComponent(ModelWhitelistSelector).props('modelValue')).toEqual([
      'claude-sonnet-4-5',
    ])
    wrapper.unmount()

    vi.mocked(adminAPI.providers.getById).mockClear()
    const filteredWrapper = mountModal({
      providerIds: [1, 2],
      selectedPlatforms: ['anthropic'],
      selectedTypes: ['apikey'],
      target: {
        mode: 'filtered',
        filters: { platform: 'anthropic' },
        previewCount: 20,
        selectedPlatforms: ['anthropic'],
        selectedTypes: ['apikey'],
      },
    })
    await flushPromises()
    expect(adminAPI.providers.getById).not.toHaveBeenCalled()
    filteredWrapper.unmount()
  })

  // Enabling the custom-error-codes apply checkbox while keeping its switch off
  // clears custom error codes across the selected providers.
  it('supports bulk disabling custom error codes with an explicit false flag', async () => {
    const wrapper = mountModal({ selectedPlatforms: ['anthropic'], selectedTypes: ['apikey'] })
    await flushPromises()
    await wrapper.get('#bulk-edit-custom-error-codes-enabled').setValue(true)
    await wrapper.get('#bulk-edit-provider-form').trigger('submit.prevent')
    await flushPromises()
    expect(adminAPI.providers.bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        custom_error_codes_enabled: false,
        custom_error_codes: [],
      },
    })
    wrapper.unmount()
  })
})

useProtocolCatalogFixture()
