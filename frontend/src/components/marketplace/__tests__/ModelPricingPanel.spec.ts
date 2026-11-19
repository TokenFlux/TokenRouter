import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import ModelPricingPanel from '../ModelPricingPanel.vue'
import type { MarketplaceModel, MarketplaceModelPricing } from '@/api/marketplace.types'

// ModelPricingPanel.spec.ts checks displayed public quotes through the mounted
// pricing controls. Fixtures isolate token intervals, speed and effort tiers,
// timezone periods, image dimensions, and video prices, while deterministic
// translation output exposes labels, units, and interpolation arguments.

vi.mock('@/composables/useBalanceDisplay', () => ({
  // useBalanceDisplay supplies the fixed unit name used by price assertions.
  useBalanceDisplay: () => ({
    balanceUnitName: { value: '点' },
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    // useI18n supplies deterministic translation output without changing the actual module's other exports.
    useI18n: () => ({
      // t exposes translation keys and optional interpolation parameters for price assertions.
      // Calls with parameters append their JSON representation after a colon; calls
      // without parameters return the raw key for label matching.
      t: (key: string, params?: Record<string, unknown>) =>
        params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

// IconStub exposes the icon name through a DOM attribute so disclosure tests
// can inspect arrow selection and classes without the artwork implementation.
const IconStub = {
  name: 'Icon',
  props: {
    name: { type: String, default: '' }, // Requested icon name.
    size: { type: String, default: 'md' }, // Accepted size option.
    strokeWidth: { type: Number, default: 1.5 }, // Accepted stroke width.
  },
  template: '<span class="icon-stub" :data-icon="name" />',
}

// marketplaceModel pairs the requested identifier with its public pricing fixture.
function marketplaceModel(id: string, pricing: MarketplaceModelPricing): MarketplaceModel {
  return {
    id,
    display_name: id,
    pricing,
  }
}

// mountPanel mounts the real pricing disclosure with a deterministic icon substitute.
// Callers interact with the returned wrapper while translation and unit mocks
// keep formatted price assertions independent of application stores.
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

// tokenPricing provides one-unit input and two-unit output per million tokens,
// making the effects of optional pricing multipliers visible in row assertions.
const tokenPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  input_price_per_token: 0.000001,
  output_price_per_token: 0.000002,
}

// fastPricing extends the standard quote with distinct fast-mode amounts so
// tests can detect a selector that leaves ordinary price rows visible.
const fastPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  input_price_per_token: 0.000001,
  output_price_per_token: 0.000002,
  fast_input_price_per_token: 0.000005,
  fast_output_price_per_token: 0.000006,
}

// intervalPricing supplies a bounded context range and an open-ended successor
// with distinct prices, exposing both range labels and selected-row replacement.
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

// imagePricing provides only the smallest image tier, allowing tests to inspect
// image labels while confirming token-specific selectors remain absent from the panel.
const imagePricing: MarketplaceModelPricing = {
  pricing_mode: 'image',
  price_status: 'priced',
  image_price_1k: 0.5,
}

// unpricedPricing explicitly lacks a priced billing mode, distinguishing a missing
// quote from the separately tested zero-valued image and video prices.
const unpricedPricing: MarketplaceModelPricing = {
  pricing_mode: 'unknown',
  price_status: 'unpriced',
}

describe('ModelPricingPanel', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  // An explicitly unpriced model reaches the pricing panel without usable quote data.
  // The component must omit its disclosure control entirely for this case.
  it('无定价模型不渲染展开入口', () => {
    const wrapper = mountPanel(marketplaceModel('m1', unpricedPricing))

    expect(wrapper.find('[data-testid="model-pricing-toggle"]').exists()).toBe(false)
  })

  // A priced panel is toggled open and then closed through its actual control.
  // Expanded semantics, content visibility, and the arrow classes follow the disclosure state.
  it('点击触发条展开收起面板，右下角箭头同步切换方向', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', tokenPricing))
    const toggle = wrapper.get('[data-testid="model-pricing-toggle"]')
    const drawer = wrapper.get('.motion-collapse')

    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(toggle.get('.icon-stub[data-icon="chevronDown"]').exists()).toBe(true)
    expect((drawer.element as HTMLElement).style.display).toBe('none')

    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(toggle.get('.icon-stub[data-icon="chevronDown"]').classes()).toContain('rotate-180')
    expect((drawer.element as HTMLElement).style.display).not.toBe('none')

    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(
      wrapper
        .get('[data-testid="model-pricing-toggle"]')
        .get('.icon-stub[data-icon="chevronDown"]')
        .exists(),
    ).toBe(true)
  })

  // A token quote exposes its row layout after expansion in the DOM fixture.
  // Classes retain single-column, shrinkable text without imposing nowrap on the price rows.
  it('完整定价单列展示，窄屏允许标签和价格换行', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', tokenPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const rows = wrapper.get('[data-testid="pricing-rows"]')
    expect(wrapper.find('.md\\:grid-cols-2').exists()).toBe(false)
    expect(rows.findAll('.whitespace-nowrap')).toHaveLength(0)
    expect(rows.findAll('span').every((span) => span.classes().includes('min-w-0'))).toBe(true)
  })

  // The standard token fixture contains only positive input and output prices.
  // Its expanded row labels must show those two categories in their expected order.
  it('标准价格行展示全部计费项', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', tokenPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const labels = wrapper
      .get('[data-testid="pricing-rows"]')
      .findAll('span:first-child')
      .map((el) => el.text())
    expect(labels).toEqual(['marketplace.input', 'marketplace.output'])
  })

  // A token quote supplies different ordinary and one-hour cache-write prices.
  // The expanded panel must retain the independent one-hour label and its converted amount.
  it('展示独立的 1h 缓存写入价格', async () => {
    const wrapper = mountPanel(
      marketplaceModel('m1', {
        ...tokenPricing,
        cache_write_price_per_token: 0.000003,
        cache_write_1h_price_per_token: 0.000006,
      }),
    )

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const labels = wrapper
      .get('[data-testid="pricing-rows"]')
      .findAll('span:first-child')
      .map((el) => el.text())
    expect(labels).toContain('marketplace.cacheWrite1h')
    expect(wrapper.text()).toContain('6.00')
  })

  // A token quote sets both 5m and 1h cache-write prices to zero as explicit free tiers.
  // Expanding the panel renders both free cache-write rows instead of omitting zero-valued lifetime tiers.
  it('展示明确免费的 5m 与 1h 缓存写入价格', async () => {
    const wrapper = mountPanel(
      marketplaceModel('m1', {
        ...tokenPricing,
        cache_write_5m_price_per_token: 0,
        cache_write_1h_price_per_token: 0,
      }),
    )

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const labels = wrapper
      .get('[data-testid="pricing-rows"]')
      .findAll('span:first-child')
      .map((el) => el.text())
    expect(labels).toContain('marketplace.cacheWriteLifetime:{"ttl":"5m"}')
    expect(labels).toContain('marketplace.cacheWrite1h')
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('0.0000 点')
  })

  // A quote advertises distinct standard and fast token prices before selection changes.
  // Activating fast mode must replace the row labels with only fast categories.
  it('存在 fast mode 计价时展示切换，切换后只显示 fast 价格行', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', fastPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    expect(wrapper.text()).toContain('marketplace.input')

    const fastSwitch = wrapper.get('[data-testid="pricing-fast-switch"]')
    await fastSwitch.findAll('button')[1].trigger('click')

    const labels = wrapper
      .get('[data-testid="pricing-rows"]')
      .findAll('span:first-child')
      .map((el) => el.text())
    expect(labels).toEqual(['marketplace.fastInput', 'marketplace.fastOutput'])
  })

  // Two context intervals provide distinct input prices and an open-ended second range.
  // Selecting the second interval updates the displayed amount and removes the first amount.
  it('上下文区间模型展示区间切换，定价行随选中区间联动', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', intervalPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const intervalSwitch = wrapper.get('[data-testid="pricing-interval-switch"]')
    const buttons = intervalSwitch.findAll('button')
    expect(buttons.map((button) => button.text())).toEqual(['0-32k', '32k+'])

    // The first range converts its per-token amount to one unit per million.
    expect(wrapper.text()).toContain('1.00')

    await buttons[1].trigger('click')
    // The second range replaces that input amount with three units per million.
    expect(wrapper.text()).toContain('3.00')
    expect(wrapper.text()).not.toContain('1.00')
  })

  // A priced default interval omits zero-valued fields beside a positive successor.
  // Both choices remain selectable, and switching back restores the explicit free display.
  it('保留省略零价格字段的免费默认区间，并允许切换到收费区间', async () => {
    const wrapper = mountPanel(
      marketplaceModel('free-range', {
        pricing_mode: 'token',
        price_status: 'priced',
        context_intervals: [
          { min_tokens: 0, max_tokens: 100 },
          { min_tokens: 100, max_tokens: null, input_price_per_token: 0.000002 },
        ],
      }),
    )
    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    const buttons = wrapper.get('[data-testid="pricing-interval-switch"]').findAll('button')
    expect(buttons).toHaveLength(2)
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('0.00')
    await buttons[1].trigger('click')
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('2.00')
    await buttons[0].trigger('click')
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('0.00')
  })

  // The quote provides standard and fast prices with a maximum-effort multiplier.
  // Enabling that tier scales both standard categories and continues scaling after selecting fast mode.
  it('Max 推理档位切换后全部 token 价格乘以倍率，Fast 价格同样适用', async () => {
    const wrapper = mountPanel(
      marketplaceModel('m1', { ...fastPricing, max_reasoning_effort_multiplier: 1.5 }),
    )

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    const effortButtons = wrapper.get('[data-testid="pricing-effort-switch"]').findAll('button')
    expect(effortButtons.map((button) => button.text())).toEqual([
      'marketplace.pricingDefaultEffort',
      'marketplace.pricingMaxEffort',
    ])
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('1.00')

    await effortButtons[1].trigger('click')
    // The selected effort factor scales both standard input and output prices.
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('1.50')
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('3.00')

    await wrapper.get('[data-testid="pricing-fast-switch"]').findAll('button')[1].trigger('click')
    // The same effort factor remains active after selecting fast input pricing.
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('7.50')
  })

  // Unsorted scheduled periods leave a gap containing the fixed local test time.
  // The panel orders the periods, starts at other hours, and scales prices after selection.
  it('分时切换列出其他时段和各时段，选中时段后价格乘以对应倍率', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    // Shanghai local time is 08:00, outside both configured pricing periods.
    vi.setSystemTime(new Date('2026-10-05T00:00:00Z'))
    const wrapper = mountPanel(
      marketplaceModel('m1', {
        ...tokenPricing,
        time_pricing: {
          timezone: 'Asia/Shanghai',
          weekdays_only: false,
          periods: [
            { start_time: '20:00:00', end_time: '00:00', multiplier: 0.5 },
            { start_time: '09:00', end_time: '12:00', multiplier: 2 },
          ],
        },
      }),
    )

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    const buttons = wrapper.get('[data-testid="pricing-time-switch"]').findAll('button')
    expect(buttons.map((button) => button.text())).toEqual([
      'marketplace.timePricingOtherHours',
      '09:00-12:00',
      '20:00-24:00',
    ])
    expect(buttons[0].classes()).toContain('segmented-item-active')
    expect(wrapper.get('[data-testid="pricing-time-note"]').text()).toBe(
      'marketplace.timePricingNoteEveryDay:{"timezone":"Asia/Shanghai"}',
    )

    await buttons[1].trigger('click')
    // Selecting the morning period doubles the displayed standard input price.
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('2.00')
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).not.toContain('1.00')
  })

  // The fixed clock falls inside a configured evening period in Shanghai time.
  // Opening the panel selects that period and displays its discounted token amount.
  it('默认选中规则时区下当前所在的时段', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    // Shanghai local time is 22:00, inside the period ending at midnight.
    vi.setSystemTime(new Date('2026-10-05T14:00:00Z'))
    const wrapper = mountPanel(
      marketplaceModel('m1', {
        ...tokenPricing,
        time_pricing: {
          timezone: 'Asia/Shanghai',
          weekdays_only: false,
          periods: [{ start_time: '20:00', end_time: '00:00', multiplier: 0.5 }],
        },
      }),
    )

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const buttons = wrapper.get('[data-testid="pricing-time-switch"]').findAll('button')
    expect(buttons[1].classes()).toContain('segmented-item-active')
    // The evening multiplier halves the standard input amount before display.
    expect(wrapper.get('[data-testid="pricing-rows"]').text()).toContain('0.5000')
  })

  // Two daily periods cover the complete day without a weekdays-only restriction.
  // The selector must contain only those periods and omit an unnecessary other-hours choice.
  it('分时规则覆盖全天且每天生效时省略其他时段', async () => {
    const wrapper = mountPanel(
      marketplaceModel('m1', {
        ...tokenPricing,
        time_pricing: {
          timezone: 'UTC',
          weekdays_only: false,
          periods: [
            { start_time: '00:00', end_time: '12:00', multiplier: 0.8 },
            { start_time: '12:00', end_time: '00:00', multiplier: 1.2 },
          ],
        },
      }),
    )

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const labels = wrapper
      .get('[data-testid="pricing-time-switch"]')
      .findAll('button')
      .map((button) => button.text())
    expect(labels).toEqual(['00:00-12:00', '12:00-24:00'])
  })

  // A weekdays-only schedule covers the full day while the fixed clock is Sunday.
  // The panel starts at other hours and explains the weekday restriction below the quote.
  it('仅工作日生效时周末默认停在其他时段', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    // This fixed UTC date falls on Sunday for the configured timezone.
    vi.setSystemTime(new Date('2026-10-04T12:00:00Z'))
    const wrapper = mountPanel(
      marketplaceModel('m1', {
        ...tokenPricing,
        time_pricing: {
          timezone: 'UTC',
          weekdays_only: true,
          periods: [{ start_time: '00:00', end_time: '00:00', multiplier: 0.8 }],
        },
      }),
    )

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    const buttons = wrapper.get('[data-testid="pricing-time-switch"]').findAll('button')
    expect(buttons.map((button) => button.text())).toEqual([
      'marketplace.timePricingOtherHours',
      '00:00-24:00',
    ])
    expect(buttons[0].classes()).toContain('segmented-item-active')
    expect(wrapper.get('[data-testid="pricing-time-note"]').text()).toContain(
      'marketplace.timePricingNoteWeekdays',
    )
  })

  // A basic token quote has neither effort multipliers nor time-pricing configuration.
  // Expanding it must leave both optional selectors and the schedule note absent.
  it('没有倍率的模型不展示档位和分时切换', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', tokenPricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    expect(wrapper.find('[data-testid="pricing-effort-switch"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pricing-time-switch"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pricing-time-note"]').exists()).toBe(false)
  })

  // An image quote exposes its declared smallest resolution after the panel expands.
  // The image price appears while token speed and context-range selectors remain absent.
  it('图片模型展示分档价格且不显示 fast 切换', async () => {
    const wrapper = mountPanel(marketplaceModel('m1', imagePricing))

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')

    expect(wrapper.text()).toContain('1K')
    expect(wrapper.find('[data-testid="pricing-fast-switch"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="pricing-interval-switch"]').exists()).toBe(false)
  })
})

