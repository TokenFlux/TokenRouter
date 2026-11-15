<template>
  <BaseDialog
    :show="show"
    :title="t('admin.usage.cleanup.title')"
    :subtitle="t('admin.usage.cleanup.subtitle')"
    width="wide"
    @close="handleClose"
  >
    <template #header-icon>
      <div
        class="flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-rose-500/10 text-rose-600 dark:bg-rose-500/15 dark:text-rose-400"
      >
        <Icon name="trash" size="sm" :animate-on-hover="false" />
      </div>
    </template>

    <div class="space-y-4">
      <!-- Top section: configure calendar bounds, IANA timezone, narrowing filters, and submit. -->
      <div
        class="space-y-4 rounded-surface border border-gray-200 bg-gray-50/60 p-4 dark:border-dark-600 dark:bg-dark-950/60"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-dark-50">
            {{ t('admin.usage.cleanup.scopeSectionTitle') }}
          </h4>
          <div class="flex flex-wrap items-center gap-1.5">
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              data-testid="cleanup-sync-page"
              @click="syncFromPage"
            >
              {{ t('admin.usage.cleanup.syncPageScope') }}
            </button>
            <button
              v-if="effectiveFilterChips.length > 0"
              type="button"
              class="btn btn-ghost btn-sm"
              data-testid="cleanup-clear-filters"
              @click="clearAllFilters"
            >
              {{ t('admin.usage.cleanup.clearFilters') }}
            </button>
          </div>
        </div>

        <!-- Calendar date range picker and IANA timezone selector. -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.usage.cleanup.dateRangeLabel') }}</label>
            <DateRangePicker
              v-model:start-date="localStartDate"
              v-model:end-date="localEndDate"
              date-only
            />
          </div>
          <div>
            <label class="input-label">{{ t('admin.usage.cleanup.timezoneLabel') }}</label>
            <div class="flex items-center gap-2">
              <div class="min-w-0 flex-1">
                <Select
                  :model-value="selectedTimezone"
                  :options="timezoneOptions"
                  searchable
                  :search-placeholder="t('admin.usage.cleanup.timezoneSearchPlaceholder')"
                  :aria-label="t('admin.usage.cleanup.timezoneLabel')"
                  @update:model-value="handleTimezoneSelect"
                />
              </div>
              <button
                type="button"
                class="btn btn-secondary shrink-0"
                data-testid="cleanup-switch-utc"
                :disabled="selectedTimezone === 'UTC'"
                @click="handleTimezoneSelect('UTC')"
              >
                <Icon name="globe" size="sm" />
                <span>{{ t('admin.usage.cleanup.switchUtc') }}</span>
              </button>
            </div>
          </div>
        </div>

        <!-- Live inclusive window, day-span badge, and per-task range limit guidance. -->
        <div
          :class="[
            'rounded-control border px-3 py-2 text-xs',
            !isRangeValid
              ? 'border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-300'
              : exceedsDefaultRangeLimit
                ? 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200'
                : 'border-gray-200 bg-white text-gray-600 dark:border-dark-600 dark:bg-dark-900 dark:text-dark-200',
          ]"
          data-testid="cleanup-window-preview"
        >
          <div v-if="isRangeValid" class="space-y-1.5">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                <span class="font-medium text-gray-700 dark:text-dark-100">
                  {{ t('admin.usage.cleanup.windowPreviewLabel') }}:
                </span>
                <span class="font-mono tabular-nums">
                  [{{ localStartDate }} 00:00:00, {{ localEndDate }} 23:59:59] ({{
                    selectedTimezone
                  }})
                </span>
              </div>
              <span
                :class="[
                  'rounded-compact px-2 py-0.5 font-medium tabular-nums',
                  exceedsDefaultRangeLimit
                    ? 'bg-amber-100 text-amber-800 dark:bg-amber-500/20 dark:text-amber-200'
                    : 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-dark-100',
                ]"
              >
                {{ t('admin.usage.cleanup.daySpanBadge', { days: selectedDayCount }) }}
              </span>
            </div>
            <div
              v-if="exceedsDefaultRangeLimit"
              role="alert"
              class="flex items-start gap-1.5 font-medium"
            >
              <Icon
                name="exclamationTriangle"
                size="sm"
                class="mt-0.5 shrink-0"
                :animate-on-hover="false"
              />
              <span>
                {{
                  t('admin.usage.cleanup.rangeExceedsDefaultHint', {
                    days: selectedDayCount,
                    maxDays: DEFAULT_MAX_RANGE_DAYS,
                  })
                }}
              </span>
            </div>
          </div>
          <div v-else role="alert" class="flex items-center gap-1.5 font-medium">
            <Icon name="exclamationCircle" size="sm" class="shrink-0" :animate-on-hover="false" />
            <span>{{ t('admin.usage.cleanup.invalidRangeError') }}</span>
          </div>
        </div>

        <!-- Optional narrowing filters, unsupported-predicate warning, and inline filter matrix. -->
        <div class="space-y-2.5">
          <SettingsNotice
            v-if="excludedFilterNames.length > 0"
            tone="warning"
            data-testid="cleanup-excluded-filters-notice"
          >
            {{
              t('admin.usage.cleanup.excludedPageFiltersNotice', {
                filters: excludedFilterNames.join(', '),
              })
            }}
          </SettingsNotice>

          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-200">
              {{ t('admin.usage.cleanup.filtersLabel') }}
            </span>
            <span
              v-if="effectiveFilterChips.length > 0"
              class="text-xs tabular-nums text-primary-700 dark:text-primary-400"
            >
              {{
                t('admin.usage.cleanup.activeFilterCountLabel', {
                  count: effectiveFilterChips.length,
                })
              }}
            </span>
          </div>

          <div
            v-if="effectiveFilterChips.length === 0"
            class="flex items-start gap-2 rounded-control border border-amber-200 bg-amber-50/80 px-3 py-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
            data-testid="cleanup-unfiltered-scope"
          >
            <Icon
              name="exclamationTriangle"
              size="sm"
              class="mt-0.5 shrink-0"
              :animate-on-hover="false"
            />
            <div class="min-w-0 space-y-0.5">
              <div class="font-medium">{{ t('admin.usage.cleanup.unfilteredScopeBadge') }}</div>
              <p class="text-amber-700 dark:text-amber-300">
                {{ t('admin.usage.cleanup.unfilteredScopeHint') }}
              </p>
            </div>
          </div>

          <div v-else class="flex flex-wrap gap-1.5" data-testid="cleanup-active-filter-chips">
            <span
              v-for="chip in effectiveFilterChips"
              :key="chip.key"
              class="inline-flex items-center gap-1 rounded-compact border border-primary-500/20 bg-primary-500/8 py-0.5 pl-2 pr-1 text-xs text-primary-700 dark:border-primary-500/25 dark:bg-primary-500/10 dark:text-primary-400"
            >
              <span class="font-medium">{{ chip.label }}:</span>
              <span>{{ chip.value }}</span>
              <button
                type="button"
                class="inline-flex h-4 w-4 items-center justify-center rounded-compact text-primary-700/70 transition-colors hover:bg-primary-500/15 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"
                :aria-label="t('admin.usage.cleanup.removeFilterAria', { label: chip.label })"
                :data-testid="`cleanup-remove-chip-${chip.key}`"
                @click="removeFilterChip(chip.key)"
              >
                <Icon name="x" size="xs" :animate-on-hover="false" />
              </button>
            </span>
          </div>

          <div
            class="rounded-surface border border-gray-200 bg-white p-1.5 dark:border-dark-600 dark:bg-dark-900"
          >
            <UsageFilters
              ref="cleanupFiltersRef"
              v-model="localFilters"
              mode="cleanup"
              variant="inline"
              flat
              :show-actions="false"
              :start-date="localStartDate"
              :end-date="localEndDate"
              :model-options="modelOptions"
              :seed-labels="seedLabels"
              @change="handleFiltersChange"
            />
          </div>
        </div>

        <!-- Irreversibility warning paired with the destructive submit CTA. -->
        <div
          class="flex flex-col gap-3 border-t border-gray-200 pt-3 dark:border-dark-600 sm:flex-row sm:items-center sm:justify-between"
        >
          <div class="flex items-start gap-2 text-xs text-amber-700 dark:text-amber-300">
            <Icon
              name="exclamationTriangle"
              size="sm"
              class="mt-0.5 shrink-0"
              :animate-on-hover="false"
            />
            <p class="min-w-0">{{ t('admin.usage.cleanup.warning') }}</p>
          </div>
          <button
            type="button"
            class="btn btn-danger shrink-0"
            data-testid="open-cleanup-confirm-btn"
            :disabled="!isRangeValid || submitting"
            @click="openConfirm"
          >
            <Icon
              :name="submitting ? 'loader' : 'trash'"
              size="sm"
              :class="submitting && 'animate-spin'"
              :animate-on-hover="!submitting"
            />
            <span>
              {{
                submitting ? t('admin.usage.cleanup.submitting') : t('admin.usage.cleanup.submit')
              }}
            </span>
          </button>
        </div>
      </div>

      <!-- Bottom section: paginated cleanup task history with skeleton loading and live polling. -->
      <div
        class="overflow-hidden rounded-surface border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-900"
      >
        <div
          class="flex items-center justify-between border-b border-gray-100 px-4 py-3 dark:border-dark-700"
        >
          <div class="flex items-center gap-2">
            <h4 class="text-sm font-semibold text-gray-900 dark:text-dark-50">
              {{ t('admin.usage.cleanup.recentTasks') }}
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
            data-testid="refresh-cleanup-tasks-btn"
            :disabled="tasksLoading"
            @click="loadTasks(false)"
          >
            <Icon
              name="refresh"
              size="sm"
              :class="tasksLoading && 'animate-spin'"
              :animate-on-hover="!tasksLoading"
            />
            <span>{{ t('common.refresh') }}</span>
          </button>
        </div>

        <div class="p-4">
          <div
            v-if="tasksInitialLoading && tasks.length === 0"
            class="space-y-2"
            aria-busy="true"
            data-testid="cleanup-tasks-skeleton"
          >
            <Skeleton v-for="idx in 3" :key="idx" :height="68" />
          </div>

          <div
            v-else-if="tasks.length === 0"
            class="py-6 text-center text-sm text-gray-500 dark:text-dark-400"
            data-testid="cleanup-tasks-empty"
          >
            {{ t('admin.usage.cleanup.noTasks') }}
          </div>

          <div v-else class="space-y-2.5" data-testid="cleanup-tasks-list">
            <div
              v-for="task in tasks"
              :key="task.id"
              class="space-y-2 rounded-control border border-gray-200 p-3 dark:border-dark-600"
              :data-testid="`cleanup-task-card-${task.id}`"
            >
              <div class="flex flex-wrap items-center justify-between gap-2">
                <div class="flex flex-wrap items-center gap-2">
                  <span
                    :class="[
                      'inline-flex items-center gap-1 rounded-compact px-2 py-0.5 text-xs font-medium',
                      statusClass(task.status),
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
                    v-if="taskFilterChips(task.filters).length === 0"
                    class="rounded-compact bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-300"
                  >
                    {{ t('admin.usage.cleanup.scopeAllRecords') }}
                  </span>
                  <template v-else>
                    <span
                      v-for="chip in taskFilterChips(task.filters)"
                      :key="`${task.id}-${chip.key}`"
                      class="rounded-compact bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-200"
                    >
                      {{ chip.label }}: {{ chip.value }}
                    </span>
                  </template>
                </div>

                <div class="flex flex-wrap items-center gap-2">
                  <span class="text-xs tabular-nums text-gray-400 dark:text-dark-400">
                    {{
                      t('admin.usage.cleanup.createdAtLabel', {
                        time: formatInstantDisplay(task.created_at),
                      })
                    }}
                  </span>
                  <button
                    v-if="canCancel(task)"
                    type="button"
                    class="btn btn-secondary btn-sm text-rose-600 hover:text-rose-700 dark:text-rose-400"
                    :data-testid="`cancel-cleanup-task-${task.id}`"
                    :disabled="cancelingTaskIds.has(task.id)"
                    @click="openCancelConfirm(task)"
                  >
                    {{
                      cancelingTaskIds.has(task.id)
                        ? t('admin.usage.cleanup.canceling')
                        : t('admin.usage.cleanup.cancel')
                    }}
                  </button>
                </div>
              </div>

              <div
                class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500 dark:text-dark-300"
              >
                <span class="font-mono tabular-nums text-gray-700 dark:text-dark-200">
                  {{ formatRange(task) }}
                </span>
                <span class="tabular-nums">
                  {{
                    t('admin.usage.cleanup.deletedRowsCount', {
                      count: task.deleted_rows.toLocaleString(),
                    })
                  }}
                </span>
              </div>

              <p
                v-if="task.error_message"
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
          :page-size-options="[5]"
          :show-page-size-selector="false"
          :show-jump="true"
          @update:page="handleTaskPageChange"
          @update:pageSize="handleTaskPageSizeChange"
        />
      </div>
    </div>
  </BaseDialog>

  <ConfirmDialog
    :show="confirmVisible"
    :title="t('admin.usage.cleanup.confirmTitle')"
    :message="t('admin.usage.cleanup.confirmMessage')"
    :confirm-text="t('admin.usage.cleanup.confirmSubmit')"
    :loading="submitting"
    :z-index="Z_INDEX.MODAL_NESTED"
    danger
    @confirm="submitCleanup"
    @cancel="confirmVisible = false"
  >
    <div class="space-y-3">
      <div
        class="space-y-2 rounded-control border border-gray-200 bg-gray-50/80 p-3 text-xs dark:border-dark-600 dark:bg-dark-950/60"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="font-medium text-gray-700 dark:text-dark-200">
            {{ t('admin.usage.cleanup.confirmRangeLabel') }}
          </span>
          <span class="font-mono tabular-nums text-gray-900 dark:text-dark-50">
            {{ localStartDate }} ~ {{ localEndDate }} ({{ selectedTimezone }}) ·
            {{ t('admin.usage.cleanup.daySpanBadge', { days: selectedDayCount ?? 0 }) }}
          </span>
        </div>
        <div v-if="effectiveFilterChips.length > 0" class="space-y-1.5 pt-1">
          <div class="font-medium text-gray-700 dark:text-dark-200">
            {{ t('admin.usage.cleanup.confirmScopeLabel') }}
          </div>
          <div class="flex flex-wrap gap-1.5">
            <span
              v-for="chip in effectiveFilterChips"
              :key="`confirm-${chip.key}`"
              class="inline-flex items-center gap-1 rounded-compact border border-primary-500/20 bg-primary-500/8 px-2 py-0.5 text-xs text-primary-700 dark:border-primary-500/25 dark:bg-primary-500/10 dark:text-primary-400"
            >
              <span class="font-medium">{{ chip.label }}:</span>
              <span>{{ chip.value }}</span>
            </span>
          </div>
        </div>
      </div>
      <SettingsNotice
        v-if="effectiveFilterChips.length === 0"
        tone="warning"
        data-testid="cleanup-confirm-all-records-warning"
      >
        {{ t('admin.usage.cleanup.confirmAllRecordsWarning') }}
      </SettingsNotice>
    </div>
  </ConfirmDialog>

  <ConfirmDialog
    :show="cancelConfirmVisible"
    :title="t('admin.usage.cleanup.cancelConfirmTitle')"
    :message="t('admin.usage.cleanup.cancelConfirmMessage')"
    :confirm-text="t('admin.usage.cleanup.cancelConfirm')"
    :loading="canceling"
    :z-index="Z_INDEX.MODAL_NESTED"
    danger
    @confirm="cancelTask"
    @cancel="cancelConfirmVisible = false"
  >
    <div
      v-if="cancelTarget"
      class="space-y-1 rounded-control border border-gray-200 bg-gray-50/80 p-3 text-xs dark:border-dark-600 dark:bg-dark-950/60"
    >
      <div class="font-mono font-medium text-gray-900 dark:text-dark-50">
        #{{ cancelTarget.id }}
      </div>
      <div class="font-mono tabular-nums text-gray-600 dark:text-dark-300">
        {{ formatRange(cancelTarget) }}
      </div>
    </div>
  </ConfirmDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import Icon from '@/components/icons/Icon.vue'
