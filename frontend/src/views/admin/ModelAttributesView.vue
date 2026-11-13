<template>
  <AppLayout>
    <div
      role="tablist"
      :aria-label="t('admin.modelAttributes.title')"
      class="mb-4 flex border-b border-gray-200 dark:border-dark-700"
    >
      <button
        v-for="tab in ['configs', 'defaults'] as const"
        :key="tab"
        type="button"
        role="tab"
        :aria-selected="activeTab === tab"
        class="border-b-2 px-4 py-3 text-sm font-medium"
        :class="
          activeTab === tab
            ? 'border-primary-500 text-primary-700 dark:text-primary-300'
            : 'border-transparent text-gray-500 dark:text-dark-400'
        "
        @click="activeTab = tab"
      >
        {{ t(`admin.modelAttributes.tabs.${tab}`) }}
      </button>
    </div>
    <TablePageLayout>
      <template #filters>
        <div class="space-y-2">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <!-- Give mobile filters a full row so action buttons cannot collapse the search input beneath its icon. -->
            <div class="flex w-full min-w-0 flex-none items-center gap-2 sm:w-auto">
              <div class="input-icon-wrap min-w-0 flex-1 sm:w-64 sm:flex-none">
                <Icon name="search" size="md" class="input-icon text-gray-400 dark:text-gray-500" />
                <input
                  v-model="search"
                  type="text"
                  class="input input-has-icon"
                  :placeholder="searchPlaceholder"
                  :aria-label="searchPlaceholder"
                />
              </div>
              <Select
                v-if="activeTab === 'configs'"
                v-model="status"
                :options="statusOptions"
                class="w-32 shrink-0"
              />
              <FilterDropdown
                v-else
                :active-count="activeFilterCount"
                @reset="
                  provider = '';
                  capability = '';
                "
              >
                <FilterField :label="t('admin.modelAttributes.columns.provider')" icon="tag">
                  <FilterOptions v-model="provider" :options="providerOptions" searchable>
                    <template #icon="{ value }">
                      <ProviderIcon :brand="String(value)" size="16px" hide-fallback />
                    </template>
                  </FilterOptions>
                </FilterField>
                <FilterField :label="t('admin.modelAttributes.columns.capability')" icon="puzzle">
                  <FilterOptions v-model="capability" :options="capabilityOptions" />
                </FilterField>
              </FilterDropdown>
            </div>
            <div class="ml-auto flex shrink-0 flex-wrap items-center justify-end gap-2">
              <button
                class="btn btn-secondary btn-icon"
                :disabled="loading || updating"
                :aria-label="t('common.refresh')"
                @click="load"
              >
                <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
              </button>
              <button v-if="activeTab === 'configs'" class="btn btn-primary" @click="edit()">
                <Icon name="plus" size="sm" class="mr-2" />{{ t('admin.modelAttributes.create') }}
              </button>
              <button v-else class="btn btn-primary" :disabled="updating" @click="updateCatalog">
                {{
                  t(updating ? 'admin.pricing.defaults.updating' : 'admin.pricing.defaults.update')
                }}
              </button>
            </div>
          </div>
          <ModelCatalogInfo
            v-if="activeTab === 'defaults'"
            :version="version"
            :updated-at="updatedAt"
          />
          <p
            v-if="error || (activeTab === 'defaults' && catalogError)"
            role="alert"
            class="text-sm text-red-600 dark:text-red-400"
          >
            {{ error || catalogError }}
          </p>
        </div>
      </template>
      <template #table>
        <DataTable
          v-if="activeTab === 'configs'"
          :columns="configColumns"
          :data="configs"
          :loading="loading"
          column-order-storage-key="model-attribute-config-columns"
        >
          <template #cell-status="{ row }"
            ><Toggle :model-value="row.status === 'active'" @update:model-value="toggleStatus(row)"
          /></template>
          <template #cell-groups="{ row }">{{ row.group_ids.length }}</template>
          <template #cell-rules="{ row }">{{ row.rules.length }}</template>
          <template #cell-actions="{ row }">
            <!-- Row actions share the icon-above-label arrangement, with deletion using its danger hover color. -->
            <div class="flex items-center gap-1">
              <button
                type="button"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                :aria-label="t('common.edit')"
                @click="edit(row)"
              >
                <Icon name="edit" size="sm" />
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>
              <button
                type="button"
                class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
                :aria-label="t('common.delete')"
                @click="deleting = row"
              >
                <Icon name="trash" size="sm" />
                <span class="text-xs">{{ t('common.delete') }}</span>
              </button>
            </div>
          </template>
        </DataTable>
        <DataTable
          v-else
          :columns="defaultColumns"
          :data="defaults"
          :loading="loading"
          column-order-storage-key="model-default-attribute-columns"
        >
          <template #cell-name="{ row }">{{
            row.attributes.display_name ?? t('admin.modelAttributes.unknown')
          }}</template>
          <template #cell-context="{ row }">{{
            row.attributes.context?.toLocaleString() ?? t('admin.modelAttributes.unknown')
          }}</template>
          <template #cell-output="{ row }">{{
            row.attributes.output_limit?.toLocaleString() ?? t('admin.modelAttributes.unknown')
          }}</template>
          <template #cell-actions="{ row }">
            <button
              type="button"
              class="flex flex-col items-center gap-0.5 rounded-control p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              :title="t('admin.modelAttributes.details')"
              :aria-label="t('admin.modelAttributes.details')"
              @click="detail = row"
            >
              <Icon name="eye" size="sm" />
              <span class="text-xs">{{ t('admin.modelAttributes.details') }}</span>
            </button>
          </template>
        </DataTable>
      </template>
      <template #pagination>
        <Pagination
          v-if="total"
          :page="page"
          :total="total"
          :page-size="pageSize"
          @update:page="
            page = $event;
            load();
          "
          @update:page-size="
            pageSize = $event;
            page = 1;
            load();
          "
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="showEditor"
      :title="t(form.id ? 'admin.modelAttributes.edit' : 'admin.modelAttributes.create')"
      width="extra-wide"
      :body-scroll="false"
      @close="showEditor = false"
    >
      <form
        id="attribute-form"
        class="flex min-h-0 flex-1 flex-col"
        novalidate
        @submit.prevent="save"
      >
        <p v-if="formError" role="alert" class="mb-4 text-sm text-red-600 dark:text-red-400">
          {{ formError }}
        </p>
        <SettingsTabs
          ref="tabsRef"
          id-prefix="model-attribute-editor"
          :label="t(form.id ? 'admin.modelAttributes.edit' : 'admin.modelAttributes.create')"
          :tabs="editorTabs"
        >
          <template #basic>
            <div class="space-y-4">
              <div class="grid gap-4 sm:grid-cols-2">
                <label
                  ><span class="input-label">{{ t('common.name') }}</span
                  ><input v-model="form.name" required maxlength="100" class="input"
                /></label>
                <div>
                  <label class="input-label">{{ t('common.status') }}</label
                  ><Select
                    v-model="form.status"
                    :aria-label="t('common.status')"
                    :options="editStatusOptions"
                  />
                </div>
                <label class="sm:col-span-2"
                  ><span class="input-label">{{
                    t('admin.modelAttributes.configDescription')
                  }}</span
                  ><textarea v-model="form.description" class="input" rows="2" />
                </label>
              </div>
              <div>
                <label class="input-label">
                  {{ t('admin.modelAttributes.groups') }}
                  <span v-if="form.group_ids.length" class="ml-1 font-normal text-gray-400">
                    ({{ t('admin.pricing.form.selectedCount', { count: form.group_ids.length }) }})
                  </span>
                </label>
                <!-- Group selection reuses price configuration badges and selected-row backgrounds. -->
                <div
                  class="max-h-menu overflow-auto rounded-control border border-gray-200 p-3 dark:border-dark-600"
                >
                  <p v-if="groupsLoading" class="py-2 text-center text-xs text-gray-500">
                    {{ t('common.loading') }}
                  </p>
                  <p v-else-if="!groups.length" class="py-2 text-center text-xs text-gray-500">
                    {{ t('admin.pricing.form.noGroupsAvailable') }}
                  </p>
                  <div v-else class="flex flex-wrap gap-2">
                    <label
                      v-for="group in groups"
                      :key="group.id"
                      class="inline-flex max-w-full cursor-pointer items-center gap-2 rounded-control p-1.5 transition-colors hover:bg-gray-50 dark:hover:bg-dark-700"
                      :class="
                        form.group_ids.includes(group.id)
                          ? 'bg-primary-50 dark:bg-primary-500/8 dark:text-primary-500'
                          : ''
                      "
                    >
                      <input
                        v-model="form.group_ids"
                        type="checkbox"
                        :value="group.id"
                        class="h-4 w-4 shrink-0 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                      />
                      <GroupBadge
                        :name="group.name"
                        :display-brand="group.display_brand"
                        :rate-multiplier="group.rate_multiplier"
                        class="min-w-0"
                      />
                    </label>
                  </div>
                </div>
              </div>
            </div>
          </template>
          <template #rules>
            <RuleListEditor
              :items="form.rules"
              :title="t('admin.modelAttributes.rules')"
              :empty-text="t('admin.modelAttributes.emptyRules')"
              :item-label="(index) => t('common.ruleIndex', { index: index + 1 })"
              variant="card"
              @add="addRule()"
              @remove="form.rules.splice($event, 1)"
              @move="moveRule"
            >
              <template #row="{ item }">
                <div class="space-y-4">
                  <div>
                    <label class="input-label">{{ t('admin.modelAttributes.models') }}</label>
                    <ModelTagInput
                      :models="item.models"
                      :aria-label="t('admin.modelAttributes.models')"
                      :placeholder="t('admin.modelAttributes.modelHint')"
                      @update:models="onModelsUpdate(item, $event)"
                    />
                  </div>
                  <div>
                    <label class="input-label">{{ t('admin.pricing.ruleMode') }}</label>
                    <Select
                      :model-value="isReferenceRule(item) ? 'reference' : 'custom'"
                      :aria-label="t('admin.pricing.ruleMode')"
                      :options="[
                        { value: 'custom', label: t('admin.pricing.customMode') },
                        { value: 'reference', label: t('admin.pricing.referenceMode') },
                      ]"
                      @update:model-value="setReferenceMode(item, $event === 'reference')"
                    />
                  </div>
                  <ModelReferenceInput
                    v-if="isReferenceRule(item)"
                    :model-value="item.reference_model || ''"
                    @update:model-value="setReferenceModel(item, $event)"
                    @change="loadRuleDefaults(item)"
                  />
                  <ModelAttributesFields
                    v-else
                    v-model="item.attributes"
                    :inherited="ruleDefaults.get(item)?.attributes"
                  />
                  <ModelAttributeRouteFields v-model="item.match" />
                  <ModelAttributeDisclosure
                    v-if="ruleDefaults.get(item)?.attributes"
                    :title="t('admin.modelAttributes.defaultReference')"
                    :meta="ruleDefaults.get(item)?.model"
                  >
                    <ModelAttributesSummary :attributes="ruleDefaults.get(item)!.attributes!" />
                  </ModelAttributeDisclosure>
                </div>
              </template>
            </RuleListEditor>
          </template>
          <template #preview>
            <div class="space-y-4">
              <p class="input-hint">{{ t('admin.modelAttributes.previewHint') }}</p>
              <div class="grid items-end gap-4 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]">
                <div>
                  <label class="input-label">{{ t('admin.modelAttributes.groups') }}</label
                  ><Select
                    v-model="previewGroup"
                    :aria-label="t('admin.modelAttributes.groups')"
                    :options="previewGroups"
                  />
                </div>
                <label
                  ><span class="input-label">{{ t('admin.modelAttributes.previewModel') }}</span
                  ><input v-model="previewModel" class="input"
                /></label>
                <button
                  type="button"
                  class="btn btn-secondary"
                  :disabled="previewLoading || !previewGroup || !previewModel.trim()"
                  @click="preview"
                >
                  <Icon name="eye" size="sm" class="mr-2" />{{ t('admin.modelAttributes.preview') }}
                </button>
              </div>
              <p v-if="previewError" role="alert" class="text-sm text-red-600 dark:text-red-400">
                {{ previewError }}
              </p>
              <p
                v-else-if="previewRoutes?.length === 0"
                class="rounded-control border border-dashed border-gray-200 px-3 py-4 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400"
              >
                {{ t('admin.modelAttributes.previewEmpty') }}
              </p>
              <div
                v-for="(route, index) in previewRoutes ?? []"
                :key="index"
                class="space-y-4 rounded-surface border border-gray-200 p-4 dark:border-dark-600"
              >
                <div class="flex flex-wrap items-center gap-2">
                  <span
                    class="break-all font-mono text-sm font-medium text-gray-900 dark:text-dark-50"
                    >{{ route.target.model }}</span
                  >
                  <span
                    v-for="tag in routeTags(route)"
                    :key="tag"
                    class="rounded-compact bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-dark-200"
                    >{{ tag }}</span
                  >
                </div>
                <p
                  v-if="route.reference_model"
                  class="break-all text-sm text-gray-500 dark:text-gray-400"
                  >{{ route.target.model }} → {{ route.reference_model }}</p
                >
                <ModelAttributesSummary :attributes="route.attributes" />
                <ModelAttributeDisclosure :title="t('admin.modelAttributes.fieldSource')">
                  <dl class="space-y-2 text-sm">
                    <div
                      v-for="(source, field) in route.sources"
                      :key="field"
                      class="flex flex-wrap justify-between gap-2"
                    >
                      <dt class="text-gray-500 dark:text-dark-400">
                        {{ attributeFieldLabel(field) }}
                      </dt>
                      <dd class="text-gray-900 dark:text-dark-50">{{ sourceLabel(source) }}</dd>
                    </div>
                  </dl>
                </ModelAttributeDisclosure>
              </div>
            </div>
          </template>
        </SettingsTabs>
      </form>
      <template #footer
        ><div class="flex justify-end gap-2">
          <button class="btn btn-secondary" @click="showEditor = false">
            {{ t('common.cancel') }}</button
          ><button
            form="attribute-form"
            type="submit"
            class="btn btn-primary"
            :disabled="saving || groupsLoading"
          >
            {{ t('common.save') }}
          </button>
        </div></template
      >
    </BaseDialog>

    <BaseDialog :show="!!detail" :title="detail?.model ?? ''" @close="detail = null">
      <div v-if="detail" class="space-y-4">
        <p class="text-sm text-gray-500">{{ detail.source }} · {{ detail.provider }}</p>
        <p v-if="detail.canonical_model_id" class="break-all text-sm">
          {{ detail.canonical_model_id }}
        </p>
        <ModelAttributesSummary :attributes="detail.attributes" />
      </div>
    </BaseDialog>
    <ConfirmDialog
      :show="!!deleting"
      :title="t('common.delete')"
      :message="t('admin.modelAttributes.deleteConfirm', { name: deleting?.name })"
      :danger="true"
      @confirm="remove"
      @cancel="deleting = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { newContentID } from '@/i18n/content'
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'
import Select from '@/components/common/Select.vue'
import FilterDropdown from '@/components/common/FilterDropdown.vue'
import FilterField from '@/components/common/FilterField.vue'
import FilterOptions from '@/components/common/FilterOptions.vue'
import { MODEL_CAPABILITY_ICONS } from '@/components/common/modelCapabilityIcons'
import type { FilterOption } from '@/components/common/filterPanel'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import SettingsTabs from '@/components/common/settings/SettingsTabs.vue'
import ModelReferenceInput from '@/components/admin/model-attributes/ModelReferenceInput.vue'
import { validateModelReferences } from '@/components/admin/model-attributes/modelReferences'
import ModelAttributeDisclosure from '@/components/admin/model-attributes/ModelAttributeDisclosure.vue'
import ModelAttributesFields from '@/components/admin/model-attributes/ModelAttributesFields.vue'
import ModelAttributeRouteFields from '@/components/admin/model-attributes/ModelAttributeRouteFields.vue'
import ModelCatalogInfo from '@/components/admin/model-attributes/ModelCatalogInfo.vue'
import ModelTagInput from '@/components/admin/pricing/ModelTagInput.vue'
import ModelAttributesSummary from '@/components/common/ModelAttributesSummary.vue'
import {
  modelAttributesAPI,
  type AttributeConfig,
  type AttributeRule,
  type DefaultAttributes,
  type ResolvedAttributes,
} from '@/api/admin/modelAttributes'
import { adminAPI } from '@/api/admin'
import type { AdminGroup } from '@/api/groups.types'
import { attributeCapabilities, type ModelAttributes } from '@/api/modelAttributes.types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'

