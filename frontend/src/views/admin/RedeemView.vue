<template>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-wrap items-center gap-3">
          <!-- 左侧：搜索和筛选 -->
          <div class="flex-1 sm:max-w-64">
            <input
              v-model="searchQuery"
              type="text"
              :placeholder="t('admin.redeem.searchCodes')"
              class="input"
              @input="handleSearch"
            />
          </div>
          <Select
            v-model="filters.type"
            :options="filterTypeOptions"
            class="w-36"
            @change="loadCodes"
          />
          <Select
            v-model="filters.status"
            :options="filterStatusOptions"
            class="w-36"
            @change="loadCodes"
          />

          <!-- 右侧：操作按钮 -->
          <div class="flex flex-1 flex-wrap items-center justify-end gap-2">
            <button
              @click="loadCodes"
              :disabled="loading"
              class="btn btn-secondary"
              :title="t('common.refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button @click="handleExportCodes" class="btn btn-secondary">
              {{ t('admin.redeem.exportCsv') }}
            </button>
            <button
              data-testid="batch-update-open"
              @click="openBatchUpdateDialog"
              :disabled="selectedCount === 0 || batchUpdating"
              class="btn btn-secondary"
            >
              <Icon name="edit" size="md" class="mr-2" />
              {{ t('admin.redeem.batchUpdate') }}
            </button>
            <button data-testid="generate-open" @click="showGenerateDialog = true" class="btn btn-primary">
              {{ t('admin.redeem.generateCodes') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
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
              class="h-4 w-4 cursor-pointer rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="allVisibleSelected"
              @click.stop
              @change="toggleSelectAllVisible($event)"
            />
          </template>

          <template #cell-select="{ row }">
            <input
              data-testid="select-code"
              type="checkbox"
              class="h-4 w-4 cursor-pointer rounded border-gray-300 text-primary-600 focus:ring-primary-500"
              :checked="selectedCodeIds.has(row.id)"
              @click.stop
              @change="toggleSelectRow(row.id, $event)"
            />
          </template>

          <template #cell-code="{ value }">
            <div class="flex items-center space-x-2">
              <code class="font-mono text-sm text-gray-900 dark:text-gray-100">{{ value }}</code>
              <button
                @click="copyToClipboard(value)"
                :class="[
                  'flex items-center transition-colors',
                  copiedCode === value
                    ? 'text-green-500'
                    : 'text-gray-400 hover:text-gray-600 dark:hover:text-gray-300'
                ]"
                :title="copiedCode === value ? t('admin.redeem.copied') : t('keys.copyToClipboard')"
              >
                <Icon v-if="copiedCode !== value" name="copy" size="sm" :stroke-width="2" />
                <svg v-else class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M5 13l4 4L19 7"
                  />
                </svg>
              </button>
            </div>
          </template>

          <template #cell-type="{ value }">
            <span
              :class="[
                'badge',
                value === 'balance' || value === 'affiliate_balance'
                  ? 'badge-success'
                  : value === 'subscription'
                    ? 'badge-warning'
                    : 'badge-primary'
              ]"
            >
              {{ t('admin.redeem.types.' + value) }}
            </span>
          </template>

          <template #cell-value="{ value, row }">
            <span class="text-sm font-medium text-gray-900 dark:text-white">
              <template v-if="row.type === 'balance' || row.type === 'affiliate_balance'">
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
            <span
              :class="[
                'badge',
                getStatusBadgeClass(value)
              ]"
            >
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
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-primary-50 hover:text-primary-600 dark:hover:bg-primary-900/20 dark:hover:text-primary-400"
                :title="t('admin.redeem.editCode')"
              >
                <Icon name="edit" size="sm" :stroke-width="2" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                v-if="row.status === 'unused'"
                @click="handleDelete(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              >
                <svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
                  />
                </svg>
                <span class="text-xs">{{ t('common.delete') }}</span>
              </button>
              <span v-if="!canEditCode(row) && row.status !== 'unused'" class="text-gray-400 dark:text-dark-500">-</span>
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

        <!-- 批量操作 -->
        <div
          v-if="selectedCount > 0"
          class="mb-4 flex flex-wrap items-center justify-between gap-3 rounded-lg bg-primary-50 p-3 dark:bg-primary-900/20"
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

    <!-- 删除确认弹窗 -->
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

    <!-- 删除未使用兑换码弹窗 -->
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

    <!-- 生成兑换码弹窗 -->
    <Teleport to="body">
      <div v-if="showGenerateDialog" class="fixed inset-0 z-50 flex items-center justify-center">
        <div class="fixed inset-0 bg-black/50" @click="showGenerateDialog = false"></div>
        <div
          class="relative z-10 w-full max-w-md rounded-xl bg-white p-6 shadow-xl dark:bg-dark-800"
        >
          <h2 class="mb-4 text-lg font-semibold text-gray-900 dark:text-white">
            {{ t('admin.redeem.generateCodesTitle') }}
          </h2>
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
                maxlength="32"
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

            <!-- 余额/并发类型：显示数值输入 -->
            <div v-if="generateForm.type !== 'subscription' && generateForm.type !== 'invitation'">
              <label class="input-label">
                {{
                  generateForm.type === 'balance'
                    ? t('admin.redeem.amount')
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
            <!-- 邀请码类型：显示提示信息 -->
            <div v-if="generateForm.type === 'invitation'" class="rounded-lg bg-blue-50 p-3 dark:bg-blue-900/20">
              <p class="text-sm text-blue-700 dark:text-blue-300">
                {{ t('admin.redeem.invitationHint') }}
              </p>
            </div>
            <!-- 订阅类型：显示套餐选择 -->
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
        </div>
      </div>
    </Teleport>

    <!-- 编辑兑换码弹窗 -->
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
          <input :value="t('admin.redeem.types.' + editingCode.type)" type="text" readonly class="input" />
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
                ? t('admin.redeem.amount')
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

        <div v-if="editingCode && editingCode.used_count === 0 && editingCode.type === 'subscription'">
          <label class="input-label">{{ t('payment.admin.planName') }}</label>
          <Select
            v-model="editForm.plan_id"
            :options="subscriptionPlanOptions"
            :placeholder="t('admin.announcements.form.selectPackages')"
          />
        </div>

        <div v-if="editingCode && editingCode.used_count > 0" class="rounded-lg bg-yellow-50 p-3 dark:bg-yellow-900/20">
          <p class="text-sm text-yellow-700 dark:text-yellow-300">
            {{ t('admin.redeem.valueLockedHint') }}
          </p>
        </div>

        <div>
          <label class="input-label">{{ t('admin.redeem.maxUses') }}</label>
          <input
            v-model.number="editForm.max_uses"
            type="number"
            min="0"
            required
            class="input"
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
          <input v-model="editForm.expires_at_str" type="datetime-local" class="input" />
        </div>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" @click="closeEditDialog" class="btn btn-secondary">
            {{ t('common.cancel') }}
          </button>
          <button type="submit" form="edit-redeem-form" :disabled="updating" class="btn btn-primary">
            {{ updating ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- 批量修改弹窗 -->
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
          <label class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300">
            <input
              data-testid="batch-field-status"
              v-model="batchUpdateForm.update_status"
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
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
          <label class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300">
            <input
              v-model="batchUpdateForm.update_expires_at"
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
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
          <label class="flex items-center gap-2 text-sm font-medium text-gray-700 dark:text-gray-300">
            <input
              data-testid="batch-field-notes"
              v-model="batchUpdateForm.update_notes"
              type="checkbox"
              class="h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
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

    <!-- 生成结果弹窗 -->
    <Teleport to="body">
      <div v-if="showResultDialog" class="fixed inset-0 z-50 flex items-center justify-center p-4">
        <div class="fixed inset-0 bg-black/50" @click="closeResultDialog"></div>
        <div class="relative z-10 w-full max-w-lg rounded-xl bg-white shadow-xl dark:bg-dark-800">
          <!-- 头部 -->
          <div
            class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-dark-600"
          >
            <div class="flex items-center gap-3">
              <div
                class="flex h-10 w-10 items-center justify-center rounded-full bg-green-100 dark:bg-green-900/30"
              >
                <svg
                  class="h-5 w-5 text-green-600 dark:text-green-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M5 13l4 4L19 7"
                  />
                </svg>
              </div>
              <div>
                <h2 class="text-base font-semibold text-gray-900 dark:text-white">
                  {{ t('admin.redeem.generatedSuccessfully') }}
                </h2>
                <p class="text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.redeem.codesCreated', { count: generatedCodes.length }) }}
                </p>
              </div>
            </div>
            <button
              @click="closeResultDialog"
              class="rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-dark-700 dark:hover:text-gray-300"
            >
              <Icon name="x" size="md" :stroke-width="2" />
            </button>
          </div>
          <!-- 内容 -->
          <div class="p-5">
            <div class="relative">
              <textarea
                readonly
                :value="generatedCodesText"
                :style="{ height: textareaHeight }"
                class="w-full resize-none rounded-lg border border-gray-200 bg-gray-50 p-3 font-mono text-sm text-gray-800 focus:outline-none dark:border-dark-600 dark:bg-dark-700 dark:text-gray-200"
              ></textarea>
            </div>
          </div>
          <!-- 底部 -->
          <div
            class="flex justify-end gap-2 rounded-b-xl border-t border-gray-200 bg-gray-50 px-5 py-4 dark:border-dark-600 dark:bg-dark-700/50"
          >
            <button
              @click="copyGeneratedCodes"
              :class="[
                'btn flex items-center gap-2 transition-all',
                copiedAll ? 'btn-success' : 'btn-secondary'
              ]"
            >
              <Icon v-if="!copiedAll" name="copy" size="sm" :stroke-width="2" />
              <svg v-else class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M5 13l4 4L19 7"
                />
              </svg>
              {{ copiedAll ? t('admin.redeem.copied') : t('admin.redeem.copyAll') }}
            </button>
            <button @click="downloadGeneratedCodes" class="btn btn-primary flex items-center gap-2">
              <Icon name="download" size="sm" :stroke-width="2" />
              {{ t('admin.redeem.download') }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useTableSelection } from '@/composables/useTableSelection'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { adminAPI } from '@/api/admin'
import { formatDateTime, formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import type { BatchUpdateRedeemCodeFields, RedeemCode, RedeemCodeType } from '@/types'
import type { SubscriptionPlan } from '@/types/payment'
import type { Column } from '@/components/common/types'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard: clipboardCopy } = useClipboard()
const { formatBalanceAmount } = useBalanceDisplay()

interface PlanOption {
  value: number
  label: string
  description: string | null
  [key: string]: unknown
}

const showGenerateDialog = ref(false)
const showResultDialog = ref(false)
const generatedCodes = ref<RedeemCode[]>([])
const subscriptionPlans = ref<SubscriptionPlan[]>([])

const subscriptionPlanOptions = computed<PlanOption[]>(() =>
  subscriptionPlans.value.map((plan) => ({
    value: plan.id,
    label: plan.name,
    description: plan.description
  }))
)

const generatedCodesText = computed(() => {
  return generatedCodes.value.map((code) => code.code).join('\n')
})

const textareaHeight = computed(() => {
  const lineCount = generatedCodes.value.length
  const lineHeight = 24 // 近似行高，单位 px
  const padding = 24 // 上下内边距
  const minHeight = 60
  const maxHeight = 240
  const calculatedHeight = Math.min(
    Math.max(lineCount * lineHeight + padding, minHeight),
    maxHeight
  )
  return `${calculatedHeight}px`
})

const copiedAll = ref(false)

const closeResultDialog = () => {
  showResultDialog.value = false
  generatedCodes.value = []
  copiedAll.value = false
}

const copyGeneratedCodes = async () => {
  const success = await clipboardCopy(generatedCodesText.value, t('admin.redeem.copied'))
  if (success) {
    copiedAll.value = true
    setTimeout(() => {
      copiedAll.value = false
    }, 2000)
  }
}

const downloadGeneratedCodes = () => {
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
  { key: 'actions', label: t('admin.redeem.columns.actions') }
])

const typeOptions = computed(() => [
  { value: 'balance', label: t('admin.redeem.balance') },
  { value: 'concurrency', label: t('admin.redeem.concurrency') },
  { value: 'subscription', label: t('admin.redeem.subscription') },
  { value: 'invitation', label: t('admin.redeem.invitation') }
])

const filterTypeOptions = computed(() => [
  { value: '', label: t('admin.redeem.allTypes') },
  { value: 'balance', label: t('admin.redeem.balance') },
  { value: 'affiliate_balance', label: t('admin.redeem.types.affiliate_balance') },
  { value: 'concurrency', label: t('admin.redeem.concurrency') },
  { value: 'subscription', label: t('admin.redeem.subscription') },
  { value: 'invitation', label: t('admin.redeem.invitation') }
])

const filterStatusOptions = computed(() => [
  { value: '', label: t('admin.redeem.allStatus') },
  { value: 'unused', label: t('admin.redeem.unused') },
  { value: 'active', label: t('admin.redeem.status.active') },
  { value: 'used', label: t('admin.redeem.used') },
  { value: 'expired', label: t('admin.redeem.status.expired') },
  { value: 'disabled', label: t('admin.redeem.status.disabled') }
])

const batchStatusOptions = computed(() => [
  { value: 'unused', label: t('admin.redeem.status.unused') },
  { value: 'disabled', label: t('admin.redeem.status.disabled') }
])

const batchExpiryModeOptions = computed(() => [
  { value: 'clear', label: t('admin.redeem.neverExpires') },
  { value: 'custom', label: t('admin.redeem.customExpiry') }
])

const codes = ref<RedeemCode[]>([])
const loading = ref(false)
const generating = ref(false)
const updating = ref(false)
const batchUpdating = ref(false)
const searchQuery = ref('')
const filters = reactive({
  type: '',
  status: ''
})
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})
const sortState = reactive({
  sort_by: 'id',
  sort_order: 'desc' as 'asc' | 'desc'
})

