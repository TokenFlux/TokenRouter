<template>
  <BaseDialog
    :show="show"
    :title="t('usage.exportPanel.title')"
    :subtitle="t('usage.exportPanel.subtitle')"
    width="wide"
    @close="handleClose"
  >
    <template #header-icon>
      <div
        class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-primary-500/10 text-primary-600 dark:bg-primary-500/15 dark:text-primary-500"
      >
        <Icon name="download" size="sm" :animate-on-hover="false" />
      </div>
    </template>

    <div class="space-y-4">
      <!-- Top section: configure second-precision bounds, IANA timezone, and filter inheritance. -->
      <div
        class="space-y-4 rounded-surface border border-gray-200 bg-gray-50/60 p-4 dark:border-dark-600 dark:bg-dark-950/60"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-dark-50">
            {{ t('usage.exportPanel.createSectionTitle') }}
          </h4>
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              data-testid="export-preset-page"
              @click="applyPageDateRange"
            >
              {{ t('usage.exportPanel.syncPageRange') }}
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              data-testid="export-preset-1h"
              @click="applyRelativeHoursPreset(1)"
            >
              {{ t('usage.exportPanel.presetLast1h') }}
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              data-testid="export-preset-6h"
              @click="applyRelativeHoursPreset(6)"
            >
              {{ t('usage.exportPanel.presetLast6h') }}
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              data-testid="export-preset-24h"
              @click="applyRelativeHoursPreset(24)"
            >
              {{ t('usage.exportPanel.presetLast24h') }}
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              data-testid="export-preset-today"
              @click="applyCalendarDayPreset(0)"
            >
              {{ t('usage.exportPanel.presetToday') }}
            </button>
            <button
              type="button"
              class="btn btn-ghost btn-sm"
              data-testid="export-preset-yesterday"
              @click="applyCalendarDayPreset(-1)"
            >
              {{ t('usage.exportPanel.presetYesterday') }}
            </button>
          </div>
        </div>

        <!-- Explicit IANA timezone selector paired with a one-click UTC switch. -->
        <div>
          <label class="input-label">{{ t('usage.exportPanel.timezoneLabel') }}</label>
          <div class="flex items-center gap-2">
            <div class="min-w-0 flex-1">
              <Select
                :model-value="selectedTimezone"
                :options="timezoneOptions"
                searchable
                :search-placeholder="t('usage.exportPanel.timezoneSearchPlaceholder')"
                :aria-label="t('usage.exportPanel.timezoneLabel')"
                @update:model-value="handleTimezoneSelect"
              />
            </div>
            <button
              type="button"
              class="btn btn-secondary shrink-0"
              data-testid="export-switch-utc"
              :disabled="selectedTimezone === 'UTC'"
              @click="handleTimezoneSelect('UTC')"
            >
              <Icon name="globe" size="sm" />
              <span>{{ t('usage.exportPanel.switchUtc') }}</span>
            </button>
          </div>
        </div>

        <!-- Inclusive start and exclusive end pickers at second resolution. -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('usage.exportPanel.startTimeLabel') }}</label>
            <DateTimePicker
              :model-value="startLocal"
              precision="second"
              :clearable="false"
              :now-resolver="resolveNowForStart"
              @update:model-value="handleStartPickerUpdate"
            />
          </div>
          <div>
            <label class="input-label">{{ t('usage.exportPanel.endTimeLabel') }}</label>
            <DateTimePicker
              :model-value="endLocal"
              precision="second"
              :clearable="false"
              :min="startLocal"
              :now-resolver="resolveNowForEnd"
              @update:model-value="handleEndPickerUpdate"
            />
          </div>
        </div>

        <!-- Optional timestamp / Unix epoch quick-paste helper for developer and billing workflows. -->
        <div>
          <button
            type="button"
            class="inline-flex items-center gap-1.5 text-xs font-medium text-primary-600 transition-colors duration-fast hover:text-primary-700 dark:text-primary-500 dark:hover:text-primary-400"
            data-testid="export-toggle-paste"
            @click="toggleQuickPaste"
          >
            <Icon name="clock" size="xs" />
            <span>
              {{
                showQuickPaste
                  ? t('usage.exportPanel.quickPasteHide')
                  : t('usage.exportPanel.quickPasteToggle')
              }}
            </span>
          </button>

          <Collapse :open="showQuickPaste">
            <div
              class="mt-2 space-y-2 rounded-control border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-900"
            >
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <div class="flex items-center gap-2">
                  <input
                    v-model="pasteStartInput"
                    type="text"
                    class="input min-w-0 flex-1 font-mono text-xs"
                    data-testid="export-paste-start-input"
                    :placeholder="t('usage.exportPanel.quickPasteStartPlaceholder')"
                    @keyup.enter="applyPasteBound('start')"
                  />
                  <button
                    type="button"
                    class="btn btn-secondary shrink-0"
                    data-testid="export-paste-start-apply"
                    @click="applyPasteBound('start')"
                  >
                    {{ t('usage.exportPanel.quickPasteApply') }}
                  </button>
                </div>
                <div class="flex items-center gap-2">
                  <input
                    v-model="pasteEndInput"
                    type="text"
                    class="input min-w-0 flex-1 font-mono text-xs"
                    data-testid="export-paste-end-input"
                    :placeholder="t('usage.exportPanel.quickPasteEndPlaceholder')"
                    @keyup.enter="applyPasteBound('end')"
                  />
                  <button
                    type="button"
                    class="btn btn-secondary shrink-0"
                    data-testid="export-paste-end-apply"
                    @click="applyPasteBound('end')"
                  >
                    {{ t('usage.exportPanel.quickPasteApply') }}
                  </button>
                </div>
              </div>
              <p class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('usage.exportPanel.quickPasteHint') }}
              </p>
              <p
                v-if="pasteError"
                role="alert"
                class="text-xs font-medium text-rose-600 dark:text-rose-400"
              >
                {{ pasteError }}
              </p>
            </div>
          </Collapse>
        </div>

        <!-- Live half-open interval and UTC equivalent preview bar. -->
        <div
          :class="[
            'rounded-control border px-3 py-2 text-xs',
            isRangeValid
              ? 'border-gray-200 bg-white text-gray-600 dark:border-dark-600 dark:bg-dark-900 dark:text-dark-200'
              : 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-300',
          ]"
          data-testid="export-boundary-preview"
        >
          <div v-if="isRangeValid" class="space-y-1">
            <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
              <span class="font-medium text-gray-700 dark:text-dark-100">
                {{ t('usage.exportPanel.boundaryLabel') }}:
              </span>
              <span class="font-mono tabular-nums">
                [{{ formattedLocalStart }}, {{ formattedLocalEnd }}) ({{ selectedTimezone }})
              </span>
            </div>
            <div
              class="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-gray-500 dark:text-dark-300"
            >
              <span class="font-medium">{{ t('usage.exportPanel.utcPreviewLabel') }}:</span>
              <span class="font-mono tabular-nums">
                [{{ utcPreviewStart }}, {{ utcPreviewEnd }})
              </span>
            </div>
          </div>
          <div v-else role="alert" class="flex items-center gap-1.5 font-medium">
            <Icon name="exclamationCircle" size="sm" :animate-on-hover="false" />
            <span>{{ t('usage.exportPanel.rangeInvalidError') }}</span>
          </div>
        </div>

        <!-- Active page filter inheritance summary and toggle. -->
        <div class="space-y-2">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-200">
              {{ t('usage.exportPanel.filtersLabel') }}
            </span>
            <div v-if="hasActivePageFilters" class="inline-flex items-center gap-2">
              <span class="text-xs text-gray-600 dark:text-dark-300">
                {{ t('usage.exportPanel.includePageFilters') }}
              </span>
              <Toggle v-model="includePageFilters" size="sm" />
            </div>
          </div>

          <p v-if="!hasActivePageFilters" class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('usage.exportPanel.noActiveFiltersHint') }}
          </p>
          <p v-else-if="!includePageFilters" class="text-xs text-amber-600 dark:text-amber-400">
            {{ t('usage.exportPanel.excludedFiltersHint') }}
          </p>
          <div v-else class="flex flex-wrap gap-1.5" data-testid="export-active-filter-chips">
            <span
              v-for="chip in effectiveFilterChips"
              :key="chip.key"
              class="inline-flex items-center gap-1 rounded-compact border border-primary-500/20 bg-primary-500/8 px-2 py-0.5 text-xs text-primary-700 dark:border-primary-500/25 dark:bg-primary-500/10 dark:text-primary-400"
            >
              <span class="font-medium">{{ chip.label }}:</span>
              <span>{{ chip.value }}</span>
            </span>
          </div>
        </div>

        <!-- Submission row with CSV retention note and create CTA. -->
        <div
          class="flex flex-col gap-3 border-t border-gray-200 pt-3 dark:border-dark-600 sm:flex-row sm:items-center sm:justify-between"
        >
          <p class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('usage.exportPanel.formatRetentionHint') }}
          </p>
          <button
            type="button"
            class="btn btn-primary shrink-0"
            data-testid="create-export-task-btn"
            :disabled="!isRangeValid || creatingTask"
            @click="submitCreateTask"
          >
            <Icon
              :name="creatingTask ? 'loader' : 'download'"
              size="sm"
              :class="creatingTask && 'animate-spin'"
              :animate-on-hover="!creatingTask"
            />
            <span>
              {{
                creatingTask
                  ? t('usage.exportPanel.creatingButton')
                  : t('usage.exportPanel.createButton')
              }}
            </span>
          </button>
        </div>
      </div>

      <!-- Bottom section: paginated export tasks with live progress polling and signed downloads. -->
      <div
        class="overflow-hidden rounded-surface border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-900"
      >
        <div
          class="flex items-center justify-between border-b border-gray-100 px-4 py-3 dark:border-dark-700"
        >
          <div class="flex items-center gap-2">
            <h4 class="text-sm font-semibold text-gray-900 dark:text-dark-50">
              {{ t('usage.exportPanel.taskListTitle') }}
            </h4>
            <span
              v-if="tasksTotal > 0"
              class="rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium tabular-nums text-gray-600 dark:bg-dark-700 dark:text-dark-200"
            >
              {{ tasksTotal }}
            </span>
          </div>
          <button
            type="button"
            class="btn btn-ghost btn-sm"
            data-testid="refresh-export-tasks-btn"
            :disabled="tasksLoading"
            @click="loadTasks(false)"
          >
            <Icon
              name="refresh"
              size="sm"
              :class="tasksLoading && 'animate-spin'"
              :animate-on-hover="!tasksLoading"
            />
            <span>{{ t('usage.exportPanel.refreshTasks') }}</span>
          </button>
        </div>

        <div class="p-4">
          <div
            v-if="tasksInitialLoading && tasks.length === 0"
            class="space-y-2"
            aria-busy="true"
            data-testid="export-tasks-skeleton"
          >
            <Skeleton v-for="idx in 3" :key="idx" :height="68" />
          </div>

          <div
            v-else-if="tasks.length === 0"
            class="py-6 text-center text-sm text-gray-500 dark:text-dark-400"
            data-testid="export-tasks-empty"
          >
            {{ t('usage.exportPanel.emptyTasks') }}
          </div>

          <div v-else class="space-y-2.5" data-testid="export-tasks-list">
            <div
              v-for="task in tasks"
              :key="task.id"
              class="space-y-2 rounded-control border border-gray-200 p-3 dark:border-dark-600"
              :data-testid="`export-task-card-${task.id}`"
            >
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div class="flex flex-wrap items-center gap-2">
                  <span
                    :class="[
                      'inline-flex items-center gap-1 rounded-compact px-2 py-0.5 text-xs font-medium',
                      statusBadgeClass(task.status),
                    ]"
                  >
                    <Icon
                      v-if="task.status === 'running'"
                      name="loader"
                      size="xs"
                      class="animate-spin"
                      :animate-on-hover="false"
                    />
                    <span>{{ statusLabel(task.status) }}</span>
                  </span>
                  <span class="font-mono text-xs text-gray-400 dark:text-dark-400">
                    #{{ task.id }}
                  </span>
                  <span
                    class="rounded-compact bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-200"
                  >
                    {{ task.filters.timezone }}
                  </span>
                  <span
                    class="rounded-compact bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-300"
                  >
                    {{ taskFilterSummaryLabel(task.filters) }}
                  </span>
                </div>

                <div class="flex flex-wrap items-center gap-2">
                  <span class="text-xs tabular-nums text-gray-400 dark:text-dark-400">
                    {{
                      t('usage.exportPanel.createdAtLabel', {
                        time: formatTaskBound(task.created_at, task.filters.timezone),
                      })
                    }}
                  </span>
                  <button
                    v-if="isTaskActive(task.status)"
                    type="button"
                    class="btn btn-secondary btn-sm text-rose-600 hover:text-rose-700 dark:text-rose-400"
                    :data-testid="`cancel-export-task-${task.id}`"
                    :disabled="cancelingTaskIds.has(task.id)"
                    @click="handleCancelTask(task)"
                  >
                    {{
                      cancelingTaskIds.has(task.id)
                        ? t('usage.exportPanel.cancelingTask')
                        : t('usage.exportPanel.cancelTask')
                    }}
                  </button>
                  <button
                    v-if="task.status === 'succeeded'"
                    type="button"
                    class="btn btn-primary btn-sm"
                    :data-testid="`download-export-task-${task.id}`"
                    :disabled="downloadingTaskIds.has(task.id)"
                    @click="handleDownloadTask(task)"
                  >
                    <Icon
                      :name="downloadingTaskIds.has(task.id) ? 'loader' : 'download'"
                      size="sm"
                      :class="downloadingTaskIds.has(task.id) && 'animate-spin'"
                      :animate-on-hover="!downloadingTaskIds.has(task.id)"
                    />
                    <span>
                      {{
                        downloadingTaskIds.has(task.id)
                          ? t('usage.exportPanel.downloadingCsv')
                          : t('usage.exportPanel.downloadCsv')
                      }}
                    </span>
                  </button>
                </div>
              </div>

              <div class="font-mono text-xs tabular-nums text-gray-700 dark:text-dark-200">
                [{{ formatTaskBound(task.filters.start_time, task.filters.timezone) }},
                {{ formatTaskBound(task.filters.end_time, task.filters.timezone) }})
              </div>

              <!-- Active progress bar and row counter while pending or running. -->
              <div v-if="isTaskActive(task.status)" class="space-y-1">
                <div class="h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                  <div
                    class="h-full rounded-full bg-primary-600 transition-all duration-normal"
                    :style="{ width: `${taskProgressPercent(task)}%` }"
                  />
                </div>
                <div
                  class="flex items-center justify-between text-xs tabular-nums text-gray-500 dark:text-dark-300"
                >
                  <span>
                    {{
                      t('usage.exportPanel.progressRows', {
                        exported: task.exported_rows.toLocaleString(),
                        total: task.total_rows.toLocaleString(),
                      })
                    }}
                  </span>
                  <span>{{ taskProgressPercent(task) }}%</span>
                </div>
              </div>

              <!-- Completed file size, row total, and retention expiry metadata. -->
              <div
                v-else-if="task.status === 'succeeded'"
                class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs tabular-nums text-gray-500 dark:text-dark-300"
              >
                <span>
                  {{
                    task.total_rows === 0 && task.exported_rows === 0
                      ? t('usage.exportPanel.zeroRowsCompleted')
                      : t('usage.exportPanel.progressRows', {
                          exported: task.exported_rows.toLocaleString(),
                          total: Math.max(task.total_rows, task.exported_rows).toLocaleString(),
                        })
                  }}
                </span>
                <span>
                  {{
                    t('usage.exportPanel.fileSizeLabel', {
                      size: formatBytes(task.file_size_bytes),
                    })
                  }}
                </span>
                <span v-if="task.expires_at">
                  {{
                    t('usage.exportPanel.expiresAtLabel', {
                      time: formatTaskBound(task.expires_at, task.filters.timezone),
                    })
                  }}
                </span>
              </div>

              <p
                v-else-if="task.status === 'expired'"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{ t('usage.exportPanel.expiredNotice') }}
              </p>

              <p
                v-else-if="task.status === 'failed' && task.error_message"
                class="break-words text-xs text-rose-600 dark:text-rose-400"
              >
                {{ task.error_message }}
              </p>
            </div>
          </div>
        </div>

        <Pagination
          v-if="tasksTotal > tasksPageSize"
          :total="tasksTotal"
          :page="tasksPage"
          :page-size="tasksPageSize"
          :page-size-options="[10]"
          :show-page-size-selector="false"
          :show-jump="false"
          @update:page="handlePageChange"
        />
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Collapse from '@/components/common/Collapse.vue'
import DateTimePicker from '@/components/common/DateTimePicker.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import { usageAPI } from '@/api/usage'
import { adminUsageAPI } from '@/api/admin/usage'
import type {
  CreateAdminUsageExportTaskRequest,
  CreateUserUsageExportTaskRequest,
  UsageExportFilters,
  UsageExportTask,
  UsageExportTaskListResponse,
  UsageExportTaskStatus,
  UsageRequestType,
} from '@/api/usage.types'
import { useAppStore } from '@/stores/app'
import type { UsageExportFilterLabel } from '@/types/ui'
import { formatBytes } from '@/utils/format'
import {
  dateRangeToHalfOpenLocalRange,
  formatInstantInTimezoneDisplay,
  formatInstantRFC3339InTimezone,
  formatInstantUTCPreview,
  getBrowserTimezone,
  getTimezoneSelectOptions,
  instantToZonedLocalDateTime,
  isValidIanaTimezone,
  parseFlexibleTimestamp,
  shiftCalendarDate,
  zonedDateTimeToInstant,
} from '@/utils/timezone'