// ModelAttributesView.vue manages ordered attribute configurations and displays catalog
// defaults. Editors keep a separate configuration draft, per-rule reference attributes, and
// route preview results. Saved rules associate model patterns and optional route conditions
// with groups, while the defaults tab provides catalog lookup.
//
// List requests use sequence numbers so newer tab or filter selections supersede older
// responses. Preview requests use a separate sequence invalidated by draft, group, and model
// changes. Reference lookups are accepted while the editor remains open and the rule still
// names the requested model. Each operation publishes errors through the corresponding page or
// form state.

// t resolves the current locale messages used by labels, validation, and action notifications.
const { t, te } = useI18n()
// activeTab selects configuration management or catalog defaults, and searchPlaceholder
// describes the model or configuration search for that selection.
const activeTab = ref<'configs' | 'defaults'>('configs')
const searchPlaceholder = computed(() =>
  t(
    activeTab.value === 'configs'
      ? 'admin.modelAttributes.searchConfigs'
      : 'admin.modelAttributes.searchModels',
  ),
)
// search, status, provider, and capability supply independent query constraints; the active tab
// determines which constraints the API receives.
const search = ref(''),
  status = ref(''),
  provider = ref(''),
  capability = ref('')
// activeFilterCount reports selected provider and capability filters on the defaults toolbar,
// excluding text search and configuration status.
const activeFilterCount = computed(() => Number(!!provider.value) + Number(!!capability.value))
// page, pageSize, and total hold the shared pagination state for the currently selected
// configurations or defaults table.
const page = ref(1),
  pageSize = ref(20),
  total = ref(0)
