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
            <Select v-model="selectedPlatform" :options="platformSelectOptions" />
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
                  <span :class="platformBadgeClass(group.platform)">
                    {{ platformLabel(group.platform) }}
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
                  <span class="mt-0.5 h-10 w-1 shrink-0 rounded-full" :class="groupAccentClass(group.platform)"></span>
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
                  <template
                    v-if="pricingKind(model.pricing) === 'token' && tokenPricingRows(model.pricing).length > 0"
                  >
                    <div
                      v-for="row in tokenPricingRows(model.pricing)"
                      :key="row.key"
                      class="flex items-center justify-between gap-3 rounded-xl border border-gray-100 bg-white/90 px-3 py-2.5 text-sm dark:border-dark-700 dark:bg-dark-950/90"
                    >
                      <span class="text-gray-500 dark:text-dark-400">{{ row.label }}</span>
                      <span class="font-medium text-gray-900 dark:text-white">{{ row.value }}</span>
                    </div>
                  </template>

                  <template v-else-if="pricingKind(model.pricing) === 'image' && imagePricingRows(model.pricing).length > 0">
                    <div
                      v-for="row in imagePricingRows(model.pricing)"
                      :key="row.key"
                      class="flex items-center justify-between gap-3 rounded-xl border border-gray-100 bg-white/90 px-3 py-2.5 text-sm dark:border-dark-700 dark:bg-dark-950/90"
                    >
                      <span class="text-gray-500 dark:text-dark-400">{{ row.label }}</span>
                      <span class="font-medium text-gray-900 dark:text-white">{{ row.value }}</span>
                    </div>
                  </template>

                  <div
                    v-else
                    class="rounded-xl border border-dashed border-gray-200 bg-white/80 px-3 py-4 text-sm leading-6 text-gray-500 dark:border-dark-700 dark:bg-dark-950/90 dark:text-dark-400"
                  >
                    {{ t('marketplace.pricingUnavailable') }}
                  </div>
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
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Select from '@/components/common/Select.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { getMarketplaceModels } from '@/api/marketplace'
import type { GroupPlatform, MarketplaceGroup, MarketplaceModelPricing } from '@/types'
import { useAppStore, useAuthStore } from '@/stores'

type VisibleMarketplaceGroup = MarketplaceGroup
type PricingFilter = 'all' | 'token' | 'image' | 'unpriced'
type OverviewIcon = 'grid' | 'sparkles' | 'globe'

interface PricingRow {
  key: string
  label: string
  value: string
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
const selectedPlatform = ref<GroupPlatform | 'all'>('all')
const selectedPricingMode = ref<PricingFilter>('all')
const selectedGroupId = ref<number | 'all'>('all')

const platformOrder: GroupPlatform[] = ['openai', 'anthropic', 'gemini', 'antigravity']

const isAuthenticated = computed(() => authStore.isAuthenticated)
const isAdmin = computed(() => authStore.isAdmin)
const dashboardPath = computed(() => {
  if (!isAuthenticated.value) {
    return '/login'
  }
  return isAdmin.value ? '/admin/dashboard' : '/dashboard'
})

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() => appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '')
const docUrl = computed(() => appStore.cachedPublicSettings?.doc_url || appStore.docUrl || '')

const normalizedSearch = computed(() => search.value.trim().toLowerCase())

const sortedGroups = computed(() =>
  [...groups.value].sort((left, right) => {
    const platformDiff = platformRank(left.platform) - platformRank(right.platform)
    if (platformDiff !== 0) {
      return platformDiff
    }
    return left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: 'base' })
  })
)

const totalGroupCount = computed(() => groups.value.length)
const totalModelCount = computed(() => groups.value.reduce((sum, group) => sum + group.models.length, 0))

const platformCounts = computed<Record<GroupPlatform, number>>(() => {
  const counts: Record<GroupPlatform, number> = {
    openai: 0,
    anthropic: 0,
    gemini: 0,
    antigravity: 0,
  }

  for (const group of groups.value) {
    counts[group.platform] += group.models.length
  }

  return counts
})

