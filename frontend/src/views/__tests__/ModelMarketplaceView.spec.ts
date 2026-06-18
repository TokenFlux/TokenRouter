import { defineComponent, nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ModelMarketplaceView from '../ModelMarketplaceView.vue'
import type { MarketplaceGroup, MarketplaceModelPricing } from '@/types'

const getMarketplaceModels = vi.hoisted(() => vi.fn())
const checkAuth = vi.hoisted(() => vi.fn())
const fetchPublicSettings = vi.hoisted(() => vi.fn())

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
  }),
}))

vi.mock('@/composables/useTheme', () => ({
  initTheme: vi.fn(),
}))

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
      t: (key: string, params?: Record<string, string>) => {
        if (key === 'marketplace.rateMultiplierValue') {
          return `marketplace.rateMultiplierValue ${params?.multiplier || ''}`
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
    marketplaceGroup(1, 'Plus', false, [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
      marketplaceModel('legacy-unpriced', 'Legacy Unpriced', unpricedPricing),
    ]),
    marketplaceGroup(2, 'Pro', false, [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
    ]),
    marketplaceGroup(3, 'Plus Data Sharing', true, [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
    ]),
    marketplaceGroup(4, 'Pro Data Sharing', true, [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
    ]),
  ]
}

function marketplaceGroup(id: number, name: string, dataSharingEnabled: boolean, models: MarketplaceGroup['models']): MarketplaceGroup {
  return {
    id,
    name,
    description: `${name} group`,
    platform: 'openai',
    display_brand: 'OpenAI',
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

function marketplaceModel(id: string, displayName: string, pricing: MarketplaceModelPricing): MarketplaceGroup['models'][number] {
  return {
    id,
    display_name: displayName,
    pricing,
  }
}

async function mountMarketplace() {
  const wrapper = mount(ModelMarketplaceView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        Icon: { template: '<span />' },
        LoadingSpinner: { template: '<span />' },
        LocaleSwitcher: { template: '<span />' },
        ProviderIcon: { template: '<span />' },
        GroupCapacityBadge: { template: '<span />' },
        BaseDialog: BaseDialogStub,
        SearchInput: SearchInputStub,
        Select: SelectStub,
      },
    },
  })
  await flushPromises()
  return wrapper
}

function modelCards(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('[data-testid="marketplace-model-card"]')
}

function groupEntries(wrapper: ReturnType<typeof mount>) {
  return wrapper.findAll('[data-testid="marketplace-model-group-entry"]')
}

describe('ModelMarketplaceView', () => {
  beforeEach(() => {
    localStorage.clear()
    getMarketplaceModels.mockResolvedValue(marketplaceFixture())
    checkAuth.mockClear()
    fetchPublicSettings.mockClear()
  })

  it('默认按模型-分组展示，并按模型 ID 去重', async () => {
    const wrapper = await mountMarketplace()

    const gptCards = modelCards(wrapper).filter((card) => card.text().includes('gpt-5.5'))

    expect(gptCards).toHaveLength(1)
    expect(gptCards[0].findAll('[data-testid="marketplace-model-group-entry"]')).toHaveLength(4)
    expect(gptCards[0].text()).toContain('Plus')
    expect(gptCards[0].text()).toContain('x1')
    expect(gptCards[0].text()).not.toContain('marketplace.rateMultiplierValue')
    expect(gptCards[0].text()).toContain('Pro')
    expect(gptCards[0].text()).toContain('Plus Data Sharing')
    expect(gptCards[0].text()).toContain('Pro Data Sharing')
  })

  it('可以切换到分组-模型模式并保存本地偏好', async () => {
    const wrapper = await mountMarketplace()

    await wrapper.get('[data-testid="select-option-group-model"]').trigger('click')
    await nextTick()

    expect(localStorage.getItem('tokenrouter:model-marketplace:view-mode')).toBe('group-model')
    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(4)
    expect(wrapper.findAll('[data-testid="marketplace-model-card"]')).toHaveLength(0)

    wrapper.unmount()

    const restored = await mountMarketplace()
    expect(restored.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(4)
  })

  it('模型-分组模式下按分组、搜索和计费类型裁剪分组条目', async () => {
    const wrapper = await mountMarketplace()

    await wrapper.get('[data-testid="select-option-2"]').trigger('click')
    await nextTick()
    expect(modelCards(wrapper).filter((card) => card.text().includes('gpt-5.5'))).toHaveLength(1)
    expect(groupEntries(wrapper)).toHaveLength(1)
    expect(groupEntries(wrapper)[0].text()).toContain('Pro')
    expect(modelCards(wrapper).map((card) => card.text()).join('\n')).not.toContain('Plus Data Sharing')

    await wrapper.findAll('[data-testid="select-option-all"]').at(-1)!.trigger('click')
    await wrapper.get('[data-testid="marketplace-search"]').setValue('Plus Data Sharing')
    await nextTick()
    expect(groupEntries(wrapper)).toHaveLength(1)
    const entryText = groupEntries(wrapper).map((entry) => entry.text()).join('\n')
    expect(entryText).toContain('Plus Data Sharing')
    expect(entryText).not.toContain('Pro Data Sharing')

    await wrapper.get('[data-testid="marketplace-search"]').setValue('')
    await wrapper.get('[data-testid="select-option-token"]').trigger('click')
    await nextTick()
    expect(modelCards(wrapper).filter((card) => card.text().includes('gpt-5.5'))).toHaveLength(1)
    expect(modelCards(wrapper).some((card) => card.text().includes('legacy-unpriced'))).toBe(false)
  })

  it('点击分组条目会打开对应分组定价弹窗', async () => {
    const wrapper = await mountMarketplace()
    const groupEntry = groupEntries(wrapper)[0]

    expect(groupEntry.exists()).toBe(true)
    await groupEntry.trigger('click')
    await nextTick()

    const dialog = wrapper.get('[data-testid="pricing-dialog"]')
    expect(dialog.get('h2').text()).toBe('Plus · marketplace.groupDetail')
    expect(dialog.text()).toContain('GPT 5.5')
    expect(dialog.text()).toContain('gpt-5.5')
    expect(dialog.text().match(/Plus/g)).toHaveLength(1)
  })

  it('分组-模型模式下定价弹窗不重复显示分组名称', async () => {
    const wrapper = await mountMarketplace()

    await wrapper.get('[data-testid="select-option-group-model"]').trigger('click')
    await nextTick()

    const pricingButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('marketplace.viewPricing'))

    expect(pricingButton?.exists()).toBe(true)
    await pricingButton!.trigger('click')
    await nextTick()

    const dialog = wrapper.get('[data-testid="pricing-dialog"]')
    expect(dialog.get('h2').text()).toBe('GPT 5.5 · marketplace.pricingDetail')
    expect(dialog.text()).toContain('GPT 5.5')
    expect(dialog.text()).toContain('gpt-5.5')
    expect(dialog.text()).not.toContain('Plus')
  })
})
