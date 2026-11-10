<template>
  <component
    :is="isAuthenticated ? AppLayout : 'div'"
    :class="isAuthenticated ? '' : 'ba-theme-shell relative min-h-screen overflow-hidden'"
  >
    <template v-if="isAuthenticated" #page-heading-actions>
      <div
        class="model-marketplace-toolbar flex w-[calc(100vw-2rem)] max-w-full min-w-0 items-center gap-2 sm:w-auto"
      >
        <div class="min-w-0 flex-1 sm:w-80 sm:flex-none lg:w-[min(24rem,32vw)]">
          <SearchInput
            v-model="search"
            :placeholder="t('marketplace.searchPlaceholder')"
            :debounce-ms="120"
          />
        </div>

        <FilterDropdown
          :active-count="activeFilterCount"
          :columns="3"
          keep-mounted
          @reset="resetPanelFilters"
        >
          <FilterField :label="t('marketplace.filterBrand')">
            <Select v-model="selectedBrand" :options="brandSelectOptions" />
          </FilterField>
          <FilterField :label="t('marketplace.filterPricingMode')">
            <Select v-model="selectedPricingMode" :options="pricingSelectOptions" />
          </FilterField>
          <FilterField :label="t('marketplace.filterGroup')">
            <Select v-model="selectedGroupId" :options="groupSelectOptions" searchable />
          </FilterField>
        </FilterDropdown>
      </div>
    </template>

    <template v-if="!isAuthenticated">
      <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

      <header class="site-header relative z-20 border-b border-primary-900/10 px-4 sm:px-6">
        <nav class="mx-auto flex h-[var(--header-h)] max-w-7xl items-center justify-between gap-4">
          <router-link to="/home" class="flex min-w-0 items-center gap-2.5">
            <span class="h-8 w-8 shrink-0 overflow-hidden rounded-control shadow-sm">
              <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
            </span>
            <span class="truncate text-base font-semibold text-gray-950 dark:text-white">{{
              siteName
            }}</span>
          </router-link>

          <div class="flex items-center gap-2 sm:gap-3">
            <div
              class="hidden items-center gap-5 text-sm font-medium text-gray-600 dark:text-dark-300 md:flex"
            >
              <router-link to="/models" class="text-link">
                {{ t('home.nav.models') }}
              </router-link>
              <a
                v-if="docUrl"
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="text-link"
              >
                {{ t('home.docs') }}
              </a>
            </div>

            <LocaleSwitcher />

            <button
              type="button"
              @click="toggleTheme"
              class="flex rounded-control text-primary-900/90 transition-colors hover:bg-primary-100 hover:text-primary-900 dark:text-dark-100/80 dark:hover:bg-dark-800 dark:hover:text-white btn-icon"
              :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            >
              <Icon v-if="isDark" name="sun" size="md" />
              <Icon v-else name="moon" size="md" />
            </button>

            <router-link
              to="/login"
              class="inline-flex items-center rounded-full bg-gray-950 px-4 py-2 text-xs font-semibold text-white transition hover:bg-gray-800 dark:bg-white dark:text-dark-950 dark:hover:bg-dark-200"
            >
              {{ t('home.login') }}
            </router-link>
          </div>
        </nav>
      </header>
    </template>

    <section
      v-content-reveal="!isAuthenticated && motionRoute?.path"
      :class="isAuthenticated ? 'space-y-4' : 'relative z-10 px-4 pb-12 pt-6 sm:px-6 lg:px-8'"
    >
      <div :class="isAuthenticated ? 'space-y-4' : 'relative mx-auto max-w-[1400px] space-y-5'">
        <div v-if="!isAuthenticated" class="page-heading mb-4">
          <h1 class="page-title">{{ t('marketplace.title') }}</h1>
          <p class="page-description">{{ t('marketplace.subtitle') }}</p>
        </div>
        <div v-if="!isAuthenticated" class="flex min-w-0 items-center gap-2">
          <div class="min-w-0 flex-1 sm:w-80 sm:flex-none xl:w-96">
            <SearchInput
              v-model="search"
              :placeholder="t('marketplace.searchPlaceholder')"
              :debounce-ms="120"
            />
          </div>

          <FilterDropdown
            :active-count="activeFilterCount"
            :columns="3"
            keep-mounted
            @reset="resetPanelFilters"
          >
            <FilterField :label="t('marketplace.filterBrand')">
              <Select v-model="selectedBrand" :options="brandSelectOptions" />
            </FilterField>
            <FilterField :label="t('marketplace.filterPricingMode')">
              <Select v-model="selectedPricingMode" :options="pricingSelectOptions" />
            </FilterField>
            <FilterField :label="t('marketplace.filterGroup')">
              <Select v-model="selectedGroupId" :options="groupSelectOptions" searchable />
            </FilterField>
          </FilterDropdown>
        </div>

        <ModelMarketplaceSkeleton v-if="loading" />

        <div v-else-if="errorMessage" class="card border-red-200 p-6 dark:border-red-500/30">
          <div class="flex flex-col gap-4 md:flex-row md:items-end md:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
                {{ t('common.error') }}
              </h2>
              <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">
                {{ errorMessage }}
              </p>
            </div>
            <button class="btn btn-primary" type="button" @click="fetchMarketplace">
              {{ t('common.refresh') }}
            </button>
          </div>
        </div>

        <div v-else-if="!hasMarketplaceResults" class="card px-6 py-14">
          <div
            class="mx-auto flex h-16 w-16 items-center justify-center rounded-surface bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-300"
          >
            <Icon name="inbox" size="xl" />
          </div>
          <h2 class="mt-6 text-center text-2xl font-semibold text-gray-950 dark:text-white">
            {{ t('marketplace.emptyTitle') }}
          </h2>
          <p
            class="mx-auto mt-3 max-w-xl text-center text-sm leading-7 text-gray-600 dark:text-dark-300"
          >
            {{ t('marketplace.emptyDescription') }}
          </p>
          <div class="mt-6 text-center">
            <button class="btn btn-secondary" type="button" @click="resetFilters">
              {{ t('common.reset') }}
            </button>
          </div>
        </div>

        <div v-else class="space-y-4">
          <section
            v-for="group in filteredGroups"
            :key="group.id"
            class="card overflow-hidden"
            data-testid="marketplace-group-section"
          >
            <div
              class="card-header flex flex-col gap-4 px-4 py-4 md:px-5 xl:flex-row xl:items-center xl:justify-between"
            >
              <div class="min-w-0 flex-1 space-y-3">
                <div class="flex flex-wrap items-center gap-2">
                  <span
                    v-if="group.is_exclusive"
                    data-testid="group-exclusive-tag"
                    class="marketplace-exclusive-tag inline-flex items-center gap-1 rounded-full border px-3 py-1 text-xs font-semibold"
                  >
                    <Icon name="crown" size="sm" :animate-on-hover="false" />
                    {{ t('marketplace.exclusive') }}
                  </span>
                  <span :class="brandBadgeClass(group)">
                    <ProviderIcon :brand="groupBrandSource(group)" size="14px" />
                    {{ groupBrandLabel(group) }}
                  </span>
                  <!-- A group can hide its discount tag; visible discounts require a valid ratio and offer click or hover explanations. -->
                  <HelpTooltip
                    v-if="
                      !group.hide_max_discount && formatMaxDiscountOff(group.official_price_ratio)
                    "
                    trigger="both"
                    width-class="w-72"
                    :closable="false"
                    :content="t('marketplace.maxDiscountHint')"
                  >
                    <template #trigger>
                      <span
                        data-testid="group-max-discount-tag"
                        class="rounded-full border border-emerald-200 bg-emerald-50 px-3 py-1 text-xs font-semibold text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-500/10 dark:text-emerald-200"
                      >
                        {{ formatMaxDiscountOff(group.official_price_ratio) }}
                      </span>
                    </template>
                  </HelpTooltip>
                  <HelpTooltip
                    v-if="!group.hide_rate_multiplier"
                    trigger="both"
                    width-class="w-72"
                    :closable="false"
                    :content="t('marketplace.rateMultiplierHint')"
                  >
                    <template #trigger>
                      <!-- The multiplier uses a neutral fill that remains distinct from the surrounding card in dark mode. -->
                      <span
                        data-testid="group-rate-multiplier-tag"
                        class="rounded-full border border-gray-200/80 bg-gray-50 px-3 py-1 text-xs font-semibold text-gray-600 dark:border-dark-600 dark:bg-dark-700/60 dark:text-dark-200"
                      >
                        {{ formatRateMultiplierLabel(group.rate_multiplier) }}
                      </span>
                    </template>
                  </HelpTooltip>
                </div>

                <div class="flex items-start gap-3">
                  <span
                    class="mt-0.5 flex h-12 w-12 shrink-0 items-center justify-center rounded-surface border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-950"
                  >
                    <ProviderIcon :brand="groupBrandSource(group)" size="28px" />
                  </span>
                  <div class="min-w-0">
                    <h2 class="text-lg font-semibold text-gray-950 dark:text-white">
                      {{ group.name }}
                    </h2>
                    <p
                      v-if="group.description"
                      class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300"
                    >
                      {{ group.description }}
                    </p>
                  </div>
                </div>
              </div>
              <div
                v-if="group.availability"
                class="w-full xl:ml-6 xl:w-[560px] xl:shrink-0"
                data-testid="marketplace-group-availability"
              >
                <!-- Public group headers expose availability in the trailing region without adding administrator capacity controls. -->
                <GroupAvailabilityBar :availability="group.availability" class="min-w-0" />
              </div>
            </div>

            <div
              class="grid min-w-0 grid-cols-1 items-start gap-4 p-4 md:grid-cols-2 lg:grid-cols-3 md:p-5"
            >
              <!-- Shrinkable cards wrap long pricing content in one narrow-screen column and expand to the desktop grid. -->
              <article
                v-for="model in group.models"
                :key="`${group.id}-${model.id}`"
                class="group min-w-0 max-w-full rounded-surface border border-gray-100 bg-gray-50/80 p-4 transition-colors hover:border-black/20 dark:border-dark-700 dark:bg-dark-950/80 dark:hover:border-primary-500/50"
              >
                <div class="flex min-w-0 flex-wrap items-start justify-between gap-2">
                  <div class="flex min-w-0 flex-1 basis-32 items-center">
                    <h3
                      class="min-w-0 truncate text-base font-semibold text-gray-950 dark:text-white"
                    >
                      {{ model.display_name }}
                    </h3>
                    <!-- Keep attribute and protocol details beside the title, opening a floating panel without expanding card height. -->
                    <ModelAttributeDetails
                      v-if="model.attributes || marketplaceProtocols(model.protocols).length > 0"
                      :model="model"
                      class="shrink-0"
                    />
                  </div>
                  <ModelCapabilityTags :model="model" />
                </div>
                <!-- Give the identifier its own row while capability icons remain beside the model title. -->
                <ModelIdLabel :model-id="model.id" class="mt-1" />

                <!-- Compact quotes stay visible above the independently expandable full pricing panel. -->
                <div class="mt-4">
                  <template v-if="compactPricingRows(model.pricing).length > 0">
                    <dl class="space-y-2">
                      <div
                        v-for="row in compactPricingRows(model.pricing)"
                        :key="row.key"
                        class="flex items-baseline justify-between gap-3 text-sm"
                      >
                        <dt class="shrink-0 text-gray-500 dark:text-dark-400">{{ row.label }}</dt>
                        <dd
                          class="min-w-0 break-words text-right font-medium tabular-nums [overflow-wrap:anywhere] text-gray-900 dark:text-white"
                        >
                          {{ row.value }}
                        </dd>
                      </div>
                    </dl>
                  </template>
                  <p v-else class="text-sm text-gray-400 dark:text-dark-500">
                    {{ t('marketplace.pricingUnavailable') }}
                  </p>

                  <!-- The child panel owns disclosure and quote selectors while this page supplies the authorized model. -->
                  <ModelPricingPanel :model="model" />
                </div>
              </article>
            </div>
          </section>
        </div>
      </div>
    </section>
  </component>
