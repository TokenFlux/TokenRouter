<template>
  <component
    :is="isAuthenticated ? AppLayout : 'div'"
    :class="isAuthenticated ? '' : 'ba-theme-shell relative min-h-screen overflow-x-hidden'"
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
      class="marketplace-page"
      :class="isAuthenticated ? 'marketplace-page--app' : 'relative z-10 px-4 pb-12 pt-6 sm:px-6 lg:px-8'"
    >
      <div :class="isAuthenticated ? 'marketplace-container marketplace-container--app' : 'marketplace-container'">
        <div class="marketplace-page-header">
          <div class="min-w-0">
            <div class="marketplace-page-eyebrow">
              {{ marketplaceBrandCount }} {{ t('marketplace.brandsStat') }} ·
              {{ marketplaceGroupCount }} {{ t('marketplace.groupsStat') }} ·
              {{ marketplaceModelCount }} {{ t('marketplace.modelsStat') }}
            </div>
            <h1>{{ t('marketplace.title') }}</h1>
            <p>{{ t('marketplace.subtitle') }}</p>
          </div>
        </div>

        <div class="marketplace-filter-bar">
          <div class="min-w-[280px] flex-1 xl:max-w-[520px]">
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

        <div v-else-if="marketplaceBrands.length === 0" class="card px-6 py-14">
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

        <div v-else class="brand-section-stack">
          <section
            v-for="brand in marketplaceBrands"
            :key="brand.key"
            class="brand-section"
            data-testid="marketplace-brand-section"
          >
            <div class="brand-section-header">
              <div class="flex min-w-0 items-center gap-3">
                <span class="brand-section-icon" :class="brandIconWrapClass(brand)">
                  <ProviderIcon :brand="brand.source" size="22px" />
                </span>
                <div class="min-w-0">
                  <h2>{{ brand.label }}</h2>
                  <div class="brand-section-meta">
                    <span>{{ brand.groupCount }} {{ t('marketplace.groupsStat') }}</span>
                    <span>{{ brand.models.length }} {{ t('marketplace.modelsStat') }}</span>
                  </div>
                </div>
              </div>
              <span class="brand-refresh-badge">{{ t('marketplace.autoRefreshing') }}</span>
            </div>

            <div class="card-grid">
              <article
                v-for="model in brand.models"
                :key="model.key"
                class="model-card"
                data-testid="marketplace-model-card"
                role="button"
                tabindex="0"
                @click="openSelectedModel(model.key)"
                @keydown.enter.prevent="openSelectedModel(model.key)"
                @keydown.space.prevent="openSelectedModel(model.key)"
              >
                <div class="card-header-row">
                  <div class="model-icon">
                    <ProviderIcon :brand="brand.source" size="22px" />
                  </div>
                  <div class="min-w-0">
                    <div class="model-title">{{ model.displayName }}</div>
                    <div class="model-tags">
                      <span class="model-provider">{{ brand.label }}</span>
                      <span class="model-provider">{{ model.availabilities.length }} {{ t('marketplace.groupsStat') }}</span>
                      <span class="model-provider">{{ modelPricingKindLabel(model) }}</span>
                    </div>
                  </div>
                </div>

                <div class="card-stats">
                  <span
                    v-for="row in modelCardPricingRows(model)"
                    :key="row.key"
                    class="card-stat-item"
                  >
                    <span class="card-stat-label">{{ row.label }}</span>
                    <span class="card-stat-value">
                      <span
                        v-for="(part, partIndex) in cardStatValueParts(row.value)"
                        :key="`${row.key}-${partIndex}`"
                        class="card-stat-value-part"
                      >
                        {{ part }}
                      </span>
                    </span>
                  </span>
                </div>

                <div class="card-footer-row">
                  <span class="card-footer-meta">
                    <span>{{ availableAvailabilityCount(model) }}/{{ model.availabilities.length }} {{ t('marketplace.groupsStat') }}</span>
                    <span>{{ t('marketplace.lowestGroupPricing') }} {{ formatMultiplier(lowestRateMultiplier(model)) }}</span>
                  </span>
                  <span class="card-health-summary" :title="modelRecentHealthTitle(model)" :aria-label="modelRecentHealthTitle(model)">
                    <span><span :class="modelStatusDotClass(model)"></span>{{ modelStateLabel(model) }}</span>
                    <span class="card-recent-health-dots" aria-hidden="true">
                      <span
                        v-for="dot in modelRecentHealthDots(model)"
                        :key="dot.key"
                        :class="dot.class"
                      ></span>
                    </span>
                  </span>
                </div>
              </article>
            </div>
          </section>
        </div>
      </div>
    </section>

    <Teleport to="body">
      <div
        class="detail-overlay"
        data-testid="marketplace-detail-overlay"
        :class="{ active: selectedMarketplaceModel !== null }"
        role="dialog"
        aria-modal="true"
        :aria-label="selectedMarketplaceModel ? `${selectedMarketplaceModel.displayName} ${t('marketplace.groupAvailability')}` : t('marketplace.groupAvailability')"
        @click.self="closeSelectedModel"
      >
        <div class="detail-modal">
          <div v-if="selectedMarketplaceModel" class="detail-modal-inner">
            <div class="detail-modal-header">
              <div class="header-left">
                <div class="model-icon model-icon--large">
                  <ProviderIcon :brand="selectedMarketplaceBrand?.source || selectedMarketplaceModel.displayName" size="24px" />
                </div>
                <div class="min-w-0">
                  <div class="header-title-row">
                    <h2>{{ selectedMarketplaceModel.displayName }}</h2>
                    <div class="header-model-id-row">
                      <span>{{ t('marketplace.callModelId') }}</span>
                      <code>{{ selectedMarketplaceModel.id }}</code>
                      <button
                        type="button"
                        class="header-model-id-copy-btn"
                        :aria-label="t('marketplace.copyModelId')"
                        @click.stop="copyMarketplaceModelId(selectedMarketplaceModel.id)"
                      >
                        {{ t('common.copy') }}
                      </button>
                    </div>
                  </div>
                  <div class="header-tags">
                    <span>{{ selectedMarketplaceBrand?.label || '-' }}</span>
                    <span>{{ selectedMarketplaceModel.availabilities.length }} {{ t('marketplace.groupsStat') }}</span>
                    <span>{{ modelPricingKindLabel(selectedMarketplaceModel) }}</span>
                  </div>
                </div>
              </div>
              <button class="close-btn" type="button" :aria-label="t('common.close')" @click="closeSelectedModel">
                &times;
              </button>
            </div>

            <div class="detail-modal-body">
              <div class="left-column">
                <h3>{{ t('marketplace.availabilitySummary') }} ({{ selectedMarketplaceModel.availabilities.length }})</h3>

                <article
                  v-for="availability in selectedMarketplaceModel.availabilities"
                  :key="availability.key"
                  :class="groupCardClass(availability)"
                  data-testid="marketplace-availability-card"
                  role="button"
                  tabindex="0"
                  @click="selectAvailability(availability.key)"
                  @keydown.enter.prevent="selectAvailability(availability.key)"
                  @keydown.space.prevent="selectAvailability(availability.key)"
                >
                  <div class="uptime-header">
                    <div class="group-title">
                      <span :class="availabilityStatusDotClass(availability)"></span>
                      {{ availability.group.name }}
                    </div>
                    <div class="uptime-percent">{{ requestSuccessSummary(availability) }}</div>
                  </div>

                  <div
                    :ref="(el) => setRequestBarRef(availability.key, el)"
                    class="uptime-bars-wrapper"
                    :class="{ 'is-empty': !hasRecentRequests(availability) }"
                    :title="requestStatusBarTitle(availability)"
                    :aria-label="requestStatusBarTitle(availability)"
                  >
                    <span
                      v-for="bar in availabilityRequestSegments(availability)"
                      :key="bar.key"
                      :class="bar.class"
                    ></span>
                  </div>

                  <div class="metrics-row">
                    <div class="concurrency-wrapper">
                      <span>{{ t('marketplace.concurrency') }} ({{ capacityMetricLabel(availability.group.capacity?.concurrency_used, availability.group.capacity?.concurrency_max) }})</span>
                      <div class="progress-bg">
                        <div
                          class="progress-fill"
                          :class="availabilityProgressClass(availability)"
                          :style="{ width: `${Math.round(capacityUsageRatio(availability.group) * 100)}%` }"
                        ></div>
                      </div>
                    </div>
                    <div class="metric-badge highlight">{{ t('marketplace.rateMultiplier') }} {{ formatMultiplier(availability.group.rate_multiplier) }}</div>
                    <div class="metric-badge">{{ officialDiscountLabel(availability) }}</div>
                  </div>
                </article>
              </div>

              <div class="right-column">
                <Transition name="pricing-panel" mode="out-in">
                  <aside
                    v-if="selectedMarketplaceAvailability"
                    :key="selectedMarketplaceAvailability.key"
                    class="sticky-panel"
                  >
                    <div class="panel-title">
                      <span>{{ selectedMarketplaceAvailability.group.name }} {{ t('marketplace.groupPricingDetail') }}</span>
                      <span class="sharing-badge">
                        {{ selectedMarketplaceAvailability.group.data_sharing_enabled ? t('marketplace.dataSharingTag') : pricingLabel(selectedMarketplaceAvailability.model.pricing) }}
                      </span>
                    </div>
                    <table v-if="detailedPricingRows(selectedMarketplaceAvailability).length > 0" class="price-table">
                      <thead>
                        <tr>
                          <th>{{ t('marketplace.priceBreakdown') }}</th>
                          <th>{{ t('marketplace.officialRate') }}</th>
                          <th>{{ t('marketplace.finalBillingPrice') }}</th>
                        </tr>
                      </thead>
                      <tbody>
                        <tr
                          v-for="row in detailedPricingRows(selectedMarketplaceAvailability).slice(0, 6)"
                          :key="row.key"
                        >
                          <td>{{ row.label }}</td>
                          <td class="num">{{ row.official }}</td>
                          <td class="num final-price">{{ row.final }}</td>
                        </tr>
                      </tbody>
                    </table>

                    <div v-else class="pricing-empty">
                      {{ t('marketplace.pricingUnavailable') }}
                    </div>

                    <button
                      v-if="hasDisplayPricing(selectedMarketplaceAvailability.model.pricing)"
                      type="button"
                      class="btn btn-primary btn-sm mt-5 w-full"
                      data-testid="marketplace-view-pricing-button"
                      @click="openPricingDialog(selectedMarketplaceAvailability.group, selectedMarketplaceAvailability.model)"
                    >
                      {{ t('marketplace.viewPricing') }}
                    </button>
                  </aside>
                </Transition>
              </div>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <BaseDialog
      :show="selectedPricing !== null"
      :title="selectedPricingTitle"
      width="wide"
      :z-index="120"
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
          v-if="pricingKind(selectedPricing.model.pricing) === 'token' && contextIntervalPricingRows(selectedPricing.model.pricing, 'symbol').length > 0"
        >
          <div class="grid gap-3 md:grid-cols-2">
            <div
              v-for="interval in contextIntervalPricingRows(selectedPricing.model.pricing, 'symbol')"
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
          v-else-if="pricingKind(selectedPricing.model.pricing) === 'token' && tokenPricingRows(selectedPricing.model.pricing, 'symbol').length > 0"
        >
          <div class="grid gap-2 md:grid-cols-2">
            <div
              v-for="row in tokenPricingRows(selectedPricing.model.pricing, 'symbol')"
              :key="row.key"
              class="flex items-center justify-between gap-3 rounded-xl border border-gray-100 bg-white/90 px-3 py-2.5 text-sm dark:border-dark-700 dark:bg-dark-950/90"
            >
              <span class="text-gray-500 dark:text-dark-400">{{ row.label }}</span>
              <span class="min-w-0 text-right font-medium text-gray-900 dark:text-white">{{ row.value }}</span>
            </div>
          </div>
        </template>

        <template v-else-if="pricingKind(selectedPricing.model.pricing) === 'image' && imagePricingRows(selectedPricing.model.pricing, 'symbol').length > 0">
          <div class="grid gap-2 md:grid-cols-2">
            <div
              v-for="row in imagePricingRows(selectedPricing.model.pricing, 'symbol')"
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
import { computed, onMounted, onUnmounted, ref } from 'vue'
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
import { useClipboard } from '@/composables/useClipboard'
import { initTheme } from '@/composables/useTheme'
import { getMarketplaceModels } from '@/api/marketplace'
import { providerBrandDisplayName, providerBrandFilterKey, resolveProviderBrand } from '@/utils/providerBrand'
import type { MarketplaceGroup, MarketplaceModel, MarketplaceModelPricing, MarketplacePricingInterval } from '@/types'
import { useAppStore, useAuthStore } from '@/stores'

