import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { defineComponent } from 'vue'
import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupsView from '../GroupsView.vue'
import Select from '@/components/common/Select.vue'
import GroupModelRoutingFields from '@/components/admin/group/GroupModelRoutingFields.vue'
import GroupAdvancedSchedulerOverridesModal from '@/components/admin/group/GroupAdvancedSchedulerOverridesModal.vue'
import GroupSettingsForm from '@/components/admin/group/GroupSettingsForm.vue'
import GroupClientProtocolSelector from '@/components/admin/group/GroupClientProtocolSelector.vue'
import { defaultRoutingPolicy } from '@/components/admin/group/routingPolicy'
import type { AdminGroup } from '@/api/groups.types'

// GroupsView.tabs.spec.ts checks group editor tab navigation, complete draft submission, and
// asynchronous provider searches. Mounted components or pure helper calls exercise the real
// owning implementation, while controlled API and display dependencies expose the observable
// updates asserted by each scenario.

// Hoisted group and provider mocks control asynchronous search and persistence outcomes, while
// showError records the validation notifications asserted below.
const { groups, providers, showError } = vi.hoisted(() => ({
  groups: {
    list: vi.fn(),
    getAll: vi.fn(),
    getModelsListCandidates: vi.fn(),
    getUsageSummary: vi.fn(),
    getCapacitySummary: vi.fn(),
    getLiveCapability: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
  },
  providers: { list: vi.fn(), getById: vi.fn() },
  showError: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ adminAPI: { groups, providers } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep: vi.fn(() => false), nextStep: vi.fn() }),
}))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key }),
}))

// Dialog, Page, Layout, and Table expose editor slots and row actions while allowing the real
// group form and tab controls to mount.
const Dialog = defineComponent({
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})
const Page = defineComponent({
  template: '<div><slot name="filters" /><slot name="table" /></div>',
})
const Layout = defineComponent({ template: '<div><slot /></div>' })
const Table = defineComponent({
  props: ['data'],
  template:
    '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>',
})
// wrappers retains mounted page instances so test teardown can unmount them and remove the DOM
// used by tab and focus assertions.
const wrappers: VueWrapper[] = []

// group creates the saved group fixture used by tabbed editor integration tests.
function group(): AdminGroup {
  // The fixture supplies fields used by the rendered form; edit hydration supplies defaults for
  // absent optional settings.
  return {
    id: 42,
    name: 'Existing',
    rate_multiplier: 1,
    status: 'active',
    scheduler_type: 'basic',
    is_exclusive: false,
    model_routing: null,
    supported_model_scopes: ['claude', 'gemini_text', 'gemini_image'],
  } as AdminGroup
}

// open mounts the group page and opens its create or edit dialog. Callers choose the mode and
// saved-field overrides; the helper waits for API work so subsequent interactions address the
// ready form.
async function open(
  mode: 'create' | 'edit',
  _providerType: string,
  overrides: Partial<AdminGroup> = {},
) {
  groups.list.mockResolvedValue({ items: [{ ...group(), ...overrides }], total: 1, pages: 1 })
  const wrapper = mount(GroupsView, {
    attachTo: document.body,
    global: {
      plugins: [createPinia()],
      stubs: {
        AppLayout: Layout,
        TablePageLayout: Page,
        BaseDialog: Dialog,
        DataTable: Table,
        Select: true,
        Icon: true,
        PlatformIcon: true,
        ProviderIcon: true,
        Pagination: true,
        ConfirmDialog: true,
        EmptyState: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        GroupAdvancedSchedulerOverridesModal: true,
        VueDraggable: true,
      },
    },
  })
  wrappers.push(wrapper)
  await flushPromises()
  const button =
    mode === 'create'
      ? wrapper.get('[data-tour="groups-create-btn"]')
      : wrapper.findAll('button').find((button) => button.text() === 'common.edit')!
  await button.trigger('click')
  await flushPromises()
  if (mode === 'create') {
    await wrapper.get('[data-group-field="name"] input').setValue('New group')
  }
  return wrapper
}

