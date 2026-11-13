import type { IconName } from '@/components/icons/registry'
import type { FilterOption } from './filterPanel'
import type { SelectOption } from './Select.vue'

// filterOptionIcons.ts maps the shared usage filter values to the line icons drawn in
// filter rows. User usage, the usage dashboard, administrator usage, and the pricing
// filters each build their own option lists for these values; reading the icons from one
// table keeps a value's icon identical on every page. Each map gives distinct icons to the
// values of one field, keyed by the option value converted to a string, and a value
// missing from a map renders no icon.

// withOptionIcons returns the options with the icon of each mapped value attached. Values
// are looked up as strings, so numeric and boolean values use keys such as "0" and
// "true"; unmapped values, including the unfiltered entry, keep their original record.
export function withOptionIcons(
  options: readonly SelectOption[],
  icons: Record<string, IconName>,
): FilterOption[] {
  return options.map((option) => {
    const icon = icons[String(option.value)]
    return icon ? { ...option, icon } : option
  })
}

// BILLING_MODE_ICONS marks the billing units shared by usage records and model pricing,
// plus the marketplace's unpriced choice; the media modes reuse the modality glyphs.
export const BILLING_MODE_ICONS: Record<string, IconName> = {
  token: 'modalityText',
  per_request: 'chatBubble',
  image: 'modalityImage',
  video: 'modalityVideo',
  unpriced: 'minus',
}

// BILLING_TYPE_ICONS marks the numeric billing sources of usage records: zero charges
// the balance and one consumes a subscription.
export const BILLING_TYPE_ICONS: Record<string, IconName> = {
  '0': 'dollar',
  '1': 'calendar',
}

// REQUEST_TYPE_ICONS marks the canonical request transport modes; Cyber marks requests
// stopped by the cyber risk policy.
export const REQUEST_TYPE_ICONS: Record<string, IconName> = {
  ws_v2: 'swap',
  live: 'play',
  stream: 'moveRight',
  sync: 'document',
  cyber: 'shieldX',
}

// COMPACTION_ICONS marks native compaction v2 with true and legacy compaction with false,
// matching the boolean filter values.
export const COMPACTION_ICONS: Record<string, IconName> = {
  true: 'sparkles',
  false: 'archive',
}

// REQUEST_SOURCE_ICONS marks who issued an administrator usage record: customers, the
// system, or one of the internal probes and provider tests.
export const REQUEST_SOURCE_ICONS: Record<string, IconName> = {
  user: 'user',
  system: 'cog',
  group_probe: 'folder',
  provider_test: 'beaker',
  scheduled_test: 'history',
  quota_probe: 'chartBar',
}

// ERROR_PHASE_ICONS marks the request lifecycle phase that produced an error.
export const ERROR_PHASE_ICONS: Record<string, IconName> = {
  upstream: 'cloud',
  provider_auth: 'key',
  request: 'document',
  auth: 'lock',
  routing: 'swap',
  internal: 'cpu',
}

// ERROR_CATEGORY_ICONS marks the searchable error categories shown to users and
// administrators.
export const ERROR_CATEGORY_ICONS: Record<string, IconName> = {
  auth: 'lock',
  rate_limit: 'clock',
  quota: 'ban',
  invalid_request: 'xCircle',
  service_unavailable: 'power',
  upstream: 'cloud',
  internal: 'cpu',
  cyber: 'shieldX',
}
