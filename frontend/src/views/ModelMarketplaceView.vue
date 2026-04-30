<template>
  <component
    :is="isAuthenticated ? AppLayout : 'div'"
    :class="isAuthenticated ? '' : 'ba-theme-shell relative min-h-screen overflow-hidden'"
  >
    <template v-if="!isAuthenticated">
      <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

      <header class="relative z-20 border-b border-primary-200/70 bg-white/75 backdrop-blur-xl dark:border-dark-600/70 dark:bg-dark-700/95">
        <nav class="mx-auto flex max-w-[1400px] items-center justify-between gap-4 px-4 py-5 sm:px-6 lg:px-8">
          <RouterLink to="/home" class="flex min-w-0 items-center gap-3">
            <div class="h-11 w-11 overflow-hidden rounded-2xl border border-primary-200/70 bg-white shadow-md dark:border-dark-600 dark:bg-dark-900">
              <img :src="siteLogo || '/logo.png'" alt="Logo" class="h-full w-full object-contain" />
            </div>
            <div class="min-w-0">
              <div class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ siteName }}</div>
              <div class="truncate text-xs text-gray-500 dark:text-dark-400">{{ t('marketplace.title') }}</div>
            </div>
          </RouterLink>

          <div class="flex items-center gap-3">
            <LocaleSwitcher />
            <a
              v-if="docUrl"
              :href="docUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="rounded-full border border-primary-200/80 bg-white/80 px-4 py-2 text-sm font-medium text-primary-900 shadow-sm backdrop-blur transition hover:border-primary-300 hover:text-primary-700 dark:border-dark-600 dark:bg-dark-900/80 dark:text-dark-100 dark:hover:border-primary-500"
            >
              {{ t('home.docs') }}
            </a>
            <RouterLink
              to="/home"
              class="rounded-full border border-primary-200/80 bg-white/80 px-4 py-2 text-sm font-medium text-primary-900 shadow-sm backdrop-blur transition hover:border-primary-300 hover:text-primary-700 dark:border-dark-600 dark:bg-dark-900/80 dark:text-dark-100 dark:hover:border-primary-500"
            >
              {{ t('marketplace.backHome') }}
            </RouterLink>
            <RouterLink
              :to="dashboardPath"
              class="rounded-full bg-primary-900 px-4 py-2 text-sm font-medium text-white transition hover:bg-primary-800 dark:bg-primary-100 dark:text-dark-950 dark:hover:bg-white"
            >
              {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
            </RouterLink>
          </div>
        </nav>
      </header>
    </template>

    <section
      :class="isAuthenticated
        ? 'space-y-4'
        : 'relative z-10 px-4 pb-12 pt-6 sm:px-6 lg:px-8'"
    >
      <div :class="isAuthenticated ? 'space-y-4' : 'relative mx-auto max-w-[1400px] space-y-5'">
        <section class="card overflow-hidden p-4 md:p-5">
          <div class="grid gap-4 xl:grid-cols-[minmax(0,1.45fr)_repeat(3,minmax(0,210px))]">
            <div class="min-w-0 space-y-3">
              <div>
                <h1 class="text-2xl font-bold tracking-tight text-gray-950 dark:text-white">
                  {{ t('marketplace.title') }}
                </h1>
                <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-600 dark:text-dark-300">
                  {{ t('marketplace.subtitle') }}
                </p>
              </div>

              <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
                <span class="rounded-full bg-gray-100 px-3 py-1.5 dark:bg-dark-950">
                  {{ t('marketplace.actualPricingNote', { unitName: balanceUnitName }) }}
                </span>
                <span class="rounded-full bg-gray-100 px-3 py-1.5 dark:bg-dark-950">
                  {{ totalGroupCount }} {{ t('marketplace.groupsStat') }}
                </span>
                <span class="rounded-full bg-gray-100 px-3 py-1.5 dark:bg-dark-950">
                  {{ totalModelCount }} {{ t('marketplace.modelsStat') }}
                </span>
              </div>
            </div>

            <div
              v-for="card in overviewCards"
              :key="card.key"
              class="rounded-xl border border-gray-100 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-950/80"
            >
              <div class="flex items-start gap-3">
                <div class="rounded-lg p-2" :class="overviewIconWrapClass(card.key)">
                  <Icon :name="card.icon" size="md" :class="overviewIconClass(card.key)" />
                </div>
                <div class="min-w-0">
                  <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                    {{ card.label }}
                  </p>
                  <p class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">
                    {{ card.value }}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-[280px] flex-1 xl:max-w-[420px]">
            <SearchInput
              v-model="search"
              :placeholder="t('marketplace.searchPlaceholder')"
              :debounce-ms="120"
            />
          </div>

          <div class="w-full sm:w-[200px] xl:w-[180px]">
            <Select v-model="selectedBrand" :options="brandSelectOptions" />
          </div>

          <div class="w-full sm:w-[200px] xl:w-[180px]">
            <Select v-model="selectedPricingMode" :options="pricingSelectOptions" />
          </div>

          <div class="w-full sm:w-[220px] xl:w-[220px]">
            <Select v-model="selectedGroupId" :options="groupSelectOptions" searchable />
          </div>
        </div>

        <div v-if="loading" class="card px-6 py-14 text-center">
          <LoadingSpinner size="lg" />
          <p class="mt-4 text-sm text-gray-500 dark:text-dark-400">{{ t('common.loading') }}</p>
        </div>

        <div v-else-if="errorMessage" class="card border-red-200 p-6 dark:border-red-500/30">
          <div class="flex flex-col gap-4 md:flex-row md:items-center md:justify-between">
            <div>
              <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('common.error') }}</h2>
              <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-dark-300">{{ errorMessage }}</p>
            </div>
            <button class="btn btn-primary" type="button" @click="fetchMarketplace">
              {{ t('common.refresh') }}
            </button>
          </div>
        </div>

        <div v-else-if="filteredGroups.length === 0" class="card px-6 py-14">
          <div class="mx-auto flex h-16 w-16 items-center justify-center rounded-3xl bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-300">
            <Icon name="inbox" size="xl" />
          </div>
          <h2 class="mt-6 text-center text-2xl font-semibold text-gray-950 dark:text-white">{{ t('marketplace.emptyTitle') }}</h2>
          <p class="mx-auto mt-3 max-w-xl text-center text-sm leading-7 text-gray-600 dark:text-dark-300">
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
          >
            <div class="card-header px-4 py-4 md:px-5">
              <div class="min-w-0 space-y-3">
                <div class="flex flex-wrap items-center gap-2">
                  <span :class="brandBadgeClass(group)">
                    <ProviderIcon :brand="groupBrandSource(group)" size="14px" />
                    {{ groupBrandLabel(group) }}
                  </span>
                  <span class="rounded-full border border-gray-200 bg-gray-100 px-3 py-1 text-xs font-semibold text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200">
                    {{ t('marketplace.rateMultiplier') }} {{ formatMultiplier(group.rate_multiplier) }}
                  </span>
                  <span class="rounded-full border border-gray-200 bg-gray-100 px-3 py-1 text-xs font-semibold text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200">
                    {{ group.model_count }} {{ t('marketplace.modelsStat') }}
                  </span>
                  <span
                    v-if="hasOfficialPriceRatio(group.official_price_ratio)"
                    class="rounded-full border border-amber-200 bg-amber-50 px-3 py-1 text-xs font-semibold text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
                  >
                    {{ formatOfficialPriceRatio(group.official_price_ratio) }}
                  </span>
                </div>

                <div class="flex items-start gap-3">
                  <span
                    class="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded-lg ring-1 ring-inset"
                    :class="groupBrandIconWrapClass(group)"
                  >
                    <ProviderIcon :brand="groupBrandSource(group)" size="22px" />
                  </span>
                  <div class="min-w-0">
                    <h2 class="text-lg font-semibold text-gray-950 dark:text-white">{{ group.name }}</h2>
                    <p v-if="group.description" class="mt-1 text-sm leading-6 text-gray-600 dark:text-dark-300">
                      {{ group.description }}
                    </p>
                  </div>
                </div>
              </div>
            </div>

            <div class="grid gap-3 p-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4 md:p-5">
              <article
                v-for="model in group.models"
                :key="`${group.id}-${model.id}`"
                class="group rounded-xl border border-gray-100 bg-gray-50/80 p-4 transition hover:-translate-y-0.5 hover:border-primary-300 hover:shadow-card dark:border-dark-700 dark:bg-dark-950/80 dark:hover:border-primary-500/50"
              >
                <div class="flex items-start justify-between gap-3">
                  <div class="min-w-0">
                    <h3 class="truncate text-base font-semibold text-gray-950 dark:text-white">{{ model.display_name }}</h3>
                    <p class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ model.id }}</p>
                  </div>
                  <span :class="pricingBadgeClass(model.pricing)">
                    {{ pricingLabel(model.pricing) }}
                  </span>
                </div>

                <div class="mt-4 grid gap-2">
                  <template v-if="compactPricingRows(model.pricing).length > 0">
                    <div
                      v-for="row in compactPricingRows(model.pricing)"
                      :key="row.key"
                      class="flex items-center justify-between gap-3 rounded-xl border border-gray-100 bg-white/90 px-3 py-2.5 text-sm dark:border-dark-700 dark:bg-dark-950/90"
                    >
                      <span class="text-gray-500 dark:text-dark-400">{{ row.label }}</span>
                      <span class="min-w-0 text-right font-medium text-gray-900 dark:text-white">{{ row.value }}</span>
                    </div>
                  </template>
                  <div
                    v-else
                    class="rounded-xl border border-dashed border-gray-200 bg-white/80 px-3 py-3 text-sm text-gray-500 dark:border-dark-700 dark:bg-dark-950/90 dark:text-dark-400"
                  >
                    {{ t('marketplace.pricingUnavailable') }}
                  </div>

                  <button
                    v-if="hasDisplayPricing(model.pricing)"
                    type="button"
                    class="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-primary-50 px-3 py-2 text-sm font-medium text-primary-700 transition hover:bg-primary-100 dark:bg-primary-500/15 dark:text-primary-300 dark:hover:bg-primary-500/25"
                    @click="openPricingDialog(group, model)"
                  >
                    <Icon name="eye" size="sm" />
                    {{ t('marketplace.viewPricing') }}
                  </button>
                </div>
              </article>
            </div>
          </section>
        </div>
      </div>
    </section>

    <BaseDialog
      :show="selectedPricing !== null"
      :title="selectedPricingTitle"
      width="wide"
      :close-on-click-outside="true"
      @close="closePricingDialog"
    >
      <div v-if="selectedPricing" class="space-y-4">
        <div class="rounded-xl border border-gray-100 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-950/80">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="text-base font-semibold text-gray-950 dark:text-white">{{ selectedPricing.model.display_name }}</div>
              <div class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ selectedPricing.model.id }}</div>
              <div class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ selectedPricing.group.name }}</div>
            </div>
            <span :class="pricingBadgeClass(selectedPricing.model.pricing)">
              {{ pricingLabel(selectedPricing.model.pricing) }}
            </span>
          </div>
        </div>

        <template
          v-if="pricingKind(selectedPricing.model.pricing) === 'token' && contextIntervalPricingRows(selectedPricing.model.pricing).length > 0"
        >
          <div class="grid gap-3 md:grid-cols-2">
            <div
              v-for="interval in contextIntervalPricingRows(selectedPricing.model.pricing)"
              :key="interval.key"
              class="rounded-xl border border-gray-100 bg-white/90 px-3 py-3 text-sm dark:border-dark-700 dark:bg-dark-950/90"
            >
              <div class="flex items-center justify-between gap-3">
                <span class="text-gray-500 dark:text-dark-400">{{ t('marketplace.contextTokens') }}</span>
                <span class="font-medium text-gray-900 dark:text-white">{{ interval.range }}</span>
              </div>
              <div class="mt-2 grid gap-1.5 border-t border-gray-100 pt-2 dark:border-dark-700">
                <div
                  v-for="row in interval.rows"
                  :key="row.key"
                  class="flex items-center justify-between gap-3"
                >
                  <span class="text-gray-500 dark:text-dark-400">{{ row.label }}</span>
                  <span class="min-w-0 text-right font-medium text-gray-900 dark:text-white">{{ row.value }}</span>
                </div>
              </div>
            </div>
          </div>
        </template>

        <template
          v-else-if="pricingKind(selectedPricing.model.pricing) === 'token' && tokenPricingRows(selectedPricing.model.pricing).length > 0"
        >
          <div class="grid gap-2 md:grid-cols-2">
            <div
              v-for="row in tokenPricingRows(selectedPricing.model.pricing)"
              :key="row.key"
              class="flex items-center justify-between gap-3 rounded-xl border border-gray-100 bg-white/90 px-3 py-2.5 text-sm dark:border-dark-700 dark:bg-dark-950/90"
            >
              <span class="text-gray-500 dark:text-dark-400">{{ row.label }}</span>
              <span class="min-w-0 text-right font-medium text-gray-900 dark:text-white">{{ row.value }}</span>
            </div>
          </div>
        </template>

        <template v-else-if="pricingKind(selectedPricing.model.pricing) === 'image' && imagePricingRows(selectedPricing.model.pricing).length > 0">
          <div class="grid gap-2 md:grid-cols-2">
            <div
              v-for="row in imagePricingRows(selectedPricing.model.pricing)"
              :key="row.key"
              class="flex items-center justify-between gap-3 rounded-xl border border-gray-100 bg-white/90 px-3 py-2.5 text-sm dark:border-dark-700 dark:bg-dark-950/90"
            >
              <span class="text-gray-500 dark:text-dark-400">{{ row.label }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ row.value }}</span>
            </div>
          </div>
        </template>

        <div
          v-else
          class="rounded-xl border border-dashed border-gray-200 bg-white/80 px-3 py-4 text-sm leading-6 text-gray-500 dark:border-dark-700 dark:bg-dark-950/90 dark:text-dark-400"
        >
          {{ t('marketplace.pricingUnavailable') }}
        </div>
      </div>
    </BaseDialog>
  </component>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { initTheme } from '@/composables/useTheme'