// tab selects the named group settings tab and waits for pending view updates.
async function tab(wrapper: VueWrapper, name: string) {
  await wrapper.get(`[data-settings-tab-button="${name}"]`).trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  providers.list.mockResolvedValue({ items: [] })
  groups.getAll.mockResolvedValue([])
  groups.getModelsListCandidates.mockResolvedValue(['gpt-test'])
  groups.getUsageSummary.mockResolvedValue([])
  groups.getCapacitySummary.mockResolvedValue([])
  groups.getLiveCapability.mockResolvedValue({ supported: true })
  groups.create.mockResolvedValue({ id: 43 })
  groups.update.mockResolvedValue({ id: 42 })
})
afterEach(() => {
  vi.useRealTimers()
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  document.body.innerHTML = ''
})

// Both editor modes start with visible marketplace tags. Saving and reopening carries each
// independent switch through payload and hydration, while edits retain unrelated compatibility settings.
it.each(['create', 'edit'] as const)('%s 保存并重新打开模型广场标签开关', async (mode) => {
  const policy = {
    ...defaultRoutingPolicy(),
    features_config: { web_search_emulation: { anthropic: true } },
  }
  const wrapper = await open(mode, 'mixed', { routing_policy: policy })
  const discount = wrapper.get('[data-setting="show_marketplace_max_discount"]')
  const multiplier = wrapper.get('[data-setting="show_marketplace_rate_multiplier"]')
  expect(discount.attributes('aria-checked')).toBe('true')
  expect(multiplier.attributes('aria-checked')).toBe('true')
  await discount.trigger('click')
  expect(discount.attributes('aria-checked')).toBe('false')
  expect(multiplier.attributes('aria-checked')).toBe('true')
  await wrapper.get(`#${mode}-group-form`).trigger('submit')
  await flushPromises()
  const payload =
    mode === 'create' ? groups.create.mock.calls[0][0] : groups.update.mock.calls[0][1]
  expect(payload.routing_policy.features_config.hide_marketplace_max_discount).toBe(true)
  expect(policy.features_config).toEqual({ web_search_emulation: { anthropic: true } })
  if (mode === 'edit') {
    expect(payload.routing_policy.features_config.web_search_emulation).toEqual({ anthropic: true })
  }

  const reopened = await open('edit', 'mixed', { routing_policy: payload.routing_policy })
  const savedDiscount = reopened.get('[data-setting="show_marketplace_max_discount"]')
  const savedMultiplier = reopened.get('[data-setting="show_marketplace_rate_multiplier"]')
  expect(savedDiscount.attributes('aria-checked')).toBe('false')
  expect(savedMultiplier.attributes('aria-checked')).toBe('true')
  await savedMultiplier.trigger('click')
  await savedDiscount.trigger('click')
  await reopened.get('#edit-group-form').trigger('submit')
  await flushPromises()
  expect(groups.update.mock.lastCall?.[1].routing_policy.features_config).toMatchObject({
    hide_marketplace_max_discount: false,
    hide_marketplace_rate_multiplier: true,
  })
})

// A failed group update leaves the marketplace display draft open for retry.
// The selected hidden tags remain in the form after the request is rejected.
it('保存失败后保留模型广场标签开关的草稿', async () => {
  const wrapper = await open('edit', 'mixed')
  const discount = wrapper.get('[data-setting="show_marketplace_max_discount"]')
  const multiplier = wrapper.get('[data-setting="show_marketplace_rate_multiplier"]')
  await discount.trigger('click')
  await multiplier.trigger('click')
  groups.update.mockRejectedValueOnce(new Error('Save failed'))
  await wrapper.get('#edit-group-form').trigger('submit')
  await flushPromises()
  expect(showError).toHaveBeenCalled()
  expect(wrapper.find('#edit-group-form').exists()).toBe(true)
  expect(discount.attributes('aria-checked')).toBe('false')
  expect(multiplier.attributes('aria-checked')).toBe('false')
})

// A historical disabled routing policy is opened for editing. Opening performs no update, while
// saving emits an enabled policy together with the edited group settings.
it('编辑历史停用策略后保存即应用，打开表单时不提前更新服务端', async () => {
  const policy = {
    ...defaultRoutingPolicy(),
    enabled: false,
    model_mapping: { alias: 'gpt-test' },
    features: '历史文字',
  }
  const wrapper = await open('edit', 'openai', { routing_policy: policy })
  await tab(wrapper, 'models')
  expect(wrapper.get('input[aria-label="admin.groups.routingPolicy.source"]').isVisible()).toBe(
    true,
  )
  expect(wrapper.text()).not.toContain('admin.groups.routingPolicy.enabled')
  expect(groups.update).not.toHaveBeenCalled()
  expect(policy.enabled).toBe(false)
  await wrapper.get('#edit-group-form').trigger('submit')
  await flushPromises()
  expect(groups.update.mock.calls[0]?.[1].routing_policy).toMatchObject({
    enabled: true,
    model_mapping: policy.model_mapping,
    features: '历史文字',
  })
})

