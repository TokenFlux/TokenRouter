<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.advancedSchedulerScore.title')"
    width="extra-wide"
    @close="handleClose"
  >
    <div class="min-h-[320px] space-y-5">
      <div v-if="loadingOverview && !response" class="space-y-4" aria-busy="true">
        <div class="h-14 animate-pulse rounded-control bg-gray-100 dark:bg-dark-700"></div>
        <div class="h-10 animate-pulse rounded-control bg-gray-100 dark:bg-dark-700"></div>
        <div class="grid grid-cols-2 gap-3 md:grid-cols-5">
          <div v-for="index in 5" :key="index" class="h-20 animate-pulse rounded-control bg-gray-100 dark:bg-dark-700"></div>
        </div>
      </div>

      <div v-else-if="errorMessage" class="flex min-h-[240px] flex-col items-center justify-center gap-3 text-center">
        <Icon name="exclamationCircle" size="xl" class="text-red-500" />
        <p class="max-w-lg text-sm text-gray-600 dark:text-dark-300">{{ errorMessage }}</p>
        <button type="button" class="btn btn-secondary btn-sm" @click="refresh">
          <Icon name="refresh" size="sm" :class="loadingOverview || loadingDetail ? 'animate-spin' : ''" />
          {{ t('admin.accounts.advancedSchedulerScore.retry') }}
        </button>
      </div>

      <template v-else-if="response">
        <header class="flex flex-col gap-3 border-b border-gray-200 pb-4 dark:border-dark-600 sm:flex-row sm:items-start sm:justify-between">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
              <h4 class="truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ response.account.name }}</h4>
              <span class="font-mono text-xs text-gray-500 dark:text-dark-400">#{{ response.account.id }}</span>
              <span class="text-xs text-gray-500 dark:text-dark-400">{{ response.account.platform }} / {{ response.account.type }}</span>
              <span :class="response.account.status === 'active' ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'" class="text-xs font-medium">
                {{ response.account.status }}
              </span>
            </div>
            <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
              {{ t('admin.accounts.advancedSchedulerScore.generatedAt') }}: {{ formatTimestamp(response.generated_at) }}
              <span class="mx-1">·</span>
              {{ t('admin.accounts.advancedSchedulerScore.calculationVersion') }}: {{ response.calculation_version }}
            </p>
          </div>
          <button
            type="button"
            class="flex shrink-0 rounded-control border border-gray-200 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-white btn-icon-sm"
            :title="t('admin.accounts.advancedSchedulerScore.refresh')"
            :aria-label="t('admin.accounts.advancedSchedulerScore.refresh')"
            :disabled="loadingOverview || loadingDetail"
            @click="refresh"
          >
            <Icon name="refresh" size="sm" :class="loadingOverview || loadingDetail ? 'animate-spin' : ''" />
          </button>
        </header>

        <div v-if="groups.length === 0" class="flex min-h-[220px] flex-col items-center justify-center gap-2 text-center">
          <Icon name="calculator" size="xl" class="text-gray-400 dark:text-dark-500" />
          <p class="text-sm text-gray-600 dark:text-dark-300">{{ t('admin.accounts.advancedSchedulerScore.empty') }}</p>
        </div>

        <template v-else>
          <div class="-mx-1 overflow-x-auto px-1 pb-1">
            <div class="flex min-w-max gap-2" role="tablist" :aria-label="t('admin.accounts.advancedSchedulerScore.groups')">
              <button
                v-for="group in groups"
                :key="group.id"
                type="button"
                role="tab"
                :aria-selected="activeGroupID === group.id"
                :class="[
                  'flex min-h-12 flex-col justify-center rounded-control border px-3 text-left transition-colors',
                  activeGroupID === group.id
                    ? 'border-primary-500 bg-primary-50 text-primary-800 dark:border-primary-500 dark:bg-primary-500/10 dark:text-primary-200'
                    : 'border-gray-200 bg-white text-gray-700 hover:border-gray-300 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-300 dark:hover:border-dark-500'
                ]"
                @click="selectGroup(group.id)"
              >
                <span class="max-w-48 truncate text-sm font-medium">{{ group.name }}</span>
                <span class="mt-0.5 text-xs opacity-75">
                  {{ group.eligible ? formatNumber(group.final_score) : t('admin.accounts.advancedSchedulerScore.filtered') }}
                </span>
              </button>
            </div>
          </div>

          <div v-if="loadingDetail && !detail" class="space-y-4 py-3" aria-busy="true">
            <div class="h-12 animate-pulse rounded-control bg-gray-100 dark:bg-dark-700"></div>
            <div class="grid grid-cols-2 gap-3 md:grid-cols-5">
              <div v-for="index in 5" :key="index" class="h-20 animate-pulse rounded-control bg-gray-100 dark:bg-dark-700"></div>
            </div>
            <div class="h-48 animate-pulse rounded-control bg-gray-100 dark:bg-dark-700"></div>
          </div>

          <template v-else-if="detail">
            <section class="border-b border-gray-200 pb-5 dark:border-dark-600">
              <button
                type="button"
                class="flex w-full items-center justify-between gap-3 text-left"
                :aria-expanded="showScenario"
                @click="showScenario = !showScenario"
              >
                <div>
                  <h5 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.accounts.advancedSchedulerScore.scenario') }}</h5>
                  <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                    {{ detail.context.baseline ? t('admin.accounts.advancedSchedulerScore.baseline') : t('admin.accounts.advancedSchedulerScore.simulationActive') }}
                  </p>
                </div>
                <Icon name="chevronDown" size="sm" :class="['transition-transform', showScenario && 'rotate-180']" />
              </button>
              <div v-if="showScenario" class="mt-4 grid gap-3 border-t border-gray-100 pt-4 dark:border-dark-700 md:grid-cols-3">
                <label class="min-w-0">
                  <span class="input-label">{{ t('admin.accounts.advancedSchedulerScore.requestedModel') }}</span>
                  <input v-model.trim="scenario.requested_model" class="input" :placeholder="t('admin.accounts.advancedSchedulerScore.requestedModelPlaceholder')" />
                </label>
                <label class="min-w-0">
                  <span class="input-label">{{ t('admin.accounts.advancedSchedulerScore.sessionSticky') }}</span>
                  <Select
                    v-model="scenario.sticky_account_id"
                    :options="candidateOptions"
                    :placeholder="t('admin.accounts.advancedSchedulerScore.notSpecified')"
                    clearable
                  />
                </label>
                <label class="min-w-0">
                  <span class="input-label">{{ t('admin.accounts.advancedSchedulerScore.previousResponse') }}</span>
                  <Select
                    v-model="scenario.previous_response_account_id"
                    :options="candidateOptions"
                    :placeholder="t('admin.accounts.advancedSchedulerScore.notSpecified')"
                    clearable
                  />
                </label>
                <div class="md:col-span-3 flex flex-wrap items-center gap-2">
                  <button type="button" class="btn btn-primary btn-sm" :disabled="loadingDetail" @click="preview">
                    <Icon name="refresh" size="sm" :class="loadingDetail ? 'animate-spin' : ''" />
                    {{ t('admin.accounts.advancedSchedulerScore.recalculate') }}
                  </button>
                  <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingDetail" @click="restoreBaseline">
                    {{ t('admin.accounts.advancedSchedulerScore.restoreBaseline') }}
                  </button>
                </div>
              </div>
            </section>

            <section v-if="!detail.eligible" class="border-b border-gray-200 py-5 dark:border-dark-600">
              <div class="flex items-start gap-3 rounded-control border border-amber-200 bg-amber-50 p-3 text-sm text-amber-900 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
                <Icon name="exclamationTriangle" size="md" class="mt-0.5 shrink-0" />
                <div>
                  <p class="font-medium">{{ t('admin.accounts.advancedSchedulerScore.ineligible') }}</p>
                  <p class="mt-1 text-xs leading-5">{{ hardFilterText(detail.hard_filter_reasons) }}</p>
                </div>
              </div>
            </section>

            <template v-else-if="detail.score">
              <section class="border-b border-gray-200 py-5 dark:border-dark-600">
                <h5 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.accounts.advancedSchedulerScore.scoreSummary') }}</h5>
                <div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
                  <div v-for="item in scoreSummaryItems" :key="item.key" class="rounded-control border border-gray-200 px-3 py-2.5 dark:border-dark-600">
                    <p class="text-xs text-gray-500 dark:text-dark-400">{{ item.label }}</p>
                    <p class="mt-1 font-mono text-sm font-semibold text-gray-900 dark:text-gray-100">{{ item.value }}</p>
                  </div>
                </div>
                <p class="mt-3 text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.selectionExplanation') }}</p>
              </section>

              <section class="border-b border-gray-200 py-5 dark:border-dark-600">
                <div class="flex items-center justify-between gap-3">
                  <h5 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.accounts.advancedSchedulerScore.formula') }}</h5>
                  <button
                    type="button"
                    class="flex shrink-0 rounded-control border border-gray-200 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:border-dark-600 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-white btn-icon-sm"
                    :title="t('admin.accounts.advancedSchedulerScore.copyFormula')"
                    :aria-label="t('admin.accounts.advancedSchedulerScore.copyFormula')"
                    @click="copyFormula(detail.score.formula)"
                  >
                    <Icon name="copy" size="sm" />
                  </button>
                </div>
                <div class="mt-3 overflow-x-auto rounded-control border border-gray-200 bg-gray-50 px-3 py-3 dark:border-dark-600 dark:bg-dark-900">
                  <code class="block min-w-max whitespace-pre font-mono text-xs leading-6 text-gray-800 dark:text-dark-200">{{ detail.score.formula }}</code>
                </div>
              </section>

              <section class="border-b border-gray-200 py-5 dark:border-dark-600">
                <div class="flex flex-wrap items-baseline justify-between gap-2">
                  <h5 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.accounts.advancedSchedulerScore.metrics') }}</h5>
                  <p class="text-xs text-gray-500 dark:text-dark-400">{{ normalizationRangeText(detail.candidate_pool.normalization_ranges) }}</p>
                </div>
                <div class="mt-3 hidden overflow-x-auto lg:block">
                  <table class="w-full min-w-[860px] text-left text-xs">
                    <thead class="border-b border-gray-200 text-gray-500 dark:border-dark-600 dark:text-dark-400">
                      <tr>
                        <th class="px-2 py-2 font-medium">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.metric') }}</th>
                        <th class="px-2 py-2 font-medium">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.raw') }}</th>
                        <th class="px-2 py-2 font-medium">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.normalization') }}</th>
                        <th class="px-2 py-2 font-medium">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.normalized') }}</th>
                        <th class="px-2 py-2 font-medium">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.weight') }}</th>
                        <th class="px-2 py-2 font-medium">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.contribution') }}</th>
                        <th class="px-2 py-2 font-medium">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.source') }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="metric in detail.metrics" :key="metric.key" class="border-b border-gray-100 align-top dark:border-dark-700">
                        <td class="px-2 py-2.5 font-medium text-gray-900 dark:text-gray-100">{{ metricLabel(metric.key) }}</td>
                        <td class="px-2 py-2.5 text-gray-600 dark:text-dark-300">{{ metric.raw_value }}</td>
                        <td class="max-w-xs px-2 py-2.5 font-mono text-xs leading-5 text-gray-600 dark:text-dark-300">{{ metric.normalization }}</td>
                        <td class="px-2 py-2.5 font-mono text-gray-900 dark:text-gray-100">{{ formatNumber(metric.normalized_value) }}</td>
                        <td class="px-2 py-2.5 font-mono text-gray-900 dark:text-gray-100">{{ formatNumber(metric.weight) }}</td>
                        <td class="px-2 py-2.5 font-mono text-gray-900 dark:text-gray-100">{{ formatNumber(metric.weighted_contribution) }}</td>
                        <td class="px-2 py-2.5 text-gray-500 dark:text-dark-400">
                          <span>{{ metricStatus(metric) }}</span>
                          <span v-if="metric.observed_at" class="block pt-0.5">{{ formatTimestamp(metric.observed_at) }}</span>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <div class="mt-3 space-y-2 lg:hidden">
                  <details v-for="metric in detail.metrics" :key="metric.key" class="rounded-control border border-gray-200 px-3 py-2 dark:border-dark-600">
                    <summary class="flex cursor-pointer list-none items-center justify-between gap-3 text-sm">
                      <span class="font-medium text-gray-900 dark:text-gray-100">{{ metricLabel(metric.key) }}</span>
                      <span class="font-mono text-xs text-gray-600 dark:text-dark-300">{{ formatNumber(metric.weighted_contribution) }}</span>
                    </summary>
                    <dl class="mt-3 grid gap-2 border-t border-gray-100 pt-3 text-xs dark:border-dark-700">
                      <div><dt class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.raw') }}</dt><dd class="mt-0.5 text-gray-800 dark:text-dark-200">{{ metric.raw_value }}</dd></div>
                      <div><dt class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.normalization') }}</dt><dd class="mt-0.5 break-words font-mono text-xs leading-5 text-gray-800 dark:text-dark-200">{{ metric.normalization }}</dd></div>
                      <div class="grid grid-cols-3 gap-2"><div><dt class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.normalized') }}</dt><dd class="mt-0.5 font-mono">{{ formatNumber(metric.normalized_value) }}</dd></div><div><dt class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.weight') }}</dt><dd class="mt-0.5 font-mono">{{ formatNumber(metric.weight) }}</dd></div><div><dt class="text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.metricColumns.contribution') }}</dt><dd class="mt-0.5 font-mono">{{ formatNumber(metric.weighted_contribution) }}</dd></div></div>
                    </dl>
                  </details>
                </div>
              </section>
            </template>

            <section class="border-b border-gray-200 py-5 dark:border-dark-600">
              <h5 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.accounts.advancedSchedulerScore.candidatePool') }}</h5>
              <div class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
                <div class="rounded-control border border-gray-200 px-3 py-2.5 dark:border-dark-600"><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.poolTotal') }}</p><p class="mt-1 font-mono text-sm font-semibold">{{ detail.candidate_pool.total_candidates }}</p></div>
                <div class="rounded-control border border-gray-200 px-3 py-2.5 dark:border-dark-600"><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.poolEligible') }}</p><p class="mt-1 font-mono text-sm font-semibold">{{ detail.candidate_pool.eligible_candidates }}</p></div>
                <div class="rounded-control border border-gray-200 px-3 py-2.5 dark:border-dark-600"><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.poolExcluded') }}</p><p class="mt-1 font-mono text-sm font-semibold">{{ detail.candidate_pool.excluded_candidates }}</p></div>
                <div class="rounded-control border border-gray-200 px-3 py-2.5 dark:border-dark-600"><p class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.advancedSchedulerScore.poolTopK') }}</p><p class="mt-1 font-mono text-sm font-semibold">{{ detail.candidate_pool.top_k }}</p></div>
              </div>
              <p v-if="exclusionSummary" class="mt-3 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ exclusionSummary }}</p>
              <div v-if="detail.candidate_pool.candidates.length" class="mt-4 max-h-56 overflow-auto border-t border-gray-100 pt-3 dark:border-dark-700">
                <div v-for="candidate in detail.candidate_pool.candidates" :key="candidate.id" class="grid grid-cols-[2rem_minmax(0,1fr)_5rem_4rem] items-center gap-2 border-b border-gray-100 py-2 text-xs dark:border-dark-700">
                  <span class="font-mono text-gray-500 dark:text-dark-400">#{{ candidate.rank }}</span>
                  <span class="truncate text-gray-800 dark:text-dark-200">{{ candidate.name }} <span class="text-gray-400">#{{ candidate.id }}</span></span>
                  <span class="font-mono text-right text-gray-800 dark:text-dark-200">{{ formatNumber(candidate.final_score) }}</span>
                  <span class="text-right text-gray-500 dark:text-dark-400">{{ candidate.in_top_k ? t('admin.accounts.advancedSchedulerScore.inTopK') : '' }}</span>
                </div>
              </div>
            </section>

            <section class="grid gap-5 py-5" :class="detail.policy_signals.length ? 'lg:grid-cols-2' : ''">
              <div>
                <h5 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.accounts.advancedSchedulerScore.effectiveSettings') }}</h5>
                <dl data-testid="advanced-scheduler-effective-settings" class="mt-3 grid grid-cols-1 gap-x-6 border-t border-gray-100 text-xs dark:border-dark-700 sm:grid-cols-2">
                  <div v-for="setting in detail.effective_settings" :key="setting.key" class="grid grid-cols-[minmax(0,1fr)_auto] gap-3 border-b border-gray-100 py-2 dark:border-dark-700">
                    <dt class="truncate text-gray-600 dark:text-dark-300">{{ settingLabel(setting.key) }}</dt>
                    <dd class="flex items-baseline justify-end gap-2 whitespace-nowrap text-right"><span class="font-mono text-gray-900 dark:text-gray-100">{{ setting.value }}</span><span class="text-gray-400">{{ settingSourceLabel(setting.source) }}</span></dd>
                  </div>
                </dl>
              </div>
              <div v-if="detail.policy_signals.length">
                <h5 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.accounts.advancedSchedulerScore.policySignals') }}</h5>
                <div class="mt-3 space-y-2">
                  <div v-for="signal in detail.policy_signals" :key="signal.key" class="border-l-2 border-gray-200 pl-3 dark:border-dark-600">
                    <p class="text-xs font-medium text-gray-800 dark:text-dark-200">{{ policyLabel(signal.key) }} <span class="font-normal text-gray-400">{{ policyStateLabel(signal.state) }}</span></p>
                    <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ signal.detail }}</p>
                  </div>
                </div>
              </div>
            </section>
          </template>
        </template>
      </template>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import { Icon } from '@/components/icons'
