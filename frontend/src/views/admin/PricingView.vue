<template>
  <AppLayout>
    <div class="mb-4 flex gap-2 border-b border-gray-200 dark:border-dark-700" role="tablist" :aria-label="t('admin.pricing.title')">
      <button v-for="tab in ['configs', 'defaults'] as const" :key="tab" class="px-4 py-3 text-sm font-medium border-b-2" :class="pageTab === tab ? 'border-primary-500 text-primary-600' : 'border-transparent text-gray-500'" role="tab" :aria-selected="pageTab === tab" @click="pageTab = tab">{{ t(`admin.pricing.tabs.${tab}`) }}</button>
    </div>
    <DefaultPricingPanel v-if="pageTab === 'defaults'" />
    <TablePageLayout v-show="pageTab === 'configs'">
      <template #filters>
        <div class="flex flex-wrap items-center gap-2">
          <!-- Left: Search + Filters -->
          <div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon
                name="search"
                size="md"
                class="input-icon text-gray-400 dark:text-gray-500"
              />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="t('admin.pricing.searchPricingConfigs', 'Search price configurations...')"
                class="input input-has-icon"
                @input="handleSearch"
              />
            </div>

            <Select
              v-model="filters.status"
              :options="statusFilterOptions"
              :placeholder="t('admin.pricing.allStatus', 'All Status')"
              class="w-32 shrink-0"
              @change="loadPricingConfigs"
            />
          </div>

          <!-- Right: Actions -->
          <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
            <button
              @click="loadPricingConfigs"
              :disabled="loading"
              class="btn btn-secondary shrink-0 btn-icon"
              :title="t('common.refresh', 'Refresh')"
            >
              <Icon name="refresh" size="md" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button @click="openCreateDialog" class="btn btn-primary whitespace-nowrap px-3 sm:px-4">
              <Icon name="plus" size="md" class="mr-2" />
              {{ t('admin.pricing.createPricingConfig', 'Create price configuration') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="pricingConfigs"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-name="{ value }">
            <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
          </template>

          <template #cell-description="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-400">{{ value || '-' }}</span>
          </template>

          <template #cell-status="{ row }">
            <Toggle
              :modelValue="row.status === 'active'"
              @update:modelValue="togglePricingConfigStatus(row)"
            />
          </template>

          <template #cell-group_count="{ row }">
            <span
              class="inline-flex items-center rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
            >
              {{ (row.group_ids || []).length }}
              {{ t('admin.pricing.groupsUnit', 'groups') }}
            </span>
          </template>

          <template #cell-pricing_count="{ row }">
            <span
              class="inline-flex items-center rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-800 dark:bg-dark-600 dark:text-gray-300"
            >
              {{ (row.model_pricing || []).length }}
              {{ t('admin.pricing.pricingUnit', 'pricing rules') }}
            </span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-600 dark:text-gray-400">
              {{ formatDate(value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <button
                @click="openEditDialog(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit', 'Edit') }}</span>
              </button>
              <button
                @click="handleDelete(row)"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              >
                <Icon name="trash" size="sm" />
                <span class="text-xs">{{ t('common.delete', 'Delete') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.pricing.noPricingConfigsYet', 'No price configurations yet')"
              :description="t('admin.pricing.createFirstPricingConfig', 'Create your first price configuration to manage model pricing')"
              :action-text="t('admin.pricing.createPricingConfig', 'Create price configuration')"
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

    <!-- Create/Edit Dialog -->
    <BaseDialog
      :show="showDialog"
      :title="editingPricingConfig ? t('admin.pricing.editPricingConfig', 'Edit price configuration') : t('admin.pricing.createPricingConfig', 'Create price configuration')"
      width="extra-wide"
      @close="closeDialog"
    >
      <div class="pricing-dialog-body">
        <!-- Tab Bar -->
        <div class="flex items-center border-b border-gray-200 dark:border-dark-700 flex-shrink-0 -mx-4 sm:-mx-6 px-4 sm:px-6 -mt-3 sm:-mt-4">
          <!-- Basic Settings Tab -->
          <button
            type="button"
            @click="activeTab = 'basic'"
            class="pricing-tab"
            :class="activeTab === 'basic' ? 'pricing-tab-active' : 'pricing-tab-inactive'"
          >
            {{ t('admin.pricing.form.basicSettings', '基础设置') }}
          </button>
          <!-- Platform Tabs (only enabled) -->
          <button
            v-for="section in form.platforms.filter(s => s.enabled)"
            :key="section.platform"
            type="button"
            @click="activeTab = section.platform"
            class="pricing-tab group"
            :class="activeTab === section.platform ? 'pricing-tab-active' : 'pricing-tab-inactive'"
          >
            <PlatformIcon :platform="section.platform" size="xs" :class="platformTextClass(section.platform)" />
            <span :class="platformTextClass(section.platform)">{{ t('admin.groups.platforms.' + section.platform, section.platform) }}</span>
          </button>
        </div>

        <!-- Tab Content -->
        <form id="pricing-form" @submit.prevent="handleSubmit" class="flex-1 overflow-y-auto pt-4">
          <!-- Basic Settings Tab -->
          <div v-show="activeTab === 'basic'" class="space-y-5">
            <!-- Name -->
            <div>
              <label class="input-label">{{ t('admin.pricing.form.name', 'Name') }} <span class="text-red-500">*</span></label>
              <input
                v-model="form.name"
                type="text"
                required
                class="input"
                :placeholder="t('admin.pricing.form.namePlaceholder', 'Enter price configuration name')"
              />
            </div>

            <!-- Description -->
            <div>
              <label class="input-label">{{ t('admin.pricing.form.description', 'Description') }}</label>
              <textarea
                v-model="form.description"
                rows="2"
                class="input"
                :placeholder="t('admin.pricing.form.descriptionPlaceholder', 'Optional description')"
              ></textarea>
            </div>

            <!-- Status (edit only) -->
            <div v-if="editingPricingConfig">
              <label class="input-label">{{ t('admin.pricing.form.status', 'Status') }}</label>
              <Select v-model="form.status" :options="statusEditOptions" />
            </div>

            <!-- Billing Basis -->
            <div>
              <label class="input-label">{{ t('admin.pricing.form.billingModelSource', 'Billing model source') }}</label>
              <Select v-model="form.billing_model_source" :options="billingModelSourceOptions" />
              <p class="mt-1 text-xs text-gray-400" data-testid="billing-model-source-hint">
                {{ billingModelSourceHint }}
              </p>
            </div>

            <!-- Platform Management -->
            <div class="space-y-3">
              <label class="input-label mb-0">{{ t('admin.pricing.form.platformConfig', '平台配置') }}</label>
              <div class="flex flex-wrap gap-2">
                <label
                  v-for="p in platformOrder"
                  :key="p"
                  class="inline-flex cursor-pointer items-center gap-1.5 rounded-control border px-3 py-1.5 text-sm transition-colors"
                  :class="activePlatforms.includes(p)
                    ? 'bg-primary-50 border-primary-300 dark:bg-primary-900/20 dark:border-primary-700'
                    : 'border-gray-200 hover:bg-gray-50 dark:border-dark-600 dark:hover:bg-dark-700'"
                >
                  <input
                    type="checkbox"
                    :checked="activePlatforms.includes(p)"
                    class="h-3.5 w-3.5 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                    @change="togglePlatform(p)"
                  />
                  <PlatformIcon :platform="p" size="xs" :class="platformTextClass(p)" />
                  <span :class="platformTextClass(p)">{{ t('admin.groups.platforms.' + p, p) }}</span>
                </label>
              </div>
            </div>

            <!-- Apply Pricing to Account Stats (toggle only in basic settings) -->
            <div class="border-t border-gray-200 pt-4 dark:border-dark-700">
              <div class="flex items-center justify-between">
                <div>
                  <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
                    {{ t('admin.pricing.form.applyPricingToAccountStats') }}
                  </label>
                  <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.pricing.form.applyPricingToAccountStatsDesc') }}
                  </p>
                </div>
                <Toggle
                  :modelValue="form.apply_pricing_to_account_stats"
                  @update:modelValue="form.apply_pricing_to_account_stats = $event"
                />
              </div>
            </div>
          </div>

          <!-- Platform Tab Content -->
          <div
            v-for="(section, sIdx) in form.platforms"
            :key="'tab-' + section.platform"
            v-show="section.enabled && activeTab === section.platform"
            class="space-y-4"
          >
            <!-- Groups -->
            <div>
              <label class="input-label text-xs">
                {{ t('admin.pricing.form.groups', 'Associated Groups') }} <span class="text-red-500">*</span>
                <span v-if="section.group_ids.length > 0" class="ml-1 font-normal text-gray-400">
                  ({{ t('admin.pricing.form.selectedCount', { count: section.group_ids.length }, `已选 ${section.group_ids.length} 个`) }})
                </span>
              </label>
              <div class="max-h-40 overflow-auto rounded-control border border-gray-200 bg-gray-50 p-2 dark:border-dark-600 dark:bg-dark-900">
                <div v-if="groupsLoading" class="py-2 text-center text-xs text-gray-500">
                  {{ t('common.loading', 'Loading...') }}
                </div>
                <div v-else-if="getGroupsForPlatform(section.platform).length === 0" class="py-2 text-center text-xs text-gray-500">
                  {{ t('admin.pricing.form.noGroupsAvailable', 'No groups available') }}
                </div>
                <div v-else class="flex flex-wrap gap-1">
                  <label
                    v-for="group in getGroupsForPlatform(section.platform)"
                    :key="group.id"
                    class="inline-flex cursor-pointer items-center gap-1.5 rounded-compact border border-gray-200 px-2 py-1 text-xs transition-colors hover:bg-gray-50 dark:border-dark-600 dark:hover:bg-dark-700"
                    :class="[
                      section.group_ids.includes(group.id) ? 'bg-primary-50 border-primary-300 dark:bg-primary-900/20 dark:border-primary-700' : '',
                      isGroupInOtherPricingConfig(group.id, section.platform) ? 'opacity-40' : ''
                    ]"
                  >
                    <input
                      type="checkbox"
                      :checked="section.group_ids.includes(group.id)"
                      :disabled="isGroupInOtherPricingConfig(group.id, section.platform)"
                      class="h-3 w-3 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                      @change="toggleGroupInSection(sIdx, group.id)"
                    />
                    <span :class="['font-medium', platformTextClass(group.platform)]">{{ group.name }}</span>
                    <span
                      :class="['rounded-full px-1 py-0 text-xs', platformBadgeLightClass(group.platform)]"
                    >{{ group.rate_multiplier }}x</span>
                    <span class="text-xs text-gray-400">{{ group.account_count || 0 }}</span>
                    <span
                      v-if="isGroupInOtherPricingConfig(group.id, section.platform)"
                      class="text-xs text-gray-400"
                    >{{ getGroupInOtherPricingConfigLabel(group.id) }}</span>
                  </label>
                </div>
              </div>
            </div>

            <!-- Model Pricing -->
            <div>
              <div class="mb-1 flex items-center justify-between">
                <label class="input-label text-xs mb-0">{{ t('admin.pricing.form.modelPricing', 'Model Pricing') }}</label>
                <div class="flex items-center gap-2">
                  <button
                    type="button"
                    @click="syncLatestModels(sIdx)"
                    :disabled="syncingPlatform === section.platform"
                    class="text-xs text-gray-500 hover:text-primary-600 disabled:opacity-50"
                  >
                    {{ syncingPlatform === section.platform ? t('admin.pricing.form.syncingModels') : t('admin.pricing.form.syncLatestModels') }}
                  </button>
                  <button type="button" @click="addPricingEntry(sIdx)" class="text-xs text-primary-600 hover:text-primary-700">
                    + {{ t('common.add', 'Add') }}
                  </button>
                </div>
              </div>
              <div
                v-if="section.model_pricing.length === 0"
                class="rounded-compact border border-dashed border-gray-300 p-2 text-center text-xs text-gray-400 dark:border-dark-500"
              >
                {{ t('admin.pricing.form.noPricingRules', 'No pricing rules yet. Click "Add" to create one.') }}
              </div>
              <div v-else class="space-y-2">
                <PricingEntryCard
                  v-for="(entry, idx) in section.model_pricing"
                  :key="idx"
                  :entry="entry"
                  :platform="section.platform"
                  enable-time-pricing
                  enable-tier-multipliers
                  @update="updatePricingEntry(sIdx, idx, $event)"
                  @remove="removePricingEntry(sIdx, idx)"
                />
              </div>
            </div>

            <!-- Account Stats Pricing Rules (per-platform, always visible) -->
            <div class="mt-4 border-t border-gray-200 pt-4 dark:border-dark-700 space-y-3">
              <div class="flex items-center justify-between">
                <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300">
                  {{ t('admin.pricing.form.accountStatsPricingRules') }}
                </h4>
                <button
                  type="button"
                  @click="addAccountStatsRule(sIdx)"
                  class="rounded-control border border-primary-300 px-3 py-1 text-xs font-medium text-primary-600 hover:bg-primary-50 dark:border-primary-600 dark:text-primary-400 dark:hover:bg-primary-900/20"
                >
                  + {{ t('admin.pricing.form.addRule') }}
                </button>
              </div>

              <!-- Filter rules for this platform's groups -->
              <p
                v-if="section.account_stats_pricing_rules.length === 0"
                class="text-xs italic text-gray-400 dark:text-gray-500"
              >
                {{ t('admin.pricing.form.noRulesConfigured') }}
              </p>

              <div
                v-for="(rule, ruleIndex) in section.account_stats_pricing_rules"
                :key="ruleIndex"
                class="space-y-3 rounded-control border border-gray-200 p-4 dark:border-dark-600"
              >
                <div class="flex items-center justify-between">
                  <input
                    v-model="rule.name"
                    :placeholder="t('admin.pricing.form.ruleName')"
                    class="bg-transparent text-sm font-medium text-gray-700 placeholder-gray-400 outline-none dark:text-gray-300"
                  />
                  <button type="button" @click="removeAccountStatsRule(sIdx, ruleIndex)" class="text-xs text-red-500 hover:text-red-700">
                    {{ t('common.delete') }}
                  </button>
                </div>

                <div>
                  <label class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.pricing.form.ruleGroups') }}</label>
                  <div class="mt-1 flex flex-wrap gap-1">
                    <label
                      v-for="gid in section.group_ids"
                      :key="gid"
                      class="inline-flex cursor-pointer items-center gap-1 rounded-compact border px-2 py-1 text-xs transition-colors"
                      :class="rule.group_ids.includes(gid)
                        ? 'border-primary-300 bg-primary-50 dark:border-primary-700 dark:bg-primary-900/20'
                        : 'border-gray-200 hover:bg-gray-50 dark:border-dark-600 dark:hover:bg-dark-700'"
                    >
                      <input type="checkbox" :checked="rule.group_ids.includes(gid)" class="h-3 w-3 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500" @change="rule.group_ids.includes(gid) ? rule.group_ids.splice(rule.group_ids.indexOf(gid), 1) : rule.group_ids.push(gid)" />
                      <span :class="['font-medium', platformTextClass(section.platform)]">{{ getGroupNameById(gid) }}</span>
                    </label>
                  </div>
                  <p v-if="section.group_ids.length === 0" class="mt-1 text-xs text-gray-400">
                    {{ t('admin.pricing.form.noGroupsInPricingConfig') }}
                  </p>
                </div>

                <div>
                  <label class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.pricing.form.ruleAccounts') }}</label>
                  <!-- Selected account chips -->
                  <div class="mt-1 flex flex-wrap gap-1">
                    <span
                      v-for="accountId in rule.account_ids"
                      :key="accountId"
                      class="inline-flex items-center gap-1 rounded-compact border border-primary-300 bg-primary-50 px-2 py-0.5 text-xs dark:border-primary-700 dark:bg-primary-900/20"
                    >
                      <span :class="['font-medium', platformTextClass(section.platform)]">{{ getRuleAccountLabel(accountId) }}</span>
                      <button type="button" @click="removeRuleAccount(rule, accountId)" class="text-gray-400 hover:text-red-500">
                        <Icon name="x" size="xs" />
                      </button>
                    </span>
                  </div>
                  <!-- Account search input -->
                  <div class="relative mt-1 rule-account-search-container">
                    <input
                      v-model="ruleAccountSearchKeyword[`${section.platform}-${ruleIndex}`]"
                      type="text"
                      class="input text-sm"
                      :placeholder="t('admin.pricing.form.searchAccountPlaceholder')"
                      @input="onRuleAccountSearchInput(section.platform, ruleIndex)"
                      @focus="onRuleAccountSearchFocus(section.platform, ruleIndex)"
                    />
                    <!-- Search results dropdown -->
                    <div
                      v-if="showRuleAccountDropdown[`${section.platform}-${ruleIndex}`] && (ruleAccountSearchResults[`${section.platform}-${ruleIndex}`]?.length ?? 0) > 0"
                      class="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-control border bg-white shadow-lg dark:border-dark-600 dark:bg-dark-800"
                    >
                      <button
                        v-for="account in ruleAccountSearchResults[`${section.platform}-${ruleIndex}`]"
                        :key="account.id"
                        type="button"
                        @click="selectRuleAccount(rule, account, section.platform, ruleIndex)"
                        class="dropdown-item-sm"
                        :class="{ 'opacity-50': rule.account_ids.includes(account.id) }"
                        :disabled="rule.account_ids.includes(account.id)"
                      >
                        <span :class="platformTextClass(account.platform)">{{ account.name }}</span>
                        <span class="text-xs text-gray-400">#{{ account.id }}</span>
                      </button>
                    </div>
                  </div>
                  <p class="mt-1 text-xs text-gray-400">
                    {{ t('admin.pricing.form.ruleAccountsHint') }}
                  </p>
                </div>

                <div>
                  <div class="mb-1 flex items-center justify-between">
                    <label class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.pricing.form.ruleModelPricing') }}</label>
                    <button type="button" @click="addRulePricingEntry(sIdx, ruleIndex)" class="text-xs text-primary-600 hover:text-primary-700">
                      + {{ t('common.add') }}
                    </button>
                  </div>
                  <div v-if="rule.pricing.length === 0" class="rounded-compact border border-dashed border-gray-300 p-2 text-center text-xs text-gray-400 dark:border-dark-500">
                    {{ t('admin.pricing.form.noPricingRules') }}
                  </div>
                  <div v-else class="space-y-2">
                    <PricingEntryCard
                      v-for="(entry, pIdx) in rule.pricing"
                      :key="pIdx"
                      :entry="entry"
                      :platform="section.platform"
                      @update="rule.pricing.splice(pIdx, 1, $event)"
                      @remove="removeRulePricingEntry(sIdx, ruleIndex, pIdx)"
                    />
                  </div>
                </div>
              </div>
            </div>
          </div>
        </form>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeDialog" type="button" class="btn btn-secondary">
            {{ t('common.cancel', 'Cancel') }}
          </button>
          <button
            type="submit"
            form="pricing-form"
            :disabled="submitting"
            class="btn btn-primary"
          >
            {{ submitting
              ? t('common.submitting', 'Submitting...')
              : editingPricingConfig
                ? t('common.update', 'Update')
                : t('common.create', 'Create')
            }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.pricing.deletePricingConfig', 'Delete price configuration')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete', 'Delete')"
      :cancel-text="t('common.cancel', 'Cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'
import { adminAPI } from '@/api/admin'
import type { PricingConfig, ModelPricingEntry, CreatePricingConfigRequest, UpdatePricingConfigRequest, AccountStatsPricingRule } from '@/api/admin/pricing'
import type { PricingFormEntry } from '@/components/admin/pricing/types'
import { pricingEntryFromAPI, pricingEntryToAPI, validatePricingForm } from '@/components/admin/pricing/pricingForm'
import { apiIntervalsToForm, createDefaultTimePricingForm, formIntervalsToAPI, hasExplicitPricing, mTokToPerToken, perTokenToMTok, toNullableNumber } from '@/components/admin/pricing/types'
import type { AdminGroup, GroupPlatform } from '@/types'
import type { Column } from '@/components/common/types'
import { platformTextClass, platformBadgeLightClass } from '@/utils/platformColors'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Toggle from '@/components/common/Toggle.vue'
import DefaultPricingPanel from '@/components/admin/pricing/DefaultPricingPanel.vue'
import PricingEntryCard from '@/components/admin/pricing/PricingEntryCard.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useKeyedDebouncedSearch } from '@/composables/useKeyedDebouncedSearch'

const { t } = useI18n()
const pageTab = ref<'configs' | 'defaults'>('configs')
const appStore = useAppStore()

// Web Search global enabled state (loaded once on mount)

// ── Form-level pricing rule type (per-platform) ──
interface FormPricingRule {
  name: string
  group_ids: number[]
  account_ids: number[]
  pricing: PricingFormEntry[]
}

// ── Platform Section type ──
interface PlatformSection {
  platform: GroupPlatform
  enabled: boolean
  collapsed: boolean
  group_ids: number[]

  model_pricing: PricingFormEntry[]

  account_stats_pricing_rules: FormPricingRule[]
}

// ── Table columns ──
const columns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.pricing.columns.name', 'Name'), sortable: true },
  { key: 'description', label: t('admin.pricing.columns.description', 'Description'), sortable: false },
  { key: 'status', label: t('admin.pricing.columns.status', 'Status'), sortable: true },
  { key: 'group_count', label: t('admin.pricing.columns.groups', 'Groups'), sortable: false },
  { key: 'pricing_count', label: t('admin.pricing.columns.pricing', 'Pricing'), sortable: false },
  { key: 'created_at', label: t('admin.pricing.columns.createdAt', 'Created'), sortable: true },
  { key: 'actions', label: t('admin.pricing.columns.actions', 'Actions'), sortable: false }
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.pricing.allStatus', 'All Status') },
  { value: 'active', label: t('admin.pricing.statusActive', 'Active') },
  { value: 'disabled', label: t('admin.pricing.statusDisabled', 'Disabled') }
])

const statusEditOptions = computed(() => [
  { value: 'active', label: t('admin.pricing.statusActive', 'Active') },
  { value: 'disabled', label: t('admin.pricing.statusDisabled', 'Disabled') }
])

const billingModelSourceOptions = computed(() => [
  { value: 'group_mapped', label: t('admin.pricing.form.billingModelSourceGroupMapped', 'Group-mapped model (default)') },
  { value: 'requested', label: t('admin.pricing.form.billingModelSourceRequested', 'Client request model') },
  { value: 'upstream', label: t('admin.pricing.form.billingModelSourceUpstream', 'Account final upstream model') }
])

// ── State ──
const pricingConfigs = ref<PricingConfig[]>([])
const loading = ref(false)
const searchQuery = ref('')
const filters = reactive({ status: '' })
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0
})
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

// Dialog state
const showDialog = ref(false)
const editingPricingConfig = ref<PricingConfig | null>(null)
const submitting = ref(false)
const showDeleteDialog = ref(false)
const deletingPricingConfig = ref<PricingConfig | null>(null)
const activeTab = ref<string>('basic')

// Groups
const allGroups = ref<AdminGroup[]>([])
const groupsLoading = ref(false)

// 关联冲突检查使用独立的价格配置列表，不受当前分页影响。
const allPricingConfigsForConflict = ref<PricingConfig[]>([])

// Form data
const form = reactive({
  name: '',
  description: '',
  status: 'active',

  billing_model_source: 'group_mapped' as string,
  platforms: [] as PlatformSection[],
  apply_pricing_to_account_stats: false,
})

// 计费模型来源只决定查价口径。
const billingModelSourceHint = computed(() => {
  switch (form.billing_model_source) {
    case 'requested':
      return t('admin.pricing.form.billingModelSourceHintRequested')
    case 'upstream':
      return t('admin.pricing.form.billingModelSourceHintUpstream')
    default:
      return t('admin.pricing.form.billingModelSourceHintGroupMapped')
  }
})

let abortController: AbortController | null = null

// ── Platform config ──
const platformOrder: GroupPlatform[] = ['anthropic', 'openai', 'gemini', 'antigravity', 'qoder', 'grok', 'kimi', 'zhipu', 'deepseek']

// ── Helpers ──
function formatDate(value: string): string {
  if (!value) return '-'
  return new Date(value).toLocaleDateString()
}

// ── Platform section helpers ──
const activePlatforms = computed(() => form.platforms.filter(s => s.enabled).map(s => s.platform))

function addPlatformSection(platform: GroupPlatform) {
  form.platforms.push({
    platform,
    enabled: true,
    collapsed: false,
    group_ids: [],

    model_pricing: [],

    account_stats_pricing_rules: [],
  })
}

function togglePlatform(platform: GroupPlatform) {
  const section = form.platforms.find(s => s.platform === platform)
  if (section) {
    section.enabled = !section.enabled
    if (!section.enabled && activeTab.value === platform) {
      activeTab.value = 'basic'
    }
  } else {
    addPlatformSection(platform)
  }
}

function getGroupsForPlatform(platform: GroupPlatform): AdminGroup[] {
  return allGroups.value.filter(g => g.platform === platform)
}

// ── Group helpers ──
const groupToPricingConfigMap = computed(() => {
  const map = new Map<number, PricingConfig>()
  for (const ch of allPricingConfigsForConflict.value) {
    if (editingPricingConfig.value && ch.id === editingPricingConfig.value.id) continue
    for (const gid of ch.group_ids || []) {
      map.set(gid, ch)
    }
  }
  return map
})

function isGroupInOtherPricingConfig(groupId: number, _platform: string): boolean {
  return groupToPricingConfigMap.value.has(groupId)
}

function getGroupPricingConfigName(groupId: number): string {
  return groupToPricingConfigMap.value.get(groupId)?.name || ''
}

function getGroupInOtherPricingConfigLabel(groupId: number): string {
  const name = getGroupPricingConfigName(groupId)
  return t('admin.pricing.form.inOtherPricingConfig', { name }, `In "${name}"`)
}

const deleteConfirmMessage = computed(() => {
  const name = deletingPricingConfig.value?.name || ''
  return t(
    'admin.pricing.deleteConfirm',
    { name },
    `Are you sure you want to delete price configuration "${name}"? This action cannot be undone.`
  )
})

function toggleGroupInSection(sectionIdx: number, groupId: number) {
  const section = form.platforms[sectionIdx]
  const idx = section.group_ids.indexOf(groupId)
  if (idx >= 0) {
    section.group_ids.splice(idx, 1)
  } else {
    section.group_ids.push(groupId)
  }
}

// ── Pricing helpers ──
function addPricingEntry(sectionIdx: number) {
  form.platforms[sectionIdx].model_pricing.push({
    models: [],
    billing_mode: 'token',
    price_multiplier: null,
    fast_mode_multiplier: null,
    fast_multiplier: null,
    flex_multiplier: null,
    max_reasoning_effort_multiplier: null,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_1h_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm()
  })
}

const syncingPlatform = ref<string | null>(null)

async function syncLatestModels(sectionIdx: number) {
  const platform = form.platforms[sectionIdx].platform
  if (syncingPlatform.value) return
  syncingPlatform.value = platform
  try {
    const result = await adminAPI.pricing.syncPricingModels(platform)
    // 收集当前平台定价条目里已有的模型名
    const existingModels = new Set<string>()
    for (const entry of form.platforms[sectionIdx].model_pricing) {
      for (const m of entry.models) existingModels.add(m)
    }
    const newModels = result.models.filter(m => !existingModels.has(m))
    if (newModels.length === 0) {
      appStore.showSuccess(t('admin.pricing.form.syncModelsAlreadyUpToDate'))
      return
    }
    let defaultPricing: Pick<PricingFormEntry, 'input_price' | 'output_price' | 'cache_write_price' | 'cache_write_1h_price' | 'cache_read_price' | 'image_input_price' | 'image_output_price' | 'max_reasoning_effort_multiplier'> = {
      input_price: null,
      output_price: null,
      cache_write_price: null,
      cache_write_1h_price: null,
      cache_read_price: null,
      image_input_price: null,
      image_output_price: null,
      max_reasoning_effort_multiplier: null
    }
    if (platform === 'qoder') {
      try {
        const pricing = await adminAPI.pricing.getModelDefaultPricing(newModels[0], platform)
        if (pricing.found) {
          defaultPricing = {
            input_price: perTokenToMTok(pricing.input_price ?? null),
            output_price: perTokenToMTok(pricing.output_price ?? null),
            cache_write_price: perTokenToMTok(pricing.cache_write_price ?? null),
            cache_write_1h_price: perTokenToMTok(pricing.cache_write_1h_price ?? null),
            cache_read_price: perTokenToMTok(pricing.cache_read_price ?? null),
            image_input_price: perTokenToMTok(pricing.image_input_price ?? null),
            image_output_price: perTokenToMTok(pricing.image_output_price ?? null),
            max_reasoning_effort_multiplier: pricing.max_reasoning_effort_multiplier ?? null
          }
        }
      } catch {
        // 查询默认价格失败不影响同步模型列表，用户仍可手动填写。
      }
    }
    // 将新增模型合并为一个可继续手动调整价格的定价条目
    form.platforms[sectionIdx].model_pricing.push({
      models: newModels,
      billing_mode: 'token',
      price_multiplier: null,
      fast_mode_multiplier: null,
      fast_multiplier: null,
      flex_multiplier: null,
      max_reasoning_effort_multiplier: defaultPricing.max_reasoning_effort_multiplier ?? null,
      input_price: defaultPricing.input_price,
      output_price: defaultPricing.output_price,
      cache_write_price: defaultPricing.cache_write_price,
      cache_write_1h_price: defaultPricing.cache_write_1h_price,
      cache_read_price: defaultPricing.cache_read_price,
      image_input_price: defaultPricing.image_input_price,
      image_output_price: defaultPricing.image_output_price,
      per_request_price: null,
      intervals: [],
      time_pricing: createDefaultTimePricingForm()
    })
    appStore.showSuccess(t('admin.pricing.form.syncModelsSuccess', { count: newModels.length }))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.pricing.form.syncModelsError')))
  } finally {
    syncingPlatform.value = null
  }
}

function updatePricingEntry(sectionIdx: number, idx: number, updated: PricingFormEntry) {
  form.platforms[sectionIdx].model_pricing.splice(idx, 1, updated)
}

function removePricingEntry(sectionIdx: number, idx: number) {
  form.platforms[sectionIdx].model_pricing.splice(idx, 1)
}

// ── Account Stats Pricing helpers ──
function addAccountStatsRule(sectionIdx: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules.push({
    name: '',
    group_ids: [],
    account_ids: [],
    pricing: []
  })
}

function addRulePricingEntry(sectionIdx: number, ruleIndex: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules[ruleIndex].pricing.push({
    models: [],
    billing_mode: 'token',
    price_multiplier: null,
    fast_mode_multiplier: null,
    fast_multiplier: null,
    flex_multiplier: null,
    max_reasoning_effort_multiplier: null,
    input_price: null,
    output_price: null,
    cache_write_price: null,
    cache_write_1h_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm()
  })
}

function removeAccountStatsRule(sectionIdx: number, ruleIndex: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules.splice(ruleIndex, 1)
  // Clear all search state since indices shift after removal
  ruleAccountSearchRunner.clearAll()
  clearAllRuleAccountSearchState()
}

function removeRulePricingEntry(sectionIdx: number, ruleIndex: number, pricingIndex: number) {
  form.platforms[sectionIdx].account_stats_pricing_rules[ruleIndex].pricing.splice(pricingIndex, 1)
}

function getGroupNameById(groupId: number): string {
  const group = allGroups.value.find(g => g.id === groupId)
  return group ? group.name : `#${groupId}`
}

// ── Account search for pricing rules ──
interface SimpleAccount { id: number; name: string; platform: string }

const ruleAccountSearchKeyword = ref<Record<string, string>>({})
const ruleAccountSearchResults = ref<Record<string, SimpleAccount[]>>({})
const showRuleAccountDropdown = ref<Record<string, boolean>>({})
// Cache: account ID → name, populated when search results are selected
const ruleAccountNameCache = ref<Record<number, string>>({})

const ruleAccountSearchRunner = useKeyedDebouncedSearch<SimpleAccount[]>({
  delay: SEARCH_DEBOUNCE_MS,
  search: async (keyword, { key, signal }) => {
    const platform = key.split('-')[0]
    const res = await adminAPI.accounts.list(1, 20, { platform, search: keyword }, { signal })
    return res.items.map(a => ({ id: a.id, name: a.name, platform: a.platform }))
  },
  onSuccess: (key, result) => { ruleAccountSearchResults.value[key] = result },
  onError: (key) => { ruleAccountSearchResults.value[key] = [] },
})

function onRuleAccountSearchInput(platform: string, ruleIndex: number) {
  const key = `${platform}-${ruleIndex}`
  showRuleAccountDropdown.value[key] = true
  ruleAccountSearchRunner.trigger(key, ruleAccountSearchKeyword.value[key] || '')
}

function onRuleAccountSearchFocus(platform: string, ruleIndex: number) {
  const key = `${platform}-${ruleIndex}`
  showRuleAccountDropdown.value[key] = true
  if (!ruleAccountSearchResults.value[key]?.length) {
    ruleAccountSearchRunner.trigger(key, ruleAccountSearchKeyword.value[key] || '')
  }
}

function selectRuleAccount(
  rule: { account_ids: number[] },
  account: SimpleAccount,
  platform: string,
  ruleIndex: number,
) {
  if (!rule.account_ids.includes(account.id)) {
    rule.account_ids.push(account.id)
    ruleAccountNameCache.value[account.id] = account.name
  }
  const key = `${platform}-${ruleIndex}`
  ruleAccountSearchKeyword.value[key] = ''
  showRuleAccountDropdown.value[key] = false
}

function removeRuleAccount(rule: { account_ids: number[] }, accountId: number) {
  const idx = rule.account_ids.indexOf(accountId)
  if (idx !== -1) rule.account_ids.splice(idx, 1)
}

function getRuleAccountLabel(accountId: number): string {
  const name = ruleAccountNameCache.value[accountId]
  return name ? `${name} #${accountId}` : `#${accountId}`
}

function handleRuleAccountClickOutside(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (!target.closest('.rule-account-search-container')) {
    Object.keys(showRuleAccountDropdown.value).forEach(key => {
      showRuleAccountDropdown.value[key] = false
    })
  }
}

function clearAllRuleAccountSearchState() {
  ruleAccountSearchKeyword.value = {}
  ruleAccountSearchResults.value = {}
  showRuleAccountDropdown.value = {}
}

function accountStatsRulesToAPI(): AccountStatsPricingRule[] {
  const rules: AccountStatsPricingRule[] = []
  for (const section of form.platforms) {
    if (!section.enabled) continue
    for (const rule of section.account_stats_pricing_rules) {
      rules.push({
        name: rule.name,
        group_ids: rule.group_ids,
        account_ids: rule.account_ids,
        pricing: rule.pricing
          .filter(p => p.models.length > 0)
          .map(p => ({
            platform: section.platform,
            models: p.models,
            billing_mode: p.billing_mode,
            price_multiplier: toNullableNumber(p.price_multiplier),
            fast_mode_multiplier: toNullableNumber(p.fast_mode_multiplier),
            fast_multiplier: toNullableNumber(p.fast_multiplier),
            flex_multiplier: toNullableNumber(p.flex_multiplier),
            max_reasoning_effort_multiplier: toNullableNumber(p.max_reasoning_effort_multiplier),
            input_price: mTokToPerToken(p.input_price),
            output_price: mTokToPerToken(p.output_price),
            cache_write_price: mTokToPerToken(p.cache_write_price),
            cache_write_1h_price: mTokToPerToken(p.cache_write_1h_price),
            cache_read_price: mTokToPerToken(p.cache_read_price),
            image_input_price: mTokToPerToken(p.image_input_price),
            image_output_price: mTokToPerToken(p.image_output_price),
            per_request_price: p.per_request_price != null && p.per_request_price !== '' ? Number(p.per_request_price) : null,
            intervals: formIntervalsToAPI(p.intervals || []),
            time_pricing: null
          }))
      })
    }
  }
  return rules
}

// ── Form ↔ API conversion ──
function formToAPI(): { group_ids: number[], model_pricing: ModelPricingEntry[] } {
  const group_ids: number[] = []
  const model_pricing: ModelPricingEntry[] = []
  for (const section of form.platforms) {
    if (!section.enabled) continue
    group_ids.push(...section.group_ids)
    for (const entry of section.model_pricing) {
      if (entry.models.length) model_pricing.push(pricingEntryToAPI(entry, section.platform))
    }
  }
  return { group_ids, model_pricing }
}

function apiToForm(pricingConfig: PricingConfig): PlatformSection[] {
  // Build a map: groupID → platform
  const groupPlatformMap = new Map<number, GroupPlatform>()
  for (const g of allGroups.value) {
    groupPlatformMap.set(g.id, g.platform)
  }

  // Determine which platforms are active (from groups + pricing + mapping)
  const activePlatforms = new Set<GroupPlatform>()
  for (const gid of pricingConfig.group_ids || []) {
    const p = groupPlatformMap.get(gid)
    if (p) activePlatforms.add(p)
  }
  for (const p of pricingConfig.model_pricing || []) {
    if (p.platform) activePlatforms.add(p.platform as GroupPlatform)
  }

  // Build sections in platform order
  const sections: PlatformSection[] = []
  for (const platform of platformOrder) {
    if (!activePlatforms.has(platform)) continue

    const groupIds = (pricingConfig.group_ids || []).filter(gid => groupPlatformMap.get(gid) === platform)
    const pricing = (pricingConfig.model_pricing || [])
      .filter(p => (p.platform || 'anthropic') === platform)
      .map(pricingEntryFromAPI)

    sections.push({
      platform,
      enabled: true,
      collapsed: false,
      group_ids: groupIds,

      model_pricing: pricing,

      account_stats_pricing_rules: [],
    })
  }

  return sections
}

// ── Load data ──
async function loadPricingConfigs() {
  if (abortController) abortController.abort()
  const ctrl = new AbortController()
  abortController = ctrl
  loading.value = true

  try {
    const response = await adminAPI.pricing.list(pagination.page, pagination.page_size, {
      status: filters.status || undefined,
      search: searchQuery.value || undefined,
      sort_by: sortState.sort_by,
      sort_order: sortState.sort_order
    }, { signal: ctrl.signal })

    if (ctrl.signal.aborted || abortController !== ctrl) return
    pricingConfigs.value = response.items || []
    pagination.total = response.total
  } catch (error: unknown) {
    const e = error as { name?: string; code?: string }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(error, t('admin.pricing.loadError', 'Failed to load price configurations')))
  } finally {
    if (abortController === ctrl) {
      loading.value = false
      abortController = null
    }
  }
}

async function loadGroups() {
  groupsLoading.value = true
  try {
    allGroups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Error loading groups:', error)
  } finally {
    groupsLoading.value = false
  }
}

async function loadAllPricingConfigsForConflict() {
  try {
    const response = await adminAPI.pricing.list(1, 1000)
    allPricingConfigsForConflict.value = response.items || []
  } catch (error) {
    // Fallback to current page data
    allPricingConfigsForConflict.value = pricingConfigs.value
  }
}

let searchTimeout: ReturnType<typeof setTimeout>
function handleSearch() {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadPricingConfigs()
  }, 300)
}