</template>

<script setup lang="ts">
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import { vContentReveal } from '@/directives/contentReveal'
import { useRoute as useMotionRoute } from 'vue-router'

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelMarketplaceSkeleton from '@/components/marketplace/ModelMarketplaceSkeleton.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import GroupAvailabilityBar from '@/components/marketplace/GroupAvailabilityBar.vue'
import ModelCapabilityTags from '@/components/marketplace/ModelCapabilityTags.vue'
import ModelAttributeDetails from '@/components/marketplace/ModelAttributeDetails.vue'
import ModelPricingPanel from '@/components/marketplace/ModelPricingPanel.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import ModelIdLabel from '@/components/common/ModelIdLabel.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { initTheme, useTheme } from '@/composables/useTheme'
import { getAvailableMarketplaceModels, getMarketplaceModels } from '@/api/marketplace'
import {
  providerBrandDisplayName,
  providerBrandFilterKey,
  resolveProviderBrand,
} from '@/utils/providerBrand'
import { formatCompactTokenRange } from '@/utils/formatters'
import { formatPriceNumber, pricingKind, videoPricingEntries } from '@/utils/marketplacePricing'
import { marketplaceProtocols } from '@/utils/marketplaceProtocols'
import { sanitizeUrl } from '@/utils/url'
import type {
  MarketplaceGroup,
  MarketplaceModelPricing,
  MarketplacePricingInterval,
} from '@/api/marketplace.types'

