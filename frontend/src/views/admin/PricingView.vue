<template>
  <AppLayout>
    <div
      class="mb-4 flex gap-2 border-b border-gray-200 dark:border-dark-700"
      role="tablist"
      :aria-label="t('admin.pricing.title')"
    >
      <button
        v-for="tab in ['configs', 'defaults'] as const"
        :key="tab"
        class="px-4 py-3 text-sm font-medium border-b-2"
        :class="
          pageTab === tab
            ? 'border-primary-500 text-primary-700 dark:text-primary-300'
            : 'border-transparent text-gray-500 hover:text-gray-900 dark:text-gray-400 dark:hover:text-white'
        "
        role="tab"
        :aria-selected="pageTab === tab"
        @click="pageTab = tab"
      >
        {{ t(`admin.pricing.tabs.${tab}`) }}
      </button>
    </div>
    <DefaultPricingPanel v-if="pageTab === 'defaults'" />
    <TablePageLayout v-show="pageTab === 'configs'">
      <template #filters>
        <div class="flex flex-wrap items-center gap-2">
          <!-- Left: Search + Filters -->
          <div class="flex min-w-0 flex-1 flex-wrap items-center gap-2">
            <div class="input-icon-wrap min-w-0 flex-1 sm:flex-none sm:w-64">
              <Icon name="search" size="md" class="input-icon text-gray-400 dark:text-gray-500" />
              <input
                v-model="searchQuery"
                type="text"
                :placeholder="
                  t('admin.pricing.searchPricingConfigs', 'Search price configurations...')
                "
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
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            </button>
            <button
              @click="openCreateDialog"
              class="btn btn-primary whitespace-nowrap px-3 sm:px-4"
            >
              <Icon name="plus" size="sm" class="mr-2" />
              {{ t('admin.pricing.createPricingConfig', 'Create price configuration') }}
            </button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          column-order-storage-key="admin-pricing-column-order"
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
              :description="
                t(
                  'admin.pricing.createFirstPricingConfig',
                  'Create your first price configuration to manage model pricing',
                )
              "
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
      :title="
        editingPricingConfig
          ? t('admin.pricing.editPricingConfig', 'Edit price configuration')
          : t('admin.pricing.createPricingConfig', 'Create price configuration')
      "
      width="extra-wide"
      @close="closeDialog"
    >
      <div class="pricing-dialog-body">
        <div
          role="tablist"
          :aria-label="t('admin.pricing.editPricingConfig')"
          class="flex shrink-0 items-center overflow-x-auto border-b border-gray-200 dark:border-dark-700"
        >
          <button
            v-for="tab in dialogTabs"
            :id="`pricing-tab-${tab.key}`"
            :key="tab.key"
            type="button"
            role="tab"
            :aria-selected="activeTab === tab.key"
            :aria-controls="`pricing-panel-${tab.key}`"
            :tabindex="activeTab === tab.key ? 0 : -1"
            class="pricing-tab"
            :class="activeTab === tab.key ? 'pricing-tab-active' : 'pricing-tab-inactive'"
            @click="activeTab = tab.key"
            @keydown="onDialogTabKeydown($event, tab.key)"
          >
            {{ t(tab.label) }}
          </button>
        </div>

        <!-- Tab Content -->
        <form
          ref="dialogForm"
          novalidate
          id="pricing-form"
          @submit.prevent="handleSubmit"
          class="flex-1 overflow-y-auto pt-4"
        >
          <section
            id="pricing-panel-billing"
            v-show="activeTab === 'billing'"
            v-content-reveal="activeTab === 'billing'"
            role="tabpanel"
            aria-labelledby="pricing-tab-billing"
            data-pricing-panel="billing"
          >
            <BillingSettingsPanel v-model="form.billing_settings" />
          </section>
          <!-- Basic Settings Tab -->
          <div
            id="pricing-panel-basic"
            v-show="activeTab === 'basic'"
            v-content-reveal="activeTab === 'basic'"
            role="tabpanel"
            aria-labelledby="pricing-tab-basic"
            data-pricing-panel="basic"
            class="space-y-5"
          >
            <!-- Name -->
            <div>
              <label class="input-label"
                >{{ t('admin.pricing.form.name', 'Name') }}
                <span class="text-red-500">*</span></label
              >
              <input
                v-model="form.name"
                type="text"
                required
                class="input"
                :placeholder="
                  t('admin.pricing.form.namePlaceholder', 'Enter price configuration name')
                "
              />
            </div>

            <!-- Description -->
            <div>
              <label class="input-label">{{
                t('admin.pricing.form.description', 'Description')
              }}</label>
              <textarea
                v-model="form.description"
                rows="2"
                class="input"
                :placeholder="
                  t('admin.pricing.form.descriptionPlaceholder', 'Optional description')
                "
              ></textarea>
            </div>

            <!-- Status (edit only) -->
            <div v-if="editingPricingConfig">
              <label class="input-label">{{ t('admin.pricing.form.status', 'Status') }}</label>
              <Select v-model="form.status" :options="statusEditOptions" />
            </div>

            <!-- Billing Basis -->
            <div>
              <label class="input-label">{{
                t('admin.pricing.form.billingModelSource', 'User billing model source')
              }}</label>
              <Select v-model="form.billing_model_source" :options="billingModelSourceOptions" />
              <p class="mt-1 text-xs text-gray-400" data-testid="billing-model-source-hint">
                {{ billingModelSourceHint }}
              </p>
            </div>
          </div>

          <!-- Model billing and provider cost rules. -->

          <div
            v-for="(section, sIdx) in form.sections"
            :key="sIdx"
            id="pricing-panel-pricing"
            role="tabpanel"
            aria-labelledby="pricing-tab-pricing"
            data-pricing-panel="pricing"
            v-show="activeTab === 'pricing'"
            v-content-reveal="activeTab === 'pricing'"
            class="space-y-4"
          >
            <!-- Groups -->
            <div>
              <label class="input-label text-xs">
                {{ t('admin.pricing.form.groups', 'Associated Groups') }}
                <span class="text-red-500">*</span>
                <span v-if="section.group_ids.length > 0" class="ml-1 font-normal text-gray-400">
                  ({{
                    t(
                      'admin.pricing.form.selectedCount',
                      { count: section.group_ids.length },
                      `已选 ${section.group_ids.length} 个`,
                    )
                  }})
                </span>
              </label>
              <div
                class="max-h-40 overflow-auto rounded-control border border-gray-200 p-3 dark:border-dark-600"
              >
                <div v-if="groupsLoading" class="py-2 text-center text-xs text-gray-500">
                  {{ t('common.loading', 'Loading...') }}
                </div>
                <div
                  v-else-if="allGroups.length === 0"
                  class="py-2 text-center text-xs text-gray-500"
                >
                  {{ t('admin.pricing.form.noGroupsAvailable', 'No groups available') }}
                </div>
                <div v-else class="flex flex-wrap gap-2">
                  <label
                    v-for="group in allGroups"
                    :key="group.id"
                    class="inline-flex max-w-full cursor-pointer items-center gap-2 rounded-control p-1.5 transition-colors hover:bg-gray-50 dark:hover:bg-dark-700"
                    :class="[
                      section.group_ids.includes(group.id)
                        ? 'bg-primary-50 dark:bg-primary-500/8 dark:text-primary-500'
                        : '',
                      isGroupInOtherPricingConfig(group.id) ? 'cursor-not-allowed opacity-40' : '',
                    ]"
                  >
                    <input
                      type="checkbox"
                      :checked="section.group_ids.includes(group.id)"
                      :disabled="isGroupInOtherPricingConfig(group.id)"
                      class="h-4 w-4 shrink-0 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                      @change="toggleGroupInSection(sIdx, group.id)"
                    />
                    <GroupBadge
                      :name="group.name"
                      :display-brand="group.display_brand"
                      :rate-multiplier="group.rate_multiplier"
                      class="min-w-0"
                    />
                    <span
                      v-if="isGroupInOtherPricingConfig(group.id)"
                      class="text-xs text-gray-400"
                      >{{ getGroupInOtherPricingConfigLabel(group.id) }}</span
                    >
                  </label>
                </div>
              </div>
            </div>

            <!-- Model Pricing -->
            <RuleListEditor
              :items="section.model_pricing"
              :title="t('admin.pricing.form.modelPricing')"
              :empty-text="t('admin.pricing.form.noPricingRules')"
              test-id="model-pricing-entries"
              :removable="false"
              @add="addPricingEntry(sIdx)"
            >
              <template #row="{ item: entry, index: idx }">
                <PricingEntryCard
                  :entry="entry"
                  allow-reference
                  enable-time-pricing
                  enable-tier-multipliers
                  @update="updatePricingEntry(sIdx, idx, $event)"
                  @remove="removePricingEntry(sIdx, idx)"
                />
              </template>
            </RuleListEditor>

            <!-- Provider cost rules. -->
            <RuleListEditor
              class="mt-4 border-t border-gray-200 pt-4 dark:border-dark-700"
              :items="section.provider_stats_pricing_rules"
              :title="t('admin.pricing.form.providerStatsPricingRules')"
              :add-label="t('admin.pricing.form.addRule')"
              :empty-text="t('admin.pricing.form.noRulesConfigured')"
              variant="card"
              :item-label="(index) => t('common.ruleIndex', { index: index + 1 })"
              test-id="provider-stats-rules"
              @add="addProviderStatsRule(sIdx)"
              @remove="removeProviderStatsRule(sIdx, $event)"
            >
              <template #row="{ item: rule, index: ruleIndex }">
                <div class="space-y-3">
                  <div class="flex items-center justify-between">
                    <input
                      v-model="rule.name"
                      :placeholder="t('admin.pricing.form.ruleName')"
                      class="input text-sm"
                    />
                  </div>
                  <div>
                    <label class="text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.pricing.form.ruleGroups')
                    }}</label>
                    <div class="mt-1 flex flex-wrap gap-1">
                      <label
                        v-for="gid in section.group_ids"
                        :key="gid"
                        class="inline-flex cursor-pointer items-center gap-1 rounded-compact border px-2 py-1 text-xs transition-colors"
                        :class="
                          rule.group_ids.includes(gid)
                            ? 'border-primary-300 bg-primary-50 dark:border-primary-500/15 dark:bg-primary-500/8 dark:text-primary-500'
                            : 'border-gray-200 hover:bg-gray-50 dark:border-dark-600 dark:hover:bg-dark-700'
                        "
                      >
                        <input
                          type="checkbox"
                          :checked="rule.group_ids.includes(gid)"
                          class="h-3 w-3 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                          @change="
                            rule.group_ids.includes(gid)
                              ? rule.group_ids.splice(rule.group_ids.indexOf(gid), 1)
                              : rule.group_ids.push(gid)
                          "
                        />
                        <span :class="['font-medium', 'text-gray-900 dark:text-gray-100']">{{
                          getGroupNameById(gid)
                        }}</span>
                      </label>
                    </div>
                    <p v-if="section.group_ids.length === 0" class="mt-1 text-xs text-gray-400">
                      {{ t('admin.pricing.form.noGroupsInPricingConfig') }}
                    </p>
                  </div>
                  <div>
                    <label class="text-xs text-gray-500 dark:text-gray-400">{{
                      t('admin.pricing.form.ruleProviders')
                    }}</label>
                    <!-- Selected provider chips -->
                    <div class="mt-1 flex flex-wrap gap-1">
                      <span
                        v-for="providerId in rule.provider_ids"
                        :key="providerId"
                        class="inline-flex items-center gap-1 rounded-compact border border-primary-300 bg-primary-50 px-2 py-0.5 text-xs dark:border-primary-700 dark:bg-primary-900/20"
                      >
                        <span :class="['font-medium', 'text-gray-900 dark:text-gray-100']">{{
                          getRuleProviderLabel(providerId)
                        }}</span>
                        <button
                          type="button"
                          @click="removeRuleProvider(rule, providerId)"
                          class="text-gray-400 hover:text-red-500"
                        >
                          <Icon name="x" size="xs" />
                        </button>
                      </span>
                    </div>
                    <!-- Provider search input -->
                    <div class="relative mt-1 rule-provider-search-container">
                      <input
                        v-model="ruleProviderSearchKeyword[`${'pricing'}-${ruleIndex}`]"
                        type="text"
                        class="input text-sm"
                        :placeholder="t('admin.pricing.form.searchProviderPlaceholder')"
                        @input="onRuleProviderSearchInput('pricing', ruleIndex)"
                        @focus="onRuleProviderSearchFocus('pricing', ruleIndex)"
                      />
                      <!-- Search results dropdown -->
                      <MotionTransition name="dropdown-fade">
                        <div
                          v-if="
                            showRuleProviderDropdown[`${'pricing'}-${ruleIndex}`] &&
                            (ruleProviderSearchResults[`${'pricing'}-${ruleIndex}`]?.length ?? 0) >
                              0
                          "
                          :inert="
                            !(
                              showRuleProviderDropdown[`${'pricing'}-${ruleIndex}`] &&
                              (ruleProviderSearchResults[`${'pricing'}-${ruleIndex}`]?.length ??
                                0) > 0
                            ) || undefined
                          "
                          class="dropdown z-50 mt-1 max-h-48 w-full overflow-auto py-0"
                        >
                          <button
                            v-for="provider in ruleProviderSearchResults[
                              `${'pricing'}-${ruleIndex}`
                            ]"
                            :key="provider.id"
                            type="button"
                            @click="selectRuleProvider(rule, provider, 'pricing', ruleIndex)"
                            class="dropdown-item-sm"
                            :class="{ 'opacity-50': rule.provider_ids.includes(provider.id) }"
                            :disabled="rule.provider_ids.includes(provider.id)"
                          >
                            <span :class="platformTextClass(provider.platform)">{{
                              provider.name
                            }}</span>
                            <span class="text-xs text-gray-400">#{{ provider.id }}</span>
                          </button>
                        </div>
                      </MotionTransition>
                    </div>
                    <p class="mt-1 text-xs text-gray-400">
                      {{ t('admin.pricing.form.ruleProvidersHint') }}
                    </p>
                  </div>
                  <RuleListEditor
                    :items="rule.pricing"
                    :title="t('admin.pricing.form.ruleModelPricing')"
                    :empty-text="t('admin.pricing.form.noPricingRules')"
                    :test-id="`provider-rule-pricing-${ruleIndex}`"
                    @add="addRulePricingEntry(sIdx, ruleIndex)"
                    :removable="false"
                  >
                    <template #row="{ item: entry, index: idx }">
                      <PricingEntryCard
                        :entry="entry"
                        @remove="removeRulePricingEntry(sIdx, ruleIndex, idx)"
                        @update="Object.assign(entry, $event)"
                      />
                    </template>
                  </RuleListEditor>
                </div>
              </template>
            </RuleListEditor>
          </div>
        </form>
      </div>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button @click="closeDialog" type="button" class="btn btn-secondary">
            {{ t('common.cancel', 'Cancel') }}
          </button>
          <button type="submit" form="pricing-form" :disabled="submitting" class="btn btn-primary">
            {{
              submitting
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
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { vContentReveal } from '@/directives/contentReveal'

import { nextTick, ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'
import { adminAPI } from '@/api/admin'
import type {
  PricingConfig,
  ModelPricingEntry,
  CreatePricingConfigRequest,
  UpdatePricingConfigRequest,
  ProviderStatsPricingRule,
} from '@/api/admin/pricing'
import type { PricingFormEntry } from '@/components/admin/pricing/types'
import {
  pricingEntryFromAPI,
  pricingEntryToAPI,
  validatePricingForm,
} from '@/components/admin/pricing/pricingForm'
import {
  createDefaultTimePricingForm,
  hasExplicitPricing,
  toNullableNumber,
} from '@/components/admin/pricing/types'
import type { AdminGroup } from '@/api/groups.types'
import type { Column } from '@/components/common/table/types'
import { platformTextClass } from '@/utils/platformColors'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import BillingSettingsPanel from '@/components/admin/pricing/BillingSettingsPanel.vue'
import {
  defaultBillingSettings,
  billingSettingsToAPI,
  validateBillingSettings,
} from '@/components/admin/pricing/billingSettings'
import DefaultPricingPanel from '@/components/admin/pricing/DefaultPricingPanel.vue'
import PricingEntryCard from '@/components/admin/pricing/PricingEntryCard.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useKeyedDebouncedSearch } from '@/composables/useKeyedDebouncedSearch'

// PricingView.vue manages shared price configurations, group associations, and provider cost
// rules. The editor separates basic, pricing, and billing settings while retaining one draft
// across tabs. Submission validates native fields and domain rules before converting form
// amounts and nested rules into API payloads.
//
// List requests use abort-controller identity to keep newer filter results current. Provider
// search keys combine section and row positions, so removing a rule clears pending searches
// before shifted indexes can reuse them. Pricing card updates preserve entry objects to retain
// their local collapse and input state. Group conflict lookup uses a separate configuration
// list from the paginated table.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t } = useI18n()
// pageTab switches between managed price configurations and the shared default catalog, leaving
// each panel to own its displayed records.
const pageTab = ref<'configs' | 'defaults'>('configs')
// appStore displays operation notifications after requests complete or validation prevents the
// requested action.
const appStore = useAppStore()

// FormPricingRule represents an editable provider cost rule and its pricing cards.
interface FormPricingRule {
  name: string // record display name
  group_ids: number[] // selected group identifiers
  provider_ids: number[] // selected provider identifiers
  pricing: PricingFormEntry[] // provider cost price cards
}

// PricingSection groups selected groups, model prices, and ordered provider cost rules.
interface PricingSection {
  group_ids: number[] // selected group identifiers

  model_pricing: PricingFormEntry[] // model billing price cards

  provider_stats_pricing_rules: FormPricingRule[] // ordered provider cost rules
}

// columns defines the displayed table fields and their translated headings, ordering, and
// sorting controls.
const columns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.pricing.columns.name', 'Name'), sortable: true },
  {
    key: 'description',
    label: t('admin.pricing.columns.description', 'Description'),
    sortable: false,
  },
  { key: 'status', label: t('admin.pricing.columns.status', 'Status'), sortable: true },
  { key: 'group_count', label: t('admin.pricing.columns.groups', 'Groups'), sortable: false },
  { key: 'pricing_count', label: t('admin.pricing.columns.pricing', 'Pricing'), sortable: false },
  { key: 'created_at', label: t('admin.pricing.columns.createdAt', 'Created'), sortable: true },
  { key: 'actions', label: t('admin.pricing.columns.actions', 'Actions'), sortable: false },
])

// statusFilterOptions, statusEditOptions, and billingModelSourceOptions define supported list,
// activation, and model-identity selections with their translated labels.
const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.pricing.allStatus', 'All Status') },
  { value: 'active', label: t('admin.pricing.statusActive', 'Active') },
  { value: 'disabled', label: t('admin.pricing.statusDisabled', 'Disabled') },
])