import { useClipboard } from '@/composables/useClipboard'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type {
  AdvancedSchedulerScoreDiagnosticDetail,
  AdvancedSchedulerScoreDiagnosticMetric,
  AdvancedSchedulerScoreDiagnosticResponse,
  AdvancedSchedulerScorePreviewRequest
} from '@/api/admin/accounts'
import type { Account } from '@/types'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const response = ref<AdvancedSchedulerScoreDiagnosticResponse | null>(null)
const detail = computed<AdvancedSchedulerScoreDiagnosticDetail | undefined>(() => response.value?.detail)
const activeGroupID = ref<number | null>(null)
const loadingOverview = ref(false)
const loadingDetail = ref(false)
const errorMessage = ref('')
const showScenario = ref(false)
const detailCache = new Map<number, AdvancedSchedulerScoreDiagnosticResponse>()
const scenario = reactive<{
  requested_model: string
  sticky_account_id: number | null
  previous_response_account_id: number | null
}>({
  requested_model: '',
  sticky_account_id: null,
  previous_response_account_id: null
})

const groups = computed(() => response.value?.groups ?? [])
const candidateOptions = computed<SelectOption[]>(() => (detail.value?.candidate_pool.candidates ?? []).map(candidate => ({
  value: candidate.id,
  label: `${candidate.name} #${candidate.id}`
})))
const scoreSummaryItems = computed(() => {
  const score = detail.value?.score
  if (!score) return []
  return [
    { key: 'base', label: t('admin.accounts.advancedSchedulerScore.baseScore'), value: formatNumber(score.base_score) },
    { key: 'sticky', label: t('admin.accounts.advancedSchedulerScore.stickyBonus'), value: formatNumber(score.sticky_bonus) },
    { key: 'final', label: t('admin.accounts.advancedSchedulerScore.finalScore'), value: formatNumber(score.final_score) },
    { key: 'rank', label: t('admin.accounts.advancedSchedulerScore.rank'), value: score.in_top_k ? `${score.rank} / Top-${detail.value?.candidate_pool.top_k ?? 0}` : `${score.rank}` },
    { key: 'probability', label: t('admin.accounts.advancedSchedulerScore.selectionProbability'), value: score.selection_probability === undefined ? '—' : formatPercent(score.selection_probability) }
  ]
})
const exclusionSummary = computed(() => {
  const exclusions = detail.value?.candidate_pool.exclusion_reasons ?? {}
  const items = Object.entries(exclusions)
  if (items.length === 0) return ''
  return items.map(([reason, count]) => `${hardFilterLabel(reason)} ${count}`).join(' · ')
})

