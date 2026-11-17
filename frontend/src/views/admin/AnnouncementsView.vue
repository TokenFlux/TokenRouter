<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
          <div class="flex min-w-0 flex-1 items-center gap-2">
            <div class="min-w-0 flex-1 sm:flex-none sm:w-64">
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.announcements.searchAnnouncements')"
                class="input"
                @input="handleSearch"
              />
            </div>
            <Select
              v-model="filters.status"
              :options="statusFilterOptions"
              class="w-32 shrink-0 sm:w-40"
              @change="handleStatusChange"
            />
          </div>

          <div class="flex shrink-0 items-center justify-end gap-2">
            <button
              @click="loadAnnouncements"
              :disabled="loading"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button @click="openCreateDialog" class="btn btn-primary shrink-0 whitespace-nowrap">
              <Icon name="plus" size="sm" class="mr-1" />
              {{ t('admin.announcements.createAnnouncement') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-announcements-column-order"
          :columns="columns"
          :data="announcements"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-title="{ value, row }">
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="truncate font-medium text-gray-900 dark:text-white">{{ value }}</span>
              </div>
              <div class="mt-1 flex items-center gap-2 text-xs text-gray-500 dark:text-dark-400">
                <span>#{{ row.id }}</span>
                <span class="text-gray-300 dark:text-dark-700">·</span>
                <span>{{ formatDateTime(row.created_at) }}</span>
              </div>
            </div>
          </template>

          <template #cell-status="{ value }">
            <span
              :class="[
                'badge',
                value === 'active'
                  ? 'badge-success'
                  : value === 'draft'
                    ? 'badge-gray'
                    : 'badge-warning',
              ]"
            >
              {{ statusLabel(value) }}
            </span>
          </template>

          <template #cell-notify_mode="{ row }">
            <span :class="['badge', row.notify_mode === 'popup' ? 'badge-warning' : 'badge-gray']">
              {{
                row.notify_mode === 'popup'
                  ? t('admin.announcements.notifyModeLabels.popup')
                  : t('admin.announcements.notifyModeLabels.silent')
              }}
            </span>
          </template>

          <template #cell-targeting="{ row }">
            <span class="text-sm text-gray-600 dark:text-gray-300">
              {{ targetingSummary(row.targeting) }}
            </span>
          </template>

          <template #cell-timeRange="{ row }">
            <div class="text-sm text-gray-600 dark:text-gray-300">
              <div>
                <span class="font-medium">{{ t('admin.announcements.form.startsAt') }}:</span>
                <span class="ml-1">{{
                  row.starts_at
                    ? formatDateTime(row.starts_at)
                    : t('admin.announcements.timeImmediate')
                }}</span>
              </div>
              <div class="mt-0.5">
                <span class="font-medium">{{ t('admin.announcements.form.endsAt') }}:</span>
                <span class="ml-1">{{
                  row.ends_at ? formatDateTime(row.ends_at) : t('admin.announcements.timeNever')
                }}</span>
              </div>
            </div>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{
              formatDateTime(value)
            }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center space-x-1">
              <button
                @click="openPreview(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-primary-50 hover:text-primary-600 dark:hover:bg-primary-500/8 dark:hover:text-primary-500"
                :title="t('admin.announcements.preview')"
              >
                <Icon name="eye" size="sm" />
              </button>
              <button
                @click="openReadStatus(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-primary-50 hover:text-primary-600 dark:hover:bg-primary-500/8 dark:hover:text-primary-500"
                :title="t('admin.announcements.readStatus')"
              >
                <Icon name="chartBar" size="sm" />
              </button>
              <button
                @click="openEditDialog(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-300"
                :title="t('common.edit')"
              >
                <Icon name="edit" size="sm" />
              </button>
              <button
                @click="handleDelete(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                :title="t('common.delete')"
              >
                <Icon name="trash" size="sm" />
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('empty.noData')"
              :description="t('admin.announcements.createFirstAnnouncement')"
              :action-text="t('admin.announcements.createAnnouncement')"
              @action="openCreateDialog"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showEditDialog"
      :title="
        isEditing
          ? t('admin.announcements.editAnnouncement')
          : t('admin.announcements.createAnnouncement')
      "
      width="wide"
      @close="closeEdit"
    >
      <form id="announcement-form" @submit.prevent="handleSave" class="space-y-4">
        <LocalizedFieldsEditor
          v-model="form.localization"
          :source="form.localization.source"
          :fields="[
            { key: 'title', label: t('admin.announcements.form.title'), required: true },
            {
              key: 'content',
              label: t('admin.announcements.form.content'),
              multiline: true,
              rows: 6,
              required: true,
            },
          ]"
        />

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.announcements.form.status') }}</label>
            <Select v-model="form.status" :options="statusOptions" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.announcements.form.notifyMode') }}</label>
            <Select v-model="form.notify_mode" :options="notifyModeOptions" />
            <p class="input-hint">{{ t('admin.announcements.form.notifyModeHint') }}</p>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label" for="announcement-starts-at">{{
              t('admin.announcements.form.startsAt')
            }}</label>
            <DateTimePicker
              id="announcement-starts-at"
              v-model="form.starts_at_str"
              :placeholder="t('admin.announcements.form.startsAtPlaceholder')"
            />
          </div>
          <div>
            <label class="input-label" for="announcement-ends-at">{{
              t('admin.announcements.form.endsAt')
            }}</label>
            <DateTimePicker
              id="announcement-ends-at"
              v-model="form.ends_at_str"
              :placeholder="t('admin.announcements.form.endsAtPlaceholder')"
              :min="form.starts_at_str"
              :presets="endsAtPresets"
              :presets-title="t('admin.announcements.form.duration')"
            />
          </div>
        </div>

        <AnnouncementTargetingEditor v-model="form.targeting" :plans="subscriptionPlans" />
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" @click="closeEdit" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" form="announcement-form" :disabled="saving" class="btn btn-primary">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.announcements.deleteAnnouncement')"
      :message="t('admin.announcements.deleteConfirm')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <AnnouncementReadStatusDialog
      :show="showReadStatusDialog"
      :announcement-id="readStatusAnnouncementId"
      @close="showReadStatusDialog = false"
    />

    <AnnouncementPopup
      :announcement="previewAnnouncement"
      preview
      @close="previewAnnouncement = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import LocalizedFieldsEditor from '@/components/common/LocalizedFieldsEditor.vue'