const statusEditOptions = computed(() => [
  { value: 'active', label: t('admin.pricing.statusActive', 'Active') },
  { value: 'disabled', label: t('admin.pricing.statusDisabled', 'Disabled') },
])

const billingModelSourceOptions = computed(() => [
  {
    value: 'group_mapped',
    label: t('admin.pricing.form.billingModelSourceGroupMapped', 'Group-mapped model (default)'),
  },
  {
    value: 'requested',
    label: t('admin.pricing.form.billingModelSourceRequested', 'Client request model'),
  },
  {
    value: 'upstream',
    label: t('admin.pricing.form.billingModelSourceUpstream', 'Provider final upstream model'),
  },
])

// pricingConfigs holds the current server page, while loading and searchQuery describe the
// active list request and editable search text.
const pricingConfigs = ref<PricingConfig[]>([])
// loading tracks the current list request so the table can present pending data to
// administrators.
const loading = ref(false)
// searchQuery stores the editable query text used when the page requests its next filtered
// list.
const searchQuery = ref('')
// filters stores the selected list constraints, while pagination and sortState supply the
// requested page and ordering.
const filters = reactive({ status: '' })
// pagination stores the current page, page size, and server totals used by the table controls.
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
})
// sortState stores the selected server ordering so filter and page changes reuse the same sort.
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc',
})