// The loading flags distinguish list reads, catalog updates, configuration saves, editor
// visibility, and group-option loading for their respective controls.
const loading = ref(false),
  updating = ref(false),
  saving = ref(false),
  showEditor = ref(false),
  groupsLoading = ref(false)
// error and formError identify page and editor failures, while catalogError, version, and
// updatedAt describe the loaded catalog snapshot.
const error = ref(''),
  formError = ref(''),
  catalogError = ref(''),
  version = ref(''),
  updatedAt = ref('')
// configs and defaults retain separate table records, providers supplies catalog filter
// choices, and groups supplies configuration association options.
const configs = ref<AttributeConfig[]>([]),
  defaults = ref<DefaultAttributes[]>([]),
  providers = ref<string[]>([])
const groups = ref<AdminGroup[]>([])
// detail selects a catalog row for inspection, while deleting identifies the configuration
// awaiting removal through the confirmation dialog.
const detail = ref<DefaultAttributes | null>(null),
  deleting = ref<AttributeConfig | null>(null)
// blank creates an active attribute configuration for the new editor. Name and description
// begin empty, and fresh group and rule lists keep pending additions separate from other
// configuration drafts.
const blank = (): AttributeConfig => ({
  name: '',
  description: '',
  status: 'active',
  group_ids: [],
  rules: [],
})
// form owns a copied attribute configuration draft so rule and group edits are submitted
// together through the editor save operation.
const form = ref<AttributeConfig>(blank())
// referenceModes retains each rule's mode separately from its editable target and fields.
const referenceModes = reactive(new WeakMap<AttributeRule, boolean>())