describe.each(['create', 'edit'] as const)('GroupsView %s tabs', (mode) => {
  // Create and edit dialogs are opened through the page. Both arrange their settings into the
  // same five responsibility tabs so navigation remains consistent across editing modes.
  it('创建与编辑均按五类职责排列页签', async () => {
    const wrapper = await open(mode, 'mixed')
    const keys = wrapper
      .findAll('[data-settings-tab-button]')
      .map((button) => button.attributes('data-settings-tab-button'))
    expect(wrapper.find('[data-settings-tab-button="pricing"]').exists()).toBe(false)
    expect(keys).toEqual(['general', 'models', 'scheduling', 'protocol', 'request'])
    expect(wrapper.get('[data-settings-tab="general"]').isVisible()).toBe(true)
    expect(
      wrapper
        .get('[data-tour="group-form-multiplier"]')
        .element.closest('[data-settings-tab]')
        ?.getAttribute('data-settings-tab'),
    ).toBe('general')
    expect(
      wrapper
        .getComponent(GroupClientProtocolSelector)
        .element.closest('[data-settings-tab]')
        ?.getAttribute('data-settings-tab'),
    ).toBe('protocol')
    expect(wrapper.find('[data-group-field="reasoning"]').exists()).toBe(true)
    expect(wrapper.find('[data-group-field="image-capabilities"]').exists()).toBe(false)
  })

  // The form exposes model, scheduler, and request compatibility sections. Each setting has one
  // editing entry point so duplicate controls cannot compete over the same draft values.
  it('模型、调度和兼容设置各自只有一个入口', async () => {
    const wrapper = await open(mode, 'mixed')
    const positions = {
      restrict_models: 'models',
      model_routing_enabled: 'models',
      enabled: 'models',
      require_oauth_only: 'scheduling',
      require_privacy_set: 'scheduling',
      session_isolation_enabled: 'scheduling',
      availability_probe_enabled: 'scheduling',
      claude_code_only: 'protocol',
      openai_fast_policy: 'request',
      web_search_emulation: 'request',
      bedrock_cc_compat: 'request',
    }
    for (const [field, page] of Object.entries(positions)) {
      const matches = wrapper.findAll(`[data-setting="${field}"]`)
      expect(matches).toHaveLength(1)
      expect(
        matches[0].element.closest('[data-settings-tab]')?.getAttribute('data-settings-tab'),
      ).toBe(page)
    }
  })

  // Incomplete mapping rows coexist with marketplace display and compatibility edits in other
  // tabs. Validation returns to the model tab, and completing the rows saves every policy edit.
  it('跨页修改展示和兼容功能后保留未完成映射，补全后一起保存', async () => {
    const wrapper = await open(mode, 'mixed', {
      routing_policy: {
        ...defaultRoutingPolicy(),
        features: '历史说明',
        features_config: { untouched: { openai: false } },
      },
    })
    await tab(wrapper, 'models')
    const add = wrapper
      .get('[data-group-field="routing-policy"]')
      .findAll('button')
      .find((button) => button.text() === 'admin.groups.routingPolicy.addMapping')!
    await add.trigger('click')
    await add.trigger('click')
    const targets = wrapper.findAll('input[aria-label="admin.groups.routingPolicy.target"]')
    await targets[0].setValue('first-target')
    await targets[1].setValue('second-target')
    await tab(wrapper, 'general')
    await wrapper.get('[data-setting="show_marketplace_max_discount"]').trigger('click')
    await wrapper.get('[data-setting="show_marketplace_rate_multiplier"]').trigger('click')
    await tab(wrapper, 'request')
    await wrapper.get('[data-setting="web_search_emulation"]').trigger('click')
    await tab(wrapper, 'models')
    expect(
      wrapper
        .findAll('input[aria-label="admin.groups.routingPolicy.target"]')
        .map((input) => (input.element as HTMLInputElement).value),
    ).toEqual(['first-target', 'second-target'])
    await tab(wrapper, 'general')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-settings-tab="models"]').isVisible()).toBe(true)
    expect(groups[mode === 'create' ? 'create' : 'update']).not.toHaveBeenCalled()
    const sources = wrapper.findAll('input[aria-label="admin.groups.routingPolicy.source"]')
    await sources[0].setValue('first-alias')
    await sources[1].setValue('second-alias')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload =
      mode === 'create' ? groups.create.mock.calls[0][0] : groups.update.mock.calls[0][1]
    expect(payload.routing_policy.model_mapping).toEqual({
      'first-alias': 'first-target',
      'second-alias': 'second-target',
    })
    expect(payload.routing_policy.features_config.web_search_emulation).toEqual({ anthropic: true })
    expect(payload.routing_policy.features_config).toMatchObject({
      hide_marketplace_max_discount: true,
      hide_marketplace_rate_multiplier: true,
    })
    if (mode === 'edit') {
      expect(payload.routing_policy.features).toBe('历史说明')
      expect(payload.routing_policy.features_config.untouched).toEqual({ openai: false })
    }
  })

  // Advanced scheduler overrides are edited in their nested dialog. Saving that dialog updates
  // the main draft first, and the later group submission includes those values in its complete
  // payload.
  it('高级调度参数先回写主草稿，再随整份表单保存', async () => {
    const wrapper = await open(mode, 'mixed')
    await tab(wrapper, 'scheduling')
    wrapper
      .findAllComponents(Select)
      .find((select) => select.attributes('id') === `${mode}-group-scheduler`)!
      .vm.$emit('update:modelValue', 'advanced')
    await flushPromises()
    const settings = wrapper.getComponent(GroupSettingsForm)
    settings.vm.$emit('configureScheduler')
    await flushPromises()
    wrapper
      .getComponent(GroupAdvancedSchedulerOverridesModal)
      .vm.$emit('save', { lb_top_k: 7, sticky_weighted_enabled: false })
    await flushPromises()
    expect(groups[mode === 'create' ? 'create' : 'update']).not.toHaveBeenCalled()
    expect(settings.props('modelValue').advanced_scheduler_overrides).toEqual({
      lb_top_k: 7,
      sticky_weighted_enabled: false,
    })
    await tab(wrapper, 'models')
    await wrapper.get('[data-setting="model_routing_enabled"]').trigger('click')
    wrapper.getComponent(GroupModelRoutingFields).vm.$emit('add')
    await flushPromises()
    const routing = wrapper.getComponent(GroupModelRoutingFields)
    const rule = routing.props('rules')[0]
    routing.vm.$emit('pattern', rule, 'gpt-*')
    routing.vm.$emit('selectProvider', rule, { id: 19, name: 'Selected provider' })
    await tab(wrapper, 'general')
    await tab(wrapper, 'models')
    expect(wrapper.getComponent(GroupModelRoutingFields).props('rules')[0]).toEqual({
      pattern: 'gpt-*',
      providers: [{ id: 19, name: 'Selected provider' }],
    })
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload =
      mode === 'create' ? groups.create.mock.calls[0][0] : groups.update.mock.calls[0][1]
    expect(payload.scheduler_type).toBe('advanced')
    expect(payload.advanced_scheduler_overrides).toEqual({
      lb_top_k: 7,
      sticky_weighted_enabled: false,
    })
    expect(payload.model_routing).toEqual({ 'gpt-*': [19] })
  })

  // A provider search remains pending when its rule is deleted. The late response is discarded
  // and another rule retains its own selected provider, protecting stable rule ownership.
  it('删除规则后丢弃迟到搜索结果，其他规则仍保留自己的提供商', async () => {
    const wrapper = await open(mode, 'mixed')
    await tab(wrapper, 'models')
    await wrapper.get('[data-setting="model_routing_enabled"]').trigger('click')
    const routing = wrapper.getComponent(GroupModelRoutingFields)
    routing.vm.$emit('add')
    routing.vm.$emit('add')
    await flushPromises()
    const [removed, retained] = routing.props('rules')
    let resolveOld!: (value: {
      items: {
        id: number // stable record identifier
        name: string // record display name
      }[] // returned provider search results
    }) => void
    providers.list.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveOld = resolve
      }),
    )
    providers.list.mockResolvedValueOnce({ items: [{ id: 24, name: 'Current result' }] })
    vi.useFakeTimers()
    routing.vm.$emit('search', removed, 'old')
    await vi.advanceTimersByTimeAsync(500)
    const removedKey = routing.props('getKey')(removed)
    routing.vm.$emit('remove', removed)
    routing.vm.$emit('search', retained, 'current')
    await vi.advanceTimersByTimeAsync(500)
    resolveOld({ items: [{ id: 23, name: 'Stale result' }] })
    await flushPromises()
    const retainedKey = routing.props('getKey')(retained)
    expect(routing.props('search').results[removedKey]).toBeUndefined()
    expect(routing.props('search').results[retainedKey]).toEqual([
      { id: 24, name: 'Current result' },
    ])
    routing.vm.$emit('selectProvider', retained, { id: 24, name: 'Current result' })
    await tab(wrapper, 'general')
    await tab(wrapper, 'models')
    expect(routing.props('rules')).toHaveLength(1)
    expect(routing.props('rules')[0].providers).toEqual([{ id: 24, name: 'Current result' }])
  })

  // Fields across tabs are edited before a single submission. The payload includes the base
  // multiplier and Fast policy, and reopening starts navigation at the basic settings tab.
  it('跨页草稿一次提交，基础倍率与 Fast 路由策略保存，重新打开回到基本信息', async () => {
    const wrapper = await open(mode, 'openai')
    await tab(wrapper, 'models')
    await wrapper.get('[data-tour="group-form-multiplier"]').setValue('1.5')
    await tab(wrapper, 'request')
    const force = wrapper.get(`[data-testid="${mode}-openai-fast"]`).getComponent(Select)
    expect(force.props('modelValue')).toBe('follow_request')
    expect(
      force.props('options').map(
        (option: {
          value: string // stored selector value
        }) => option.value,
      ),
    ).toEqual(['follow_request', 'force_priority', 'force_ultrafast', 'force_off'])
    force.vm.$emit('update:modelValue', 'force_ultrafast')
    await flushPromises()
    await tab(wrapper, 'protocol')
    wrapper
      .getComponent(GroupClientProtocolSelector)
      .vm.$emit('update:modelValue', ['anthropic_messages'])
    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.groups.openaiMessages.exactMappingTitle')
    await tab(wrapper, 'models')
    await wrapper
      .get('[data-settings-tab="models"]')
      .findAll('button')
      .find((button) => button.text() === 'admin.groups.routingPolicy.addMapping')!
      .trigger('click')
    await wrapper
      .get('input[aria-label="admin.groups.routingPolicy.source"]')
      .setValue('claude-sonnet-4-6')
    await wrapper.get('input[aria-label="admin.groups.routingPolicy.target"]').setValue('gpt-test')
    await tab(wrapper, 'models')
    expect(
      (wrapper.get('[data-tour="group-form-multiplier"]').element as HTMLInputElement).value,
    ).toBe('1.5')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload =
      mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload.routing_policy.model_mapping).toEqual({ 'claude-sonnet-4-6': 'gpt-test' })
    expect(payload.messages_dispatch_model_config).toBeUndefined()
    expect(wrapper.find(`#${mode}-group-form`).exists()).toBe(false)
    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
    expect(wrapper.get('[data-settings-tab="general"]').isVisible()).toBe(true)
  })

  // Invalid name, multiplier, and reasoning values exist on hidden tabs. Submission reveals the
  // relevant field and remains blocked so tab visibility cannot bypass validation.
  it('隐藏页签的名称、倍率和推理错误均可定位且阻止提交', async () => {
    const wrapper = await open(mode, 'openai')
    await wrapper.get('[data-group-field="name"] input').setValue('   ')
    await tab(wrapper, 'models')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-settings-tab="general"]').isVisible()).toBe(true)
    await wrapper.get('[data-group-field="name"] input').setValue('Valid')
    await wrapper.get('[data-tour="group-form-multiplier"]').setValue('-1')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-settings-tab="general"]').isVisible()).toBe(true)
    await wrapper.get('[data-tour="group-form-multiplier"]').setValue('1')
    await tab(wrapper, 'request')
    await wrapper.get('[data-group-field="reasoning"] button').trigger('click')
    await tab(wrapper, 'general')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-settings-tab="request"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-group-field="reasoning"] [role="alert"]').exists()).toBe(true)
    expect(groups[mode === 'create' ? 'create' : 'update']).not.toHaveBeenCalled()
  })

  // An enabled probe lacks its model or prompt during submission. Validation selects scheduling
  // and focuses the corresponding missing field before any group update is sent.
  it('探测缺少模型或提示词时回到调度页并定位具体字段', async () => {
    const wrapper = await open(mode, 'openai')
    await tab(wrapper, 'scheduling')
    await wrapper.get('[data-group-field="probe"] button').trigger('click')
    await tab(wrapper, 'models')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-settings-tab="scheduling"]').isVisible()).toBe(true)
    expect(showError).toHaveBeenLastCalledWith('admin.groups.availabilityProbe.modelRequired')
    wrapper
      .findAllComponents(Select)
      .find((select) => select.attributes('data-group-field') === 'probe-model')!
      .vm.$emit('update:modelValue', 'gpt-test')
    await wrapper.get('[data-group-field="probe-prompt"]').setValue('   ')
    await tab(wrapper, 'protocol')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-settings-tab="scheduling"]').isVisible()).toBe(true)
    expect(document.activeElement).toBe(wrapper.get('[data-group-field="probe-prompt"]').element)
    expect(showError).toHaveBeenLastCalledWith('admin.groups.availabilityProbe.promptRequired')
    expect(groups[mode === 'create' ? 'create' : 'update']).not.toHaveBeenCalled()
  })

  // An administrator saves the group after its candidates load, leaving the custom list disabled.
  // Both create and edit submissions keep the unconfigured model list empty.
  it('保存分组时不将默认候选写入未启用的模型列表', async () => {
    const wrapper = await open(mode, 'openai')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload =
      mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload.models_list_config).toEqual({ enabled: false, models: [] })
  })

  // The custom model list is edited before submission. Selected display models remain in the
  // payload and removed legacy group settings are excluded from the submitted API fields.
  it('模型列表保留展示选择结果，提交不包含已移除的分组设置', async () => {
    const wrapper = await open(mode, 'antigravity')
    await tab(wrapper, 'request')
    for (const setting of ['claude', 'gemini_text', 'gemini_image', 'mcp_xml_inject']) {
      expect(wrapper.find(`[data-setting="${setting}"]`).exists()).toBe(false)
    }
    await tab(wrapper, 'models')
    await wrapper.get('[data-setting="enabled"]').trigger('click')
    const model = wrapper.get('[data-model-visibility="gpt-test"]')
    expect(model.attributes('aria-checked')).toBe('false')
    await model.trigger('click')
    expect(model.attributes('aria-checked')).toBe('true')
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload =
      mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload).not.toHaveProperty('supported_model_scopes')
    expect(payload).not.toHaveProperty('mcp_xml_inject')
    expect(payload.models_list_config).toMatchObject({ enabled: true, models: ['gpt-test'] })
  })
})