watch(
  () => [props.show, props.account?.id] as const,
  ([visible]) => {
    if (visible) {
      void loadOverview()
    } else {
      resetState()
    }
  },
  { immediate: true }
)

function resetState() {
  response.value = null
  activeGroupID.value = null
  loadingOverview.value = false
  loadingDetail.value = false
  errorMessage.value = ''
  showScenario.value = false
  detailCache.clear()
  resetScenario()
}

function resetScenario() {
  scenario.requested_model = ''
  scenario.sticky_account_id = null
  scenario.previous_response_account_id = null
}

async function loadOverview(preferredGroupID?: number | null) {
  if (!props.account) return
  loadingOverview.value = true
  errorMessage.value = ''
  detailCache.clear()
  try {
    const next = await adminAPI.accounts.getAdvancedSchedulerScore(props.account.id)
    response.value = next
    const targetGroupID = preferredGroupID && next.groups.some(group => group.id === preferredGroupID)
      ? preferredGroupID
      : next.groups[0]?.id ?? null
    activeGroupID.value = targetGroupID
    if (targetGroupID) {
      await loadDetail(targetGroupID, false)
    }
  } catch (error: any) {
    errorMessage.value = error?.message || t('admin.accounts.advancedSchedulerScore.loadFailed')
  } finally {
    loadingOverview.value = false
  }
}

