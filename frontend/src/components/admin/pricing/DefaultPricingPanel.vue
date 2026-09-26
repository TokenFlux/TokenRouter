<template>
  <TablePageLayout>
    <template #filters>
      <div class="space-y-3">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.pricing.defaults.description') }}</p>
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-3">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon name="search" size="md" class="input-icon text-gray-400 dark:text-gray-500" />
              <input v-model="search" class="input input-has-icon" :placeholder="t('admin.pricing.defaults.search')" :aria-label="t('admin.pricing.defaults.search')" />
            </div>
            <div ref="filterDropdown" class="relative shrink-0" @keydown.esc.stop.prevent="showFilters = false">
              <button type="button" class="btn btn-secondary relative btn-icon" :aria-label="t('common.filter')" :title="t('common.filter')" :aria-expanded="showFilters" @click="showFilters = !showFilters">
                <Icon name="filter" size="sm" />
                <span v-if="activeFilterCount" class="absolute -right-1 -top-1 inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary-100 px-1.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">{{ activeFilterCount }}</span>
              </button>
              <div v-if="showFilters" class="absolute left-auto right-0 top-full z-modal-nested mt-2 w-72 rounded-surface border border-gray-200 bg-white p-4 shadow-xl dark:border-dark-600 dark:bg-dark-900 sm:left-0 sm:right-auto" @click.stop>
                <div class="mb-3 flex items-center justify-between">
                  <div class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('common.filter') }}</div>
                  <button v-if="activeFilterCount" type="button" class="text-xs font-medium text-primary-600 dark:text-primary-400" @click="platform = ''; mode = ''">{{ t('common.reset') }}</button>
                </div>
                <div class="space-y-3">
                  <Select v-model="platform" :options="platformOptions" :aria-label="t('admin.pricing.defaults.columns.platform')" />
                  <Select v-model="mode" :options="modeOptions" :aria-label="t('admin.pricing.defaults.columns.billing_mode')" />
                </div>
              </div>
            </div>
          </div>
          <div class="ml-auto flex shrink-0 flex-wrap items-center justify-end gap-3">
            <button type="button" class="btn btn-secondary btn-icon" :disabled="loading || updating" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="load">
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button type="button" class="btn btn-primary whitespace-nowrap" :disabled="loading || updating" :title="t('admin.pricing.defaults.updateHint')" @click="updateCatalog">
              <Icon :name="updating ? 'refresh' : 'download'" size="md" class="mr-2" :class="updating ? 'animate-spin' : ''" />
              {{ t(updating ? 'admin.pricing.defaults.updating' : 'admin.pricing.defaults.update') }}
            </button>
          </div>
        </div>
        <p v-if="updatedAt && !updatedAt.startsWith('0001')" class="text-xs text-gray-500">{{ t('admin.pricing.defaults.updatedAt') }} {{ new Date(updatedAt).toLocaleString() }}</p>
        <p v-if="notice" role="status" class="text-sm text-emerald-600 dark:text-emerald-400">{{ notice }}</p>
        <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      </div>
    </template>
    <template #table>
      <DataTable :columns="columns" :data="items" :loading="loading">
        <template #cell-billing_mode="{ row }">{{ t(`admin.pricing.defaults.modes.${row.billing_mode}`, row.billing_mode) }}</template>
        <template #cell-price="{ row }">
          <span v-if="row.price_status === 'unpriced'">{{ t('admin.pricing.defaults.unpriced') }}</span>
          <div v-else class="space-y-1">
            <div v-for="price in row.prices.slice(0, 2)" :key="price.key" class="text-sm">{{ priceLabel(price.key) }}: {{ formatPrice(price) }}</div>
          </div>
        </template>
        <template #cell-actions="{ row }">
          <button type="button" class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400" @click="selected = row">
            <Icon name="eye" size="sm" />
            <span class="text-xs">{{ t('admin.pricing.defaults.details') }}</span>
          </button>
        </template>
        <template #empty><p class="p-6 text-center text-sm text-gray-500">{{ t('admin.pricing.defaults.empty') }}</p></template>
      </DataTable>
    </template>
    <template #pagination>
      <Pagination :page="page" :page-size="pageSize" :total="total" @update:page="page = $event" @update:page-size="pageSize = $event; page = 1" />
    </template>
  </TablePageLayout>
  <BaseDialog :show="!!selected" :title="selected?.model || ''" @close="selected = null">
    <div v-if="selected" class="space-y-4">
      <p class="text-sm text-gray-500">{{ selected.platform }} · {{ t(`admin.pricing.defaults.modes.${selected.billing_mode}`, selected.billing_mode) }}</p>
      <p v-if="selected.price_status === 'unpriced'" class="text-sm">{{ t('admin.pricing.defaults.unpriced') }}</p>
      <dl v-else class="divide-y divide-gray-200 dark:divide-dark-700">
        <div v-for="price in selected.prices" :key="price.key" class="flex justify-between gap-4 py-3 text-sm">
          <dt>{{ priceLabel(price.key) }}</dt><dd class="text-right font-mono">{{ formatPrice(price) }}</dd>
        </div>
      </dl>
      <p v-if="selected.long_context_threshold" class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.pricing.defaults.longContext', { threshold: selected.long_context_threshold, operator: selected.long_context_threshold_inclusive ? '≥' : '>' }) }}</p>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import { listDefaultPricing, updateDefaultPricing, type DefaultModelPrice, type DefaultPriceValue } from '@/api/admin/pricing'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'