// isReferenceRule reads the selected draft mode for attribute rendering and serialization.
// New rules default to custom until setReferenceMode is called; edit initializes saved
// rules before their fields are rendered.
function isReferenceRule(rule: AttributeRule): boolean {
  return referenceModes.get(rule) ?? false
}

// setReferenceMode switches serialization while retaining both sets of draft values.
function setReferenceMode(rule: AttributeRule, enabled: boolean) {
  referenceModes.set(rule, enabled)
  resetPreview()
  void loadRuleDefaults(rule)
}

// setReferenceModel updates the target and clears its previous catalog comparison.
// Removing the retained lookup identity also rejects a pending response for the
// old target. The input's change event starts the next lookup after editing finishes.
function setReferenceModel(rule: AttributeRule, model: string) {
  rule.reference_model = model
  // Discard old reference evidence immediately so typing cannot display another
  // model's attributes or accept an earlier pending result under the new target.
  ruleDefaults.delete(rule)
}

// formPayload sends reference targets or custom overrides for preview and saving. The editable
// form retains translation revisions while its source is empty so clearing and retyping an
// existing name can still pass the server's revision check.
const formPayload = computed<AttributeConfig>(() => ({
  ...form.value,
  rules: form.value.rules.map((draft) => {
    const { reference_model, ...rule } = draft
    if (isReferenceRule(draft))
      return { ...rule, reference_model: reference_model?.trim(), attributes: {} }
    if (rule.attributes.display_name_localization?.source !== '') return rule
    const attributes = { ...rule.attributes }
    delete attributes.display_name
    delete attributes.display_name_localization
    return { ...rule, attributes }
  }),
}))
// tabsRef exposes editor validation and selection, while editorTabs supplies the basic, rule,
// and preview sections with translated labels.
const tabsRef = ref<InstanceType<typeof SettingsTabs> | null>(null)
const editorTabs = computed(() =>
  (['basic', 'rules', 'preview'] as const).map((key) => ({
    key,
    label: t(`admin.modelAttributes.editorTabs.${key}`),
  })),
)
// Each rule uses its first concrete model for catalog reference labels; those values provide
// hints rather than draft overrides.
const ruleDefaults = reactive(
  new WeakMap<
    AttributeRule,
    {
      model: string // reference model identifier
      attributes?: ModelAttributes // loaded reference attributes
    }
  >(),
)
// Preview group, model, error, and loading state describe the current draft query independently
// from the main configurations and catalog list.
const previewGroup = ref(''),
  previewModel = ref(''),
  previewError = ref(''),
  previewLoading = ref(false)