import { useAppStore, useAuthStore } from '@/stores'

// ModelMarketplaceView.vue displays authorized model groups with searchable pricing
// and capability details. Authenticated visitors use the application layout,
// while guests receive the public header and theme controls. Filtering copies
// matching groups and models for display without modifying the loaded response.
// Child panels own protocol selection and expanded quote controls.
//
// Compact summaries distinguish token context intervals, image resolutions, and
// named video prices using the server's group-scaled quote. Initialization restores
// theme and account state, awaits missing public settings, then loads the catalog.
// Locale and account changes request another catalog load. Request generations
// and captured account identity reject stale responses, including errors and
// loading completion. Account changes clear the previous catalog synchronously,
// and unmounting invalidates outstanding requests. Exclusive groups appear first
// with an inline exclusive tag; all filters operate on that same authorized response.

// motionRoute supplies the route identity used by the public page's reveal directive.
const motionRoute = useMotionRoute()

// VisibleMarketplaceGroup retains the authorized group shape after client-side model filtering.
type VisibleMarketplaceGroup = MarketplaceGroup
// PricingFilter selects the public quote category shown in marketplace results.
type PricingFilter = 'all' | 'token' | 'image' | 'video' | 'unpriced'

// PricingRow pairs a stable summary identity with its translated category and amount.
interface PricingRow {
  key: string // Stable quote-row identity.
  label: string // Displayed price category.
  value: string // Formatted amount and units.
}