import { originalContent, type LocalizedUpdate } from '@/i18n/content'
import { getLocale } from '@/i18n'
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { adminAPI } from '@/api/admin'
import { formatDateTime, formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import type { Announcement, AnnouncementTargeting } from '@/api/announcements.types'
import type { SubscriptionPlan } from '@/api/payment.types'
import type { Column } from '@/components/common/table/types'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import DateTimePicker, { type DateTimePreset } from '@/components/common/DateTimePicker.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'

import AnnouncementTargetingEditor from '@/components/admin/announcements/AnnouncementTargetingEditor.vue'
import AnnouncementReadStatusDialog from '@/components/admin/announcements/AnnouncementReadStatusDialog.vue'
import AnnouncementPopup from '@/components/announcements/AnnouncementPopup.vue'

// AnnouncementsView.vue manages announcement listing, localized drafts, targeting, and
// publication schedules. The editor compares existing values with draft values so an
// update can leave a schedule unchanged, clear it, or assign a timestamp. Creation
// omits empty schedule fields, while updates use zero to request clearing an existing
// date. Editor dates have minute precision, so saving can truncate nonzero server
// seconds even when the displayed input was untouched.
//
// List reloads cancel the previous request and check controller identity before
// publishing rows or clearing the loading flag. Search owns a debounce timer that is
// cleared during unmount, together with the active list request. Subscription plans
// supply targeting choices and may fail to load independently of the announcement list.

// Translation and application notifications are shared by the list, editor, and
// confirmation flows throughout this page.
const { t } = useI18n()
const appStore = useAppStore()

// Announcement rows and the loading flag describe the displayed server page and its
// active list request.
const announcements = ref<Announcement[]>([])
const loading = ref(true)

// The status and search drafts supply optional list filters when their text is
// nonempty.
const filters = reactive({
  status: '', // configured lifecycle status
})
const searchQuery = ref('')

// Pagination stores the requested page size alongside totals and page values returned
// by the announcement endpoint.
const pagination = reactive({
  page: 1, // requested page number
  page_size: getPersistedPageSize(), // requested row count
  total: 0, // matching row count
  pages: 0, // matching page count
})

// The server sort starts with newest creation time and is replaced by table sort
// events.
const sortState = reactive({
  sort_by: 'created_at', // server sorting field
  sort_order: 'desc' as 'asc' | 'desc', // server sorting direction
})

// Localized filter, editor, schedule-preset, notification, and column choices are
// recalculated when their reactive dependencies change.
const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.announcements.allStatus') },
  { value: 'draft', label: t('admin.announcements.statusLabels.draft') },
  { value: 'active', label: t('admin.announcements.statusLabels.active') },
  { value: 'archived', label: t('admin.announcements.statusLabels.archived') },
])