async function selectGroup(groupID: number) {
  if (activeGroupID.value === groupID && detail.value) return
  activeGroupID.value = groupID
  resetScenario()
  showScenario.value = false
  errorMessage.value = ''
  await loadDetail(groupID, false)
}

async function loadDetail(groupID: number, force: boolean) {
  if (!props.account) return
  if (!force) {
    const cached = detailCache.get(groupID)
    if (cached) {
      response.value = cached
      return
    }
  }
  loadingDetail.value = true
  try {
    const next = await adminAPI.accounts.getAdvancedSchedulerScore(props.account.id, groupID)
    detailCache.set(groupID, next)
    response.value = next
  } catch (error: any) {
    errorMessage.value = error?.message || t('admin.accounts.advancedSchedulerScore.loadFailed')
  } finally {
    loadingDetail.value = false
  }
}

async function preview() {
  if (!props.account || !activeGroupID.value) return
  loadingDetail.value = true
  errorMessage.value = ''
  const payload: AdvancedSchedulerScorePreviewRequest = {
    group_id: activeGroupID.value
  }
  if (scenario.requested_model) payload.requested_model = scenario.requested_model
  if (scenario.sticky_account_id) payload.sticky_account_id = scenario.sticky_account_id
  if (scenario.previous_response_account_id) payload.previous_response_account_id = scenario.previous_response_account_id
  try {
    response.value = await adminAPI.accounts.previewAdvancedSchedulerScore(props.account.id, payload)
  } catch (error: any) {
    errorMessage.value = error?.message || t('admin.accounts.advancedSchedulerScore.previewFailed')
  } finally {
    loadingDetail.value = false
  }
}