// Display services provide translations, balance units, cached branding, account
// state, and theme actions for the authenticated and public page variants.
const { t } = useI18n()
const { balanceUnitName } = useBalanceDisplay()

const appStore = useAppStore()
const authStore = useAuthStore()
const { isDark, toggleTheme } = useTheme()

// Loaded catalog state is separate from local search and select filters, allowing
// filter resets to reuse the response without making another API request.
const groups = ref<MarketplaceGroup[]>([])
const loading = ref(true)
const errorMessage = ref('')
const search = ref('')
const selectedBrand = ref<string | 'all'>('all')
const selectedPricingMode = ref<PricingFilter>('all')
const selectedGroupId = ref<number | 'all'>('all')

// Request lifecycle state belongs to this mounted page; generation invalidation
// prevents previous identities or locales from publishing late catalog responses.
let requestGeneration = 0
let catalogReady = false
let disposed = false

// Authentication selects the page shell and catalog; branding derives from injected or cached
// settings with filtered logo and documentation URLs before rendering external links.
const isAuthenticated = computed(() => authStore.isAuthenticated)
const catalogUserID = computed(() => (isAuthenticated.value ? (authStore.user?.id ?? null) : null))

const siteName = computed(() => appStore.siteName || 'TokenRouter')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true,
  }),
)
const docUrl = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''),
)

// Search normalization and the select-only active count feed the shared filter
// controls; the free-text query is intentionally absent from the select badge count.
const normalizedSearch = computed(() => search.value.trim().toLowerCase())
const activeFilterCount = computed(
  () =>
    [
      selectedBrand.value !== 'all' ? selectedBrand.value : '',
      selectedPricingMode.value !== 'all' ? selectedPricingMode.value : '',
      selectedGroupId.value !== 'all' ? selectedGroupId.value : '',
    ].filter(Boolean).length,
)

// Exclusive groups precede public groups, then use server order and ID ties.
// Brand choices deduplicate normalized labels while retaining that group order.
const sortedGroups = computed(() =>
  [...groups.value].sort((left, right) => {
    const exclusiveDiff = Number(Boolean(right.is_exclusive)) - Number(Boolean(left.is_exclusive))
    if (exclusiveDiff !== 0) {
      return exclusiveDiff
    }
    const sortDiff = (left.sort_order ?? 0) - (right.sort_order ?? 0)
    if (sortDiff !== 0) {
      return sortDiff
    }
    return left.id - right.id
  }),
)

const availableBrands = computed(() => {
  const seen = new Set<string>()
  const brands: string[] = []
  for (const group of sortedGroups.value) {
    const brand = groupBrandLabel(group)
    const key = brandKey(brand)
    if (seen.has(key)) {
      continue
    }
    seen.add(key)
    brands.push(brand)
  }
  return brands
})

