<template>
  <div class="flex min-w-0 flex-nowrap items-center gap-3">
    <SearchInput
      :model-value="searchQuery"
      :placeholder="t('admin.accounts.searchAccounts')"
      class="min-w-0 flex-1 sm:flex-none sm:w-56 lg:w-52"
      @update:model-value="$emit('update:searchQuery', $event)"
      @search="$emit('change')"
    />

    <div ref="filterPanelRef" class="relative shrink-0">
      <button
        type="button"
        data-testid="account-filters-toggle"
        class="btn btn-secondary relative h-9 w-9 p-0"
        :class="activeFilterCount > 0 ? 'border-primary-400 text-primary-700 dark:border-primary-500 dark:text-primary-300' : ''"
        :aria-expanded="showFilters"
        :aria-label="t('common.filter')"
        :title="t('common.filter')"
        @click="toggleFilters"
      >
        <Icon name="filter" size="sm" />
        <span
          v-if="activeFilterCount > 0"
          class="pointer-events-none absolute -right-1 -top-1 inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-primary-100 px-1.5 text-xs font-semibold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
        >
          {{ activeFilterCount }}
        </span>
      </button>

      <div
        v-if="showFilters"
        class="absolute left-auto right-0 top-full z-[60] mt-2 w-[min(34rem,calc(100vw-2rem))] max-w-[calc(100vw-2rem)] rounded-surface border border-gray-200 bg-white shadow-xl dark:border-dark-600 dark:bg-dark-900 sm:left-0 sm:right-auto"
        @click.stop
      >
        <div class="flex items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <div>
            <div class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('common.filter') }}</div>
            <div class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.filterHint') }}</div>
          </div>
          <button
            v-if="activeFilterCount > 0"
            type="button"
            class="text-xs font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
            @click="clearFilters"
          >
            {{ t('common.reset') }}
          </button>
        </div>

        <div class="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.accounts.columns.platform') }}</label>
            <Select :model-value="filters.platform" class="w-full" :options="pOpts" @update:model-value="updatePlatform" @change="$emit('change')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.columns.type') }}</label>
            <Select :model-value="filters.type" class="w-full" :options="tOpts" @update:model-value="updateType" @change="$emit('change')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.columns.status') }}</label>
            <Select :model-value="filters.status" class="w-full" :options="sOpts" @update:model-value="updateStatus" @change="$emit('change')" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.privacyFilter') }}</label>
            <Select :model-value="filters.privacy_mode" class="w-full" :options="privacyOpts" @update:model-value="updatePrivacyMode" @change="$emit('change')" />
          </div>
          <div class="sm:col-span-2">
            <label class="input-label">{{ t('admin.accounts.columns.groups') }}</label>
            <Select :model-value="filters.group" class="w-full" :options="gOpts" searchable @update:model-value="updateGroup" @change="$emit('change')" />
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import SearchInput from '@/components/common/SearchInput.vue'
import Icon from '@/components/icons/Icon.vue'
import type { AdminGroup } from '@/types'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

const props = defineProps<{ searchQuery: string; filters: Record<string, any>; groups?: AdminGroup[] }>()
const emit = defineEmits(['update:searchQuery', 'update:filters', 'change'])
const { t } = useI18n()

const showFilters = ref(false)
const filterPanelRef = ref<HTMLElement | null>(null)
const filterKeys = ['platform', 'type', 'status', 'privacy_mode', 'group'] as const

const activeFilterCount = computed(() => filterKeys.filter((key) => String(props.filters?.[key] ?? '').trim() !== '').length)

const updatePlatform = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, platform: value }) }
const updateType = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, type: value }) }
const updateStatus = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, status: value }) }
const updatePrivacyMode = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, privacy_mode: value }) }
const updateGroup = (value: string | number | boolean | null) => { emit('update:filters', { ...props.filters, group: value }) }

const clearFilters = () => {
  const nextFilters = { ...props.filters }
  for (const key of filterKeys) nextFilters[key] = ''
  emit('update:filters', nextFilters)
  emit('change')
}

const toggleFilters = () => {
  showFilters.value = !showFilters.value
}

const handleDocumentClick = (event: MouseEvent) => {
  if (!showFilters.value || !filterPanelRef.value) return
  const target = event.target
  if (target instanceof Node && filterPanelRef.value.contains(target)) return
  // Select 的候选菜单挂载到 body，选择选项时不要提前关闭筛选面板。
  if (target instanceof Element && target.closest('.select-dropdown-portal')) return
  showFilters.value = false
}

const handleKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') showFilters.value = false
}

onMounted(() => {
  document.addEventListener('click', handleDocumentClick)
  document.addEventListener('keydown', handleKeydown)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleDocumentClick)
  document.removeEventListener('keydown', handleKeydown)
})

const pOpts = computed(() => [{ value: '', label: t('admin.accounts.allPlatforms') }, ...CONCRETE_PLATFORM_OPTIONS])
const tOpts = computed(() => [
  { value: '', label: t('admin.accounts.allTypes') },
  { value: 'oauth', label: t('admin.accounts.oauthType') },
  { value: 'setup-token', label: t('admin.accounts.setupToken') },
  { value: 'apikey', label: t('admin.accounts.apiKey') },
  { value: 'service_account', label: t('admin.accounts.serviceAccount') },
  { value: 'bedrock', label: 'AWS Bedrock' },
  { value: 'cosy', label: t('admin.accounts.types.qoderCosy') }
])
const sOpts = computed(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'error', label: t('admin.accounts.status.error') },
  { value: 'rate_limited', label: t('admin.accounts.status.rateLimited') },
  { value: 'temp_unschedulable', label: t('admin.accounts.status.tempUnschedulable') },
  { value: 'unschedulable', label: t('admin.accounts.status.unschedulable') }
])
const privacyOpts = computed(() => [
  { value: '', label: t('admin.accounts.allPrivacyModes') },
  { value: '__unset__', label: t('admin.accounts.privacyUnset') },
  { value: 'training_off', label: 'Privacy' },
  { value: 'training_set_cf_blocked', label: 'CF' },
  { value: 'training_set_failed', label: 'Fail' }
])
const gOpts = computed(() => [
  { value: '', label: t('admin.accounts.allGroups') },
  { value: 'ungrouped', label: t('admin.accounts.ungroupedGroup') },
  ...(props.groups || []).map(g => ({
    value: String(g.id),
    // 管理端保留禁用分组可见性，后缀只提示状态，不阻止筛选。
    label: g.status === 'active' ? g.name : `${g.name} (${t('common.inactive')})`
  }))
])
</script>