// Dialog visibility, selected create-or-edit identity, submission progress, and deletion target
// are maintained separately so each action can render its own state.
const showDialog = ref(false)
const editingPricingConfig = ref<PricingConfig | null>(null)
// submitting records an active form persistence attempt so submission controls can display
// their busy state.
const submitting = ref(false)
// showDeleteDialog controls deletion confirmation, while the selected record identifies the
// target used after confirmation.
const showDeleteDialog = ref(false)
const deletingPricingConfig = ref<PricingConfig | null>(null)
// activeTab and dialogForm coordinate validation across mounted panels, and dialogTabs supplies
// the basic, pricing, and billing tab order.
const activeTab = ref<string>('basic')
const dialogForm = ref<HTMLFormElement | null>(null)
const dialogTabs = [
  { key: 'basic', label: 'admin.pricing.form.basicSettings' },
  { key: 'pricing', label: 'admin.pricing.form.modelPricing' },
  { key: 'billing', label: 'admin.pricing.billingSettings.title' },
]

// onDialogTabKeydown navigates dialog tabs with arrows, Home, and End. Handled keys update the
// active panel and focus its tab button, while other keys retain their normal browser behavior.
function onDialogTabKeydown(event: KeyboardEvent, key: string) {
  const index = dialogTabs.findIndex((tab) => tab.key === key)
  let next: number
  if (event.key === 'ArrowRight') next = (index + 1) % dialogTabs.length
  else if (event.key === 'ArrowLeft') next = (index + dialogTabs.length - 1) % dialogTabs.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = dialogTabs.length - 1
  else return
  event.preventDefault()
  activeTab.value = dialogTabs[next].key
  document.getElementById(`pricing-tab-${activeTab.value}`)?.focus()
}

