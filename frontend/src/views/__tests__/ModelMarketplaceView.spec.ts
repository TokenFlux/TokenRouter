import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount, DOMWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelMarketplaceView from '../ModelMarketplaceView.vue'
import type { MarketplaceGroup, MarketplaceModelPricing } from '@/types'

const getMarketplaceModels = vi.hoisted(() => vi.fn())
const checkAuth = vi.hoisted(() => vi.fn())
const fetchPublicSettings = vi.hoisted(() => vi.fn())
const copyToClipboard = vi.hoisted(() => vi.fn())

vi.mock('@/api/marketplace', () => ({
  getMarketplaceModels,
}))

vi.mock('@/stores', () => ({
  useAuthStore: () => ({
    isAuthenticated: true,
    isAdmin: false,
    checkAuth,
  }),
  useAppStore: () => ({
    siteName: 'TokenRouter',
    siteLogo: '',
    docUrl: '',
    cachedPublicSettings: null,
    publicSettingsLoaded: true,
    fetchPublicSettings,
    showSuccess: vi.fn(),
    showError: vi.fn(),
  }),
}))

vi.mock('@/composables/useTheme', () => ({
  initTheme: vi.fn(),
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitName: { value: '点' },
    balanceUnitSymbol: { value: '点' },
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, string | number>) => {
        if (key === 'marketplace.recentRequestSummary') {
          return `${params?.success}/${params?.total} succeeded`
        }
        return key
      },
    }),
  }
})

const SelectStub = defineComponent({
  name: 'TokenSelectStub',
  props: {
    modelValue: {
      type: [String, Number, Boolean, null],
      default: null,
    },
    options: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['update:modelValue', 'change'],
  template: `
    <div class="select-stub">
      <button
        v-for="option in options"
        :key="String(option.value)"
        type="button"
        :data-testid="'select-option-' + String(option.value)"
        @click="$emit('update:modelValue', option.value); $emit('change', option.value, option)"
      >
        {{ option.label }}
      </button>
    </div>
  `,
})

const SearchInputStub = defineComponent({
  name: 'SearchInput',
  props: {
    modelValue: {
      type: String,
      default: '',
    },
  },
  emits: ['update:modelValue'],
  template: `
    <input
      data-testid="marketplace-search"
      :value="modelValue"
      @input="$emit('update:modelValue', $event.target.value)"
    />
  `,
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: {
      type: Boolean,
      default: false,
    },
    title: {
      type: String,
      default: '',
    },
  },
  template: `
    <div v-if="show" data-testid="pricing-dialog">
      <h2>{{ title }}</h2>
      <slot />
    </div>
  `,
})

const tokenPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  input_price_per_token: 0.000001,
  output_price_per_token: 0.000002,
}

const unpricedPricing: MarketplaceModelPricing = {
  pricing_mode: 'unknown',
  price_status: 'unpriced',
}

function marketplaceFixture(): MarketplaceGroup[] {
  return [
    marketplaceGroup(1, 'Plus', 'OpenAI', false, [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing, [
        { success: true, created_at: '2026-01-01T00:00:00Z' },
        { success: false, created_at: '2026-01-01T00:01:00Z' },
      ]),
      marketplaceModel('legacy-unpriced', 'Legacy Unpriced', unpricedPricing),
    ]),
    marketplaceGroup(2, 'Pro', 'OpenAI', false, [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing, [
        { success: true, created_at: '2026-01-01T00:02:00Z' },
      ]),
    ]),
    marketplaceGroup(3, 'Plus Data Sharing', 'OpenAI', true, [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
    ]),
    marketplaceGroup(4, 'Claude', 'Anthropic', false, [
      marketplaceModel('claude-sonnet-4.5', 'Claude Sonnet 4.5', tokenPricing),
    ]),
  ]
}

function marketplaceGroup(
  id: number,
  name: string,
  displayBrand: string,
  dataSharingEnabled: boolean,
  models: MarketplaceGroup['models']
): MarketplaceGroup {
  return {
    id,
    name,
    description: `${name} group`,
    platform: displayBrand === 'Anthropic' ? 'anthropic' : 'openai',
    display_brand: displayBrand,
    sort_order: id,
    rate_multiplier: id,
    official_price_ratio: id / 10,
    official_price_rmb_equivalent: id,
    data_sharing_enabled: dataSharingEnabled,
    capacity: {
      concurrency_used: id,
      concurrency_max: 10,
      sessions_used: id,
      sessions_max: 20,
      rpm_used: id,
      rpm_max: 60,
    },
    model_count: models.length,
    models,
  }
}

function marketplaceModel(
  id: string,
  displayName: string,
  pricing: MarketplaceModelPricing,
  recentRequests: MarketplaceGroup['models'][number]['recent_requests'] = []
): MarketplaceGroup['models'][number] {
  return {
    id,
    display_name: displayName,
    pricing,
    recent_requests: recentRequests,
  }
}