const statusOptions = computed(() => [
  { value: 'draft', label: t('admin.announcements.statusLabels.draft') },
  { value: 'active', label: t('admin.announcements.statusLabels.active') },
  { value: 'archived', label: t('admin.announcements.statusLabels.archived') },
])

const endsAtPresets = computed<DateTimePreset[]>(() =>
  [1, 3, 7, 30].map((days) => ({
    key: `${days}d`,
    label: t('admin.announcements.form.durationDays', days),
    resolve: () => {
      const start = parseDateTimeLocalInput(form.starts_at_str)
      // A blank start input anchors duration presets to the current instant so
      // choosing an end still produces a usable schedule.
      const base = start === null ? Math.floor(Date.now() / 1000) : start
      // Convert each whole-day preset into epoch seconds for the local date-time formatter.
      return formatDateTimeLocalInput(base + days * 24 * 60 * 60)
    },
  })),
)

const notifyModeOptions = computed(() => [
  { value: 'silent', label: t('admin.announcements.notifyModeLabels.silent') },
  { value: 'popup', label: t('admin.announcements.notifyModeLabels.popup') },
])

const columns = computed<Column[]>(() => [
  { key: 'title', label: t('admin.announcements.columns.title'), sortable: true },
  { key: 'status', label: t('admin.announcements.columns.status'), sortable: true },
  { key: 'notify_mode', label: t('admin.announcements.columns.notifyMode'), sortable: true },
  { key: 'targeting', label: t('admin.announcements.columns.targeting') },
  { key: 'timeRange', label: t('admin.announcements.columns.timeRange') },
  { key: 'created_at', label: t('admin.announcements.columns.createdAt'), sortable: true },
  { key: 'actions', label: t('admin.announcements.columns.actions') },
])

// statusLabel returns translated lifecycle labels and passes unknown status strings
// through. This permits rows with an unfamiliar server status to remain readable while
// known states use the current locale.
const statusLabel = (status: string) => {
  if (status === 'draft') return t('admin.announcements.statusLabels.draft')
  if (status === 'active') return t('admin.announcements.statusLabels.active')
  if (status === 'archived') return t('admin.announcements.statusLabels.archived')
  return status
}

// targetingSummary labels unrestricted targeting or counts the configured OR groups.
// Missing targeting groups produce the unrestricted label, while a nonempty list uses
// the custom-targeting summary for that group count.
const targetingSummary = (targeting: AnnouncementTargeting) => {
  const anyOf = targeting?.any_of ?? []
  if (!anyOf || anyOf.length === 0) return t('admin.announcements.targetingSummaryAll')
  return t('admin.announcements.targetingSummaryCustom', { groups: anyOf.length })
}

// currentController owns the latest list request; replacement and unmount abort it
// before older work can publish rows.
let currentController: AbortController | null = null

