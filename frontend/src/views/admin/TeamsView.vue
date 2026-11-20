<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="input-icon-wrap w-full sm:w-72">
            <Icon name="search" size="md" class="input-icon text-gray-400" />
            <input
              v-model="searchQuery"
              class="input input-has-icon"
              type="text"
              :placeholder="t('team.searchPlaceholder')"
            />
          </div>

          <div class="flex w-full items-center justify-end gap-2 sm:w-auto">
            <button
              class="btn btn-secondary shrink-0 btn-icon"
              :disabled="loading"
              :title="t('common.refresh')"
              @click="loadTeams"
            >
              <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />
            </button>
            <button class="btn btn-primary whitespace-nowrap" @click="showCreate = true">
              <Icon name="plus" size="sm" />
              {{ t('team.create') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-teams-column-order"
          :columns="columns"
          :data="paginatedTeams"
          :loading="loading"
          row-key="id"
          :actions-count="3"
          default-sort-key="created_at"
          default-sort-order="desc"
          sort-storage-key="admin-teams-table-sort"
        >
          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-gray-500 dark:text-dark-400">#{{ value }}</span>
          </template>

          <template #cell-name="{ value }">
            <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
          </template>

          <template #cell-owner_email="{ value }">
            <div class="flex items-center gap-2">
              <div
                class="flex h-8 w-8 flex-none items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30"
              >
                <span class="text-sm font-medium text-primary-700 dark:text-primary-300">
                  {{
                    String(value || '?')
                      .charAt(0)
                      .toUpperCase()
                  }}
                </span>
              </div>
              <span class="max-w-64 truncate text-gray-700 dark:text-gray-300" :title="value">
                {{ value }}
              </span>
            </div>
          </template>

          <template #cell-member_count="{ row }">
            <span class="text-gray-700 dark:text-gray-300">
              {{ row.member_count }} / {{ row.member_limit }}
            </span>
          </template>

          <template #cell-status="{ value }">
            <div class="flex items-center gap-1.5">
              <span
                :class="[
                  'inline-block h-2 w-2 rounded-full',
                  value === 'active' ? 'bg-green-500' : 'bg-red-500',
                ]"
              ></span>

              <!-- Align the mobile-card label with the status text baseline inside the combined indicator and text row. -->
              <span class="self-baseline text-gray-700 dark:text-gray-300">
                {{ value === 'active' ? t('team.statusActive') : t('team.statusSuspended') }}
              </span>
            </div>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-gray-500 dark:text-dark-400">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                class="row-action hover:text-primary-600 dark:hover:text-primary-400"
                @click="openEdit(row)"
              >
                <Icon name="edit" size="sm" />
                <span>{{ t('common.edit') }}</span>
              </button>
              <button
                :class="[
                  'row-action',
                  row.status === 'active'
                    ? 'hover:bg-orange-50 hover:text-orange-600 dark:hover:bg-orange-900/20 dark:hover:text-orange-400'
                    : 'hover:bg-green-50 hover:text-green-600 dark:hover:bg-green-900/20 dark:hover:text-green-400',
                ]"
                :disabled="statusUpdatingID === row.id"
                @click="toggleStatus(row)"
              >
                <Icon
                  :name="row.status === 'active' ? 'ban' : 'checkCircle'"
                  size="sm"
                  :class="{ 'animate-spin': statusUpdatingID === row.id }"
                />
                <span>{{ row.status === 'active' ? t('team.pause') : t('team.resume') }}</span>
              </button>
              <button
                class="team-action-menu-trigger row-action hover:text-gray-900 dark:hover:text-white"
                :class="{
                  'bg-gray-100 text-gray-900 dark:bg-dark-700 dark:text-white':
                    menuTeam?.id === row.id,
                }"
                @click="openActionMenu(row, $event)"
              >
                <Icon name="more" size="sm" />
                <span>{{ t('common.more') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('team.noTeams')"
              :description="t('team.noTeamsDescription')"
              :action-text="t('team.create')"
              @action="showCreate = true"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="filteredTeams.length > 0"
          :page="page"
          :total="filteredTeams.length"
          :page-size="pageSize"
          @update:page="page = $event"
          @update:page-size="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showCreate"
      :title="t('team.createTitle')"
      width="narrow"
      @close="showCreate = false"
    >
      <form id="admin-team-create" class="space-y-4" @submit.prevent="createTeam">
        <div>
          <label class="input-label">{{ t('team.name') }}</label>
          <input v-model.trim="createForm.name" class="input" required maxlength="100" />
        </div>
        <div>
          <label class="input-label">{{ t('team.ownerUserID') }}</label>
          <input
            v-model.number="createForm.owner_user_id"
            class="input"
            type="number"
            min="1"
            required
          />
        </div>
        <div>
          <label class="input-label">{{ t('team.memberCapacity') }}</label>
          <input
            v-model.number="createForm.member_limit"
            class="input"
            type="number"
            min="0"
            required
          />
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="showCreate = false">
            {{ t('common.cancel') }}
          </button>
          <button form="admin-team-create" class="btn btn-primary" :disabled="saving">
            {{ t('common.create') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="Boolean(editingTeam)"
      :title="t('team.editTitle')"
      width="narrow"
      @close="closeEdit"
    >
      <form id="admin-team-edit" class="space-y-4" @submit.prevent="saveEdit">
        <div>
          <label class="input-label">{{ t('team.name') }}</label>
          <input v-model.trim="editForm.name" class="input" required maxlength="100" />
        </div>
        <div>
          <label class="input-label">{{ t('team.memberCapacity') }}</label>
          <input
            v-model.number="editForm.member_limit"
            class="input"
            type="number"
            min="0"
            required
          />
          <p class="mt-1.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('team.memberCapacityDescription') }}
          </p>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="closeEdit">{{ t('common.cancel') }}</button>
          <button form="admin-team-edit" class="btn btn-primary" :disabled="saving">
            {{ t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="Boolean(detailsTeam)"
      :title="t('team.detailsTitle', { name: detailsTeam?.name || '' })"
      width="wide"
      @close="closeDetails"
    >
      <ContentSkeleton v-if="detailsLoading" variant="detail" :rows="6" class="py-4" />
      <div v-else-if="detailsTeam" class="space-y-6">
        <div
          class="flex flex-wrap items-center justify-between gap-3 rounded-control border border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-800"
        >
          <div>
            <p class="font-semibold text-gray-900 dark:text-white">{{ detailsTeam.name }}</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ detailsTeam.owner_email }} ·
              {{ t('team.memberCount', { count: detailsTeam.member_count }) }}
            </p>
          </div>
          <span
            :class="['badge', detailsTeam.status === 'active' ? 'badge-success' : 'badge-danger']"
          >
            {{
              detailsTeam.status === 'active' ? t('team.statusActive') : t('team.statusSuspended')
            }}
          </span>
        </div>

        <div>
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('team.members') }}
          </h3>
          <div
            class="mt-3 divide-y divide-gray-100 overflow-hidden rounded-control border border-gray-200 dark:divide-dark-700 dark:border-dark-600"
          >
            <div
              v-for="member in detailsMembers"
              :key="member.user_id"
              class="flex items-center justify-between gap-3 px-4 py-3"
            >
              <div class="min-w-0">
                <p class="truncate font-medium text-gray-900 dark:text-white">
                  {{ member.username || member.email }}
                </p>
                <p v-if="member.username" class="truncate text-xs text-gray-500">
                  {{ member.email }}
                </p>
              </div>
              <span class="badge" :class="member.role === 'owner' ? 'badge-primary' : 'badge-gray'">
                {{ member.role === 'owner' ? t('team.owner') : t('team.member') }}
              </span>
            </div>
          </div>
        </div>

        <div v-if="memberOptions.length" class="border-t border-gray-200 pt-5 dark:border-dark-700">
          <label class="input-label">{{ t('team.transferTitle') }}</label>
          <div class="flex flex-col gap-3 sm:flex-row">
            <div class="flex-1">
              <Select v-model="transferUserID" :options="memberOptions" />
            </div>
            <button class="btn btn-secondary" :disabled="!transferUserID" @click="forceTransfer">
              <Icon name="swap" size="sm" />
              {{ t('team.transfer') }}
            </button>
          </div>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <button class="btn btn-secondary" @click="closeDetails">{{ t('common.close') }}</button>
        </div>
      </template>
    </BaseDialog>

    <TeamActionMenu
      :show="Boolean(menuTeam)"
      :team="menuTeam"
      :position="menuPosition"
      @close="closeActionMenu"
      @details="openDetails"
      @statistics="openStatistics"
      @dissolve="askDissolve"
    />
    <TeamStatsModal
      :show="Boolean(statisticsTeam)"
      :team="statisticsTeam"
      @close="statisticsTeam = null"
    />
    <ConfirmDialog
      :show="Boolean(dissolvingTeam)"
      :title="t('team.dissolveTitle')"
      :message="t('team.dissolveMessage')"
      danger
      @cancel="dissolvingTeam = null"
      @confirm="dissolveTeam"
    />
    <TotpStepUpDialog :controller="stepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AdminTeam } from '@/api/admin/teams'
import type { TeamMembership } from '@/api/team'
import TeamActionMenu from '@/components/admin/team/TeamActionMenu.vue'
import TeamStatsModal from '@/components/admin/team/TeamStatsModal.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import type { Column } from '@/components/common/table/types'
import { useStepUp, isStepUpCancelled } from '@/composables/useStepUp'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { getFloatingPanelPosition } from '@/utils/floatingPanel'

// TeamsView.vue lists teams and handles creation, editing, member inspection, ownership
// transfer, and dissolution. Filtering and pagination operate on the complete loaded
// team list. The details dialog uses a sequence counter so selecting another team or
// closing the dialog prevents an earlier member request from repopulating its contents.
//
// Ownership transfer and dissolution run through the step-up authentication helper. A
// successful transfer reloads teams before reopening member details for the transferred
// team, while cancellation from step-up is left silent. Action-menu placement is
// computed from the trigger and viewport, and the currently selected team supplies the
// action target.

// Translation, notifications, and step-up authentication support team actions, while
// list and dialog state belong to this page.
const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
// Team rows and operation flags drive list loading, form submission, and the row
// currently changing status.
const teams = ref<AdminTeam[]>([])
const loading = ref(false)
const saving = ref(false)
const statusUpdatingID = ref<number | null>(null)
// Search text and local page controls select a slice of the complete team list returned
// by the endpoint.
const searchQuery = ref('')
const page = ref(1)
const pageSize = ref(getPersistedPageSize())
// Selected teams independently control creation, editing, details, statistics,
// dissolution, and the floating action menu state.
const showCreate = ref(false)
const editingTeam = ref<AdminTeam | null>(null)
const detailsTeam = ref<AdminTeam | null>(null)
const detailsMembers = ref<TeamMembership[]>([])
const detailsLoading = ref(false)
const transferUserID = ref<number | null>(null)
const statisticsTeam = ref<AdminTeam | null>(null)
const dissolvingTeam = ref<AdminTeam | null>(null)
const menuTeam = ref<AdminTeam | null>(null)
const menuPosition = ref<{
  top: number // viewport top pixels
  left: number // viewport left pixels
} | null>(null)
// Creation and edit drafts hold team settings, while detailsSequence invalidates member
// results after selection changes or dialog closure.
const createForm = reactive({
  owner_user_id: 0, // team owner identifier
  name: '', // displayed name text
  member_limit: 10, // allowed membership count
})
const editForm = reactive({
  name: '', // displayed name text
  member_limit: 10, // allowed membership count
})
let detailsSequence = 0

// Translated column definitions describe team identity, owner, membership, status, and
// table actions for the current locale.
const columns = computed<Column[]>(() => [
  { key: 'id', label: 'ID', sortable: true },
  { key: 'name', label: t('team.name'), sortable: true },
  { key: 'owner_email', label: t('team.owner'), sortable: true },
  { key: 'member_count', label: t('team.members'), sortable: true },
  { key: 'status', label: t('common.status'), sortable: true },
  { key: 'created_at', label: t('team.createdAt'), sortable: true },
  { key: 'actions', label: t('common.actions'), class: 'text-left' },
])

// filteredTeams matches the trimmed search against team name, owner email, or
// identifier using case-insensitive text comparison.
const filteredTeams = computed(() => {
  const query = searchQuery.value.trim().toLocaleLowerCase()
  if (!query) return teams.value
  return teams.value.filter(
    (team) =>
      team.name.toLocaleLowerCase().includes(query) ||
      team.owner_email.toLocaleLowerCase().includes(query) ||
      String(team.id).includes(query),
  )
})

// paginatedTeams slices the filtered collection using the selected local page and
// number of rows per page.
const paginatedTeams = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return filteredTeams.value.slice(start, start + pageSize.value)
})