async function restoreBaseline() {
  if (!activeGroupID.value) return
  resetScenario()
  await loadDetail(activeGroupID.value, true)
}

async function refresh() {
  const selected = activeGroupID.value
  resetScenario()
  await loadOverview(selected)
}

function handleClose() {
  emit('close')
}

function copyFormula(formula: string) {
  void copyToClipboard(formula, t('admin.accounts.advancedSchedulerScore.formulaCopied'))
}

function formatNumber(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return value.toFixed(4).replace(/\.?0+$/, '')
}

function formatPercent(value: number): string {
  return `${(value * 100).toFixed(2).replace(/\.?0+$/, '')}%`
}

function formatTimestamp(value: string | undefined): string {
  if (!value) return '—'
  return formatDateTime(value)
}

function metricLabel(key: string): string {
  return t(`admin.accounts.advancedSchedulerScore.metricNames.${key}`)
}

function settingLabel(key: string): string {
  return t(`admin.accounts.advancedSchedulerScore.settingNames.${key}`)
}

function policyLabel(key: string): string {
  return t(`admin.accounts.advancedSchedulerScore.policyNames.${key}`)
}

function policyStateLabel(state: string): string {
  return t(`admin.accounts.advancedSchedulerScore.policyStates.${state}`)
}

function settingSourceLabel(source: string): string {
  return t(`admin.accounts.advancedSchedulerScore.settingSources.${source}`)
}