// allGroups and groupsLoading supply association choices independently of the price table,
// while the conflict list tracks assignments in other configurations.
const allGroups = ref<AdminGroup[]>([])
const groupsLoading = ref(false)

// Group association checks use a separate configuration list so the current table page does not
// normally limit conflict discovery.
const allPricingConfigsForConflict = ref<PricingConfig[]>([])

// form owns editable fields for the open dialog, keeping pending changes separate from the
// displayed server records.
const form = reactive({
  billing_settings: defaultBillingSettings(),
  name: '',
  description: '',
  status: 'active',

  billing_model_source: 'group_mapped' as string,
  sections: [emptyPricingSection()] as PricingSection[],
})

// The billing-model selector identifies which request model representation is used to look up
// the configured price.
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

// abortController owns the active list read and lets the next query cancel work for the
// previous selection.
let abortController: AbortController | null = null

// formatDate formats a saved date for the configuration table. Empty values display a dash, and
// populated values use the browser's local date representation after parsing the supplied
// timestamp.
function formatDate(value: string): string {
  if (!value) return '-'
  return new Date(value).toLocaleDateString()
}

// emptyPricingSection creates a fresh section with no groups, prices, or cost rules.
function emptyPricingSection(): PricingSection {
  return { group_ids: [], model_pricing: [], provider_stats_pricing_rules: [] }
}