let abortController: AbortController | null = null

const showDeleteDialog = ref(false)
const showDeleteUnusedDialog = ref(false)
const showEditDialog = ref(false)
const showBatchUpdateDialog = ref(false)
const deletingCode = ref<RedeemCode | null>(null)
const editingCode = ref<RedeemCode | null>(null)
const copiedCode = ref<string | null>(null)

const {
  selectedSet: selectedCodeIds,
  selectedCount,
  allVisibleSelected,
  select,
  deselect,
  clear: clearSelectedCodes,
  toggleVisible
} = useTableSelection<RedeemCode>({
  rows: codes,
  getId: (code) => code.id
})

const generateForm = reactive({
  code: '',
  type: 'balance' as RedeemCodeType,
  value: 10,
  count: 1,
  plan_id: null as number | null,
  max_uses: 1,
  expires_at_str: ''
})
const hasCustomCode = computed(() => generateForm.code.trim().length > 0)

const editForm = reactive({
  value: 0,
  plan_id: null as number | null,
  max_uses: 1,
  expires_at_str: ''
})

const batchUpdateForm = reactive({
  update_status: false,
  status: 'disabled' as 'unused' | 'disabled',
  update_expires_at: false,
  expires_mode: 'clear' as 'clear' | 'custom',
  expires_at_str: '',
  update_notes: false,
  notes: ''
})

