<template>
  <div class="flex h-full min-h-0 flex-col">
    <div class="flex min-h-0 flex-1 flex-col overflow-hidden" :class="flat ? '' : 'card'">
      <IpGeoBatchToolbar
        v-if="showIpGeoToolbar"
        :ips="rows.map((r) => r.client_ip)"
        @failed="emit('ipGeoBatchFailed')"
      />

      <DataTable
        class="min-h-0 flex-1 overflow-auto"
        column-order-storage-key="admin-ops-errors-column-order"
        :columns="columns"
        :data="rows"
        :loading="loading"
        clickable-rows
        server-side-sort
        default-sort-key="created_at"
        default-sort-order="desc"
        @sort="onSort"
        @rowClick="(row) => emit('openErrorDetail', row.id)"
      >
        <template #cell-created_at="{ row }">
          <span class="text-sm text-gray-600 dark:text-gray-400">{{
            formatDateTime(row.created_at)
          }}</span>
        </template>

        <template #cell-request_id="{ row }">
          <RequestIdLink :value="row.request_id || row.client_request_id" />
        </template>

        <template #cell-type="{ row }">
          <span
            class="inline-flex items-center rounded-compact px-2 py-0.5 text-xs font-medium"
            :class="getTypeBadge(row).className"
          >
            {{ getTypeBadge(row).label }}
          </span>
        </template>

        <template #cell-endpoint="{ row }">
          <div class="max-w-[320px] space-y-1 text-xs">
            <div class="break-all text-gray-700 dark:text-gray-300">
              <span class="font-medium text-gray-500 dark:text-gray-400"
                >{{ t('usage.inbound') }}:</span
              >
              <span class="ml-1">{{ row.inbound_endpoint?.trim() || '-' }}</span>
            </div>
            <div v-if="row.upstream_endpoint" class="break-all text-gray-700 dark:text-gray-300">
              <span class="font-medium text-gray-500 dark:text-gray-400"
                >{{ t('usage.upstream') }}:</span
              >
              <span class="ml-1">{{ row.upstream_endpoint?.trim() || '-' }}</span>
            </div>
          </div>
        </template>

        <template #cell-platform="{ row }">
          <span class="text-sm text-gray-900 dark:text-white">{{ row.platform || '-' }}</span>
        </template>

        <template #cell-model="{ row }">
          <div v-if="hasModelMapping(row)" class="space-y-0.5 text-xs">
            <div class="break-all font-medium text-gray-900 dark:text-white">
              {{ row.requested_model }}
            </div>
            <div class="break-all text-gray-500 dark:text-gray-400">
              <span class="mr-0.5">↳</span>{{ row.upstream_model }}
            </div>
          </div>
          <span
            v-else-if="displayModel(row)"
            class="text-sm font-medium text-gray-900 dark:text-white"
            >{{ displayModel(row) }}</span
          >
          <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
        </template>

        <template #cell-group="{ row }">
          <span
            v-if="row.group_id"
            class="inline-flex items-center rounded-compact px-2 py-0.5 text-xs font-medium bg-indigo-100 text-indigo-800 dark:bg-indigo-900 dark:text-indigo-200"
            :title="t('admin.ops.errorLog.id') + ' ' + row.group_id"
          >
            {{ row.group_name || '#' + row.group_id }}
          </span>
          <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
        </template>

        <template #cell-user="{ row }">
          <div v-if="row.user_id" class="text-sm">
            <button
              v-if="userClickable && row.user_email"
              class="font-medium text-primary-600 underline decoration-dashed underline-offset-2 transition-colors hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
              :title="t('admin.usage.clickToViewBalance')"
              @click.stop="emit('userClick', row.user_id, row.user_email)"
            >
              {{ row.user_email }}
            </button>
            <span v-else class="font-medium text-gray-900 dark:text-white">{{
              row.user_email || '-'
            }}</span>
            <span class="ml-1 text-gray-500 dark:text-gray-400">#{{ row.user_id }}</span>
          </div>
          <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
        </template>

        <template #cell-api_key="{ row }">
          <div v-if="row.api_key_id || row.api_key_name" class="text-sm">
            <span class="text-gray-900 dark:text-white">{{
              row.api_key_name || '#' + row.api_key_id
            }}</span>
            <span
              v-if="row.api_key_deleted"
              class="ml-1 inline-flex items-center rounded-compact px-1 py-px text-xs font-medium leading-tight bg-rose-100 text-rose-600 ring-1 ring-inset ring-rose-200 dark:bg-rose-500/20 dark:text-rose-400 dark:ring-rose-500/30"
              >{{ t('admin.ops.errorLog.keyDeletedBadge') }}</span
            >
          </div>
          <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
        </template>

        <template #cell-provider="{ row }">
          <span
            v-if="row.provider_id"
            class="text-sm text-gray-900 dark:text-white"
            :title="t('admin.ops.errorLog.providerId') + ' ' + row.provider_id"
            >{{ row.provider_name || '#' + row.provider_id }}</span
          >
          <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
        </template>

        <template #cell-category="{ row }">
          <span class="text-sm text-gray-900 dark:text-white">
            {{ t('usage.errors.categories.' + mapErrorCategory(row.phase, row.type)) }}
          </span>
        </template>

        <template #cell-status="{ row }">
          <div class="flex items-center gap-1.5">
            <span
              class="inline-flex items-center rounded-compact px-2 py-0.5 text-xs font-medium"
              :class="getStatusClass(row.status_code)"
            >
              {{ row.status_code }}
            </span>
            <span
              v-if="row.severity"
              :class="[
                'rounded-compact px-1.5 py-0.5 text-xs font-medium',
                getSeverityClass(row.severity),
              ]"
              >{{ row.severity }}</span
            >
            <span
              v-if="row.request_type != null && row.request_type > 0"
              class="inline-flex items-center rounded-compact px-2 py-0.5 text-xs font-medium bg-gray-100 text-gray-800 dark:bg-dark-700 dark:text-gray-200"
              >{{ formatRequestType(row.request_type) }}</span
            >
          </div>
        </template>

        <template #cell-message="{ row }">
          <span
            v-if="row.message"
            class="block max-w-[280px] truncate text-sm text-gray-600 dark:text-gray-400"
            :title="row.message"
            >{{ formatSmartMessage(row.message) || '-' }}</span
          >
          <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
        </template>

        <template #cell-user_agent="{ row }">
          <span
            v-if="row.user_agent"
            class="block max-w-[320px] truncate text-sm text-gray-600 dark:text-gray-400"
            :title="row.user_agent"
            >{{ row.user_agent }}</span
          >
          <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
        </template>

        <template #cell-client_ip="{ row }">
          <div @click.stop>
            <div v-if="row.client_ip">
              <span class="text-sm font-mono text-gray-600 dark:text-gray-400">{{
                row.client_ip
              }}</span>
              <IpGeoCell :ip="row.client_ip" />
            </div>
            <span v-else class="text-sm text-gray-400 dark:text-gray-500">-</span>
          </div>
        </template>

        <template #cell-actions="{ row }">
          <button
            type="button"
            class="rounded-compact p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
            :title="t('admin.ops.errorLog.details')"
            @click.stop="emit('openErrorDetail', row.id)"
          >
            <Icon name="document" size="sm" class="h-4 w-4" />
          </button>
        </template>

        <template #empty><EmptyState :message="t('admin.ops.errorLog.noErrors')" /></template>
      </DataTable>
      <!-- Pagination stays inside the card outside the scrolling table body. -->
      <Pagination
        v-if="total > 0"
        :total="total"
        :page="page"
        :page-size="pageSize"
        @update:page="emit('update:page', $event)"
        @update:pageSize="emit('update:pageSize', $event)"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import RequestIdLink from '@/components/common/RequestIdLink.vue'