type VisibleMarketplaceGroup = MarketplaceGroup
type PricingFilter = 'all' | 'token' | 'image' | 'unpriced'

interface PricingRow {
  key: string
  label: string
  value: string
}

interface PricingCompareRow {
  key: string
  label: string
  official: string
  final: string
}

type PriceUnitStyle = 'name' | 'symbol'

interface ContextIntervalPricingRow {
  key: string
  range: string
  rows: PricingRow[]
}

interface SelectedPricingModel {
  group: MarketplaceGroup
  model: MarketplaceModel
}

type MarketplaceAvailabilityStatus = 'available' | 'busy' | 'unpriced'

interface MarketplaceModelAvailability {
  key: string
  group: MarketplaceGroup
  model: MarketplaceModel
  status: MarketplaceAvailabilityStatus
}

interface MarketplaceModelView {
  key: string
  id: string
  displayName: string
  availabilities: MarketplaceModelAvailability[]
}

interface MarketplaceBrandView {
  key: string
  label: string
  source: string
  sortOrder: number
  groupCount: number
  models: MarketplaceModelView[]
}

interface MarketplaceRequestSegment {
  key: string
  class: string
}

interface MarketplaceRecentHealthDot {
  key: string
  class: string
}

const requestSegmentWidth = 6
const requestSegmentGap = 2
const fallbackRequestSegmentCount = 24

const { t } = useI18n()
const { balanceUnitName, balanceUnitSymbol } = useBalanceDisplay()
const { copyToClipboard } = useClipboard()

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
const selectedModelKey = ref<string | null>(null)
const selectedAvailabilityKey = ref<string | null>(null)
const requestBarWidths = ref<Record<string, number>>({})
const requestBarElements = new Map<string, HTMLElement>()
const requestBarKeysByElement = new WeakMap<HTMLElement, string>()
let requestBarResizeObserver: ResizeObserver | null = null

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

const marketplaceBrands = computed<MarketplaceBrandView[]>(() => {
  const brandMap = new Map<string, {
    label: string
    source: string
    sortOrder: number
    groups: Set<number>
    models: Map<string, MarketplaceModelAvailability[]>
  }>()

  for (const group of filteredGroups.value) {
    const label = groupBrandLabel(group)
    const key = brandKey(label)
    const brand = brandMap.get(key) ?? {
      label,
      source: groupBrandSource(group),
      sortOrder: group.sort_order ?? 0,
      groups: new Set<number>(),
      models: new Map<string, MarketplaceModelAvailability[]>(),
    }

    brand.sortOrder = Math.min(brand.sortOrder, group.sort_order ?? 0)
    brand.groups.add(group.id)

    for (const model of group.models) {
      const modelKey = `${key}:${model.id}`
      const availabilities = brand.models.get(modelKey) ?? []
      availabilities.push({
        key: `${group.id}:${model.id}`,
        group,
        model,
        status: availabilityStatus(group, model),
      })
      brand.models.set(modelKey, availabilities)
    }

    brandMap.set(key, brand)
  }

  return Array.from(brandMap.entries())
    .map(([key, brand]) => ({
      key,
      label: brand.label,
      source: brand.source,
      sortOrder: brand.sortOrder,
      groupCount: brand.groups.size,
      models: Array.from(brand.models.entries())
        .map(([modelKey, availabilities]) => {
          const first = availabilities[0].model
          return {
            key: modelKey,
            id: first.id,
            displayName: first.display_name,
            availabilities: sortAvailabilities(availabilities),
          }
        })
        .sort((left, right) => left.displayName.localeCompare(right.displayName) || left.id.localeCompare(right.id)),
    }))
    .sort((left, right) => left.sortOrder - right.sortOrder || left.label.localeCompare(right.label))
})

