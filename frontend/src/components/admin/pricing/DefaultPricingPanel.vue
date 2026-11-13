<template>
  <TablePageLayout>
    <template #filters>
      <div class="space-y-2">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon name="search" size="md" class="input-icon text-gray-400 dark:text-gray-500" />
              <input
                v-model="search"
                class="input input-has-icon"
                :placeholder="t('admin.pricing.defaults.search')"
                :aria-label="t('admin.pricing.defaults.search')"
              />
            </div>
            <FilterDropdown
              :active-count="activeFilterCount"
              @reset="
                platform = '';
                mode = '';
              "
            >
              <FilterField :label="t('admin.pricing.defaults.columns.platform')" icon="cloud">
                <FilterOptions v-model="platform" :options="platformOptions">
                  <template #icon="{ value }">
                    <PlatformIcon
                      :platform="value as ProviderPlatform"
                      size="md"
                      :class="platformIconClass(String(value))"
                    />
                  </template>
                </FilterOptions>
              </FilterField>
              <FilterField
                :label="t('admin.pricing.defaults.columns.billing_mode')"
                icon="calculator"
              >
                <FilterOptions v-model="mode" :options="modeOptions" />
              </FilterField>
            </FilterDropdown>
          </div>
          <div class="ml-auto flex shrink-0 flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              class="btn btn-secondary btn-icon"
              :disabled="loading || updating"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="load"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button
              type="button"
              class="btn btn-primary whitespace-nowrap"
              :disabled="loading || updating"
              :title="t('admin.pricing.defaults.updateHint')"
              @click="updateCatalog"
            >
              <Icon
                :name="updating ? 'refresh' : 'download'"
                size="sm"
                class="mr-2"
                :class="updating ? 'animate-spin' : ''"
              />
              {{
                t(updating ? 'admin.pricing.defaults.updating' : 'admin.pricing.defaults.update')
              }}
            </button>
          </div>
        </div>
        <ModelCatalogInfo :version="catalogVersion" :updated-at="updatedAt" />
        <p v-if="notice" role="status" class="text-sm text-emerald-600 dark:text-emerald-400">
          {{ notice }}
        </p>
        <p v-if="error || catalogError" role="alert" class="text-sm text-red-600">
          {{ error || catalogError }}
        </p>
      </div>
    </template>
    <template #table>
      <DataTable
        column-order-storage-key="default-pricing-column-order"
        :columns="columns"
        :data="items"
        :loading="loading"
      >
        <template #cell-platform="{ row }"><PlatformBadge :platform="row.platform" /></template>
        <template #cell-billing_mode="{ row }"
          ><BillingModeBadge :mode="row.billing_mode"
        /></template>
        <template #cell-price="{ row }">
          <span v-if="row.price_status === 'unpriced'">{{
            t('admin.pricing.defaults.unpriced')
          }}</span>
          <div v-else class="space-y-1">
            <div v-for="price in row.prices.slice(0, 2)" :key="price.key" class="text-sm">
              {{ priceLabel(price) }}: {{ formatPrice(price) }}
            </div>
          </div>
        </template>
        <template #cell-actions="{ row }">
          <button
            type="button"
            class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
            @click="selected = row"
          >
            <Icon name="eye" size="sm" />
            <span class="text-xs">{{ t('admin.pricing.defaults.details') }}</span>
          </button>
        </template>
        <template #empty
          ><p class="p-6 text-center text-sm text-gray-500">
            {{ t('admin.pricing.defaults.empty') }}
          </p></template
        >
      </DataTable>
    </template>
    <template #pagination>
      <Pagination
        :page="page"
        :page-size="pageSize"
        :total="total"
        @update:page="page = $event"
        @update:page-size="
          pageSize = $event;
          page = 1;
        "
      />
    </template>
  </TablePageLayout>
  <BaseDialog :show="!!selected" :title="selected?.model || ''" @close="selected = null">
    <div v-if="selected" class="space-y-4">
      <!-- Platform and billing badges use the same components as their table columns. -->
      <div class="flex flex-wrap items-center gap-2">
        <PlatformBadge :platform="selected.platform" />
        <BillingModeBadge :mode="selected.billing_mode" />
        <span v-if="selected.source" class="text-xs text-gray-500">{{
          t(`admin.modelAttributes.sources.${selected.source}`, selected.source)
        }}</span>
      </div>
      <p
        v-if="selected.price_status === 'unpriced'"
        class="text-sm text-gray-400 dark:text-dark-500"
      >
        {{ t('admin.pricing.defaults.unpriced') }}
      </p>
      <template v-else>
        <!-- Context and tier controls select independent price dimensions, and the rows below display their combined token quote selection. -->
        <div
          v-if="availableContexts.length > 1 || availableTiers.length > 1"
          class="flex flex-wrap items-center justify-end gap-2"
        >
          <div
            v-segmented
            v-if="availableContexts.length > 1"
            class="segmented max-w-full flex-wrap"
            data-testid="pricing-context-switch"
          >
            <button
              v-for="context in availableContexts"
              :key="context"
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold"
              :class="{ 'segmented-item-active': context === activeContext }"
              @click="selectedContext = context"
            >
              {{ contextRangeLabel(context) }}
            </button>
          </div>
          <div
            v-segmented
            v-if="availableTiers.length > 1"
            class="segmented max-w-full flex-wrap"
            data-testid="pricing-tier-switch"
          >
            <button
              v-for="tier in availableTiers"
              :key="tier"
              type="button"
              class="segmented-item px-2 py-0.5 text-xs font-semibold"
              :class="{ 'segmented-item-active': tier === activeTier }"
              @click="selectedTier = tier"
            >
              {{ t(`admin.pricing.defaults.tiers.${tier}`) }}
            </button>
          </div>
        </div>
        <!-- Price rows use muted labels, tabular numbers, and separators consistent with marketplace price cards. -->
        <dl class="space-y-2.5">
          <div
            v-for="price in activePrices"
            :key="price.key"
            class="flex items-baseline justify-between gap-3 border-b border-gray-100 pb-2 text-sm dark:border-dark-700"
          >
            <dt class="min-w-0 max-w-[45%] shrink-0 break-words text-gray-500 dark:text-dark-400">
              {{ priceLabel(price)
              }}<span
                v-if="selected.price_sources?.[price.key.replace(/^(?:fast_|flex_)/, '')]"
                class="ml-1 text-xs"
                >({{
                  t(
                    `admin.modelAttributes.sources.${selected.price_sources[price.key.replace(/^(?:fast_|flex_)/, '')]}`,
                  )
                }})</span
              >
            </dt>
            <dd
              class="min-w-0 break-words text-right font-medium tabular-nums [overflow-wrap:anywhere] text-gray-900 dark:text-white"
            >
              {{ formatPrice(price) }}
            </dd>
          </div>
        </dl>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { vSegmented } from '@/directives/segmented'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import {
  listDefaultPricing,
  updateDefaultPricing,
  type DefaultModelPrice,
  type DefaultPriceValue,
} from '@/api/admin/pricing'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import { BILLING_MODE_ICONS, withOptionIcons } from '@/components/common/filterOptionIcons'
import { platformIconClass } from '@/utils/platformColors'
import type { ProviderPlatform } from '@/api/admin/providers.types'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlatformBadge from '@/components/common/PlatformBadge.vue'
import BillingModeBadge from '@/components/common/BillingModeBadge.vue'
import ModelCatalogInfo from '@/components/admin/model-attributes/ModelCatalogInfo.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatCompactTokenRange } from '@/utils/formatters'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'

