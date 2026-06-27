import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import AccountTableFilters from '../AccountTableFilters.vue'
import type { AdminGroup } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const SelectStub = {
  props: ['modelValue', 'options'],
  emits: ['update:modelValue', 'change'],
  template: `
    <div data-test="select-stub" :data-model-value="String(modelValue ?? '')">
      <!-- 测试只暴露项目 Select 的 options，不使用浏览器原生选择框行为。 -->
      <span v-for="option in options" :key="String(option.value)" data-test="select-option">
        {{ option.label }}
      </span>
    </div>
  `
}

function group(overrides: Partial<AdminGroup>): AdminGroup {
  return {
    id: 1,
    name: 'group',
    description: null,
    platform: 'openai',
    rate_multiplier: 1,
    is_exclusive: false,
    data_sharing_enabled: false,
    session_isolation_enabled: false,
    status: 'active',
    allow_image_generation: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    image_price_1k: null,
    image_price_2k: null,
    image_price_4k: null,
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    unavailable_fallback_group_id: null,
    require_oauth_only: false,
    require_privacy_set: false,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    model_routing: null,
    model_routing_enabled: false,
    mcp_xml_inject: false,
    sort_order: 0,
    ...overrides
  }
}

describe('AccountTableFilters', () => {
  it('keeps inactive groups visible in the group filter', () => {
    const wrapper = mount(AccountTableFilters, {
      props: {
        searchQuery: '',
        filters: {
          platform: '',
          type: '',
          status: '',
          privacy_mode: '',
          group: ''
        },
        groups: [
          group({ id: 10, name: 'Active Pool', status: 'active' }),
          group({ id: 11, name: 'Disabled Pool', status: 'inactive' })
        ]
      },
      global: {
        stubs: {
          Select: SelectStub,
          SearchInput: true
        }
      }
    })

    const selectComponents = wrapper.findAllComponents(SelectStub)
    const groupOptions = selectComponents.at(4)?.props('options') as Array<{ value: string; label: string }>

    expect(groupOptions).toEqual(expect.arrayContaining([
      { value: '10', label: 'Active Pool' },
      { value: '11', label: 'Disabled Pool (common.inactive)' }
    ]))
  })
})