import { getMarketplaceModels } from '@/api/marketplace'
import { providerBrandDisplayName, providerBrandFilterKey, resolveProviderBrand } from '@/utils/providerBrand'
import type { MarketplaceGroup, MarketplaceModel, MarketplaceModelPricing, MarketplacePricingInterval } from '@/types'
import { useAppStore, useAuthStore } from '@/stores'

type VisibleMarketplaceGroup = MarketplaceGroup
type PricingFilter = 'all' | 'token' | 'image' | 'unpriced'
type OverviewIcon = 'grid' | 'sparkles' | 'globe'

interface PricingRow {
  key: string
  label: string
  value: string
}

interface ContextIntervalPricingRow {
  key: string
  range: string
  rows: PricingRow[]
}

interface SelectedPricingModel {
  group: MarketplaceGroup
  model: MarketplaceModel
}

interface OverviewCard {
  key: string
  label: string
  value: number
  icon: OverviewIcon
}

const { t } = useI18n()
const { balanceUnitName } = useBalanceDisplay()

const appStore = useAppStore()
const authStore = useAuthStore()

const groups = ref<MarketplaceGroup[]>([])
const loading = ref(true)
const errorMessage = ref('')
const search = ref('')
const selectedBrand = ref<string | 'all'>('all')
const selectedPricingMode = ref<PricingFilter>('all')
const selectedGroupId = ref<number | 'all'>('all')
const selectedPricing = ref<SelectedPricingModel | null>(null)