// Filter choices retain their original values and append translated all-items
// entries; available group choices also preserve the server's additional search terms.
const brandSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allBrands') },
  ...availableBrands.value.map((brand) => ({
    value: brand,
    label: brand,
  })),
])

// pricingSelectOptions includes explicitly unpriced records alongside the three supported
// billing units so missing quotes remain discoverable through the same filter control.
const pricingSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allTypes') },
  { value: 'token', label: t('marketplace.tokenPricing') },
  { value: 'image', label: t('marketplace.imagePricing') },
  { value: 'video', label: t('marketplace.videoPricing') },
  { value: 'unpriced', label: t('marketplace.unpriced') },
])

const groupSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allGroups') },
  ...sortedGroups.value.map((group) => ({
    value: group.id,
    label: group.name,
    search_terms: group.search_terms,
  })),
])

// filteredGroups applies group restrictions before matching model pricing and text.
// A group-level text match retains every model passing the pricing filter; otherwise
// each model must match its own identifiers or attribute search terms. Empty
// groups disappear, and retained groups receive a copied, filtered model count.
const filteredGroups = computed<VisibleMarketplaceGroup[]>(() => {
  const keyword = normalizedSearch.value

  return sortedGroups.value.flatMap((group) => {
    if (
      selectedBrand.value !== 'all' &&
      brandKey(groupBrandLabel(group)) !== brandKey(selectedBrand.value)
    ) {
      return []
    }

    if (selectedGroupId.value !== 'all' && group.id !== selectedGroupId.value) {
      return []
    }

    const groupMatchesKeyword =
      !keyword ||
      [
        group.name,
        group.description,
        groupBrandSource(group),
        groupBrandLabel(group),
        ...(group.search_terms || []),
      ]
        .filter(Boolean)
        .some((value) => value.toLowerCase().includes(keyword))

    const models = group.models.filter((model) => {
      if (
        selectedPricingMode.value !== 'all' &&
        pricingKind(model.pricing) !== selectedPricingMode.value
      ) {
        return false
      }

      if (!keyword || groupMatchesKeyword) {
        return true
      }

      return [model.id, model.display_name, ...(model.attributes?.search_terms || [])].some(
        (value) => value.toLowerCase().includes(keyword),
      )
    })

    if (models.length === 0) {
      return []
    }

    return [
      {
        ...group,
        model_count: models.length,
        models,
      },
    ]
  })
})

// hasMarketplaceResults selects the populated versus empty display after loading
// and error presentation have taken precedence in the page template.
const hasMarketplaceResults = computed(() => filteredGroups.value.length > 0)

// hasPositiveValue accepts numeric values greater than zero without a finiteness check.
function hasPositiveValue(value?: number | null): value is number {
  return typeof value === 'number' && value > 0
}

// hasContextIntervalPricing reports whether the quote contains any returned context ranges.
function hasContextIntervalPricing(pricing: MarketplaceModelPricing): boolean {
  // Returned ranges remain visible even when their zero-valued price fields were
  // omitted; this check does not require a positive price within each interval.
  return (pricing.context_intervals?.length ?? 0) > 0
}

// resetPanelFilters clears select filters while retaining the current text query.
function resetPanelFilters() {
  selectedBrand.value = 'all'
  selectedPricingMode.value = 'all'
  selectedGroupId.value = 'all'
}

// resetFilters clears the text query and every select filter for the empty-state action.
function resetFilters() {
  search.value = ''
  resetPanelFilters()
}

// formatMultiplier prefixes the multiplier and uses two decimals only for fractional values.
function formatMultiplier(multiplier: number): string {
  return `x${multiplier.toFixed(multiplier % 1 === 0 ? 0 : 2)}`
}

// formatRateMultiplierLabel lets translation messages position the formatted multiplier text.
function formatRateMultiplierLabel(multiplier: number): string {
  return t('marketplace.rateMultiplierValue', { multiplier: formatMultiplier(multiplier) })
}

// formatMaxDiscountOff formats savings for a finite positive ratio below one.
// Missing, invalid, or undiscounted ratios return null and hide the label. Percentage
// formatting uses the browser locale before the translated explanation is assembled.
function formatMaxDiscountOff(ratio?: number): string | null {
  if (typeof ratio !== 'number' || !Number.isFinite(ratio) || ratio <= 0 || ratio >= 1) {
    return null
  }
  const percent = new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
  }).format((1 - ratio) * 100)
  return t('marketplace.maxDiscountOff', { percent })
}