// groupToPricingConfigMap indexes group ownership from the conflict list, excluding the
// configuration currently being edited from competing assignments.
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

// isGroupInOtherPricingConfig reports whether another configuration already references the
// selected group.
function isGroupInOtherPricingConfig(groupId: number): boolean {
  return groupToPricingConfigMap.value.has(groupId)
}

// getGroupPricingConfigName returns the competing configuration name for a group. An unassigned
// group returns empty text, and the computed lookup excludes the configuration currently being
// edited by the page.
function getGroupPricingConfigName(groupId: number): string {
  return groupToPricingConfigMap.value.get(groupId)?.name || ''
}

// getGroupInOtherPricingConfigLabel formats the conflicting price configuration name for group
// selection.
function getGroupInOtherPricingConfigLabel(groupId: number): string {
  const name = getGroupPricingConfigName(groupId)
  return t('admin.pricing.form.inOtherPricingConfig', { name }, `In "${name}"`)
}

// deleteConfirmMessage names the selected record in the translated confirmation text presented
// before the delete request.
const deleteConfirmMessage = computed(() => {
  const name = deletingPricingConfig.value?.name || ''
  return t(
    'admin.pricing.deleteConfirm',
    { name },
    `Are you sure you want to delete price configuration "${name}"? This action cannot be undone.`,
  )
})

// toggleGroupInSection adds or removes a group ID in the selected draft section.
function toggleGroupInSection(sectionIdx: number, groupId: number) {
  const section = form.sections[sectionIdx]
  const idx = section.group_ids.indexOf(groupId)
  if (idx >= 0) {
    section.group_ids.splice(idx, 1)
  } else {
    section.group_ids.push(groupId)
  }
}

// addPricingEntry appends an empty token price card to the selected section.
function addPricingEntry(sectionIdx: number) {
  form.sections[sectionIdx].model_pricing.push({
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
    cache_write_5m_price: null,
    cache_write_1h_price: null,
    cache_write_30m_price: null,
    cache_write_12h_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm(),
  })
}

// updatePricingEntry merges a child card's emitted fields into the existing page-owned model
// price entry. Callers supply valid section and row indexes from the currently rendered draft.
// The card can emit a copied entry or interval container, but this receiver retains the entry
// object that RuleListEditor uses to resolve its display key. Updating that object in place
// keeps the same card instance mounted, including its local collapse state and the retained
// interval objects used by nested input rows.
function updatePricingEntry(sectionIdx: number, idx: number, updated: PricingFormEntry) {
  // Retain the existing card object because its keyed component owns collapse state that should
  // survive editing a price field.
  Object.assign(form.sections[sectionIdx].model_pricing[idx], updated)
}

// removePricingEntry removes the indexed model price card from its section.
function removePricingEntry(sectionIdx: number, idx: number) {
  form.sections[sectionIdx].model_pricing.splice(idx, 1)
}

// addProviderStatsRule appends an empty provider cost rule to the section.
function addProviderStatsRule(sectionIdx: number) {
  form.sections[sectionIdx].provider_stats_pricing_rules.push({
    name: '',
    group_ids: [],
    provider_ids: [],
    pricing: [],
  })
}

// addRulePricingEntry appends an empty token price card to a provider cost rule.
function addRulePricingEntry(sectionIdx: number, ruleIndex: number) {
  form.sections[sectionIdx].provider_stats_pricing_rules[ruleIndex].pricing.push({
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
    cache_write_5m_price: null,
    cache_write_1h_price: null,
    cache_write_30m_price: null,
    cache_write_12h_price: null,
    cache_read_price: null,
    image_input_price: null,
    image_output_price: null,
    per_request_price: null,
    intervals: [],
    time_pricing: createDefaultTimePricingForm(),
  })
}

// removeProviderStatsRule removes a cost rule and clears all keyed provider searches. Rule
// indexes shift after removal, so pending work and stored suggestions are discarded before
// those indexes identify different rows.
function removeProviderStatsRule(sectionIdx: number, ruleIndex: number) {
  form.sections[sectionIdx].provider_stats_pricing_rules.splice(ruleIndex, 1)
  // Cancel indexed provider searches before clearing their state so a shifted or reset rule
  // index cannot receive suggestions from its previous owner.
  ruleProviderSearchRunner.clearAll()
  clearAllRuleProviderSearchState()
}

// removeRulePricingEntry deletes the indexed price card from its provider cost rule.
function removeRulePricingEntry(sectionIdx: number, ruleIndex: number, pricingIndex: number) {
  form.sections[sectionIdx].provider_stats_pricing_rules[ruleIndex].pricing.splice(pricingIndex, 1)
}

// getGroupNameById returns a loaded group name for rule display. Missing groups use their
// numeric identifier so stored group associations remain visible even when option loading is
// incomplete.
function getGroupNameById(groupId: number): string {
  const group = allGroups.value.find((g) => g.id === groupId)
  return group ? group.name : `#${groupId}`
}

// SimpleProvider identifies a searchable provider and its platform for cost rules.
interface SimpleProvider {
  id: number // stable record identifier
  name: string // record display name
  platform: string // provider platform identifier
}

// Rule provider keyword, result, and visibility maps use section-and-index keys so each cost
// rule can show its own search suggestions.
const ruleProviderSearchKeyword = ref<Record<string, string>>({})
const ruleProviderSearchResults = ref<Record<string, SimpleProvider[]>>({})
const showRuleProviderDropdown = ref<Record<string, boolean>>({})

// ruleProviderNameCache remembers provider names selected or loaded for saved rules, allowing
// numeric IDs to remain the stored association values.
const ruleProviderNameCache = ref<Record<number, string>>({})