const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => {
  if (!isAuthenticated.value) {
    return '/login'
  }
  return isAdmin.value ? '/admin/dashboard' : '/dashboard'
})

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')
const selectedPricingTitle = computed(() => {
  if (!selectedPricing.value) {
    return t('marketplace.pricingDetail')
  }
  return `${selectedPricing.value.model.display_name} · ${t('marketplace.pricingDetail')}`
})

const normalizedSearch = computed(() => search.value.trim().toLowerCase())

const sortedGroups = computed(() =>
  [...groups.value].sort((left, right) => {
    const sortDiff = (left.sort_order ?? 0) - (right.sort_order ?? 0)
    if (sortDiff !== 0) {
      return sortDiff
    }
    return left.id - right.id
  })
)

const totalGroupCount = computed(() => groups.value.length)
const totalModelCount = computed(() => groups.value.reduce((sum, group) => sum + group.models.length, 0))

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

const brandSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allBrands') },
  ...availableBrands.value.map((brand) => ({
    value: brand,
    label: brand,
  })),
])

const pricingSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allTypes') },
  { value: 'token', label: t('marketplace.tokenPricing') },
  { value: 'image', label: t('marketplace.imagePricing') },
  { value: 'unpriced', label: t('marketplace.unpriced') },
])

const groupSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allGroups') },
  ...sortedGroups.value.map((group) => ({
    value: group.id,
    label: group.name,
  })),
])