function handlePageChange(page: number) {
  pagination.page = page
  loadPricingConfigs()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadPricingConfigs()
}

function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadPricingConfigs()
}

// ── Dialog ──
function resetForm() {
  form.name = ''
  form.description = ''
  form.status = 'active'

  form.billing_model_source = 'group_mapped'
  form.platforms = []
  form.apply_pricing_to_account_stats = false
  activeTab.value = 'basic'
  ruleAccountSearchRunner.clearAll()
  clearAllRuleAccountSearchState()
  ruleAccountNameCache.value = {}
}

async function openCreateDialog() {
  editingPricingConfig.value = null
  resetForm()
  await Promise.all([loadGroups(), loadAllPricingConfigsForConflict()])
  showDialog.value = true
}

async function openEditDialog(pricingConfig: PricingConfig) {
  editingPricingConfig.value = pricingConfig
  form.name = pricingConfig.name
  form.description = pricingConfig.description || ''
  form.status = pricingConfig.status

  form.billing_model_source = pricingConfig.billing_model_source || 'group_mapped'
  form.apply_pricing_to_account_stats = pricingConfig.apply_pricing_to_account_stats || false
  // Must load groups first so apiToForm can map groupID → platform
  await Promise.all([loadGroups(), loadAllPricingConfigsForConflict()])
  form.platforms = apiToForm(pricingConfig)

  // 将账号统计定价规则按平台分配到表单。
  distributeRulesToPlatforms(pricingConfig.account_stats_pricing_rules || [])

  // Populate ruleAccountNameCache for existing rule accounts
  await populateRuleAccountNameCache()

  showDialog.value = true
}