import Icon from '@/components/icons/Icon.vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from '@/components/common/table/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Pagination from '@/components/common/Pagination.vue'
import IpGeoCell from '@/components/common/IpGeoCell.vue'
import IpGeoBatchToolbar from '@/components/common/IpGeoBatchToolbar.vue'
import type { OpsErrorLog } from '@/api/admin/ops'
import type { Column } from '@/components/common/table/types'
import { getSeverityClass, formatDateTime } from './opsErrorFormatting'
import { mapErrorCategory } from '@/utils/errorCategory'
import { mapErrorSortKey, statusCodeBadgeClass } from '@/utils/errorBadges'

// OpsErrorLogTable.vue renders error rows with request identities, routing
// context, status, and diagnostic messages. API keys and providers have separate
// columns so a recorded caller remains distinct from the upstream execution
// identity. The owner supplies pagination and receives mapped sort and
// navigation events.

// Translation supplies table labels while API rows and pagination remain
// controlled by the component owner.
const { t } = useI18n()

// allColumns orders caller identities before request shape, outcome, timestamps,
// correlation identifiers, and row actions for investigation.
const allColumns = computed<Column[]>(() => [
  { key: 'user', label: t('admin.ops.errorLog.user') },
  { key: 'api_key', label: t('admin.ops.errorLog.apiKey') },
  { key: 'provider', label: t('admin.ops.errorLog.provider') },
  { key: 'platform', label: t('admin.ops.errorLog.platform') },
  { key: 'model', label: t('admin.ops.errorLog.model'), sortable: true },
  { key: 'endpoint', label: t('admin.ops.errorLog.endpoint') },
  { key: 'group', label: t('admin.ops.errorLog.group') },
  { key: 'type', label: t('admin.ops.errorLog.type') },
  { key: 'category', label: t('usage.errors.category') },
  { key: 'status', label: t('admin.ops.errorLog.status'), sortable: true },
  { key: 'message', label: t('admin.ops.errorLog.message') },
  { key: 'created_at', label: t('admin.ops.errorLog.time'), sortable: true },
  { key: 'request_id', label: t('requests.id') },
  { key: 'user_agent', label: t('usage.userAgent') },
  { key: 'client_ip', label: t('admin.ops.errorLog.ip') },
  { key: 'actions', label: t('admin.ops.errorLog.action') },
])