const filteredGroups = computed<VisibleMarketplaceGroup[]>(() => {
  const keyword = normalizedSearch.value

  return sortedGroups.value.flatMap((group) => {
    if (selectedBrand.value !== 'all' && brandKey(groupBrandLabel(group)) !== brandKey(selectedBrand.value)) {
      return []
    }

    if (selectedGroupId.value !== 'all' && group.id !== selectedGroupId.value) {
      return []
    }

    const groupMatchesKeyword = !keyword || [group.name, group.description, groupBrandSource(group), groupBrandLabel(group)]
      .filter(Boolean)
      .some((value) => value.toLowerCase().includes(keyword))

    const models = group.models.filter((model) => {
      if (selectedPricingMode.value !== 'all' && pricingKind(model.pricing) !== selectedPricingMode.value) {
        return false
      }

      if (!keyword || groupMatchesKeyword) {
        return true
      }

      return [model.id, model.display_name].some((value) => value.toLowerCase().includes(keyword))
    })

    if (models.length === 0) {
      return []
    }

    return [{
      ...group,
      model_count: models.length,
      models,
    }]
  })
})

const visibleGroupCount = computed(() => filteredGroups.value.length)
const visibleModelCount = computed(() =>
  filteredGroups.value.reduce((sum, group) => sum + group.models.length, 0)
)

