<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-2 lg:flex-row lg:flex-wrap lg:items-start lg:justify-between">
          <div class="flex min-w-0 flex-1 flex-nowrap items-center gap-2">
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('admin.redeem.searchCodes')"
              class="input min-w-0 flex-1 sm:flex-none sm:w-56 lg:w-40 xl:w-64"
              @input="handleSearch"
            />
            <FilterDropdown :active-count="activeFilterCount" @reset="resetRedeemFilters">
              <FilterField :label="t('admin.redeem.columns.type')" icon="ticket">
                <FilterOptions
                  v-model="filters.type"
                  :options="filterTypeOptions"
                  @change="loadCodes"
                />
              </FilterField>
              <FilterField :label="t('admin.redeem.columns.status')" icon="checkCircle">
                <FilterOptions
                  v-model="filters.status"
                  :options="filterStatusOptions"
                  @change="loadCodes"
                />
              </FilterField>
            </FilterDropdown>
          </div>

          <div class="flex w-full flex-wrap items-center justify-end gap-2 lg:w-auto lg:shrink-0">
            <button
              @click="loadCodes"
              :disabled="loading"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button @click="handleExportCodes" class="btn btn-secondary shrink-0 whitespace-nowrap">
              {{ t('admin.redeem.exportCsv') }}
            </button>
            <button
              data-testid="batch-update-open"
              @click="openBatchUpdateDialog"
              :disabled="selectedCount === 0 || batchUpdating"
              class="btn btn-secondary shrink-0 whitespace-nowrap"
            >
              <Icon name="edit" size="sm" class="mr-2" />
              {{ t('admin.redeem.batchUpdate') }}
            </button>
            <button
              data-testid="generate-open"
              @click="showGenerateDialog = true"
              class="btn btn-primary shrink-0 whitespace-nowrap"
            >
              {{ t('admin.redeem.generateCodes') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-redeem-column-order"
          :columns="columns"
          :data="codes"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="id"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #header-select>
            <input
              data-testid="select-all-codes"
              type="checkbox"
              class="h-4 w-4 cursor-pointer rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="allVisibleSelected"
              @click.stop
              @change="toggleSelectAllVisible($event)"
            />
          </template>

          <template #cell-select="{ row }">
            <input
              data-testid="select-code"
              type="checkbox"
              class="h-4 w-4 cursor-pointer rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="selectedCodeIds.has(row.id)"
              @click.stop
              @change="toggleSelectRow(row.id, $event)"
            />
          </template>

          <template #cell-code="{ value, row }">
            <div class="flex items-center space-x-2">
              <code class="font-mono text-sm text-gray-900 dark:text-gray-100">{{ value }}</code>
              <span v-if="row.requires_payment" class="badge badge-warning">
                {{ t('admin.redeem.requiresPayment') }}
              </span>
              <button
                @click="copyToClipboard(value)"
                :class="[
                  'flex items-center transition-colors',
                  copiedCode === value
                    ? 'text-green-500'
                    : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-300',
                ]"
                :title="copiedCode === value ? t('admin.redeem.copied') : t('keys.copyToClipboard')"
              >
                <Icon v-if="copiedCode !== value" name="copy" size="sm" :stroke-width="2" />
                <Icon name="check" size="sm" :animate-on-hover="false" v-else class="h-4 w-4" />
              </button>
            </div>
          </template>

          <template #cell-type="{ value }">
            <span
              :class="[
                'badge',
                value === 'balance' || value === 'affiliate_balance' || value === 'plugin_balance'
                  ? 'badge-success'
                  : value === 'subscription'
                    ? 'badge-warning'
                    : 'badge-primary',
              ]"
            >
              {{ t('admin.redeem.types.' + value) }}
            </span>
          </template>

          <template #cell-value="{ value, row }">
            <span class="text-sm font-medium text-gray-900 dark:text-white">
              <template
                v-if="
                  row.type === 'balance' ||
                  row.type === 'affiliate_balance' ||
                  row.type === 'plugin_balance'
                "
              >
                {{ formatBalanceAmount(value, { fractionDigits: 2 }) }}
              </template>
              <template v-else-if="row.type === 'subscription'">
                {{ row.plan?.name || (row.plan_id ? `Plan #${row.plan_id}` : '-') }}
              </template>
              <template v-else>{{ value }}</template>
            </span>
          </template>

          <template #cell-usage_progress="{ row }">
            <span class="text-sm text-gray-600 dark:text-gray-300">
              {{ row.used_count }} / {{ row.max_uses === 0 ? '∞' : row.max_uses }}
            </span>
          </template>

          <template #cell-status="{ value }">
            <span :class="['badge', getStatusBadgeClass(value)]">
              {{ t('admin.redeem.status.' + value) }}
            </span>
          </template>

          <template #cell-expires_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ value ? formatDateTime(value) : t('admin.redeem.neverExpires') }}
            </span>
          </template>

          <template #cell-used_by="{ value, row }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ row.user?.email || (value ? t('admin.redeem.userPrefix', { id: value }) : '-') }}
            </span>
          </template>

          <template #cell-used_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{
              value ? formatDateTime(value) : '-'
            }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center space-x-2">
              <button
                v-if="canEditCode(row)"
                @click="handleEdit(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-primary-50 hover:text-primary-600 dark:hover:bg-primary-900/20 dark:hover:text-primary-400"
                :title="t('admin.redeem.editCode')"
              >
                <Icon name="edit" size="sm" :stroke-width="2" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                v-if="row.status === 'unused'"
                @click="handleDelete(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              >
                <Icon name="trash" size="sm" class="h-4 w-4" />
                <span class="text-xs">{{ t('common.delete') }}</span>
              </button>
              <span
                v-if="!canEditCode(row) && row.status !== 'unused'"
                class="text-gray-400 dark:text-dark-500"
                >-</span
              >
            </div>
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

        <div
          v-if="selectedCount > 0"
          class="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-control bg-primary-50 p-3 dark:bg-primary-900/20"
        >
          <span class="text-sm font-medium text-primary-900 dark:text-primary-100">
            {{ t('admin.redeem.selectedCount', { count: selectedCount }) }}
          </span>
          <div class="flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="text-xs font-medium text-primary-700 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"
              @click="clearSelectedCodes"
            >
              {{ t('admin.redeem.clearSelection') }}
            </button>
            <button type="button" class="btn btn-primary btn-sm" @click="openBatchUpdateDialog">
              {{ t('admin.redeem.batchUpdate') }}
            </button>
          </div>
        </div>

        <div v-if="filters.status === 'unused'" class="flex justify-end">
          <button @click="showDeleteUnusedDialog = true" class="btn btn-danger">
            {{ t('admin.redeem.deleteAllUnused') }}
          </button>
        </div>
      </template>
    </TablePageLayout>

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.redeem.deleteCode')"
      :message="t('admin.redeem.deleteCodeConfirm')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <ConfirmDialog
      :show="showDeleteUnusedDialog"
      :title="t('admin.redeem.deleteAllUnused')"
      :message="t('admin.redeem.deleteAllUnusedConfirm')"
      :confirm-text="t('admin.redeem.deleteAll')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDeleteUnused"
      @cancel="showDeleteUnusedDialog = false"
    />

    <BaseDialog
      :show="showGenerateDialog"
      :title="t('admin.redeem.generateCodesTitle')"
      width="narrow"
      close-on-click-outside
      @close="showGenerateDialog = false"
    >
      <form data-testid="generate-form" @submit.prevent="handleGenerateCodes" class="space-y-4">
        <div>
          <label class="input-label">
            {{ t('admin.redeem.customCode') }}
            <span class="ml-1 text-xs font-normal text-gray-400">
              ({{ t('common.optional') }})
            </span>
          </label>
          <input
            v-model="generateForm.code"
            type="text"
            class="input"
            :placeholder="t('admin.redeem.customCodePlaceholder')"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.redeem.customCodeHint') }}
          </p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.redeem.codeType') }}</label>
          <Select v-model="generateForm.type" :options="typeOptions" />
        </div>

        <div v-if="generateForm.type !== 'subscription' && generateForm.type !== 'invitation'">
          <label class="input-label">
            {{
              generateForm.type === 'balance'
                ? t('admin.redeem.amount', { unitName: balanceUnitName })
                : t('admin.redeem.columns.value')
            }}
          </label>
          <input
            v-model.number="generateForm.value"
            type="number"
            :step="generateForm.type === 'balance' ? '0.01' : '1'"
            :min="generateForm.type === 'balance' ? '0.01' : '1'"
            required
            class="input"
          />
        </div>

        <div
          v-if="generateForm.type === 'invitation'"
          class="rounded-control bg-blue-50 p-3 dark:bg-blue-900/20"
        >
          <p class="text-sm text-blue-700 dark:text-blue-300">
            {{ t('admin.redeem.invitationHint') }}
          </p>
        </div>

        <template v-if="generateForm.type === 'subscription'">
          <div>
            <label class="input-label">{{ t('payment.admin.planName') }}</label>
            <Select
              v-model="generateForm.plan_id"
              :options="subscriptionPlanOptions"
              :placeholder="t('admin.announcements.form.selectPackages')"
            />
          </div>
        </template>

        <!-- Payment eligibility is saved with the code and checked by the server when a user attempts redemption. -->
        <SettingToggleRow
          v-if="generateForm.type !== 'invitation'"
          id="generateForm-requires-payment"
          v-model="generateForm.requires_payment"
          :label="t('admin.redeem.requiresPayment')"
          :hint="t('admin.redeem.requiresPaymentHint')"
        />

        <div>
          <label class="input-label">{{ t('admin.redeem.maxUses') }}</label>
          <input
            data-testid="generate-max-uses"
            v-model.number="generateForm.max_uses"
            type="number"
            min="0"
            required
            class="input"
            :disabled="generateForm.type === 'invitation'"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.redeem.maxUsesHint') }}
          </p>
        </div>
        <div>
          <label class="input-label">
            {{ t('admin.redeem.expiresAt') }}
            <span class="ml-1 text-xs font-normal text-gray-400">
              ({{ t('common.optional') }})
            </span>
          </label>
          <input v-model="generateForm.expires_at_str" type="datetime-local" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.redeem.count') }}</label>
          <input
            v-model.number="generateForm.count"
            type="number"
            min="1"
            max="100"
            required
            class="input"
            :disabled="hasCustomCode"
          />
          <p v-if="hasCustomCode" class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.redeem.customCodeCountHint') }}
          </p>
        </div>
        <div class="flex justify-end gap-3 pt-2">
          <button type="button" @click="showGenerateDialog = false" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" :disabled="generating" class="btn btn-primary">
            {{ generating ? t('admin.redeem.generating') : t('admin.redeem.generate') }}
          </button>
        </div>
      </form>
    </BaseDialog>

    <BaseDialog
      :show="showEditDialog"
      :title="t('admin.redeem.editCodeTitle')"
      width="normal"
      @close="closeEditDialog"
    >
      <form id="edit-redeem-form" @submit.prevent="handleUpdateCode" class="space-y-4">
        <div v-if="editingCode">
          <label class="input-label">{{ t('admin.redeem.columns.code') }}</label>
          <input :value="editingCode.code" type="text" readonly class="input font-mono" />
        </div>

        <div v-if="editingCode">
          <label class="input-label">{{ t('admin.redeem.codeType') }}</label>
          <input
            :value="t('admin.redeem.types.' + editingCode.type)"
            type="text"
            readonly
            class="input"
          />
        </div>

        <div
          v-if="
            editingCode &&
            editingCode.used_count === 0 &&
            editingCode.type !== 'subscription' &&
            editingCode.type !== 'invitation'
          "
        >
          <label class="input-label">
            {{
              editingCode.type === 'balance'
                ? t('admin.redeem.amount', { unitName: balanceUnitName })
                : t('admin.redeem.columns.value')
            }}
          </label>
          <input
            v-model.number="editForm.value"
            type="number"
            :step="editingCode.type === 'balance' ? '0.01' : '1'"
            :min="editingCode.type === 'balance' ? '0.01' : '1'"
            required
            class="input"
          />
        </div>

        <div
          v-if="editingCode && editingCode.used_count === 0 && editingCode.type === 'subscription'"
        >
          <label class="input-label">{{ t('payment.admin.planName') }}</label>
          <Select
            v-model="editForm.plan_id"
            :options="subscriptionPlanOptions"
            :placeholder="t('admin.announcements.form.selectPackages')"
          />
        </div>

        <div
          v-if="editingCode && editingCode.used_count > 0"
          class="rounded-control bg-yellow-50 p-3 dark:bg-yellow-900/20"
        >
          <p class="text-sm text-yellow-700 dark:text-yellow-300">
            {{ t('admin.redeem.valueLockedHint') }}
          </p>
        </div>

        <SettingToggleRow
          v-if="editingCode?.type !== 'invitation'"
          id="editForm-requires-payment"
          v-model="editForm.requires_payment"
          :label="t('admin.redeem.requiresPayment')"
          :hint="t('admin.redeem.requiresPaymentHint')"
        />

        <div>
          <label class="input-label">{{ t('admin.redeem.maxUses') }}</label>
          <input v-model.number="editForm.max_uses" type="number" min="0" required class="input" />
          <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.redeem.maxUsesHint') }}
          </p>
        </div>

        <div>
          <label class="input-label">
            {{ t('admin.redeem.expiresAt') }}
            <span class="ml-1 text-xs font-normal text-gray-400">
              ({{ t('common.optional') }})
            </span>
          </label>
          <input v-model="editForm.expires_at_str" type="datetime-local" class="input" />
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" @click="closeEditDialog" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            type="submit"
            form="edit-redeem-form"
            :disabled="updating"
            class="btn btn-primary"
          >
            {{ updating ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showBatchUpdateDialog"
      :title="t('admin.redeem.batchUpdateTitle')"
      width="normal"
      @close="closeBatchUpdateDialog"
    >
      <form id="batch-update-redeem-form" class="space-y-4" @submit.prevent="handleBatchUpdate">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.redeem.selectedCount', { count: selectedCount }) }}
        </p>

        <div class="space-y-2">
          <label
            class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            <input
              data-testid="batch-field-status"
              v-model="batchUpdateForm.update_status"
              type="checkbox"
              class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
            />
            {{ t('admin.redeem.batchFields.status') }}
          </label>
          <Select
            v-if="batchUpdateForm.update_status"
            v-model="batchUpdateForm.status"
            data-testid="batch-status-select"
            :options="batchStatusOptions"
          />
        </div>

        <div class="space-y-2">
          <label
            class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            <input
              v-model="batchUpdateForm.update_expires_at"
              type="checkbox"
              class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
            />
            {{ t('admin.redeem.batchFields.expiresAt') }}
          </label>
          <template v-if="batchUpdateForm.update_expires_at">
            <Select v-model="batchUpdateForm.expires_mode" :options="batchExpiryModeOptions" />
            <input
              v-if="batchUpdateForm.expires_mode === 'custom'"
              v-model="batchUpdateForm.expires_at_str"
              type="datetime-local"
              class="input"
            />
          </template>
        </div>

        <div class="space-y-2">
          <label
            class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300"
          >
            <input
              data-testid="batch-field-notes"
              v-model="batchUpdateForm.update_notes"
              type="checkbox"
              class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
            />
            {{ t('admin.redeem.batchFields.notes') }}
          </label>
          <textarea
            v-if="batchUpdateForm.update_notes"
            data-testid="batch-notes-input"
            v-model="batchUpdateForm.notes"
            rows="3"
            class="input"
            :placeholder="t('admin.redeem.batchNotesPlaceholder')"
          ></textarea>
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" @click="closeBatchUpdateDialog" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button
            data-testid="batch-update-submit"
            type="submit"
            form="batch-update-redeem-form"
            :disabled="batchUpdating"
            class="btn btn-primary"
          >
            {{ batchUpdating ? t('common.processing') : t('admin.redeem.batchUpdate') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog
      :show="showResultDialog"
      :title="t('admin.redeem.generatedSuccessfully')"
      :subtitle="t('admin.redeem.codesCreated', { count: generatedCodes.length })"
      @close="closeResultDialog"
    >
      <template #header-icon>
        <span
          class="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30"
        >
          <Icon
            name="check"
            size="md"
            :animate-on-hover="false"
            class="h-5 w-5 text-green-600 dark:text-green-400"
          />
        </span>
      </template>
      <textarea
        readonly
        :value="generatedCodesText"
        :style="{ height: textareaHeight }"
        class="input resize-none p-3 font-mono"
      ></textarea>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button
            @click="copyGeneratedCodes"
            :class="[
              'btn flex items-center gap-2 transition',
              copiedAll ? 'btn-success' : 'btn-secondary',
            ]"
          >
            <Icon v-if="!copiedAll" name="copy" size="sm" :stroke-width="2" />
            <Icon name="check" size="sm" :animate-on-hover="false" v-else class="h-4 w-4" />
            {{ copiedAll ? t('admin.redeem.copied') : t('admin.redeem.copyAll') }}
          </button>
          <button @click="downloadGeneratedCodes" class="btn btn-primary flex items-center gap-2">
            <Icon name="download" size="sm" :stroke-width="2" />
            {{ t('admin.redeem.download') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { COPY_FEEDBACK_MS } from '@/constants/ui'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useTableSelection } from '@/composables/useTableSelection'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { adminAPI } from '@/api/admin'
import { formatDateTime, formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import type { BatchUpdateRedeemCodeFields, RedeemCode, RedeemCodeType } from '@/api/redeem.types'
import type { SubscriptionPlan } from '@/api/payment.types'
import type { Column } from '@/components/common/table/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import type { FilterOption } from '@/components/common/filterPanel'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'

// RedeemView.vue manages redeem-code generation, editing, selection, batch updates, and
// exports. Creation validates Unicode code-point length and forces a custom code to
// generate one record. Editing protects already-used value and plan fields while
// allowing expiry and supported usage conditions to change.
//
// Batch drafts distinguish unchecked fields from selected fields whose values clear
// server state. Expiry clearing uses null in batch requests and zero in individual
// updates, matching the two request shapes. Main-list reloads abort earlier requests
// and reject aborted results. Downloads create temporary links and Blob URLs, while
// selection state is delegated to the table-selection composable.

// Translation, application messages, clipboard access, and balance formatting support
// the redeem-code table and its editor dialogs.
const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard: clipboardCopy } = useClipboard()
const { balanceUnitName, formatBalanceAmount } = useBalanceDisplay()

// PlanOption describes a subscription choice with its numeric identifier and optional
// description.
interface PlanOption {
  value: number // selected plan identifier
  label: string // displayed option label
  description: string | null // optional plan description
  // Additional option metadata accepted by the shared select component.
  [key: string]: unknown
}

// Generation and result visibility track the draft and its returned codes, with plan
// choices loaded separately.
const showGenerateDialog = ref(false)
const showResultDialog = ref(false)
const generatedCodes = ref<RedeemCode[]>([])
const subscriptionPlans = ref<SubscriptionPlan[]>([])

// Subscription options expose plan identifiers, names, and descriptions, while result
// text joins returned codes with newline separators.
const subscriptionPlanOptions = computed<PlanOption[]>(() =>
  subscriptionPlans.value.map((plan) => ({
    value: plan.id,
    label: plan.name,
    description: plan.description,
  })),
)

const generatedCodesText = computed(() => {
  return generatedCodes.value.map((code) => code.code).join('\n')
})

// textareaHeight bounds the result editor height using generated line count,
// approximate line height, and vertical padding.
const textareaHeight = computed(() => {
  const lineCount = generatedCodes.value.length
  // Approximate each generated-code line as 24 pixels for bounded preview height.
  const lineHeight = 24
  // Reserve vertical padding around the generated text lines.
  const padding = 24
  // Keep short output readable and cap longer previews at 240 pixels.
  const minHeight = 60
  const maxHeight = 240
  const calculatedHeight = Math.min(
    Math.max(lineCount * lineHeight + padding, minHeight),
    maxHeight,
  )
  return `${calculatedHeight}px`
})

// copiedAll records successful whole-result clipboard feedback until the timer expires
// or the result dialog closes.
const copiedAll = ref(false)

// closeResultDialog hides generated results and clears their codes and copy
// feedback. The generated text derives from that collection, so closing also removes
// the content offered for copying or downloading.
const closeResultDialog = () => {
  showResultDialog.value = false
  generatedCodes.value = []
  copiedAll.value = false
}

// copyGeneratedCodes copies all generated codes as newline-separated text and shows
// temporary feedback. Successful copies set copiedAll until the shared feedback timer
// fires; unsuccessful copies leave that indicator unchanged.
const copyGeneratedCodes = async () => {
  const success = await clipboardCopy(generatedCodesText.value, t('admin.redeem.copied'))
  if (success) {
    copiedAll.value = true
    // Clear the successful clipboard marker after the shared feedback interval so a
    // prior copy does not remain marked indefinitely.
    setTimeout(() => {
      copiedAll.value = false
    }, COPY_FEEDBACK_MS)
  }
}

// downloadGeneratedCodes downloads the generated codes as a dated plain-text file. It
// creates a temporary anchor and Blob URL, then removes the anchor and revokes the URL
// after dispatching the click.
const downloadGeneratedCodes = () => {
  // The download owns a temporary link and Blob URL; remove the link and revoke the
  // URL after triggering the browser download.
  const blob = new Blob([generatedCodesText.value], { type: 'text/plain' })
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `redeem-codes-${new Date().toISOString().split('T')[0]}.txt`
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  window.URL.revokeObjectURL(url)
}

// Column definitions and localized type, status, and expiry selectors supply the table
// and supported editor choices.
const columns = computed<Column[]>(() => [
  { key: 'select', label: '' },
  { key: 'code', label: t('admin.redeem.columns.code') },
  { key: 'type', label: t('admin.redeem.columns.type'), sortable: true },
  { key: 'value', label: t('admin.redeem.columns.value'), sortable: true },
  { key: 'usage_progress', label: t('admin.redeem.columns.usageProgress') },
  { key: 'status', label: t('admin.redeem.columns.status'), sortable: true },
  { key: 'expires_at', label: t('admin.redeem.columns.expiresAt'), sortable: true },
  { key: 'used_by', label: t('admin.redeem.columns.usedBy') },
  { key: 'used_at', label: t('admin.redeem.columns.usedAt'), sortable: true },
  { key: 'actions', label: t('admin.redeem.columns.actions') },
])

const typeOptions = computed(() => [
  { value: 'balance', label: t('admin.redeem.balance') },
  { value: 'concurrency', label: t('admin.redeem.concurrency') },
  { value: 'subscription', label: t('admin.redeem.subscription') },
  { value: 'invitation', label: t('admin.redeem.invitation') },
])

// filterTypeOptions includes generated credentials and persisted adjustment sources for
// list filtering. Plugin balance records are history entries and are excluded from
// the separate generation choices above.
const filterTypeOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.redeem.allTypes') },
  { value: 'balance', label: t('admin.redeem.balance'), icon: 'dollar' },
  {
    value: 'affiliate_balance',
    label: t('admin.redeem.types.affiliate_balance'),
    icon: 'affiliate',
  },
  {
    value: 'plugin_balance', // Persisted plugin adjustment type.
    label: t('admin.redeem.types.plugin_balance'), // Localized source filter label.
    icon: 'puzzle',
  },
  { value: 'concurrency', label: t('admin.redeem.concurrency'), icon: 'layers' },
  { value: 'subscription', label: t('admin.redeem.subscription'), icon: 'calendar' },
  { value: 'invitation', label: t('admin.redeem.invitation'), icon: 'userPlus' },
])

const filterStatusOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.redeem.allStatus') },
  { value: 'unused', label: t('admin.redeem.unused'), dot: 'green' },
  { value: 'active', label: t('admin.redeem.status.active'), dot: 'amber' },
  { value: 'used', label: t('admin.redeem.used'), dot: 'gray' },
  { value: 'expired', label: t('admin.redeem.status.expired'), dot: 'red' },
  { value: 'disabled', label: t('admin.redeem.status.disabled'), dot: 'gray' },
])