const selectedMarketplaceModel = computed<MarketplaceModelView | null>(() => {
  if (!selectedModelKey.value) {
    return null
  }

  for (const brand of marketplaceBrands.value) {
    const model = brand.models.find((item) => item.key === selectedModelKey.value)
    if (model) {
      return model
    }
  }

  return null
})

const selectedMarketplaceBrand = computed<MarketplaceBrandView | null>(() => {
  if (!selectedModelKey.value) {
    return null
  }

  for (const brand of marketplaceBrands.value) {
    if (brand.models.some((model) => model.key === selectedModelKey.value)) {
      return brand
    }
  }

  return null
})

const selectedMarketplaceAvailability = computed<MarketplaceModelAvailability | null>(() => {
  const model = selectedMarketplaceModel.value
  if (!model) {
    return null
  }

  return model.availabilities.find((availability) => availability.key === selectedAvailabilityKey.value)
    ?? model.availabilities[0]
    ?? null
})

const marketplaceBrandCount = computed(() => marketplaceBrands.value.length)
const marketplaceModelCount = computed(() =>
  marketplaceBrands.value.reduce((total, brand) => total + brand.models.length, 0)
)
const marketplaceGroupCount = computed(() => new Set(filteredGroups.value.map((group) => group.id)).size)

function hasPositiveValue(value?: number | null): value is number {
  return typeof value === 'number' && value > 0
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

function availabilityStatus(group: MarketplaceGroup, model: MarketplaceModel): MarketplaceAvailabilityStatus {
  if (!hasDisplayPricing(model.pricing)) {
    return 'unpriced'
  }

  if (capacityUsageRatio(group) >= 1) {
    return 'busy'
  }

  return 'available'
}

function sortAvailabilities(availabilities: MarketplaceModelAvailability[]): MarketplaceModelAvailability[] {
  const statusRank: Record<MarketplaceAvailabilityStatus, number> = {
    available: 0,
    busy: 1,
    unpriced: 2,
  }

  return [...availabilities].sort((left, right) => {
    const statusDiff = statusRank[left.status] - statusRank[right.status]
    if (statusDiff !== 0) {
      return statusDiff
    }
    const rateDiff = left.group.rate_multiplier - right.group.rate_multiplier
    if (rateDiff !== 0) {
      return rateDiff
    }
    return left.group.name.localeCompare(right.group.name)
  })
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

function findMarketplaceModelByKey(modelKey: string): MarketplaceModelView | null {
  for (const brand of marketplaceBrands.value) {
    const model = brand.models.find((item) => item.key === modelKey)
    if (model) {
      return model
    }
  }
  return null
}

function openSelectedModel(modelKey: string) {
  const model = findMarketplaceModelByKey(modelKey)
  if (!model) {
    return
  }
  selectedModelKey.value = model.key
  selectedAvailabilityKey.value = model.availabilities[0]?.key ?? null
}

async function copyMarketplaceModelId(modelId: string) {
  await copyToClipboard(modelId, t('marketplace.modelIdCopied'))
}

function closeSelectedModel() {
  selectedModelKey.value = null
  selectedAvailabilityKey.value = null
}

function selectAvailability(availabilityKey: string) {
  selectedAvailabilityKey.value = availabilityKey
}

function resetFilters() {
  search.value = ''
  selectedBrand.value = 'all'
  selectedPricingMode.value = 'all'
  selectedGroupId.value = 'all'
  selectedModelKey.value = null
  selectedAvailabilityKey.value = null
}

function formatMultiplier(multiplier: number): string {
  return `x${multiplier.toFixed(multiplier % 1 === 0 ? 0 : 2)}`
}

async function handleMarketplaceKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !selectedMarketplaceModel.value || selectedPricing.value) {
    return
  }
  event.preventDefault()
  closeSelectedModel()
}

function formatPrice(value: number, unitStyle: PriceUnitStyle = 'name'): string {
  const formatted = formatPriceNumber(value)
  if (unitStyle === 'symbol') {
    return `${balanceUnitSymbol.value}${formatted}`
  }
  return `${formatted} ${balanceUnitName.value}`
}

function formatPriceNumber(value: number): string {
  const abs = Math.abs(value)
  const maximumFractionDigits = abs >= 1 ? 2 : abs >= 0.01 ? 4 : 6
  const minimumFractionDigits = abs >= 1 ? 2 : 4

  return new Intl.NumberFormat(undefined, {
    minimumFractionDigits,
    maximumFractionDigits,
  }).format(value)
}

function formatPerMillion(value: number, unitStyle: PriceUnitStyle = 'name'): string {
  return `${formatPrice(value * 1_000_000, unitStyle)} ${t('usage.perMillionTokens')}`
}

function formatCompactPerMillion(value: number): string {
  return formatPriceNumber(value * 1_000_000)
}

function formatPerImage(value: number, unitStyle: PriceUnitStyle = 'name'): string {
  return `${formatPrice(value, unitStyle)} ${t('marketplace.perImage')}`
}

function formatTokenCount(value: number): string {
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: 0,
  }).format(value)
}

function formatCompactNumber(value: number): string {
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits: value >= 100 ? 0 : 1,
  }).format(value)
}

function formatCompactTokenCount(value: number): string {
  if (value >= 1_000_000) {
    return `${formatCompactNumber(value / 1_000_000)}m`
  }
  if (value >= 1_000) {
    return `${formatCompactNumber(value / 1_000)}k`
  }
  return formatTokenCount(value)
}

// 最大 token 为空表示无上限，用 ∞ 和渠道配置页保持一致。
function formatTokenRange(minTokens: number, maxTokens?: number | null): string {
  const maxLabel = typeof maxTokens === 'number' ? formatTokenCount(maxTokens) : '∞'
  return `${formatTokenCount(minTokens)} - ${maxLabel}`
}

