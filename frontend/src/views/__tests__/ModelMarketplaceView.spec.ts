import { defineComponent, nextTick, reactive } from 'vue'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ModelMarketplaceView from '../ModelMarketplaceView.vue'
import { modelIconData } from '@/utils/modelIconData'
import type { MarketplaceGroup, MarketplaceModelPricing } from '@/api/marketplace.types'

// ModelMarketplaceView.spec.ts checks the mounted public model catalog with
// deterministic group and price responses. Interactive substitutes preserve
// filter and clipboard events while real cards verify loading placeholders,
// modality metadata, protocol tooltips, brand artwork, and expandable price rows.

// Hoisted collaborators isolate public data loading, session initialization,
// settings access, and clipboard writes so page effects remain directly observable.
const getAvailableMarketplaceModels = vi.hoisted(() => vi.fn())
const getMarketplaceModels = vi.hoisted(() => vi.fn())
const checkAuth = vi.hoisted(() => vi.fn())
const fetchPublicSettings = vi.hoisted(() => vi.fn())
const copyToClipboard = vi.hoisted(() => vi.fn())

vi.mock('@/api/marketplace', () => ({
  getAvailableMarketplaceModels,
  getMarketplaceModels,
}))

// routeState is the current route; tests set its `q` query before mounting the page.
const routeState = reactive({ path: '/models', query: {} as Record<string, string> })

vi.mock('vue-router', async () => ({
  ...(await vi.importActual<typeof import('vue-router')>('vue-router')),
  useRoute: () => routeState,
}))

vi.mock('@/composables/useClipboard', () => ({
  // useClipboard exposes a stable copied flag and the observable copy replacement.
  useClipboard: () => ({ copied: { value: false }, copyToClipboard }),
}))

// Reactive identity changes let lifecycle tests observe synchronous private-data removal.
const authState = reactive({
  isAuthenticated: true,
  isAdmin: false,
  user: { id: 7 } as { id: number } | null,
  checkAuth,
})

enableAutoUnmount(afterEach)