const batchStatusOptions = computed(() => [
  { value: 'unused', label: t('admin.redeem.status.unused') },
  { value: 'disabled', label: t('admin.redeem.status.disabled') },
])

const batchExpiryModeOptions = computed(() => [
  { value: 'clear', label: t('admin.redeem.neverExpires') },
  { value: 'custom', label: t('admin.redeem.customExpiry') },
])

// Code rows, operation flags, search filters, pagination, and sorting describe the
// current main-list request and its controls.
const codes = ref<RedeemCode[]>([])
const loading = ref(false)
const generating = ref(false)
const updating = ref(false)
const batchUpdating = ref(false)
const searchQuery = ref('')
const filters = reactive({
  type: '', // selected record kind
  status: '', // configured lifecycle status
})
const pagination = reactive({
  page: 1, // requested page number
  page_size: getPersistedPageSize(), // requested row count
  total: 0, // matching row count
  pages: 0, // matching page count
})
const sortState = reactive({
  sort_by: 'id', // server sorting field
  sort_order: 'desc' as 'asc' | 'desc', // server sorting direction
})

// activeFilterCount counts selected type and status conditions for the filter panel
// badge shown beside the controls.
const activeFilterCount = computed(() => [filters.type, filters.status].filter(Boolean).length)

// resetRedeemFilters clears type and status before loading the first page.
const resetRedeemFilters = () => {
  filters.type = ''
  filters.status = ''
  pagination.page = 1
  loadCodes()
}