const overviewCards = computed<OverviewCard[]>(() => [
  {
    key: 'visible-groups',
    label: t('marketplace.matchingGroups'),
    value: visibleGroupCount.value,
    icon: 'grid',
  },
  {
    key: 'visible-models',
    label: t('marketplace.matchingModels'),
    value: visibleModelCount.value,
    icon: 'sparkles',
  },
  {
    key: 'brands',
    label: t('marketplace.brandsStat'),
    value: availableBrands.value.length,
    icon: 'globe',
  },
])

function hasPositiveValue(value?: number | null): value is number {
  return typeof value === 'number' && value > 0
}

function hasOfficialPriceRatio(value?: number | null): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

function hasFlatTokenPricing(pricing: MarketplaceModelPricing): boolean {
  return [
    pricing.input_price_per_token,
    pricing.output_price_per_token,
    pricing.cache_write_price_per_token,
    pricing.cache_read_price_per_token,
    pricing.image_output_price_per_token,
    pricing.fast_input_price_per_token,
    pricing.fast_output_price_per_token,
    pricing.fast_cache_write_price_per_token,
    pricing.fast_cache_read_price_per_token,
    pricing.fast_image_output_price_per_token,
  ].some(hasPositiveValue)
}

// 区间定价没有顶层 flat 价格，需要单独参与“已定价”判断。
function hasContextIntervalPricing(pricing: MarketplaceModelPricing): boolean {
  return pricing.context_intervals?.some((interval) => [
    interval.input_price_per_token,
    interval.output_price_per_token,
    interval.cache_write_price_per_token,
    interval.cache_read_price_per_token,
    interval.image_output_price_per_token,
    interval.fast_input_price_per_token,
    interval.fast_output_price_per_token,
    interval.fast_cache_write_price_per_token,
    interval.fast_cache_read_price_per_token,
    interval.fast_image_output_price_per_token,
  ].some(hasPositiveValue)) ?? false
}