// formatPrice appends the selected internal balance unit to a formatted amount.
function formatPrice(value: number): string {
  return `${formatPriceNumber(value)} ${balanceUnitName.value}`
}

// formatPerMillion converts per-token amounts into the displayed million-token billing unit.
function formatPerMillion(value: number): string {
  return `${formatPrice(value * 1_000_000)} ${t('usage.perMillionTokens')}`
}

// formatCompactPerMillion scales token prices without adding the balance or billing labels.
function formatCompactPerMillion(value: number): string {
  return formatPriceNumber(value * 1_000_000)
}

// formatPerImage appends the translated per-image unit to the formatted balance amount.
function formatPerImage(value: number): string {
  return `${formatPrice(value)} ${t('marketplace.perImage')}`
}

// groupBrandSource selects the group's source text for brand labels and artwork.
// A nonblank display_brand wins after trimming; otherwise the group name is
// returned as supplied, including its surrounding whitespace.
function groupBrandSource(group: Pick<MarketplaceGroup, 'display_brand' | 'name'>): string {
  return group.display_brand?.trim() || group.name
}

// groupBrandLabel resolves the group's selected brand source into its shared display name.
function groupBrandLabel(group: Pick<MarketplaceGroup, 'display_brand' | 'name'>): string {
  return providerBrandDisplayName(groupBrandSource(group))
}

// brandKey delegates brand normalization to the shared marketplace filtering helper.
function brandKey(label: string): string {
  return providerBrandFilterKey(label)
}

// brandBadgeClass combines the common badge layout with the group's resolved brand palette.
function brandBadgeClass(group: MarketplaceGroup): string {
  const base =
    'inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-semibold ring-1 ring-inset'
  return `${base} ${resolveProviderBrand(groupBrandSource(group)).badgeClass}`
}