useProtocolCatalogFixture()

// The protocol catalog arrives after the create draft is established. Defaults initialize once,
// and a later user-cleared selection stays empty when the group is submitted.
it('首次加载目录后初始化创建默认值，清空后提交不恢复默认值', async () => {
  const { protocolCatalog } = await import('@/api/admin/protocolCapabilities')
  const { default: client } = await import('@/api/client')
  const { default: fixture } = await import('@/__tests__/fixtures/protocol-catalog.json')
  protocolCatalog.value = null
  const request = vi.spyOn(client, 'get').mockResolvedValue({ data: structuredClone(fixture) })
  try {
    const wrapper = await open('create', 'anthropic')
    const selector = wrapper.getComponent(GroupClientProtocolSelector)
    const defaults = fixture.groups[0]
    expect(selector.props('modelValue')).toEqual(defaults.defaults)
    expect(selector.props('fallbacks')).toEqual(defaults.default_fallbacks)
    selector.vm.$emit('update:modelValue', [])
    selector.vm.$emit('update:fallbacks', {})
    await flushPromises()
    expect(selector.props('modelValue')).toEqual([])
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()
    expect(groups.create.mock.calls[0]?.[0]).toMatchObject({
      allowed_protocols: [],
      protocol_fallbacks: {},
    })
    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.getComponent(GroupClientProtocolSelector).props('modelValue')).toEqual(
      defaults.defaults,
    )
    expect(wrapper.getComponent(GroupClientProtocolSelector).props('fallbacks')).toEqual(
      defaults.default_fallbacks,
    )
  } finally {
    request.mockRestore()
  }
})