// Null means no current preview result, while an empty array means the completed preview found
// no matching routes.
const previewRoutes = ref<ResolvedAttributes[] | null>(null)
// previewGroups exposes group IDs as selector strings, and previewSequence invalidates prior
// preview work when the draft or target changes.
const previewGroups = computed(() =>
  groups.value.map((group) => ({ value: String(group.id), label: group.name })),
)
let previewSequence = 0
// editStatusOptions supplies the activation values administrators may save through the current
// record editor controls.
const editStatusOptions = computed(() => [
  { value: 'active', label: t('common.active') },
  { value: 'disabled', label: t('common.disabled') },
])
// statusOptions adds an unrestricted filter to the editor activation choices, while provider
// and capability options derive from loaded catalog metadata.
const statusOptions = computed(() => [
  { value: '', label: t('admin.modelAttributes.allStatuses') },
  ...editStatusOptions.value,
])
const providerOptions = computed(() => [
  { value: '', label: t('admin.modelAttributes.allProviders') },
  ...providers.value.map((value) => ({ value, label: value })),
])
const capabilityOptions = computed<FilterOption[]>(() => [
  { value: '', label: t('admin.modelAttributes.allCapabilities') },
  ...attributeCapabilities.map((value) => ({
    value,
    label: t(`admin.modelAttributes.fields.${value}`),
    icon: MODEL_CAPABILITY_ICONS[value],
  })),
])
// configColumns and defaultColumns define the distinct table fields used by configuration
// management and catalog attribute lookup.
const configColumns = computed(() =>
  ['name', 'status', 'groups', 'rules', 'actions'].map((key) => ({
    key,
    label: t(`admin.modelAttributes.columns.${key}`),
  })),
)
const defaultColumns = computed(() =>
  ['model', 'name', 'provider', 'context', 'output', 'actions'].map((key) => ({
    key,
    label: t(`admin.modelAttributes.columns.${key}`),
  })),
)
// sequence identifies the latest list request, while searchTimer owns the delayed text query
// that unmount clears before leaving the page.
let sequence = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined

