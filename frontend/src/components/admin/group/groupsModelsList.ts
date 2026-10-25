// groupsModelsList.ts reconciles a group's saved model list with current requestable candidates
// and editable selections. Saved identifiers remain in the editor even when the backend
// candidate list changes, so a catalog refresh does not silently remove administrator choices.
// Model identifiers are trimmed and deduplicated in first-seen order.
//
// Initial hydration selects saved choices and leaves other candidates unchecked. Loading the
// catalog therefore keeps an unconfigured list empty when the group form is saved. Later
// refreshes retain current selection and ordering. Availability-probe choices intersect
// selected models with current backend candidates, preventing a historical custom entry from
// becoming a probe target after it stops being requestable.

// ModelsListConfig represents the persisted enablement flag and ordered custom model
// identifiers.
export interface ModelsListConfig {
  enabled: boolean // configured enablement flag
  models: string[] // ordered custom model identifiers
}

// ModelsListItem represents one selectable model row in the editor.
export interface ModelsListItem {
  id: string // stable record identifier
  selected: boolean // model selection flag
}

// ModelsListState separates saved identifiers, backend candidates, and editable rows.
// savedModels retains initial configuration, candidateModels records requestability, and items
// carries current ordering and selection used for saving.
export interface ModelsListState {
  enabled: boolean // configured enablement flag
  // savedModels retains normalized persisted identifiers before candidate hydration.
  // Serialization uses this list while no editable rows exist.
  savedModels: string[]
  // candidateModels records current backend requestability. Probe choices intersect enabled
  // custom selections with this list so historical rows cannot restore unavailable models.
  candidateModels: string[]
  items: ModelsListItem[] // editable model selection rows
}

// createModelsListState initializes an editable model list from optional saved configuration.
// Missing configuration disables the custom list, saved identifiers are normalized, and
// candidate and item arrays begin empty.
export const createModelsListState = (
  config?: Partial<ModelsListConfig> | null,
): ModelsListState => ({
  enabled: config?.enabled ?? false,
  savedModels: normalizeModels(config?.models ?? []),
  candidateModels: [],
  items: [],
})

// hydrateModelsListState creates a draft and reconciles the supplied backend candidates. Saved
// choices determine initial selection, and candidates absent from those choices start unchecked
// until the administrator selects individual rows or uses the select-all action.
export const hydrateModelsListState = (
  config: Partial<ModelsListConfig> | null | undefined,
  candidates: string[],
): ModelsListState => {
  const state = createModelsListState(config)
  setModelsListCandidates(state, candidates)
  return state
}

// setModelsListCandidates replaces candidates and reconciles editable model rows in place.
// Existing row order precedes saved identifiers and new candidates, with duplicate and blank
// identifiers removed. Once rows exist, their current selection takes precedence over the saved
// configuration.
//
// During initial hydration, saved models supply selection and other candidates start unchecked.
// Loading a catalog must not turn its suggestions into persisted configuration when an unrelated
// group setting is saved. Later refreshes keep current choices, including an empty selection,
// while historical saved rows remain available for editing.
export const setModelsListCandidates = (state: ModelsListState, candidates: string[]) => {
  const normalizedCandidates = normalizeModels(candidates)
  state.candidateModels = normalizedCandidates
  const currentSelected = new Set(
    state.items.filter((item) => item.selected).map((item) => item.id),
  )
  const savedSelected = new Set(state.savedModels)
  const hasExistingItems = state.items.length > 0
  const selectionOrder = normalizeModels([
    ...state.items.map((item) => item.id),
    ...state.savedModels,
    ...normalizedCandidates,
  ])

  state.items = selectionOrder.map((id) => {
    const selected = hasExistingItems ? currentSelected.has(id) : savedSelected.has(id)

    return {
      id,
      selected,
    }
  })
}

// toggleModelsListItem reverses selection for a matching model in the mutable draft. Unknown
// identifiers leave the list unchanged, allowing callers to handle rows that disappeared during
// a refresh.
export const toggleModelsListItem = (state: ModelsListState, modelID: string) => {
  const item = state.items.find((item) => item.id === modelID)
  if (item) {
    item.selected = !item.selected
  }
}

// selectAllModelsListItems marks every editable model row as selected in place.
export const selectAllModelsListItems = (state: ModelsListState) => {
  state.items.forEach((item) => {
    item.selected = true
  })
}

// invertModelsListSelection reverses every editable model selection in the supplied draft.
export const invertModelsListSelection = (state: ModelsListState) => {
  state.items.forEach((item) => {
    item.selected = !item.selected
  })
}

// moveModelsListItem moves one draft row to a different list position. Equal or out-of-range
// indexes leave the list unchanged; valid movement keeps the existing row object and selection.
export const moveModelsListItem = (state: ModelsListState, fromIndex: number, toIndex: number) => {
  if (
    fromIndex === toIndex ||
    fromIndex < 0 ||
    toIndex < 0 ||
    fromIndex >= state.items.length ||
    toIndex >= state.items.length
  ) {
    return
  }
  const [item] = state.items.splice(fromIndex, 1)
  state.items.splice(toIndex, 0, item)
}

// buildModelsListConfig serializes selected draft rows in their current display order. Before
// any rows exist, it copies saved models so loading or saving an unhydrated draft retains its
// persisted selection.
export const buildModelsListConfig = (state: ModelsListState): ModelsListConfig => ({
  enabled: state.enabled,
  models:
    state.items.length > 0
      ? state.items.filter((item) => item.selected).map((item) => item.id)
      : [...state.savedModels],
})

// getAvailabilityProbeCandidateModels returns models currently eligible for probe selection. A
// disabled custom list uses all current candidates. An enabled list uses selected rows, or
// saved models before hydration, and intersects them with backend candidates.
//
// That intersection applies after normalization because saved custom identifiers can outlive
// routing or allowlist changes. Historical rows remain editable in the custom list, but their
// presence alone cannot restore requestability for an availability probe.
export const getAvailabilityProbeCandidateModels = (state: ModelsListState): string[] => {
  // Backend candidates already reflect model mapping and allowlists, so intersecting here
  // prevents saved custom rows from restoring an unrequestable probe model.
  if (state.enabled) {
    const available = new Set(state.candidateModels)
    const selected =
      state.items.length > 0
        ? state.items.filter((item) => item.selected).map((item) => item.id)
        : state.savedModels
    return normalizeModels(selected).filter((model) => available.has(model))
  }

  // With custom listing disabled, probes use a copy of the currently returned backend candidate
  // identifiers.
  return [...state.candidateModels]
}

// normalizeModels trims identifiers and removes blank or repeated models in order. The returned
// array is newly allocated, and the first occurrence of each normalized identifier determines
// its position.
const normalizeModels = (models: string[]): string[] => {
  const seen = new Set<string>()
  const out: string[] = []
  for (const raw of models) {
    const model = raw.trim()
    if (!model || seen.has(model)) {
      continue
    }
    seen.add(model)
    out.push(model)
  }
  return out
}