/** 根据关联分组的平台拆分账号统计定价规则。 */
function distributeRulesToPlatforms(apiRules: AccountStatsPricingRule[]) {
  // Build groupID → platform lookup
  const groupPlatformMap = new Map<number, GroupPlatform>()
  for (const g of allGroups.value) {
    groupPlatformMap.set(g.id, g.platform)
  }

  for (const apiRule of apiRules) {
    // Infer platform from group_ids
    const platforms = new Set<GroupPlatform>()
    for (const gid of apiRule.group_ids || []) {
      const p = groupPlatformMap.get(gid)
      if (p) platforms.add(p)
    }
    // If pricing has a platform field, use that as fallback
    if (platforms.size === 0 && apiRule.pricing?.length > 0) {
      const p = apiRule.pricing[0].platform as GroupPlatform | undefined
      if (p) platforms.add(p)
    }
    const targetPlatform = platforms.size >= 1 ? [...platforms][0] : null
    if (!targetPlatform) continue

    const section = form.platforms.find(s => s.platform === targetPlatform)
    if (!section) continue

    const formRule: FormPricingRule = {
      name: apiRule.name || '',
      group_ids: [...(apiRule.group_ids || [])],
      account_ids: [...(apiRule.account_ids || [])],
      pricing: (apiRule.pricing || []).map(p => ({
        models: [...(p.models || [])],
        billing_mode: p.billing_mode,
        price_multiplier: p.price_multiplier ?? null,
        fast_mode_multiplier: null,
        fast_multiplier: p.fast_multiplier ?? null,
        flex_multiplier: p.flex_multiplier ?? null,
        max_reasoning_effort_multiplier: p.max_reasoning_effort_multiplier ?? null,
        input_price: perTokenToMTok(p.input_price),
        output_price: perTokenToMTok(p.output_price),
        cache_write_price: perTokenToMTok(p.cache_write_price),
        cache_write_1h_price: perTokenToMTok(p.cache_write_1h_price),
        cache_read_price: perTokenToMTok(p.cache_read_price),
        image_input_price: perTokenToMTok(p.image_input_price),
        image_output_price: perTokenToMTok(p.image_output_price),
        per_request_price: p.per_request_price,
        intervals: apiIntervalsToForm(p.intervals || []),
        time_pricing: createDefaultTimePricingForm()
      } as PricingFormEntry))
    }
    section.account_stats_pricing_rules.push(formRule)
  }
}