// abortController owns the current list request and is aborted when another reload
// starts or the component unmounts.
let abortController: AbortController | null = null

// Dialog flags and selected code records keep deletion, editing, batch updates, and
// single-code copy feedback independent.
const showDeleteDialog = ref(false)
const showDeleteUnusedDialog = ref(false)
const showEditDialog = ref(false)
const showBatchUpdateDialog = ref(false)
const deletingCode = ref<RedeemCode | null>(null)
const editingCode = ref<RedeemCode | null>(null)
const copiedCode = ref<string | null>(null)

// The selection composable tracks code identifiers across table pages and exposes
// visible-row selection and explicit clearing operations.
const {
  selectedSet: selectedCodeIds,
  selectedCount,
  allVisibleSelected,
  select,
  deselect,
  clear: clearSelectedCodes,
  toggleVisible,
} = useTableSelection<RedeemCode>({
  rows: codes,
  getId: (code) => code.id,
})

// Generation drafts include optional custom text and plan identity, code value/count,
// usage limits, payment condition, and expiry.
const generateForm = reactive({
  code: '', // custom code text
  type: 'balance' as RedeemCodeType, // generated benefit type
  value: 10, // redeem benefit amount
  count: 1, // requested generation count
  // A subscription code needs a selected plan before generation or an unused-code
  // update; other code types do not submit this plan value.
  plan_id: null as number | null, // selected plan identifier
  max_uses: 1, // zero permits unlimited uses
  requires_payment: false, // payment eligibility flag
  // An empty generation expiry becomes null and the API wrapper omits it; populated
  // input is parsed into Unix seconds for the create request.
  expires_at_str: '', // local expiry input
})
// hasCustomCode reports whether trimmed custom text exists so the generation controls
// can constrain the requested count.
const hasCustomCode = computed(() => generateForm.code.trim().length > 0)