import UsageFilters from '@/components/admin/usage/UsageFilters.vue'
import { adminUsageAPI } from '@/api/admin/usage'
import type {
  AdminUsageQueryParams,
  CreateUsageCleanupTaskRequest,
  UsageCleanupFilters,
  UsageCleanupTask,
} from '@/api/admin/usage'
import { Z_INDEX } from '@/constants/overlay'
import type { UsageExportFilterLabel } from '@/types/ui'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'
import {
  formatInstantInTimezoneDisplay,
  getBrowserTimezone,
  getTimezoneSelectOptions,
  instantToZonedLocalDateTime,
  isValidIanaTimezone,
} from '@/utils/timezone'
import { requestTypeToLegacyStream } from '@/utils/usageRequestType'

// UsageCleanupDialog.vue configures administrator usage cleanup tasks with calendar date
// bounds, explicit IANA timezone selection, and backend-compatible narrowing filters
// inside a BaseDialog, then displays persisted task progress below. When opened or synced
// from the usage page, it normalizes timestamp bounds to YYYY-MM-DD, seeds resolved
// labels, and surfaces any host-page filters omitted from batch cleanup so operators
// see the exact deletion scope before nested confirmation.
//
// Background polling runs every three seconds only while the dialog stays open and an
// active task remains pending or running. In-flight task list requests carry an
// AbortController and monotonic sequence counter so aborted or superseded polls never
// overwrite newer page state or restart polling after close or unmount.