/** Populate ruleAccountNameCache by fetching account details for all account_ids in rules */
async function populateRuleAccountNameCache() {
  const allAccountIds = new Set<number>()
  for (const section of form.platforms) {
    for (const rule of section.account_stats_pricing_rules) {
      for (const id of rule.account_ids) {
        allAccountIds.add(id)
      }
    }
  }
  if (allAccountIds.size === 0) return

  // Fetch account details in parallel (batch of individual getById calls)
  const ids = [...allAccountIds]
  const results = await Promise.allSettled(
    ids.map(id => adminAPI.accounts.getById(id))
  )
  for (let i = 0; i < ids.length; i++) {
    const result = results[i]
    if (result.status === 'fulfilled') {
      ruleAccountNameCache.value[ids[i]] = result.value.name
    }
    // If rejected, the cache won't have the name, so it'll show "#ID" which is acceptable
  }
}

function closeDialog() {
  showDialog.value = false
  editingPricingConfig.value = null
  resetForm()
}

async function handleSubmit() {
  if (submitting.value) return
  if (!form.name.trim()) {
    appStore.showError(t('admin.pricing.nameRequired', 'Please enter a price configuration name'))
    return
  }

  // 检查未选择模型的定价条目，避免保存后被静默跳过
  for (const section of form.platforms.filter(s => s.enabled)) {
    if (section.group_ids.length === 0) {
      const platformLabel = t('admin.groups.platforms.' + section.platform, section.platform)
      appStore.showError(t('admin.pricing.noGroupsSelected', { platform: platformLabel }, `${platformLabel} 平台未选择分组，请至少选择一个分组或禁用该平台`))
      activeTab.value = section.platform
      return
    }
    for (const entry of section.model_pricing) {
      if (entry.models.length === 0) {
        const platformLabel = t('admin.groups.platforms.' + section.platform, section.platform)
        appStore.showError(t('admin.pricing.emptyModelsInPricing', { platform: platformLabel }, `${platformLabel} 平台下有定价条目未添加模型，请添加模型或删除该条目`))
        activeTab.value = section.platform
        return
      }
    }
  }

  // 按平台检查模型模式冲突（重复或通配符范围重叠）
  for (const section of form.platforms.filter(s => s.enabled)) {
    const pricingError = validatePricingForm(section.model_pricing, t)
    if (pricingError) {
      appStore.showError(pricingError)
      activeTab.value = section.platform
      return
    }

  }

  // 倍率只能调整已配置的定价，不能单独继承系统默认价。
  for (const section of form.platforms.filter(s => s.enabled)) {
    const entries = [
      ...section.account_stats_pricing_rules.flatMap(rule => rule.pricing),
    ]
    for (const entry of entries) {
      if (entry.models.length === 0 || toNullableNumber(entry.price_multiplier) === null) continue
      if (!hasExplicitPricing(entry)) {
        const models = entry.models.join(', ')
        appStore.showError(t(
          'admin.pricing.form.priceMultiplierRequiresPrice',
          { models },
          `模型 ${models} 配置定价倍率时，必须至少填写一项价格`,
        ))
        activeTab.value = section.platform
        return
      }
    }
  }

  const { group_ids, model_pricing } = formToAPI()

  submitting.value = true
  try {
    if (editingPricingConfig.value) {
      const req: UpdatePricingConfigRequest = {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        status: form.status,
        group_ids,
        model_pricing,

        billing_model_source: form.billing_model_source,

        apply_pricing_to_account_stats: form.apply_pricing_to_account_stats,
        account_stats_pricing_rules: accountStatsRulesToAPI()
      }
      await adminAPI.pricing.update(editingPricingConfig.value.id, req)
      appStore.showSuccess(t('admin.pricing.updateSuccess', 'PricingConfig updated'))
    } else {
      const req: CreatePricingConfigRequest = {
        name: form.name.trim(),
        description: form.description.trim() || undefined,
        group_ids,
        model_pricing,

        billing_model_source: form.billing_model_source,

        apply_pricing_to_account_stats: form.apply_pricing_to_account_stats,
        account_stats_pricing_rules: accountStatsRulesToAPI()
      }
      await adminAPI.pricing.create(req)
      appStore.showSuccess(t('admin.pricing.createSuccess', 'PricingConfig created'))
    }
    closeDialog()
    loadPricingConfigs()
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, editingPricingConfig.value
      ? t('admin.pricing.updateError', 'Failed to update price configuration')
      : t('admin.pricing.createError', 'Failed to create price configuration')))
  } finally {
    submitting.value = false
  }
}