// UsageExportDialog.vue manages second-precision background CSV exports for both regular
// users and administrators inside a shared modal panel. Opening inherits the host usage
// page's inclusive date filter as a half-open [startDate 00:00:00, endDate+1d 00:00:00)
// interval in the selected IANA timezone along with active non-time query filters.
//
// Task polling runs only while the dialog is visible and at least one task remains in
// pending or running state, aborting in-flight list requests when closed or unmounted.
// Downloads exchange a short-lived signed ticket and trigger native browser streaming
// without buffering CSV bytes in JavaScript memory.

export type { UsageExportFilterLabel }

// Props defines the visibility, authorization mode, and inherited filter context passed
// from the user or administrator UsageView host page.
interface Props {
  show: boolean // Controls modal visibility.
  mode: 'user' | 'admin' // Selects user or administrator API routes and filter keys.
  startDate: string // Active page inclusive start date (YYYY-MM-DD).
  endDate: string // Active page inclusive end date (YYYY-MM-DD).
  filters: Record<string, unknown> // Active page filter state object.
  filterLabels?: UsageExportFilterLabel[] // Optional preformatted filter chip labels.
}

// props and emit wire parent visibility, filter inheritance, and active-task badge updates.
const props = defineProps<Props>()
const emit = defineEmits<{
  close: [] // Requests closing the export dialog.
  tasksChanged: [activeCount: number] // Reports active pending/running export task count.
  activeIdsChanged: [activeIds: number[]] // Reports active task identifiers on page one.
}>()