// Props supplies dialog visibility, host page filters, calendar bounds, and optional
// resolved filter labels and model choices.
interface Props {
  show: boolean // Controls modal visibility.
  // Host page filters copied into an editable local draft on dialog open or sync.
  filters: AdminUsageQueryParams
  startDate: string // Host page starting date or timestamp text.
  endDate: string // Host page ending date or timestamp text.
  filterLabels?: UsageExportFilterLabel[] // Pre-resolved filter labels from host UsageFilters.
  modelOptions?: string[] // Model identifier choices forwarded to inline UsageFilters.
}

// props and emit bind parent visibility and close requests for the cleanup dialog.
const props = defineProps<Props>()
const emit = defineEmits(['close'])

// Translation and toast notifications provide localized copy and feedback for cleanup
// task creation, polling transitions, and cancellation.
const { t } = useI18n()
const appStore = useAppStore()

// POLL_INTERVAL_MS, DEFAULT_MAX_RANGE_DAYS, and CLEANUP_ERROR_KEYS govern active-task
// polling cadence, default range-limit guidance, and structured error mapping.
const POLL_INTERVAL_MS = 3000
const DEFAULT_MAX_RANGE_DAYS = 31
const CLEANUP_ERROR_KEYS: Record<string, string> = {
  USAGE_CLEANUP_DISABLED: 'admin.usage.cleanup.errors.disabled',
  USAGE_CLEANUP_MISSING_RANGE: 'admin.usage.cleanup.errors.missingRange',
  USAGE_CLEANUP_INVALID_RANGE: 'admin.usage.cleanup.errors.invalidRange',
  USAGE_CLEANUP_RANGE_TOO_LARGE: 'admin.usage.cleanup.errors.rangeTooLarge',
  USAGE_CLEANUP_TASK_NOT_FOUND: 'admin.usage.cleanup.errors.notFound',
  USAGE_CLEANUP_CANCEL_CONFLICT: 'admin.usage.cleanup.errors.cancelConflict',
}

