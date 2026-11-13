<template>
  <!-- The heading toolbar wraps on narrow screens, with its width capped by page padding and controls wrapping internally. -->
  <div
    class="flex max-w-[calc(100vw-2rem)] flex-wrap items-center gap-2 md:max-w-[calc(100vw-3rem)]"
  >
    <FilterDropdown :active-count="activeCount" @reset="resetFilters">
      <FilterField :label="t('usage.apiKeyFilter')" icon="key">
        <FilterOptions
          v-model="filters.api_key_id"
          :options="apiKeyOptions"
          searchable
          data-testid="usage-filter-api-key"
          @change="applyFilters"
        />
      </FilterField>
      <FilterField :label="t('usage.model')" icon="cube">
        <FilterOptions
          v-model="filters.model"
          :options="modelOptions"
          searchable
          data-testid="usage-filter-model"
          @change="applyFilters"
        >
          <template #icon="{ value }">
            <ModelIcon :model="String(value)" size="16px" hide-fallback />
          </template>
        </FilterOptions>
      </FilterField>
      <FilterField :label="t('admin.usage.group')" icon="folder">
        <FilterOptions
          v-model="filters.group_id"
          :options="groupOptions"
          searchable
          data-testid="usage-filter-group"
          @change="applyFilters"
        >
          <template #icon="{ option }">
            <ProviderIcon
              v-if="option?.brand"
              :brand="String(option.brand)"
              size="16px"
              hide-fallback
            />
          </template>
        </FilterOptions>
      </FilterField>
      <FilterField :label="t('usage.type')" icon="bolt">
        <FilterOptions
          v-model="filters.request_type"
          :options="requestTypeOptions"
          data-testid="usage-filter-request-type"
          @change="applyFilters"
        />
      </FilterField>
      <FilterField :label="t('admin.usage.billingType')" icon="creditCard">
        <FilterOptions
          v-model="filters.billing_type"
          :options="billingTypeOptions"
          data-testid="usage-filter-billing-type"
          @change="applyFilters"
        />
      </FilterField>
      <FilterField :label="t('admin.usage.billingMode')" icon="calculator">
        <FilterOptions
          v-model="filters.billing_mode"
          :options="billingModeOptions"
          data-testid="usage-filter-billing-mode"
          @change="applyFilters"
        />
      </FilterField>
      <FilterField :label="t('usage.compactionFilter')" icon="minimize">
        <FilterOptions
          v-model="filters.native_compaction_v2"
          :options="compactionOptions"
          data-testid="usage-filter-compaction"
          @change="applyFilters"
        />
      </FilterField>
    </FilterDropdown>
    <button
      type="button"
      class="btn btn-secondary shrink-0 btn-icon"
      :disabled="refreshing"
      :title="t('common.refresh')"
      :aria-label="t('common.refresh')"
      data-testid="usage-refresh"
      @click="emit('refresh')"
    >
      <Icon name="refresh" size="sm" :class="refreshing ? 'animate-spin' : ''" />
    </button>
    <DateRangePicker :start-date="pickerStart" :end-date="pickerEnd" @change="selectCustomRange" />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { injectUsageChartState } from './usageChartState'

// UserDashboardUsageToolbar.vue renders the dashboard date picker, usage filters, and
// refresh action in the page heading. It consumes the state provided by DashboardView so
// toolbar selection changes reach the trend and ranking while the page coordinates full
// refreshes.

// refreshing reflects the page-wide refresh operation and disables the toolbar refresh
// control while its sections settle.
defineProps<{ refreshing?: boolean /* Page refresh in progress. */ }>()
// emit requests a dashboard-wide refresh; the parent coordinates account data and each
// independently loaded dashboard section.
const emit = defineEmits<{ (event: 'refresh'): void }>()

// Translations and injected chart controls keep the heading picker and filters attached to
// the page-owned usage selection.
const { t } = useI18n()
const { pickerStart, pickerEnd, filterState, applyFilters, resetFilters, selectCustomRange } =
  injectUsageChartState()
const {
  filters,
  activeCount,
  apiKeyOptions,
  modelOptions,
  groupOptions,
  requestTypeOptions,
  billingTypeOptions,
  billingModeOptions,
  compactionOptions,
} = filterState
</script>