// ruleProviderSearchRunner owns debounced provider requests and cancellation, publishing
// results into the cost rule matching the request key.
const ruleProviderSearchRunner = useKeyedDebouncedSearch<SimpleProvider[]>({
  delay: SEARCH_DEBOUNCE_MS,
  search: async (keyword, { signal }) => {
    const res = await adminAPI.providers.list(1, 20, { search: keyword }, { signal })
    return res.items.map((a) => ({ id: a.id, name: a.name, platform: a.platform }))
  },
  onSuccess: (key, result) => {
    ruleProviderSearchResults.value[key] = result
  },
  onError: (key) => {
    // A failed provider query clears this cost rule's suggestions so results from a previous
    // keyword are not presented as matches for the failed search.
    ruleProviderSearchResults.value[key] = []
  },
})

// onRuleProviderSearchInput opens the rule suggestions and schedules its keyed search.
function onRuleProviderSearchInput(scope: string, ruleIndex: number) {
  const key = `${scope}-${ruleIndex}`
  showRuleProviderDropdown.value[key] = true
  ruleProviderSearchRunner.trigger(key, ruleProviderSearchKeyword.value[key] || '')
}

// onRuleProviderSearchFocus opens a cost rule's provider suggestions. Missing results trigger a
// search with the stored keyword, while an existing result list is reused for immediate
// selection.
function onRuleProviderSearchFocus(scope: string, ruleIndex: number) {
  const key = `${scope}-${ruleIndex}`
  showRuleProviderDropdown.value[key] = true
  if (!ruleProviderSearchResults.value[key]?.length) {
    ruleProviderSearchRunner.trigger(key, ruleProviderSearchKeyword.value[key] || '')
  }
}

// selectRuleProvider adds a provider ID once and remembers its display name. It then clears the
// matching search keyword and hides suggestions, leaving other cost rules and their provider
// selections intact.
function selectRuleProvider(
  rule: {
    provider_ids: number[] // selected provider identifiers
  },
  provider: SimpleProvider,
  scope: string,
  ruleIndex: number,
) {
  if (!rule.provider_ids.includes(provider.id)) {
    rule.provider_ids.push(provider.id)
    ruleProviderNameCache.value[provider.id] = provider.name
  }
  const key = `${scope}-${ruleIndex}`
  ruleProviderSearchKeyword.value[key] = ''
  showRuleProviderDropdown.value[key] = false
}

// removeRuleProvider removes a matching provider ID from the selected cost rule.
function removeRuleProvider(
  rule: {
    provider_ids: number[] // selected provider identifiers
  },
  providerId: number,
) {
  const idx = rule.provider_ids.indexOf(providerId)
  if (idx !== -1) rule.provider_ids.splice(idx, 1)
}

// getRuleProviderLabel formats a cached provider name together with its identifier. Missing
// names display the numeric ID, allowing rules to remain readable when provider detail requests
// fail.
function getRuleProviderLabel(providerId: number): string {
  const name = ruleProviderNameCache.value[providerId]
  return name ? `${name} #${providerId}` : `#${providerId}`
}

// handleRuleProviderClickOutside hides suggestions when the click is outside all provider
// search containers.
function handleRuleProviderClickOutside(event: MouseEvent) {
  const target = event.target as HTMLElement
  if (!target.closest('.rule-provider-search-container')) {
    Object.keys(showRuleProviderDropdown.value).forEach((key) => {
      showRuleProviderDropdown.value[key] = false
    })
  }
}

// clearAllRuleProviderSearchState replaces search keywords, results, and dropdown state with
// empty records.
function clearAllRuleProviderSearchState() {
  ruleProviderSearchKeyword.value = {}
  ruleProviderSearchResults.value = {}
  showRuleProviderDropdown.value = {}
}

// providerStatsRulesToAPI flattens cost rules across sections into ordered API entries. Group
// and provider ID arrays are copied, and price cards with no models are omitted before amount
// conversion.
function providerStatsRulesToAPI(): ProviderStatsPricingRule[] {
  return form.sections.flatMap((section) =>
    section.provider_stats_pricing_rules.map((rule) => ({
      name: rule.name,
      group_ids: [...rule.group_ids],
      provider_ids: [...rule.provider_ids],
      pricing: rule.pricing.filter((entry) => entry.models.length > 0).map(pricingEntryToAPI),
    })),
  )
}

// formToAPI flattens selected groups and model price cards across draft sections. Price cards
// with empty model lists are omitted, and configured cards pass through the shared API amount
// converter.
function formToAPI(): {
  group_ids: number[] // selected group identifiers
  model_pricing: ModelPricingEntry[] // model billing price cards
} {
  const group_ids: number[] = []
  const model_pricing: ModelPricingEntry[] = []
  for (const section of form.sections) {
    group_ids.push(...section.group_ids)
    for (const entry of section.model_pricing) {
      if (entry.models.length) model_pricing.push(pricingEntryToAPI(entry))
    }
  }
  return { group_ids, model_pricing }
}

// apiToForm creates one editable section from a saved price configuration. It copies selected
// group and provider IDs, converts each price card, and uses empty arrays for missing stored
// collections.
function apiToForm(pricingConfig: PricingConfig): PricingSection[] {
  return [
    {
      group_ids: [...(pricingConfig.group_ids ?? [])],
      model_pricing: (pricingConfig.model_pricing ?? []).map(pricingEntryFromAPI),
      provider_stats_pricing_rules: (pricingConfig.provider_stats_pricing_rules ?? []).map(
        (rule) => ({
          name: rule.name,
          group_ids: [...rule.group_ids],
          provider_ids: [...rule.provider_ids],
          pricing: rule.pricing.map(pricingEntryFromAPI),
        }),
      ),
    },
  ]
}