// memberOptions offers regular members as ownership-transfer candidates, preferring
// each member username over their email label.
const memberOptions = computed<SelectOption[]>(() =>
  detailsMembers.value
    .filter((member) => member.role === 'member')
    .map((member) => ({ value: member.user_id, label: member.username || member.email })),
)

// A changed search can shrink the filtered collection, so start at page one before
// slicing it for display.
watch(searchQuery, () => {
  page.value = 1
})
// When a list reload reduces the matching row count, clamp the local page so
// pagination cannot display an empty out-of-range slice.
watch(filteredTeams, (items) => {
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize.value))
  if (page.value > totalPages) page.value = totalPages
})

// loadTeams replaces the complete team list after a successful server request. Failure
// reports an application error and retains previous rows, and the loading flag is
// released when the request settles.
const loadTeams = async () => {
  loading.value = true
  try {
    teams.value = await adminAPI.teams.list()
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    loading.value = false
  }
}

// handlePageSizeChange updates local pagination and returns to the first page.
const handlePageSizeChange = (value: number) => {
  pageSize.value = value
  page.value = 1
}

// createTeam creates the draft team and refreshes the list after success. The owner and
// name fields are cleared after persistence, while the chosen member limit remains
// available for subsequent creation.
const createTeam = async () => {
  saving.value = true
  try {
    await adminAPI.teams.create({ ...createForm })
    showCreate.value = false
    createForm.owner_user_id = 0
    createForm.name = ''
    await loadTeams()
    appStore.showSuccess(t('team.created'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    saving.value = false
  }
}

// openEdit selects a team and copies its name and member limit.
const openEdit = (team: AdminTeam) => {
  editingTeam.value = team
  editForm.name = team.name
  editForm.member_limit = team.member_limit
}

// closeEdit releases the selected team and closes its editor.
const closeEdit = () => {
  editingTeam.value = null
}

// saveEdit persists name and member limit for the selected team. Success closes the
// editor before reloading teams; errors retain the draft and release the saving flag
// when the request settles.
const saveEdit = async () => {
  if (!editingTeam.value) return
  saving.value = true
  try {
    await adminAPI.teams.update(editingTeam.value.id, {
      name: editForm.name,
      member_limit: editForm.member_limit,
    })
    closeEdit()
    await loadTeams()
    appStore.showSuccess(t('team.updated'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    saving.value = false
  }
}

// toggleStatus switches an active team to suspended or reactivates another status. The
// row identifier drives pending feedback until the update and list refresh settle, and
// failures are shown through the application store.
const toggleStatus = async (team: AdminTeam) => {
  statusUpdatingID.value = team.id
  const status = team.status === 'active' ? 'suspended' : 'active'
  try {
    await adminAPI.teams.update(team.id, { status })
    await loadTeams()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    appStore.showError(error?.message || t('common.error'))
  } finally {
    statusUpdatingID.value = null
  }
}

// openActionMenu toggles the selected team's menu at its trigger position. Missing
// triggers leave the current state unchanged; viewport-aware placement uses a
// fixed-height panel that can flip above its trigger.
const openActionMenu = (team: AdminTeam, event: MouseEvent) => {
  if (menuTeam.value?.id === team.id) {
    closeActionMenu()
    return
  }
  const target = event.currentTarget as HTMLElement | null
  if (!target) return
  const rect = target.getBoundingClientRect()
  // Use the fixed menu height to flip the whole panel when space below is
  // insufficient, while retaining trigger alignment on narrow screens.
  const position = getFloatingPanelPosition(rect, window.innerWidth, window.innerHeight, {
    maxWidth: 208,
    fixedHeight: 142,
    viewportPadding: 8,
    gap: 4,
  })
  // The fixed-height position normally provides top; retain viewport padding if the
  // helper returns no top coordinate for this panel.
  menuPosition.value = { top: position.top ?? 8, left: position.left }
  menuTeam.value = team
}

// closeActionMenu clears the selected team and its floating panel position.
const closeActionMenu = () => {
  menuTeam.value = null
  menuPosition.value = null
}

// openDetails selects a team, clears prior members, and requests its membership list.
// Each opening increments detailsSequence before dispatch, giving newer selections
// priority over earlier responses. Closing details increments that same sequence, so a
// pending completion cannot repopulate the closed dialog. Rows, errors, and loading
// cleanup are accepted only for the current sequence, while the transfer candidate is
// reset for each opening.
const openDetails = async (team: AdminTeam) => {
  // Give this detail selection a new generation before the membership request so
  // older team responses cannot overwrite the selected team.
  const sequence = ++detailsSequence
  detailsTeam.value = team
  detailsMembers.value = []
  transferUserID.value = null
  detailsLoading.value = true
  try {
    const members = await adminAPI.teams.members(team.id)
    // The dialog may have closed or switched teams while awaiting members; publish
    // rows only for the still-current selection generation.
    if (sequence !== detailsSequence) return
    detailsMembers.value = members
  } catch (error: any) {
    if (sequence === detailsSequence) appStore.showError(error?.message || t('common.error'))
  } finally {
    if (sequence === detailsSequence) detailsLoading.value = false
  }
}

// closeDetails invalidates pending detail results and clears the visible selection. The
// sequence advances before dialog state is cleared, so a completing member request
// cannot restore rows or loading state afterward.
const closeDetails = () => {
  // Invalidate pending member work before clearing the dialog so its late completion
  // cannot restore the closed detail state.
  detailsSequence++
  detailsTeam.value = null
  detailsMembers.value = []
  transferUserID.value = null
  detailsLoading.value = false
}

// openStatistics selects the team displayed by the statistics dialog component.
const openStatistics = (team: AdminTeam) => {
  statisticsTeam.value = team
}

// forceTransfer submits the selected team and member through step-up authentication.
// Success reloads teams before opening refreshed membership details; cancelling step-up
// is silent, while other failures display an error to the administrator.
const forceTransfer = async () => {
  const team = detailsTeam.value
  const targetUserID = transferUserID.value
  if (!team || !targetUserID) return
  try {
    await stepUp.run(() => adminAPI.teams.forceTransfer(team.id, targetUserID))
    await loadTeams()
    // Use the reloaded team when available; retaining the selected record allows
    // membership details to reopen even if the refreshed list omits it.
    const refreshed = teams.value.find((item) => item.id === team.id) || team
    await openDetails(refreshed)
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    // Authentication cancellation is an expected dismissal, so show an error only
    // when step-up or the requested team operation actually fails.
    if (!isStepUpCancelled(error)) appStore.showError(error?.message || t('common.error'))
  }
}

// askDissolve selects the team awaiting confirmation of permanent dissolution.
const askDissolve = (team: AdminTeam) => {
  dissolvingTeam.value = team
}

// dissolveTeam clears the confirmation target and runs dissolution through step-up
// authentication. Success reloads teams; cancelled authentication remains silent, while
// other failures are reported after the confirmation has already closed.
const dissolveTeam = async () => {
  const team = dissolvingTeam.value
  if (!team) return
  dissolvingTeam.value = null
  try {
    await stepUp.run(() => adminAPI.teams.dissolve(team.id))
    await loadTeams()
    appStore.showSuccess(t('team.operationSuccess'))
  } catch (error: any) {
    // Authentication cancellation is an expected dismissal, so show an error only
    // when step-up or the requested team operation actually fails.
    if (!isStepUpCancelled(error)) appStore.showError(error?.message || t('common.error'))
  }
}

onMounted(loadTeams)
</script>

<style scoped>
.row-action {
  @apply flex flex-col items-center gap-0.5 rounded-control p-1.5 text-xs text-gray-500 transition-colors hover:bg-gray-100 disabled:cursor-not-allowed disabled:opacity-50 dark:hover:bg-dark-700;
}
</style>