function hasTokenPricing(pricing: MarketplaceModelPricing): boolean {
  return hasFlatTokenPricing(pricing) || hasContextIntervalPricing(pricing)
}

function hasImagePricing(pricing: MarketplaceModelPricing): boolean {
  return [
    pricing.image_price_1k,
    pricing.image_price_2k,
    pricing.image_price_4k,
  ].some(hasPositiveValue)
}

function pricingKind(pricing: MarketplaceModelPricing): Exclude<PricingFilter, 'all'> {
  if (pricing.price_status !== 'priced') {
    return 'unpriced'
  }
  if (pricing.pricing_mode === 'image' && hasImagePricing(pricing)) {
    return 'image'
  }
  if (pricing.pricing_mode === 'token' && hasTokenPricing(pricing)) {
    return 'token'
  }
  return 'unpriced'
}

function hasDisplayPricing(pricing: MarketplaceModelPricing): boolean {
  return pricingKind(pricing) !== 'unpriced'
}

function openPricingDialog(group: MarketplaceGroup, model: MarketplaceModel) {
  if (!hasDisplayPricing(model.pricing)) {
    return
  }
  selectedPricing.value = { group, model }
}

function closePricingDialog() {
  selectedPricing.value = null
}

function resetFilters() {
  search.value = ''
  selectedBrand.value = 'all'
  selectedPricingMode.value = 'all'
  selectedGroupId.value = 'all'
}

function formatMultiplier(multiplier: number): string {
  return `x${multiplier.toFixed(multiplier % 1 === 0 ? 0 : 2)}`
}

function formatOfficialPriceRatio(ratio: number): string {
  const discount = new Intl.NumberFormat(undefined, {
    minimumFractionDigits: 0,
    maximumFractionDigits: 2,
  }).format(ratio * 10)

  return t('marketplace.officialPriceDiscount', { discount })
}

function overviewIconWrapClass(key: string): string {
  const variants: Record<string, string> = {
    'visible-groups': 'bg-blue-100 dark:bg-blue-900/30',
    'visible-models': 'bg-emerald-100 dark:bg-emerald-900/30',
    brands: 'bg-violet-100 dark:bg-violet-900/30',
  }

  return variants[key] ?? 'bg-gray-100 dark:bg-dark-700'
}

function overviewIconClass(key: string): string {
  const variants: Record<string, string> = {
    'visible-groups': 'text-blue-600 dark:text-blue-400',
    'visible-models': 'text-emerald-600 dark:text-emerald-400',
    brands: 'text-violet-600 dark:text-violet-400',
  }

  return variants[key] ?? 'text-gray-600 dark:text-dark-300'
}

function formatPrice(value: number): string {
  const abs = Math.abs(value)
  const maximumFractionDigits = abs >= 1 ? 2 : abs >= 0.01 ? 4 : 6
  const minimumFractionDigits = abs >= 1 ? 2 : 4

  const formatted = new Intl.NumberFormat(undefined, {
    minimumFractionDigits,
    maximumFractionDigits,
  }).format(value)

  return `${formatted} ${balanceUnitName.value}`
}

