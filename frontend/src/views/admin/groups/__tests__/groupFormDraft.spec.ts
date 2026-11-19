import { describe, expect, it, vi } from 'vitest'
import type { AdminGroup } from '@/api/groups.types'
import { createModelsListState } from '@/components/admin/group/groupsModelsList'
import {
  buildGroupFormGroupOptions,
  buildUpdateGroupRequest,
  createGroupFormDraft,
  hydrateGroupFormDraft,
  hydrateRoutingRulesConcurrently,
  resetGroupFormDraft,
} from '../groupFormDraft'

// groupFormDraft.spec.ts verifies group draft hydration, default_mapped_model
// preservation, concurrent routing-rule provider lookups, cancellation guards,
// and self-exclusion in fallback and provider-copy option builders.

// buildGroup constructs a persisted AdminGroup fixture with optional overrides.
function buildGroup(overrides: Partial<AdminGroup> = {}): AdminGroup {
  return {
    id: 10,
    name: 'Core Group',
    description: 'Primary pool',
    display_brand: 'openai',
    rate_multiplier: 1.2,
    is_exclusive: false,
    status: 'active',
    scheduler_type: 'basic',
    claude_code_only: false,
    fallback_group_id: null,
    fallback_group_id_on_invalid_request: null,
    unavailable_fallback_group_id: null,
    allow_live: false,
    default_mapped_model: 'gpt-5.4-mini',
    model_routing: null,
    model_routing_enabled: false,
    provider_count: 3,
    sort_order: 10,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  } as AdminGroup
}

describe('groupFormDraft', () => {
  // Editing a group with a configured default_mapped_model must preserve that
  // field through hydration and update request serialization, while resetting
  // the draft clears it back to an empty string.
  it('hydrates, submits, and resets default_mapped_model without losing saved values', () => {
    const draft = createGroupFormDraft()
    hydrateGroupFormDraft(draft, buildGroup({ default_mapped_model: 'claude-sonnet-4-6' }))
    expect(draft.default_mapped_model).toBe('claude-sonnet-4-6')

    const payload = buildUpdateGroupRequest(draft, [], createModelsListState(), null, (key) => key)
    expect(payload.default_mapped_model).toBe('claude-sonnet-4-6')
    expect(payload.fallback_group_id).toBe(0)
    expect(payload.fallback_group_id_on_invalid_request).toBe(0)
    expect(payload.unavailable_fallback_group_id).toBe(0)

    resetGroupFormDraft(draft, null)
    expect(draft.default_mapped_model).toBe('')
  })

  // Duplicate provider IDs across routing patterns are fetched once concurrently,
  // failed lookups fall back to `#id`, and stale completions return null.
  it('deduplicates concurrent provider lookups and honors cancellation guards', async () => {
    const getById = vi.fn(async (id: number) => {
      if (id === 99) throw new Error('missing')
      return { id, name: `Provider ${id}` }
    })

    const rules = await hydrateRoutingRulesConcurrently(
      {
        'gpt-*': [7, 8],
        'o3-*': [8, 99],
      },
      getById,
    )
    expect(getById).toHaveBeenCalledTimes(3)
    expect(rules).toEqual([
      {
        pattern: 'gpt-*',
        providers: [
          { id: 7, name: 'Provider 7' },
          { id: 8, name: 'Provider 8' },
        ],
      },
      {
        pattern: 'o3-*',
        providers: [
          { id: 8, name: 'Provider 8' },
          { id: 99, name: '#99' },
        ],
      },
    ])

    const cancelled = await hydrateRoutingRulesConcurrently({ 'gpt-*': [7] }, getById, () => false)
    expect(cancelled).toBeNull()
  })

  // Edit-mode option lists exclude the group being edited from copy and fallback choices.
  it('excludes the edited group ID from copy and fallback option lists', () => {
    const groups = [
      buildGroup({ id: 10, name: 'Self', provider_count: 2 }),
      buildGroup({ id: 11, name: 'Peer', provider_count: 4 }),
      buildGroup({ id: 12, name: 'Inactive', status: 'inactive', provider_count: 1 }),
    ]
    const editOptions = buildGroupFormGroupOptions(groups, (key) => key, 10)
    expect(editOptions.copyProviders.map((item) => item.value)).toEqual([11, 12])
    expect(editOptions.unavailableFallback.map((item) => item.value)).toEqual([null, 11])
    expect(editOptions.invalidRequestFallback.map((item) => item.value)).toEqual([null, 11])
    expect(editOptions.clientFallback.map((item) => item.value)).toEqual([null, 11])
  })
})