vi.mock('@/stores', () => ({
  // useAuthStore shares the mutable fixture identity with mounted marketplace views.
  useAuthStore: () => authState,
  // useAppStore supplies loaded public branding without requiring a real settings store.
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
  // useTheme holds the dark theme constant while providing an inert toggle collaborator.
  useTheme: () => ({ isDark: { value: true }, toggleTheme: vi.fn() }),
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  // useBalanceDisplay fixes the unit name so price assertions remain deterministic.
  useBalanceDisplay: () => ({
    balanceUnitName: { value: '点' },
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    // useI18n provides observable translation arguments for group pricing labels.
    useI18n: () => ({
      // t exposes multiplier and discount arguments for the three pricing messages.
      // Omitted parameters produce empty interpolation text for those keys, while
      // every other key is returned unchanged for assertions.
      t: (key: string, params?: Record<string, string>) => {
        if (key === 'marketplace.rateMultiplierValue') {
          return `marketplace.rateMultiplierValue ${params?.multiplier || ''}`
        }
        if (key === 'marketplace.imageRateMultiplierValue') {
          return `marketplace.imageRateMultiplierValue ${params?.multiplier || ''}`
        }
        if (key === 'marketplace.maxDiscountOff') {
          return `marketplace.maxDiscountOff ${params?.percent || ''}`
        }

        return key
      },
    }),
  }
})

// SelectStub renders each option as an actionable button and emits both selected
// value events expected by the marketplace filters, retaining original option values.
const SelectStub = defineComponent({
  name: 'TokenSelectStub',
  props: {
    // Current selected option value.
    modelValue: {
      type: [String, Number, Boolean, null],
      default: null,
    },
    // Available option records with labels and values; omission produces no selectable buttons.
    options: {
      type: Array,
      default: () => [],
    },
  },
  // Selection publishes the model value, then the change event with its option record.
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

// SearchInputStub forwards native input text immediately through the model event
// so the mounted page updates its filters; debounce timing belongs to the real SearchInput.
const SearchInputStub = defineComponent({
  name: 'SearchInput',
  props: {
    // Current search field text.
    modelValue: {
      type: String,
      default: '',
    },
  },
  // Input publishes the browser field's current text to the page-owned search state.
  emits: ['update:modelValue'],
  template: `
    <input
      data-testid="marketplace-search"
      :value="modelValue"
      @input="$emit('update:modelValue', $event.target.value)"
    />
  `,
})

// tokenPricing includes ordinary and image-input token rates plus a distinct fast
// image rate, exposing metadata inference and the panel's speed-tier selection.
const tokenPricing: MarketplaceModelPricing = {
  pricing_mode: 'token',
  price_status: 'priced',
  input_price_per_token: 0.000001,
  image_input_price_per_token: 0.000003,
  output_price_per_token: 0.000002,
  fast_image_input_price_per_token: 0.000006,
}

// imagePricing supplies one priced image resolution for model-mode filtering and
// capability fixtures that intentionally override the legacy model-name inference.
const imagePricing: MarketplaceModelPricing = {
  pricing_mode: 'image',
  price_status: 'priced',
  image_price_1k: 0.5,
}

// unpricedPricing describes a legacy record without a usable quote so cards can
// exercise fallback display alongside models with explicit token pricing.
const unpricedPricing: MarketplaceModelPricing = {
  pricing_mode: 'unknown',
  price_status: 'unpriced',
}

// marketplaceFixture creates four groups with repeated aliases and one unpriced model.
function marketplaceFixture(): MarketplaceGroup[] {
  return [
    marketplaceGroup(1, 'Plus', [
      marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
      marketplaceModel('legacy-unpriced', 'Legacy Unpriced', unpricedPricing),
    ]),
    marketplaceGroup(2, 'Pro', [marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing)]),
    marketplaceGroup(3, 'Plus 2', [marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing)]),
    marketplaceGroup(4, 'Pro 2', [marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing)]),
  ]
}

// marketplaceGroup builds a deterministic public group around the supplied models.
// Its identifier also supplies order, multiplier, discount ratio, and usage counts,
// making each group's display values distinct without additional network fixtures.
function marketplaceGroup(
  id: number,
  name: string,
  models: MarketplaceGroup['models'],
): MarketplaceGroup {
  return {
    id,
    name,
    description: `${name} group`,
    display_brand: 'OpenAI',
    sort_order: id,
    rate_multiplier: id,

    official_price_ratio: id / 10,
    official_price_rmb_equivalent: id,
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

// marketplaceModel pairs an identifier and display name with supplied public pricing.
function marketplaceModel(
  id: string,
  displayName: string,
  pricing: MarketplaceModelPricing,
): MarketplaceGroup['models'][number] {
  return {
    id,
    display_name: displayName,
    pricing,
  }
}

// mountMarketplace mounts the real catalog page with inert layout collaborators.
// Filter substitutes retain user events, while model cards remain real components.
// The helper drains pending promises before returning the wrapper to its caller.
async function mountMarketplace() {
  const wrapper = mount(ModelMarketplaceView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot name="page-heading-actions" /><slot /></div>' },
        RouterLink: { template: '<a><slot /></a>' },
        Icon: { template: '<span />' },
        LoadingSpinner: { template: '<span />' },
        LocaleSwitcher: { template: '<span />' },
        GroupCapacityBadge: { template: '<span data-testid="group-capacity" />' },
        SearchInput: SearchInputStub,
        Select: SelectStub,
      },
    },
  })
  await flushPromises()
  return wrapper
}

// visibleTooltips selects teleported tooltips whose inline display is not none.
// The actual HelpTooltip uses v-show for visibility, so this helper follows that
// convention without claiming to measure browser geometry or overlapping elements.
function visibleTooltips() {
  return Array.from(document.body.querySelectorAll<HTMLElement>('[role="tooltip"]')).filter(
    (el) => el.style.display !== 'none',
  )
}