// DefaultPricingPanel.vue lists catalog prices and displays their context and service-tier
// dimensions. Token detail controls independently choose context and tier, while video details
// expose every resolution quote with its original label. Numeric interval selections address
// the catalog's explicit context table.
//
// List requests cancel their predecessors and reject aborted results before publication.
// Catalog updates have a separate controller and reload the current filters after success.
// Unmount marks the panel disposed and aborts both request kinds. Price-key parsing applies to
// token modifiers; media price labels can contain those same prefixes and therefore use their
// unit to retain the complete label.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t } = useI18n()
// items and selected hold the loaded catalog rows and detail target, while search, platform,
// and mode select the next query.
const items = ref<DefaultModelPrice[]>([])
const selected = ref<DefaultModelPrice | null>(null)
const search = ref('')
const platform = ref('')
const mode = ref('')
// page, pageSize, and total connect server pagination with the table controls used to browse
// filtered default prices.
const page = ref(1)
// pageSize sets the number of visible rows and participates in the current page calculation for
// pagination.
const pageSize = ref(20)
const total = ref(0)
// loading tracks the current list request so the table can present pending data to
// administrators.
const loading = ref(false)
// updating and notice describe the manual catalog refresh, separately from list loading and its
// request error state.
const updating = ref(false)
const notice = ref('')
// activeFilterCount counts nonempty platform and billing-mode selections for the filter button,
// excluding the free-text query.
const activeFilterCount = computed(() => Number(!!platform.value) + Number(!!mode.value))
// error and catalog metadata distinguish this request failure from the catalog version, last
// update, and stored refresh error.
const error = ref('')
const updatedAt = ref('')
const catalogVersion = ref('')
const catalogError = ref('')
// controller and updateController own list and catalog-update requests independently; disposed
// prevents new reads after component removal.
let controller: AbortController | undefined
let updateController: AbortController | undefined
let disposed = false
// platforms stores server-provided filter choices, while platformOptions and modeOptions add
// their translated unrestricted selections.
const platforms = ref<string[]>([])
const platformOptions = computed(() => [
  { value: '', label: t('admin.pricing.defaults.allPlatforms') },
  ...platforms.value.map((value) => ({
    value,
    label: t(`admin.groups.platforms.${value}`, value),
  })),
])
const modeOptions = computed(() =>
  withOptionIcons(
    [
      { value: '', label: t('admin.pricing.defaults.allModes') },
      ...['token', 'image', 'video', 'per_request'].map((value) => ({
        value,
        label: t(`admin.pricing.defaults.modes.${value}`),
      })),
    ],
    BILLING_MODE_ICONS,
  ),
)
// columns defines the displayed table fields and their translated headings, ordering, and
// sorting controls.
const columns = computed(() =>
  ['model', 'platform', 'billing_mode', 'price', 'actions'].map((key) => ({
    key,
    label: t(`admin.pricing.defaults.columns.${key}`),
  })),
)

