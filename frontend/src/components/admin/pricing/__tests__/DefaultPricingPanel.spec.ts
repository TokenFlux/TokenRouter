import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DefaultPricingPanel from '../DefaultPricingPanel.vue'
import { listDefaultPricing, updateDefaultPricing } from '@/api/admin/pricing'

// DefaultPricingPanel.spec.ts checks catalog price dimensions, paging cancellation, filtering,
// and explicit catalog updates. Mounted components or pure helper calls exercise the real
// owning implementation, while controlled API and display dependencies expose the observable
// updates asserted by each scenario.

vi.mock('@/api/admin/pricing', () => ({
  listDefaultPricing: vi.fn(),
  updateDefaultPricing: vi.fn(),
}))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key }),
}))
// stubs supplies the display and dialog replacements used by these mounted tests, leaving the
// target component behavior active for assertions.
const stubs = {
  TablePageLayout: {
    template: '<div><slot name="filters"/><slot name="table"/><slot name="pagination"/></div>',
  },
  DataTable: {
    props: ['data'],
    template:
      '<div><div v-for="row in data" :key="row.model"><slot name="cell-price" :row="row"/><slot name="cell-actions" :row="row"/></div><slot v-if="!data.length" name="empty"/></div>',
  },
  Pagination: true,
  Select: true,
  BaseDialog: {
    name: 'BaseDialog',
    props: ['show', 'title'],
    template: '<div v-if="show">{{title}}<slot/></div>',
  },
}