// Draft state isolates calendar bounds, IANA timezone, supported filters, seeded labels,
// omitted host-page predicates, and a revision counter that refreshes summary chips.
const localFilters = ref<AdminUsageQueryParams>({})
const localStartDate = ref('')
const localEndDate = ref('')
const selectedTimezone = ref<string>(getBrowserTimezone())
const seedLabels = ref<UsageExportFilterLabel[]>([])
const excludedFilterNames = ref<string[]>([])
const filterRevision = ref(0)
const cleanupFiltersRef = ref<InstanceType<typeof UsageFilters> | null>(null)

// Task list and modal confirmation state track paginated task records, loading skeletons,
// active task IDs for completion toasts, and nested submission/cancel confirmations.
const tasks = ref<UsageCleanupTask[]>([])
const tasksInitialLoading = ref(false)
const tasksLoading = ref(false)
const tasksPage = ref(1)
const tasksPageSize = ref(5)
const tasksTotal = ref(0)
const submitting = ref(false)
const confirmVisible = ref(false)
const cancelConfirmVisible = ref(false)
const canceling = ref(false)
const cancelTarget = ref<UsageCleanupTask | null>(null)
const cancelingTaskIds = ref<Set<number>>(new Set())
const trackedActiveTaskIds = ref<Set<number>>(new Set())

// pollTimer, listAbortController, listReqSeq, and isUnmounted coordinate polling so
// closed or unmounted dialogs drop stale requests and cancel scheduled refreshes.
let pollTimer: number | null = null
let listAbortController: AbortController | null = null
let listReqSeq = 0
let isUnmounted = false

// parseCalendarDate parses a YYYY-MM-DD prefix into a validated UTC midnight timestamp
// so day spans remain immune to browser daylight-saving offsets. Overflowing calendar
// dates such as 2026-02-30 and malformed strings return null to keep draft bounds safe.
function parseCalendarDate(raw: string): { normalized: string; utcMs: number } | null {
  const candidate = raw.trim().slice(0, 10)
  if (!/^\d{4}-\d{2}-\d{2}$/.test(candidate)) {
    return null
  }
  const [year, month, day] = candidate.split('-').map(Number)
  const utcMs = Date.UTC(year, month - 1, day)
  const check = new Date(utcMs)
  if (
    check.getUTCFullYear() !== year ||
    check.getUTCMonth() + 1 !== month ||
    check.getUTCDate() !== day
  ) {
    return null
  }
  return { normalized: candidate, utcMs }
}

