import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ModelPricingPanel from '../ModelPricingPanel.vue'
import type { MarketplaceModel, MarketplaceModelPricing } from '@/types'

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: { value: '点' },
  }),
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

// Icon 打桩时透出 name，便于断言展开/收起箭头方向。
const IconStub = {
  name: 'Icon',
  props: {
    name: { type: String, default: '' },
    size: { type: String, default: 'md' },
    strokeWidth: { type: Number, default: 1.5 },
  },
  template: '<span class="icon-stub" :data-icon="name" />',
}

function marketplaceModel(id: string, pricing: MarketplaceModelPricing): MarketplaceModel {
  return {
    id,
    display_name: id,
    pricing,
  }
}

function mountPanel(model: MarketplaceModel) {
  return mount(ModelPricingPanel, {
    props: { model },
    global: {
      stubs: {
        Icon: IconStub,
      },
    },
  })
}

const tokenPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  input_price_per_token: 0.000001,
  output_price_per_token: 0.000002,
}

const fastPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  input_price_per_token: 0.000001,
  output_price_per_token: 0.000002,
  fast_input_price_per_token: 0.000005,
  fast_output_price_per_token: 0.000006,
}

const intervalPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  context_intervals: [
    {
      min_tokens: 0,
      max_tokens: 32000,
      input_price_per_token: 0.000001,
      output_price_per_token: 0.000002,
    },
    {
      min_tokens: 32000,
      max_tokens: null,
      input_price_per_token: 0.000003,
      output_price_per_token: 0.000004,
    },
  ],
}

const imagePricing: MarketplaceModelPricing = {
  pricing_mode: 'image',
  price_status: 'priced',
  image_price_1k: 0.5,
}

const unpricedPricing: MarketplaceModelPricing = {
  pricing_mode: 'unknown',
  price_status: 'unpriced',
}

describe('ModelPricingPanel', () => {
  it('无定价模型不渲染展开入口', () => {
    const wrapper = mountPanel(marketplaceModel('m1', unpricedPricing))

    expect(wrapper.find('[data-testid="model-pricing-toggle"]').exists()).toBe(false)
  })

  it('点击触发条展开收起面板，右下角箭头同步切换方向', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', tokenPricing))
    const toggle = wrapper.get('[data-testid="model-pricing-toggle"]')
    const drawer = wrapper.find('.grid')

    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(toggle.get('.icon-stub[data-icon="chevronDown"]').exists()).toBe(true)
    expect(drawer.classes()).toContain('grid-rows-[0fr]')

    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(toggle.get('.icon-stub[data-icon="chevronUp"]').exists()).toBe(true)
    expect(wrapper.find('.grid').classes()).toContain('grid-rows-[1fr]')

    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(wrapper.get('[data-testid="model-pricing-toggle"]').get('.icon-stub[data-icon="chevronDown"]').exists()).toBe(true)
  })

  it('完整定价单列展示，窄屏允许标签和价格换行', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', tokenPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const rows = wrapper.get('[data-testid="pricing-rows"]')
    expect(wrapper.find('.md\\:grid-cols-2').exists()).toBe(false)
    expect(rows.findAll('.whitespace-nowrap')).toHaveLength(0)
    expect(rows.findAll('span').every((span) => span.classes().includes('min-w-0'))).toBe(true)
  })

  it('标准价格行展示全部计费项', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', tokenPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const labels = wrapper.get('[data-testid="pricing-rows"]').findAll('span:first-child').map((el) => el.text())
    expect(labels).toEqual(['marketplace.input', 'marketplace.output'])
  })

  it('展示独立的 1h 缓存写入价格', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', {
      ...tokenPricing,
      cache_write_price_per_token: 0.000003,
      cache_write_1h_price_per_token: 0.000006,
    }))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const labels = wrapper.get('[data-testid="pricing-rows"]').findAll('span:first-child').map((el) => el.text())
    expect(labels).toContain('marketplace.cacheWrite1h')
    expect(wrapper.text()).toContain('6.00')
  })

  it('存在 fast mode 计价时展示切换，切换后只显示 fast 价格行', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', fastPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    expect(wrapper.text()).toContain('marketplace.input')

    const fastSwitch = wrapper.get('[data-testid="pricing-fast-switch"]')
    await fastSwitch.findAll('button')[1].trigger('click')

    const labels = wrapper.get('[data-testid="pricing-rows"]').findAll('span:first-child').map((el) => el.text())
    expect(labels).toEqual(['marketplace.fastInput', 'marketplace.fastOutput'])
  })

  it('上下文区间模型展示区间切换，定价行随选中区间联动', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', intervalPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const intervalSwitch = wrapper.get('[data-testid="pricing-interval-switch"]')
    const buttons = intervalSwitch.findAll('button')
    expect(buttons.map((button) => button.text())).toEqual(['0-32k', '32k+'])

    // 第一个区间输入价 0.000001/Token，即 1.00 点/百万Token。
    expect(wrapper.text()).toContain('1.00')

    await buttons[1].trigger('click')
    // 第二个区间输入价 0.000003/Token，即 3.00 点/百万Token。
    expect(wrapper.text()).toContain('3.00')
    expect(wrapper.text()).not.toContain('1.00')
  })

  it('保留省略零价格字段的免费默认区间，并允许切换到收费区间', async () => {
    const wrapper = mountPanel(marketplaceModel('free-range', {
      pricing_mode: 'token', price_status: 'priced', context_intervals: [
        { min_tokens: 0, max_tokens: 100 },
        { min_tokens: 100, max_tokens: null, input_price_per_token: 0.000002 },
      ],
    }))
    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    const buttons = wrapper.get('[data-testid="pricing-interval-switch"]').findAll('button')
    expect(buttons).toHaveLength(2)
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('0.00')
    await buttons[1].trigger('click')
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('2.00')
    await buttons[0].trigger('click')
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('0.00')
  })

  it('图片模型展示分档价格且不显示 fast 切换', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', imagePricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    expect(wrapper.text()).toContain('1K')
    expect(wrapper.find('[data-testid="pricing-fast-switch"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pricing-interval-switch"]').exists()).toBe(false)
  })
})