describe('默认价格查询', () => {
  beforeEach(() => vi.resetAllMocks())
  // Catalog prices include zero and an absent amount. The table distinguishes free pricing from
  // unavailable pricing, and detail rows retain their reported price units.
  it('区分零价和未定价，详情保留价格单位', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({
      total: 2,
      last_updated: '',
      items: [
        {
          model: 'free',
          platform: 'openai',
          billing_mode: 'token',
          price_status: 'priced',
          prices: [{ key: 'input', value: 0, unit: 'USD/MTok' }],
        },
        {
          model: 'unknown',
          platform: 'qoder',
          billing_mode: 'token',
          price_status: 'unpriced',
          prices: [],
        },
      ],
    })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    expect(wrapper.text()).toContain('0 USD/MTok')
    expect(wrapper.text()).toContain('admin.pricing.defaults.unpriced')
    const button = wrapper
      .findAll('button')
      .find((item) => item.text() === 'admin.pricing.defaults.details')!
    await button.trigger('click')
    expect(wrapper.findComponent({ name: 'BaseDialog' }).props('show')).toBe(true)
    expect(wrapper.text()).toContain('free')
    wrapper.unmount()
  })

  // Context and service-tier controls are changed independently in the detail dialog. Displayed
  // prices follow the selected standard, long-context, Fast, and combined key dimensions.
  it('详情弹窗的上下文与模式开关相互独立，切换后展示对应组合的单价', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({
      total: 1,
      last_updated: '',
      items: [
        {
          model: 'combo',
          platform: 'openai',
          billing_mode: 'token',
          price_status: 'priced',
          long_context_threshold: 200000,
          long_context_threshold_inclusive: true,
          prices: [
            { key: 'input', value: 2, unit: 'USD/MTok' },
            { key: 'output', value: 8, unit: 'USD/MTok' },
            { key: 'fast_input', value: 4, unit: 'USD/MTok' },
            { key: 'fast_output', value: 16, unit: 'USD/MTok' },
            { key: 'long_input', value: 4, unit: 'USD/MTok' },
            { key: 'long_output', value: 12, unit: 'USD/MTok' },
            { key: 'long_fast_input', value: 8, unit: 'USD/MTok' },
            { key: 'long_fast_output', value: 24, unit: 'USD/MTok' },
          ],
        },
      ],
    })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((item) => item.text() === 'admin.pricing.defaults.details')!
      .trigger('click')
    const dialog = wrapper.findComponent({ name: 'BaseDialog' })
    const contextSwitch = wrapper.get('[data-testid="pricing-context-switch"]')
    const tierSwitch = wrapper.get('[data-testid="pricing-tier-switch"]')
    // The initial detail selection uses standard context and the standard tier.
    expect(dialog.text()).toContain('2 USD/MTok')
    expect(dialog.text()).toContain('8 USD/MTok')
    expect(dialog.text()).not.toContain('16 USD/MTok')
    // Selecting Fast changes the displayed quote to its Fast tier amount.
    await tierSwitch
      .findAll('button')
      .find((item) => item.text() === 'admin.pricing.defaults.tiers.fast')!
      .trigger('click')
    expect(dialog.text()).toContain('4 USD/MTok')
    expect(dialog.text()).toContain('16 USD/MTok')
    // Long context combines with Fast to select the long_fast quote rather than resetting the
    // tier.
    expect(contextSwitch.text()).toContain('0-200k')
    await contextSwitch
      .findAll('button')
      .find((item) => item.text() === '200k+')!
      .trigger('click')
    expect(dialog.text()).toContain('8 USD/MTok')
    expect(dialog.text()).toContain('24 USD/MTok')
    // Returning to the standard tier retains the selected long-context dimension.
    await tierSwitch
      .findAll('button')
      .find((item) => item.text() === 'admin.pricing.defaults.tiers.standard')!
      .trigger('click')
    expect(dialog.text()).toContain('12 USD/MTok')
    wrapper.unmount()
  })

  // A catalog model contains multiple context intervals and a free amount. The detail selector
  // exposes each interval and retains zero when rendering the chosen interval price.
  it('展示 models.dev 的多个上下文阶梯并保留零价', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({
      total: 1,
      last_updated: '',
      items: [
        {
          model: 'multi',
          platform: 'openai',
          billing_mode: 'token',
          price_status: 'priced',
          prices: [{ key: 'input', value: 1, unit: 'USD/MTok' }],
          context_intervals: [
            {
              min_tokens: 0,
              max_tokens: 100000,
              prices: [{ key: 'input', value: 1, unit: 'USD/MTok' }],
            },
            {
              min_tokens: 100000,
              max_tokens: 200000,
              prices: [{ key: 'input', value: 2, unit: 'USD/MTok' }],
            },
            {
              min_tokens: 200000,
              max_tokens: null,
              prices: [{ key: 'input', value: 0, unit: 'USD/MTok' }],
            },
          ],
        },
      ],
    })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((item) => item.text() === 'admin.pricing.defaults.details')!
      .trigger('click')
    const buttons = wrapper.get('[data-testid="pricing-context-switch"]').findAll('button')
    expect(buttons).toHaveLength(3)
    await buttons[1]!.trigger('click')
    expect(wrapper.findComponent({ name: 'BaseDialog' }).text()).toContain('2 USD/MTok')
    await buttons[2]!.trigger('click')
    expect(wrapper.findComponent({ name: 'BaseDialog' }).text()).toContain('0 USD/MTok')
    wrapper.unmount()
  })

  // Filters change while catalog requests are controlled by the test. The panel requests the
  // selected page once and aborts its active read when unmounted.
  it('筛选变化发起一次分页查询，组件关闭时取消在途请求', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 0, items: [], last_updated: '' })
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    expect(wrapper.findAllComponents({ name: 'FilterOptions' })).toHaveLength(0)
    await wrapper.get('button[aria-label="common.filter"]').trigger('click')
    const filterOptions = wrapper.findAllComponents({ name: 'FilterOptions' })
    await filterOptions[0].vm.$emit('update:modelValue', 'grok')
    await flushPromises()
    const call = vi.mocked(listDefaultPricing).mock.lastCall!
    expect(call[0]).toMatchObject({ page: 1, platform: 'grok' })
    wrapper.unmount()
    expect(call[1]?.aborted).toBe(true)
  })

  // The filter dropdown is opened, configured, reset, and dismissed from outside. Its badge
  // count and queries follow the selected constraints, and dismissal closes the panel.
  it('筛选支持计数、重置和点击外部收起', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 0, items: [], last_updated: '' })
    const wrapper = mount(DefaultPricingPanel, { attachTo: document.body, global: { stubs } })
    await flushPromises()
    const filter = wrapper.get('button[aria-label="common.filter"]')
    await filter.trigger('click')
    const filterOptions = wrapper.findAllComponents({ name: 'FilterOptions' })
    filterOptions[0].vm.$emit('update:modelValue', 'grok')
    filterOptions[1].vm.$emit('update:modelValue', 'image')
    await flushPromises()
    expect(filter.text()).toBe('2')
    expect(vi.mocked(listDefaultPricing).mock.lastCall?.[0]).toMatchObject({
      platform: 'grok',
      billing_mode: 'image',
    })
    await wrapper.get('[data-testid="filter-reset"]').trigger('click')
    await flushPromises()
    expect(vi.mocked(listDefaultPricing).mock.lastCall?.[0]).toMatchObject({
      platform: '',
      billing_mode: '',
    })
    document.body.click()
    await flushPromises()
    expect(filter.attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })

  // Read-only refresh and catalog update actions run against separate API mocks. Update
  // completion reloads prices and repeated clicks during the pending update do not submit
  // again.
  it('刷新只查询，手动更新完成后重新读取并阻止重复提交', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({ total: 0, items: [], last_updated: '' })
    let finishUpdate!: () => void
    vi.mocked(updateDefaultPricing).mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          finishUpdate = resolve
        }),
    )
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    await wrapper.get('button[aria-label="common.refresh"]').trigger('click')
    await flushPromises()
    expect(listDefaultPricing).toHaveBeenCalledTimes(2)
    expect(updateDefaultPricing).not.toHaveBeenCalled()
    const update = wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.pricing.defaults.update')!
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

  // A catalog update fails after prices were already loaded. The panel displays the update
  // error while keeping the current price rows available for inspection.
  it('更新失败显示错误并保留当前价格列表', async () => {
    vi.mocked(listDefaultPricing).mockResolvedValue({
      total: 1,
      items: [
        {
          model: 'current',
          platform: 'openai',
          billing_mode: 'token',
          price_status: 'priced',
          prices: [{ key: 'input', value: 2, unit: 'USD/MTok' }],
        },
      ],
      last_updated: '',
    })
    vi.mocked(updateDefaultPricing).mockRejectedValue(new Error('source unavailable'))
    const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'admin.pricing.defaults.update')!
      .trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.pricing.defaults.updateError')
    expect(wrapper.text()).toContain('2 USD/MTok')
    expect(listDefaultPricing).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
})

