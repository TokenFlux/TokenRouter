import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

import PricingView from '../PricingView.vue'

const { listPricingConfigs, getGroups, getWebSearchEmulationConfig } = vi.hoisted(() => ({
  listPricingConfigs: vi.fn(),
  getGroups: vi.fn(),
  getWebSearchEmulationConfig: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    pricing: {
      list: listPricingConfigs,
      create: vi.fn(),
      update: vi.fn(),
      remove: vi.fn(),
      syncPricingModels: vi.fn(),
      getModelDefaultPricing: vi.fn()
    },
    groups: {
      getAll: getGroups
    },
    settings: {
      getWebSearchEmulationConfig
    },
    accounts: {
      list: vi.fn().mockResolvedValue({ items: [], total: 0 }),
      getById: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const BaseDialogStub = defineComponent({
  props: {
    show: {
      type: Boolean,
      default: false
    }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  props: {
    modelValue: {
      type: [String, Number],
      default: ''
    },
    options: {
      type: Array,
      default: () => []
    }
  },
  emits: ['update:modelValue'],
  template: `
    <div>
      <button
        v-for="option in options"
        :key="option.value"
        type="button"
        :data-option="option.value"
        @click="$emit('update:modelValue', option.value)"
      >
        {{ option.label }}
      </button>
    </div>
  `
})

function mountView() {
  return mount(PricingView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: true,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: SelectStub,
        Icon: true,
        PlatformIcon: true,
        Toggle: true,
        DefaultPricingPanel: true,
        PricingEntryCard: true
      }
    }
  })
}

describe('PricingView model routing copy', () => {
  beforeEach(() => {
    listPricingConfigs.mockReset()
    getGroups.mockReset()
    getWebSearchEmulationConfig.mockReset()
    listPricingConfigs.mockResolvedValue({ items: [], total: 0 })
    getGroups.mockResolvedValue([])
    getWebSearchEmulationConfig.mockResolvedValue({ enabled: false, providers: [] })
  })

  it('updates the billing hint for all three price sources', async () => {
    const wrapper = mountView()
    await flushPromises()

    const createButton = wrapper.findAll('button').find(button => button.text().includes('admin.pricing.createPricingConfig'))
    expect(createButton).toBeTruthy()
    await createButton!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).not.toContain('admin.pricing.form.applyPricingToAccountStats')
    expect(wrapper.get('[data-testid="billing-model-source-hint"]').text()).toBe('admin.pricing.form.billingModelSourceHintGroupMapped')

    await wrapper.get('[data-option="requested"]').trigger('click')
    expect(wrapper.get('[data-testid="billing-model-source-hint"]').text()).toBe('admin.pricing.form.billingModelSourceHintRequested')

    await wrapper.get('[data-option="upstream"]').trigger('click')
    expect(wrapper.get('[data-testid="billing-model-source-hint"]').text()).toBe('admin.pricing.form.billingModelSourceHintUpstream')
  })

  it('keeps routing settings out of price configuration', async () => {
    const wrapper = mountView()
    await flushPromises()
    const createButton = wrapper.findAll('button').find(button => button.text().includes('admin.pricing.createPricingConfig'))
    await createButton!.trigger('click')
    await flushPromises()

    const anthropicToggle = wrapper
      .findAll('label')
      .find(label => label.text().includes('admin.groups.platforms.anthropic'))
    expect(anthropicToggle).toBeTruthy()
    await anthropicToggle!.get('input[type="checkbox"]').trigger('change')

    const anthropicTab = wrapper
      .findAll('button')
      .find(button => button.text().includes('admin.groups.platforms.anthropic'))
    expect(anthropicTab).toBeTruthy()
    await anthropicTab!.trigger('click')

    expect(wrapper.find('[data-testid="channel-model-mapping-hint"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.pricing.form.restrictModels')
  })
})