function hardFilterLabel(reason: string): string {
  return t(`admin.accounts.advancedSchedulerScore.filterReasons.${reason}`)
}

function hardFilterText(reasons: string[] | undefined): string {
  if (!reasons?.length) return t('admin.accounts.advancedSchedulerScore.noCandidate')
  return reasons.map(hardFilterLabel).join(' · ')
}

function metricStatus(metric: AdvancedSchedulerScoreDiagnosticMetric): string {
  if (metric.neutral) return t('admin.accounts.advancedSchedulerScore.neutral')
  if (!metric.available) return t('admin.accounts.advancedSchedulerScore.unavailable')
  return metric.source
}

function normalizationRangeText(ranges: AdvancedSchedulerScoreDiagnosticDetail['candidate_pool']['normalization_ranges']): string {
  const parts = [
    `${t('admin.accounts.advancedSchedulerScore.rangePriority')}: ${ranges.priority_min}–${ranges.priority_max}`,
    `${t('admin.accounts.advancedSchedulerScore.rangeQueue')}: ${ranges.max_waiting_count}`
  ]
  if (ranges.ttft_min_ms !== undefined && ranges.ttft_max_ms !== undefined) {
    parts.push(`${t('admin.accounts.advancedSchedulerScore.rangeTTFT')}: ${formatNumber(ranges.ttft_min_ms)}–${formatNumber(ranges.ttft_max_ms)} ms`)
  }
  if (ranges.reset_min_seconds !== undefined && ranges.reset_max_seconds !== undefined) {
    parts.push(`${t('admin.accounts.advancedSchedulerScore.rangeReset')}: ${formatNumber(ranges.reset_min_seconds)}–${formatNumber(ranges.reset_max_seconds)} s`)
  }
  return parts.join(' · ')
}
</script>