// loadAnnouncements loads the current search, status, page, and sorting selection.
// Starting a load aborts the previous controller; callers may trigger replacements
// while an earlier request is still settling. Both abortion and controller identity are
// checked before rows, errors, or loading state are published. The latest successful
// response also supplies pagination values, while failures keep the displayed rows and
// report an error.
async function loadAnnouncements() {
  // Abort the preceding list request before installing its replacement so its
  // completion cannot publish an obsolete page.
  currentController?.abort()
  const requestController = new AbortController()
  currentController = requestController
  const { signal } = requestController

  try {
    loading.value = true
    const res = await adminAPI.announcements.list(
      pagination.page,
      pagination.page_size,
      {
        status: filters.status || undefined,
        search: searchQuery.value || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order,
      },
      { signal },
    )

    // Accept rows only from the active controller because filter changes can start
    // another request before this response arrives.
    if (signal.aborted || currentController !== requestController) return

    announcements.value = res.items
    pagination.total = res.total
    pagination.pages = res.pages
    pagination.page = res.page
    pagination.page_size = res.page_size
  } catch (error: any) {
    // Discard cancellation and superseded-request failures because they describe a
    // replaced query rather than the currently displayed filters.
    if (
      signal.aborted ||
      currentController !== requestController ||
      error?.name === 'AbortError' ||
      error?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading announcements:', error)
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToLoad'))
  } finally {
    // Release loading only for the controller that still owns the list, leaving a
    // replacement request visibly pending.
    if (currentController === requestController) {
      loading.value = false
      currentController = null
    }
  }
}

// handlePageChange selects the requested page and starts an announcement reload.
function handlePageChange(page: number) {
  pagination.page = page
  loadAnnouncements()
}

// handlePageSizeChange changes the page size and reloads the first page. Resetting the
// page prevents the prior offset from being reused with a different number of
// announcement rows.
function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadAnnouncements()
}

// handleStatusChange reloads announcements from the first page after filtering.
function handleStatusChange() {
  pagination.page = 1
  loadAnnouncements()
}

// handleSort installs the requested server sort and reloads page one. The sort key and
// direction travel together, so the table requests a fresh page in the selected order.
function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadAnnouncements()
}

// searchDebounceTimer holds the pending search reload and is cleared on replacement or
// component unmount.
let searchDebounceTimer: number | null = null
// handleSearch replaces the pending search timer with a delayed first-page reload. The
// component owns the timer and clears it during unmount, preventing a queued search
// from starting afterward.
function handleSearch() {
  if (searchDebounceTimer) window.clearTimeout(searchDebounceTimer)
  searchDebounceTimer = window.setTimeout(() => {
    pagination.page = 1
    loadAnnouncements()
  }, 300)
}

// Editor visibility, saving status, and selected announcement distinguish creation from
// updates and drive the dialog controls.
const showEditDialog = ref(false)
const saving = ref(false)
const editingAnnouncement = ref<Announcement | null>(null)

// isEditing derives the dialog mode from the selected announcement instead of
// maintaining a second editable flag.
const isEditing = computed(() => !!editingAnnouncement.value)

// The editor draft keeps localized text, lifecycle settings, local schedule strings,
// and targeting values until save.
const form = reactive({
  localization: originalContent({ title: '', content: '' }, getLocale()) as LocalizedUpdate<{
    title: string // source announcement title
    content: string // source announcement body
  }>, // localized announcement text
  status: 'draft', // configured lifecycle status
  notify_mode: 'silent', // announcement delivery mode
  // starts_at_str and ends_at_str keep local editor values; creation omits empty
  // dates, while updates distinguish unchanged dates from clearing an existing
  // schedule.
  starts_at_str: '', // local start input
  ends_at_str: '', // local end input
  targeting: { any_of: [] } as AnnouncementTargeting, // recipient matching conditions
})

// subscriptionPlans supplies the plan choices used by the targeting editor after its
// optional lookup finishes.
const subscriptionPlans = ref<SubscriptionPlan[]>([])

// loadSubscriptionPlans populates targeting choices from the available subscription
// plans. Failures are logged while the current choices remain available, allowing
// announcement listing to continue after the optional lookup fails.
async function loadSubscriptionPlans() {
  try {
    const response = await adminAPI.payment.getPlans()
    subscriptionPlans.value = response.data || []
  } catch (error: any) {
    // Keep the existing plan choices after lookup failure so the independently loaded
    // announcement list and editor remain available.
    console.error('Error loading plans:', error)
  }
}

// resetForm restores an empty localized draft in the current locale with
// unrestricted targeting. Status starts as draft, notification is silent, and both
// schedule inputs are cleared for creation.
function resetForm() {
  form.localization = originalContent({ title: '', content: '' }, getLocale())
  form.status = 'draft'
  form.notify_mode = 'silent'
  form.starts_at_str = ''
  form.ends_at_str = ''
  form.targeting = { any_of: [] }
}

