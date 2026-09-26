import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DefaultPricingPanel from '../DefaultPricingPanel.vue'
import { listDefaultPricing, updateDefaultPricing } from '@/api/admin/pricing'

vi.mock('@/api/admin/pricing', () => ({ listDefaultPricing: vi.fn(), updateDefaultPricing: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const stubs = {
  TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
  DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.model"><slot name="cell-price" :row="row"/><slot name="cell-actions" :row="row"/></div><slot v-if="!data.length" name="empty"/></div>' },
  Pagination: true,
  Select: true,
  BaseDialog: { name: 'BaseDialog', props: ['show', 'title'], template: '<div v-if="show">{{title}}<slot/></div>' },
}

describe('默认价格查询', () => {
  beforeEach(() => vi.resetAllMocks())
  it('区分零价和未定价，详情保留价格单位', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 2, last_updated: '', items: [
      { model: 'free', platform: 'openai', billing_mode: 'token', price_status: 'priced', prices: [{ key: 'input', value: 0, unit: 'USD/MTok' }] },
      { model: 'unknown', platform: 'qoder', billing_mode: 'token', price_status: 'unpriced', prices: [] },
    ] })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain('0 USD/MTok')
    expect(wrapper.text()).toContain('admin.pricing.defaults.unpriced')
    const button = wrapper.findAll('button').find(item => item.text() === 'admin.pricing.defaults.details')!
    await button.trigger('click')
    expect(wrapper.findComponent({ name: 'BaseDialog' }).props('show')).toBe(true)
    expect(wrapper.text()).toContain('free')
    wrapper.unmount()
  })

  it('详情弹窗的上下文与模式开关相互独立，切换后展示对应组合的单价', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 1, last_updated: '', items: [
      { model: 'combo', platform: 'openai', billing_mode: 'token', price_status: 'priced', long_context_threshold: 200000, long_context_threshold_inclusive: true, prices: [
        { key: 'input', value: 2, unit: 'USD/MTok' },
        { key: 'output', value: 8, unit: 'USD/MTok' },
        { key: 'fast_input', value: 4, unit: 'USD/MTok' },
        { key: 'fast_output', value: 16, unit: 'USD/MTok' },
        { key: 'long_input', value: 4, unit: 'USD/MTok' },
        { key: 'long_output', value: 12, unit: 'USD/MTok' },
        { key: 'long_fast_input', value: 8, unit: 'USD/MTok' },
        { key: 'long_fast_output', value: 24, unit: 'USD/MTok' },
      ] },
    ] })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    await wrapper.findAll('button').find(item => item.text() === 'admin.pricing.defaults.details')!.trigger('click')
    const dialog = wrapper.findComponent({ name: 'BaseDialog' })
    const contextSwitch = wrapper.get('[data-testid="pricing-context-switch"]')
    const tierSwitch = wrapper.get('[data-testid="pricing-tier-switch"]')
    // 默认标准上下文 + 标准模式
    expect(dialog.text()).toContain('2 USD/MTok')
    expect(dialog.text()).toContain('8 USD/MTok')
    expect(dialog.text()).not.toContain('16 USD/MTok')
    // 切到 Fast 模式：展示 fast 单价而非倍率
    await tierSwitch.findAll('button').find(item => item.text() === 'admin.pricing.defaults.tiers.fast')!.trigger('click')
    expect(dialog.text()).toContain('4 USD/MTok')
    expect(dialog.text()).toContain('16 USD/MTok')
    // 再切到长上下文：两个开关叠加，展示 long_fast 组合单价
    expect(contextSwitch.text()).toContain('0-200k')
    await contextSwitch.findAll('button').find(item => item.text() === '200k+')!.trigger('click')
    expect(dialog.text()).toContain('8 USD/MTok')
    expect(dialog.text()).toContain('24 USD/MTok')
    // 模式切回标准：仅长上下文生效
    await tierSwitch.findAll('button').find(item => item.text() === 'admin.pricing.defaults.tiers.standard')!.trigger('click')
    expect(dialog.text()).toContain('12 USD/MTok')
    wrapper.unmount()
  })

  it('筛选变化发起一次分页查询，组件关闭时取消在途请求', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 0, items: [], last_updated: '' })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    expect(wrapper.findAllComponents({ name: 'Select' })).toHaveLength(0)
    await wrapper.get('button[aria-label="common.filter"]').trigger('click')
    const selects = wrapper.findAllComponents({ name: 'Select' })
    await selects[0].vm.$emit('update:modelValue', 'grok')
    await flushPromises()
    const call = vi.mocked(listDefaultPricing).mock.lastCall!
    expect(call[0]).toMatchObject({ page: 1, platform: 'grok' })
    wrapper.unmount()
    expect(call[1]?.aborted).toBe(true)
  })

  it('筛选支持计数、重置和点击外部收起', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 0, items: [], last_updated: '' })
    const wrapper = mount(DefaultPricingPanel, { attachTo: document.body, global: { stubs } })
    await flushPromises()
    const filter = wrapper.get('button[aria-label="common.filter"]')
    await filter.trigger('click')
    const selects = wrapper.findAllComponents({ name: 'Select' })
    selects[0].vm.$emit('update:modelValue', 'grok')
    selects[1].vm.$emit('update:modelValue', 'image')
    await flushPromises()
    expect(filter.text()).toBe('2')
    expect(vi.mocked(listDefaultPricing).mock.lastCall?.[0]).toMatchObject({ platform: 'grok', billing_mode: 'image' })
    await wrapper.findAll('button').find(button => button.text() === 'common.reset')!.trigger('click')
    await flushPromises()
    expect(vi.mocked(listDefaultPricing).mock.lastCall?.[0]).toMatchObject({ platform: '', billing_mode: '' })
    document.body.click()
    await flushPromises()
    expect(filter.attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })

  it('刷新只查询，手动更新完成后重新读取并阻止重复提交', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 0, items: [], last_updated: '' })
    let finishUpdate!: () => void
    vi.mocked(updateDefaultPricing).mockImplementation(() => new Promise<void>(resolve => { finishUpdate = resolve }))
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.refresh"]').trigger('click')
    await flushPromises()
    expect(listDefaultPricing).toHaveBeenCalledTimes(2)
    expect(updateDefaultPricing).not.toHaveBeenCalled()
    const update = wrapper.findAll('button').find(button => button.text() === 'admin.pricing.defaults.update')!
    await update.trigger('click')
    expect(update.attributes('disabled')).toBeDefined()
    expect(update.text()).toBe('admin.pricing.defaults.updating')
    await update.trigger('click')
    expect(updateDefaultPricing).toHaveBeenCalledTimes(1)
    finishUpdate()
    await flushPromises()
    expect(listDefaultPricing).toHaveBeenCalledTimes(3)
    expect(wrapper.get('[role="status"]').text()).toBe('admin.pricing.defaults.updateSuccess')
    wrapper.unmount()
  })

  it('更新失败显示错误并保留当前价格列表', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 1, items: [
      { model: 'current', platform: 'openai', billing_mode: 'token', price_status: 'priced', prices: [{ key: 'input', value: 2, unit: 'USD/MTok' }] },
    ], last_updated: '' })
    vi.mocked(updateDefaultPricing).mockRejectedValue(new Error('source unavailable'))
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'admin.pricing.defaults.update')!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.pricing.defaults.updateError')
    expect(wrapper.text()).toContain('2 USD/MTok')
    expect(listDefaultPricing).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})
