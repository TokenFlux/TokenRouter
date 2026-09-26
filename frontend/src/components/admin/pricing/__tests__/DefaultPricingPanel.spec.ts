import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DefaultPricingPanel from '../DefaultPricingPanel.vue'
import { listDefaultPricing } from '@/api/admin/pricing'

vi.mock('@/api/admin/pricing', () => ({ listDefaultPricing: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const stubs = {
  TablePageLayout: { template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>' },
  DataTable: { props: ['data'], template: '<div><div v-for="row in data" :key="row.model"><slot name="cell-price" :row="row"/><slot name="cell-actions" :row="row"/></div><slot v-if="!data.length" name="empty"/></div>' },
  Pagination: true,
  Select: true,
  BaseDialog: { name: 'BaseDialog', props: ['show', 'title'], template: '<div v-if="show">{{title}}<slot/></div>' },
}

describe('默认价格查询', () => {
  beforeEach(() => vi.clearAllMocks())
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

  it('筛选变化发起一次分页查询，组件关闭时取消在途请求', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 0, items: [], last_updated: '' })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    const selects = wrapper.findAllComponents({ name: 'Select' })
    await selects[0].vm.$emit('update:modelValue', 'grok')
    await flushPromises()
    const call = vi.mocked(listDefaultPricing).mock.lastCall!
    expect(call[0]).toMatchObject({ page: 1, platform: 'grok' })
    wrapper.unmount()
    expect(call[1]?.aborted).toBe(true)
  })
})