// Video dimensions contain custom names that resemble token modifier prefixes. Detail rendering
// includes every resolution label intact instead of filtering them through token tier
// selection.
it('shows all custom video dimensions without interpreting token prefixes', async () => {
  vi.mocked(listDefaultPricing).mockResolvedValue({
    total: 1,
    last_updated: '',
    items: [
      {
        model: 'custom-video',
        platform: 'grok',
        billing_mode: 'video',
        price_status: 'priced',
        prices: [
          { key: '768p', value: 0.12, unit: 'USD/s' },
          { key: '2k', value: 0, unit: 'USD/s' },
          { key: 'fast_custom', value: 0.3, unit: 'USD/s' },
          { key: 'long_custom', value: 0.4, unit: 'USD/s' },
        ],
      },
    ],
  })
  const wrapper = mount(DefaultPricingPanel, { global: { stubs } })
  await flushPromises()
  await wrapper
    .findAll('button')
    .find((item) => item.text() === 'admin.pricing.defaults.details')!
    .trigger('click')
  const dialog = wrapper.findComponent({ name: 'BaseDialog' })
  for (const label of ['768p', '2k', 'fast_custom', 'long_custom'])
    expect(dialog.text()).toContain(label)
  expect(dialog.text()).toContain('0 USD/s')
  expect(wrapper.find('[data-testid="pricing-context-switch"]').exists()).toBe(false)
  expect(wrapper.find('[data-testid="pricing-tier-switch"]').exists()).toBe(false)
  wrapper.unmount()
})