// t, locale, and appStore provide localized copy, CSV header language selection, and toasts.
const { t, locale } = useI18n()
const appStore = useAppStore()

// POLL_INTERVAL_MS and TASKS_PAGE_SIZE govern open-dialog progress polling cadence and
// task list page density.
const POLL_INTERVAL_MS = 3000
const TASKS_PAGE_SIZE = 10

// Form draft state holds the chosen IANA timezone, second-precision local bounds,
// filter-inheritance switch, and optional timestamp quick-paste inputs.
// startExactInstant and endExactInstant retain unambiguous UTC instants from pasted
// offset/epoch timestamps, relative presets, or timezone switches so repeated DST
// fall-back hours never shift onto the earlier local occurrence on submission.
const selectedTimezone = ref<string>(getBrowserTimezone())
const startLocal = ref<string>('')
const endLocal = ref<string>('')
const startExactInstant = ref<Date | null>(null)
const endExactInstant = ref<Date | null>(null)
const includePageFilters = ref<boolean>(true)
const showQuickPaste = ref<boolean>(false)
const pasteStartInput = ref<string>('')
const pasteEndInput = ref<string>('')
const pasteError = ref<string>('')
const creatingTask = ref<boolean>(false)

// Task list state tracks paginated server tasks, initial skeleton state, per-row cancel or
// download spinners, and previously active task IDs for completion notifications.
const tasks = ref<UsageExportTask[]>([])
const tasksTotal = ref<number>(0)
const tasksPage = ref<number>(1)
const tasksPageSize = TASKS_PAGE_SIZE
const tasksInitialLoading = ref<boolean>(false)
const tasksLoading = ref<boolean>(false)
const cancelingTaskIds = ref<Set<number>>(new Set())
const downloadingTaskIds = ref<Set<number>>(new Set())
const trackedActiveTaskIds = ref<Set<number>>(new Set())
const globalActiveTaskCount = ref<number>(0)