// load fetches configurations or catalog defaults for the current tab and filters. Each call
// increments the list sequence before starting, and results publish only when that sequence
// still matches. The same check guards errors and completion so a superseded request cannot
// reset the current loading indicator.
//
// Configuration and default responses populate separate row collections but share pagination.
// Unmount increments the sequence, invalidating list work that completes after the page has
// been removed.
async function load() {
  // Advance the list generation before dispatch so a later tab or filter query invalidates
  // this response and its loading completion.
  const current = ++sequence
  loading.value = true
  error.value = ''
  try {
    if (activeTab.value === 'configs') {
      const result = await modelAttributesAPI.list({
        page: page.value,
        page_size: pageSize.value,
        search: search.value,
        status: status.value,
      })
      if (current !== sequence) return
      configs.value = result.items
      total.value = result.total
    } else {
      const result = await modelAttributesAPI.defaults({
        page: page.value,
        page_size: pageSize.value,
        search: search.value,
        provider: provider.value,
        capability: capability.value,
      })
      if (current !== sequence) return
      defaults.value = result.items
      total.value = result.total
      providers.value = result.providers
      version.value = result.version
      updatedAt.value = result.last_updated
      catalogError.value = result.last_error ?? ''
    }
  } catch (cause) {
    if (current === sequence) error.value = extractApiErrorMessage(cause, t('common.error'))
  } finally {
    if (current === sequence) loading.value = false
  }
}
// edit opens a copied configuration draft or a blank new configuration. Saved reference
// modes are captured for the copied rules before starting their catalog requests. Target
// text can then be cleared for editing while preview and save still validate a reference.
//
// The dialog opens with initial preview selections and independently loaded catalog
// comparisons. Group options load while the draft is editable, and group-loading failures
// remain visible in the editor. Mutations apply to the copied draft until save succeeds.
async function edit(config?: AttributeConfig) {
  form.value = config ? JSON.parse(JSON.stringify(config)) : blank()
  previewGroup.value = config?.group_ids[0] ? String(config.group_ids[0]) : ''
  previewModel.value =
    config?.rules.flatMap((rule) => rule.models).find((model) => !model.includes('*')) ?? ''
  formError.value = ''
  showEditor.value = true
  void tabsRef.value?.selectTab('basic')
  for (const rule of form.value.rules) {
    // Capture the stored mode before typing changes the target, keeping an empty
    // reference in reference mode until validation or an explicit mode selection.
    referenceModes.set(rule, Boolean(rule.reference_model))
    void loadRuleDefaults(rule)
  }
  groupsLoading.value = true
  try {
    groups.value = await adminAPI.groups.getAll()
  } catch (cause) {
    formError.value = extractApiErrorMessage(cause, t('common.error'))
  } finally {
    groupsLoading.value = false
  }
}
// moveRule repositions an existing draft rule while retaining its object identity.
function moveRule(from: number, to: number) {
  const item = form.value.rules.splice(from, 1)[0]
  if (item) form.value.rules.splice(to, 0, item)
}