// 监听类型变化，邀请码类型固定为单次使用，但仍允许设置兑换码自身过期时间。
watch(
  () => generateForm.type,
  (newType) => {
    if (newType === 'invitation') {
      generateForm.value = 0
      generateForm.max_uses = 1
    } else if (newType === 'subscription') {
      generateForm.value = 0
    } else if (generateForm.value === 0) {
      generateForm.value = 10
    }
  }
)

watch(
  () => generateForm.code,
  (newCode) => {
    if (newCode.trim()) {
      generateForm.count = 1
    }
  }
)

const buildRedeemQueryFilters = () => ({
  type: (filters.type || undefined) as RedeemCodeType | undefined,
  status: (filters.status || undefined) as
    | 'active'
    | 'used'
    | 'expired'
    | 'unused'
    | 'disabled'
    | undefined,
  search: searchQuery.value || undefined,
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order
})

const getStatusBadgeClass = (status: RedeemCode['status']) => {
  if (status === 'unused') return 'badge-success'
  if (status === 'active') return 'badge-warning'
  if (status === 'used') return 'badge-gray'
  if (status === 'disabled') return 'badge-gray'
  return 'badge-danger'
}

const canEditCode = (code: RedeemCode) => {
  return ['balance', 'concurrency', 'subscription', 'invitation'].includes(code.type)
}