// loadPricingConfigs fetches the current table page and aborts its predecessor. A response
// publishes rows and total only when its controller remains current and un-aborted.
// Cancellation exceptions are consumed, while other errors become page notifications.
//
// The finally block clears loading and controller state only for the request still owned by the
// page. This lets a newer filter or pagination request stay visibly pending when an older read
// finishes or is canceled.
async function loadPricingConfigs() {
  // Cancel the preceding page read before creating its replacement so accepted results and
  // loading ownership follow the newest table query.
  if (abortController) abortController.abort()
  const ctrl = new AbortController()
  abortController = ctrl
  loading.value = true

  try {
    const response = await adminAPI.pricing.list(
      pagination.page,
      pagination.page_size,
      {
        status: filters.status || undefined,
        search: searchQuery.value || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order,
      },
      { signal: ctrl.signal },
    )

    if (ctrl.signal.aborted || abortController !== ctrl) return
    pricingConfigs.value = response.items || []
    pagination.total = response.total
  } catch (error: unknown) {
    const e = error as {
      name?: string // exception classification name
      code?: string // HTTP-client error code
    }
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') return
    appStore.showError(
      extractApiErrorMessage(
        error,
        t('admin.pricing.loadError', 'Failed to load price configurations'),
      ),
    )
  } finally {
    if (abortController === ctrl) {
      loading.value = false
      abortController = null
    }
  }
}

// loadGroups fetches all group options for the pricing editor. Errors are logged while the
// prior options remain available, and the group-loading flag is released after either
// completion path.
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

// loadAllPricingConfigsForConflict fetches up to one thousand configurations for association
// checks. If the request fails, it uses the currently loaded table page, which limits available
// conflict information to those rows.
async function loadAllPricingConfigsForConflict() {
  try {
    const response = await adminAPI.pricing.list(1, 1000)
    allPricingConfigsForConflict.value = response.items || []
  } catch {
    // When complete conflict lookup fails, retain checks available from the visible page;
    // associations outside that page remain unavailable to this local check.
    allPricingConfigsForConflict.value = pricingConfigs.value
  }
}

// searchTimeout owns the pending search debounce timer, which a subsequent input clears before
// scheduling another query.
let searchTimeout: ReturnType<typeof setTimeout>
// handleSearch debounces configuration search and reloads from the first page. Repeated input
// clears the preceding timer; dispatched list requests are superseded by loadPricingConfigs
// when the latest timer runs.
function handleSearch() {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadPricingConfigs()
  }, 300)
}

// handlePageChange selects a configuration page and reloads its table rows.
function handlePageChange(page: number) {
  pagination.page = page
  loadPricingConfigs()
}

// handlePageSizeChange updates configuration page size and reloads from page one.
function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadPricingConfigs()
}

// handleSort changes configuration ordering and reloads the first matching page.
function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadPricingConfigs()
}

// resetForm replaces price sections and billing settings with fresh defaults. It returns to the
// basic tab, cancels provider searches, clears suggestions, and discards cached provider names
// for the next editor session.
function resetForm() {
  form.name = ''
  form.description = ''
  form.status = 'active'

  form.billing_settings = defaultBillingSettings()
  form.billing_model_source = 'group_mapped'
  form.sections = [emptyPricingSection()]
  activeTab.value = 'basic'
  // Cancel indexed provider searches before clearing their state so a shifted or reset rule
  // index cannot receive suggestions from its previous owner.
  ruleProviderSearchRunner.clearAll()
  clearAllRuleProviderSearchState()
  ruleProviderNameCache.value = {}
}

// openCreateDialog initializes a new price draft and loads its group and conflict options. The
// dialog opens after both loads complete, using their respective failure handling when an
// option request fails.
async function openCreateDialog() {
  editingPricingConfig.value = null
  resetForm()
  await Promise.all([loadGroups(), loadAllPricingConfigsForConflict()])
  showDialog.value = true
}

// openEditDialog hydrates a saved configuration and loads its group and provider references.
// Stored billing values override defaults when present, price cards are converted for editing,
// and provider names are populated before the dialog opens.
async function openEditDialog(pricingConfig: PricingConfig) {
  editingPricingConfig.value = pricingConfig
  form.name = pricingConfig.name
  form.description = pricingConfig.description || ''
  form.status = pricingConfig.status

  form.billing_settings = Object.fromEntries(
    Object.entries(defaultBillingSettings()).map(([key, fallback]) => [
      key,
      pricingConfig[key as keyof PricingConfig] ?? fallback,
    ]),
  ) as typeof form.billing_settings
  form.billing_model_source = pricingConfig.billing_model_source || 'group_mapped'
  await Promise.all([loadGroups(), loadAllPricingConfigsForConflict()])
  form.sections = apiToForm(pricingConfig)

  // Populate ruleProviderNameCache for existing rule providers
  await populateRuleProviderNameCache()

  showDialog.value = true
}

// populateRuleProviderNameCache fetches details for distinct provider IDs used by cost rules.
// Requests settle independently so successful names remain available despite other lookup
// failures; missing names continue to display as numeric IDs.
async function populateRuleProviderNameCache() {
  const allProviderIds = new Set<number>()
  for (const section of form.sections) {
    for (const rule of section.provider_stats_pricing_rules) {
      for (const id of rule.provider_ids) {
        allProviderIds.add(id)
      }
    }
  }
  if (allProviderIds.size === 0) return

  // Settle lookups independently so successful names can be cached when another request fails.
  // Rejected IDs retain any existing cache entry, and uncached IDs display numerically.
  const ids = [...allProviderIds]
  const results = await Promise.allSettled(ids.map((id) => adminAPI.providers.getById(id)))
  for (let i = 0; i < ids.length; i++) {
    const result = results[i]
    if (result.status === 'fulfilled') {
      ruleProviderNameCache.value[ids[i]] = result.value.name
    }
  }
}

// closeDialog hides pricing editing, clears its saved identity, and resets the draft.
function closeDialog() {
  showDialog.value = false
  editingPricingConfig.value = null
  resetForm()
}