// priceLabel displays media dimensions verbatim and translates token price bucket names. Video
// quotes use USD/s, which preserves custom labels even when they begin with token modifier
// prefixes.
function priceLabel(price: DefaultPriceValue): string {
  if (price.unit === 'USD/s') return price.key
  const stripped = price.key.replace(/^(?:long_)?(?:fast_|flex_)?/, '')
  return t(`admin.pricing.defaults.keys.${stripped}`, stripped)
}

// PricingContext identifies standard, long-context, or indexed catalog token intervals.
type PricingContext = 'standard' | 'long_context' | number
// PricingTier identifies the standard, Fast, or Flex price modifier.
type PricingTier = 'standard' | 'fast' | 'flex'
// selectedContext and selectedTier retain independent detail selections, which active values
// constrain to dimensions supported by the current row.
const selectedContext = ref<PricingContext>('standard')
const selectedTier = ref<PricingTier>('standard')
// comboPrefix constructs the catalog key prefix for the selected context and tier.
function comboPrefix(context: PricingContext, tier: PricingTier): string {
  return (
    (context === 'long_context' ? 'long_' : '') +
    (tier === 'fast' ? 'fast_' : tier === 'flex' ? 'flex_' : '')
  )
}
// comboOf extracts context and service tier from an encoded token price key. Long-context
// prefixes are removed before tier inspection, and missing recognized modifiers select the
// standard context or tier.
function comboOf(key: string): {
  context: PricingContext // selected token context
  tier: PricingTier // selected service tier
} {
  const context: PricingContext = key.startsWith('long_') ? 'long_context' : 'standard'
  const rest = context === 'long_context' ? key.slice('long_'.length) : key
  const tier: PricingTier = rest.startsWith('fast_')
    ? 'fast'
    : rest.startsWith('flex_')
      ? 'flex'
      : 'standard'
  return { context, tier }
}

// availableContexts lists token interval indexes or standard and long contexts, while video
// prices remain in one resolution view.
const availableContexts = computed<PricingContext[]>(() => {
  if (selected.value?.billing_mode === 'video') return ['standard']
  if (selected.value?.context_intervals?.length)
    return selected.value.context_intervals.map((_, index) => index)
  if (!selected.value?.prices.some((price) => comboOf(price.key).context === 'long_context'))
    return ['standard']
  return ['standard', 'long_context']
})

// availableTiers derives supported token service tiers from price keys, while video details
// keep the standard tier selection.
const availableTiers = computed<PricingTier[]>(() => {
  if (!selected.value || selected.value.billing_mode === 'video') return ['standard']
  const present = new Set(selected.value.prices.map((price) => comboOf(price.key).tier))
  return (['standard', 'fast', 'flex'] as PricingTier[]).filter(
    (tier) => tier === 'standard' || present.has(tier),
  )
})
// activeContext and activeTier fall back to supported dimensions when a newly selected model
// lacks the previous detail choices.
const activeContext = computed<PricingContext>(() =>
  availableContexts.value.includes(selectedContext.value)
    ? selectedContext.value
    : (availableContexts.value[0] ?? 'standard'),
)
const activeTier = computed<PricingTier>(() =>
  availableTiers.value.includes(selectedTier.value) ? selectedTier.value : 'standard',
)