// The individual edit draft holds value, plan, usage limits, payment condition, and a
// local expiry input.
const editForm = reactive({
  value: 0, // redeem benefit amount
  // A subscription code needs a selected plan before generation or an unused-code
  // update; other code types do not submit this plan value.
  plan_id: null as number | null, // selected plan identifier
  max_uses: 1, // zero permits unlimited uses
  requires_payment: false, // payment eligibility flag
  // An empty individual edit expiry sends zero to clear the stored date; populated
  // input is parsed into Unix seconds for the update request.
  expires_at_str: '', // local expiry input
})

// Batch update flags decide field inclusion independently of field values, allowing
// selected empty notes or cleared expiry to be submitted.
const batchUpdateForm = reactive({
  // update_status, update_expires_at, and update_notes independently include their
  // matching values in the patch; unchecked fields leave the existing server values
  // untouched.
  update_status: false, // include status flag
  status: 'disabled' as 'unused' | 'disabled', // configured lifecycle status
  update_expires_at: false, // include expiry flag
  // Clear mode submits null when expiry editing is enabled; custom mode requires a
  // parsed local timestamp before the patch can be sent.
  expires_mode: 'clear' as 'clear' | 'custom', // expiry clearing mode
  // Custom batch mode rejects empty input when expiry updates are enabled; clear
  // mode ignores this draft and sends null to remove the stored expiry. Valid custom
  // input becomes an ISO timestamp after parsing through Unix seconds.
  expires_at_str: '', // local expiry input
  update_notes: false, // include notes flag
  notes: '', // administrator note text
})