// handleSubmit validates and persists the entire price configuration draft. It reveals invalid
// native inputs across tabs, then checks names, billing settings, group selection, model
// patterns, and price requirements. Validation failures select the responsible tab and leave
// the draft open.
//
// Accepted input is flattened and converted before creating or updating the configuration.
// Successful persistence closes and resets the dialog before refreshing the list. API errors
// retain the draft and display an operation-specific message, while finally releases the
// submission flag.
async function handleSubmit() {
  if (submitting.value) return
  const invalid = Array.from(
    dialogForm.value?.querySelectorAll<HTMLInputElement>('input') ?? [],
  ).find((field) => field.willValidate && !field.validity.valid)
  if (invalid) {
    activeTab.value =
      invalid.closest<HTMLElement>('[data-pricing-panel]')?.dataset.pricingPanel ?? 'basic'
    await nextTick()
    invalid.focus()
    invalid.reportValidity()
    return
  }
  if (!form.name.trim()) {
    activeTab.value = 'basic'
    appStore.showError(t('admin.pricing.nameRequired', 'Please enter a price configuration name'))
    return
  }

  const settingsError = validateBillingSettings(form.billing_settings)
  if (settingsError) {
    activeTab.value = 'billing'
    appStore.showError(t(`admin.pricing.billingSettings.${settingsError}`))
    return
  }

  // Model tags are validated before serialization so empty cards cannot disappear silently when
  // the payload builder skips unconfigured entries.
  for (const section of form.sections) {
    if (section.group_ids.length === 0) {
      appStore.showError(t('admin.pricing.noGroupsSelected'))
      activeTab.value = 'pricing'
      return
    }
    for (const entry of section.model_pricing) {
      if (entry.models.length === 0) {
        appStore.showError(t('admin.pricing.emptyModelsInPricing'))
        activeTab.value = 'pricing'
        return
      }
    }
  }

  // Compare complete normalized model patterns before saving so duplicate names and overlapping
  // wildcard scopes receive the same conflict handling.
  for (const section of form.sections) {
    const pricingError = validatePricingForm(section.model_pricing, t)
    if (pricingError) {
      appStore.showError(pricingError)
      activeTab.value = 'pricing'
      return
    }
  }

  // Provider cost cards with a configured price factor need an actual participating amount
  // before submission can apply that factor.
  for (const section of form.sections) {
    const entries = [...section.provider_stats_pricing_rules.flatMap((rule) => rule.pricing)]
    for (const entry of entries) {
      if (entry.models.length === 0 || toNullableNumber(entry.price_multiplier) === null) continue
      if (!hasExplicitPricing(entry)) {
        const models = entry.models.join(', ')
        appStore.showError(
          t(
            'admin.pricing.form.priceMultiplierRequiresPrice',
            { models },
            `模型 ${models} 配置定价倍率时，必须至少填写一项价格`,
          ),
        )
        activeTab.value = 'pricing'
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
        ...billingSettingsToAPI(form.billing_settings),

        provider_stats_pricing_rules: providerStatsRulesToAPI(),
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
        ...billingSettingsToAPI(form.billing_settings),

        provider_stats_pricing_rules: providerStatsRulesToAPI(),
      }
      await adminAPI.pricing.create(req)
      appStore.showSuccess(t('admin.pricing.createSuccess', 'PricingConfig created'))
    }
    closeDialog()
    loadPricingConfigs()
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        editingPricingConfig.value
          ? t('admin.pricing.updateError', 'Failed to update price configuration')
          : t('admin.pricing.createError', 'Failed to create price configuration'),
      ),
    )
  } finally {
    submitting.value = false
  }
}

// togglePricingConfigStatus persists the opposite enabled state for a configuration. If the
// result conflicts with the current status filter, it reloads the list; otherwise it updates
// the existing row after persistence succeeds.
async function togglePricingConfigStatus(pricingConfig: PricingConfig) {
  const newStatus = pricingConfig.status === 'active' ? 'disabled' : 'active'
  try {
    await adminAPI.pricing.update(pricingConfig.id, { status: newStatus })
    if (filters.status && filters.status !== newStatus) {
      // The saved status falls outside this filter, so reload to replace the row and refresh
      // the server total for the remaining matching configurations.
      await loadPricingConfigs()
    } else {
      pricingConfig.status = newStatus
    }
  } catch (error) {
    appStore.showError(t('admin.pricing.updateError', 'Failed to update price configuration'))
    console.error('Error toggling pricingConfig status:', error)
  }
}

// handleDelete selects a price configuration and opens its deletion confirmation.
function handleDelete(pricingConfig: PricingConfig) {
  deletingPricingConfig.value = pricingConfig
  showDeleteDialog.value = true
}

// confirmDelete removes the selected configuration and refreshes the table after success.
// Missing selection skips deletion, while API failures leave the confirmation available and
// display the extracted server error.
async function confirmDelete() {
  if (!deletingPricingConfig.value) return

  try {
    await adminAPI.pricing.remove(deletingPricingConfig.value.id)
    appStore.showSuccess(t('admin.pricing.deleteSuccess', 'PricingConfig deleted'))
    showDeleteDialog.value = false
    deletingPricingConfig.value = null
    loadPricingConfigs()
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(
        error,
        t('admin.pricing.deleteError', 'Failed to delete price configuration'),
      ),
    )
  }
}

onMounted(() => {
  loadPricingConfigs()
  loadGroups()

  document.addEventListener('click', handleRuleProviderClickOutside)
})

// Clear delayed searches, abort the active list read, and remove document and provider-search
// work so the departed page stops publishing those results.
onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
  document.removeEventListener('click', handleRuleProviderClickOutside)
  // Cancel indexed provider searches before clearing their state so a shifted or reset rule
  // index cannot receive suggestions from its previous owner.
  ruleProviderSearchRunner.clearAll()
  clearAllRuleProviderSearchState()
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
