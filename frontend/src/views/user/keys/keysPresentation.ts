import type { Group } from '@/api/groups.types'
import type { ApiKey } from '@/api/keys.types'
import { groupForKeyConfig } from '@/utils/clientConfig'
import { resolveProviderBrand } from '@/utils/providerBrand'

/**
 * keysPresentation.ts provides pure group-option formatting, composite-chip
 * slicing, concurrency occupancy styling, and reset countdown formatting shared
 * across KeysView and its inline group-selector and client-import dialogs.
 */

/**
 * KeyGroupSelectOption describes a routing group entry in the form and inline
 * group selectors.
 */
export type KeyGroupSelectOption = {
  value: number // Group numeric identifier.
  label: string // Display name or raw group name.
  search_terms?: string[] | null // Optional search keywords from group metadata.
  description?: string | null // Optional group description.
  displayBrand: string | null // Trimmed brand identifier or null.
  rate: number // Base group rate multiplier.
  userRate: number | null // User-specific rate override or null.
}

/**
 * COMPOSITE_GROUP_PREVIEW_LIMIT bounds ordinary inline mapping previews while
 * allowing a single extra mapping to render directly without a +1 remainder.
 */
export const COMPOSITE_GROUP_PREVIEW_LIMIT = 4

/**
 * buildKeyGroupOptions projects Group records and user rate overrides into
 * selectable options for the key form and inline group dropdown.
 */
export function buildKeyGroupOptions(
  source: Group[],
  userGroupRates: Record<number, number>,
): KeyGroupSelectOption[] {
  return source.map((group) => ({
    value: group.id,
    label: group.display_name || group.name,
    search_terms: group.search_terms,
    description: group.description,
    displayBrand: group.display_brand?.trim() || null,
    rate: group.rate_multiplier,
    userRate: userGroupRates[group.id] ?? null,
  }))
}

/**
 * resolveKeyGroupWithModels merges loaded catalog metadata with embedded key
 * group info and applies per-key model redirects for client setup instructions.
 */
export function resolveKeyGroupWithModels(key: ApiKey | null, groups: Group[]): Group | undefined {
  if (!key?.group_id) return undefined
  const available = groups.find((group) => group.id === key.group_id)
  return groupForKeyConfig(
    available ? { ...key.group, ...available } : (key.group ?? undefined),
    key.model_mapping,
  )
}

/**
 * visibleCompositeGroups returns the inline composite mapping preview slice for
 * a key row.
 */
export function visibleCompositeGroups(row: ApiKey) {
  const groups = row.composite_groups ?? []
  if (groups.length <= COMPOSITE_GROUP_PREVIEW_LIMIT + 1) {
    return groups
  }
  return groups.slice(0, COMPOSITE_GROUP_PREVIEW_LIMIT)
}

/**
 * compositeGroupChipClass selects the provider-brand badge palette or neutral
 * inset surface for a composite mapping chip.
 */
export function compositeGroupChipClass(displayBrand?: string | null): string {
  const brand = displayBrand?.trim()
  if (!brand) {
    return 'ring-1 ring-inset bg-gray-100 text-gray-900 ring-gray-200 dark:bg-dark-800 dark:text-dark-50 dark:ring-dark-600'
  }
  return `ring-1 ring-inset ${resolveProviderBrand(brand).badgeClass}`
}

/**
 * concurrencyTone colors active concurrency occupancy against the configured cap.
 */
export function concurrencyTone(row: ApiKey): {
  dot: string // Tailwind dot background class.
  text: string // Tailwind text emphasis class.
} {
  const current = row.current_concurrency ?? 0
  const limit = row.concurrency_limit ?? 0
  if (limit > 0 && current >= limit) {
    return { dot: 'bg-red-500', text: 'font-medium text-red-600 dark:text-red-400' }
  }
  if (limit > 0 && current >= limit * 0.8) {
    return { dot: 'bg-amber-500', text: 'font-medium text-amber-600 dark:text-amber-400' }
  }
  return { dot: 'bg-emerald-500', text: 'text-gray-500 dark:text-dark-300' }
}

/**
 * formatKeyResetCountdown formats a future reset timestamp against now or
 * returns resetNowText once elapsed.
 */
export function formatKeyResetCountdown(
  resetAt: string | null,
  now: Date,
  resetNowText: string,
): string {
  if (!resetAt) return ''
  const diff = new Date(resetAt).getTime() - now.getTime()
  if (diff <= 0) return resetNowText
  // Truncate elapsed milliseconds to whole day, hour, and minute units.
  const days = Math.floor(diff / 86400000)
  const hours = Math.floor((diff % 86400000) / 3600000)
  const mins = Math.floor((diff % 3600000) / 60000)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}
