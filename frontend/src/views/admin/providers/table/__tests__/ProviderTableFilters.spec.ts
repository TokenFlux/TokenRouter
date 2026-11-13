import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import ProviderTableFilters from '../ProviderTableFilters.vue'
import type { AdminGroup } from '@/api/groups.types'

// ProviderTableFilters.spec.ts checks group-filter options for active and
// inactive provider groups. The real filter panel renders each group as an
// option row, so assertions read the visible row labels, including the
// translated inactive suffix.

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

// group builds an active OpenAI group with optional policies disabled.
// Overrides are applied last so filter tests can vary group identity and
// status while retaining the remaining API shape.
function group(overrides: Partial<AdminGroup>): AdminGroup {
  return {
    id: 1,
    name: 'group',
    description: null,
    platform: 'openai',
    rate_multiplier: 1,
    is_exclusive: false,
    session_isolation_enabled: false,
    status: 'active',
    allow_image_generation: false,
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
    ...overrides,
  }
}

describe('ProviderTableFilters', () => {
  // The filter receives one active group and one inactive group. Opening its
  // controls retains both IDs and adds the translated inactive suffix only to
  // the disabled group.
  it('keeps inactive groups visible in the group filter', async () => {
    const wrapper = mount(ProviderTableFilters, {
      props: {
        searchQuery: '',
        filters: {
          platform: '',
          type: '',
          status: '',
          privacy_mode: '',
          group: '',
        },
        groups: [
          group({ id: 10, name: 'Active Pool', status: 'active' }),
          group({ id: 11, name: 'Disabled Pool', status: 'inactive' }),
        ],
      },
      global: {
        stubs: {
          SearchInput: true,
          Icon: true,
        },
      },
    })

    await wrapper.get('button[aria-label="common.filter"]').trigger('click')

    const groupField = wrapper
      .findAll('section')
      .find((section) => section.get('button').text() === 'admin.providers.columns.groups')
    expect(groupField).toBeDefined()
    await groupField!.get('button').trigger('click')
    const rowLabels = groupField!.findAll('[role="option"]').map((row) => row.text())

    expect(rowLabels).toEqual(
      expect.arrayContaining(['Active Pool', 'Disabled Pool (common.inactive)']),
    )
  })
})