// fillFormFromAnnouncement copies localized content and translates server dates into
// editor values. Localization is cloned through JSON, while targeting uses the
// announcement value directly or an unrestricted default. Missing notification mode
// becomes silent, and missing dates become empty inputs. RFC3339 timestamps pass
// through epoch seconds into minute-resolution local strings. The formatter discards
// seconds, so buildUpdatePayload can detect a timestamp difference after opening and
// saving an untouched date.
function fillFormFromAnnouncement(a: Announcement) {
  // Clone the localized text before editing so source and translation changes cannot
  // mutate the loaded announcement through shared objects.
  form.localization = JSON.parse(
    JSON.stringify(a.localization || originalContent({ title: a.title, content: a.content })),
  )
  form.status = a.status
  form.notify_mode = a.notify_mode || 'silent'

  // Server dates are timestamp strings; convert milliseconds to epoch seconds before
  // formatting minute-resolution inputs; nonzero server seconds are discarded here.
  form.starts_at_str = a.starts_at
    ? formatDateTimeLocalInput(Math.floor(new Date(a.starts_at).getTime() / 1000))
    : ''
  form.ends_at_str = a.ends_at
    ? formatDateTimeLocalInput(Math.floor(new Date(a.ends_at).getTime() / 1000))
    : ''

  // The targeting value keeps the loaded object reference; the child editor emits
  // replacements, while an absent server value starts with unrestricted targeting.
  form.targeting = a.targeting ?? { any_of: [] }
}

// openCreateDialog clears the selected announcement and initializes a fresh draft
// before showing the editor. Clearing the selection makes the save handler use
// creation and prevents values from an earlier edit from being reused.
function openCreateDialog() {
  editingAnnouncement.value = null
  resetForm()
  showEditDialog.value = true
}

// openEditDialog selects the loaded announcement and fills its draft before showing
// the editor. Saving then compares against that selected record so unchanged fields
// can remain absent from the update payload.
function openEditDialog(row: Announcement) {
  editingAnnouncement.value = row
  fillFormFromAnnouncement(row)
  showEditDialog.value = true
}

// closeEdit hides the announcement editor and releases its selected row.
function closeEdit() {
  showEditDialog.value = false
  editingAnnouncement.value = null
}

// buildCreatePayload builds an announcement request from the current editor draft.
// Empty schedule inputs become omitted properties, and source title and content
// accompany the localized content object expected by creation.
function buildCreatePayload() {
  const startsAt = parseDateTimeLocalInput(form.starts_at_str)
  const endsAt = parseDateTimeLocalInput(form.ends_at_str)

  return {
    localization: form.localization,
    title: form.localization.source.title,
    content: form.localization.source.content,
    status: form.status as any,
    notify_mode: form.notify_mode as any,
    targeting: form.targeting,
    starts_at: startsAt ?? undefined,
    ends_at: endsAt ?? undefined,
  }
}

// buildUpdatePayload compares the draft with its loaded announcement and returns
// changed fields. Schedule comparison uses exact epoch seconds, while the editor holds
// minute-resolution input text. An equal timestamp is omitted, an emptied date becomes
// zero, and a populated date becomes its parsed timestamp. An untouched input can
// therefore replace nonzero server seconds with zero. Localization and targeting use
// JSON comparisons to decide whether their complete values belong in the patch.
function buildUpdatePayload(original: Announcement) {
  const payload: any = {}

  if (JSON.stringify(form.localization) !== JSON.stringify(original.localization))
    payload.localization = form.localization
  if (form.status !== original.status) payload.status = form.status
  if (form.notify_mode !== (original.notify_mode || 'silent'))
    payload.notify_mode = form.notify_mode

  // Compare exact server seconds with the minute-resolution draft; discarded seconds
  // can produce an update even when the visible date input was untouched.
  const originalStarts = original.starts_at
    ? Math.floor(new Date(original.starts_at).getTime() / 1000)
    : null
  const originalEnds = original.ends_at
    ? Math.floor(new Date(original.ends_at).getTime() / 1000)
    : null

  const newStarts = parseDateTimeLocalInput(form.starts_at_str)
  const newEnds = parseDateTimeLocalInput(form.ends_at_str)

  // An omitted schedule field leaves server state untouched, while zero requests
  // clearing; retain that distinction for each changed date.
  if (newStarts !== originalStarts) {
    payload.starts_at = newStarts === null ? 0 : newStarts
  }
  if (newEnds !== originalEnds) {
    payload.ends_at = newEnds === null ? 0 : newEnds
  }

  if (JSON.stringify(form.targeting ?? {}) !== JSON.stringify(original.targeting ?? {})) {
    payload.targeting = form.targeting
  }

  return payload
}