// Invitation codes force single use and no payment condition, while subscription
// codes defer their value to the chosen plan.
watch(
  () => generateForm.type,
  (newType) => {
    if (newType === 'invitation') {
      generateForm.value = 0
      generateForm.max_uses = 1
      generateForm.requires_payment = false
    } else if (newType === 'subscription') {
      generateForm.value = 0
    } else if (generateForm.value === 0) {
      generateForm.value = 10
    }
  },
)

// A supplied custom code names one resulting record, so typing nonempty custom text
// constrains generation count to one.
watch(
  () => generateForm.code,
  (newCode) => {
    if (newCode.trim()) {
      generateForm.count = 1
    }
  },
)

// buildRedeemQueryFilters returns optional type, status, search, and server sort
// conditions. Empty filter values become undefined, giving both listing and export the
// same treatment of unselected conditions and search text.
const buildRedeemQueryFilters = () => ({
  type: (filters.type || undefined) as RedeemCodeType | undefined,
  status: (filters.status || undefined) as
    'active' | 'used' | 'expired' | 'unused' | 'disabled' | undefined,
  search: searchQuery.value || undefined,
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order,
})

// getStatusBadgeClass maps redeem-code lifecycle states to table badge classes.
const getStatusBadgeClass = (status: RedeemCode['status']) => {
  if (status === 'unused') return 'badge-success'
  if (status === 'active') return 'badge-warning'
  if (status === 'used') return 'badge-gray'
  if (status === 'disabled') return 'badge-gray'
  return 'badge-danger'
}