// normalizeDateBound coerces date or timestamp inputs to YYYY-MM-DD so date-only cleanup
// requests never carry time components. Empty or unparseable host bounds fall back to
// today's local date interpreted in fallbackZone.
function normalizeDateBound(raw: string, fallbackZone: string): string {
  const parsed = parseCalendarDate(raw)
  if (parsed) {
    return parsed.normalized
  }
  return instantToZonedLocalDateTime(new Date(), fallbackZone).slice(0, 10)
}

// selectedDayCount, isRangeValid, and exceedsDefaultRangeLimit compute inclusive
// calendar days, validity, and whether the span exceeds DEFAULT_MAX_RANGE_DAYS.
const selectedDayCount = computed<number | null>(() => {
  const start = parseCalendarDate(localStartDate.value)
  const end = parseCalendarDate(localEndDate.value)
  if (!start || !end || end.utcMs < start.utcMs) {
    return null
  }
  return Math.round((end.utcMs - start.utcMs) / 86400000) + 1
})

const isRangeValid = computed(() => selectedDayCount.value !== null && selectedDayCount.value >= 1)

const exceedsDefaultRangeLimit = computed(
  () => selectedDayCount.value !== null && selectedDayCount.value > DEFAULT_MAX_RANGE_DAYS,
)

// timezoneOptions builds searchable IANA zone choices anchored at the start date.
const timezoneOptions = computed(() => {
  const parsed = parseCalendarDate(localStartDate.value)
  const anchor = parsed ? new Date(parsed.utcMs) : new Date()
  return getTimezoneSelectOptions(anchor, selectedTimezone.value)
})

// buildFallbackChips derives chip summaries directly from localFilters and seedLabels
// during the initial render tick before cleanupFiltersRef completes mounting. Seeded
// host labels take precedence over raw numeric IDs when available.
function buildFallbackChips(): UsageExportFilterLabel[] {
  const current = localFilters.value
  const labelMap = new Map((seedLabels.value ?? []).map((item) => [item.key, item]))
  const chips: UsageExportFilterLabel[] = []
  // pushField appends either the seeded host chip or a fallback label derived from rawValue.
  const pushField = (key: string, labelKey: string, rawValue: string) => {
    const seeded = labelMap.get(key)
    chips.push(seeded ?? { key, label: t(labelKey), value: rawValue })
  }

  if (typeof current.team_id === 'number' && current.team_id > 0) {
    pushField('team_id', 'admin.usage.teamFilter', `#${current.team_id}`)
  }
  if (
    typeof current.request_source === 'string' &&
    current.request_source.trim() !== '' &&
    current.request_source.trim() !== 'all'
  ) {
    pushField('request_source', 'admin.usage.requestSource', current.request_source.trim())
  }
  if (typeof current.user_id === 'number' && current.user_id > 0) {
    pushField('user_id', 'admin.usage.userFilter', `#${current.user_id}`)
  }
  if (typeof current.api_key_id === 'number' && current.api_key_id > 0) {
    pushField('api_key_id', 'usage.apiKeyFilter', `#${current.api_key_id}`)
  }
  if (typeof current.model === 'string' && current.model.trim() !== '') {
    pushField('model', 'usage.model', current.model.trim())
  }
  if (typeof current.provider_id === 'number' && current.provider_id > 0) {
    pushField('provider_id', 'admin.usage.provider', `#${current.provider_id}`)
  }
  if (typeof current.group_id === 'number' && current.group_id > 0) {
    pushField('group_id', 'admin.usage.group', `#${current.group_id}`)
  }
  if (typeof current.request_type === 'string' && current.request_type.trim() !== '') {
    pushField('request_type', 'usage.type', current.request_type.trim())
  } else if (typeof current.stream === 'boolean') {
    pushField('request_type', 'usage.type', current.stream ? t('usage.stream') : t('usage.sync'))
  }
  if (typeof current.billing_type === 'number' && !Number.isNaN(current.billing_type)) {
    pushField('billing_type', 'admin.usage.billingType', String(current.billing_type))
  }
  return chips
}

// effectiveFilterChips reflects the active cleanup predicates shown above the inline
// filter matrix and inside the confirmation summary.
const effectiveFilterChips = computed<UsageExportFilterLabel[]>(() => {
  void filterRevision.value
  if (cleanupFiltersRef.value?.getFilterSummaryLabels) {
    return cleanupFiltersRef.value.getFilterSummaryLabels()
  }
  return buildFallbackChips()
})

// handleFiltersChange bumps filterRevision when inline UsageFilters updates predicates or
// finishes resolving team and group labels.
function handleFiltersChange() {
  filterRevision.value += 1
}

// handleTimezoneSelect applies a validated IANA timezone selection to selectedTimezone.
function handleTimezoneSelect(nextValue: string | number | boolean | null) {
  if (typeof nextValue === 'string' && isValidIanaTimezone(nextValue)) {
    selectedTimezone.value = nextValue.trim()
  }
}