// columns filters the standard column list when visibleColumnKeys is supplied,
// preserving standard order and allowing an empty selection.
const columns = computed<Column[]>(() =>
  props.visibleColumnKeys
    ? allColumns.value.filter((c) => props.visibleColumnKeys!.includes(c.key))
    : allColumns.value,
)

// isUpstreamRow requires both the upstream phase and provider ownership for
// classification.
function isUpstreamRow(log: OpsErrorLog): boolean {
  const phase = String(log.phase || '').toLowerCase()
  const owner = String(log.error_owner || '').toLowerCase()
  return phase === 'upstream' && owner === 'provider'
}

// hasModelMapping reports whether both recorded model names are nonempty and
// differ after trimming.
function hasModelMapping(log: OpsErrorLog): boolean {
  const requested = String(log.requested_model || '').trim()
  const upstream = String(log.upstream_model || '').trim()
  return !!requested && !!upstream && requested !== upstream
}

// displayModel prefers the recorded upstream model, then the requested model,
// then the legacy model. Each candidate is trimmed so empty snapshots can yield
// to the next available identity.
function displayModel(log: OpsErrorLog): string {
  const upstream = String(log.upstream_model || '').trim()
  if (upstream) return upstream
  const requested = String(log.requested_model || '').trim()
  if (requested) return requested
  return String(log.model || '').trim()
}

// formatRequestType translates the supported synchronous, streaming, WebSocket,
// and cyber request codes. Missing or unknown codes return an empty label
// because this table has no matching request-type classification.
function formatRequestType(type: number | null | undefined): string {
  switch (type) {
    case 1:
      return t('admin.ops.errorLog.requestTypeSync')
    case 2:
      return t('admin.ops.errorLog.requestTypeStream')
    case 3:
      return t('admin.ops.errorLog.requestTypeWs')
    case 4:
      return t('admin.ops.errorLog.requestTypeCyber')
    default:
      return ''
  }
}

// getTypeBadge maps phase and owner combinations to a translated error-origin
// badge. Provider authentication uses its phase directly; unmatched combinations
// display the phase, then owner, then an unknown label.
function getTypeBadge(log: OpsErrorLog): {
  label: string // translated display label text
  className: string // badge CSS class list
} {
  const phase = String(log.phase || '').toLowerCase()
  const owner = String(log.error_owner || '').toLowerCase()

  if (isUpstreamRow(log)) {
    return {
      label: t('admin.ops.errorLog.typeUpstream'),
      className: 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200',
    }
  }
  if (phase === 'request' && owner === 'client') {
    return {
      label: t('admin.ops.errorLog.typeRequest'),
      className: 'bg-amber-100 text-amber-800 dark:bg-amber-900 dark:text-amber-200',
    }
  }
  if (phase === 'auth' && owner === 'client') {
    return {
      label: t('admin.ops.errorLog.typeAuth'),
      className: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200',
    }
  }
  if (phase === 'provider_auth') {
    return {
      label: t('admin.ops.errorLog.typeProviderAuth'),
      className: 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200',
    }
  }
  if (phase === 'routing' && owner === 'platform') {
    return {
      label: t('admin.ops.errorLog.typeRouting'),
      className: 'bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200',
    }
  }
  if (phase === 'internal' && owner === 'platform') {
    return {
      label: t('admin.ops.errorLog.typeInternal'),
      className: 'bg-gray-100 text-gray-800 dark:bg-dark-700 dark:text-gray-200',
    }
  }

  const fallback = phase || owner || t('common.unknown')
  return {
    label: fallback,
    className: 'bg-gray-100 text-gray-800 dark:bg-dark-700 dark:text-gray-200',
  }
}