// canEditCode reports whether the code type supports the individual editor.
const canEditCode = (code: RedeemCode) => {
  return ['balance', 'concurrency', 'subscription', 'invitation'].includes(code.type)
}

// toDateTimeLocalString converts a stored timestamp into the local expiry input format.
// Null or empty values produce an empty input, allowing the editor to represent codes
// without an expiry date.
const toDateTimeLocalString = (value: string | null) => {
  if (!value) return ''
  // Convert timestamp milliseconds to epoch seconds before formatting the local
  // expiry control.
  return formatDateTimeLocalInput(Math.floor(new Date(value).getTime() / 1000))
}

// resetGenerateForm returns the draft to one balance code worth ten units and clears
// custom text, plan, and expiry. It also restores single-use limits and disables the
// payment condition for subsequent generation.
const resetGenerateForm = () => {
  generateForm.code = ''
  generateForm.type = 'balance'
  generateForm.value = 10
  generateForm.count = 1
  generateForm.plan_id = null
  generateForm.max_uses = 1
  generateForm.requires_payment = false
  generateForm.expires_at_str = ''
}

// resetBatchUpdateForm disables every batch field and prepares its default values. The
// custom expiry draft starts one day ahead, but it is submitted only if expiry editing
// and custom mode are selected.
const resetBatchUpdateForm = () => {
  batchUpdateForm.update_status = false
  batchUpdateForm.status = 'disabled'
  batchUpdateForm.update_expires_at = false
  batchUpdateForm.expires_mode = 'clear'
  // Prepare tomorrow as the custom-expiry draft; the update flag and mode still
  // decide whether this timestamp is submitted.
  batchUpdateForm.expires_at_str = formatDateTimeLocalInput(
    Math.floor(Date.now() / 1000) + 24 * 60 * 60,
  )
  batchUpdateForm.update_notes = false
  batchUpdateForm.notes = ''
}

// loadCodes loads a redeem-code page after aborting the previous request. The request's
// signal guards success and cancellation errors, keeping aborted loads from publishing
// their rows or reporting failures. A successful load replaces rows and pagination
// totals together. Cleanup clears the shared loading flag only when this controller
// remains current and is not aborted, so an earlier request cannot release a
// replacement request's loading indicator.
const loadCodes = async () => {
  // Abort the previous list request before installing a replacement so its cancelled
  // completion cannot publish rows for obsolete filters.
  if (abortController) {
    abortController.abort()
  }
  const currentController = new AbortController()
  abortController = currentController
  loading.value = true
  try {
    const response = await adminAPI.redeem.list(
      pagination.page,
      pagination.page_size,
      buildRedeemQueryFilters(),
      {
        signal: currentController.signal,
      },
    )
    // Discard an aborted response even if transport resolves it, because replacement
    // or teardown has withdrawn ownership of this list request.
    if (currentController.signal.aborted) {
      return
    }
    codes.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error: any) {
    // Treat cancellation as expected when search or pagination replaces a request,
    // leaving error feedback for a current failed operation.
    if (
      currentController.signal.aborted ||
      error?.name === 'AbortError' ||
      error?.code === 'ERR_CANCELED'
    ) {
      return
    }
    appStore.showError(t('admin.redeem.failedToLoad'))
    console.error('Error loading redeem codes:', error)
  } finally {
    // Loading belongs to the active nonaborted controller; an old finalizer must not
    // release feedback for a newer list request.
    if (abortController === currentController && !currentController.signal.aborted) {
      loading.value = false
      abortController = null
    }
  }
}

// searchTimeout retains the delayed list-search callback so subsequent input and
// unmount can cancel queued work.
let searchTimeout: ReturnType<typeof setTimeout>
// handleSearch replaces the pending search timer with a delayed first-page reload.
// Unmount clears this timer and aborts the list request, preventing queued search work
// from starting after the page closes.
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadCodes()
  }, 300)
}

// handlePageChange selects the requested redeem-code page and starts its reload.
const handlePageChange = (page: number) => {
  pagination.page = page
  loadCodes()
}

// handlePageSizeChange stores the selected page size and reloads from page one.
// Resetting the current page before requesting rows presents the beginning of the
// newly sized result set.
const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadCodes()
}

// handleSort records the selected server field and direction, then reloads from the
// first page. The page reset presents the beginning of the reordered records under
// the current filters.
const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadCodes()
}

// toggleSelectRow applies a row checkbox's checked state to the selected code
// identifiers. Checking adds that identifier and returns immediately; clearing the
// checkbox removes it while leaving other selections unchanged.
const toggleSelectRow = (id: number, event: Event) => {
  const target = event.target as HTMLInputElement
  if (target.checked) {
    select(id)
    return
  }
  deselect(id)
}