// ── Toggle status ──
async function togglePricingConfigStatus(pricingConfig: PricingConfig) {
  const newStatus = pricingConfig.status === 'active' ? 'disabled' : 'active'
  try {
    await adminAPI.pricing.update(pricingConfig.id, { status: newStatus })
    if (filters.status && filters.status !== newStatus) {
      // Item no longer matches the active filter — reload list
      await loadPricingConfigs()
    } else {
      pricingConfig.status = newStatus
    }
  } catch (error) {
    appStore.showError(t('admin.pricing.updateError', 'Failed to update price configuration'))
    console.error('Error toggling pricingConfig status:', error)
  }
}

// ── Delete ──
function handleDelete(pricingConfig: PricingConfig) {
  deletingPricingConfig.value = pricingConfig
  showDeleteDialog.value = true
}

async function confirmDelete() {
  if (!deletingPricingConfig.value) return

  try {
    await adminAPI.pricing.remove(deletingPricingConfig.value.id)
    appStore.showSuccess(t('admin.pricing.deleteSuccess', 'PricingConfig deleted'))
    showDeleteDialog.value = false
    deletingPricingConfig.value = null
    loadPricingConfigs()
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t('admin.pricing.deleteError', 'Failed to delete price configuration')))
  }
}

// ── Lifecycle ──
onMounted(() => {
  loadPricingConfigs()
  loadGroups()

  document.addEventListener('click', handleRuleAccountClickOutside)
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
  document.removeEventListener('click', handleRuleAccountClickOutside)
  ruleAccountSearchRunner.clearAll()
  clearAllRuleAccountSearchState()
})
</script>

<style scoped>
.pricing-dialog-body {
  display: flex;
  flex-direction: column;
  height: 70vh;
  min-height: 400px;
}

.pricing-tab {
  @apply flex h-9 items-center gap-1.5 px-3 py-1.5 text-sm font-medium border-b-2 transition-colors whitespace-nowrap;
}

.pricing-tab-active {
  @apply border-primary-600 text-primary-600 dark:border-primary-400 dark:text-primary-400;
}

.pricing-tab-inactive {
  @apply border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300;
}
</style>