// pollTimer, listAbortController, listReqSeq, and isUnmounted coordinate recurring
// refreshes and ensure late create/cancel/download responses never restart polling
// after the dialog closes or unmounts.
let pollTimer: number | null = null
let listAbortController: AbortController | null = null
let listReqSeq = 0
let isUnmounted = false

// resolveBoundInstant prefers exactInstant while its wall-clock projection in timeZone
// still matches localValue, falling back to wall-clock resolution after manual edits.
function resolveBoundInstant(
  localValue: string,
  exactInstant: Date | null,
  timeZone: string,
): Date | null {
  if (
    exactInstant &&
    !Number.isNaN(exactInstant.getTime()) &&
    instantToZonedLocalDateTime(exactInstant, timeZone) === localValue
  ) {
    return exactInstant
  }
  return zonedDateTimeToInstant(localValue, timeZone)
}

// startInstant and endInstant resolve each bound against selectedTimezone while
// preserving unambiguous instants across DST fall-back folds.
const startInstant = computed(() =>
  resolveBoundInstant(startLocal.value, startExactInstant.value, selectedTimezone.value),
)
const endInstant = computed(() =>
  resolveBoundInstant(endLocal.value, endExactInstant.value, selectedTimezone.value),
)

// isRangeValid requires both timestamps to parse cleanly and endInstant to strictly follow startInstant.
const isRangeValid = computed(() => {
  if (!startInstant.value || !endInstant.value) {
    return false
  }
  return endInstant.value.getTime() > startInstant.value.getTime()
})

// timezoneOptions builds searchable IANA zone options anchored at startInstant when valid
// so daylight-saving offset labels match the selected export window.
const timezoneOptions = computed(() =>
  getTimezoneSelectOptions(startInstant.value ?? new Date(), selectedTimezone.value),
)

// formattedLocalStart, formattedLocalEnd, utcPreviewStart, and utcPreviewEnd render the
// exact half-open [start, end) boundaries in both selectedTimezone and UTC.
const formattedLocalStart = computed(() =>
  startInstant.value
    ? formatInstantInTimezoneDisplay(startInstant.value, selectedTimezone.value)
    : '',
)
const formattedLocalEnd = computed(() =>
  endInstant.value ? formatInstantInTimezoneDisplay(endInstant.value, selectedTimezone.value) : '',
)
const utcPreviewStart = computed(() =>
  startInstant.value ? formatInstantUTCPreview(startInstant.value) : '',
)
const utcPreviewEnd = computed(() =>
  endInstant.value ? formatInstantUTCPreview(endInstant.value) : '',
)