// onModelsUpdate replaces a rule model list and refreshes its catalog reference.
function onModelsUpdate(rule: AttributeRule, models: string[]) {
  rule.models = models
  void loadRuleDefaults(rule)
}

// loadRuleDefaults fetches the mapped target or first concrete source model. A
// matching stored model skips repeat lookup, and a rule with no concrete model loses its
// reference entry. The WeakMap entry records the requested model before awaiting the API.
//
// Results are accepted while the editor is open and that rule still points to the same model.
// Failed lookup leaves manual editing available with no loaded reference attributes. The stored
// model also prevents immediate automatic retries for that same reference.
async function loadRuleDefaults(rule: AttributeRule) {
  const model = isReferenceRule(rule)
    ? rule.reference_model?.trim()
    : rule.models.find((item) => !item.includes('*'))
  if (model === ruleDefaults.get(rule)?.model) return
  if (!model) {
    ruleDefaults.delete(rule)
    return
  }
  ruleDefaults.set(rule, { model })
  try {
    const attributes = await modelAttributesAPI.getModelDefaultAttributes(model)
    // The request may finish after closure or a model change, so accept attributes only while
    // this rule still targets the requested reference.
    if (!showEditor.value || ruleDefaults.get(rule)?.model !== model) return
    ruleDefaults.set(rule, { model, attributes })
  } catch {
    // Reference lookup failure leaves the draft editable; users can enter overrides even when
    // the catalog provides no comparison values.
  }
}

// resetPreview invalidates pending route previews and clears their displayed routes, error,
// and loading state. Draft and selection watchers call it when the current preview inputs
// change, including replacement of the editor's draft. Advancing previewSequence makes
// independently finishing requests fail their publication checks for results, errors,
// and loading completion. The requests can continue in flight, while clearing the loading flag
// permits a fresh preview. Null routes identify the reset state until a current request succeeds.
function resetPreview() {
  // Invalidate older completions before clearing visible state so their result, error, or
  // finally handlers cannot overwrite a reset or a subsequent preview.
  previewSequence++
  previewRoutes.value = null
  previewError.value = ''
  previewLoading.value = false
}

// preview resolves the current draft for a selected group and nonempty model. Missing selection
// or a pending preview skips the request. A new sequence identifies the invocation and guards
// result, error, and loading updates when it completes.
//
// Draft or selection watchers call resetPreview, which advances that sequence and clears the
// previous result. Null routes represent an unrequested or reset preview, while a returned
// empty array represents a completed lookup with no matched routes.
async function preview() {
  if (validateModelReferences(form.value.rules.filter(isReferenceRule))) {
    previewError.value = t('admin.pricing.referenceInvalid')
    return
  }
  if (!previewGroup.value || !previewModel.value.trim() || previewLoading.value) return
  // Capture this preview generation before starting the request so completion handlers can
  // reject its result after a reset or a newer preview begins.
  const sequence = ++previewSequence
  previewLoading.value = true
  previewError.value = ''
  try {
    const result = await modelAttributesAPI.preview(
      Number(previewGroup.value),
      previewModel.value.trim(),
      formPayload.value,
    )
    if (sequence === previewSequence) previewRoutes.value = result.routes
  } catch (cause) {
    if (sequence === previewSequence)
      previewError.value = extractApiErrorMessage(cause, t('common.error'))
  } finally {
    if (sequence === previewSequence) previewLoading.value = false
  }
}

