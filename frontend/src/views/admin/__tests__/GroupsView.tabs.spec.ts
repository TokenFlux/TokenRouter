import { defineComponent } from 'vue'
import { createPinia } from 'pinia'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import GroupsView from '../GroupsView.vue'
import Select from '@/components/common/Select.vue'
import GroupClientProtocolSelector from '@/components/admin/group/GroupClientProtocolSelector.vue'
import PricingEntryCard from '@/components/admin/channel/PricingEntryCard.vue'
import { pricingEntryFromAPI } from '@/components/admin/channel/pricingForm'
import type { ChannelModelPricing } from '@/api/admin/channels'
import type { AdminGroup, GroupPlatform } from '@/types'

const { groups, showError } = vi.hoisted(() => ({
  groups: {
    list: vi.fn(), getAll: vi.fn(), getModelsListCandidates: vi.fn(),
    getUsageSummary: vi.fn(), getCapacitySummary: vi.fn(), getLiveCapability: vi.fn(),
    create: vi.fn(), update: vi.fn(),
  },
  showError: vi.fn(),
}))
vi.mock('@/api/admin', () => ({ adminAPI: { groups, accounts: { list: vi.fn(), getById: vi.fn() } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn() }) }))
vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({ isCurrentStep: vi.fn(() => false), nextStep: vi.fn() }),
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const Dialog = defineComponent({
  props: ['show'],
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})
const Page = defineComponent({ template: '<div><slot name="filters" /><slot name="table" /></div>' })
const Layout = defineComponent({ template: '<div><slot /></div>' })
const Table = defineComponent({ props: ['data'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>' })
const wrappers: VueWrapper[] = []
const platforms: GroupPlatform[] = ['anthropic', 'openai', 'gemini', 'antigravity', 'grok', 'qoder', 'kimi', 'zhipu', 'deepseek']

function group(platform: GroupPlatform): AdminGroup {
  // 只提供界面依赖的存量字段，其他配置由编辑初始化逻辑使用默认值。
  return {
    id: 42, name: 'Existing', platform, rate_multiplier: 1, status: 'active',
    scheduler_type: 'basic', is_exclusive: false, model_routing: null,
    supported_model_scopes: ['claude', 'gemini_text', 'gemini_image'],

  } as AdminGroup
}

async function open(mode: 'create' | 'edit', platform: GroupPlatform, overrides: Partial<AdminGroup> = {}) {
  groups.list.mockResolvedValue({ items: [{ ...group(platform), ...overrides }], total: 1, pages: 1 })
  const wrapper = mount(GroupsView, {
    attachTo: document.body,
    global: { plugins: [createPinia()], stubs: {
      AppLayout: Layout, TablePageLayout: Page, BaseDialog: Dialog, DataTable: Table,
      Select: true, Icon: true, PlatformIcon: true, ProviderIcon: true,
      Pagination: true, ConfirmDialog: true, EmptyState: true, GroupCapacityBadge: true,
      GroupRateMultipliersModal: true, GroupRPMOverridesModal: true,
      GroupAdvancedSchedulerOverridesModal: true, VueDraggable: true,
    } },
  })
  wrappers.push(wrapper)
  await flushPromises()
  const button = mode === 'create'
    ? wrapper.get('[data-tour="groups-create-btn"]')
    : wrapper.findAll('button').find(button => button.text() === 'common.edit')!
  await button.trigger('click')
  await flushPromises()
  if (mode === 'create') {
    wrapper.findAllComponents(Select).find(select => select.attributes('data-tour') === 'group-form-platform')!
      .vm.$emit('update:modelValue', platform)
    await flushPromises()
    await wrapper.get('[data-group-field="name"] input').setValue('New group')
  }
  return wrapper
}

async function tab(wrapper: VueWrapper, name: string) {
  await wrapper.get(`[data-group-tab-button="${name}"]`).trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
  groups.getAll.mockResolvedValue([])
  groups.getModelsListCandidates.mockResolvedValue(['gpt-test'])
  groups.getUsageSummary.mockResolvedValue([])
  groups.getCapacitySummary.mockResolvedValue([])
  groups.getLiveCapability.mockResolvedValue({ supported: true })
  groups.create.mockResolvedValue({ id: 43 })
  groups.update.mockResolvedValue({ id: 42 })
})
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
  document.body.innerHTML = ''
})