const toDateTimeLocalString = (value: string | null) => {
  if (!value) return ''
  return formatDateTimeLocalInput(Math.floor(new Date(value).getTime() / 1000))
}

const resetGenerateForm = () => {
  generateForm.code = ''
  generateForm.type = 'balance'
  generateForm.value = 10
  generateForm.count = 1
  generateForm.plan_id = null
  generateForm.max_uses = 1
  generateForm.expires_at_str = ''
}

const resetBatchUpdateForm = () => {
  batchUpdateForm.update_status = false
  batchUpdateForm.status = 'disabled'
  batchUpdateForm.update_expires_at = false
  batchUpdateForm.expires_mode = 'clear'
  batchUpdateForm.expires_at_str = formatDateTimeLocalInput(
    Math.floor(Date.now() / 1000) + 24 * 60 * 60
  )
  batchUpdateForm.update_notes = false
  batchUpdateForm.notes = ''
}

const loadCodes = async () => {
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
        signal: currentController.signal
      }
    )
    if (currentController.signal.aborted) {
      return
    }
    codes.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error: any) {
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
    if (abortController === currentController && !currentController.signal.aborted) {
      loading.value = false
      abortController = null
    }
  }
}

let searchTimeout: ReturnType<typeof setTimeout>
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadCodes()
  }, 300)
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadCodes()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadCodes()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadCodes()
}