// extractActiveFilterEntries inspects props.filters for non-empty user or administrator
// usage predicates, preserving numeric zero (Balance billing_type) and boolean false.
const activeFilterPayload = computed<Partial<CreateAdminUsageExportTaskRequest>>(() => {
  const raw = props.filters || {}
  const out: Partial<CreateAdminUsageExportTaskRequest> = {}

  if (typeof raw.api_key_id === 'number' && raw.api_key_id > 0) {
    out.api_key_id = raw.api_key_id
  }
  if (typeof raw.group_id === 'number' && raw.group_id > 0) {
    out.group_id = raw.group_id
  }
  if (typeof raw.team_id === 'number' && raw.team_id > 0) {
    out.team_id = raw.team_id
  }
  if (raw.personal_only === true) {
    out.personal_only = true
  }
  if (typeof raw.request_id === 'string' && raw.request_id.trim() !== '') {
    out.request_id = raw.request_id.trim()
  }
  if (typeof raw.model === 'string' && raw.model.trim() !== '') {
    out.model = raw.model.trim()
    if (
      props.mode === 'admin' &&
      (raw.model_filter_source === 'requested' ||
        raw.model_filter_source === 'upstream' ||
        raw.model_filter_source === 'mapping')
    ) {
      out.model_filter_source = raw.model_filter_source
    }
  }
  if (typeof raw.request_type === 'string' && raw.request_type.trim() !== '') {
    out.request_type = raw.request_type.trim() as UsageRequestType
  } else if (typeof raw.stream === 'boolean') {
    out.stream = raw.stream
  }
  if (typeof raw.native_compaction_v2 === 'boolean') {
    out.native_compaction_v2 = raw.native_compaction_v2
  }
  if (typeof raw.billing_type === 'number' && !Number.isNaN(raw.billing_type)) {
    out.billing_type = raw.billing_type
  }
  if (typeof raw.billing_mode === 'string' && raw.billing_mode.trim() !== '') {
    out.billing_mode = raw.billing_mode.trim()
  }

  if (props.mode === 'admin') {
    if (
      typeof raw.request_source === 'string' &&
      raw.request_source.trim() !== '' &&
      raw.request_source.trim() !== 'all'
    ) {
      out.request_source = raw.request_source.trim()
    }
    if (typeof raw.user_id === 'number' && raw.user_id > 0) {
      out.user_id = raw.user_id
    }
    if (typeof raw.provider_id === 'number' && raw.provider_id > 0) {
      out.provider_id = raw.provider_id
    }
  }

  return out
})

// effectiveFilterChips prefers caller-supplied human-readable chips and falls back to
// key/value representations derived from activeFilterPayload when labels are omitted.
const effectiveFilterChips = computed<UsageExportFilterLabel[]>(() => {
  if (props.filterLabels && props.filterLabels.length > 0) {
    return props.filterLabels
  }
  return Object.entries(activeFilterPayload.value)
    .filter(([key]) => key !== 'model_filter_source')
    .map(([key, value]) => ({
      key,
      label: key,
      value: String(value),
    }))
})

// hasActivePageFilters reports whether the host page currently applies any non-date filter.
const hasActivePageFilters = computed(() => Object.keys(activeFilterPayload.value).length > 0)

// isTaskActive reports whether a persisted task is still queued or running on the server.
function isTaskActive(status: UsageExportTaskStatus): boolean {
  return status === 'pending' || status === 'running'
}

// resolveExportErrorMessage maps structured backend error reasons to localized export copy
// before falling back to server message text or the supplied fallback key.
function resolveExportErrorMessage(err: unknown, fallbackKey: string): string {
  const record = (typeof err === 'object' && err !== null ? err : {}) as Record<string, unknown>
  const reason = typeof record.reason === 'string' ? record.reason.toUpperCase() : ''
  switch (reason) {
    case 'USAGE_EXPORT_ACTIVE_LIMIT_EXCEEDED':
      return t('usage.exportPanel.errors.activeLimitExceeded')
    case 'USAGE_EXPORT_EXPIRED':
      return t('usage.exportPanel.errors.expired')
    case 'USAGE_EXPORT_NOT_READY':
      return t('usage.exportPanel.errors.notReady')
    case 'USAGE_EXPORT_NOT_FOUND':
      return t('usage.exportPanel.errors.notFound')
    case 'USAGE_EXPORT_NOT_CANCELABLE':
      return t('usage.exportPanel.errors.notCancelable')
    case 'USAGE_EXPORT_DISABLED':
      return t('usage.exportPanel.errors.disabled')
    case 'INVALID_TIMEZONE':
      return t('usage.exportPanel.errors.invalidTimezone')
    case 'INVALID_TIME_RANGE':
      return t('usage.exportPanel.errors.invalidTimeRange')
    default:
      if (typeof record.message === 'string' && record.message.trim() !== '') {
        return record.message
      }
      return t(fallbackKey)
  }
}

// handleStartPickerUpdate updates startLocal from DateTimePicker and clears any
// pinned exact instant whenever the wall-clock string changes manually.
function handleStartPickerUpdate(nextValue: string): void {
  if (nextValue !== startLocal.value) {
    startExactInstant.value = null
  }
  startLocal.value = nextValue
}

// handleEndPickerUpdate updates endLocal from DateTimePicker and clears any
// pinned exact instant whenever the wall-clock string changes manually.
function handleEndPickerUpdate(nextValue: string): void {
  if (nextValue !== endLocal.value) {
    endExactInstant.value = null
  }
  endLocal.value = nextValue
}

// resolveNowForStart pins startExactInstant to the current whole-second UTC instant
// and returns its wall-clock string in selectedTimezone for DateTimePicker's Now action.
function resolveNowForStart(): string {
  const truncatedNow = new Date(Math.floor(Date.now() / 1000) * 1000)
  const localNow = instantToZonedLocalDateTime(truncatedNow, selectedTimezone.value)
  startExactInstant.value = truncatedNow
  startLocal.value = localNow
  return localNow
}

// resolveNowForEnd pins endExactInstant to the current whole-second UTC instant
// and returns its wall-clock string in selectedTimezone for DateTimePicker's Now action.
function resolveNowForEnd(): string {
  const truncatedNow = new Date(Math.floor(Date.now() / 1000) * 1000)
  const localNow = instantToZonedLocalDateTime(truncatedNow, selectedTimezone.value)
  endExactInstant.value = truncatedNow
  endLocal.value = localNow
  return localNow
}