// syncFromPage copies normalized host dates and cleanup-supported predicates into the
// local draft, replacing any previous draft selections. Unsupported host predicates are
// omitted from localFilters and collected in excludedFilterNames so a notice warns the
// operator before submission.
function syncFromPage() {
  const zone = isValidIanaTimezone(selectedTimezone.value)
    ? selectedTimezone.value
    : getBrowserTimezone()
  selectedTimezone.value = zone
  localStartDate.value = normalizeDateBound(props.startDate, zone)
  localEndDate.value = normalizeDateBound(props.endDate, zone)

  const src = props.filters || {}
  const next: AdminUsageQueryParams = {
    start_date: localStartDate.value,
    end_date: localEndDate.value,
  }
  if (typeof src.team_id === 'number' && src.team_id > 0) next.team_id = src.team_id
  if (typeof src.request_source === 'string' && src.request_source.trim() !== '') {
    next.request_source = src.request_source
  }
  if (typeof src.user_id === 'number' && src.user_id > 0) next.user_id = src.user_id
  if (typeof src.api_key_id === 'number' && src.api_key_id > 0) next.api_key_id = src.api_key_id
  if (typeof src.provider_id === 'number' && src.provider_id > 0) next.provider_id = src.provider_id
  if (typeof src.group_id === 'number' && src.group_id > 0) next.group_id = src.group_id
  if (typeof src.model === 'string' && src.model.trim() !== '') next.model = src.model
  if (src.request_type) next.request_type = src.request_type
  if (typeof src.stream === 'boolean') next.stream = src.stream
  if (typeof src.billing_type === 'number' && !Number.isNaN(src.billing_type)) {
    next.billing_type = src.billing_type
  }

  const excluded: string[] = []
  if (typeof src.request_id === 'string' && src.request_id.trim() !== '') {
    excluded.push(t('admin.usage.requestId'))
  }
  if (typeof src.billing_mode === 'string' && src.billing_mode.trim() !== '') {
    excluded.push(t('admin.usage.billingMode'))
  }
  if (typeof src.native_compaction_v2 === 'boolean') {
    excluded.push(t('usage.compactionFilter'))
  }

  localFilters.value = next
  excludedFilterNames.value = excluded
  seedLabels.value = [...(props.filterLabels ?? [])]
  filterRevision.value += 1
}

// removeFilterChip clears one predicate from localFilters. Clearing user_id also clears
// api_key_id because keys belong to the selected user, and clearing request_type removes
// both canonical request_type and legacy stream.
function removeFilterChip(key: string) {
  const next: AdminUsageQueryParams = { ...localFilters.value }
  if (key === 'user_id') {
    next.user_id = undefined
    next.api_key_id = undefined
  } else if (key === 'request_type') {
    next.request_type = undefined
    next.stream = undefined
  } else {
    ;(next as Record<string, unknown>)[key] = undefined
  }
  localFilters.value = next
  filterRevision.value += 1
}

// clearAllFilters resets all optional narrowing filters while preserving the selected
// calendar date range and timezone. It clears seeded labels and omitted-filter warnings
// before resetting the inline UsageFilters model and advancing filterRevision.
function clearAllFilters() {
  seedLabels.value = []
  excludedFilterNames.value = []
  if (cleanupFiltersRef.value?.resetPanelFilters) {
    cleanupFiltersRef.value.resetPanelFilters()
  } else {
    localFilters.value = {
      start_date: localStartDate.value,
      end_date: localEndDate.value,
    }
  }
  filterRevision.value += 1
}

// isDialogActive reports whether the cleanup modal remains mounted and visible.
function isDialogActive(): boolean {
  return !isUnmounted && props.show
}