const { t } = useI18n()
const items = ref<DefaultModelPrice[]>([])
const selected = ref<DefaultModelPrice | null>(null)
const search = ref('')
const platform = ref('')
const mode = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(false)
const updating = ref(false)
const notice = ref('')
const showFilters = ref(false)
const filterDropdown = ref<HTMLElement | null>(null)
const activeFilterCount = computed(() => Number(!!platform.value) + Number(!!mode.value))
const error = ref('')
const updatedAt = ref('')
let controller: AbortController | undefined
let updateController: AbortController | undefined
let disposed = false
const platforms = ref<string[]>([])
const platformOptions = computed(() => [{ value: '', label: t('admin.pricing.defaults.allPlatforms') }, ...platforms.value.map(value => ({ value, label: t(`admin.groups.platforms.${value}`, value) }))])
const modeOptions = computed(() => [{ value: '', label: t('admin.pricing.defaults.allModes') }, ...['token', 'image', 'video', 'per_request'].map(value => ({ value, label: t(`admin.pricing.defaults.modes.${value}`) }))])
const columns = computed(() => ['model', 'platform', 'billing_mode', 'price', 'actions'].map(key => ({ key, label: t(`admin.pricing.defaults.columns.${key}`) })))
function priceLabel(key: string): string {
  const prefix = key.startsWith('fast_') ? 'Fast ' : key.startsWith('flex_') ? 'Flex ' : ''
  return prefix + t(`admin.pricing.defaults.keys.${key.replace(/^(fast_|flex_)/, '')}`, key.replace(/^(fast_|flex_)/, ''))
}
function formatPrice(price: DefaultPriceValue): string {
  if (price.value == null) return t('admin.pricing.defaults.notApplicable')
  const unit = price.unit === 'multiplier' ? '×' : price.unit
  return `${price.value.toLocaleString(undefined, { maximumSignificantDigits: 8 })} ${unit}`
}
async function load() {
  if (disposed) return
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = ''
  try {
    const result = await listDefaultPricing({ page: page.value, page_size: pageSize.value, search: search.value, platform: platform.value, billing_mode: mode.value }, current.signal)
    if (current.signal.aborted) return
    items.value = result.items
    total.value = result.total
    updatedAt.value = result.last_updated
    platforms.value = result.platforms ?? platforms.value
  } catch {
    if (!current.signal.aborted) error.value = t('admin.pricing.loadError')
  } finally { if (!current.signal.aborted) loading.value = false }
}

// 更新与只读刷新分开，更新成功后保留当前筛选条件重新查询一次。
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

function closeFiltersOutside(event: MouseEvent) {
  if (event.target instanceof Node && !filterDropdown.value?.contains(event.target)) showFilters.value = false
}
const searchChanged = useDebounceFn(() => { if (page.value !== 1) page.value = 1; else void load() }, SEARCH_DEBOUNCE_MS)
watch(search, searchChanged)
watch([platform, mode], () => { if (page.value !== 1) page.value = 1; else void load() })
watch([page, pageSize], load, { immediate: true })
onMounted(() => document.addEventListener('click', closeFiltersOutside))
onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
  updateController?.abort()
  document.removeEventListener('click', closeFiltersOutside)
})
</script>