// contextRangeLabel formats an indexed token interval or the model's long-context threshold.
// Missing indexed intervals return empty text, and named context choices use the shared compact
// token-range formatter.
function contextRangeLabel(context: PricingContext): string {
  if (typeof context === 'number') {
    const interval = selected.value?.context_intervals?.[context]
    return interval ? formatCompactTokenRange(interval.min_tokens, interval.max_tokens) : ''
  }
  const threshold = selected.value?.long_context_threshold ?? 0
  return context === 'long_context'
    ? formatCompactTokenRange(threshold, null)
    : formatCompactTokenRange(0, threshold)
}

// activePrices combines the active token context and tier, while video details return every
// resolution quote with its label intact.
const activePrices = computed(() => {
  if (selected.value?.billing_mode === 'video') return selected.value.prices
  const prefix = comboPrefix(activeContext.value, activeTier.value)
  const prices =
    typeof activeContext.value === 'number'
      ? selected.value?.context_intervals?.[activeContext.value]?.prices
      : selected.value?.prices
  return (prices ?? []).filter((price) => {
    if (!price.key.startsWith(prefix)) return false
    // A longer modifier prefix belongs to another context or tier, so exclude it after
    // stripping the selected prefix from the key.
    return !/^(?:long_|fast_|flex_)/.test(price.key.slice(prefix.length))
  })
})
// A new detail target resets both selectors before active dimensions are derived, preventing
// another model's previous context or tier from controlling the dialog.
watch(selected, () => {
  selectedContext.value = 'standard'
  selectedTier.value = 'standard'
})
// formatPrice formats a catalog amount and its display unit for the detail table. Null amounts
// use the not-applicable label, multipliers use the multiplication sign, and numbers display at
// most eight significant digits.
function formatPrice(price: DefaultPriceValue): string {
  if (price.value == null) return t('admin.pricing.defaults.notApplicable')
  const unit = price.unit === 'multiplier' ? '×' : price.unit
  return `${price.value.toLocaleString(undefined, { maximumSignificantDigits: 8 })} ${unit}`
}
// load fetches the current catalog price page and supersedes the preceding list request. It
// aborts the old controller before allocating a new one, then publishes rows, totals, and
// catalog metadata only while that request remains active. Aborted requests also leave the
// current error and loading state alone.
//
// A disposed panel skips new reads. Filters and pagination are read at invocation, while the
// shared update action can call this function after persistence to refresh that same selection.
async function load() {
  if (disposed) return
  // Cancel the preceding list read before registering its replacement so a late response
  // cannot publish rows for an older filter selection.
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = ''
  try {
    const result = await listDefaultPricing(
      {
        page: page.value,
        page_size: pageSize.value,
        search: search.value,
        platform: platform.value,
        billing_mode: mode.value,
      },
      current.signal,
    )
    // An aborted response belongs to a superseded query, so leave rows and catalog metadata
    // owned by the active request untouched.
    if (current.signal.aborted) return
    items.value = result.items
    total.value = result.total
    updatedAt.value = result.last_updated
    catalogVersion.value = result.version ?? ''
    catalogError.value = result.last_error ?? ''
    platforms.value = result.platforms ?? platforms.value
  } catch {
    if (!current.signal.aborted) error.value = t('admin.pricing.loadError')
  } finally {
    if (!current.signal.aborted) loading.value = false
  }
}

// updateCatalog starts one catalog refresh and then reloads the visible price page. A pending
// update suppresses repeated clicks, and the separate update controller lets unmount cancel
// mutation waiting independently from list reads.
//
// After completion, disposed or aborted updates skip notices and list reloads. Failures from an
// active panel become its update error, and finally releases the updating flag so the control
// is available after the attempt ends.
async function updateCatalog() {
  if (updating.value) return
  updating.value = true
  error.value = ''
  notice.value = ''
  const current = new AbortController()
  updateController = current
  try {
    await updateDefaultPricing(current.signal)
    if (disposed || current.signal.aborted) return
    notice.value = t('admin.pricing.defaults.updateSuccess')
    await load()
  } catch {
    if (!disposed && !current.signal.aborted) error.value = t('admin.pricing.defaults.updateError')
  } finally {
    updating.value = false
  }
}

// searchChanged debounces text queries and resets pagination, allowing the page watcher to
// issue one query when the page changes.
const searchChanged = useDebounceFn(() => {
  if (page.value !== 1) page.value = 1
  else void load()
}, SEARCH_DEBOUNCE_MS)
watch(search, searchChanged)
watch([platform, mode], () => {
  if (page.value !== 1) page.value = 1
  else void load()
})
watch([page, pageSize], load, { immediate: true })
// Mark the panel disposed before aborting both controllers so delayed debounced callbacks
// cannot start another list request after component removal.
onBeforeUnmount(() => {
  disposed = true
  // After disposal blocks new list work, abort both active reads and updates to release their
  // requests when this panel is removed from the page.
  controller?.abort()
  updateController?.abort()
})
</script>