// A priced image quote explicitly sets one resolution to zero and omits the others.
// The declared free resolution remains in the content without unpriced size labels.
it('显示明确免费的图片尺寸，省略未定价尺寸', () => {
  const wrapper = mountPanel(
    marketplaceModel('free-image-size', {
      pricing_mode: 'image',
      price_status: 'priced',
      image_price_1k: 0,
    }),
  )
  expect(wrapper.text()).toContain('1K')
  expect(wrapper.text()).not.toContain('2K')
  expect(wrapper.text()).not.toContain('4K')
  expect(wrapper.text()).not.toContain('marketplace.unpriced')
})

// Video rows retain their declared units, including free custom dimensions.
// Token mode controls must remain absent from this per-second price view.
it('shows custom video resolutions and the explicit default per second', async () => {
  const wrapper = mountPanel(
    marketplaceModel('video', {
      pricing_mode: 'video',
      price_status: 'priced',
      video_prices: { '768p': 0.12, '2k': 0 },
      video_price_per_second: 0.3,
    }),
  )
  await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
  const rows = wrapper.get('[data-testid="pricing-rows"]')
  expect(rows.text()).toContain('768p')
  expect(rows.text()).toContain('2k')
  expect(rows.text()).toContain('0.0000 点 marketplace.perSecond')
  expect(rows.text()).toContain('marketplace.defaultVideoPrice')
  expect(wrapper.find('[data-testid="pricing-fast-switch"]').exists()).toBe(false)
  wrapper.unmount()
})

// An explicit free default alone is enough to display a video quote.
// The expanded panel must not substitute zero-token rows for this valid quote.
it('shows a free default-only video quote', async () => {
  const wrapper = mountPanel(
    marketplaceModel('video', {
      pricing_mode: 'video',
      price_status: 'priced',
      video_price_per_second: 0,
    }),
  )
  await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
  expect(wrapper.text()).toContain('marketplace.defaultVideoPrice')
  expect(wrapper.text()).toContain('marketplace.perSecond')
  expect(wrapper.text()).not.toContain('marketplace.input')
  wrapper.unmount()
})