const toggleSelectRow = (id: number, event: Event) => {
  const target = event.target as HTMLInputElement
  if (target.checked) {
    select(id)
    return
  }
  deselect(id)
}

const toggleSelectAllVisible = (event: Event) => {
  const target = event.target as HTMLInputElement
  toggleVisible(target.checked)
}

const openBatchUpdateDialog = () => {
  if (selectedCount.value === 0) {
    appStore.showInfo(t('admin.redeem.selectCodesFirst'))
    return
  }
  resetBatchUpdateForm()
  showBatchUpdateDialog.value = true
}

const closeBatchUpdateDialog = () => {
  showBatchUpdateDialog.value = false
}

const buildBatchUpdateFields = (): BatchUpdateRedeemCodeFields | null => {
  const fields: BatchUpdateRedeemCodeFields = {}

  if (batchUpdateForm.update_status) {
    fields.status = batchUpdateForm.status
  }
  if (batchUpdateForm.update_expires_at) {
    if (batchUpdateForm.expires_mode === 'clear') {
      fields.expires_at = null
    } else {
      const expiresAt = parseDateTimeLocalInput(batchUpdateForm.expires_at_str)
      if (!expiresAt) {
        appStore.showError(t('admin.redeem.expiryDaysRequired'))
        return null
      }
      fields.expires_at = new Date(expiresAt * 1000).toISOString()
    }
  }
  if (batchUpdateForm.update_notes) {
    fields.notes = batchUpdateForm.notes
  }

  return Object.keys(fields).length > 0 ? fields : null
}

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
      customCode || undefined
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

const copyToClipboard = async (text: string) => {
  const success = await clipboardCopy(text, t('admin.redeem.copied'))
  if (success) {
    copiedCode.value = text
    setTimeout(() => {
      copiedCode.value = null
    }, 2000)
  }
}

const handleExportCodes = async () => {
  try {
    const blob = await adminAPI.redeem.exportCodes(buildRedeemQueryFilters())

    // 创建下载链接
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

const handleEdit = (code: RedeemCode) => {
  editingCode.value = code
  editForm.value = code.value
  editForm.plan_id = code.plan_id ?? null
  editForm.max_uses = code.max_uses
  editForm.expires_at_str = toDateTimeLocalString(code.expires_at)
  showEditDialog.value = true
}

const closeEditDialog = () => {
  showEditDialog.value = false
  editingCode.value = null
}

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
    value?: number
    plan_id?: number | null
    max_uses?: number
    expires_at?: number | null
  } = {
    max_uses: code.type === 'invitation' ? 1 : editForm.max_uses
  }

  if (code.used_count === 0) {
    if (code.type === 'subscription') {
      payload.plan_id = editForm.plan_id
    } else if (code.type !== 'invitation') {
      payload.value = editForm.value
    }
  }
  payload.expires_at = parseDateTimeLocalInput(editForm.expires_at_str) || 0

  updating.value = true
  try {
    await adminAPI.redeem.update(code.id, payload)
    appStore.showSuccess(t('admin.redeem.codeUpdated'))
    closeEditDialog()
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || error.response?.data?.message || t('admin.redeem.failedToUpdate'))
    console.error('Error updating code:', error)
  } finally {
    updating.value = false
  }
}

const handleDelete = (code: RedeemCode) => {
  deletingCode.value = code
  showDeleteDialog.value = true
}

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

const confirmDeleteUnused = async () => {
  try {
    // 拉取当前未使用兑换码并批量删除。
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

const loadSubscriptionPlans = async () => {
  try {
    const response = await adminAPI.payment.getPlans()
    subscriptionPlans.value = response.data || []
  } catch (error) {
    console.error('Error loading subscription plans:', error)
  }
}

onMounted(() => {
  loadCodes()
  loadSubscriptionPlans()
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
