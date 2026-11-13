import type { ComputedRef, InjectionKey, Ref } from 'vue'
import type { IconName } from '@/components/icons/registry'
import type { SelectOption } from './Select.vue'

// filterPanel.ts defines the injected connections between a filter panel, its
// collapsible fields, and the option lists inside those fields, plus the value and
// search rules those lists share with Select. The panel tells fields when it opens and
// how many fields it holds, so each field can decide whether to start expanded. Option
// lists report their selection to the owning field, which shows it as a summary while
// the field is collapsed.
//
// Static filter option lists begin with an unfiltered entry whose value is blank or the
// literal all. The shared empty-value helpers recognize that entry, so option lists can
// hide it, clicking the selected row can restore it, and the field can tell whether its
// control contributes an active condition. Lists without such an entry, such as remote
// search results, treat null as unfiltered unless their field supplies a reset value.

// FilterDotColor names the status dot colors available to filter options. The names follow
// the badge palettes used by list tables, so a status row matches the badge of the same
// value in the table.
export type FilterDotColor =
  'green' | 'yellow' | 'amber' | 'orange' | 'red' | 'blue' | 'cyan' | 'purple' | 'gray' | 'brand'

// FilterOption is a Select option that can also carry a row marker for filter lists.
export interface FilterOption extends SelectOption {
  icon?: IconName // Line icon drawn in the text color.
  dot?: FilterDotColor // Status dot color, used when no icon is given.
}

// FilterControlState reports a nested control selection to its owning field.
export interface FilterControlState {
  active: boolean // Contributing filter selection flag.
  text: string // Visible selected value text.
}

// FilterPanelContext lets fields follow panel openings and count their siblings.
export interface FilterPanelContext {
  // openCount increases each time the panel opens, so fields can reset their expanded
  // state for a new opening without tracking the closing transition.
  openCount: Readonly<Ref<number>>
  // fieldCount reports how many fields are mounted in the panel; small panels expand
  // every field because collapsing a short list only adds clicks.
  fieldCount: Readonly<Ref<number>>
  // registerField counts one mounted field and returns the matching removal callback,
  // which the field calls before unmounting.
  registerField: () => () => void
}

// FilterFieldContext connects a nested control with its field reset value and summary.
export interface FilterFieldContext {
  // emptyValue returns the field reset value for its nested control. Undefined tells
  // the control to infer it from the first available option, while other values
  // override that inference.
  emptyValue: () => unknown
  // labelId is the element id of the field heading; option groups reference it as
  // their accessible name.
  labelId: string
  // bindControl installs a computed control state on the owning field. A later
  // registration replaces the previous state, and the field reads the current computed
  // value when building its collapsed summary.
  bindControl: (state: ComputedRef<FilterControlState>) => void
}

// FILTER_PANEL_KEY connects fields to their panel, while FILTER_FIELD_KEY connects a
// control to the reset value and summary registration of one field.
export const FILTER_PANEL_KEY: InjectionKey<FilterPanelContext> = Symbol('filterPanel')
export const FILTER_FIELD_KEY: InjectionKey<FilterFieldContext> = Symbol('filterField')

// isBlank recognizes null, undefined, and empty strings as unselected values.
const isBlank = (value: unknown) => value === null || value === undefined || value === ''

// isUnfilteredValue reports whether an option value means "no condition": a blank value
// or the literal all used by marketplace filters.
export function isUnfilteredValue(value: unknown): boolean {
  return isBlank(value) || value === 'all'
}

// resolveEmptyValue infers the reset value from the first option. An unfiltered first
// value is retained, while any other first option produces null for an unselected
// field.
export function resolveEmptyValue(firstOptionValue: unknown): unknown {
  return isUnfilteredValue(firstOptionValue) ? firstOptionValue : null
}

// isSameFilterValue compares a selected value against its reset value. Null,
// undefined, and an empty string are interchangeable blanks; other values require
// strict equality to count as inactive.
export function isSameFilterValue(value: unknown, emptyValue: unknown): boolean {
  return value === emptyValue || (isBlank(value) && isBlank(emptyValue))
}

// SearchableOption lists the option record fields that text search inspects.
export interface SearchableOption {
  label?: unknown // Visible option label.
  description?: unknown // Secondary option text.
  displayBrand?: unknown // Brand shown beside model options.
  search_terms?: unknown // Additional aliases, such as translated names.
}

// matchesOptionQuery reports whether an option matches a lowercase search query. It
// compares the configured label text, description, display brand, and search aliases
// by substring, so translated or vendor names find the same option as its label.
export function matchesOptionQuery(
  option: SearchableOption,
  label: string,
  lowercaseQuery: string,
): boolean {
  const aliases = Array.isArray(option.search_terms) ? option.search_terms : []
  const texts = [label, option.description, option.displayBrand, ...aliases]
  return texts.some((text) => !isBlank(text) && String(text).toLowerCase().includes(lowercaseQuery))
}