// applyPageDateRange aligns startLocal and endLocal to [props.startDate 00:00:00, props.endDate+1d 00:00:00)
// in selectedTimezone, falling back to today's full calendar day when page dates are absent.
function applyPageDateRange(): void {
  pasteError.value = ''
  const inherited = dateRangeToHalfOpenLocalRange(props.startDate, props.endDate)
  if (inherited) {
    startExactInstant.value = null
    endExactInstant.value = null
    startLocal.value = inherited.startLocal
    endLocal.value = inherited.endLocal
    return
  }
  applyCalendarDayPreset(0)
}

// applyRelativeHoursPreset sets [now - hours, now) at whole-second resolution in selectedTimezone
// while pinning both exact instants across daylight-saving transitions.
function applyRelativeHoursPreset(hours: number): void {
  pasteError.value = ''
  const endMs = Math.floor(Date.now() / 1000) * 1000
  const startMs = endMs - hours * 3600 * 1000
  const startDate = new Date(startMs)
  const endDate = new Date(endMs)
  startExactInstant.value = startDate
  endExactInstant.value = endDate
  startLocal.value = instantToZonedLocalDateTime(startDate, selectedTimezone.value)
  endLocal.value = instantToZonedLocalDateTime(endDate, selectedTimezone.value)
}

// applyCalendarDayPreset sets [day 00:00:00, nextDay 00:00:00) in selectedTimezone for
// today (dayOffset = 0) or yesterday (dayOffset = -1).
function applyCalendarDayPreset(dayOffset: number): void {
  pasteError.value = ''
  const todayInZone = instantToZonedLocalDateTime(new Date(), selectedTimezone.value).slice(0, 10)
  const targetDay = dayOffset === 0 ? todayInZone : shiftCalendarDate(todayInZone, dayOffset)
  const nextDay = shiftCalendarDate(targetDay, 1)
  if (targetDay && nextDay) {
    startExactInstant.value = null
    endExactInstant.value = null
    startLocal.value = `${targetDay}T00:00:00`
    endLocal.value = `${nextDay}T00:00:00`
  }
}

// handleTimezoneSelect switches selectedTimezone while preserving the physical start and
// end instants already chosen in the previous timezone whenever both bounds are valid.
function handleTimezoneSelect(nextValue: string | number | boolean | null): void {
  if (typeof nextValue !== 'string' || !isValidIanaTimezone(nextValue)) {
    return
  }
  const cleanZone = nextValue.trim()
  if (cleanZone === selectedTimezone.value) {
    return
  }
  const previousStart = startInstant.value
  const previousEnd = endInstant.value
  selectedTimezone.value = cleanZone
  pasteError.value = ''
  if (previousStart) {
    startExactInstant.value = previousStart
    startLocal.value = instantToZonedLocalDateTime(previousStart, cleanZone)
  }
  if (previousEnd) {
    endExactInstant.value = previousEnd
    endLocal.value = instantToZonedLocalDateTime(previousEnd, cleanZone)
  }
}

// toggleQuickPaste expands or collapses the timestamp paste helper and clears stale errors.
function toggleQuickPaste(): void {
  showQuickPaste.value = !showQuickPaste.value
  pasteError.value = ''
}

// applyPasteBound parses pasted Unix seconds, milliseconds, explicit-offset ISO text, or
// wall-clock text into startLocal/endLocal while preserving the exact parsed instant.
function applyPasteBound(bound: 'start' | 'end'): void {
  const raw = bound === 'start' ? pasteStartInput.value : pasteEndInput.value
  const parsed = parseFlexibleTimestamp(raw, selectedTimezone.value)
  if (!parsed) {
    pasteError.value = t('usage.exportPanel.quickPasteInvalid')
    return
  }
  pasteError.value = ''
  if (bound === 'start') {
    startExactInstant.value = parsed.instant
    startLocal.value = parsed.localValue
  } else {
    endExactInstant.value = parsed.instant
    endLocal.value = parsed.localValue
  }
}

// statusLabel maps persisted export task statuses to localized badge captions.
function statusLabel(status: UsageExportTaskStatus): string {
  return t(`usage.exportPanel.status.${status}`)
}

// statusBadgeClass selects semantic badge colors matching light and dark surface conventions.
function statusBadgeClass(status: UsageExportTaskStatus): string {
  switch (status) {
    case 'pending':
      return 'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300'
    case 'running':
      return 'bg-primary-500/10 text-primary-700 dark:bg-primary-500/15 dark:text-primary-400'
    case 'succeeded':
      return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
    case 'failed':
      return 'bg-rose-50 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
  }
}

// formatTaskBound formats a persisted RFC3339 timestamp in the task's snapshotted timezone.
function formatTaskBound(isoTimestamp: string, taskTimezone: string): string {
  const safeZone = isValidIanaTimezone(taskTimezone) ? taskTimezone : selectedTimezone.value
  return formatInstantInTimezoneDisplay(isoTimestamp, safeZone) || isoTimestamp
}

// taskProgressPercent computes integer completion percentage bounded to [0, 100].
function taskProgressPercent(task: UsageExportTask): number {
  if (task.status === 'succeeded') {
    return 100
  }
  if (task.total_rows <= 0) {
    return 0
  }
  const ratio = Math.round((task.exported_rows / task.total_rows) * 100)
  return Math.max(0, Math.min(100, ratio))
}

// countPersistedFilters counts non-time selectors persisted inside a task's filter snapshot.
function countPersistedFilters(taskFilters: UsageExportFilters): number {
  let count = 0
  if (taskFilters.api_key_id) count++
  if (taskFilters.group_id) count++
  if (taskFilters.team_id) count++
  if (taskFilters.personal_only) count++
  if (taskFilters.request_id) count++
  if (taskFilters.model) count++
  if (taskFilters.request_type || typeof taskFilters.stream === 'boolean') count++
  if (typeof taskFilters.native_compaction_v2 === 'boolean') count++
  if (typeof taskFilters.billing_type === 'number') count++
  if (taskFilters.billing_mode) count++
  if (taskFilters.request_source && taskFilters.request_source !== 'all') count++
  if (taskFilters.user_id) count++
  if (taskFilters.provider_id) count++
  return count
}