describe('ModelMarketplaceView', () => {
  beforeEach(() => {
    localStorage.clear()
    routeState.query = {}
    authState.isAuthenticated = true
    authState.user = { id: 7 }
    getAvailableMarketplaceModels.mockReset()
    getMarketplaceModels.mockReset()
    getAvailableMarketplaceModels.mockResolvedValue(marketplaceFixture())
    getMarketplaceModels.mockResolvedValue(marketplaceFixture())
    checkAuth.mockClear()
    fetchPublicSettings.mockClear()
    copyToClipboard.mockClear()
  })

  // The global search palette opens the page with q set to a model ID. The page applies it
  // as the search text, scrolls to that model's first card, and highlights every group's
  // copy of the model, while other models are filtered out.
  it('applies the q query and highlights the requested model', async () => {
    const scrollIntoView = vi.fn()
    Element.prototype.scrollIntoView = scrollIntoView
    routeState.query = { q: 'gpt-5.5' }
    const wrapper = await mountMarketplace()

    expect(
      (wrapper.get('[data-testid="marketplace-search"]').element as HTMLInputElement).value,
    ).toBe('gpt-5.5')
    const cards = wrapper.findAll('[data-model-id="gpt-5.5"]')
    expect(cards).toHaveLength(4)
    expect(cards.every((card) => card.classes('marketplace-card-flash'))).toBe(true)
    expect(scrollIntoView).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-model-id="legacy-unpriced"]').exists()).toBe(false)
  })

  // Guest browsing stays on the public endpoint even though personal browsing exists.
  // Authenticated visits use the protected endpoint without changing homepage API defaults.
  it('selects public and personal endpoints from authentication', async () => {
    const personal = await mountMarketplace()
    expect(getAvailableMarketplaceModels).toHaveBeenCalledOnce()
    expect(getMarketplaceModels).not.toHaveBeenCalled()
    personal.unmount()
    authState.isAuthenticated = false
    authState.user = null
    const guest = await mountMarketplace()
    expect(getMarketplaceModels).toHaveBeenCalledOnce()
    expect(guest.find('[data-testid="group-exclusive-tag"]').exists()).toBe(false)
  })

  // Private groups sort ahead of public groups with stable order and ID ties.
  // Their inline labels remain visible when the same catalog is searched or filtered.
  it('places exclusive cards first and retains their labels through filters', async () => {
    const fixture = marketplaceFixture()
    fixture[1] = { ...fixture[1], is_exclusive: true, sort_order: 9 }
    fixture[3] = { ...fixture[3], is_exclusive: true, sort_order: 9 }
    getAvailableMarketplaceModels.mockResolvedValue([
      fixture[3],
      fixture[2],
      fixture[1],
      fixture[0],
    ])
    const wrapper = await mountMarketplace()
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')
    expect(sections.map((section) => section.get('h2').text())).toEqual([
      'Pro',
      'Pro 2',
      'Plus',
      'Plus 2',
    ])
    expect(wrapper.findAll('[data-testid="group-exclusive-tag"]')).toHaveLength(2)
    await wrapper.get('[data-testid="marketplace-search"]').setValue('Pro 2')
    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="group-exclusive-tag"]').text()).toBe('marketplace.exclusive')
    await wrapper.get('[data-testid="marketplace-search"]').setValue('')
    await wrapper.get('[data-testid="select-option-2"]').trigger('click')
    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="group-exclusive-tag"]').exists()).toBe(true)
  })

  // Logout replaces an outstanding personal request with a public catalog request.
  // Late private success or failure cannot overwrite guest data or its loading state.
  it.each(['success', 'failure'])('rejects a late private %s after logout', async (completion) => {
    let resolveOld!: (groups: MarketplaceGroup[]) => void
    let rejectOld!: (error: Error) => void
    getAvailableMarketplaceModels.mockImplementationOnce(
      () =>
        new Promise((resolve, reject) => {
          resolveOld = resolve
          rejectOld = reject
        }),
    )
    const wrapper = await mountMarketplace()
    authState.isAuthenticated = false
    authState.user = null
    await flushPromises()
    const privateGroup = {
      ...marketplaceFixture()[0],
      name: 'Old private account',
      is_exclusive: true,
    }
    if (completion === 'success') resolveOld([privateGroup])
    else rejectOld(new Error('Old account failed'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('Old private account')
    expect(wrapper.text()).not.toContain('Old account failed')
    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(4)
    expect(wrapper.find('[data-testid="marketplace-skeleton"]').exists()).toBe(false)
  })

  // Switching accounts clears rendered private data before the new read completes.
  // A selected group absent from the new account resets so its catalog remains visible.
  it('clears prior account groups and resets an unavailable group filter', async () => {
    getAvailableMarketplaceModels.mockResolvedValueOnce([
      { ...marketplaceFixture()[0], is_exclusive: true },
    ])
    const wrapper = await mountMarketplace()
    await wrapper.get('[data-testid="select-option-1"]').trigger('click')
    let resolveNew!: (groups: MarketplaceGroup[]) => void
    getAvailableMarketplaceModels.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveNew = resolve
        }),
    )
    authState.user = { id: 8 }
    await nextTick()
    expect(wrapper.find('[data-testid="group-exclusive-tag"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="marketplace-skeleton"]').exists()).toBe(true)
    resolveNew([{ ...marketplaceFixture()[1], name: 'New account', is_exclusive: true }])
    await flushPromises()
    expect(wrapper.get('[data-testid="marketplace-group-section"] h2').text()).toBe('New account')
  })

  // Locale reloads can finish after a more recent account request has succeeded.
  // Request generations keep the newest catalog and ignore errors from older reads.
  it('rejects an older locale response after switching accounts', async () => {
    const wrapper = await mountMarketplace()
    let resolveOld!: (groups: MarketplaceGroup[]) => void
    getAvailableMarketplaceModels.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve
        }),
    )
    window.dispatchEvent(new Event('locale-changed'))
    await flushPromises()
    getAvailableMarketplaceModels.mockResolvedValueOnce([
      { ...marketplaceFixture()[1], name: 'Latest account' },
    ])
    authState.user = { id: 8 }
    await flushPromises()
    resolveOld([{ ...marketplaceFixture()[0], name: 'Previous locale', is_exclusive: true }])
    await flushPromises()
    expect(wrapper.text()).toContain('Latest account')
    expect(wrapper.text()).not.toContain('Previous locale')
  })

  // A manually resolved catalog request keeps the page in its loading phase.
  // Skeletons remain until resolution, then give way to all four group sections.
  it('取数期间保留模型卡片占位，不提前显示空状态', async () => {
    // The deferred response separates initial loading assertions from populated
    // rendering without introducing a real request or timing-dependent network delay.
    let resolveModels!: (value: MarketplaceGroup[]) => void
    getAvailableMarketplaceModels.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveModels = resolve
        }),
    )
    const wrapper = await mountMarketplace()
    expect(wrapper.find('[data-testid="marketplace-skeleton"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('marketplace.emptyTitle')

    resolveModels(marketplaceFixture())
    await flushPromises()
    expect(wrapper.find('[data-testid="marketplace-skeleton"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(4)
    wrapper.unmount()
  })

  // The default fixture supplies four independently identified public model groups.
  // Once loading settles, the page must render one group section for each response record.
  it('默认按分组-模型展示', async () => {
    const wrapper = await mountMarketplace()

    expect(wrapper.findAll('[data-testid="marketplace-group-section"]')).toHaveLength(4)
  })

  // A regular user's catalog group includes a reported availability aggregate.
  // Capacity controls stay absent and the availability region retains its desktop width class.
  it('用户侧不展示分组容量，并将可用率状态条靠右放置', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],
      availability: {
        window_days: 7,
        bucket_minutes: 1440,
        success_count: 6,
        total_count: 7,
        availability_rate: 6 / 7,
        days: [],
      },
    }
    getAvailableMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()

    expect(wrapper.findAll('[data-testid="group-capacity"]')).toHaveLength(0)
    expect(wrapper.get('[data-testid="marketplace-group-availability"]').classes()).toContain(
      'xl:w-[560px]',
    )
  })

  // Groups provide discounted, undiscounted, and missing official-price ratios.
  // Only valid discounts receive labels, and clicking their explanation controls opens the corresponding tooltips.
  it('分组头部用最高优惠标签替换模型数标签，无有效折扣的分组不渲染', async () => {
    const fixture = marketplaceFixture()
    // Ratios at or above one offer no discount; omission supplies no official comparison.
    fixture[1] = { ...fixture[1], official_price_ratio: 1.2 }
    fixture[2] = { ...fixture[2], official_price_ratio: undefined }
    getAvailableMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')

    // The first group's ratio of 0.1 produces the displayed ninety-percent discount.
    expect(sections[0].text()).toContain('marketplace.maxDiscountOff 90')
    expect(sections[0].text()).not.toContain('marketplace.modelsStat')
    expect(sections[1].text()).not.toContain('marketplace.maxDiscountOff')
    expect(sections[2].text()).not.toContain('marketplace.maxDiscountOff')
    expect(sections[3].text()).toContain('marketplace.maxDiscountOff 60')

    // Click activation exposes the explanations without relying on a hover-capable pointer.
    await sections[0].get('[data-testid="group-max-discount-tag"]').trigger('click')
    await nextTick()
    expect(
      visibleTooltips().some((el) => el.textContent?.includes('marketplace.maxDiscountHint')),
    ).toBe(true)
    await sections[0].get('[data-testid="group-rate-multiplier-tag"]').trigger('click')
    await nextTick()
    expect(
      visibleTooltips().some((el) => el.textContent?.includes('marketplace.rateMultiplierHint')),
    ).toBe(true)
  })

  // Four groups use every combination of the two display flags, including absent flags.
  // Each header hides its selected tags while the group's model cards remain available.
  it('最高优惠和倍率标签按分组独立关闭，旧配置默认显示', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = { ...fixture[0], hide_max_discount: true, hide_rate_multiplier: false }
    fixture[1] = { ...fixture[1], hide_max_discount: false, hide_rate_multiplier: true }
    fixture[2] = { ...fixture[2], hide_max_discount: true, hide_rate_multiplier: true }
    getAvailableMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')
    expect(
      sections.map((section) => section.find('[data-testid="group-max-discount-tag"]').exists()),
    ).toEqual([false, true, false, true])
    expect(
      sections.map((section) => section.find('[data-testid="group-rate-multiplier-tag"]').exists()),
    ).toEqual([true, false, false, true])
    for (const section of sections) {
      expect(section.text()).toContain('GPT 5.5')
    }
    wrapper.unmount()
  })

  // A priced model combines an omitted zero-price default interval with a paid successor.
  // Its card summary retains the free amount and both context-range labels.
  it('模型卡片摘要保留免费默认区间', async () => {
    const fixture = marketplaceFixture()
    fixture[0].models[0].pricing = {
      pricing_mode: 'token',
      price_status: 'priced',
      context_intervals: [
        { min_tokens: 0, max_tokens: 100 },
        { min_tokens: 100, max_tokens: null, input_price_per_token: 0.000002 },
      ],
    }
    getAvailableMarketplaceModels.mockResolvedValue(fixture)
    const wrapper = await mountMarketplace()
    const section = wrapper.findAll('[data-testid="marketplace-group-section"]')[0]
    expect(section.text()).toContain('0.00')
    expect(section.text()).toContain('0-100')
    expect(section.text()).toContain('100+')
  })

  // Loaded model cards expose their identifier copy controls in the mounted page.
  // Clicking the first control sends that model's exact identifier to the clipboard collaborator.
  it('模型卡片中的模型 ID 支持一键复制', async () => {
    const wrapper = await mountMarketplace()

    // The card action copies the model identifier rather than its display label.
    const cardCopyButtons = wrapper.findAll('[data-testid="model-id-copy"]')
    expect(cardCopyButtons.length).toBeGreaterThan(0)
    await cardCopyButtons[0].trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith('gpt-5.5')
  })

  // Legacy models omit explicit modalities while their prices provide different inference hints.
  // Cards show text-plus-image input for the priced model and the unpriced text fallback.
  it('模型卡片右上角展示输入输出能力标签', async () => {
    const wrapper = await mountMarketplace()

    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')
    const gptCards = sections[0].findAll('article')
    // An image-input token price supplies the legacy image-input capability hint.
    expect(
      gptCards[0]
        .get('[data-testid="model-capability-tags"]')
        .findAll('[data-modality]')
        .map((tag) => tag.attributes('data-modality')),
    ).toEqual(['input-text', 'input-image', 'output-text'])
    // The unpriced fixture follows the existing text-only fallback capability path.
    expect(
      gptCards[1]
        .get('[data-testid="model-capability-tags"]')
        .findAll('[data-modality]')
        .map((tag) => tag.attributes('data-modality')),
    ).toEqual(['input-text', 'output-text'])
  })

  // Explicit server modalities conflict with the legacy inference for an image model.
  // The displayed capability tags must follow server text input and image output metadata.
  it('能力标签优先使用接口下发的模态元数据', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],
      models: [
        // Explicit text-only input overrides the model-name rule that would also infer image input.
        {
          ...marketplaceModel('gpt-image-2', 'GPT Image 2', imagePricing),
          input_modalities: ['text'],
          output_modalities: ['image'],
        },
      ],
    }
    getAvailableMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')

    expect(
      sections[0]
        .get('[data-testid="model-capability-tags"]')
        .findAll('[data-modality]')
        .map((tag) => tag.attributes('data-modality')),
    ).toEqual(['input-text', 'output-image'])
  })

  // Two tiered Gemini identifiers receive complete server-supplied audiovisual input metadata.
  // Each card retains all four input modalities and its declared text output tag.
  it('Gemini Flash 档位展示接口下发的完整音视频输入能力', async () => {
    const fixture = marketplaceFixture()
    // Tier identifiers stay unchanged while explicit metadata supplies their complete input set.
    fixture[0].models = ['gemini-3.7-flash-tiered', 'gemini-3.8-flash-tiered'].map((id) => ({
      ...marketplaceModel(id, id, imagePricing),
      input_modalities: ['text', 'image', 'audio', 'video'],
      output_modalities: ['text'],
    }))
    getAvailableMarketplaceModels.mockResolvedValue(fixture)
    const wrapper = await mountMarketplace()
    const cards = wrapper.findAll('[data-testid="marketplace-group-section"]')[0].findAll('article')
    expect(cards).toHaveLength(2)
    for (const card of cards) {
      expect(
        card
          .get('[data-testid="model-capability-tags"]')
          .findAll('[data-modality]')
          .map((tag) => tag.attributes('data-modality')),
      ).toEqual(['input-text', 'input-image', 'input-audio', 'input-video', 'output-text'])
    }
  })

  // A legacy model advertises protocols without a per-protocol attribute map.
  // Its details tooltip preserves catalog order, endpoint hints, native flags, and the native-protocol legend.
  it('模型信息浮层按目录顺序列出可用协议并标出原生协议，没有属性时也显示入口', async () => {
    getAvailableMarketplaceModels.mockResolvedValueOnce([
      marketplaceGroup(1, 'Plus', [
        {
          ...marketplaceModel('gpt-5.5', 'GPT 5.5', tokenPricing),
          protocols: ['openai_chat_completions', 'anthropic_messages'],
          native_protocols: ['openai_chat_completions'],
        },
        marketplaceModel('legacy-unpriced', 'Legacy Unpriced', unpricedPricing),
      ]),
    ])
    const wrapper = await mountMarketplace()
    const triggers = wrapper.findAll('[data-testid="model-attributes-trigger"]')
    expect(triggers).toHaveLength(1)

    await triggers[0].trigger('click')
    await nextTick()
    const tooltip = visibleTooltips().find((el) =>
      el.querySelector('[data-testid="model-protocols"]'),
    )
    expect(tooltip).toBeDefined()
    const chips = Array.from(tooltip!.querySelectorAll<HTMLElement>('[data-protocol]'))
    expect(chips.map((chip) => chip.dataset.protocol)).toEqual([
      'anthropic_messages',
      'openai_chat_completions',
    ])
    expect(chips.map((chip) => chip.title)).toEqual([
      'POST /v1/messages · marketplace.protocolConvertedHint',
      'POST /v1/chat/completions · marketplace.protocolNativeHint',
    ])
    expect(chips.map((chip) => chip.dataset.native)).toEqual(['false', 'true'])
    expect(tooltip!.querySelector('[data-testid="model-protocols-native-legend"]')).not.toBeNull()
    expect(chips[0].textContent).toContain('marketplace.protocolNames.anthropic_messages')
    wrapper.unmount()
  })

  // A group advertises the xAI display brand while using a Grok title.
  // The header renders the mapped graphical provider icon and omits the fallback-letter element.
  it('xAI 分组展示品牌图形', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],
      name: 'Grok',
      display_brand: 'xAI',
    }
    getAvailableMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()

    // The xAI alias selects existing Grok artwork instead of the fallback initial.
    const grokGroup = wrapper
      .findAll('[data-testid="marketplace-group-section"]')
      .find((section) => section.get('h2').text() === 'Grok')
    expect(grokGroup?.find('.provider-icon[width="28px"]').exists()).toBe(true)
    expect(grokGroup?.find('.provider-icon-fallback').exists()).toBe(false)
  })

  // Group names and explicit display brands exercise the supported Zhipu aliases.
  // Both icon sizes use the same expected SVG paths and omit fallback artwork.
  it.each([
    ['GLM', ''],
    ['zai', ''],
    ['Z.AI', ''],
    ['zai Pro', ''],
    ['自定义分组', '智谱'],
    ['自定义分组', 'zai'],
    ['自定义分组', 'z.ai'],
  ])('分组 %s、展示品牌 %s 显示智谱图标', async (name, displayBrand) => {
    getAvailableMarketplaceModels.mockResolvedValue([
      {
        ...marketplaceGroup(1, name, [marketplaceModel('glm-5', 'GLM 5', tokenPricing)]),
        display_brand: displayBrand,
      },
    ])

    const wrapper = await mountMarketplace()
    const group = wrapper.get('[data-testid="marketplace-group-section"]')

    for (const size of ['14px', '28px']) {
      const icon = group.get(`svg.provider-icon[width="${size}"]`)
      expect(icon.findAll('path').map((path) => path.attributes('d'))).toEqual(
        modelIconData.zhipu.paths,
      )
    }
    expect(group.find('.provider-icon-fallback').exists()).toBe(false)
    expect(group.text()).toContain('智谱')
    wrapper.unmount()
  })

  // An image model appears in a group using the shared multiplier display.
  // The page must omit a separate image-rate label across all rendered group sections.
  it('只展示统一分组倍率', async () => {
    const fixture = marketplaceFixture()
    fixture[0] = {
      ...fixture[0],

      models: [marketplaceModel('gpt-image-1', 'GPT Image', imagePricing)],
    }
    getAvailableMarketplaceModels.mockResolvedValue(fixture)

    const wrapper = await mountMarketplace()

    expect(
      wrapper
        .findAll('[data-testid="marketplace-group-section"]')
        .map((section) => section.text())
        .join('\n'),
    ).not.toContain('marketplace.imageRateMultiplierValue')
  })

  // A model card expands its pricing disclosure and then selects fast mode.
  // Standard categories appear initially, fast rows replace them, and the disclosure can close again.
  it('模型卡片可展开抽屉式定价面板并切换 fast mode', async () => {
    const wrapper = await mountMarketplace()

    const toggle = wrapper.get('[data-testid="model-pricing-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')

    await toggle.trigger('click')
    await nextTick()

    // The expanded card initially renders its standard input and output price categories.
    const sections = wrapper.findAll('[data-testid="marketplace-group-section"]')
    const firstCard = sections[0].findAll('article')[0]
    expect(firstCard.text()).toContain('marketplace.input')
    expect(firstCard.text()).toContain('marketplace.imageInput')
    expect(firstCard.text()).toContain('marketplace.output')

    const fastSwitch = firstCard.get('[data-testid="pricing-fast-switch"]')
    await fastSwitch.findAll('button')[1].trigger('click')
    await nextTick()

    // Fast selection replaces the standard image-input category with its fast counterpart.
    expect(firstCard.text()).toContain('marketplace.fastImageInput')
    expect(firstCard.text()).not.toContain('marketplace.imageInput')

    await wrapper.get('[data-testid="model-pricing-toggle"]').trigger('click')
    await nextTick()
    expect(wrapper.get('[data-testid="model-pricing-toggle"]').attributes('aria-expanded')).toBe(
      'false',
    )
  })
})

// Public model cards display a custom video quote alongside a token-priced model.
// Selecting the video filter retains that video model and removes the token model.
it('shows and filters custom video prices', async () => {
  getAvailableMarketplaceModels.mockResolvedValueOnce([
    marketplaceGroup(1, 'Videos', [
      marketplaceModel('custom-video', 'Custom Video', {
        pricing_mode: 'video',
        price_status: 'priced',
        video_prices: { '768p': 0.12, '2k': 0 },
      }),
      marketplaceModel('text-model', 'Text Model', tokenPricing),
    ]),
  ])
  const wrapper = await mountMarketplace()
  expect(wrapper.text()).toContain('768p')
  expect(wrapper.text()).toContain('marketplace.perSecond')
  await wrapper.get('[data-testid="select-option-video"]').trigger('click')
  expect(wrapper.text()).toContain('Custom Video')
  expect(wrapper.text()).not.toContain('Text Model')
  wrapper.unmount()
})