const availablePlatforms = computed(() =>
  platformOrder.filter((platform) => platformCounts.value[platform] > 0)
)

const platformSelectOptions = computed(() => [
  { value: 'all', label: t('marketplace.allPlatforms') },
  ...availablePlatforms.value
    .map((platform) => ({
      value: platform,
      label: platformLabel(platform),
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
    if (selectedPlatform.value !== 'all' && group.platform !== selectedPlatform.value) {
      return []
    }

    if (selectedGroupId.value !== 'all' && group.id !== selectedGroupId.value) {
      return []
    }

    const groupMatchesKeyword = !keyword || [group.name, group.description, platformLabel(group.platform)]
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
    key: 'platforms',
    label: t('marketplace.platformsStat'),
    value: availablePlatforms.value.length,
    icon: 'globe',
  },
])

function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  if (
    savedTheme === 'dark' ||
    (!savedTheme && window.matchMedia('(prefers-color-scheme: dark)').matches)
  ) {
    document.documentElement.classList.add('dark')
  }
}

function platformRank(platform: GroupPlatform): number {
  const index = platformOrder.indexOf(platform)
  return index === -1 ? platformOrder.length : index
}

function hasPositiveValue(value?: number | null): value is number {
  return typeof value === 'number' && value > 0
}

function hasOfficialPriceRatio(value?: number | null): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0
}

function hasTokenPricing(pricing: MarketplaceModelPricing): boolean {
  return [
    pricing.input_price_per_token,
    pricing.output_price_per_token,
    pricing.cache_write_price_per_token,
    pricing.cache_read_price_per_token,
    pricing.image_output_price_per_token,
  ].some(hasPositiveValue)
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

function resetFilters() {
  search.value = ''
  selectedPlatform.value = 'all'
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
    platforms: 'bg-violet-100 dark:bg-violet-900/30',
  }

  return variants[key] ?? 'bg-gray-100 dark:bg-dark-700'
}

function overviewIconClass(key: string): string {
  const variants: Record<string, string> = {
    'visible-groups': 'text-blue-600 dark:text-blue-400',
    'visible-models': 'text-emerald-600 dark:text-emerald-400',
    platforms: 'text-violet-600 dark:text-violet-400',
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

function platformLabel(platform: GroupPlatform): string {
  return t(`marketplace.platforms.${platform}`)
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
  return pricingFilterLabel(pricingKind(pricing))
}

function platformBadgeClass(platform: GroupPlatform): string {
  const base = 'inline-flex items-center rounded-full px-3 py-1 text-xs font-semibold ring-1 ring-inset'
  const variants: Record<GroupPlatform, string> = {
    openai: 'bg-emerald-100 text-emerald-900 ring-emerald-200 dark:bg-emerald-500/20 dark:text-emerald-50 dark:ring-emerald-400/30',
    anthropic: 'bg-orange-100 text-orange-900 ring-orange-200 dark:bg-orange-500/20 dark:text-orange-50 dark:ring-orange-400/30',
    gemini: 'bg-blue-100 text-blue-900 ring-blue-200 dark:bg-blue-500/20 dark:text-blue-50 dark:ring-blue-400/30',
    antigravity: 'bg-rose-100 text-rose-900 ring-rose-200 dark:bg-rose-500/20 dark:text-rose-50 dark:ring-rose-400/30',
  }
  return `${base} ${variants[platform]}`
}

function groupAccentClass(platform: GroupPlatform): string {
  const variants: Record<GroupPlatform, string> = {
    openai: 'bg-emerald-500',
    anthropic: 'bg-orange-500',
    gemini: 'bg-blue-500',
    antigravity: 'bg-rose-500',
  }
  return variants[platform]
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

function tokenPricingRows(pricing: MarketplaceModelPricing): PricingRow[] {
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

  return rows
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