// stopPolling clears the active task refresh interval without cancelling requests.
function stopPolling() {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

// abortInFlightList cancels any pending cleanup task list fetch so superseded responses
// cannot overwrite newer page state.
function abortInFlightList() {
  if (listAbortController) {
    listAbortController.abort()
    listAbortController = null
  }
}

// syncPollingInterval starts three-second silent polling only while the dialog stays
// open and at least one listed cleanup task remains pending or running. When no active
// task remains or the dialog hides, it stops any running interval.
function syncPollingInterval() {
  const shouldPoll = isDialogActive() && trackedActiveTaskIds.value.size > 0
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

// handleClose stops active polling, increments listReqSeq to discard late task responses,
// aborts in-flight list requests, clears confirmation and busy flags, and requests dialog
// dismissal from the parent page.
function handleClose() {
  listReqSeq += 1
  stopPolling()
  abortInFlightList()
  confirmVisible.value = false
  cancelConfirmVisible.value = false
  canceling.value = false
  cancelTarget.value = null
  submitting.value = false
  emit('close')
}

// statusLabel translates recognized cleanup task states into localized badge captions.
// Unrecognized server state strings return verbatim so future backend states stay visible
// without breaking task rendering.
function statusLabel(status: string): string {
  const map: Record<string, string> = {
    pending: t('admin.usage.cleanup.status.pending'),
    running: t('admin.usage.cleanup.status.running'),
    succeeded: t('admin.usage.cleanup.status.succeeded'),
    failed: t('admin.usage.cleanup.status.failed'),
    canceled: t('admin.usage.cleanup.status.canceled'),
  }
  return map[status] || status
}

// statusClass returns semantic badge colors matching light and dark surface tokens for
// each cleanup task status. Unrecognized statuses receive the neutral gray palette.
function statusClass(status: string): string {
  const map: Record<string, string> = {
    pending: 'bg-amber-50 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300',
    running: 'bg-primary-500/10 text-primary-700 dark:bg-primary-500/15 dark:text-primary-400',
    succeeded: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300',
    failed: 'bg-rose-50 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300',
    canceled: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300',
  }
  return map[status] || 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-dark-300'
}

// formatInstantDisplay formats a server RFC3339 timestamp in selectedTimezone for task
// range and creation labels. Missing timestamps render as two dashes, and unparseable
// timestamp text falls back verbatim.
function formatInstantDisplay(value?: string | null): string {
  if (!value) return '--'
  return formatInstantInTimezoneDisplay(value, selectedTimezone.value) || value
}

// formatRange formats a cleanup task's inclusive start and end bounds in selectedTimezone.
function formatRange(task: UsageCleanupTask): string {
  return `${formatInstantDisplay(task.filters.start_time)} ~ ${formatInstantDisplay(task.filters.end_time)}`
}

// taskFilterChips resolves persisted task filters through inline UsageFilters catalogs.
function taskFilterChips(taskFilters: UsageCleanupFilters): UsageExportFilterLabel[] {
  return cleanupFiltersRef.value?.formatCleanupTaskFilters?.(taskFilters) ?? []
}

// resolveCleanupErrorMessage maps structured backend cleanup error codes to localized
// copy defined in CLEANUP_ERROR_KEYS. Unrecognized rejections fall back to server error
// text or the translated fallbackKey message.
function resolveCleanupErrorMessage(err: unknown, fallbackKey: string): string {
  const code = extractApiErrorCode(err)?.toUpperCase()
  if (code && CLEANUP_ERROR_KEYS[code]) {
    return t(CLEANUP_ERROR_KEYS[code])
  }
  return extractApiErrorMessage(err, t(fallbackKey))
}

// updateTrackedActiveTasks reconciles current page items against trackedActiveTaskIds so
// polling stops once visible tasks settle. Silent polls emit completion or failure
// notifications when previously active tasks reach terminal states.
function updateTrackedActiveTasks(items: UsageCleanupTask[], isSilentPoll: boolean) {
  const previousActive = trackedActiveTaskIds.value
  const byId = new Map<number, UsageCleanupTask>()
  const nextActive = new Set<number>()
  for (const item of items) {
    byId.set(item.id, item)
    if (canCancel(item)) {
      nextActive.add(item.id)
    }
  }

  if (isSilentPoll) {
    for (const prevId of previousActive) {
      if (nextActive.has(prevId)) continue
      const settled = byId.get(prevId)
      if (settled?.status === 'succeeded') {
        appStore.showSuccess(t('admin.usage.cleanup.taskCompletedToast'))
      } else if (settled?.status === 'failed') {
        appStore.showError(t('admin.usage.cleanup.taskFailedToast'))
      }
    }
  }

  trackedActiveTaskIds.value = nextActive
}

// loadTasks fetches the current cleanup task page while the dialog remains mounted and
// visible, updating tasks, pagination totals, active-task tracking, and polling timers.
// Explicit loads set tasksLoading and display skeletons only when no rows are rendered;
// silent polls retain existing rows and suppress error toasts.
//
// Each invocation aborts any prior in-flight request via listAbortController and claims a
// monotonic listReqSeq token. Late responses arriving after close, unmount, or a newer
// request are discarded before updating state or clearing loading indicators.
async function loadTasks(silent = false) {
  if (!isDialogActive()) return
  abortInFlightList()
  const controller = new AbortController()
  listAbortController = controller
  const seq = ++listReqSeq

  if (!silent) {
    tasksLoading.value = true
    if (tasks.value.length === 0) {
      tasksInitialLoading.value = true
    }
  }

  try {
    const res = await adminUsageAPI.listCleanupTasks(
      {
        page: tasksPage.value,
        page_size: tasksPageSize.value,
      },
      { signal: controller.signal },
    )
    if (!isDialogActive() || seq !== listReqSeq) return
    const items = res.items || []
    tasks.value = items
    tasksTotal.value = res.total || 0
    if (res.page) tasksPage.value = res.page
    if (res.page_size) tasksPageSize.value = res.page_size
    updateTrackedActiveTasks(items, silent)
    syncPollingInterval()
  } catch (error) {
    // Aborted polls and post-unmount rejections need no error toast; visible explicit
    // loads report failure while keeping prior rows on screen for retry.
    if (isUnmounted || controller.signal.aborted) return
    if (!silent && props.show) {
      appStore.showError(resolveCleanupErrorMessage(error, 'admin.usage.cleanup.loadFailed'))
    }
  } finally {
    if (!isUnmounted && seq === listReqSeq) {
      tasksLoading.value = false
      tasksInitialLoading.value = false
    }
  }
}

// handleTaskPageChange switches to the requested page and reloads cleanup tasks.
function handleTaskPageChange(page: number) {
  tasksPage.value = page
  void loadTasks(false)
}

// handleTaskPageSizeChange validates positive finite page sizes, updates tasksPageSize,
// and resets pagination to page one before fetching. Nonpositive or nonfinite values are
// ignored without issuing a request.
function handleTaskPageSizeChange(size: number) {
  if (!Number.isFinite(size) || size <= 0) return
  tasksPageSize.value = size
  tasksPage.value = 1
  void loadTasks(false)
}

// openConfirm opens the nested submission confirmation dialog when the selected calendar
// range is valid. Invalid or inverted date bounds show an error toast and leave the
// confirmation dialog closed.
function openConfirm() {
  if (!isRangeValid.value) {
    appStore.showError(t('admin.usage.cleanup.invalidRangeError'))
    return
  }
  confirmVisible.value = true
}

// canCancel reports whether a cleanup task remains pending or running.
function canCancel(task: UsageCleanupTask): boolean {
  return task.status === 'pending' || task.status === 'running'
}

// openCancelConfirm selects the target task and opens the nested cancellation dialog.
function openCancelConfirm(task: UsageCleanupTask) {
  cancelTarget.value = task
  cancelConfirmVisible.value = true
}

// buildPayload validates calendar bounds and builds CreateUsageCleanupTaskRequest using
// selectedTimezone and positive or non-empty predicates from localFilters. Missing or
// inverted dates show an error toast and return null so submitCleanup aborts without
// sending a request.
//
// Canonical request_type takes precedence over legacy stream: when request_type is set,
// buildPayload forwards a compatible stream flag from requestTypeToLegacyStream if one
// exists, and uses localFilters.stream directly only when request_type is absent.
// Zero-valued billing_type stays preserved because 0 identifies balance billing.
function buildPayload(): CreateUsageCleanupTaskRequest | null {
  const start = parseCalendarDate(localStartDate.value)
  const end = parseCalendarDate(localEndDate.value)
  if (!start || !end) {
    appStore.showError(t('admin.usage.cleanup.missingRange'))
    return null
  }
  if (end.utcMs < start.utcMs) {
    appStore.showError(t('admin.usage.cleanup.invalidRangeError'))
    return null
  }

  const payload: CreateUsageCleanupTaskRequest = {
    start_date: start.normalized,
    end_date: end.normalized,
    timezone: selectedTimezone.value,
  }

  const current = localFilters.value
  if (current.user_id && current.user_id > 0) payload.user_id = current.user_id
  if (current.api_key_id && current.api_key_id > 0) payload.api_key_id = current.api_key_id
  if (current.provider_id && current.provider_id > 0) payload.provider_id = current.provider_id
  if (current.group_id && current.group_id > 0) payload.group_id = current.group_id
  if (current.team_id && current.team_id > 0) payload.team_id = current.team_id
  if (typeof current.model === 'string' && current.model.trim() !== '') {
    payload.model = current.model.trim()
  }
  if (current.request_source && current.request_source !== 'all') {
    payload.request_source = current.request_source
  }
  // Canonical request_type takes precedence over legacy stream; forward compatible
  // legacy stream values only when requestTypeToLegacyStream defines one.
  if (current.request_type) {
    payload.request_type = current.request_type
    const legacyStream = requestTypeToLegacyStream(current.request_type)
    if (legacyStream !== null && legacyStream !== undefined) {
      payload.stream = legacyStream
    }
  } else if (current.stream !== null && current.stream !== undefined) {
    payload.stream = current.stream
  }
  if (current.billing_type !== null && current.billing_type !== undefined) {
    payload.billing_type = current.billing_type
  }

  return payload
}

// submitCleanup closes the nested confirmation dialog and creates a cleanup task from the
// validated draft. Invalid drafts return before sending; successes reload page one while
// failures report localized errors and preserve the draft for retry.
async function submitCleanup() {
  const payload = buildPayload()
  if (!payload) {
    confirmVisible.value = false
    return
  }
  submitting.value = true
  confirmVisible.value = false
  try {
    await adminUsageAPI.createCleanupTask(payload)
    if (isUnmounted) return
    appStore.showSuccess(t('admin.usage.cleanup.submitSuccess'))
    if (isDialogActive()) {
      tasksPage.value = 1
      await loadTasks(false)
    }
  } catch (error) {
    // Creation failure leaves the draft intact so the operator can adjust the range or
    // filters and retry without re-entering selections.
    if (!isUnmounted) {
      appStore.showError(resolveCleanupErrorMessage(error, 'admin.usage.cleanup.submitFailed'))
    }
  } finally {
    if (!isUnmounted) {
      submitting.value = false
    }
  }
}

// cancelTask submits cancellation for cancelTarget while guarding against duplicate
// concurrent clicks via cancelingTaskIds. Failures refresh tasks quietly while the dialog
// stays open, and finally releases per-task busy state and clears cancelTarget.
async function cancelTask() {
  const task = cancelTarget.value
  if (!task || cancelingTaskIds.value.has(task.id)) {
    cancelConfirmVisible.value = false
    return
  }
  canceling.value = true
  cancelConfirmVisible.value = false
  const nextSet = new Set(cancelingTaskIds.value)
  nextSet.add(task.id)
  cancelingTaskIds.value = nextSet

  try {
    await adminUsageAPI.cancelCleanupTask(task.id)
    if (isUnmounted) return
    appStore.showSuccess(t('admin.usage.cleanup.cancelSuccess'))
    if (isDialogActive()) {
      await loadTasks(false)
    }
  } catch (error) {
    // Cancellation errors keep the task list state visible and reload quietly while open
    // so status transitions reported by the server remain accurate.
    if (!isUnmounted) {
      appStore.showError(resolveCleanupErrorMessage(error, 'admin.usage.cleanup.cancelFailed'))
      if (isDialogActive()) {
        void loadTasks(true)
      }
    }
  } finally {
    if (!isUnmounted) {
      const cleaned = new Set(cancelingTaskIds.value)
      cleaned.delete(task.id)
      cancelingTaskIds.value = cleaned
      canceling.value = false
      cancelTarget.value = null
    }
  }
}

watch(
  () => props.show,
  (show) => {
    if (show) {
      tasksPage.value = 1
      tasksTotal.value = 0
      syncFromPage()
      void loadTasks(false)
    } else {
      listReqSeq += 1
      stopPolling()
      abortInFlightList()
    }
  },
  { immediate: true },
)

// Release polling timers and abort in-flight task requests when the dialog unmounts.
onUnmounted(() => {
  isUnmounted = true
  listReqSeq += 1
  stopPolling()
  abortInFlightList()
})
</script>
