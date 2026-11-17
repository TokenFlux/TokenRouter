<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'

import Icon from '@/components/icons/Icon.vue'
import { computed, onMounted, ref } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { TABLE_DESKTOP_MEDIA_QUERY } from '@/constants/layout'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import { adminAPI } from '@/api'
import { opsAPI } from '@/api/admin/ops'
import type { AlertRule, MetricType, Operator } from '../types'
import type { OpsSeverity } from '@/api/admin/ops'
import { formatDateTime } from '../utils/opsFormatters'

// OpsAlertRulesCard.vue edits persisted Ops alert rules through a local draft
// and validation messages. Metric definitions supply translated descriptions and
// recommendations, while group-specific metrics require a positive group
// identifier before submission. Existing rules are cloned for editing so changes
// remain local until the save request.
//
// The group selector writes into the optional filters object and removes that
// object when its last entry is cleared. Create, update, and delete operations
// refresh the rule list after the server mutation succeeds. A failed refresh can
// therefore show an empty list even though the preceding mutation completed
// successfully.

// Translation and application notifications provide rule editor labels and API
// feedback for this component instance.
const { t } = useI18n()
const appStore = useAppStore()

// isDesktopViewport uses the shared desktop breakpoint to choose rule tables or
// compact cards for narrower screens.
const isDesktopViewport = useMediaQuery(TABLE_DESKTOP_MEDIA_QUERY)

// Rule list state keeps retrieval progress separate from the editor draft and
// its mutation progress.
const loading = ref(false)
const rules = ref<AlertRule[]>([])

