import { describe, expect, it } from 'vitest'

import {
  buildModelsListConfig,
  createModelsListState,
  getAvailabilityProbeCandidateModels,
  hydrateModelsListState,
  invertModelsListSelection,
  moveModelsListItem,
  selectAllModelsListItems,
  setModelsListCandidates,
  toggleModelsListItem,
} from '../groupsModelsList'

// groupsModelsList.spec.ts checks custom model selection reconciliation and probe candidates
// through the shared state helpers. Saved choices, candidate refreshes, and display order
// affect serialized models, while probe targets remain restricted to currently requestable
// backend candidates.

describe('groupsModelsList', () => {
  // A new disabled custom list receives backend model candidates. Saving the group keeps its
  // configuration empty until the administrator chooses models, even after candidate refreshes.
  it('keeps default candidates out of a new disabled config', () => {
    const state = createModelsListState()

    setModelsListCandidates(state, ['gpt-5.5', 'gpt-5.4'])

    expect(state.enabled).toBe(false)
    expect(state.items).toEqual([
      { id: 'gpt-5.5', selected: false },
      { id: 'gpt-5.4', selected: false },
    ])
    setModelsListCandidates(state, ['gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini'])
    expect(buildModelsListConfig(state)).toEqual({ enabled: false, models: [] })
  })

  // An existing group has an empty disabled list and receives the complete model catalog.
  // Opening and saving its editor keeps the stored list empty despite the large candidate set.
  it('keeps an existing empty list empty after loading a large catalog', () => {
    const candidates = Array.from({ length: 12000 }, (_, index) => `model-${index}`)
    const state = hydrateModelsListState({ enabled: false, models: [] }, candidates)

    expect(state.items).toHaveLength(candidates.length)
    expect(buildModelsListConfig(state)).toEqual({ enabled: false, models: [] })
  })

  // An administrator selects models in a new list and then disables it before saving.
  // The chosen identifiers survive serialization and reopening, while later candidates stay unchecked.
  it('retains deliberate choices when a new list is disabled and reopened', () => {
    const state = hydrateModelsListState(undefined, ['gpt-5.5', 'gpt-5.4'])
    state.enabled = true
    selectAllModelsListItems(state)
    toggleModelsListItem(state, 'gpt-5.4')
    state.enabled = false

    const config = buildModelsListConfig(state)
    expect(config).toEqual({ enabled: false, models: ['gpt-5.5'] })
    const reopened = hydrateModelsListState(config, ['gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini'])
    expect(buildModelsListConfig(reopened)).toEqual(config)
  })

  // A saved custom list receives an expanded backend candidate set. Saved selections remain
  // checked and newly discovered models stay unchecked until the administrator selects them.
  it('keeps saved selections and marks new candidates as unselected when editing', () => {
    const state = createModelsListState({
      enabled: true,
      models: ['gpt-5.5', 'gpt-5.4'],
    })

    setModelsListCandidates(state, ['gpt-5.4', 'legacy-gpt', 'gpt-5.5'])

    expect(state.enabled).toBe(true)
    expect(state.items).toEqual([
      { id: 'gpt-5.5', selected: true },
      { id: 'gpt-5.4', selected: true },
      { id: 'legacy-gpt', selected: false },
    ])
  })

  // A saved model list receives its first set of available candidates. Hydration selects the
  // saved model and leaves the additional candidate unchecked in the resulting editable rows.
  it('preserves explicitly unselected saved candidates when candidates refresh', () => {
    const state = createModelsListState({
      enabled: true,
      models: ['gpt-5.5'],
    })

    setModelsListCandidates(state, ['gpt-5.5', 'gpt-5.4'])

    expect(state.items).toEqual([
      { id: 'gpt-5.5', selected: true },
      { id: 'gpt-5.4', selected: false },
    ])
  })

  // Selected rows are moved into a different display order before serialization. The saved
  // models follow the edited order and exclude unchecked rows from the payload.
  it('builds config with selected models in current display order', () => {
    const state = hydrateModelsListState(
      {
        enabled: true,
        models: ['gpt-5.5', 'gpt-5.4', 'legacy-gpt'],
      },
      ['gpt-5.5', 'gpt-5.4', 'legacy-gpt'],
    )

    toggleModelsListItem(state, 'legacy-gpt')
    moveModelsListItem(state, 1, 0)

    expect(buildModelsListConfig(state)).toEqual({
      enabled: true,
      models: ['gpt-5.4', 'gpt-5.5'],
    })
  })

  // A custom model list is disabled after selections are made. Serialization retains those
  // chosen identifiers so reopening and enabling the list can restore the administrator draft.
  it('keeps selected models in payload even when disabled so reopening can restore choices', () => {
    const state = hydrateModelsListState(
      {
        enabled: false,
        models: ['gpt-5.5'],
      },
      ['gpt-5.5', 'gpt-5.4'],
    )

    expect(buildModelsListConfig(state)).toEqual({
      enabled: false,
      models: ['gpt-5.5'],
    })
  })

  // Saved models are present before any candidates have loaded. Serializing that unhydrated
  // state copies saved choices rather than replacing the persisted list with an empty
  // selection.
  it('preserves saved models when candidates have not loaded yet', () => {
    const state = createModelsListState({
      enabled: true,
      models: ['gpt-5.5', 'gpt-5.4'],
    })

    expect(buildModelsListConfig(state)).toEqual({
      enabled: true,
      models: ['gpt-5.5', 'gpt-5.4'],
    })
  })

  // The draft contains a mixture of selected and unselected candidate rows. The select-all
  // operation marks every row selected for subsequent display and configuration serialization.
  it('selects all candidate models from the toolbar action', () => {
    const state = hydrateModelsListState(
      {
        enabled: true,
        models: ['gpt-5.5'],
      },
      ['gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini'],
    )

    selectAllModelsListItems(state)

    expect(state.items).toEqual([
      { id: 'gpt-5.5', selected: true },
      { id: 'gpt-5.4', selected: true },
      { id: 'gpt-5.4-mini', selected: true },
    ])
  })

  // The draft contains both selected and unselected model rows. Inverting selection flips each
  // state so the resulting configuration contains the complementary set of visible choices.
  it('inverts selected models from the toolbar action', () => {
    const state = hydrateModelsListState(
      {
        enabled: true,
        models: ['gpt-5.5'],
      },
      ['gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini'],
    )

    invertModelsListSelection(state)

    expect(state.items).toEqual([
      { id: 'gpt-5.5', selected: false },
      { id: 'gpt-5.4', selected: true },
      { id: 'gpt-5.4-mini', selected: true },
    ])
  })

  // Custom model listing is enabled with a subset of candidate rows selected. Probe options
  // include the selected requestable models and exclude candidates left unchecked in the draft.
  it('limits availability probe candidates to selected models when custom list is enabled', () => {
    const state = hydrateModelsListState(
      {
        enabled: true,
        models: ['deepseek-v4-flash'],
      },
      ['gpt-5.5', 'deepseek-v4-flash', 'deepseek-v4-pro'],
    )

    expect(getAvailabilityProbeCandidateModels(state)).toEqual(['deepseek-v4-flash'])
  })

  // Custom model listing is disabled despite saved or edited selections. Probe options use
  // every current backend candidate because custom-list selection is not restricting this mode.
  it('uses all candidate models for availability probe when custom list is disabled', () => {
    const state = hydrateModelsListState(
      {
        enabled: false,
        models: ['deepseek-v4-flash'],
      },
      ['gpt-5.5', 'deepseek-v4-pro'],
    )

    expect(getAvailabilityProbeCandidateModels(state)).toEqual(['gpt-5.5', 'deepseek-v4-pro'])
  })

  // Backend candidates change after an earlier probe option list was built. The next probe list
  // drops stale identifiers and reflects the refreshed requestable candidate set.
  it('refreshes availability probe candidates without keeping stale models', () => {
    const state = hydrateModelsListState(
      {
        enabled: false,
        models: ['deepseek-v4-flash'],
      },
      ['gpt-5.5', 'deepseek-v4-flash'],
    )

    setModelsListCandidates(state, ['deepseek-v4-pro'])

    expect(getAvailabilityProbeCandidateModels(state)).toEqual(['deepseek-v4-pro'])
  })

  // A historical custom list contains an upstream model absent from current candidates. Probe
  // selection does not restore that identifier, preserving backend mapping and allowlist
  // restrictions.
  it('does not restore upstream model names from a saved custom list', () => {
    const state = hydrateModelsListState(
      {
        enabled: true,
        models: ['gemini-3.8-flash-tiered', 'gemini-3.8-flash'],
      },
      ['gemini-3.1-pro', 'gemini-3.7-flash', 'gemini-3.8-flash'],
    )

    expect(getAvailabilityProbeCandidateModels(state)).toEqual(['gemini-3.8-flash'])
    setModelsListCandidates(state, [])
    expect(getAvailabilityProbeCandidateModels(state)).toEqual([])
  })
})