// Both create and edit modes are exercised with the same protocol controls. The parameterized
// cases protect persisted protocol selection and conversion policy across the two page
// submission paths.
it.each(['create', 'edit'] as const)(
  '目录失败时阻止 %s 提交，重试恢复且保留编辑的空配置',
  async (mode) => {
    const { protocolCatalog } = await import('@/api/admin/protocolCapabilities')
    const { default: client } = await import('@/api/client')
    const { default: fixture } = await import('@/__tests__/fixtures/protocol-catalog.json')
    protocolCatalog.value = null
    const request = vi.spyOn(client, 'get').mockRejectedValue(new Error('offline'))
    try {
      const wrapper = await open(mode, 'anthropic', {
        allowed_protocols: [],
        protocol_fallbacks: {},
      })
      await wrapper.get(`#${mode}-group-form`).trigger('submit')
      await flushPromises()
      expect(groups.create).not.toHaveBeenCalled()
      expect(groups.update).not.toHaveBeenCalled()
      expect(
        wrapper.getComponent(GroupClientProtocolSelector).find('[role="alert"]').exists(),
      ).toBe(true)
      request.mockResolvedValue({ data: structuredClone(fixture) })
      await wrapper.get('[data-testid="protocol-catalog-retry"]').trigger('click')
      await flushPromises()
      const selector = wrapper.getComponent(GroupClientProtocolSelector)
      expect(selector.find('[role="alert"]').exists()).toBe(false)
      expect(selector.props('modelValue')).toEqual(
        mode === 'create' ? fixture.groups[0].defaults : [],
      )
      await wrapper.get(`#${mode}-group-form`).trigger('submit')
      await flushPromises()
      expect(groups[mode === 'create' ? 'create' : 'update']).toHaveBeenCalledTimes(1)
    } finally {
      request.mockRestore()
    }
  },
)