// routeTags builds nonempty provider and protocol labels for one resolved route.
function routeTags(route: ResolvedAttributes) {
  const {
    platform,
    provider_type,
    provider_mode,
    site,
    catalog_provider,
    client_protocol,
    upstream_protocol,
  } = route.target
  const platformLabel =
    CONCRETE_PLATFORM_OPTIONS.find((option) => option.value === platform)?.label ?? platform
  const protocols = [client_protocol, upstream_protocol].filter(Boolean).join(' → ')
  return [platformLabel, provider_type, provider_mode, site, catalog_provider, protocols].filter(
    (tag): tag is string => !!tag,
  )
}

// attributeFieldLabel translates an attribute source field using its matching field namespace.
// Media fields use the media labels, and unknown fields remain visible under their raw property
// name.
function attributeFieldLabel(field: string) {
  const key = field.startsWith('media.')
    ? `admin.modelAttributes.mediaFields.${field.slice(6)}`
    : `admin.modelAttributes.fields.${field}`
  return te(key) ? t(key) : field
}

// sourceLabel translates a known attribute source identifier for the preview. Unknown
// identifiers retain their raw name so newly returned source categories can still be displayed
// by the page.
function sourceLabel(source: string) {
  const key = `admin.modelAttributes.sources.${source}`
  return te(key) ? t(key) : source
}

// addRule appends an identified empty attribute rule to the active draft.
function addRule() {
  form.value.rules.push({ id: newContentID(), models: [], attributes: {} })
}

// save validates the editor and persists its current attribute configuration. It rejects rules
// without model tags after native field validation and selects the rules tab for that error.
// Successful persistence closes the editor before reloading the list.
//
// The saving flag suppresses calls that begin during a pending save and is released in finally.
// API failures retain the draft and publish a form error so the administrator can correct or
// retry the configuration.
async function save() {
  if (saving.value) return
  formError.value = ''
  if (validateModelReferences(form.value.rules.filter(isReferenceRule))) {
    formError.value = t('admin.pricing.referenceInvalid')
    void tabsRef.value?.selectTab('rules')
    return
  }
  if (tabsRef.value && !(await tabsRef.value.validate())) return
  // Tag controls do not participate in native input validation, so check their model lists
  // before persisting attribute rules.
  if (form.value.rules.some((rule) => rule.models.length === 0)) {
    formError.value = t('admin.modelAttributes.modelsRequired')
    void tabsRef.value?.selectTab('rules')
    return
  }
  saving.value = true
  try {
    // Persist the validated draft before closing the editor and reloading configurations so
    // the list reflects a completed save attempt.
    await modelAttributesAPI.save(formPayload.value)
    showEditor.value = false
    await load()
  } catch (cause) {
    formError.value = extractApiErrorMessage(cause, t('common.error'))
  } finally {
    saving.value = false
  }
}
// toggleStatus saves the opposite active state for the selected configuration. After
// persistence it reloads the page; failures display a page error while the existing
// configuration list remains available.
async function toggleStatus(config: AttributeConfig) {
  try {
    await modelAttributesAPI.save({
      ...config,
      status: config.status === 'active' ? 'disabled' : 'active',
    })
    await load()
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('common.error'))
  }
}
// remove clears the pending delete selection and removes its saved configuration ID. Missing
// identifiers skip deletion; successful removal reloads the list, and failures are displayed as
// page errors.
async function remove() {
  const id = deleting.value?.id
  deleting.value = null
  if (!id) return
  try {
    await modelAttributesAPI.remove(id)
    await load()
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('common.error'))
  }
}
// updateCatalog requests a catalog refresh and then reloads the active tab. Repeated calls
// during the update are ignored, and failures retain the displayed data while setting the page
// error.
async function updateCatalog() {
  if (updating.value) return
  updating.value = true
  error.value = ''
  try {
    await modelAttributesAPI.update()
    await load()
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('common.error'))
  } finally {
    updating.value = false
  }
}
watch([activeTab, status, provider, capability], () => {
  page.value = 1
  load()
})
// Draft and preview-target changes invalidate pending resolution, preventing a completed
// response from describing inputs that the administrator has already edited.
watch([form, previewGroup, previewModel], resetPreview, { deep: true })
watch(search, () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => {
    page.value = 1
    load()
  }, SEARCH_DEBOUNCE_MS)
})
onMounted(load)
// Invalidate list requests, close reference acceptance, and clear delayed search work when
// the page leaves; preview generations are managed separately by draft watchers.
onUnmounted(() => {
  sequence++
  showEditor.value = false
  clearTimeout(searchTimer)
})
</script>