// toggleSelectAllVisible forwards the header checkbox state to the table-selection
// composable. The composable receives the current visible code rows, so this action
// changes their selection while retaining identifiers from other pages.
const toggleSelectAllVisible = (event: Event) => {
  const target = event.target as HTMLInputElement
  toggleVisible(target.checked)
}

// openBatchUpdateDialog opens a reset batch draft when codes are selected. With an
// empty selection it shows an informational message and leaves the dialog closed until
// a target exists.
const openBatchUpdateDialog = () => {
  if (selectedCount.value === 0) {
    appStore.showInfo(t('admin.redeem.selectCodesFirst'))
    return
  }
  resetBatchUpdateForm()
  showBatchUpdateDialog.value = true
}

// closeBatchUpdateDialog hides the batch editor while retaining the current selection.
const closeBatchUpdateDialog = () => {
  showBatchUpdateDialog.value = false
}

// buildBatchUpdateFields converts checked batch controls into a sparse request object.
// Unchecked fields are omitted, preserving the distinction between leaving notes
// unchanged and intentionally sending an empty string. Selected expiry clearing sends
// null; custom expiry parses local input and sends an ISO timestamp. Invalid custom
// expiry reports an error and returns null, while selecting no fields also returns null
// for the caller to handle.
const buildBatchUpdateFields = (): BatchUpdateRedeemCodeFields | null => {
  const fields: BatchUpdateRedeemCodeFields = {}

  if (batchUpdateForm.update_status) {
    fields.status = batchUpdateForm.status
  }
  if (batchUpdateForm.update_expires_at) {
    if (batchUpdateForm.expires_mode === 'clear') {
      // A selected clear operation sends null so the batch endpoint can distinguish
      // removing expiry from leaving expiry absent in the patch.
      fields.expires_at = null
    } else {
      const expiresAt = parseDateTimeLocalInput(batchUpdateForm.expires_at_str)
      if (!expiresAt) {
        appStore.showError(t('admin.redeem.expiryDaysRequired'))
        return null
      }
      // The parser returns seconds; convert back to milliseconds to encode the batch
      // expiry field as an ISO timestamp.
      fields.expires_at = new Date(expiresAt * 1000).toISOString()
    }
  }
  if (batchUpdateForm.update_notes) {
    fields.notes = batchUpdateForm.notes
  }

  return Object.keys(fields).length > 0 ? fields : null
}

// handleGenerateCodes validates draft limits and requests one or more redeem codes.
// Custom text is trimmed and counted by Unicode code points, and a subscription
// requires a plan before generation proceeds. A supplied custom code forces count one
// while empty custom text uses the requested count. Success publishes returned codes in
// the result dialog, resets the draft, and reloads the list; failure keeps the draft
// available.
const handleGenerateCodes = async () => {
  if (generateForm.type === 'subscription' && !generateForm.plan_id) {
    appStore.showError(t('admin.announcements.form.selectPackages'))
    return
  }
  if (!Number.isInteger(generateForm.max_uses) || generateForm.max_uses < 0) {
    appStore.showError(t('admin.redeem.maxUsesRequired'))
    return
  }

  const customCode = generateForm.code.trim()
  // Count Unicode code points to match backend rune validation, allowing
  // supplementary characters without treating their UTF-16 surrogate pairs as two
  // characters.
  if ([...customCode].length > 32) {
    appStore.showError(t('admin.redeem.customCodeTooLong'))
    return
  }
  const expiresAt = parseDateTimeLocalInput(generateForm.expires_at_str)

  generating.value = true
  try {
    const result = await adminAPI.redeem.generate(
      customCode ? 1 : generateForm.count,
      generateForm.type,
      generateForm.value,
      generateForm.type === 'subscription' ? generateForm.plan_id : undefined,
      generateForm.max_uses,
      expiresAt,
      customCode || undefined,
      undefined,
      generateForm.requires_payment,
    )
    showGenerateDialog.value = false
    generatedCodes.value = result
    showResultDialog.value = true
    resetGenerateForm()
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToGenerate'))
    console.error('Error generating codes:', error)
  } finally {
    generating.value = false
  }
}

// copyToClipboard copies one code and temporarily identifies it through copiedCode. A
// successful copy schedules feedback removal after the shared duration, while a failed
// copy leaves the feedback state unchanged.
const copyToClipboard = async (text: string) => {
  const success = await clipboardCopy(text, t('admin.redeem.copied'))
  if (success) {
    copiedCode.value = text
    // Clear the successful clipboard marker after the shared feedback interval so a
    // prior copy does not remain marked indefinitely.
    setTimeout(() => {
      copiedCode.value = null
    }, COPY_FEEDBACK_MS)
  }
}

// handleExportCodes downloads CSV records matching the active query filters. After
// receiving the Blob, it dispatches a temporary anchor click and revokes the Blob URL;
// request failures display an export error.
const handleExportCodes = async () => {
  try {
    const blob = await adminAPI.redeem.exportCodes(buildRedeemQueryFilters())

    // Own the temporary CSV link and Blob URL together, removing the element and
    // revoking its URL after initiating the download.
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `redeem-codes-${new Date().toISOString().split('T')[0]}.csv`
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    window.URL.revokeObjectURL(url)

    appStore.showSuccess(t('admin.redeem.codesExported'))
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToExport'))
    console.error('Error exporting codes:', error)
  }
}

// handleEdit copies supported code values into the individual edit dialog. Missing
// plan, payment condition, and expiry receive their editor defaults so the selected
// record can be presented through the shared controls.
const handleEdit = (code: RedeemCode) => {
  editingCode.value = code
  editForm.value = code.value
  editForm.plan_id = code.plan_id ?? null
  editForm.max_uses = code.max_uses
  editForm.requires_payment = code.requires_payment ?? false
  editForm.expires_at_str = toDateTimeLocalString(code.expires_at)
  showEditDialog.value = true
}

// closeEditDialog hides the individual code editor and clears its selected record.
const closeEditDialog = () => {
  showEditDialog.value = false
  editingCode.value = null
}

