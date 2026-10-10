<template>
  <div class="flex min-h-0 flex-1 flex-col gap-4" data-testid="provider-batch-test">
    <ProviderTestResultView v-if="batch.detailRow" :run="batch.detailRow.run" :decision="decision">
      <template #leading>
        <button
          type="button"
          class="btn-icon-sm text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100"
          :title="t('admin.providers.testDialog.batch.back')"
          :aria-label="t('admin.providers.testDialog.batch.back')"
          @click="batch.showDetail(null)"
        >
          <Icon name="arrowLeft" size="sm" />
        </button>
        <ModelIcon :model="batch.detailRow.model" size="16px" />
        <span class="truncate text-sm font-medium text-gray-900 dark:text-dark-50" :title="batch.detailRow.model">
          {{ batch.detailRow.model }}
        </span>
      </template>
    </ProviderTestResultView>

    <template v-else>
      <div class="flex shrink-0 flex-wrap items-center gap-2">
        <div class="input-icon-wrap min-w-0 flex-1">
          <div class="input-icon">
            <Icon name="search" size="sm" class="text-gray-400" />
          </div>
          <input
            v-model="query"
            type="text"
            class="input input-has-icon"
            :placeholder="t('admin.providers.testDialog.batch.search')"
            :aria-label="t('admin.providers.testDialog.batch.search')"
            data-testid="provider-batch-search"
            @keydown.enter.prevent="addQueryModel"
          />
        </div>
        <button
          v-if="canAdd"
          type="button"
          class="btn btn-secondary"
          :disabled="batch.running || batch.selected.size >= MAX_BATCH_MODELS"
          @click="addQueryModel"
        >
          <Icon name="plus" size="sm" />
          {{ t('admin.providers.testDialog.batch.add') }}
        </button>
        <span
          class="text-xs text-gray-500 dark:text-dark-400"
          :title="t('admin.providers.testDialog.batch.limit', { limit: MAX_BATCH_MODELS })"
        >
          {{ t('admin.providers.testDialog.batch.selected', { count: batch.selected.size, total: batch.models.length }) }}
        </span>
        <button
          type="button"
          class="btn btn-ghost btn-sm"
          :disabled="batch.running || batch.selected.size === 0"
          @click="batch.clearSelection()"
        >
          {{ t('admin.providers.testDialog.batch.clear') }}
        </button>
      </div>

      <div v-if="batch.entries.length > 0" class="shrink-0 space-y-2" aria-live="polite">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <span class="text-gray-700 dark:text-dark-200">{{ progressLabel }}</span>
          <span class="text-green-600 dark:text-green-400">
            {{ t('admin.providers.testDialog.batch.successCount', { count: batch.successCount }) }}
          </span>
          <span class="text-red-600 dark:text-red-400">
            {{ t('admin.providers.testDialog.batch.failedCount', { count: batch.failedModels.length }) }}
          </span>
          <span v-if="batch.stoppedCount > 0" class="text-gray-500 dark:text-dark-400">
            {{ t('admin.providers.testDialog.batch.stoppedCount', { count: batch.stoppedCount }) }}
          </span>
        </div>
        <div
          class="h-1 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700"
          role="progressbar"
          :aria-valuenow="Math.round(batch.progress)"
          aria-valuemin="0"
          aria-valuemax="100"
          :aria-label="t('admin.providers.testDialog.batch.progressLabel')"
        >
          <div class="h-full rounded-full bg-primary-500 transition-all duration-normal" :style="{ width: `${batch.progress}%` }"></div>
        </div>
      </div>

      <div class="flex min-h-64 flex-1 flex-col overflow-hidden rounded-surface border border-gray-200 dark:border-dark-600">
        <div v-if="visibleModels.length > 0" class="min-h-0 flex-1 overflow-auto overscroll-contain">
          <table class="w-full text-left" :aria-label="t('admin.providers.testDialog.batch.models')">
            <thead class="sticky top-0 z-[1] bg-white dark:bg-dark-900"> <!-- check-ui-allow: 弹窗内 sticky 表头 -->
              <tr class="border-b border-gray-200 text-xs font-medium tracking-wider text-gray-500 dark:border-dark-600 dark:text-dark-400">
                <th class="w-11 min-w-11 px-3 py-2 text-center">
                  <input
                    type="checkbox"
                    class="h-4 w-4"
                    :checked="allVisibleSelected"
                    :indeterminate="!allVisibleSelected && anyVisibleSelected"
                    :disabled="batch.running"
                    :aria-label="t('admin.providers.testDialog.batch.selectAll')"
                    @change="batch.setMany(visibleModels, ($event.target as HTMLInputElement).checked)"
                  />
                </th>
                <th class="px-4 py-2">{{ t('admin.providers.testDialog.model') }}</th>
                <th class="px-4 py-2">{{ t('admin.providers.testDialog.batch.status') }}</th>
                <th v-if="!decision" class="px-4 py-2">{{ t('admin.providers.testDialog.metricFirstToken') }}</th>
                <th class="px-4 py-2">{{ t('admin.providers.testDialog.metricTotal') }}</th>
                <th class="w-20 px-4 py-2"></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr
                v-for="model in visibleModels"
                :key="model"
                class="hover:bg-gray-50 dark:hover:bg-dark-800"
                :data-testid="`provider-batch-row-${model}`"
              >
                <td class="w-11 min-w-11 px-3 py-3 text-center">
                  <input
                    type="checkbox"
                    class="h-4 w-4"
                    :checked="batch.selected.has(model)"
                    :disabled="batch.running || (!batch.selected.has(model) && batch.selected.size >= MAX_BATCH_MODELS)"
                    :aria-label="t('admin.providers.testDialog.batch.selectModel', { model })"
                    @change="batch.toggle(model, ($event.target as HTMLInputElement).checked)"
                  />
                </td>
                <td class="max-w-64 px-4 py-3">
                  <div class="flex min-w-0 items-center gap-2">
                    <ModelIcon :model="model" size="16px" />
                    <span class="truncate text-sm text-gray-900 dark:text-dark-50" :title="model">{{ model }}</span>
                  </div>
                </td>
                <td class="px-4 py-3">
                  <span
                    :class="['inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium', stateToneClass(rowOf(model)?.state)]"
                  >
                    <span :class="['h-1.5 w-1.5 rounded-full bg-current', { 'animate-pulse': rowOf(model)?.state === 'running' }]"></span>
                    {{ t(`admin.providers.testDialog.batch.state.${rowOf(model)?.state ?? 'ready'}`) }}
                  </span>
                </td>
                <td v-if="!decision" class="px-4 py-3 font-mono text-xs tabular-nums text-gray-700 dark:text-dark-200">
                  {{ formatSeconds(rowOf(model)?.run.firstTokenMs) }}
                </td>
                <td class="px-4 py-3 font-mono text-xs tabular-nums text-gray-700 dark:text-dark-200">
                  {{ formatSeconds(rowOf(model)?.run.totalMs) }}
                </td>
                <td class="px-4 py-3 text-right">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm"
                    :disabled="!hasDetail(model)"
                    :aria-label="t('admin.providers.testDialog.batch.detailsNamed', { model })"
                    @click="batch.showDetail(model)"
                  >
                    {{ t('admin.providers.testDialog.batch.details') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div v-else class="flex flex-1 flex-col items-center justify-center gap-2 px-4 py-10 text-center">
          <Icon name="search" size="xl" class="mb-2 text-gray-400 dark:text-dark-500" />
          <div class="text-sm font-medium text-gray-900 dark:text-dark-50">
            {{ t('admin.providers.testDialog.batch.emptyTitle') }}
          </div>
          <p class="max-w-sm text-xs leading-relaxed text-gray-500 dark:text-dark-400">
            {{ t('admin.providers.testDialog.batch.emptyDescription') }}
          </p>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { UnwrapNestedRefs } from 'vue'
import { useI18n } from 'vue-i18n'
import ModelIcon from '@/components/common/ModelIcon.vue'
import { Icon } from '@/components/icons'
import ProviderTestResultView from './ProviderTestResultView.vue'
import {
  MAX_BATCH_MODELS,
  type ProviderBatchRowState,
  type ProviderBatchTest
} from './useProviderBatchTest'

const props = defineProps<{
  batch: UnwrapNestedRefs<ProviderBatchTest>
  decision?: boolean
}>()

const { t } = useI18n()
const query = ref('')

const visibleModels = computed(() => {
  const keyword = query.value.trim().toLowerCase()
  return props.batch.models.filter((model) => model.toLowerCase().includes(keyword))
})
const allVisibleSelected = computed(
  () => visibleModels.value.length > 0 && visibleModels.value.every((model) => props.batch.selected.has(model))
)
const anyVisibleSelected = computed(() => visibleModels.value.some((model) => props.batch.selected.has(model)))

// 输入的模型 ID 不在列表里时允许直接添加。
const canAdd = computed(() => {
  const model = query.value.trim()
  return model !== '' && model.length <= 256 && !props.batch.models.includes(model)
})

const progressLabel = computed(() => {
  const params = { done: props.batch.doneCount, total: props.batch.entries.length }
  if (props.batch.stopping) return t('admin.providers.testDialog.batch.stopping')
  if (props.batch.running) return t('admin.providers.testDialog.batch.progress', params)
  return t('admin.providers.testDialog.batch.finished', params)
})

const rowOf = (model: string) => props.batch.rows[model]

const hasDetail = (model: string) => {
  const state = rowOf(model)?.state
  return state === 'running' || state === 'success' || state === 'failed'
}

const formatSeconds = (value: number | null | undefined) => (value == null ? '—' : `${(value / 1000).toFixed(2)} s`)

const stateToneClass = (state: ProviderBatchRowState | undefined) => {
  switch (state) {
    case 'running':
      return 'bg-primary-500/10 text-primary-600 dark:text-primary-400'
    case 'success':
      return 'bg-green-500/10 text-green-600 dark:text-green-400'
    case 'failed':
      return 'bg-red-500/10 text-red-600 dark:text-red-400'
    case 'queued':
      return 'bg-amber-500/10 text-amber-600 dark:text-amber-400'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-dark-300'
  }
}

function addQueryModel() {
  if (!canAdd.value || props.batch.running) return
  props.batch.addModel(query.value.trim())
}
</script>