// 卡片预览空间有限，用紧凑区间避免上下文数字换行。
function formatCompactTokenRange(minTokens: number, maxTokens?: number | null): string {
  if (typeof maxTokens !== 'number') {
    return `${formatCompactTokenCount(minTokens)}+`
  }
  return `${formatCompactTokenCount(minTokens)}-${formatCompactTokenCount(maxTokens)}`
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

function brandIconWrapClass(brand: MarketplaceBrandView): string {
  return resolveProviderBrand(brand.source).iconWrapClass
}

function modelCardPricingRows(model: MarketplaceModelView): PricingRow[] {
  const availability = bestPricedAvailability(model) ?? model.availabilities[0]
  if (!availability || !hasDisplayPricing(availability.model.pricing)) {
    return [
      { key: 'groups', label: t('marketplace.availableAndTotalGroups'), value: `${availableAvailabilityCount(model)}/${model.availabilities.length}` },
      { key: 'rate', label: t('marketplace.lowestGroupPricing'), value: formatMultiplier(lowestRateMultiplier(model)) },
    ]
  }

  const rows = compactPricingRows(scalePricingForOfficial(availability.model.pricing, availability.group.rate_multiplier))
  if (rows.length >= 2) {
    return rows.slice(0, 2).map((row) => ({
      key: row.key,
      label: row.label,
      value: row.value.replace(` ${balanceUnitName.value} ${t('usage.perMillionTokens')}`, ''),
    }))
  }

  return [
    { key: 'pricing', label: t('marketplace.pricingRule'), value: pricingLabel(availability.model.pricing) },
    { key: 'rate', label: t('marketplace.lowestGroupPricing'), value: formatMultiplier(lowestRateMultiplier(model)) },
  ]
}

function cardStatValueParts(value: string): string[] {
  return value.split(/\s+\/\s+/).filter(Boolean)
}

function groupCardClass(availability: MarketplaceModelAvailability): string {
  return `group-card${selectedMarketplaceAvailability.value?.key === availability.key ? ' active' : ''}`
}

function availabilityStatusDotClass(availability: MarketplaceModelAvailability): string {
  const percent = availabilityPercent(availability)
  const state = percent >= 85 ? 'good' : percent >= 35 ? 'warn' : 'bad'
  return `status-dot is-${state}`
}

function availabilityProgressClass(availability: MarketplaceModelAvailability): string {
  const ratio = capacityUsageRatio(availability.group)
  if (ratio >= 0.85 || availability.status === 'busy') {
    return 'is-warn'
  }
  if (availability.status === 'unpriced') {
    return 'is-bad'
  }
  return ''
}

function availableAvailabilityCount(model: MarketplaceModelView): number {
  return model.availabilities.filter((availability) => availability.status === 'available').length
}

function availableRatioForAvailability(availability: MarketplaceModelAvailability): number {
  if (availability.status === 'unpriced') {
    return 0
  }
  if (availability.status === 'busy') {
    return 0.12
  }
  return 1 - capacityUsageRatio(availability.group)
}

function availabilityPercent(availability: MarketplaceModelAvailability): number {
  return Math.round(availableRatioForAvailability(availability) * 1000) / 10
}

function modelAvailabilityPercent(model: MarketplaceModelView): number {
  if (model.availabilities.length === 0) {
    return 0
  }
  const average = model.availabilities.reduce((total, availability) => (
    total + availableRatioForAvailability(availability)
  ), 0) / model.availabilities.length
  return Math.round(average * 1000) / 10
}

function bestPricedAvailability(model: MarketplaceModelView): MarketplaceModelAvailability | null {
  return model.availabilities.find((availability) => availability.status === 'available' && hasDisplayPricing(availability.model.pricing))
    ?? model.availabilities.find((availability) => hasDisplayPricing(availability.model.pricing))
    ?? null
}

function modelState(model: MarketplaceModelView): 'good' | 'warn' | 'bad' {
  const percent = modelAvailabilityPercent(model)
  if (availableAvailabilityCount(model) === 0 || percent < 35) {
    return 'bad'
  }
  if (percent < 85 || availableAvailabilityCount(model) < model.availabilities.length) {
    return 'warn'
  }
  return 'good'
}

function modelStateLabel(model: MarketplaceModelView): string {
  const state = modelState(model)
  if (state === 'good') {
    return t('marketplace.healthGood')
  }
  if (state === 'bad') {
    return t('marketplace.statusBusy')
  }
  return t('marketplace.healthModerate')
}

function modelStatusDotClass(model: MarketplaceModelView): string {
  return `marketplace-status-dot is-${modelState(model)}`
}

function recentRequestsForModel(model: MarketplaceModelView) {
  return model.availabilities
    .flatMap((availability) => availability.model.recent_requests ?? [])
    .sort((left, right) => new Date(left.created_at).getTime() - new Date(right.created_at).getTime())
}

function modelRecentHealthDots(model: MarketplaceModelView): MarketplaceRecentHealthDot[] {
  const recentRequests = recentRequestsForModel(model).slice(-3)
  const leadingEmptyCount = Math.max(0, 3 - recentRequests.length)
  return Array.from({ length: 3 }, (_, index) => {
    const request = recentRequests[index - leadingEmptyCount]
    if (!request) {
      return {
        key: `${model.key}-recent-empty-${index}`,
        class: 'card-recent-health-dot is-empty',
      }
    }
    return {
      key: `${model.key}-recent-${index}-${request.created_at}`,
      class: `card-recent-health-dot is-${request.success ? 'success' : 'failed'}`,
    }
  })
}

function modelRecentHealthTitle(model: MarketplaceModelView): string {
  const recentRequests = recentRequestsForModel(model).slice(-3)
  if (recentRequests.length === 0) {
    return `${t('marketplace.recentThreeRequests')} · ${t('marketplace.noRecentRequests')}`
  }
  const successCount = recentRequests.filter((request) => request.success).length
  return `${t('marketplace.recentThreeRequests')} · ${t('marketplace.recentRequestSummary', {
    success: successCount,
    total: recentRequests.length,
  })}`
}

function modelPricingKindLabel(model: MarketplaceModelView): string {
  const availability = bestPricedAvailability(model) ?? model.availabilities[0]
  return availability ? pricingLabel(availability.model.pricing) : t('marketplace.unpriced')
}

function capacityMetricLabel(used?: number, max?: number): string {
  if (!max || max <= 0) {
    return t('marketplace.capacityUnlimited')
  }
  return `${used ?? 0}/${max}`
}

function requestSegmentCountForAvailability(availability: MarketplaceModelAvailability): number {
  const width = requestBarWidths.value[availability.key]
  if (!width || width <= 0) {
    return fallbackRequestSegmentCount
  }
  return Math.max(1, Math.floor((width + requestSegmentGap) / (requestSegmentWidth + requestSegmentGap)))
}

function recentRequestsForAvailability(availability: MarketplaceModelAvailability) {
  return availability.model.recent_requests ?? []
}

function hasRecentRequests(availability: MarketplaceModelAvailability): boolean {
  return recentRequestsForAvailability(availability).length > 0
}

function availabilityRequestSegments(availability: MarketplaceModelAvailability): MarketplaceRequestSegment[] {
  const segmentCount = requestSegmentCountForAvailability(availability)
  const recentRequests = recentRequestsForAvailability(availability)
  if (recentRequests.length === 0) {
    return Array.from({ length: segmentCount }, (_, index) => ({
      key: `${availability.key}-request-empty-${index}`,
      class: 'marketplace-request-segment is-empty',
    }))
  }

  const visibleRequests = recentRequests.slice(-segmentCount)
  const leadingEmptyCount = Math.max(0, segmentCount - visibleRequests.length)
  return Array.from({ length: segmentCount }, (_, index) => {
    const request = visibleRequests[index - leadingEmptyCount]
    if (!request) {
      return {
        key: `${availability.key}-request-pad-${index}`,
        class: 'marketplace-request-segment is-empty',
      }
    }
    return {
      key: `${availability.key}-request-${index}-${request.created_at}`,
      class: `marketplace-request-segment is-${request.success ? 'success' : 'failed'}`,
    }
  })
}

function requestSuccessSummary(availability: MarketplaceModelAvailability): string {
  const recentRequests = recentRequestsForAvailability(availability)
  if (recentRequests.length === 0) {
    return t('marketplace.noRecentRequests')
  }
  const successCount = recentRequests.filter((request) => request.success).length
  return t('marketplace.recentRequestSummary', {
    success: successCount,
    total: recentRequests.length,
  })
}

function updateRequestBarWidth(key: string, element: HTMLElement) {
  const width = Math.round(element.getBoundingClientRect().width)
  if (width > 0 && requestBarWidths.value[key] !== width) {
    requestBarWidths.value = { ...requestBarWidths.value, [key]: width }
  }
}

function setRequestBarRef(key: string, element: unknown) {
  const previous = requestBarElements.get(key)

  if (!(element instanceof HTMLElement)) {
    if (previous) {
      requestBarResizeObserver?.unobserve(previous)
      requestBarKeysByElement.delete(previous)
      requestBarElements.delete(key)
    }
    return
  }

  if (previous === element) {
    updateRequestBarWidth(key, element)
    return
  }

  if (previous) {
    requestBarResizeObserver?.unobserve(previous)
    requestBarKeysByElement.delete(previous)
  }

  requestBarElements.set(key, element)
  requestBarKeysByElement.set(element, key)
  requestBarResizeObserver?.observe(element)
  updateRequestBarWidth(key, element)
}

function requestStatusBarTitle(availability: MarketplaceModelAvailability): string {
  return `${t('marketplace.requestStatusSource')} · ${requestSuccessSummary(availability)}`
}

function lowestRateMultiplier(model: MarketplaceModelView): number {
  return Math.min(...model.availabilities.map((availability) => availability.group.rate_multiplier))
}

function availabilityOfficialRatio(availability: MarketplaceModelAvailability): number | null {
  const ratio = availability.group.official_price_ratio
  return typeof ratio === 'number' && Number.isFinite(ratio) && ratio > 0 ? ratio : null
}

function formatOfficialRatio(ratio: number): string {
  const discount = new Intl.NumberFormat(undefined, {
    minimumFractionDigits: ratio < 1 ? 1 : 0,
    maximumFractionDigits: 2,
  }).format(ratio * 10)

  return t('marketplace.officialDiscountValue', { discount })
}

function officialDiscountLabel(availability: MarketplaceModelAvailability): string {
  const ratio = availabilityOfficialRatio(availability)
  return ratio ? formatOfficialRatio(ratio) : t('marketplace.officialDiscountUnavailable')
}

function scalePricingForOfficial(pricing: MarketplaceModelPricing, multiplier: number): MarketplaceModelPricing {
  if (!Number.isFinite(multiplier) || multiplier <= 0 || multiplier === 1) {
    return pricing
  }

  const scaled: MarketplaceModelPricing = { ...pricing }
  const scale = (value?: number) => hasPositiveValue(value) ? value / multiplier : value
  scaled.input_price_per_token = scale(pricing.input_price_per_token)
  scaled.output_price_per_token = scale(pricing.output_price_per_token)
  scaled.cache_write_price_per_token = scale(pricing.cache_write_price_per_token)
  scaled.cache_read_price_per_token = scale(pricing.cache_read_price_per_token)
  scaled.image_output_price_per_token = scale(pricing.image_output_price_per_token)
  scaled.fast_input_price_per_token = scale(pricing.fast_input_price_per_token)
  scaled.fast_output_price_per_token = scale(pricing.fast_output_price_per_token)
  scaled.fast_cache_write_price_per_token = scale(pricing.fast_cache_write_price_per_token)
  scaled.fast_cache_read_price_per_token = scale(pricing.fast_cache_read_price_per_token)
  scaled.fast_image_output_price_per_token = scale(pricing.fast_image_output_price_per_token)
  scaled.image_price_1k = scale(pricing.image_price_1k)
  scaled.image_price_2k = scale(pricing.image_price_2k)
  scaled.image_price_4k = scale(pricing.image_price_4k)
  scaled.context_intervals = pricing.context_intervals?.map((interval) => ({
    ...interval,
    input_price_per_token: scale(interval.input_price_per_token),
    output_price_per_token: scale(interval.output_price_per_token),
    cache_write_price_per_token: scale(interval.cache_write_price_per_token),
    cache_read_price_per_token: scale(interval.cache_read_price_per_token),
    image_output_price_per_token: scale(interval.image_output_price_per_token),
    fast_input_price_per_token: scale(interval.fast_input_price_per_token),
    fast_output_price_per_token: scale(interval.fast_output_price_per_token),
    fast_cache_write_price_per_token: scale(interval.fast_cache_write_price_per_token),
    fast_cache_read_price_per_token: scale(interval.fast_cache_read_price_per_token),
    fast_image_output_price_per_token: scale(interval.fast_image_output_price_per_token),
  }))
  return scaled
}

function capacityDimensionUsageRatio(used?: number, max?: number): number | null {
  if (!max || max <= 0) {
    return null
  }
  return Math.min(1, Math.max(0, (used ?? 0) / max))
}

function capacityUsageRatio(group: MarketplaceGroup): number {
  const capacity = group.capacity
  if (!capacity) {
    return 0
  }

  const ratios = [
    capacityDimensionUsageRatio(capacity.concurrency_used, capacity.concurrency_max),
    capacityDimensionUsageRatio(capacity.sessions_used, capacity.sessions_max),
    capacityDimensionUsageRatio(capacity.rpm_used, capacity.rpm_max),
  ].filter((ratio): ratio is number => ratio !== null)

  return ratios.length > 0 ? Math.max(...ratios) : 0
}

function detailedPricingRows(availability: MarketplaceModelAvailability): PricingCompareRow[] {
  if (!hasDisplayPricing(availability.model.pricing)) {
    return []
  }

  const officialPricing = scalePricingForOfficial(availability.model.pricing, availability.group.rate_multiplier)
  const actualPricing = availability.model.pricing
  const officialRows = comparablePricingRows(officialPricing, 'name')
  const actualRows = comparablePricingRows(actualPricing, 'symbol')

  return actualRows.map((actualRow) => {
    const officialRow = officialRows.find((row) => row.key === actualRow.key)
    return {
      key: actualRow.key,
      label: actualRow.label,
      official: officialRow?.value ?? '-',
      final: actualRow.value,
    }
  })
}

function comparablePricingRows(pricing: MarketplaceModelPricing, unitStyle: PriceUnitStyle = 'name'): PricingRow[] {
  if (pricingKind(pricing) === 'token' && contextIntervalPricingRows(pricing).length > 0) {
    return contextIntervalPricingRows(pricing, unitStyle).flatMap((interval) =>
      interval.rows
        .filter((row) => ['input', 'output', 'cache_write', 'cache_read'].includes(row.key))
        .map((row) => ({
          key: `${interval.key}-${row.key}`,
          label: `${interval.range} · ${row.label}`,
          value: row.value,
        }))
    )
  }
  if (pricingKind(pricing) === 'token') {
    return tokenPricingRows(pricing, unitStyle).filter((row) =>
      ['input', 'output', 'cache_write', 'cache_read'].includes(row.key)
    )
  }
  if (pricingKind(pricing) === 'image') {
    return imagePricingRows(pricing, unitStyle)
  }
  return []
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

function tokenPricingRowsFromValues(pricing: MarketplaceModelPricing | MarketplacePricingInterval, unitStyle: PriceUnitStyle = 'name'): PricingRow[] {
  const rows: PricingRow[] = []

  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({ key: 'input', label: t('marketplace.input'), value: formatPerMillion(pricing.input_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({ key: 'output', label: t('marketplace.output'), value: formatPerMillion(pricing.output_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.cache_write_price_per_token)) {
    rows.push({ key: 'cache_write', label: t('marketplace.cacheWrite'), value: formatPerMillion(pricing.cache_write_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.cache_read_price_per_token)) {
    rows.push({ key: 'cache_read', label: t('marketplace.cacheRead'), value: formatPerMillion(pricing.cache_read_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.image_output_price_per_token)) {
    rows.push({ key: 'image_output', label: t('marketplace.imageOutput'), value: formatPerMillion(pricing.image_output_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.fast_input_price_per_token)) {
    rows.push({ key: 'fast_input', label: t('marketplace.fastInput'), value: formatPerMillion(pricing.fast_input_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.fast_output_price_per_token)) {
    rows.push({ key: 'fast_output', label: t('marketplace.fastOutput'), value: formatPerMillion(pricing.fast_output_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.fast_cache_write_price_per_token)) {
    rows.push({ key: 'fast_cache_write', label: t('marketplace.fastCacheWrite'), value: formatPerMillion(pricing.fast_cache_write_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.fast_cache_read_price_per_token)) {
    rows.push({ key: 'fast_cache_read', label: t('marketplace.fastCacheRead'), value: formatPerMillion(pricing.fast_cache_read_price_per_token, unitStyle) })
  }
  if (hasPositiveValue(pricing.fast_image_output_price_per_token)) {
    rows.push({ key: 'fast_image_output', label: t('marketplace.fastImageOutput'), value: formatPerMillion(pricing.fast_image_output_price_per_token, unitStyle) })
  }

  return rows
}

function tokenPricingRows(pricing: MarketplaceModelPricing, unitStyle: PriceUnitStyle = 'name'): PricingRow[] {
  return tokenPricingRowsFromValues(pricing, unitStyle)
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
    const rows = compactIntervalTokenPricingRows(interval)
    if (rows.length === 0) {
      return []
    }
    return [{
      key: `compact-${interval.min_tokens}-${interval.max_tokens ?? 'up'}-${index}`,
      label: formatCompactTokenRange(interval.min_tokens, interval.max_tokens),
      value: rows.map((row) => `${row.label} ${row.value}`).join(' / '),
    }]
  }) ?? []
}

function compactIntervalTokenPricingRows(pricing: MarketplacePricingInterval): PricingRow[] {
  const rows: PricingRow[] = []
  if (hasPositiveValue(pricing.input_price_per_token)) {
    rows.push({ key: 'input', label: t('marketplace.input'), value: formatCompactPerMillion(pricing.input_price_per_token) })
  }
  if (hasPositiveValue(pricing.output_price_per_token)) {
    rows.push({ key: 'output', label: t('marketplace.output'), value: formatCompactPerMillion(pricing.output_price_per_token) })
  }
  if (rows.length > 0) {
    return rows
  }

  return tokenPricingRowsFromValues(pricing)
    .filter((row) => !row.key.startsWith('fast_'))
    .slice(0, 2)
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
function contextIntervalPricingRows(pricing: MarketplaceModelPricing, unitStyle: PriceUnitStyle = 'name'): ContextIntervalPricingRow[] {
  return pricing.context_intervals?.flatMap((interval, index) => {
    const rows = tokenPricingRowsFromValues(interval, unitStyle)
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

function imagePricingRows(pricing: MarketplaceModelPricing, unitStyle: PriceUnitStyle = 'name'): PricingRow[] {
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
      value: formatPerImage(item.price, unitStyle),
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
  if (typeof ResizeObserver !== 'undefined') {
    requestBarResizeObserver = new ResizeObserver((entries) => {
      const nextWidths: Record<string, number> = {}
      for (const entry of entries) {
        const element = entry.target instanceof HTMLElement ? entry.target : null
        const key = element ? requestBarKeysByElement.get(element) : undefined
        if (key) {
          nextWidths[key] = Math.round(entry.contentRect.width)
        }
      }
      if (Object.keys(nextWidths).length > 0) {
        requestBarWidths.value = { ...requestBarWidths.value, ...nextWidths }
      }
    })
    for (const [key, element] of requestBarElements.entries()) {
      requestBarResizeObserver.observe(element)
      updateRequestBarWidth(key, element)
    }
  }
  window.addEventListener('keydown', handleMarketplaceKeydown)
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) {
    await appStore.fetchPublicSettings()
  }
  await fetchMarketplace()
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleMarketplaceKeydown)
  requestBarResizeObserver?.disconnect()
  requestBarResizeObserver = null
  requestBarElements.clear()
})
</script>

<style scoped>
.marketplace-page {
  min-width: 0;
}

.marketplace-container {
  max-width: 1200px;
  margin: 0 auto;
  display: grid;
  gap: 20px;
}

.marketplace-container--app {
  max-width: none;
}

.marketplace-page-header {
  margin-bottom: 8px;
}

.marketplace-page-header h1 {
  margin: 4px 0 0;
  color: rgb(17, 24, 39);
  font-size: 24px;
  font-weight: 650;
  line-height: 1.2;
}

.dark .marketplace-page-header h1 {
  color: rgb(255, 255, 255);
}

.marketplace-page-header p {
  margin-top: 8px;
  max-width: 760px;
  color: rgb(107, 114, 128);
  font-size: 14px;
  line-height: 1.65;
}

.dark .marketplace-page-header p {
  color: rgb(148, 163, 184);
}

.marketplace-page-eyebrow {
  color: rgb(79, 70, 229);
  font-size: 12px;
  font-weight: 720;
}

.dark .marketplace-page-eyebrow {
  color: rgb(125, 211, 252);
}

.marketplace-filter-bar {
  position: sticky;
  top: 0;
  z-index: 20;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  border: 1px solid rgba(229, 231, 235, .9);
  border-radius: 14px;
  background: rgba(255, 255, 255, .84);
  padding: 12px;
  box-shadow: 0 1px 3px rgba(15, 23, 42, .04);
  backdrop-filter: blur(18px);
}

.dark .marketplace-filter-bar {
  border-color: rgba(51, 65, 85, .75);
  background: rgba(15, 23, 42, .74);
}

.brand-section-stack {
  display: grid;
  gap: 22px;
}

.brand-section {
  display: grid;
  gap: 16px;
}

.brand-section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
}

.brand-section-header h2 {
  margin: 0;
  color: rgb(17, 24, 39);
  font-size: 18px;
  font-weight: 650;
  line-height: 1.2;
}

.dark .brand-section-header h2 {
  color: rgb(255, 255, 255);
}

.brand-section-icon {
  display: inline-flex;
  width: 40px;
  height: 40px;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
}

.brand-section-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}

.brand-section-meta span,
.brand-refresh-badge {
  display: inline-flex;
  align-items: center;
  border-radius: 6px;
  background: rgb(249, 250, 251);
  padding: 2px 8px;
  color: rgb(107, 114, 128);
  font-size: 12px;
  font-weight: 560;
}

.dark .brand-section-meta span,
.dark .brand-refresh-badge {
  background: rgba(30, 41, 59, .82);
  color: rgb(148, 163, 184);
}

.brand-refresh-badge {
  color: rgb(79, 70, 229);
  background: rgb(238, 242, 255);
}

.dark .brand-refresh-badge {
  color: rgb(125, 211, 252);
  background: rgba(14, 165, 233, .14);
}

.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(300px, 1fr));
  gap: 20px;
}

.model-card {
  overflow: hidden;
  border: 1px solid rgb(229, 231, 235);
  border-radius: 12px;
  background: rgb(255, 255, 255);
  padding: 20px;
  cursor: pointer;
  box-shadow: 0 1px 3px rgba(0, 0, 0, .05);
  transition: border-color .2s ease, box-shadow .2s ease, transform .2s ease, background-color .2s ease;
}

.model-card:hover,
.model-card:focus-visible {
  transform: translateY(-2px);
  border-color: rgb(79, 70, 229);
  box-shadow: 0 10px 15px -3px rgba(0, 0, 0, .10);
  outline: none;
}

.dark .model-card {
  border-color: rgba(51, 65, 85, .9);
  background: rgba(15, 23, 42, .86);
  box-shadow: 0 1px 3px rgba(0, 0, 0, .25);
}

.dark .model-card:hover,
.dark .model-card:focus-visible {
  border-color: rgb(14, 165, 233);
  box-shadow: 0 16px 30px rgba(14, 165, 233, .12);
}

.card-header-row {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 16px;
}

.model-icon {
  display: flex;
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: rgb(238, 242, 255);
  color: rgb(79, 70, 229);
  font-size: 18px;
  font-weight: 700;
}

.model-icon--large {
  width: 44px;
  height: 44px;
}

.dark .model-icon {
  background: rgba(14, 165, 233, .14);
  color: rgb(125, 211, 252);
}

.model-title {
  overflow: hidden;
  color: rgb(17, 24, 39);
  font-size: 18px;
  font-weight: 650;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .model-title {
  color: rgb(255, 255, 255);
}

.model-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 5px;
}

.model-provider {
  display: inline-flex;
  width: fit-content;
  border-radius: 4px;
  background: rgb(249, 250, 251);
  padding: 2px 8px;
  color: rgb(107, 114, 128);
  font-size: 12px;
  line-height: 1.35;
}

.dark .model-provider {
  background: rgba(30, 41, 59, .85);
  color: rgb(148, 163, 184);
}

.card-stats {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-bottom: 12px;
  border-bottom: 1px dashed rgb(229, 231, 235);
  padding-bottom: 12px;
  color: rgb(107, 114, 128);
}

.dark .card-stats {
  border-bottom-color: rgba(51, 65, 85, .9);
  color: rgb(148, 163, 184);
}

.card-stat-item {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 4px;
}

.card-stat-label {
  min-width: 0;
  overflow: hidden;
  color: rgb(107, 114, 128);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: .01em;
  line-height: 1.25;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .card-stat-label {
  color: rgb(148, 163, 184);
}

.card-stat-value {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 3px 8px;
  color: rgb(17, 24, 39);
  font-size: 13px;
  font-weight: 650;
  line-height: 1.35;
}

.card-stat-value-part {
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .card-stat-value {
  color: rgb(226, 232, 240);
}

.card-footer-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  color: rgb(107, 114, 128);
  font-size: 12px;
  font-weight: 500;
}

.card-footer-row > span {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-footer-meta {
  gap: 8px;
}

.card-footer-meta > span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-footer-meta > span:last-child {
  color: rgb(75, 85, 99);
}

.card-footer-meta > span + span {
  position: relative;
  padding-left: 8px;
}

.card-footer-meta > span + span::before {
  position: absolute;
  left: 0;
  color: rgb(203, 213, 225);
  content: '·';
}

.dark .card-footer-meta > span + span::before {
  color: rgb(71, 85, 105);
}

.dark .card-footer-meta > span:last-child {
  color: rgb(203, 213, 225);
}

.dark .card-footer-row {
  color: rgb(148, 163, 184);
}

.card-health-summary {
  gap: 8px;
}

.card-health-summary > span:first-child {
  display: inline-flex;
  min-width: 0;
  align-items: center;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.card-recent-health-dots {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
}

.card-recent-health-dot {
  width: 6px;
  height: 6px;
  border-radius: 999px;
  background: rgb(16, 185, 129);
  box-shadow: 0 0 0 2px rgba(16, 185, 129, .12);
}

.card-recent-health-dot.is-failed {
  background: rgb(239, 68, 68);
  box-shadow: 0 0 0 2px rgba(239, 68, 68, .12);
}

.card-recent-health-dot.is-empty {
  background: rgb(203, 213, 225);
  box-shadow: none;
  opacity: .62;
}

.dark .card-recent-health-dot.is-empty {
  background: rgb(71, 85, 105);
  opacity: .72;
}

.marketplace-status-dot,
.status-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  margin-right: 4px;
  border-radius: 999px;
  background: rgb(16, 185, 129);
}

.marketplace-status-dot.is-warn,
.status-dot.is-warn {
  background: rgb(251, 191, 36);
}

.marketplace-status-dot.is-bad,
.status-dot.is-bad {
  background: rgb(239, 68, 68);
}

.detail-overlay {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  background: rgba(17, 24, 39, .40);
  opacity: 0;
  pointer-events: none;
  backdrop-filter: blur(4px);
  transition: opacity .3s ease;
}

.detail-overlay.active {
  opacity: 1;
  pointer-events: auto;
}

.detail-modal {
  width: 95%;
  max-width: 1100px;
  height: 90vh;
  overflow: hidden;
  border-radius: 20px 20px 0 0;
  background: rgb(249, 250, 251);
  box-shadow: 0 -10px 40px rgba(0, 0, 0, .10);
  transform: translateY(100%);
  transition: transform .4s cubic-bezier(.16, 1, .3, 1);
}

.detail-overlay.active .detail-modal {
  transform: translateY(0);
}

.dark .detail-modal {
  background: rgb(2, 6, 23);
  box-shadow: 0 -16px 46px rgba(0, 0, 0, .45);
}

.detail-modal-inner {
  display: flex;
  height: 100%;
  min-height: 0;
  flex-direction: column;
}

.detail-modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  border-bottom: 1px solid rgb(229, 231, 235);
  background: rgb(255, 255, 255);
  padding: 20px 30px;
}

.dark .detail-modal-header {
  border-bottom-color: rgba(51, 65, 85, .9);
  background: rgb(15, 23, 42);
}

.header-left {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 15px;
}

.header-title-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.header-title-row h2 {
  margin: 0;
  overflow: hidden;
  color: rgb(17, 24, 39);
  font-size: 22px;
  font-weight: 650;
  line-height: 1.2;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .header-title-row {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 10px;
}

.dark .header-title-row h2 {
  color: rgb(255, 255, 255);
}

.header-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 7px;
}

.header-tags span {
  border-radius: 4px;
  background: rgb(249, 250, 251);
  padding: 4px 8px;
  color: rgb(107, 114, 128);
  font-size: 12px;
  line-height: 1.2;
}

.dark .header-tags span {
  background: rgba(30, 41, 59, .88);
  color: rgb(148, 163, 184);
}

.header-model-id-row {
  display: inline-flex;
  max-width: min(360px, 45vw);
  align-items: center;
  gap: 8px;
  border: 1px solid rgba(209, 213, 219, .8);
  border-radius: 999px;
  background: rgba(249, 250, 251, .9);
  padding: 4px 5px 4px 10px;
  color: rgb(107, 114, 128);
  font-size: 12px;
}

.dark .header-model-id-row {
  border-color: rgba(51, 65, 85, .9);
  background: rgba(15, 23, 42, .92);
  color: rgb(148, 163, 184);
}

.header-model-id-row code {
  min-width: 0;
  overflow: hidden;
  color: rgb(17, 24, 39);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
  font-weight: 620;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .header-model-id-row code {
  color: rgb(226, 232, 240);
}

.header-model-id-copy-btn {
  flex-shrink: 0;
  border: 1px solid rgba(14, 165, 233, .35);
  border-radius: 999px;
  background: rgba(14, 165, 233, .12);
  padding: 3px 8px;
  color: rgb(3, 105, 161);
  font-size: 12px;
  font-weight: 620;
  line-height: 1.2;
  transition: background-color .16s ease, border-color .16s ease, color .16s ease;
}

.header-model-id-copy-btn:hover {
  border-color: rgba(14, 165, 233, .55);
  background: rgba(14, 165, 233, .18);
  color: rgb(2, 132, 199);
}

.dark .header-model-id-copy-btn {
  border-color: rgba(14, 165, 233, .32);
  background: rgba(14, 165, 233, .14);
  color: rgb(125, 211, 252);
}

.close-btn {
  border: none;
  background: none;
  color: rgb(107, 114, 128);
  cursor: pointer;
  font-size: 28px;
  line-height: 1;
  transition: color .18s ease, transform .18s ease;
}

.close-btn:hover {
  transform: scale(1.05);
  color: rgb(17, 24, 39);
}

.dark .close-btn:hover {
  color: rgb(255, 255, 255);
}

.detail-modal-body {
  display: flex;
  flex: 1;
  gap: 24px;
  min-height: 0;
  overflow-y: auto;
  padding: 20px 30px;
}

.left-column {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  gap: 16px;
}

.left-column > h3 {
  margin: 0 0 -2px;
  color: rgb(107, 114, 128);
  font-size: 14px;
  font-weight: 620;
}

.dark .left-column > h3 {
  color: rgb(148, 163, 184);
}

.group-card {
  position: relative;
  overflow: hidden;
  border: 1px solid rgb(229, 231, 235);
  border-radius: 12px;
  background: rgb(255, 255, 255);
  padding: 20px;
  cursor: pointer;
  box-shadow: 0 1px 2px rgba(0, 0, 0, .02);
  transition: border-color .2s ease, box-shadow .2s ease, transform .2s ease;
}

.group-card:hover {
  border-color: rgb(199, 210, 254);
  box-shadow: 0 4px 6px -1px rgba(0, 0, 0, .05);
}

.group-card.active {
  border-color: rgb(79, 70, 229);
  box-shadow: 0 0 0 1px rgb(79, 70, 229);
}

.group-card::before {
  content: '';
  position: absolute;
  inset: 0 auto 0 0;
  width: 4px;
  background: transparent;
  transition: background-color .2s ease;
}

.group-card.active::before {
  background: rgb(79, 70, 229);
}

.dark .group-card {
  border-color: rgba(51, 65, 85, .9);
  background: rgb(15, 23, 42);
}

.dark .group-card:hover {
  border-color: rgba(14, 165, 233, .42);
}

.dark .group-card.active {
  border-color: rgb(14, 165, 233);
  box-shadow: 0 0 0 1px rgb(14, 165, 233);
}

.dark .group-card.active::before {
  background: rgb(14, 165, 233);
}

.uptime-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 14px;
  margin-bottom: 12px;
}

.group-title {
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 8px;
  overflow: hidden;
  color: rgb(17, 24, 39);
  font-size: 15px;
  font-weight: 650;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .group-title {
  color: rgb(255, 255, 255);
}

.uptime-percent {
  flex-shrink: 0;
  color: rgb(107, 114, 128);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 13px;
}

.dark .uptime-percent {
  color: rgb(148, 163, 184);
}

.uptime-bars-wrapper {
  display: flex;
  align-items: stretch;
  justify-content: flex-start;
  gap: 2px;
  width: 100%;
  height: 32px;
  margin-bottom: 16px;
  overflow: hidden;
}

.marketplace-request-segment {
  flex: 0 0 6px;
  width: 6px;
  height: 100%;
  min-width: 6px;
  border-radius: 2px;
  background: rgb(16, 185, 129);
  opacity: .90;
  transition: opacity .2s ease, filter .2s ease, transform .2s ease;
}

.marketplace-request-segment:hover {
  opacity: 1;
  filter: brightness(1.1);
}

.marketplace-request-segment.is-failed {
  background: rgb(239, 68, 68);
}

.marketplace-request-segment.is-empty {
  background: rgb(229, 231, 235);
  opacity: .65;
}

.dark .marketplace-request-segment.is-empty {
  background: rgb(51, 65, 85);
  opacity: .75;
}

.metrics-row {
  display: flex;
  align-items: center;
  gap: 16px;
  border-top: 1px solid rgb(249, 250, 251);
  padding-top: 12px;
  color: rgb(107, 114, 128);
  font-size: 12px;
}

.dark .metrics-row {
  border-top-color: rgba(30, 41, 59, .9);
  color: rgb(148, 163, 184);
}

.concurrency-wrapper {
  display: flex;
  min-width: 160px;
  flex: 1;
  align-items: center;
  gap: 8px;
}

.concurrency-wrapper > span {
  flex-shrink: 0;
}

.progress-bg {
  flex: 1;
  max-width: 100px;
  height: 6px;
  overflow: hidden;
  border-radius: 3px;
  background: rgb(249, 250, 251);
}

.dark .progress-bg {
  background: rgb(30, 41, 59);
}

.progress-fill {
  height: 100%;
  border-radius: 3px;
  background: rgb(16, 185, 129);
}

.progress-fill.is-warn {
  background: rgb(251, 191, 36);
}

.progress-fill.is-bad {
  background: rgb(239, 68, 68);
}

.metric-badge {
  flex-shrink: 0;
  border-radius: 4px;
  background: rgb(249, 250, 251);
  padding: 4px 8px;
  color: rgb(17, 24, 39);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 12px;
}

.metric-badge.highlight {
  color: rgb(79, 70, 229);
  background: rgb(238, 242, 255);
}

.dark .metric-badge {
  background: rgb(30, 41, 59);
  color: rgb(226, 232, 240);
}

.dark .metric-badge.highlight {
  color: rgb(125, 211, 252);
  background: rgba(14, 165, 233, .14);
}

.right-column {
  width: 420px;
  flex-shrink: 0;
}

.sticky-panel {
  position: sticky;
  top: 0;
  border: 1px solid rgb(229, 231, 235);
  border-radius: 12px;
  background: rgb(255, 255, 255);
  padding: 24px;
  box-shadow: 0 4px 6px rgba(0, 0, 0, .02);
}

.dark .sticky-panel {
  border-color: rgba(51, 65, 85, .9);
  background: rgb(15, 23, 42);
}

.panel-title {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 24px;
  color: rgb(17, 24, 39);
  font-size: 16px;
  font-weight: 650;
}

.dark .panel-title {
  color: rgb(255, 255, 255);
}

.sharing-badge {
  flex-shrink: 0;
  border-radius: 4px;
  background: rgb(238, 242, 255);
  padding: 2px 8px;
  color: rgb(79, 70, 229);
  font-size: 12px;
  font-weight: 560;
}

.dark .sharing-badge {
  background: rgba(14, 165, 233, .14);
  color: rgb(125, 211, 252);
}

.price-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}

.price-table th,
.price-table td {
  border-bottom: 1px solid rgb(229, 231, 235);
  padding: 14px 8px;
  text-align: left;
  vertical-align: top;
}

.dark .price-table th,
.dark .price-table td {
  border-bottom-color: rgba(51, 65, 85, .9);
}

.price-table th {
  color: rgb(107, 114, 128);
  font-size: 13px;
  font-weight: 400;
}

.dark .price-table th {
  color: rgb(148, 163, 184);
}

.price-table td {
  color: rgb(17, 24, 39);
}

.dark .price-table td {
  color: rgb(226, 232, 240);
}

.price-table td.num {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
}

.price-table td.final-price {
  color: rgb(79, 70, 229);
  font-weight: 650;
}

.dark .price-table td.final-price {
  color: rgb(125, 211, 252);
}

.price-table tr:last-child td {
  border-bottom: none;
}

.pricing-empty {
  border: 1px dashed rgb(229, 231, 235);
  border-radius: 12px;
  padding: 18px;
  color: rgb(107, 114, 128);
  font-size: 14px;
  line-height: 1.6;
}

.dark .pricing-empty {
  border-color: rgba(51, 65, 85, .9);
  color: rgb(148, 163, 184);
}

.pricing-panel-enter-active,
.pricing-panel-leave-active {
  transition: opacity .12s ease, transform .12s ease;
}

.pricing-panel-enter-from,
.pricing-panel-leave-to {
  opacity: 0;
  transform: translateY(4px);
}

@media (max-width: 900px) {
  .detail-modal-body {
    flex-direction: column;
  }

  .right-column {
    width: 100%;
  }
}

@media (max-width: 640px) {
  .marketplace-container {
    gap: 16px;
  }

  .brand-section-header,
  .detail-modal-header,
  .uptime-header,
  .metrics-row {
    align-items: flex-start;
    flex-direction: column;
  }

  .card-grid {
    grid-template-columns: minmax(0, 1fr);
  }

  .detail-modal {
    width: 100%;
    height: 92vh;
    border-radius: 18px 18px 0 0;
  }

  .detail-modal-body,
  .detail-modal-header {
    padding-left: 18px;
    padding-right: 18px;
  }

  .concurrency-wrapper {
    width: 100%;
  }
}

@media (prefers-reduced-motion: reduce) {
  .model-card,
  .detail-overlay,
  .detail-modal,
  .group-card,
  .marketplace-request-segment,
  .close-btn {
    transition-duration: 1ms;
  }
}
</style>