describe.each(['create', 'edit'] as const)('GroupsView %s tabs', mode => {
  it.each(platforms)('%s 显示正确页签并将通用价格和协议字段放入对应页', async platform => {
    const wrapper = await open(mode, platform)
    const keys = wrapper.findAll('[data-group-tab-button]').map(button => button.attributes('data-group-tab-button'))
    expect(keys).toEqual(['anthropic', 'openai', 'gemini', 'antigravity'].includes(platform)
      ? ['general', 'platform', 'pricing', 'protocol'] : ['general', 'pricing', 'protocol'])
    expect(wrapper.get('[data-group-tab="general"]').isVisible()).toBe(true)
    expect(wrapper.get('[data-tour="group-form-multiplier"]').element.closest('[data-group-tab]')?.getAttribute('data-group-tab')).toBe('pricing')
    expect(wrapper.getComponent(GroupClientProtocolSelector).element.closest('[data-group-tab]')?.getAttribute('data-group-tab')).toBe('protocol')
    expect(wrapper.find('[data-group-field="reasoning"]').exists()).toBe(['openai', 'anthropic'].includes(platform))
    expect(wrapper.find('[data-group-field="image-capabilities"]').exists()).toBe(['openai', 'gemini', 'antigravity', 'grok'].includes(platform))
  })

  it('完整价卡在创建和编辑中开放区间及倍率，提交后可重新回填', async () => {
    const pricing: ChannelModelPricing = {
      platform: 'openai', models: ['gpt-test'], billing_mode: 'token', price_multiplier: 1.2,
      fast_multiplier: 1.5, flex_multiplier: 0.4, max_reasoning_effort_multiplier: 2,
      input_price: 0, output_price: 0.000003, cache_write_price: null, cache_write_1h_price: 0.000005,
      cache_read_price: null, image_input_price: null, image_output_price: null, per_request_price: null,
      intervals: [{ min_tokens: 100000, max_tokens: null, tier_label: '', input_price: 0.000002,
        output_price: null, cache_write_price: null, cache_read_price: null, input_multiplier: null,
        output_multiplier: 2, cache_write_multiplier: null, cache_read_multiplier: null, per_request_price: null, sort_order: 0 }],
      time_pricing: { timezone: 'Asia/Shanghai', weekdays_only: true, periods: [{ start_time: '09:00:00', end_time: '10:00:00', multiplier: 0.5 }] },
    }
    const wrapper = await open(mode, 'openai', { model_pricing: [pricing], free_openai_fast: true })
    await tab(wrapper, 'pricing')
    if (mode === 'create') {
      await wrapper.findAll('button').find(button => button.text().includes('admin.groups.modelPricing.add'))!.trigger('click')
      wrapper.getComponent(PricingEntryCard).vm.$emit('update', pricingEntryFromAPI(pricing))
      await flushPromises()
    }
    const card = wrapper.getComponent(PricingEntryCard)
    expect(card.props('hideTokenIntervals')).toBe(false)
    expect(card.props('enableTierMultipliers')).toBe(true)
    expect(card.props('enableTimePricing')).toBe(true)
    expect(card.props('entry').output_price).toBe(3)
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload = mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload.model_pricing[0]).toMatchObject(pricing)
    const reopened = await open('edit', 'openai', payload)
    await tab(reopened, 'pricing')
    expect(reopened.getComponent(PricingEntryCard).props('entry')).toEqual(pricingEntryFromAPI(payload.model_pricing[0]))
  })

  it('冲突模型阻止提交，并定位回计费页', async () => {
    const wrapper = await open(mode, 'openai')
    await tab(wrapper, 'pricing')
    await wrapper.findAll('button').find(button => button.text().includes('admin.groups.modelPricing.add'))!.trigger('click')
    const card = wrapper.getComponent(PricingEntryCard)
    card.vm.$emit('update', { ...card.props('entry'), models: ['gpt-test', 'gpt-test'], fast_multiplier: 1.5 })
    await tab(wrapper, 'general')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(groups.create).not.toHaveBeenCalled()
    expect(groups.update).not.toHaveBeenCalled()
    expect(showError).toHaveBeenCalledWith(expect.stringContaining('modelConflict'))
    expect(wrapper.get('[data-group-tab="pricing"]').isVisible()).toBe(true)
  })

  it('跨页草稿一次提交，强制与免费 Fast 独立保存，重新打开回到通用', async () => {
    const wrapper = await open(mode, 'openai')
    await tab(wrapper, 'pricing')
    await wrapper.get('[data-tour="group-form-multiplier"]').setValue('1.5')
    await wrapper.get(`[data-testid="${mode}-free-openai-fast"]`).trigger('click')
    await tab(wrapper, 'platform')
    const force = wrapper.get(`[data-testid="${mode}-openai-fast"]`).getComponent(Select)
    expect(force.props('modelValue')).toBe('follow_request')
    expect(force.props('options').map((option: { value: string }) => option.value)).toEqual(['follow_request', 'force_priority', 'force_ultrafast', 'force_off'])
    force.vm.$emit('update:modelValue', 'force_ultrafast')
    await flushPromises()
    await tab(wrapper, 'protocol')
    wrapper.getComponent(GroupClientProtocolSelector).vm.$emit('update:modelValue', ['anthropic_messages'])
    await flushPromises()
    const mapping = wrapper.get('[data-group-tab="protocol"] input[type="text"]')
    await mapping.setValue('gpt-test')
    await tab(wrapper, 'pricing')
    expect((wrapper.get('[data-tour="group-form-multiplier"]').element as HTMLInputElement).value).toBe('1.5')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload = mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload).toMatchObject({ rate_multiplier: 1.5, openai_fast_policy: 'force_ultrafast', free_openai_fast: true, allowed_client_protocols: ['anthropic_messages'] })
    expect(JSON.stringify(payload.messages_dispatch_model_config)).toContain('gpt-test')
    expect(wrapper.find(`#${mode}-group-form`).exists()).toBe(false)
    await wrapper.get('[data-tour="groups-create-btn"]').trigger('click')
    expect(wrapper.get('[data-group-tab="general"]').isVisible()).toBe(true)
  })

  it('隐藏页签的名称、倍率和推理错误均可定位且阻止提交', async () => {
    const wrapper = await open(mode, 'openai')
    await wrapper.get('[data-group-field="name"] input').setValue('   ')
    await tab(wrapper, 'pricing')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-group-tab="general"]').isVisible()).toBe(true)
    await wrapper.get('[data-group-field="name"] input').setValue('Valid')
    await wrapper.get('[data-tour="group-form-multiplier"]').setValue('-1')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-group-tab="pricing"]').isVisible()).toBe(true)
    await wrapper.get('[data-tour="group-form-multiplier"]').setValue('1')
    await tab(wrapper, 'platform')
    await wrapper.get('[data-group-field="reasoning"] button').trigger('click')
    await tab(wrapper, 'general')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-group-tab="platform"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-group-field="reasoning"] [role="alert"]').exists()).toBe(true)
    expect(groups[mode === 'create' ? 'create' : 'update']).not.toHaveBeenCalled()
  })

  it('探测缺少模型或提示词时回到通用并定位具体字段', async () => {
    const wrapper = await open(mode, 'openai')
    await wrapper.get('[data-group-field="probe"] button').trigger('click')
    await tab(wrapper, 'pricing')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-group-tab="general"]').isVisible()).toBe(true)
    expect(showError).toHaveBeenLastCalledWith('admin.groups.availabilityProbe.modelRequired')
    wrapper.findAllComponents(Select).find(select => select.attributes('data-group-field') === 'probe-model')!
      .vm.$emit('update:modelValue', 'gpt-test')
    await wrapper.get('[data-group-field="probe-prompt"]').setValue('   ')
    await tab(wrapper, 'protocol')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-group-tab="general"]').isVisible()).toBe(true)
    expect(document.activeElement).toBe(wrapper.get('[data-group-field="probe-prompt"]').element)
    expect(showError).toHaveBeenLastCalledWith('admin.groups.availabilityProbe.promptRequired')
    expect(groups[mode === 'create' ? 'create' : 'update']).not.toHaveBeenCalled()
  })

  it('批量图片能力在协议页控制价格字段，关闭图片能力沿用原清理规则', async () => {
    const wrapper = await open(mode, 'gemini')
    await tab(wrapper, 'protocol')
    const capabilities = wrapper.get('[data-group-field="image-capabilities"]')
    await capabilities.get('[role="switch"]').trigger('click')
    await capabilities.findAll('[role="switch"]')[1]!.trigger('click')
    await tab(wrapper, 'pricing')
    const batch = wrapper.findAll('input').find(input => input.attributes('placeholder') === '0.5')!
    expect(batch.isVisible()).toBe(true)
    await batch.setValue('0.4')
    await tab(wrapper, 'protocol')
    await capabilities.get('[role="switch"]').trigger('click')
    await tab(wrapper, 'pricing')
    expect(wrapper.findAll('input').some(input => input.attributes('placeholder') === '0.5')).toBe(false)
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload = mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload).toMatchObject({ allow_image_generation: false, allow_batch_image_generation: false })
  })

  it('关闭再开启 Messages 保留映射草稿，价格组件切页保持展开状态', async () => {
    const wrapper = await open(mode, 'openai')
    await tab(wrapper, 'protocol')
    const protocols = wrapper.getComponent(GroupClientProtocolSelector)
    protocols.vm.$emit('update:modelValue', ['anthropic_messages'])
    await flushPromises()
    await wrapper.get('[data-group-tab="protocol"] input[type="text"]').setValue('gpt-mapped')
    protocols.vm.$emit('update:modelValue', [])
    await flushPromises()
    expect(wrapper.find('[data-group-tab="protocol"] input[type="text"]').exists()).toBe(false)
    protocols.vm.$emit('update:modelValue', ['anthropic_messages'])
    await flushPromises()
    expect((wrapper.get('[data-group-tab="protocol"] input[type="text"]').element as HTMLInputElement).value).toBe('gpt-mapped')
    await tab(wrapper, 'pricing')
    await wrapper.findAll('button').find(button => button.text().includes('admin.groups.modelPricing.add'))!.trigger('click')
    const card = wrapper.getComponent(PricingEntryCard)
    await card.get('.cursor-pointer').trigger('click')
    const collapsedBefore = card.get('.collapsible-content').classes()
    await tab(wrapper, 'general')
    await tab(wrapper, 'pricing')
    expect(wrapper.getComponent(PricingEntryCard).element).toBe(card.element)
    expect(card.get('.collapsible-content').classes()).toEqual(collapsedBefore)
  })

  it('计费开关继续显示关联字段并保存正确的布尔值', async () => {
    const wrapper = await open(mode, 'grok')
    await tab(wrapper, 'pricing')
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    for (const field of ['peak_rate_enabled', 'long_context_pricing_enabled']) {
      await wrapper.get(`[data-group-setting="${field}"]`).trigger('click')
    }
    const times = wrapper.findAll('input[type="time"]')
    expect(times).toHaveLength(2)
    await times[0]!.setValue('09:00')
    await times[1]!.setValue('10:00')
    expect(wrapper.find('[data-group-setting=\"image_rate_independent\"]').exists()).toBe(false)
    expect(wrapper.find('[data-group-setting=\"video_rate_independent\"]').exists()).toBe(false)
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload = mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload).toMatchObject({ peak_rate_enabled: true, long_context_pricing_enabled: false })
  })

  it('模型系列与模型列表开关保留展示选择结果', async () => {
    const wrapper = await open(mode, 'antigravity')
    await tab(wrapper, 'platform')
    for (const scope of ['claude', 'gemini_text']) {
      await wrapper.get(`[data-group-setting="${scope}"]`).trigger('click')
    }
    expect(wrapper.get('[data-group-setting="gemini_image"]').attributes('aria-checked')).toBe('true')
    await tab(wrapper, 'protocol')
    await wrapper.get('[data-group-setting="enabled"]').trigger('click')
    const model = wrapper.get('[data-model-visibility="gpt-test"]')
    expect(model.attributes('aria-checked')).toBe('true')
    await model.trigger('click')
    expect(model.attributes('aria-checked')).toBe('false')
    expect(wrapper.find('input[type="checkbox"]').exists()).toBe(false)
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    const payload = mode === 'create' ? groups.create.mock.calls[0]?.[0] : groups.update.mock.calls[0]?.[1]
    expect(payload.supported_model_scopes).toEqual(['gemini_image'])
    expect(payload.models_list_config).toMatchObject({ enabled: true, models: [] })
  })

  it('校验隐藏页签中的折叠价格条目时先展开再聚焦', async () => {
    const wrapper = await open(mode, 'openai')
    await tab(wrapper, 'pricing')
    await wrapper.findAll('button').find(button => button.text().includes('admin.groups.modelPricing.add'))!.trigger('click')
    const card = wrapper.getComponent(PricingEntryCard)
    await card.get('input[type="number"]').setValue('-1')
    await card.get('.cursor-pointer').trigger('click')
    expect(card.get('.collapsible-content').classes()).toContain('collapsible-content--collapsed')
    await tab(wrapper, 'general')
    await wrapper.get(`#${mode}-group-form`).trigger('submit')
    await flushPromises()
    expect(wrapper.get('[data-group-tab="pricing"]').isVisible()).toBe(true)
    expect(card.get('.collapsible-content').classes()).not.toContain('collapsible-content--collapsed')
    expect(document.activeElement).toBe(card.get('input[type="number"]').element)
    expect(groups[mode === 'create' ? 'create' : 'update']).not.toHaveBeenCalled()
  })
})