async function mountMarketplace() {
  const wrapper = mount(ModelMarketplaceView, {
    attachTo: document.body,
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        Icon: { template: '<span />' },
        LoadingSpinner: { template: '<span />' },
        LocaleSwitcher: { template: '<span />' },
        ProviderIcon: { template: '<span />' },
        GroupCapacityBadge: { template: '<span />' },
        Transition: { template: '<div><slot /></div>' },
        BaseDialog: BaseDialogStub,
        SearchInput: SearchInputStub,
        Select: SelectStub,
      },
    },
  })
  await flushPromises()
  await nextTick()
  return wrapper
}

function modelCards(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('[data-testid="marketplace-model-card"]')
}

function brandSections(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('[data-testid="marketplace-brand-section"]')
}

function teleportedDetailOverlay(): HTMLElement {
  const overlay = document.body.querySelector<HTMLElement>('[data-testid="marketplace-detail-overlay"]')
  expect(overlay).not.toBeNull()
  return overlay!
}

function availabilityCards(): DOMWrapper<HTMLElement>[] {
  return Array.from(document.body.querySelectorAll<HTMLElement>('[data-testid="marketplace-availability-card"]'))
    .map((element) => new DOMWrapper(element))
}

describe('ModelMarketplaceView', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
    getMarketplaceModels.mockResolvedValue(marketplaceFixture())
    copyToClipboard.mockResolvedValue(true)
    checkAuth.mockClear()
    fetchPublicSettings.mockClear()
    copyToClipboard.mockClear()
  })

  it('按品牌分区展示，并在同一品牌下按模型 ID 聚合可用分组', async () => {
    const wrapper = await mountMarketplace()

    expect(brandSections(wrapper)).toHaveLength(2)
    expect(brandSections(wrapper)[0].text()).toContain('OpenAI')
    expect(brandSections(wrapper)[0].text()).toContain(`3 marketplace.groupsStat`)
    expect(brandSections(wrapper)[0].text()).toContain(`2 marketplace.modelsStat`)
    expect(modelCards(wrapper)).toHaveLength(3)

    const gptCards = modelCards(wrapper).filter((card) => card.text().includes('GPT 5.5'))
    expect(gptCards).toHaveLength(1)
    expect(gptCards[0].text()).toContain('OpenAI')
    expect(gptCards[0].text()).toContain(`3 marketplace.groupsStat`)
    expect(gptCards[0].text()).toContain('x1')
    expect(gptCards[0].text()).toContain('2/3 succeeded')
    expect(gptCards[0].find('.marketplace-status-dot.is-warn').exists()).toBe(true)
    expect(gptCards[0].findAll('.card-recent-health-dot.is-success')).toHaveLength(2)
    expect(gptCards[0].findAll('.card-recent-health-dot.is-failed')).toHaveLength(1)

    const claudeCard = modelCards(wrapper).find((card) => card.text().includes('Claude Sonnet 4.5'))!
    expect(claudeCard.text()).toContain('marketplace.noRecentRequests')
    expect(claudeCard.find('.marketplace-status-dot.is-empty').exists()).toBe(true)
  })

  it('按品牌、分组、搜索和计费类型过滤品牌分区卡片', async () => {
    const wrapper = await mountMarketplace()

    await wrapper.get('[data-testid="select-option-Anthropic"]').trigger('click')
    await nextTick()
    expect(brandSections(wrapper)).toHaveLength(1)
    expect(brandSections(wrapper)[0].text()).toContain('Anthropic')
    expect(modelCards(wrapper).map((card) => card.text()).join('\n')).not.toContain('GPT 5.5')

    await wrapper.get('[data-testid="select-option-all"]').trigger('click')
    await wrapper.get('[data-testid="select-option-2"]').trigger('click')
    await nextTick()
    expect(modelCards(wrapper)).toHaveLength(1)
    expect(modelCards(wrapper)[0].text()).toContain('GPT 5.5')
    expect(modelCards(wrapper)[0].text()).toContain(`1 marketplace.groupsStat`)

    await wrapper.findAll('[data-testid="select-option-all"]').at(-1)!.trigger('click')
    await wrapper.get('[data-testid="marketplace-search"]').setValue('Plus Data Sharing')
    await nextTick()
    expect(modelCards(wrapper)).toHaveLength(1)
    expect(brandSections(wrapper)[0].text()).toContain('GPT 5.5')
    expect(brandSections(wrapper)[0].text()).not.toContain('Claude Sonnet 4.5')

    await wrapper.get('[data-testid="marketplace-search"]').setValue('')
    await wrapper.get('[data-testid="select-option-token"]').trigger('click')
    await nextTick()
    expect(modelCards(wrapper).map((card) => card.text()).join('\n')).toContain('GPT 5.5')
    expect(modelCards(wrapper).map((card) => card.text()).join('\n')).not.toContain('Legacy Unpriced')
  })

  it('点击模型卡片打开覆盖式详情，并可在分组间切换右侧定价面板', async () => {
    const wrapper = await mountMarketplace()
    const gptCard = modelCards(wrapper).find((card) => card.text().includes('GPT 5.5'))!

    await gptCard.trigger('click')
    await nextTick()

    const overlay = teleportedDetailOverlay()
    expect(overlay.classList.contains('active')).toBe(true)
    expect(overlay.textContent).toContain('GPT 5.5')
    expect(overlay.textContent).toContain('gpt-5.5')
    expect(availabilityCards()).toHaveLength(3)
    expect(availabilityCards()[0].text()).toContain('Plus')
    expect(overlay.textContent).toContain('Plus marketplace.groupPricingDetail')
    expect(document.body.querySelectorAll('.uptime-bars-wrapper')).toHaveLength(3)
    expect(document.body.querySelectorAll('.marketplace-request-segment')).not.toHaveLength(0)

    await availabilityCards()[1].trigger('click')
    await nextTick()
    expect(overlay.textContent).toContain('Pro marketplace.groupPricingDetail')
  })

  it('详情 modal 的请求状态条会消费超过卡片 3 点摘要的历史窗口', async () => {
    const recentRequests = Array.from({ length: 8 }, (_, index) => ({
      success: index % 2 === 0,
      created_at: `2026-01-01T00:${String(index).padStart(2, '0')}:00Z`,
    }))
    getMarketplaceModels.mockResolvedValue([
      marketplaceGroup(1, 'Plus', 'OpenAI', false, [
        marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing, recentRequests),
      ]),
    ])
    const wrapper = await mountMarketplace()
    const gptCard = modelCards(wrapper).find((card) => card.text().includes('GPT 5.5'))!

    await gptCard.trigger('click')
    await nextTick()

    const firstAvailability = availabilityCards()[0]
    expect(firstAvailability.text()).toContain('4/8 succeeded')
    expect(firstAvailability.findAll('.marketplace-request-segment')).toHaveLength(8)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-success')).toHaveLength(4)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-failed')).toHaveLength(4)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-empty')).toHaveLength(0)
    expect(gptCard.findAll('.card-recent-health-dot')).toHaveLength(3)
  })

  it('详情 modal 有少量真实请求时不会用灰色空槽补满状态条', async () => {
    const recentRequests = Array.from({ length: 3 }, (_, index) => ({
      success: true,
      created_at: `2026-01-01T00:${String(index).padStart(2, '0')}:00Z`,
    }))
    getMarketplaceModels.mockResolvedValue([
      marketplaceGroup(1, 'Plus', 'OpenAI', false, [
        marketplaceModel('gpt-5.2', 'GPT 5.2', tokenPricing, recentRequests),
      ]),
    ])
    const wrapper = await mountMarketplace()
    const gptCard = modelCards(wrapper).find((card) => card.text().includes('GPT 5.2'))!

    await gptCard.trigger('click')
    await nextTick()

    const firstAvailability = availabilityCards()[0]
    expect(firstAvailability.text()).toContain('3/3 succeeded')
    expect(firstAvailability.findAll('.marketplace-request-segment')).toHaveLength(3)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-success')).toHaveLength(3)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-failed')).toHaveLength(0)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-empty')).toHaveLength(0)
  })

  it('详情 modal 摘要数字与实际可见请求状态段使用同一个窗口', async () => {
    const recentRequests = Array.from({ length: 30 }, (_, index) => ({
      success: index >= 6,
      created_at: `2026-01-01T00:${String(index).padStart(2, '0')}:00Z`,
    }))
    getMarketplaceModels.mockResolvedValue([
      marketplaceGroup(1, 'Plus', 'OpenAI', false, [
        marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing, recentRequests),
      ]),
    ])
    const wrapper = await mountMarketplace()
    const gptCard = modelCards(wrapper).find((card) => card.text().includes('GPT 5.5'))!

    await gptCard.trigger('click')
    await nextTick()

    const firstAvailability = availabilityCards()[0]
    expect(firstAvailability.findAll('.marketplace-request-segment')).toHaveLength(24)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-success')).toHaveLength(24)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-failed')).toHaveLength(0)
    expect(firstAvailability.findAll('.marketplace-request-segment.is-empty')).toHaveLength(0)
    expect(firstAvailability.text()).toContain('24/24 succeeded')
    expect(firstAvailability.text()).not.toContain('24/30 succeeded')
  })

  it('详情面板支持复制模型 ID，并从当前分组打开定价弹窗', async () => {
    const wrapper = await mountMarketplace()
    const gptCard = modelCards(wrapper).find((card) => card.text().includes('GPT 5.5'))!

    await gptCard.trigger('click')
    await nextTick()

    const copyButton = document.body.querySelector<HTMLButtonElement>('.header-model-id-copy-btn')
    expect(copyButton).not.toBeNull()
    copyButton!.click()
    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.5', 'marketplace.modelIdCopied')

    const pricingButton = document.body.querySelector<HTMLButtonElement>('[data-testid="marketplace-view-pricing-button"]')
    expect(pricingButton).not.toBeNull()
    pricingButton!.click()
    await nextTick()

    const dialog = wrapper.get('[data-testid="pricing-dialog"]')
    expect(dialog.get('h2').text()).toBe('GPT 5.5 · marketplace.pricingDetail')
    expect(dialog.text()).toContain('gpt-5.5')
    expect(dialog.text()).toContain('Plus')
    expect(dialog.text()).toContain('marketplace.input')
  })
})