// handleUpdateCode validates usage limits and constructs an update for the selected
// code. A positive maximum cannot be below observed usage, and unused subscription
// codes require a plan. Value or plan changes are included only before the first use;
// invitation codes force one use and disable the payment condition. Empty expiry sends
// zero to clear it, and successful persistence closes the editor before reloading the
// list.
const handleUpdateCode = async () => {
  const code = editingCode.value
  if (!code) return
  if (!Number.isInteger(editForm.max_uses) || editForm.max_uses < 0) {
    appStore.showError(t('admin.redeem.maxUsesRequired'))
    return
  }
  if (editForm.max_uses > 0 && editForm.max_uses < code.used_count) {
    appStore.showError(t('admin.redeem.maxUsesBelowUsed'))
    return
  }
  if (code.type === 'subscription' && code.used_count === 0 && !editForm.plan_id) {
    appStore.showError(t('admin.announcements.form.selectPackages'))
    return
  }

  const payload: {
    value?: number // redeem benefit amount
    plan_id?: number | null // selected plan identifier
    requires_payment?: boolean // payment eligibility flag
    max_uses?: number // zero permits unlimited uses
    // Individual updates send Unix seconds for a populated date, or zero when the
    // editor input is empty and the stored expiry should be cleared.
    expires_at?: number | null
  } = {
    requires_payment: code.type === 'invitation' ? false : editForm.requires_payment,
    max_uses: code.type === 'invitation' ? 1 : editForm.max_uses,
  }

  // After any use, omit value and plan changes so editing usage conditions cannot
  // rewrite the benefit attached to an already-used code.
  if (code.used_count === 0) {
    if (code.type === 'subscription') {
      payload.plan_id = editForm.plan_id
    } else if (code.type !== 'invitation') {
      payload.value = editForm.value
    }
  }
  // The individual update endpoint uses zero for clearing expiry, unlike the batch
  // request which uses null for that operation.
  payload.expires_at = parseDateTimeLocalInput(editForm.expires_at_str) || 0

  updating.value = true
  try {
    await adminAPI.redeem.update(code.id, payload)
    appStore.showSuccess(t('admin.redeem.codeUpdated'))
    closeEditDialog()
    loadCodes()
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail ||
        error.response?.data?.message ||
        t('admin.redeem.failedToUpdate'),
    )
    console.error('Error updating code:', error)
  } finally {
    updating.value = false
  }
}

// handleDelete selects a redeem code and opens its deletion confirmation.
const handleDelete = (code: RedeemCode) => {
  deletingCode.value = code
  showDeleteDialog.value = true
}

// confirmDelete removes the selected code and reloads after a successful response.
// Missing selection returns immediately, while failure retains the target and displays
// the endpoint detail or translated deletion error.
const confirmDelete = async () => {
  if (!deletingCode.value) return

  try {
    await adminAPI.redeem.delete(deletingCode.value.id)
    appStore.showSuccess(t('admin.redeem.codeDeleted'))
    showDeleteDialog.value = false
    deletingCode.value = null
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToDelete'))
    console.error('Error deleting code:', error)
  }
}

// confirmDeleteUnused requests up to 1000 unused codes and deletes the returned
// identifiers. An empty result closes confirmation with an informational message; this
// operation covers the fetched page and does not paginate through additional unused
// codes.
const confirmDeleteUnused = async () => {
  try {
    // This deletion acts on the first page of unused codes, capped at 1000;
    // additional pages are not fetched by this operation.
    const unusedCodesResponse = await adminAPI.redeem.list(1, 1000, { status: 'unused' })
    const unusedCodeIds = unusedCodesResponse.items.map((code) => code.id)

    if (unusedCodeIds.length === 0) {
      appStore.showInfo(t('admin.redeem.noUnusedCodes'))
      showDeleteUnusedDialog.value = false
      return
    }

    const result = await adminAPI.redeem.batchDelete(unusedCodeIds)
    appStore.showSuccess(t('admin.redeem.codesDeleted', { count: result.deleted }))
    showDeleteUnusedDialog.value = false
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToDeleteUnused'))
    console.error('Error deleting unused codes:', error)
  }
}

// handleBatchUpdate snapshots selected identifiers and validates the checked batch
// fields before submission. A successful request closes the dialog, clears selection,
// and reloads the list; validation or request failure retains the current selection.
const handleBatchUpdate = async () => {
  const ids = Array.from(selectedCodeIds.value)
  if (ids.length === 0) {
    appStore.showInfo(t('admin.redeem.selectCodesFirst'))
    return
  }

  const hasSelectedFields =
    batchUpdateForm.update_status ||
    batchUpdateForm.update_expires_at ||
    batchUpdateForm.update_notes
  if (!hasSelectedFields) {
    appStore.showError(t('admin.redeem.noBatchFieldsSelected'))
    return
  }

  const fields = buildBatchUpdateFields()
  if (!fields) {
    return
  }

  batchUpdating.value = true
  try {
    const result = await adminAPI.redeem.batchUpdate(ids, fields)
    appStore.showSuccess(t('admin.redeem.batchUpdateSuccess', { count: result.updated }))
    showBatchUpdateDialog.value = false
    clearSelectedCodes()
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToBatchUpdate'))
    console.error('Error batch updating codes:', error)
  } finally {
    batchUpdating.value = false
  }
}

// loadSubscriptionPlans loads plan choices used by subscription-code creation and
// editing. Errors are logged while current choices remain available, so a failed plan
// lookup does not replace the redeem-code list.
const loadSubscriptionPlans = async () => {
  try {
    const response = await adminAPI.payment.getPlans()
    subscriptionPlans.value = response.data || []
  } catch (error) {
    // Keep current plan choices after lookup failure so the independently loaded
    // redeem-code table remains available for other code types.
    console.error('Error loading subscription plans:', error)
  }
}

onMounted(() => {
  loadCodes()
  loadSubscriptionPlans()
})

// Cancel pending searches and abort the current list request before teardown can
// leave a delayed list update on the page.
onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
