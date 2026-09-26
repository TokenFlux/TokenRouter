<template>
  <TablePageLayout>
    <template #filters>
      <div class="space-y-3">
        <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.pricing.defaults.description') }}</p>
        <div class="flex flex-wrap items-center gap-2">
          <input v-model="search" class="input min-w-0 flex-1" :placeholder="t('admin.pricing.defaults.search')" :aria-label="t('admin.pricing.defaults.search')" />
          <Select v-model="platform" :options="platformOptions" class="w-40" />
          <Select v-model="mode" :options="modeOptions" class="w-40" />
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="load">{{ t('common.refresh') }}</button>
        </div>
        <p v-if="updatedAt && !updatedAt.startsWith('0001')" class="text-xs text-gray-500">{{ t('admin.pricing.defaults.updatedAt') }} {{ new Date(updatedAt).toLocaleString() }}</p>
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
        <template #cell-actions="{ row }"><button type="button" class="btn btn-secondary btn-sm" @click="selected = row">{{ t('admin.pricing.defaults.details') }}</button></template>
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
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDebounceFn } from '@vueuse/core'
import { listDefaultPricing, type DefaultModelPrice, type DefaultPriceValue } from '@/api/admin/pricing'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
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
const error = ref('')
const updatedAt = ref('')
let controller: AbortController | undefined
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
const searchChanged = useDebounceFn(() => { if (page.value !== 1) page.value = 1; else void load() }, SEARCH_DEBOUNCE_MS)
watch(search, searchChanged)
watch([platform, mode], () => { if (page.value !== 1) page.value = 1; else void load() })
watch([page, pageSize], load, { immediate: true })
onBeforeUnmount(() => controller?.abort())
</script>
