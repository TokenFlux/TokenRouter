<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex min-w-0 w-full items-center gap-2 sm:w-auto">
            <div class="input-icon-wrap min-w-0 flex-1 sm:w-64 sm:flex-none">
              <Icon name="search" size="md" class="input-icon text-gray-400" />
              <input
                v-model.trim="filters.q"
                type="text"
                class="input input-has-icon"
                :aria-label="t('admin.audit.filters.q')"
                :placeholder="t('admin.audit.filters.qPlaceholder')"
                @keyup.enter="search"
              />
            </div>
            <FilterDropdown
              :active-count="activeFilterCount"
              keep-mounted
              @reset="resetAuditFilters"
            >
              <FilterField
                :label="t('requests.id')"
                icon="document"
                :value-text="filters.request_id"
              >
                <FilterTextInput
                  v-model.trim="filters.request_id"
                  mono
                  maxlength="255"
                  @submit="search"
                  @clear="clearAuditFilter('request_id')"
                />
              </FilterField>
              <FilterField
                :label="t('admin.audit.filters.actorEmail')"
                icon="userCircle"
                :value-text="filters.actor_email"
              >
                <FilterTextInput
                  v-model.trim="filters.actor_email"
                  @submit="search"
                  @clear="clearAuditFilter('actor_email')"
                />
              </FilterField>
              <FilterField
                :label="t('admin.audit.filters.action')"
                icon="edit"
                :value-text="filters.action"
              >
                <FilterTextInput
                  v-model.trim="filters.action"
                  @submit="search"
                  @clear="clearAuditFilter('action')"
                />
              </FilterField>
              <FilterField
                :label="t('admin.audit.filters.clientIp')"
                icon="globe"
                :value-text="filters.client_ip"
              >
                <FilterTextInput
                  v-model.trim="filters.client_ip"
                  @submit="search"
                  @clear="clearAuditFilter('client_ip')"
                />
              </FilterField>
              <FilterField :label="t('admin.audit.filters.method')" icon="swap">
                <FilterOptions v-model="filters.method" :options="methodOptions" @change="search" />
              </FilterField>
              <FilterField :label="t('admin.audit.filters.authMethod')" icon="lock">
                <FilterOptions
                  v-model="filters.auth_method"
                  :options="authMethodOptions"
                  @change="search"
                />
              </FilterField>
              <FilterField :label="t('admin.audit.filters.result')" icon="shieldCheck">
                <FilterOptions
                  v-model="filters.success"
                  :options="resultOptions"
                  @change="search"
                />
              </FilterField>
              <FilterField :label="t('admin.dashboard.timeRange')" icon="clock">
                <FilterOptions
                  :model-value="timeRange"
                  :options="timeRangeOptions"
                  @update:model-value="handleTimeRangeChange"
                />
              </FilterField>
            </FilterDropdown>
          </div>

          <div class="ml-auto flex flex-wrap items-center justify-end gap-2">
            <button
              type="button"
              class="btn btn-primary whitespace-nowrap px-3 sm:px-4"
              :disabled="loading"
              @click="search"
            >
              {{ t('common.search') }}
            </button>
            <button
              type="button"
              class="btn btn-danger whitespace-nowrap px-3 sm:px-4"
              @click="openClearDialog"
            >
              <Icon name="trash" size="sm" class="mr-1.5" />
              {{ t('admin.audit.clearAll') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-audit-logs-column-order"
          :columns="columns"
          :data="logs"
          :loading="loading"
          row-key="id"
        >
          <template #cell-created_at="{ value }">
            <span class="whitespace-nowrap text-gray-600 dark:text-gray-300">{{
              formatTime(value)
            }}</span>
          </template>

          <template #cell-actor="{ row }">
            <div class="min-w-0 max-w-[220px]">
              <div
                class="truncate font-medium text-gray-900 dark:text-white"
                :title="row.actor_email"
              >
                {{ row.actor_email || '—' }}
              </div>
              <div class="mt-0.5 truncate text-xs text-gray-400">
                {{ row.actor_role
                }}<span v-if="row.auth_method"> · {{ authMethodLabel(row.auth_method) }}</span>
              </div>
            </div>
          </template>

          <template #cell-action="{ row }">
            <div class="min-w-0 max-w-xs">
              <div
                class="truncate font-mono text-sm text-gray-800 dark:text-gray-200"
                :title="row.action"
              >
                {{ row.action }}
              </div>
              <div
                class="mt-0.5 truncate font-mono text-xs text-gray-400"
                :title="`${row.method} ${row.path}`"
              >
                {{ row.method }} {{ row.path }}
              </div>
            </div>
          </template>

          <template #cell-status_code="{ row }">
            <span :class="statusBadgeClass(row.status_code)">
              <span
                class="h-1.5 w-1.5 rounded-full"
                :class="statusDotClass(row.status_code)"
              ></span>

              <!-- Align the mobile-card label with the status-code baseline rather than the enclosing badge icon. -->
              <span class="self-baseline">{{ row.status_code }}</span>
            </span>
          </template>

          <template #cell-latency_ms="{ value }">
            <span class="whitespace-nowrap text-gray-500 dark:text-gray-400">{{ value }} ms</span>
          </template>

          <template #cell-client_ip="{ value }">
            <span class="whitespace-nowrap font-mono text-gray-600 dark:text-gray-300">{{
              value || '—'
            }}</span>
          </template>

          <template #cell-actions="{ row }">
            <button
              type="button"
              class="inline-flex items-center gap-1 font-medium text-primary-600 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
              @click="openDetail(row.id)"
            >
              <Icon name="eye" size="sm" />
              {{ t('admin.audit.columns.detail') }}
            </button>
          </template>

          <template #empty>
            <div class="flex flex-col items-center py-8">
              <Icon
                name="shield"
                size="xl"
                class="mb-4 h-12 w-12 text-gray-300 dark:text-dark-600"
              />
              <p class="text-sm font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.audit.empty') }}
              </p>
            </div>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="total > 0"
          :total="total"
          :page="page"
          :page-size="pageSize"
          @update:page="onPageChange"
          @update:pageSize="onPageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="detailVisible"
      :title="t('admin.audit.detail.title')"
      width="wide"
      :close-on-click-outside="true"
      @close="detailVisible = false"
    >
      <ContentSkeleton v-if="detailLoading" variant="detail" :rows="8" class="py-4" />

      <div v-else-if="detail" class="space-y-5 py-2">
        <div
          class="rounded-surface border border-gray-200 bg-gray-50/60 p-5 dark:border-dark-700 dark:bg-dark-900/60"
        >
          <div class="flex flex-wrap items-center gap-3">
            <span :class="statusBadgeClass(detail.status_code)">
              <span
                class="h-1.5 w-1.5 rounded-full"
                :class="statusDotClass(detail.status_code)"
              ></span>
              {{ detail.status_code }} {{ statusText(detail.status_code) }}
            </span>
            <span class="break-all font-mono text-base font-semibold text-gray-900 dark:text-white">
              {{ detail.action }}
            </span>
          </div>

          <div
            class="mt-3 flex items-center gap-2 rounded-control bg-white px-3 py-2 ring-1 ring-gray-200 dark:bg-dark-800 dark:ring-dark-600"
          >
            <span
              class="rounded-compact bg-gray-100 px-1.5 py-0.5 font-mono text-xs font-bold text-gray-700 dark:bg-dark-700 dark:text-gray-200"
            >
              {{ detail.method }}
            </span>
            <span class="break-all font-mono text-xs text-gray-600 dark:text-gray-300">{{
              detail.path
            }}</span>
          </div>

          <div
            class="mt-3 flex flex-wrap items-center gap-x-5 gap-y-1.5 text-xs text-gray-500 dark:text-gray-400"
          >
            <span class="inline-flex items-center gap-1.5">
              <Icon name="clock" size="xs" />
              {{ formatTime(detail.created_at) }}
            </span>
            <span>{{ t('admin.audit.detail.latency') }} {{ detail.latency_ms }} ms</span>
            <RequestIdLink v-if="detail.request_id" :value="detail.request_id" />
          </div>
        </div>

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div class="rounded-control bg-gray-50 p-4 dark:bg-dark-900">
            <div class="text-xs font-bold uppercase tracking-wider text-gray-400">
              {{ t('admin.audit.columns.actor') }}
            </div>
            <div class="mt-1 break-all text-sm font-medium text-gray-900 dark:text-white">
              {{ detail.actor_email || '—' }}
            </div>
            <div class="mt-0.5 text-xs text-gray-400">{{ detail.actor_role }}</div>
          </div>

          <div class="rounded-control bg-gray-50 p-4 dark:bg-dark-900">
            <div class="text-xs font-bold uppercase tracking-wider text-gray-400">
              {{ t('admin.audit.filters.authMethod') }}
            </div>
            <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">
              {{ authMethodLabel(detail.auth_method) || '—' }}
            </div>
            <div
              v-if="detail.credential_masked"
              class="mt-0.5 break-all font-mono text-xs text-gray-400"
            >
              {{ detail.credential_masked }}
            </div>
          </div>

          <div class="rounded-control bg-gray-50 p-4 dark:bg-dark-900">
            <div class="text-xs font-bold uppercase tracking-wider text-gray-400">
              {{ t('admin.audit.columns.clientIp') }}
            </div>
            <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">
              {{ detail.client_ip || '—' }}
            </div>
          </div>
        </div>

        <section>
          <h4 class="mb-1.5 text-xs font-bold uppercase tracking-wider text-gray-400">
            {{ t('admin.audit.detail.userAgent') }}
          </h4>
          <div
            class="break-all rounded-control bg-gray-50 p-3 font-mono text-xs leading-relaxed text-gray-600 dark:bg-dark-900 dark:text-gray-400"
          >
            {{ detail.user_agent || '—' }}
          </div>
        </section>

        <section v-if="detail.request_body">
          <h4 class="mb-1.5 text-xs font-bold uppercase tracking-wider text-gray-400">
            {{ t('admin.audit.detail.requestBody') }}
          </h4>
          <pre
            class="max-h-72 overflow-auto rounded-control bg-gray-50 p-4 font-mono text-xs leading-relaxed text-gray-600 dark:bg-dark-900 dark:text-gray-400"
            >{{ prettyBody(detail.request_body) }}</pre>
        </section>

        <section v-if="detail.extra && Object.keys(detail.extra).length">
          <h4 class="mb-1.5 text-xs font-bold uppercase tracking-wider text-gray-400">
            {{ t('admin.audit.detail.extra') }}
          </h4>
          <pre
            class="max-h-48 overflow-auto rounded-control bg-gray-50 p-4 font-mono text-xs leading-relaxed text-gray-600 dark:bg-dark-900 dark:text-gray-400"
            >{{ JSON.stringify(detail.extra, null, 2) }}</pre>
        </section>
      </div>
    </BaseDialog>

    <BaseDialog
      :show="showCustomTimeRangeDialog"
      :title="t('admin.ops.timeRange.custom')"
      width="narrow"
      @close="handleCustomTimeRangeCancel"
    >
      <div class="space-y-4 py-2">
        <div>
          <label class="input-label">{{ t('admin.ops.customTimeRange.startTime') }}</label>
          <input v-model="customStartTimeInput" type="datetime-local" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.ops.customTimeRange.endTime') }}</label>
          <input v-model="customEndTimeInput" type="datetime-local" class="input" />
        </div>
      </div>
      <template #footer>
        <button type="button" class="btn btn-secondary" @click="handleCustomTimeRangeCancel">
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="!customStartTimeInput || !customEndTimeInput"
          @click="handleCustomTimeRangeConfirm"
        >
          {{ t('common.confirm') }}
        </button>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="clearConfirmVisible"
      :title="t('admin.audit.clearConfirm.title')"
      :message="t('admin.audit.clearConfirm.message')"
      :confirm-text="t('admin.audit.clearAll')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="onClearConfirmed"
      @cancel="clearConfirmVisible = false"
    />

    <BaseDialog
      :show="clearTotpVisible"
      :title="t('admin.audit.clearConfirm.totpTitle')"
      width="narrow"
      :z-index="60"
      @close="cancelClearTotp"
    >
      <div class="py-2">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.audit.clearConfirm.totpHint') }}
        </p>
        <input
          v-model.trim="clearTotpCode"
          type="text"
          inputmode="numeric"
          maxlength="6"
          autocomplete="one-time-code"
          class="input mt-4 text-center text-lg tracking-[0.5em]"
          placeholder="••••••"
          @keyup.enter="submitClear"
        />
      </div>
      <template #footer>
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="clearing"
          @click="cancelClearTotp"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-danger"
          :disabled="clearing || clearTotpCode.length !== 6"
          @click="submitClear"
        >
          {{ clearing ? t('common.loading') : t('admin.audit.clearAll') }}
        </button>
      </template>
    </BaseDialog>
  </AppLayout>
</template>
<script setup lang="ts">
import RequestIdLink from '@/components/common/RequestIdLink.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { adminAPI, type AuditLog } from '@/api/admin'
import { totpAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import type { Column } from '@/components/common/table/types'
import Pagination from '@/components/common/Pagination.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import type { FilterOption } from '@/components/common/filterPanel'
import FilterTextInput from '@/components/common/FilterTextInput.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'

// AuditLogView.vue lists administrative audit records and opens their request details.
// Text filters and optional time windows become server query parameters, while the
// toolbar keyword remains separate from the filter panel's active-condition count.
//
// Clearing records requires a loaded TOTP-enabled status, a destructive-action
// confirmation, and a six-character code submission. These are separate dialogs so
// cancelling the code entry does not submit deletion. Detail and list requests maintain
// their own loading flags, and failed list requests leave the displayed rows available.
// Custom time inputs stay in a draft until the user confirms them.

// Translations and application notifications supply labels and request feedback for the
// audit page controls.
const { t } = useI18n()
const appStore = useAppStore()
// route carries the token-prefix filter from the MCP management audit link.
const route = useRoute()

// List loading, rows, total, page, and page size describe the current audit table
// request and pagination.
const loading = ref(false)
const logs = ref<AuditLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
// requestVersion prevents an older filter response from replacing a newer route's rows.
let requestVersion = 0

// Text filters remain editable between requests; empty strings are omitted when
// building endpoint query parameters.
const filters = reactive({
  request_id: '', // exact request identifier
  q: '', // toolbar search keyword
  actor_email: '', // actor email condition
  action: '', // audit action condition
  client_ip: '', // client address condition
  method: '', // HTTP method condition
  auth_method: '', // authentication method condition
  success: '', // result success condition
})
// activeFilterCount counts populated panel filters and a selected time range while
// excluding the separate keyword input.
const activeFilterCount = computed(
  () =>
    Object.entries(filters).filter(([key, value]) => key !== 'q' && value.trim() !== '').length +
    (timeRange.value ? 1 : 0),
)

// The active preset and confirmed custom dates remain separate from dialog drafts until
// custom-range confirmation succeeds.
const timeRange = ref('')
const customStartTime = ref('')
const customEndTime = ref('')
const showCustomTimeRangeDialog = ref(false)
const customStartTimeInput = ref('')
const customEndTimeInput = ref('')

// TIME_RANGE_MINUTES maps the supported rolling-window keys to minute durations used to
// calculate query start timestamps.
const TIME_RANGE_MINUTES: Record<string, number> = {
  '30m': 30,
  '1h': 60,
  '6h': 6 * 60,
  '24h': 24 * 60,
  '7d': 7 * 24 * 60,
  '30d': 30 * 24 * 60,
}

// Time-range choices include a custom label derived from the confirmed local start and
// end inputs.
const timeRangeOptions = computed(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: '30m', label: t('admin.ops.timeRange.30m') },
  { value: '1h', label: t('admin.ops.timeRange.1h') },
  { value: '6h', label: t('admin.ops.timeRange.6h') },
  { value: '24h', label: t('admin.ops.timeRange.24h') },
  { value: '7d', label: t('admin.ops.timeRange.7d') },
  { value: '30d', label: t('admin.ops.timeRange.30d') },
  {
    value: 'custom',
    label:
      timeRange.value === 'custom' && customStartTime.value && customEndTime.value
        ? `${t('admin.ops.timeRange.custom')} (${formatCustomTimeRangeLabel(customStartTime.value, customEndTime.value)})`
        : t('admin.ops.timeRange.custom'),
  },
])

// formatCustomTimeRangeLabel formats two local dates for the selected range label.
// Invalid date strings are displayed verbatim so the label retains the input that could
// not be parsed.
function formatCustomTimeRangeLabel(startTime: string, endTime: string): string {
  // fmt formats a local date with month, day, hour, and minute. Invalid input is
  // returned unchanged, retaining the original text in the custom time-range label when
  // parsing fails.
  const fmt = (raw: string) => {
    const d = new Date(raw)
    // Keep an invalid local input visible in the range label so formatting failure
    // does not conceal the supplied text.
    if (Number.isNaN(d.getTime())) return raw
    // pad returns a two-character decimal segment for local date formatting.
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  }
  return `${fmt(startTime)} ~ ${fmt(endTime)}`
}

// toDatetimeLocal formats a Date as a minute-resolution local input value.
function toDatetimeLocal(d: Date): string {
  // pad returns a two-character decimal segment for local date formatting.
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// handleTimeRangeChange applies a preset or opens the custom-range draft dialog.
// Existing custom values are reused; otherwise the inputs start with the previous local
// hour and current time before confirmation.
function handleTimeRangeChange(val: string | number | boolean | null) {
  const value = String(val ?? '')
  if (value === 'custom') {
    const now = new Date()
    // Reuse confirmed dates when reopening the editor; otherwise start with the
    // previous local hour so confirmation has populated draft bounds.
    customStartTimeInput.value =
      customStartTime.value || toDatetimeLocal(new Date(now.getTime() - 60 * 60 * 1000))
    customEndTimeInput.value = customEndTime.value || toDatetimeLocal(now)
    showCustomTimeRangeDialog.value = true
    return
  }
  timeRange.value = value
  search()
}

// handleCustomTimeRangeConfirm applies populated custom inputs and reloads the first
// page. Both inputs must be nonempty; accepted values become the active custom range
// before the dialog closes and searching begins.
function handleCustomTimeRangeConfirm() {
  if (!customStartTimeInput.value || !customEndTimeInput.value) return
  customStartTime.value = customStartTimeInput.value
  customEndTime.value = customEndTimeInput.value
  timeRange.value = 'custom'
  showCustomTimeRangeDialog.value = false
  search()
}

// clearAuditFilter removes one supported text condition and searches page one.
function clearAuditFilter(key: 'request_id' | 'actor_email' | 'action' | 'client_ip') {
  filters[key] = ''
  search()
}

// resetAuditFilters clears the panel conditions and custom dates before searching
// again. The independent keyword remains in filters.q, so resetting the panel continues
// to constrain results by the toolbar text.
function resetAuditFilters() {
  for (const key of Object.keys(filters) as Array<keyof typeof filters>) {
    if (key !== 'q') filters[key] = ''
  }
  timeRange.value = ''
  customStartTime.value = ''
  customEndTime.value = ''
  search()
}

// handleCustomTimeRangeCancel hides the custom-date dialog while retaining the active
// selection.
function handleCustomTimeRangeCancel() {
  showCustomTimeRangeDialog.value = false
}

// Audit table columns and method, authentication, and result selectors use labels from
// the current translation context; authentication rows carry icons and result rows carry
// the same green and red as the status dots in the table.
const columns = computed<Column[]>(() => [
  { key: 'created_at', label: t('admin.audit.columns.time') },
  { key: 'actor', label: t('admin.audit.columns.actor') },
  { key: 'action', label: t('admin.audit.columns.action') },
  { key: 'status_code', label: t('admin.audit.columns.result') },
  { key: 'latency_ms', label: t('admin.audit.detail.latency') },
  { key: 'client_ip', label: t('admin.audit.columns.clientIp') },
  { key: 'actions', label: t('common.actions') },
])

const methodOptions = computed(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: 'POST', label: 'POST' },
  { value: 'PUT', label: 'PUT' },
  { value: 'PATCH', label: 'PATCH' },
  { value: 'DELETE', label: 'DELETE' },
  { value: 'GET', label: 'GET' },
])

const authMethodOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: 'jwt', label: 'JWT', icon: 'login' },
  { value: 'admin_api_key', label: 'Admin API Key', icon: 'key' },
  { value: 'mcp_token', label: t('admin.mcp.authMethod'), icon: 'puzzle' },
])

const resultOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.audit.filters.all') },
  { value: 'true', label: t('admin.audit.filters.resultSuccess'), dot: 'green' },
  { value: 'false', label: t('admin.audit.filters.resultFailure'), dot: 'red' },
])

// authMethodLabel returns a configured authentication label or the raw method. Empty
// and unknown methods retain their incoming text, allowing the detail dialog to display
// values outside the select options.
function authMethodLabel(method: string): string {
  const found = authMethodOptions.value.find((o) => o.value === method)
  return found && found.value ? found.label : method
}

// toRFC3339 converts a valid local input into an ISO timestamp. Empty or invalid inputs
// return undefined so callers can omit those query bounds when building the audit
// request.
function toRFC3339(local: string): string | undefined {
  if (!local) return undefined
  const d = new Date(local)
  // Omit invalid query bounds because the audit endpoint expects timestamps; a failed
  // conversion must not submit an invalid date string.
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

// buildTimeRangeQuery returns custom timestamp bounds or a rolling preset start.
// Unknown or empty presets produce no bounds; custom inputs are converted independently
// and invalid bounds remain undefined in the result.
function buildTimeRangeQuery(): {
  start_time?: string // inclusive query start
  end_time?: string // inclusive query end
} {
  if (timeRange.value === 'custom') {
    return {
      start_time: toRFC3339(customStartTime.value),
      end_time: toRFC3339(customEndTime.value),
    }
  }
  const minutes = TIME_RANGE_MINUTES[timeRange.value]
  if (!minutes) return {}
  // Convert preset minutes to milliseconds before subtracting from the current clock
  // to produce the rolling-window start timestamp.
  return { start_time: new Date(Date.now() - minutes * 60 * 1000).toISOString() }
}

// buildQuery assembles pagination, nonempty filters, and the selected time bounds.
// Empty filter strings become undefined so the endpoint receives optional conditions
// rather than empty matching values for those fields.
function buildQuery() {
  return {
    page: page.value,
    page_size: pageSize.value,
    request_id: filters.request_id || undefined,
    q: filters.q || undefined,
    actor_email: filters.actor_email || undefined,
    action: filters.action || undefined,
    client_ip: filters.client_ip || undefined,
    method: filters.method || undefined,
    auth_method: filters.auth_method || undefined,
    success: filters.success || undefined,
    ...buildTimeRangeQuery(),
  }
}

// fetchLogs reads the currently displayed filters and publishes their audit rows.
// A route change can start a later request before the preceding one finishes;
// only the latest request version may update rows, errors, or loading state.
// This keeps an old token's records from appearing beneath another token's filters.
//
// Existing rows remain visible when the current request fails. Component disposal
// advances the version so a late response cannot publish after leaving the page.
async function fetchLogs() {
  const version = ++requestVersion
  loading.value = true
  try {
    const res = await adminAPI.audit.list(buildQuery())
    if (version !== requestVersion) return
    logs.value = res.items
    total.value = res.total
  } catch (err: any) {
    if (version === requestVersion) appStore.showError(err?.message || t('admin.audit.loadFailed'))
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

// search resets pagination and requests matching audit records from page one.
function search() {
  page.value = 1
  fetchLogs()
}

// onPageChange selects the requested audit page and starts its load.
function onPageChange(p: number) {
  page.value = p
  fetchLogs()
}

// onPageSizeChange applies a new size and reloads audit page one.
function onPageSizeChange(ps: number) {
  pageSize.value = ps
  page.value = 1
  fetchLogs()
}

// The detail dialog owns its visibility, request loading flag, and loaded record
// independently of list pagination.
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<AuditLog | null>(null)

// openDetail opens an empty detail dialog and fetches one audit record. Failure closes
// the dialog and reports the request error; the detail loading flag is released when
// the request settles.
async function openDetail(id: number) {
  detailVisible.value = true
  detailLoading.value = true
  detail.value = null
  try {
    detail.value = await adminAPI.audit.get(id)
  } catch (err: any) {
    appStore.showError(err?.message || t('admin.audit.loadFailed'))
    detailVisible.value = false
  } finally {
    detailLoading.value = false
  }
}

// prettyBody indents valid JSON for inspection and returns other bodies verbatim.
// Parsing failures are suppressed because recorded request bodies may contain plain
// text that still needs to be displayed.
function prettyBody(body: string): string {
  try {
    return JSON.stringify(JSON.parse(body), null, 2)
  } catch {
    // Recorded request bodies may be plain text, so failed JSON parsing keeps the
    // captured body available for detail inspection.
    return body
  }
}

// Clear confirmation, TOTP entry, and submission flags separate setup checks from the
// destructive request and its retry state.
const clearConfirmVisible = ref(false)
const clearTotpVisible = ref(false)
const clearTotpCode = ref('')
const clearing = ref(false)
const checkingTotpStatus = ref(false)

// openClearDialog checks TOTP setup before opening the destructive-action confirmation.
// Concurrent status checks are ignored, and disabled TOTP or lookup failure produces an
// error while leaving the confirmation closed.
async function openClearDialog() {
  if (checkingTotpStatus.value) return
  checkingTotpStatus.value = true
  try {
    const status = await totpAPI.getStatus()
    if (!status.enabled) {
      appStore.showError(t('stepUp.notEnabled'))
      return
    }
  } catch (err: any) {
    appStore.showError(err?.message || t('admin.audit.loadFailed'))
    return
  } finally {
    checkingTotpStatus.value = false
  }
  clearConfirmVisible.value = true
}

// onClearConfirmed replaces destructive confirmation with an empty TOTP code dialog.
function onClearConfirmed() {
  // Confirmation advances to a fresh TOTP entry so a code from an earlier clear
  // attempt is not reused.
  clearConfirmVisible.value = false
  clearTotpCode.value = ''
  clearTotpVisible.value = true
}

// cancelClearTotp closes code entry unless a clear request is running. Keeping the
// dialog open during submission preserves the visible operation state until the request
// succeeds or reports an error.
function cancelClearTotp() {
  if (clearing.value) return
  clearTotpVisible.value = false
}

// submitClear submits a six-character TOTP code to clear all audit records. Success
// closes code entry and reloads page one; failure clears the rejected code and leaves
// the dialog available for retry.
async function submitClear() {
  if (clearTotpCode.value.length !== 6) return
  clearing.value = true
  try {
    const res = await adminAPI.audit.clear(clearTotpCode.value)
    clearTotpVisible.value = false
    appStore.showSuccess(t('admin.audit.clearConfirm.success', { count: res.deleted }))
    search()
  } catch (err: any) {
    appStore.showError(err?.message || t('admin.audit.clearConfirm.failed'))
    // Clear a rejected code while keeping the dialog open so the next attempt asks
    // for a freshly entered authenticator value.
    clearTotpCode.value = ''
  } finally {
    clearing.value = false
  }
}

// formatTime renders a valid timestamp using the browser's local date format. Invalid
// timestamps remain unchanged so the detail display retains the server value when Date
// cannot parse it.
function formatTime(iso: string): string {
  const d = new Date(iso)
  // Retain the recorded timestamp text when browser parsing fails so the detail view
  // still exposes the server-supplied value.
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

// statusText classifies HTTP status codes below 400 as successful audit results.
function statusText(status: number): string {
  // HTTP statuses below 400 use the successful-result label.
  return status < 400
    ? t('admin.audit.filters.resultSuccess')
    : t('admin.audit.filters.resultFailure')
}

// statusBadgeClass returns badge styling for successful, client-error, and server-error
// responses.
function statusBadgeClass(status: number): string {
  const base = 'inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-semibold '
  // Server errors use red indicators; client errors begin at 400 and use amber.
  if (status >= 500) return base + 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  if (status >= 400)
    return base + 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
  return base + 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
}

// statusDotClass returns a matching indicator color for the HTTP status class.
function statusDotClass(status: number): string {
  // Server errors use red indicators; client errors begin at 400 and use amber.
  if (status >= 500) return 'bg-red-500'
  if (status >= 400) return 'bg-amber-500'
  return 'bg-green-500'
}

// Reused route components receive incoming filters before fetching the corresponding
// records. Missing query keys clear earlier link filters instead of retaining another token.
watch(
  () => route.query,
  (query) => {
    for (const key of Object.keys(filters) as Array<keyof typeof filters>) {
      filters[key] = typeof query[key] === 'string' ? query[key] : ''
    }
    timeRange.value = ''
    customStartTime.value = ''
    customEndTime.value = ''
    search()
  },
  { immediate: true },
)

// Responses already in flight cannot publish rows or errors after page disposal.
onBeforeUnmount(() => {
  requestVersion++
})
</script>