function formatPerMillion(value: number): string {
  return `${formatPrice(value * 1_000_000)} ${t('usage.perMillionTokens')}`
}

function formatPerImage(value: number): string {
  return `${formatPrice(value)} ${t('marketplace.perImage')}`
}

function formatTokenCount(value: number): string {
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 0,
  }).format(value)
}

// 最大 token 为空表示无上限，用 ∞ 和渠道配置页保持一致。
function formatTokenRange(minTokens: number, maxTokens?: number | null): string {
  const maxLabel = typeof maxTokens === 'number' ? formatTokenCount(maxTokens) : '∞'
  return `${formatTokenCount(minTokens)} - ${maxLabel}`
}

function pricingFilterLabel(mode: Exclude<PricingFilter, 'all'>): string {
  switch (mode) {
    case 'token':
      return t('marketplace.tokenPricing')
    case 'image':
      return t('marketplace.imagePricing')
    case 'unpriced':
      return t('marketplace.unpriced')
  }
}

function pricingLabel(pricing: MarketplaceModelPricing): string {
  if (pricingKind(pricing) === 'token' && hasContextIntervalPricing(pricing)) {
    return t('marketplace.contextIntervalPricing')
  }
  return pricingFilterLabel(pricingKind(pricing))
}

function groupBrandSource(group: Pick<MarketplaceGroup, 'display_brand' | 'name'>): string {
  return group.display_brand?.trim() || group.name
}

function groupBrandLabel(group: Pick<MarketplaceGroup, 'display_brand' | 'name'>): string {
  return providerBrandDisplayName(groupBrandSource(group))
}

function brandKey(label: string): string {
  return providerBrandFilterKey(label)
}

function brandBadgeClass(group: MarketplaceGroup): string {
  const base = 'inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-semibold ring-1 ring-inset'
  return `${base} ${resolveProviderBrand(groupBrandSource(group)).badgeClass}`
}

function groupBrandIconWrapClass(group: MarketplaceGroup): string {
  return resolveProviderBrand(groupBrandSource(group)).iconWrapClass
}

function pricingBadgeClass(pricing: MarketplaceModelPricing): string {
  const base = 'inline-flex shrink-0 items-center rounded-full px-3 py-1 text-xs font-semibold'
  const kind = pricingKind(pricing)

  if (kind === 'token') {
    return `${base} bg-primary-100 text-primary-700 dark:bg-primary-500/15 dark:text-primary-300`
  }
  if (kind === 'image') {
    return `${base} bg-fuchsia-100 text-fuchsia-700 dark:bg-fuchsia-500/15 dark:text-fuchsia-300`
  }
  return `${base} bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-300`
}

function tokenPricingRowsFromValues(pricing: MarketplaceModelPricing | MarketplacePricingInterval): PricingRow[] {
  const rows: PricingRow[] = []

  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({ key: 'input', label: t('marketplace.input'), value: formatPerMillion(pricing.input_price_per_token) })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({ key: 'output', label: t('marketplace.output'), value: formatPerMillion(pricing.output_price_per_token) })
  }
  if (hasPositiveValue(pricing.cache_write_price_per_token)) {
    rows.push({ key: 'cache_write', label: t('marketplace.cacheWrite'), value: formatPerMillion(pricing.cache_write_price_per_token) })
  }
  if (hasPositiveValue(pricing.cache_read_price_per_token)) {
    rows.push({ key: 'cache_read', label: t('marketplace.cacheRead'), value: formatPerMillion(pricing.cache_read_price_per_token) })
  }
  if (hasPositiveValue(pricing.image_output_price_per_token)) {
    rows.push({ key: 'image_output', label: t('marketplace.imageOutput'), value: formatPerMillion(pricing.image_output_price_per_token) })
  }
  if (hasPositiveValue(pricing.fast_input_price_per_token)) {
    rows.push({ key: 'fast_input', label: t('marketplace.fastInput'), value: formatPerMillion(pricing.fast_input_price_per_token) })
  }
  if (hasPositiveValue(pricing.fast_output_price_per_token)) {
    rows.push({ key: 'fast_output', label: t('marketplace.fastOutput'), value: formatPerMillion(pricing.fast_output_price_per_token) })
  }
  if (hasPositiveValue(pricing.fast_cache_write_price_per_token)) {
    rows.push({ key: 'fast_cache_write', label: t('marketplace.fastCacheWrite'), value: formatPerMillion(pricing.fast_cache_write_price_per_token) })
  }
  if (hasPositiveValue(pricing.fast_cache_read_price_per_token)) {
    rows.push({ key: 'fast_cache_read', label: t('marketplace.fastCacheRead'), value: formatPerMillion(pricing.fast_cache_read_price_per_token) })
  }
  if (hasPositiveValue(pricing.fast_image_output_price_per_token)) {
    rows.push({ key: 'fast_image_output', label: t('marketplace.fastImageOutput'), value: formatPerMillion(pricing.fast_image_output_price_per_token) })
  }

  return rows
}

function tokenPricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  return tokenPricingRowsFromValues(pricing)
}

function compactTokenPricingRows(pricing: MarketplaceModelPricing | MarketplacePricingInterval): PricingRow[] {
  const primaryRows: PricingRow[] = []
  if (hasPositiveValue(pricing.input_price_per_token)) {
    primaryRows.push({ key: 'input', label: t('marketplace.input'), value: formatPerMillion(pricing.input_price_per_token) })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    primaryRows.push({ key: 'output', label: t('marketplace.output'), value: formatPerMillion(pricing.output_price_per_token) })
  }
  if (primaryRows.length > 0) {
    return primaryRows
  }

  return tokenPricingRowsFromValues(pricing)
    .filter((row) => !row.key.startsWith('fast_'))
    .slice(0, 2)
}

function compactContextIntervalRows(pricing: MarketplaceModelPricing): PricingRow[] {
  return pricing.context_intervals?.flatMap((interval, index) => {
    const rows = compactTokenPricingRows(interval)
    if (rows.length === 0) {
      return []
    }
    return [{
      key: `compact-${interval.min_tokens}-${interval.max_tokens ?? 'up'}-${index}`,
      label: formatTokenRange(interval.min_tokens, interval.max_tokens),
      value: rows.map((row) => `${row.label} ${row.value}`).join(' / '),
    }]
  }) ?? []
}

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
  return []
}

// 上下文区间价格直接复用 token 价格行，只额外展示区间范围。
function contextIntervalPricingRows(pricing: MarketplaceModelPricing): ContextIntervalPricingRow[] {
  return pricing.context_intervals?.flatMap((interval, index) => {
    const rows = tokenPricingRowsFromValues(interval)
    if (rows.length === 0) {
      return []
    }

    return [{
      key: `${interval.min_tokens}-${interval.max_tokens ?? 'up'}-${index}`,
      range: formatTokenRange(interval.min_tokens, interval.max_tokens),
      rows,
    }]
  }) ?? []
}

function imagePricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
  const values = [
    { key: '1k', label: '1K', price: pricing.image_price_1k },
    { key: '2k', label: '2K', price: pricing.image_price_2k },
    { key: '4k', label: '4K', price: pricing.image_price_4k },
  ]

  return values.flatMap((item) => {
    if (!hasPositiveValue(item.price)) {
      return []
    }

    return [{
      key: item.key,
      label: item.label,
      value: formatPerImage(item.price),
    }]
  })
}

async function fetchMarketplace() {
  loading.value = true
  errorMessage.value = ''

  try {
    groups.value = await getMarketplaceModels()
  } catch (error) {
    console.error('Failed to load marketplace models:', error)
    errorMessage.value =
      typeof error === 'object' && error !== null && 'message' in error
        ? String(error.message)
        : t('common.unknownError')
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    await appStore.fetchPublicSettings()
  }
  await fetchMarketplace()
})
</script>