// Both editor modes intersect configured entries with backend probe capabilities. Saved
// selections survive an admission change, and submission reports the mismatch without resetting them.
it.each(['create', 'edit'] as const)('%s 探针协议按允许范围展示并校验失效选择', async (mode) => {
  const wrapper = await open(mode, 'mixed', {
    allowed_protocols: ['openai_responses', 'openai_embeddings', 'openai_images_generations'],
    availability_probe_config: { enabled: true, model_id: 'gpt-test', prompt: 'hi' },
  })
  const settings = wrapper.findComponent(GroupSettingsForm)
  settings.vm.$emit('patch', {
    allowed_protocols: ['openai_responses', 'openai_embeddings', 'openai_images_generations'],
    availability_probe_enabled: true,
    availability_probe_model_id: 'gpt-test',
    availability_probe_prompt: 'hi',
  })
  await flushPromises()
  await tab(wrapper, 'scheduling')
  const selector = wrapper
    .findAllComponents(Select)
    .find((item) => item.attributes('id') === `${mode}-group-probe-protocol`)!
  expect(selector.props('modelValue')).toBe('')
  expect(selector.props('options').map((item: { value: string }) => item.value)).toEqual([
    '',
    'openai_responses',
    'openai_images_generations',
  ])
  selector.vm.$emit('update:modelValue', 'openai_responses')
  await flushPromises()
  await wrapper.get(`#${mode}-group-form`).trigger('submit')
  await flushPromises()
  const payload =
    mode === 'create' ? groups.create.mock.lastCall?.[0] : groups.update.mock.lastCall?.[1]
  expect(payload.availability_probe_config.protocol).toBe('openai_responses')
  expect(payload.availability_probe_protocol).toBeUndefined()

  const reopened = await open('edit', 'mixed', {
    ...payload,
    allowed_protocols: ['openai_images_generations'],
  })
  await tab(reopened, 'scheduling')
  const saved = reopened
    .findAllComponents(Select)
    .find((item) => item.attributes('id') === 'edit-group-probe-protocol')!
  expect(saved.props('modelValue')).toBe('openai_responses')
  expect(reopened.get('[data-group-field="probe-protocol"] [role="alert"]').text()).toBe(
    'admin.groups.availabilityProbe.protocolUnavailable',
  )
  const before = groups.update.mock.calls.length
  await reopened.get('#edit-group-form').trigger('submit')
  await flushPromises()
  expect(groups.update.mock.calls).toHaveLength(before)
  expect(showError).toHaveBeenLastCalledWith('admin.groups.availabilityProbe.protocolUnavailable')
  expect(saved.props('modelValue')).toBe('openai_responses')
})