// load retrieves persisted rules and replaces the visible list. Retrieval
// failures report an application error and clear existing rows, while the
// loading flag resets after either completion path.
async function load() {
  loading.value = true
  try {
    rules.value = await opsAPI.listAlertRules()
  } catch (err: any) {
    // Discard previous rules after retrieval failure; the error notification
    // distinguishes this empty display from a successful query with no rules.
    console.error('[OpsAlertRulesCard] Failed to load rules', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertRules.loadFailed'))
    rules.value = []
  } finally {
    loading.value = false
  }
}

// Load rules and group choices when the editor mounts; these independent reads
// populate list content and group-filter choices separately.
onMounted(() => {
  load()
  loadGroups()
})

// sortedRules presents newer identifiers first using a copied array so the API
// response order remains untouched.
const sortedRules = computed(() => {
  return [...rules.value].sort((a, b) => (b.id || 0) - (a.id || 0))
})

// Editor state distinguishes a new rule from a persisted identifier and owns the
// mutable draft until save or dismissal.
const showEditor = ref(false)
const saving = ref(false)
const editingId = ref<number | null>(null)
const draft = ref<AlertRule | null>(null)

// MetricGroup separates metric choices into system, group, and provider
// sections.
type MetricGroup = 'system' | 'group' | 'provider'

// MetricDefinition describes a selectable metric and its recommended comparison
// settings.
interface MetricDefinition {
  type: MetricType // metric API identity code
  group: MetricGroup // metric selector section identity
  label: string // translated display label text
  description: string // translated metric explanation text
  // recommendedOperator and recommendedThreshold describe suggested comparison
  // settings for this metric. The optional unit labels the threshold magnitude
  // in the editor explanation.
  recommendedOperator: Operator // suggested metric comparison operator
  recommendedThreshold: number // suggested comparison threshold value
  unit?: string // optional metric unit label
}

// groupMetricTypes identifies metrics whose evaluation requires a concrete group
// ID in the rule filters.
const groupMetricTypes = new Set<MetricType>([
  'group_available_providers',
  'group_available_ratio',
  'group_rate_limit_ratio',
])

// parsePositiveInt accepts positive numeric values or parseable integer string
// prefixes. Null, booleans, nonfinite numbers, and nonpositive values return
// null for group-filter validation and selector normalization.
function parsePositiveInt(value: unknown): number | null {
  if (value == null) return null
  if (typeof value === 'boolean') return null
  const n = typeof value === 'number' ? value : Number.parseInt(String(value), 10)
  return Number.isFinite(n) && n > 0 ? n : null
}

// groupOptionsBase stores API group identities and labels before adding the
// optional all-groups choice.
const groupOptionsBase = ref<SelectOption[]>([])

// loadGroups retrieves group identifiers and names for rule filtering. A
// retrieval failure clears the available choices and logs the error; existing
// draft filters remain available for later editing.
async function loadGroups() {
  try {
    const list = await adminAPI.groups.getAll()
    groupOptionsBase.value = list.map((g) => ({ value: g.id, label: g.name }))
  } catch (err) {
    // Keep the editor usable with no fetched group choices when this independent
    // read fails, retaining the current draft for later correction.
    console.error('[OpsAlertRulesCard] Failed to load groups', err)
    groupOptionsBase.value = []
  }
}

// Group metric selection governs whether an all-groups option is permitted and
// how the draft filter is exposed.
const isGroupMetricSelected = computed(() => {
  const metricType = draft.value?.metric_type
  return metricType ? groupMetricTypes.has(metricType) : false
})

const draftGroupId = computed<number | null>({
  // get exposes the draft group filter as a normalized selector value. Missing
  // drafts or nonpositive filter values return null through parsePositiveInt,
  // allowing the group control to represent an unselected filter.
  get() {
    return parsePositiveInt(draft.value?.filters?.group_id)
  },
  // set updates the draft group filter or removes it when cleared. Removing the
  // final filter also removes the filters object, while a missing draft leaves
  // the setter with nothing to update.
  set(value) {
    if (!draft.value) return
    if (value == null) {
      if (!draft.value.filters) return
      delete draft.value.filters.group_id
      if (Object.keys(draft.value.filters).length === 0) {
        delete draft.value.filters
      }
      return
    }
    if (!draft.value.filters) draft.value.filters = {}
    draft.value.filters.group_id = value
  },
})

const groupOptions = computed<SelectOption[]>(() => {
  if (isGroupMetricSelected.value) return groupOptionsBase.value
  return [
    { value: null, label: t('admin.ops.alertRules.form.allGroups') },
    ...groupOptionsBase.value,
  ]
})

// Metric definitions pair backend metric codes with translated descriptions,
// recommended comparisons, and display units for the editor.
const metricDefinitions = computed(() => {
  return [
    // System-level metrics
    {
      type: 'success_rate',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.successRate'),
      description: t('admin.ops.alertRules.metricDescriptions.successRate'),
      recommendedOperator: '<',
      recommendedThreshold: 99,
      unit: '%',
    },
    {
      type: 'error_rate',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.errorRate'),
      description: t('admin.ops.alertRules.metricDescriptions.errorRate'),
      recommendedOperator: '>',
      recommendedThreshold: 1,
      unit: '%',
    },
    {
      type: 'upstream_error_rate',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.upstreamErrorRate'),
      description: t('admin.ops.alertRules.metricDescriptions.upstreamErrorRate'),
      recommendedOperator: '>',
      recommendedThreshold: 1,
      unit: '%',
    },
    {
      type: 'cpu_usage_percent',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.cpu'),
      description: t('admin.ops.alertRules.metricDescriptions.cpu'),
      recommendedOperator: '>',
      recommendedThreshold: 80,
      unit: '%',
    },
    {
      type: 'memory_usage_percent',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.memory'),
      description: t('admin.ops.alertRules.metricDescriptions.memory'),
      recommendedOperator: '>',
      recommendedThreshold: 80,
      unit: '%',
    },
    {
      type: 'disk_usage_percent',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.disk'),
      description: t('admin.ops.alertRules.metricDescriptions.disk'),
      recommendedOperator: '>',
      recommendedThreshold: 85,
      unit: '%',
    },
    {
      type: 'concurrency_queue_depth',
      group: 'system',
      label: t('admin.ops.alertRules.metrics.queueDepth'),
      description: t('admin.ops.alertRules.metricDescriptions.queueDepth'),
      recommendedOperator: '>',
      recommendedThreshold: 10,
    },

    // Group-level metrics (requires group_id filter)
    {
      type: 'group_available_providers',
      group: 'group',
      label: t('admin.ops.alertRules.metrics.groupAvailableProviders'),
      description: t('admin.ops.alertRules.metricDescriptions.groupAvailableProviders'),
      recommendedOperator: '<',
      recommendedThreshold: 1,
    },
    {
      type: 'group_available_ratio',
      group: 'group',
      label: t('admin.ops.alertRules.metrics.groupAvailableRatio'),
      description: t('admin.ops.alertRules.metricDescriptions.groupAvailableRatio'),
      recommendedOperator: '<',
      recommendedThreshold: 50,
      unit: '%',
    },
    {
      type: 'group_rate_limit_ratio',
      group: 'group',
      label: t('admin.ops.alertRules.metrics.groupRateLimitRatio'),
      description: t('admin.ops.alertRules.metricDescriptions.groupRateLimitRatio'),
      recommendedOperator: '>',
      recommendedThreshold: 10,
      unit: '%',
    },

    // Provider-level metrics
    {
      type: 'provider_rate_limited_count',
      group: 'provider',
      label: t('admin.ops.alertRules.metrics.providerRateLimitedCount'),
      description: t('admin.ops.alertRules.metricDescriptions.providerRateLimitedCount'),
      recommendedOperator: '>',
      recommendedThreshold: 0,
    },
    {
      type: 'provider_error_count',
      group: 'provider',
      label: t('admin.ops.alertRules.metrics.providerErrorCount'),
      description: t('admin.ops.alertRules.metricDescriptions.providerErrorCount'),
      recommendedOperator: '>',
      recommendedThreshold: 0,
    },
    {
      type: 'provider_error_ratio',
      group: 'provider',
      label: t('admin.ops.alertRules.metrics.providerErrorRatio'),
      description: t('admin.ops.alertRules.metricDescriptions.providerErrorRatio'),
      recommendedOperator: '>',
      recommendedThreshold: 5,
      unit: '%',
    },
    {
      type: 'overload_provider_count',
      group: 'provider',
      label: t('admin.ops.alertRules.metrics.overloadProviderCount'),
      description: t('admin.ops.alertRules.metricDescriptions.overloadProviderCount'),
      recommendedOperator: '>',
      recommendedThreshold: 0,
    },
  ] satisfies MetricDefinition[]
})

// selectedMetricDefinition resolves the draft metric description; missing or
// unlisted metric codes leave the selection description unavailable.
const selectedMetricDefinition = computed(() => {
  const metricType = draft.value?.metric_type
  if (!metricType) return null
  return metricDefinitions.value.find((m) => m.type === metricType) ?? null
})

// metricOptions renders translated group headings followed by selectable metric
// entries in system, group, and provider order.
const metricOptions = computed(() => {
  // buildGroup builds one disabled heading followed by its selectable metrics.
  // Groups without definitions return no options, so the selector never receives
  // an empty section heading for display.
  const buildGroup = (group: MetricGroup): SelectOption[] => {
    const items = metricDefinitions.value.filter((m) => m.group === group)
    if (items.length === 0) return []
    const headerValue = `__group__${group}`
    return [
      {
        value: headerValue,
        label: t(`admin.ops.alertRules.metricGroups.${group}`),
        disabled: true,
        kind: 'group',
      },
      ...items.map((m) => ({ value: m.type, label: m.label })),
    ]
  }

  return [...buildGroup('system'), ...buildGroup('group'), ...buildGroup('provider')]
})

// Comparison, severity, and evaluation-window choices use the exact values
// accepted by the alert rule editor.
const operatorOptions = computed(() => {
  const ops: Operator[] = ['>', '>=', '<', '<=', '==', '!=']
  return ops.map((o) => ({ value: o, label: o }))
})

const severityOptions = computed(() => {
  const sev: OpsSeverity[] = ['P0', 'P1', 'P2', 'P3']
  return sev.map((s) => ({ value: s, label: s }))
})

const windowOptions = computed(() => {
  const windows = [1, 5, 60]
  return windows.map((m) => ({ value: m, label: `${m}m` }))
})

// newRuleDraft creates an enabled error-rate rule with editor defaults.
// Durations are in minutes, email notification starts enabled, and the caller
// receives a fresh object for local edits.
function newRuleDraft(): AlertRule {
  return {
    name: '',
    description: '',
    enabled: true,
    metric_type: 'error_rate',
    operator: '>',
    threshold: 1,
    window_minutes: 1,
    sustained_minutes: 2,
    severity: 'P1',
    cooldown_minutes: 10,
    notify_email: true,
  }
}

// openCreate opens a fresh rule draft with no persisted editing identifier.
function openCreate() {
  editingId.value = null
  draft.value = newRuleDraft()
  showEditor.value = true
}

// openEdit opens a JSON-cloned rule draft and remembers its persisted
// identifier. The clone isolates nested filters during editing, and an absent
// identifier causes save to use the create endpoint.
function openEdit(rule: AlertRule) {
  editingId.value = rule.id ?? null
  // Clone nested filter data before editing so changes in the dialog draft cannot
  // mutate the fetched rule displayed in the list.
  draft.value = JSON.parse(JSON.stringify(rule))
  showEditor.value = true
}

// editorValidation checks required metric inputs and duration limits before
// save, including the group requirement of group-specific metrics.
const editorValidation = computed(() => {
  const errors: string[] = []
  const r = draft.value
  if (!r) return { valid: true, errors }
  if (!r.name || !r.name.trim()) errors.push(t('admin.ops.alertRules.validation.nameRequired'))
  if (!r.metric_type) errors.push(t('admin.ops.alertRules.validation.metricRequired'))
  if (groupMetricTypes.has(r.metric_type) && !parsePositiveInt(r.filters?.group_id)) {
    errors.push(t('admin.ops.alertRules.validation.groupIdRequired'))
  }
  if (!r.operator) errors.push(t('admin.ops.alertRules.validation.operatorRequired'))
  if (!(typeof r.threshold === 'number' && Number.isFinite(r.threshold)))
    errors.push(t('admin.ops.alertRules.validation.thresholdRequired'))
  if (!(
    typeof r.window_minutes === 'number' &&
    Number.isFinite(r.window_minutes) &&
    [1, 5, 60].includes(r.window_minutes)
  )) {
    errors.push(t('admin.ops.alertRules.validation.windowRange'))
  }
  if (!(
    typeof r.sustained_minutes === 'number' &&
    Number.isFinite(r.sustained_minutes) &&
    r.sustained_minutes >= 1 &&
    r.sustained_minutes <= 1440
  )) {
    errors.push(t('admin.ops.alertRules.validation.sustainedRange'))
  }
  if (!(
    typeof r.cooldown_minutes === 'number' &&
    Number.isFinite(r.cooldown_minutes) &&
    r.cooldown_minutes >= 0 &&
    r.cooldown_minutes <= 1440
  )) {
    errors.push(t('admin.ops.alertRules.validation.cooldownRange'))
  }
  return { valid: errors.length === 0, errors }
})

// save validates and persists the current rule draft through create or update.
// Success closes and clears the editor before refreshing the list; failure
// reports an error and retains whichever local state the completed steps
// produced.
async function save() {
  if (!draft.value) return
  if (!editorValidation.value.valid) {
    appStore.showError(
      editorValidation.value.errors[0] || t('admin.ops.alertRules.validation.invalid'),
    )
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await opsAPI.updateAlertRule(editingId.value, draft.value)
    } else {
      await opsAPI.createAlertRule(draft.value)
    }
    showEditor.value = false
    draft.value = null
    editingId.value = null
    await load()
    appStore.showSuccess(t('admin.ops.alertRules.saveSuccess'))
  } catch (err: any) {
    console.error('[OpsAlertRulesCard] Failed to save rule', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertRules.saveFailed'))
  } finally {
    saving.value = false
  }
}

// Deletion state retains the selected persisted rule until confirmation succeeds
// or the user cancels the dialog.
const showDeleteConfirm = ref(false)
const pendingDelete = ref<AlertRule | null>(null)

// requestDelete opens confirmation for the selected alert rule without modifying
// it.
function requestDelete(rule: AlertRule) {
  pendingDelete.value = rule
  showDeleteConfirm.value = true
}

// confirmDelete removes the pending persisted rule before reloading visible
// rules. Missing identifiers make the action return; failure reports an error,
// and completed server deletion is not rolled back by a later refresh.
async function confirmDelete() {
  if (!pendingDelete.value?.id) return
  try {
    // Wait for persisted deletion before clearing confirmation and refreshing rows,
    // so a rejected mutation leaves the selected rule available for retry.
    await opsAPI.deleteAlertRule(pendingDelete.value.id)
    showDeleteConfirm.value = false
    pendingDelete.value = null
    await load()
    appStore.showSuccess(t('admin.ops.alertRules.deleteSuccess'))
  } catch (err: any) {
    console.error('[OpsAlertRulesCard] Failed to delete rule', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.alertRules.deleteFailed'))
  }
}

// cancelDelete closes deletion confirmation and releases the pending rule
// selection.
function cancelDelete() {
  showDeleteConfirm.value = false
  pendingDelete.value = null
}
</script>

<template>
  <div
    class="rounded-surface bg-white p-6 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-900 dark:ring-dark-700"
  >
    <div class="mb-4 flex flex-wrap items-end justify-between gap-3 sm:gap-4">
      <div>
        <h3 class="text-sm font-bold text-gray-900 dark:text-white">
          {{ t('admin.ops.alertRules.title') }}
        </h3>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.ops.alertRules.description') }}
        </p>
      </div>

      <div class="flex items-center gap-2">
        <button class="btn btn-sm btn-primary" :disabled="loading" @click="openCreate">
          {{ t('admin.ops.alertRules.create') }}
        </button>
        <button
          class="flex items-center gap-1.5 rounded-control bg-gray-100 px-3 py-1.5 text-xs font-bold text-gray-700 transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50 dark:bg-dark-950 dark:text-gray-300 dark:hover:bg-dark-800"
          :disabled="loading"
          @click="load"
        >
          <Icon
            name="refresh"
            size="xs"
            :animate-on-hover="false"
            class="h-3.5 w-3.5"
            :class="{ 'animate-spin': loading }"
          />
          {{ t('common.refresh') }}
        </button>
      </div>
    </div>

    <div
      v-if="!loading && sortedRules.length === 0"
      class="rounded-surface border border-dashed border-gray-200 p-8 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-gray-400"
    >
      {{ t('admin.ops.alertRules.empty') }}
    </div>

    <div
      v-else
      class="max-h-[520px] overflow-hidden rounded-surface border border-gray-200 dark:border-dark-700"
    >
      <!-- check-ui-allow: Local alert-rule table height -->
      <div class="max-h-[520px] overflow-y-auto">
        <!-- check-ui-allow: Local alert-rule table height -->
        <div v-if="!isDesktopViewport" class="divide-y divide-gray-100 dark:divide-dark-800">
          <div
            v-if="loading && !sortedRules.length"
            role="status"
            :aria-label="t('common.loading')"
            aria-busy="true"
            data-loading-skeleton
          >
            <div
              v-for="row in 4"
              :key="row"
              class="space-y-2 p-4 border-b border-gray-100 last:border-0 dark:border-dark-800"
              aria-hidden="true"
            >
              <div class="flex items-start justify-between gap-2">
                <Skeleton :width="120" :height="16" />
                <Skeleton :width="50" :height="14" />
              </div>
              <Skeleton :width="140" :height="14" />
              <div class="flex items-center justify-between gap-2">
                <Skeleton :width="48" :height="14" />
                <div class="flex gap-2">
                  <Skeleton v-for="action in 2" :key="action" :width="48" :height="28" />
                </div>
              </div>
            </div>
          </div>
          <div v-else v-for="row in sortedRules" :key="row.id" class="space-y-2 p-4">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="text-xs font-bold text-gray-900 dark:text-white">{{ row.name }}</div>
                <div
                  v-if="row.description"
                  class="mt-0.5 line-clamp-2 text-xs text-gray-500 dark:text-gray-400"
                >
                  {{ row.description }}
                </div>
              </div>
              <span class="shrink-0 text-xs font-bold text-gray-700 dark:text-gray-200">{{
                row.severity
              }}</span>
            </div>
            <div class="text-xs text-gray-700 dark:text-gray-200">
              <span class="font-mono">{{ row.metric_type }}</span>
              <span class="mx-1 text-gray-400">{{ row.operator }}</span>
              <span class="font-mono">{{ row.threshold }}</span>
            </div>
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs text-gray-700 dark:text-gray-200">
                {{ row.enabled ? t('common.enabled') : t('common.disabled') }}
              </span>
              <div class="flex items-center gap-2">
                <button class="btn btn-sm btn-secondary" @click="openEdit(row)">
                  {{ t('common.edit') }}
                </button>
                <button class="btn btn-sm btn-danger" @click="requestDelete(row)">
                  {{ t('common.delete') }}
                </button>
              </div>
            </div>
            <div v-if="row.updated_at" class="text-xs text-gray-400">
              {{ formatDateTime(row.updated_at) }}
            </div>
          </div>
        </div>
        <table v-else class="min-w-full divide-y divide-gray-200 dark:divide-dark-700">
          <thead class="sticky top-0 z-10 bg-gray-50 dark:bg-dark-950">
            <tr>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertRules.table.name') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertRules.table.metric') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertRules.table.severity') }}
              </th>
              <th
                class="px-4 py-3 text-left text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertRules.table.enabled') }}
              </th>
              <th
                class="px-4 py-3 text-right text-xs font-bold tracking-wider text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.ops.alertRules.table.actions') }}
              </th>
            </tr>
          </thead>
          <TableSkeletonBody
            v-if="loading && !sortedRules.length"
            :columns="5"
            cell-class="px-4 py-3"
          />
          <tbody
            v-else
            class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900"
          >
            <tr
              v-for="row in sortedRules"
              :key="row.id"
              class="hover:bg-gray-50 dark:hover:bg-dark-700/50"
            >
              <td class="px-4 py-3">
                <div class="text-xs font-bold text-gray-900 dark:text-white">{{ row.name }}</div>
                <div
                  v-if="row.description"
                  class="mt-0.5 line-clamp-2 text-xs text-gray-500 dark:text-gray-400"
                >
                  {{ row.description }}
                </div>
                <div v-if="row.updated_at" class="mt-1 text-xs text-gray-400">
                  {{ formatDateTime(row.updated_at) }}
                </div>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-700 dark:text-gray-200">
                <span class="font-mono">{{ row.metric_type }}</span>
                <span class="mx-1 text-gray-400">{{ row.operator }}</span>
                <span class="font-mono">{{ row.threshold }}</span>
              </td>
              <td
                class="whitespace-nowrap px-4 py-3 text-xs font-bold text-gray-700 dark:text-gray-200"
              >
                {{ row.severity }}
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-xs text-gray-700 dark:text-gray-200">
                {{ row.enabled ? t('common.enabled') : t('common.disabled') }}
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-right text-xs">
                <button class="btn btn-sm btn-secondary" @click="openEdit(row)">
                  {{ t('common.edit') }}
                </button>
                <button class="ml-2 btn btn-sm btn-danger" @click="requestDelete(row)">
                  {{ t('common.delete') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <BaseDialog
      :show="showEditor"
      :title="
        editingId ? t('admin.ops.alertRules.editTitle') : t('admin.ops.alertRules.createTitle')
      "
      width="wide"
      @close="showEditor = false"
    >
      <div class="space-y-4">
        <div
          v-if="!editorValidation.valid"
          class="rounded-surface bg-red-50 p-4 text-xs text-red-700 dark:bg-red-900/30 dark:text-red-300"
        >
          <div class="font-bold">{{ t('admin.ops.alertRules.validation.title') }}</div>
          <ul class="mt-1 list-disc pl-5">
            <li v-for="e in editorValidation.errors" :key="e">{{ e }}</li>
          </ul>
        </div>

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div class="md:col-span-2">
            <label class="input-label">{{ t('admin.ops.alertRules.form.name') }}</label>
            <input v-model="draft!.name" class="input" type="text" />
          </div>

          <div class="md:col-span-2">
            <label class="input-label">{{ t('admin.ops.alertRules.form.description') }}</label>
            <input v-model="draft!.description" class="input" type="text" />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.metric') }}</label>
            <Select v-model="draft!.metric_type" :options="metricOptions" />
            <div
              v-if="selectedMetricDefinition"
              class="mt-1 space-y-0.5 text-xs text-gray-500 dark:text-gray-400"
            >
              <p>{{ selectedMetricDefinition.description }}</p>
              <p>
                {{
                  t('admin.ops.alertRules.hints.recommended', {
                    operator: selectedMetricDefinition.recommendedOperator,
                    threshold: selectedMetricDefinition.recommendedThreshold,
                    unit: selectedMetricDefinition.unit || '',
                  })
                }}
              </p>
            </div>
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.operator') }}</label>
            <Select v-model="draft!.operator" :options="operatorOptions" />
          </div>

          <div class="md:col-span-2">
            <label class="input-label">
              {{ t('admin.ops.alertRules.form.groupId') }}
              <span v-if="isGroupMetricSelected" class="ml-1 text-red-500">*</span>
            </label>
            <Select
              v-model="draftGroupId"
              :options="groupOptions"
              searchable
              :placeholder="t('admin.ops.alertRules.form.groupPlaceholder')"
              :error="isGroupMetricSelected && !draftGroupId"
            />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{
                isGroupMetricSelected
                  ? t('admin.ops.alertRules.hints.groupRequired')
                  : t('admin.ops.alertRules.hints.groupOptional')
              }}
            </p>
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.threshold') }}</label>
            <input v-model.number="draft!.threshold" class="input" type="number" />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.severity') }}</label>
            <Select v-model="draft!.severity" :options="severityOptions" />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.window') }}</label>
            <Select v-model="draft!.window_minutes" :options="windowOptions" />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.sustained') }}</label>
            <input
              v-model.number="draft!.sustained_minutes"
              class="input"
              type="number"
              min="1"
              max="1440"
            />
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.alertRules.form.cooldown') }}</label>
            <input
              v-model.number="draft!.cooldown_minutes"
              class="input"
              type="number"
              min="0"
              max="1440"
            />
          </div>

          <div
            class="flex items-center justify-between rounded-surface bg-gray-50 px-4 py-3 dark:bg-dark-900/50 md:col-span-2"
          >
            <span class="text-xs font-bold text-gray-700 dark:text-gray-200">{{
              t('admin.ops.alertRules.form.enabled')
            }}</span>
            <input
              v-model="draft!.enabled"
              type="checkbox"
              class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
            />
          </div>

          <div
            class="flex items-center justify-between rounded-surface bg-gray-50 px-4 py-3 dark:bg-dark-900/50 md:col-span-2"
          >
            <span class="text-xs font-bold text-gray-700 dark:text-gray-200">{{
              t('admin.ops.alertRules.form.notifyEmail')
            }}</span>
            <input
              v-model="draft!.notify_email"
              type="checkbox"
              class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
            />
          </div>
        </div>
      </div>

      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <button class="btn btn-secondary" :disabled="saving" @click="showEditor = false">
            {{ t('common.cancel') }}
          </button>
          <button class="btn btn-primary" :disabled="saving" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteConfirm"
      :title="t('admin.ops.alertRules.deleteConfirmTitle')"
      :message="t('admin.ops.alertRules.deleteConfirmMessage')"
      :confirmText="t('common.delete')"
      :cancelText="t('common.cancel')"
      @confirm="confirmDelete"
      @cancel="cancelDelete"
    />
  </div>
</template>