// Props supplies error rows and owner-controlled table display and pagination
// settings.
interface Props {
  rows: OpsErrorLog[] // current error page records
  total: number // total matching record count
  loading: boolean // parent request in progress
  page: number // one-based current page number
  pageSize: number // records per requested page
  // userClickable enables caller navigation when the table owner handles
  // userClick; omitted values leave user labels noninteractive.
  userClickable?: boolean
  // visibleColumnKeys filters columns in their standard order when supplied;
  // omission shows all columns and an empty array shows none.
  visibleColumnKeys?: string[]
  // flat suppresses the table card appearance when an enclosing component
  // already provides the surrounding surface and spacing.
  flat?: boolean
  // showIpGeoToolbar defaults to true; owners with a separate batch geolocation
  // action disable this internal toolbar to avoid duplicate controls.
  showIpGeoToolbar?: boolean
}

// Emits forwards row navigation, sorting, pagination, and geolocation failures
// to the owner.
interface Emits {
  // openErrorDetail forwards a persisted error identifier for single-record
  // inspection.
  (e: 'openErrorDetail', id: number): void
  // update:page selects the one-based result page for the owning list
  // controller.
  (e: 'update:page', value: number): void
  // update:pageSize requests a different row limit from the owning list
  // controller.
  (e: 'update:pageSize', value: number): void
  // ipGeoBatchFailed reports unsuccessful batch geolocation to the table owner.
  (e: 'ipGeoBatchFailed'): void
  // sort publishes the mapped backend field and direction for the owning query.
  (e: 'sort', sortBy: string, sortOrder: 'asc' | 'desc'): void
  // userClick identifies the recorded user and optional email for owner-provided
  // navigation.
  (e: 'userClick', userId: number, email?: string): void
}

// Component bindings retain parent ownership of rows and expose the optional
// batch geolocation toolbar by default.
const props = withDefaults(defineProps<Props>(), {
  showIpGeoToolbar: true,
})
const emit = defineEmits<Emits>()
const showIpGeoToolbar = computed(() => props.showIpGeoToolbar)

// onSort maps a visible column key to the API sort field before emitting.
function onSort(key: string, order: 'asc' | 'desc') {
  emit('sort', mapErrorSortKey(key), order)
}

// getStatusClass shares status badge styling with other request views through
// the central status-code helper.
const getStatusClass = statusCodeBadgeClass

// formatSmartMessage extracts readable JSON message fields or summarizes a raw
// diagnostic string. Malformed JSON continues through text handling, and
// recognizable transport failures receive localized labels.
//
// JSON objects lacking message fields are serialized and bounded to 150
// characters. Other raw text is bounded to 200 characters after the common-error
// checks, while extracted message fields return directly. These display
// transformations leave the supplied row payload available to the detail dialog
// for inspection.
function formatSmartMessage(msg: string): string {
  if (!msg) return ''

  if (msg.startsWith('{') || msg.startsWith('[')) {
    try {
      const obj = JSON.parse(msg)
      if (obj?.error?.message) return String(obj.error.message)
      if (obj?.message) return String(obj.message)
      if (obj?.detail) return String(obj.detail)
      // Bound serialized payload previews to keep error rows compact.
      if (typeof obj === 'object') return JSON.stringify(obj).substring(0, 150)
    } catch {
      // Malformed JSON remains available as raw text so common transport-message
      // handling and bounded display can still provide a description.
    }
  }

  if (msg.includes('context deadline exceeded'))
    return t('admin.ops.errorLog.commonErrors.contextDeadlineExceeded')
  if (msg.includes('connection refused'))
    return t('admin.ops.errorLog.commonErrors.connectionRefused')
  if (msg.toLowerCase().includes('rate limit'))
    return t('admin.ops.errorLog.commonErrors.rateLimit')

  // Mark truncated raw messages while keeping the full payload available in
  // details.
  return msg.length > 200 ? msg.substring(0, 200) + '...' : msg
}
</script>