// tokenPricingRowsFromValues collects positive standard and fast token-price categories.
// Each row retains its category-specific key and the complete million-token unit.
// Callers choose compact subsets or zero fallbacks from this ordered result.
function tokenPricingRowsFromValues(
  pricing: MarketplaceModelPricing | MarketplacePricingInterval,
): PricingRow[] {
  const rows: PricingRow[] = []

  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({
      key: 'input',
      label: t('marketplace.input'),
      value: formatPerMillion(pricing.input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.image_input_price_per_token)) {
    rows.push({
      key: 'image_input',
      label: t('marketplace.imageInput'),
      value: formatPerMillion(pricing.image_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({
      key: 'output',
      label: t('marketplace.output'),
      value: formatPerMillion(pricing.output_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_write_price_per_token)) {
    rows.push({
      key: 'cache_write',
      label: t('marketplace.cacheWrite'),
      value: formatPerMillion(pricing.cache_write_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_write_1h_price_per_token)) {
    rows.push({
      key: 'cache_write_1h',
      label: t('marketplace.cacheWrite1h'),
      value: formatPerMillion(pricing.cache_write_1h_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.cache_read_price_per_token)) {
    rows.push({
      key: 'cache_read',
      label: t('marketplace.cacheRead'),
      value: formatPerMillion(pricing.cache_read_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.image_output_price_per_token)) {
    rows.push({
      key: 'image_output',
      label: t('marketplace.imageOutput'),
      value: formatPerMillion(pricing.image_output_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_input_price_per_token)) {
    rows.push({
      key: 'fast_input',
      label: t('marketplace.fastInput'),
      value: formatPerMillion(pricing.fast_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_image_input_price_per_token)) {
    rows.push({
      key: 'fast_image_input',
      label: t('marketplace.fastImageInput'),
      value: formatPerMillion(pricing.fast_image_input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_output_price_per_token)) {
    rows.push({
      key: 'fast_output',
      label: t('marketplace.fastOutput'),
      value: formatPerMillion(pricing.fast_output_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_write_price_per_token)) {
    rows.push({
      key: 'fast_cache_write',
      label: t('marketplace.fastCacheWrite'),
      value: formatPerMillion(pricing.fast_cache_write_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_write_1h_price_per_token)) {
    rows.push({
      key: 'fast_cache_write_1h',
      label: t('marketplace.fastCacheWrite1h'),
      value: formatPerMillion(pricing.fast_cache_write_1h_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_cache_read_price_per_token)) {
    rows.push({
      key: 'fast_cache_read',
      label: t('marketplace.fastCacheRead'),
      value: formatPerMillion(pricing.fast_cache_read_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.fast_image_output_price_per_token)) {
    rows.push({
      key: 'fast_image_output',
      label: t('marketplace.fastImageOutput'),
      value: formatPerMillion(pricing.fast_image_output_price_per_token),
    })
  }

  return rows
}

// compactTokenPricingRows prefers ordinary input and output prices for card summaries.
// When absent, it uses at most two other standard categories. A completely empty
// price list receives two zero-priced input and output rows; a fast-only list
// has no standard fallback rows.
function compactTokenPricingRows(
  pricing: MarketplaceModelPricing | MarketplacePricingInterval,
): PricingRow[] {
  const primaryRows: PricingRow[] = []
  if (hasPositiveValue(pricing.input_price_per_token)) {
    primaryRows.push({
      key: 'input',
      label: t('marketplace.input'),
      value: formatPerMillion(pricing.input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    primaryRows.push({
      key: 'output',
      label: t('marketplace.output'),
      value: formatPerMillion(pricing.output_price_per_token),
    })
  }
  if (primaryRows.length > 0) {
    return primaryRows
  }

  // Inspect all price categories after ordinary input/output are absent so a
  // completely empty quote can show free input/output prices while fast-only
  // quotes stay distinct.
  const rows = tokenPricingRowsFromValues(pricing)
  if (rows.length === 0) {
    return zeroTokenPricingRows()
  }

  // Use at most two standard alternatives for the card summary; fast categories
  // belong to the expanded speed selector and cannot replace standard summary prices.
  return rows.filter((row) => !row.key.startsWith('fast_')).slice(0, 2)
}

// zeroTokenPricingRows formats the input and output categories with explicit free amounts.
function zeroTokenPricingRows(): PricingRow[] {
  return [
    { key: 'input', label: t('marketplace.input'), value: formatPerMillion(0) },
    { key: 'output', label: t('marketplace.output'), value: formatPerMillion(0) },
  ]
}

// compactContextIntervalRows retains each returned context range as a summary row.
// Available price categories are joined within that range; an interval without
// standard prices uses the existing zero fallback with its full unit labels.
function compactContextIntervalRows(pricing: MarketplaceModelPricing): PricingRow[] {
  return (
    pricing.context_intervals?.flatMap((interval, index) => {
      const pricedRows = compactIntervalTokenPricingRows(interval)
      // Keep an interval visible when it has no standard summary categories by
      // supplying free input/output rows, including intervals containing only fast prices.
      const rows = pricedRows.length > 0 ? pricedRows : zeroTokenPricingRows()
      return [
        {
          key: `compact-${interval.min_tokens}-${interval.max_tokens ?? 'up'}-${index}`,
          label: formatCompactTokenRange(interval.min_tokens, interval.max_tokens),
          value: rows.map((row) => `${row.label} ${row.value}`).join(' / '),
        },
      ]
    }) ?? []
  )
}

// compactIntervalTokenPricingRows prefers bare input and output amounts for context summaries.
// Otherwise it takes at most two non-fast categories from the complete formatter,
// retaining that fallback's full unit text rather than adding a second label.
function compactIntervalTokenPricingRows(pricing: MarketplacePricingInterval): PricingRow[] {
  const rows: PricingRow[] = []
  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({
      key: 'input',
      label: t('marketplace.input'),
      value: formatCompactPerMillion(pricing.input_price_per_token),
    })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({
      key: 'output',
      label: t('marketplace.output'),
      value: formatCompactPerMillion(pricing.output_price_per_token),
    })
  }
  if (rows.length > 0) {
    return rows
  }

  // Without ordinary input/output prices, retain up to two other standard
  // categories with their full unit labels; the interval caller supplies free rows
  // if none remain.
  return tokenPricingRowsFromValues(pricing)
    .filter((row) => !row.key.startsWith('fast_'))
    .slice(0, 2)
}

/**
 * compactPricingRows selects summary rows with units matching the public quote.
 * Unpriced models return no rows. Token context ranges use compact summaries,
 * while image and video models expose their available unit prices.
 */
function compactPricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  const kind = pricingKind(pricing)
  if (kind === 'token' && hasContextIntervalPricing(pricing)) {
    return compactContextIntervalRows(pricing)
  }
  if (kind === 'token') {
    return compactTokenPricingRows(pricing)
  }
  if (kind === 'image') {
    return imagePricingRows(pricing)
  }
  if (kind === 'video') {
    return videoPricingRows(pricing)
  }
  return []
}

/**
 * videoPricingRows formats video dimensions using the displayed balance unit.
 * Named dimensions use their supplied labels; the explicit default uses a
 * translated label, and every row retains the per-second billing unit.
 */
function videoPricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  return videoPricingEntries(pricing).map((item) => ({
    key: item.key,
    label: item.resolution ?? t('marketplace.defaultVideoPrice'),
    value: `${formatPrice(item.price)} ${t('marketplace.perSecond')}`,
  }))
}

// imagePricingRows emits the declared finite, nonnegative image-resolution prices.
// Explicit zero values remain visible as free tiers. Missing, negative, and
// nonfinite values are omitted while the standard resolution order remains unchanged.
function imagePricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  const values = [
    { key: '1k', label: '1K', price: pricing.image_price_1k },
    { key: '2k', label: '2K', price: pricing.image_price_2k },
    { key: '4k', label: '4K', price: pricing.image_price_4k },
  ]

  return values.flatMap((item) => {
    if (typeof item.price !== 'number' || !Number.isFinite(item.price) || item.price < 0) {
      return []
    }

    return [
      {
        key: item.key,
        label: item.label,
        value: formatPerImage(item.price),
      },
    ]
  })
}

// fetchMarketplace loads the catalog for the current authenticated identity.
// Each request captures its generation and user before awaiting the API. A newer
// request, account change, or unmount prevents that completion from publishing
// data, errors, or loading state. Same-account refresh failures retain previous
// data behind the error view; successful refreshes reset unavailable group filters.
//
// Public and personal endpoints remain distinct so homepage behavior stays public.
// Request completion checks identity as well as generation because authentication
// can change while the shared API client refreshes a token or retries a request.
async function fetchMarketplace() {
  if (disposed) return
  const generation = ++requestGeneration
  const userID = catalogUserID.value
  const authenticated = isAuthenticated.value
  loading.value = true
  errorMessage.value = ''

  // isCurrent checks all ownership boundaries before a pending request publishes state.
  const isCurrent = () =>
    !disposed &&
    generation === requestGeneration &&
    userID === catalogUserID.value &&
    authenticated === isAuthenticated.value

  try {
    const result = await (authenticated ? getAvailableMarketplaceModels() : getMarketplaceModels())
    if (!isCurrent()) return
    groups.value = result
    if (
      selectedGroupId.value !== 'all' &&
      !result.some((group) => group.id === selectedGroupId.value)
    ) {
      selectedGroupId.value = 'all'
    }
  } catch (error) {
    if (!isCurrent()) return
    console.error('Failed to load marketplace models:', error)
    errorMessage.value =
      typeof error === 'object' && error !== null && 'message' in error
        ? String(error.message)
        : t('common.unknownError')
  } finally {
    if (isCurrent()) loading.value = false
  }
}

// Clear personal data before the next render when identity changes. Synchronous
// invalidation also prevents an already queued response from restoring the old catalog.
watch(
  [isAuthenticated, catalogUserID],
  () => {
    requestGeneration++
    groups.value = []
    errorMessage.value = ''
    loading.value = true
    if (catalogReady) void fetchMarketplace()
  },
  { flush: 'sync' },
)

// Invalidate every pending completion before unmount; queued locale refresh callbacks
// also check disposal and cannot start another request for this retired page instance.
onBeforeUnmount(() => {
  disposed = true
  requestGeneration++
})

// Restore theme and account state before loading missing public settings, then
// request model groups so branding is available for the initial public presentation.
onMounted(async () => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    await appStore.fetchPublicSettings()
  }
  if (disposed) return
  catalogReady = true
  await fetchMarketplace()
})

// Language changes reload localized catalog records while preserving the page's
// current search and select filters through the shared locale-refresh subscription.
useLocaleRefresh(fetchMarketplace)
</script>

<style scoped>
/*
 * The exclusive badge uses a muted gold palette distinct from warning amber.
 * Its crown and text inherit one foreground, while theme-specific surfaces keep
 * the label readable beside the existing brand and pricing tags.
 */
.marketplace-exclusive-tag {
  border-color: #dfcea3;
  background-color: #f7f2e5;
  color: #7c6128;
}

.dark .marketplace-exclusive-tag {
  border-color: #594b2c;
  background-color: #29251c;
  color: #d6bd7b;
}
</style>