// handleSave validates targeting group limits and creates or updates the announcement.
// Successful persistence closes the editor before refreshing the list; errors keep the
// draft available and show the applicable operation message.
async function handleSave() {
  const anyOf = form.targeting?.any_of ?? []
  // Reject more than fifty OR groups before saving to stay within the announcement
  // targeting limit.
  if (anyOf.length > 50) {
    appStore.showError(t('admin.announcements.failedToCreate'))
    return
  }
  for (const g of anyOf) {
    const allOf = g?.all_of ?? []
    // Each OR group may contain at most fifty AND conditions in a valid targeting request.
    if (allOf.length > 50) {
      appStore.showError(t('admin.announcements.failedToCreate'))
      return
    }
  }

  saving.value = true
  try {
    if (!editingAnnouncement.value) {
      const payload = buildCreatePayload()
      await adminAPI.announcements.create(payload)
      appStore.showSuccess(t('common.success'))
      showEditDialog.value = false
      await loadAnnouncements()
      return
    }

    const original = editingAnnouncement.value
    const payload = buildUpdatePayload(original)
    await adminAPI.announcements.update(original.id, payload)
    appStore.showSuccess(t('common.success'))
    showEditDialog.value = false
    editingAnnouncement.value = null
    await loadAnnouncements()
  } catch (error: any) {
    console.error('Failed to save announcement:', error)
    appStore.showError(
      error.response?.data?.detail ||
        (editingAnnouncement.value
          ? t('admin.announcements.failedToUpdate')
          : t('admin.announcements.failedToCreate')),
    )
  } finally {
    saving.value = false
  }
}

// Deletion state retains the selected announcement until a successful request closes
// and clears its confirmation.
const showDeleteDialog = ref(false)
const deletingAnnouncement = ref<Announcement | null>(null)

// handleDelete selects an announcement and opens its deletion confirmation dialog.
function handleDelete(row: Announcement) {
  deletingAnnouncement.value = row
  showDeleteDialog.value = true
}

// confirmDelete deletes the selected announcement and reloads the list after success.
// With no selected row it returns immediately, and a failed request keeps the
// confirmation target available for another attempt.
async function confirmDelete() {
  if (!deletingAnnouncement.value) return

  try {
    await adminAPI.announcements.delete(deletingAnnouncement.value.id)
    appStore.showSuccess(t('common.success'))
    showDeleteDialog.value = false
    deletingAnnouncement.value = null
    await loadAnnouncements()
  } catch (error: any) {
    console.error('Failed to delete announcement:', error)
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToDelete'))
  }
}

// Readership visibility and announcement identity control the detail dialog, while
// previewAnnouncement independently selects the popup preview.
const showReadStatusDialog = ref(false)
const readStatusAnnouncementId = ref<number | null>(null)
const previewAnnouncement = ref<Announcement | null>(null)

// openPreview selects the announcement displayed by the popup preview component.
function openPreview(row: Announcement) {
  previewAnnouncement.value = row
}

// openReadStatus opens the readership dialog for the selected announcement identifier.
function openReadStatus(row: Announcement) {
  readStatusAnnouncementId.value = row.id
  showReadStatusDialog.value = true
}

// Load plan choices before the initial announcement page so targeting controls have
// their available options after initialization completes.
onMounted(async () => {
  await loadSubscriptionPlans()
  await loadAnnouncements()
})

// Cancel delayed searching and abort the active list request so page teardown cannot
// publish a pending list response.
onUnmounted(() => {
  if (searchDebounceTimer) window.clearTimeout(searchDebounceTimer)
  currentController?.abort()
})
</script>