// taskFilterSummaryLabel summarizes whether a task exported all records or a filtered subset.
function taskFilterSummaryLabel(taskFilters: UsageExportFilters): string {
  const count = countPersistedFilters(taskFilters)
  if (count === 0) {
    return t('usage.exportPanel.scopeAllRecords')
  }
  return t('usage.exportPanel.filterCountTag', { count })
}

// isDialogActive reports whether the component remains mounted and currently visible.
function isDialogActive(): boolean {
  return !isUnmounted && props.show
}

// stopPolling clears any active progress interval and aborts in-flight list refreshes.
function stopPolling(): void {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

// abortInFlightList cancels any pending listExportTasks request so superseded responses
// never overwrite newer page state.
function abortInFlightList(): void {
  if (listAbortController) {
    listAbortController.abort()
    listAbortController = null
  }
}

// syncPollingInterval starts or stops the 3-second silent refresh timer depending on
// mounted visibility and whether any scoped export task remains pending or running.
function syncPollingInterval(): void {
  const shouldPoll = isDialogActive() && globalActiveTaskCount.value > 0
  if (!shouldPoll) {
    stopPolling()
    return
  }
  if (pollTimer === null) {
    pollTimer = window.setInterval(() => {
      void loadTasks(true)
    }, POLL_INTERVAL_MS)
  }
}

// updateTrackedTransitions reconciles both current-page items and off-page watched_items
// against trackedActiveTaskIds so completion toasts and toolbar badge counts stay accurate
// even when older active tasks move past page one or the operator views another page.
function updateTrackedTransitions(res: UsageExportTaskListResponse, isSilentPoll: boolean): void {
  const previousActive = trackedActiveTaskIds.value
  const fetchedItems = Array.isArray(res.items) ? res.items : []
  const knownById = new Map<number, UsageExportTask>()
  for (const item of fetchedItems) {
    knownById.set(item.id, item)
  }
  if (Array.isArray(res.watched_items)) {
    for (const item of res.watched_items) {
      knownById.set(item.id, item)
    }
  }

  const nextActive = new Set<number>()
  if (Array.isArray(res.active_ids)) {
    for (const id of res.active_ids) {
      if (typeof id === 'number' && id > 0) {
        nextActive.add(id)
      }
    }
  }
  // Always retain any returned item whose status is still pending or running so
  // polling never stops before the UI has observed that row's terminal state.
  for (const item of knownById.values()) {
    if (isTaskActive(item.status)) {
      nextActive.add(item.id)
    }
  }

  if (isSilentPoll) {
    for (const prevId of previousActive) {
      if (nextActive.has(prevId)) {
        continue
      }
      const settled = knownById.get(prevId)
      if (settled?.status === 'succeeded') {
        appStore.showSuccess(t('usage.exportPanel.taskCompletedToast'))
      } else if (settled?.status === 'failed') {
        appStore.showError(t('usage.exportPanel.taskFailedToast'))
      }
    }
  }

  trackedActiveTaskIds.value = nextActive
  const nextCount =
    typeof res.active_count === 'number'
      ? Math.max(nextActive.size, res.active_count)
      : nextActive.size
  globalActiveTaskCount.value = nextCount
  emit('tasksChanged', nextCount)
  emit('activeIdsChanged', [...nextActive])
}

// loadTasks fetches the current page of user or administrator export tasks while mounted
// and visible. Silent polls preserve existing rows without toggling initial skeletons.
async function loadTasks(silent = false): Promise<void> {
  if (!isDialogActive()) {
    return
  }
  abortInFlightList()
  const controller = new AbortController()
  listAbortController = controller
  const seq = ++listReqSeq
  const watchIds =
    trackedActiveTaskIds.value.size > 0 ? [...trackedActiveTaskIds.value].join(',') : undefined

  if (!silent) {
    tasksLoading.value = true
    if (tasks.value.length === 0) {
      tasksInitialLoading.value = true
    }
  }

  try {
    const params = {
      page: tasksPage.value,
      page_size: tasksPageSize,
      ...(watchIds ? { watch_ids: watchIds } : {}),
    }
    const res =
      props.mode === 'admin'
        ? await adminUsageAPI.listExportTasks(params, { signal: controller.signal })
        : await usageAPI.listExportTasks(params, { signal: controller.signal })

    if (!isDialogActive() || seq !== listReqSeq) {
      return
    }
    tasks.value = res.items || []
    tasksTotal.value = res.total || 0
    updateTrackedTransitions(res, silent)
    syncPollingInterval()
  } catch (err: unknown) {
    // Aborted polls or post-unmount rejections need no error toast.
    if (isUnmounted || controller.signal.aborted) {
      return
    }
    if (!silent && props.show) {
      appStore.showError(resolveExportErrorMessage(err, 'usage.exportPanel.loadTasksFailed'))
    }
  } finally {
    if (!isUnmounted && seq === listReqSeq) {
      tasksLoading.value = false
      tasksInitialLoading.value = false
    }
  }
}

// submitCreateTask validates bounds, formats explicit-offset RFC3339 timestamps directly
// from startInstant and endInstant so DST fall-back offsets stay intact, submits the
// export task, and refreshes page 1 only when the dialog remains mounted and open.
async function submitCreateTask(): Promise<void> {
  if (!isRangeValid.value || creatingTask.value || !startInstant.value || !endInstant.value) {
    return
  }
  const startRfc3339 = formatInstantRFC3339InTimezone(startInstant.value, selectedTimezone.value)
  const endRfc3339 = formatInstantRFC3339InTimezone(endInstant.value, selectedTimezone.value)
  if (!startRfc3339 || !endRfc3339) {
    appStore.showError(t('usage.exportPanel.errors.invalidTimeRange'))
    return
  }

  const normalizedLocale = locale.value.toLowerCase().startsWith('zh') ? 'zh' : 'en'
  const inheritedFilters =
    hasActivePageFilters.value && includePageFilters.value ? activeFilterPayload.value : {}

  creatingTask.value = true
  try {
    let created: UsageExportTask
    if (props.mode === 'admin') {
      const payload: CreateAdminUsageExportTaskRequest = {
        start_time: startRfc3339,
        end_time: endRfc3339,
        timezone: selectedTimezone.value,
        locale: normalizedLocale,
        ...inheritedFilters,
      }
      created = await adminUsageAPI.createExportTask(payload)
    } else {
      const payload: CreateUserUsageExportTaskRequest = {
        start_time: startRfc3339,
        end_time: endRfc3339,
        timezone: selectedTimezone.value,
        locale: normalizedLocale,
        ...inheritedFilters,
      }
      created = await usageAPI.createExportTask(payload)
    }

    // Discard post-unmount completions so navigating away never restarts list polling.
    if (isUnmounted) {
      return
    }

    appStore.showSuccess(t('usage.exportPanel.taskCreatedToast'))
    if (!props.show) {
      // If the user closed the modal while staying on UsageView, hand the new active ID
      // to the page-level indicator without starting open-dialog polling.
      if (created && isTaskActive(created.status)) {
        const nextActive = new Set(trackedActiveTaskIds.value)
        nextActive.add(created.id)
        trackedActiveTaskIds.value = nextActive
        globalActiveTaskCount.value = Math.max(globalActiveTaskCount.value, nextActive.size)
        emit('activeIdsChanged', [...nextActive])
        emit('tasksChanged', globalActiveTaskCount.value)
      }
      return
    }

    tasksPage.value = 1
    await loadTasks(false)
  } catch (err: unknown) {
    if (!isUnmounted) {
      appStore.showError(resolveExportErrorMessage(err, 'usage.exportPanel.createTaskFailed'))
    }
  } finally {
    if (!isUnmounted) {
      creatingTask.value = false
    }
  }
}

// handleCancelTask cancels a queued or running export task and refreshes active counts.
async function handleCancelTask(task: UsageExportTask): Promise<void> {
  if (cancelingTaskIds.value.has(task.id)) {
    return
  }
  const nextSet = new Set(cancelingTaskIds.value)
  nextSet.add(task.id)
  cancelingTaskIds.value = nextSet

  try {
    const updated =
      props.mode === 'admin'
        ? await adminUsageAPI.cancelExportTask(task.id)
        : await usageAPI.cancelExportTask(task.id)

    if (isUnmounted) {
      return
    }

    tasks.value = tasks.value.map((item) => (item.id === task.id ? updated : item))
    const nextTracked = new Set(trackedActiveTaskIds.value)
    const wasTracked = nextTracked.delete(task.id)
    trackedActiveTaskIds.value = nextTracked
    if (wasTracked && globalActiveTaskCount.value > 0) {
      globalActiveTaskCount.value = Math.max(nextTracked.size, globalActiveTaskCount.value - 1)
    } else {
      globalActiveTaskCount.value = nextTracked.size
    }
    emit('tasksChanged', globalActiveTaskCount.value)
    emit('activeIdsChanged', [...nextTracked])
    syncPollingInterval()
    appStore.showInfo(t('usage.exportPanel.taskCanceledToast'))
  } catch (err: unknown) {
    if (!isUnmounted) {
      appStore.showError(resolveExportErrorMessage(err, 'usage.exportPanel.cancelTaskFailed'))
      if (isDialogActive()) {
        void loadTasks(true)
      }
    }
  } finally {
    if (!isUnmounted) {
      const cleaned = new Set(cancelingTaskIds.value)
      cleaned.delete(task.id)
      cancelingTaskIds.value = cleaned
    }
  }
}

// handleDownloadTask exchanges a 60-second signed ticket and triggers native browser CSV
// streaming. Expired artifacts refresh the list so the row transitions to expired status.
async function handleDownloadTask(task: UsageExportTask): Promise<void> {
  if (downloadingTaskIds.value.has(task.id)) {
    return
  }
  const nextSet = new Set(downloadingTaskIds.value)
  nextSet.add(task.id)
  downloadingTaskIds.value = nextSet

  try {
    const ticket =
      props.mode === 'admin'
        ? await adminUsageAPI.createExportDownloadTicket(task.id)
        : await usageAPI.createExportDownloadTicket(task.id)
    if (isUnmounted) {
      return
    }
    usageAPI.triggerExportDownloadUrl(ticket.download_url, task.file_name)
  } catch (err: unknown) {
    if (!isUnmounted) {
      appStore.showError(resolveExportErrorMessage(err, 'usage.exportPanel.downloadFailed'))
      if (isDialogActive()) {
        void loadTasks(true)
      }
    }
  } finally {
    if (!isUnmounted) {
      const cleaned = new Set(downloadingTaskIds.value)
      cleaned.delete(task.id)
      downloadingTaskIds.value = cleaned
    }
  }
}

// handlePageChange navigates to another task list page and fetches its records.
function handlePageChange(nextPage: number): void {
  tasksPage.value = nextPage
  void loadTasks(false)
}

// handleClose notifies the parent view to dismiss the modal.
function handleClose(): void {
  emit('close')
}

// Re-initialize inherited page bounds and task polling whenever the dialog opens, and
// tear down timers and in-flight GET polls immediately when it closes.
watch(
  () => props.show,
  (visible) => {
    if (visible) {
      if (!isValidIanaTimezone(selectedTimezone.value)) {
        selectedTimezone.value = getBrowserTimezone()
      }
      includePageFilters.value = true
      tasksPage.value = 1
      applyPageDateRange()
      void loadTasks(false)
    } else {
      listReqSeq += 1
      stopPolling()
      abortInFlightList()
    }
  },
  { immediate: true },
)

// Release polling timers, mark unmounted, and abort in-flight requests on unmount.
onUnmounted(() => {
  isUnmounted = true
  listReqSeq += 1
  stopPolling()
  abortInFlightList()
})
</script>
