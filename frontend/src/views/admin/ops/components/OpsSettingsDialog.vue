<script setup lang="ts">
import Skeleton from '@/components/common/Skeleton.vue'

import Disclosure from '@/components/common/Disclosure.vue'

import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { opsAPI } from '@/api/admin/ops'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import type {
  OpsAlertRuntimeSettings,
  EmailNotificationConfig,
  AlertSeverity,
  OpsAdvancedSettings,
  OpsMetricThresholds,
} from '../types'

// OpsSettingsDialog.vue edits alert evaluation, email delivery, retention,
// dashboard display, and metric threshold settings. Each settings family has its
// own API response and update endpoint, while the dialog presents one validation
// result and one save action. Missing status-code configuration receives the
// standard ignored-code defaults when loaded.
//
// Save normalizes ignored codes and disables enabled email sections with no
// recipients before issuing independent updates in parallel. A failed update
// does not roll back successful sibling writes, and the dialog stays open with
// an error message. Saved and close events are emitted after every requested
// update succeeds so the dashboard can reload its presentation settings.

// Translation and application notifications supply dialog labels, validation
// feedback, and settings persistence results for this editor.
const { t } = useI18n()
const appStore = useAppStore()

// props controls dialog visibility, while close and saved events tell the parent
// when to hide or refresh it.
const props = defineProps<{
  show: boolean // owner-controlled dialog visibility
}>()

// emit publishes successful persistence and dismissal events after the
// corresponding dialog action completes for the parent.
const emit = defineEmits<{
  close: [] // dialog dismissal event payload
  saved: [] // successful settings persistence event
}>()

// Loading and saving flags track request phases separately, while the
// ignored-code input retains editable text until validation.
const loading = ref(false)
const saving = ref(false)
const ignoredStatusCodesInput = ref('401, 403')

// runtimeSettings holds the loaded alert evaluator settings; null means that
// settings family has not been populated.
const runtimeSettings = ref<OpsAlertRuntimeSettings | null>(null)
// emailConfig owns editable alert and report recipient lists and enablement
// flags before the combined save action.
const emailConfig = ref<EmailNotificationConfig | null>(null)
// advancedSettings holds retention and dashboard options, including the
// normalized ignored status codes sent on save.
const advancedSettings = ref<OpsAdvancedSettings | null>(null)
// metricThresholds begins with dashboard defaults and accepts individually
// supplied server values when threshold settings load.
const metricThresholds = ref<OpsMetricThresholds>({
  sla_percent_min: 99.5,
  ttft_p99_ms_max: 500,
  request_error_rate_percent_max: 5,
  upstream_error_rate_percent_max: 5,
})

// parseIgnoredStatusCodes validates comma- or whitespace-separated decimal HTTP
// statuses. Empty text returns an empty accepted list; invalid tokens reject the
// whole input, while valid codes are deduplicated and sorted numerically.
function parseIgnoredStatusCodes(raw: string): {
  // valid distinguishes an accepted empty list from rejected input; callers consult this flag
  // before using codes.
  valid: boolean // input acceptance flag
  // codes contains sorted unique statuses when valid is true; rejected input returns
  // an empty list alongside a validation error.
  codes: number[] // normalized HTTP status list
  error?: string // optional translated validation explanation
} {
  const text = raw.trim()
  if (!text) return { valid: true, codes: [] }
  const parts = text
    .split(/[,\s]+/)
    .map((s) => s.trim())
    .filter(Boolean)
  const seen = new Set<number>()
  const codes: number[] = []
  for (const part of parts) {
    if (!/^\d+$/.test(part)) {
      return {
        valid: false,
        codes: [],
        error: t('admin.ops.settings.validation.ignoredStatusCodesRange'),
      }
    }
    const code = Number.parseInt(part, 10)
    // Reject statuses outside the HTTP response range before saving, so one
    // malformed entry cannot enter the ignored-status configuration.
    if (!Number.isFinite(code) || code < 100 || code > 599) {
      return {
        valid: false,
        codes: [],
        error: t('admin.ops.settings.validation.ignoredStatusCodesRange'),
      }
    }
    if (!seen.has(code)) {
      seen.add(code)
      codes.push(code)
    }
  }
  codes.sort((a, b) => a - b)
  return { valid: true, codes }
}

// formatIgnoredStatusCodes formats validated status codes as comma-separated
// text for editing. Missing arrays use 401 and 403, and an invalid supplied list
// also falls back to that displayed default.
function formatIgnoredStatusCodes(codes: number[] | null | undefined): string {
  const fallback = Array.isArray(codes) ? codes : [401, 403]
  const parsed = parseIgnoredStatusCodes(fallback.join(', '))
  return parsed.valid ? parsed.codes.join(', ') : '401, 403'
}

// loadAllSettings retrieves four settings families together before publishing
// their editable values. Missing ignored-code arrays and omitted threshold
// fields receive defaults, while a failed request reports an error and leaves
// previous drafts available.
async function loadAllSettings() {
  loading.value = true
  try {
    const [runtime, email, advanced, thresholds] = await Promise.all([
      opsAPI.getAlertRuntimeSettings(),
      opsAPI.getEmailNotificationConfig(),
      opsAPI.getAdvancedSettings(),
      opsAPI.getMetricThresholds(),
    ])
    runtimeSettings.value = runtime
    emailConfig.value = email
    advancedSettings.value = advanced
    // Older or incomplete settings responses can omit the ignored-code array;
    // seed the editable list so saving has a concrete status selection.
    if (!Array.isArray(advancedSettings.value.ignored_status_codes)) {
      advancedSettings.value.ignored_status_codes = [401, 403]
    }
    ignoredStatusCodesInput.value = formatIgnoredStatusCodes(
      advancedSettings.value.ignored_status_codes,
    )
    // Apply supplied threshold fields individually so partially populated
    // settings retain usable defaults for the remaining metric badges.
    if (thresholds && Object.keys(thresholds).length > 0) {
      metricThresholds.value = {
        sla_percent_min: thresholds.sla_percent_min ?? 99.5,
        ttft_p99_ms_max: thresholds.ttft_p99_ms_max ?? 500,
        request_error_rate_percent_max: thresholds.request_error_rate_percent_max ?? 5,
        upstream_error_rate_percent_max: thresholds.upstream_error_rate_percent_max ?? 5,
      }
    }
  } catch (err: any) {
    console.error('[OpsSettingsDialog] Failed to load settings', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.settings.loadFailed'))
  } finally {
    loading.value = false
  }
}

// Opening the dialog reloads persisted settings into its drafts before the next
// edit, while hidden state remains mounted.
watch(
  () => props.show,
  (show) => {
    if (show) {
      loadAllSettings()
    }
  },
)

// Recipient inputs remain separate from persisted lists until an add action
// validates and appends a normalized email address.
const alertRecipientInput = ref('')
const reportRecipientInput = ref('')

// severityOptions maps notification severity names to translated labels; its
// empty value leaves email severity unrestricted.
const severityOptions: Array<{
  value: AlertSeverity | '' // notification severity selector value
  label: string // translated display label text
}> = [
  { value: '', label: t('admin.ops.email.minSeverityAll') },
  { value: 'critical', label: t('common.critical') },
  { value: 'warning', label: t('common.warning') },
  { value: 'info', label: t('common.info') },
]

// isValidEmailAddress checks the editor email shape for whitespace and domain
// separators.
function isValidEmailAddress(email: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)
}

// addRecipient validates the selected email input and appends a lowercase
// address to its recipient list. Existing exact normalized addresses are not
// duplicated, and accepted input is cleared after processing.
function addRecipient(target: 'alert' | 'report') {
  if (!emailConfig.value) return
  const raw = (target === 'alert' ? alertRecipientInput.value : reportRecipientInput.value).trim()
  if (!raw) return

  if (!isValidEmailAddress(raw)) {
    appStore.showError(t('common.invalidEmail'))
    return
  }

  // Normalize newly entered addresses before comparing against the recipient list so
  // repeated lowercase entries do not create duplicate destinations.
  const normalized = raw.toLowerCase()
  const list =
    target === 'alert' ? emailConfig.value.alert.recipients : emailConfig.value.report.recipients
  if (!list.includes(normalized)) {
    list.push(normalized)
  }
  if (target === 'alert') alertRecipientInput.value = ''
  else reportRecipientInput.value = ''
}

// removeRecipient removes the first exact address match from the chosen
// recipient list. An unloaded email configuration or an absent address leaves
// the current lists unchanged for the caller.
function removeRecipient(target: 'alert' | 'report', email: string) {
  if (!emailConfig.value) return
  const list =
    target === 'alert' ? emailConfig.value.alert.recipients : emailConfig.value.report.recipients
  const idx = list.indexOf(email)
  if (idx >= 0) list.splice(idx, 1)
}

// validation collects evaluator, retention, ignored-code, and metric errors
// before saving; enabled email lists with no recipients are handled during
// normalization.
const validation = computed(() => {
  const errors: string[] = []

  // Evaluation intervals range from one second to one day.
  if (runtimeSettings.value) {
    const evalSeconds = runtimeSettings.value.evaluation_interval_seconds
    if (!Number.isFinite(evalSeconds) || evalSeconds < 1 || evalSeconds > 86400) {
      errors.push(t('admin.ops.runtime.validation.evalIntervalRange'))
    }
  }

  // Recipient-free email sections pass validation because saveAllSettings
  // disables their delivery flags before sending the corresponding configuration
  // updates.

  // Retention and cleanup inputs are checked against the supported day,
  // batch-count, and pause-duration limits before requests are sent.
  if (advancedSettings.value) {
    const {
      error_log_retention_days,
      minute_metrics_retention_days,
      hourly_metrics_retention_days,
    } = advancedSettings.value.data_retention
    if (error_log_retention_days < 0 || error_log_retention_days > 365) {
      errors.push(t('admin.ops.settings.validation.retentionDaysRange'))
    }
    if (minute_metrics_retention_days < 0 || minute_metrics_retention_days > 365) {
      errors.push(t('admin.ops.settings.validation.retentionDaysRange'))
    }
    if (hourly_metrics_retention_days < 0 || hourly_metrics_retention_days > 365) {
      errors.push(t('admin.ops.settings.validation.retentionDaysRange'))
    }
    const { cleanup_batch_size, cleanup_pause_ms } = advancedSettings.value.data_retention
    if (
      !Number.isInteger(cleanup_batch_size) ||
      cleanup_batch_size < 100 ||
      cleanup_batch_size > 5000
    ) {
      errors.push(t('admin.ops.settings.validation.cleanupBatchSizeRange'))
    }
    if (!Number.isInteger(cleanup_pause_ms) || cleanup_pause_ms < 1 || cleanup_pause_ms > 2000) {
      errors.push(t('admin.ops.settings.validation.cleanupPauseRange'))
    }
  }
  const ignoredStatusCodes = parseIgnoredStatusCodes(ignoredStatusCodesInput.value)
  if (!ignoredStatusCodes.valid) {
    errors.push(
      ignoredStatusCodes.error || t('admin.ops.settings.validation.ignoredStatusCodesRange'),
    )
  }

  // Percentage thresholds use zero to one hundred, while first-token duration
  // accepts nonnegative milliseconds when supplied by the editor.
  if (
    metricThresholds.value.sla_percent_min != null &&
    (metricThresholds.value.sla_percent_min < 0 || metricThresholds.value.sla_percent_min > 100)
  ) {
    errors.push(t('admin.ops.settings.validation.slaMinPercentRange'))
  }
  if (
    metricThresholds.value.ttft_p99_ms_max != null &&
    metricThresholds.value.ttft_p99_ms_max < 0
  ) {
    errors.push(t('admin.ops.settings.validation.ttftP99MaxRange'))
  }
  if (
    metricThresholds.value.request_error_rate_percent_max != null &&
    (metricThresholds.value.request_error_rate_percent_max < 0 ||
      metricThresholds.value.request_error_rate_percent_max > 100)
  ) {
    errors.push(t('admin.ops.settings.validation.requestErrorRateMaxRange'))
  }
  if (
    metricThresholds.value.upstream_error_rate_percent_max != null &&
    (metricThresholds.value.upstream_error_rate_percent_max < 0 ||
      metricThresholds.value.upstream_error_rate_percent_max > 100)
  ) {
    errors.push(t('admin.ops.settings.validation.upstreamErrorRateMaxRange'))
  }

  return { valid: errors.length === 0, errors }
})

// saveAllSettings validates the dialog and persists each loaded settings family.
// Email sections with no recipients are disabled, and ignored HTTP codes are
// normalized before requests begin.
//
// The four update operations run independently in Promise.all, so one rejection
// can leave other server updates completed and the local drafts already
// normalized. Failure keeps the dialog open and reports available API detail.
// Saved and close events follow complete success, and the saving flag resets on
// either path.
async function saveAllSettings() {
  if (!validation.value.valid) {
    appStore.showError(validation.value.errors[0])
    return
  }

  saving.value = true
  try {
    // Disable delivery for empty recipient lists before persistence so enabled
    // email sections do not request notifications with no destination.
    if (emailConfig.value) {
      if (emailConfig.value.alert.enabled && emailConfig.value.alert.recipients.length === 0) {
        emailConfig.value.alert.enabled = false
      }
      if (emailConfig.value.report.enabled && emailConfig.value.report.recipients.length === 0) {
        emailConfig.value.report.enabled = false
      }
    }
    if (advancedSettings.value) {
      const ignoredStatusCodes = parseIgnoredStatusCodes(ignoredStatusCodesInput.value)
      advancedSettings.value.ignored_status_codes = ignoredStatusCodes.codes
      ignoredStatusCodesInput.value = ignoredStatusCodes.codes.join(', ')
    }
    // These independent updates can partly succeed before another rejects; success
    // events wait for all results, while failures leave drafts available for
    // correction.
    await Promise.all([
      runtimeSettings.value
        ? opsAPI.updateAlertRuntimeSettings(runtimeSettings.value)
        : Promise.resolve(),
      emailConfig.value
        ? opsAPI.updateEmailNotificationConfig(emailConfig.value)
        : Promise.resolve(),
      advancedSettings.value
        ? opsAPI.updateAdvancedSettings(advancedSettings.value)
        : Promise.resolve(),
      opsAPI.updateMetricThresholds(metricThresholds.value),
    ])
    appStore.showSuccess(t('admin.ops.settings.saveSuccess'))
    emit('saved')
    emit('close')
  } catch (err: any) {
    console.error('[OpsSettingsDialog] Failed to save settings', err)
    appStore.showError(
      err?.response?.data?.message ||
        err?.response?.data?.detail ||
        t('admin.ops.settings.saveFailed'),
    )
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <BaseDialog
    :show="show"
    :title="t('admin.ops.settings.title')"
    width="extra-wide"
    @close="emit('close')"
  >
    <div
      v-if="loading"
      class="space-y-6"
      role="status"
      :aria-label="t('common.loading')"
      aria-busy="true"
      data-loading-skeleton
    >
      <div class="space-y-3 rounded-surface bg-gray-50 p-4 dark:bg-dark-950" aria-hidden="true">
        <Skeleton :width="142" :height="18" />
        <div class="space-y-2">
          <Skeleton :width="124" :height="14" />
          <Skeleton :height="36" />
          <Skeleton :width="260" :height="12" class="max-w-full" />
        </div>
      </div>
      <div
        v-for="section in 2"
        :key="section"
        class="space-y-3 rounded-surface bg-gray-50 p-4 dark:bg-dark-950"
        aria-hidden="true"
      >
        <Skeleton :width="176" :height="18" />
        <div class="flex items-center justify-between gap-4">
          <Skeleton :width="124" :height="16" />
          <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
        </div>
      </div>
    </div>

    <div v-else-if="runtimeSettings && emailConfig && advancedSettings" class="space-y-6">
      <!-- Configuration validation errors -->
      <div
        v-if="!validation.valid"
        class="rounded-control border border-amber-200 bg-amber-50 p-3 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-900/20 dark:text-amber-200"
      >
        <div class="font-bold">{{ t('admin.ops.settings.validation.title') }}</div>
        <ul class="mt-1 list-disc space-y-1 pl-4">
          <li v-for="msg in validation.errors" :key="msg">{{ msg }}</li>
        </ul>
      </div>

      <!-- Collection and evaluation frequency -->
      <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
        <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{
          t('admin.ops.settings.dataCollection')
        }}</h4>
        <div>
          <label class="input-label">{{ t('admin.ops.settings.evaluationInterval') }}</label>
          <input
            v-model.number="runtimeSettings.evaluation_interval_seconds"
            type="number"
            min="1"
            max="86400"
            class="input"
          />
          <p class="mt-1 text-xs text-gray-500">{{
            t('admin.ops.settings.evaluationIntervalHint')
          }}</p>
        </div>
      </div>

      <!-- Alert email configuration -->
      <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
        <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{
          t('admin.ops.settings.alertConfig')
        }}</h4>

        <div class="space-y-4">
          <div class="flex items-center justify-between">
            <div>
              <label class="font-medium text-gray-900 dark:text-white">{{
                t('admin.ops.settings.enableAlert')
              }}</label>
            </div>
            <Toggle v-model="emailConfig.alert.enabled" />
          </div>

          <div v-if="emailConfig.alert.enabled">
            <label class="input-label">{{ t('admin.ops.settings.alertRecipients') }}</label>
            <div class="flex gap-2">
              <input
                v-model="alertRecipientInput"
                type="email"
                class="input"
                :placeholder="t('admin.ops.settings.emailPlaceholder')"
                @keydown.enter.prevent="addRecipient('alert')"
              />
              <button
                class="btn btn-secondary whitespace-nowrap"
                type="button"
                @click="addRecipient('alert')"
              >
                {{ t('common.add') }}
              </button>
            </div>
            <div class="mt-2 flex flex-wrap gap-2">
              <span
                v-for="email in emailConfig.alert.recipients"
                :key="email"
                class="inline-flex items-center gap-2 rounded-full bg-primary-100 px-3 py-1 text-xs font-medium text-primary-700 dark:bg-primary-500/8 dark:text-primary-500"
              >
                {{ email }}
                <button
                  type="button"
                  class="text-primary-700/80 hover:text-primary-900"
                  @click="removeRecipient('alert', email)"
                  >×</button
                >
              </span>
            </div>
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.ops.settings.recipientsHint') }}
            </p>
          </div>

          <div v-if="emailConfig.alert.enabled">
            <label class="input-label">{{ t('admin.ops.settings.minSeverity') }}</label>
            <Select v-model="emailConfig.alert.min_severity" :options="severityOptions" />
          </div>
        </div>
      </div>

      <!-- Scheduled report configuration -->
      <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
        <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{
          t('admin.ops.settings.reportConfig')
        }}</h4>

        <div class="space-y-4">
          <div class="flex items-center justify-between">
            <div>
              <label class="font-medium text-gray-900 dark:text-white">{{
                t('admin.ops.settings.enableReport')
              }}</label>
            </div>
            <Toggle v-model="emailConfig.report.enabled" />
          </div>

          <div v-if="emailConfig.report.enabled">
            <label class="input-label">{{ t('admin.ops.settings.reportRecipients') }}</label>
            <div class="flex gap-2">
              <input
                v-model="reportRecipientInput"
                type="email"
                class="input"
                :placeholder="t('admin.ops.settings.emailPlaceholder')"
                @keydown.enter.prevent="addRecipient('report')"
              />
              <button
                class="btn btn-secondary whitespace-nowrap"
                type="button"
                @click="addRecipient('report')"
              >
                {{ t('common.add') }}
              </button>
            </div>
            <div class="mt-2 flex flex-wrap gap-2">
              <span
                v-for="email in emailConfig.report.recipients"
                :key="email"
                class="inline-flex items-center gap-2 rounded-full bg-primary-100 px-3 py-1 text-xs font-medium text-primary-700 dark:bg-primary-500/8 dark:text-primary-500"
              >
                {{ email }}
                <button
                  type="button"
                  class="text-primary-700/80 hover:text-primary-900"
                  @click="removeRecipient('report', email)"
                  >×</button
                >
              </span>
            </div>
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.ops.settings.recipientsHint') }}
            </p>
          </div>

          <div v-if="emailConfig.report.enabled" class="grid grid-cols-1 gap-4 md:grid-cols-2">
            <div class="flex items-center justify-between">
              <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                t('admin.ops.settings.dailySummary')
              }}</label>
              <Toggle v-model="emailConfig.report.daily_summary_enabled" />
            </div>
            <div v-if="emailConfig.report.daily_summary_enabled">
              <input
                v-model="emailConfig.report.daily_summary_schedule"
                type="text"
                class="input"
                placeholder="0 9 * * *"
              />
            </div>
            <div class="flex items-center justify-between">
              <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                t('admin.ops.settings.weeklySummary')
              }}</label>
              <Toggle v-model="emailConfig.report.weekly_summary_enabled" />
            </div>
            <div v-if="emailConfig.report.weekly_summary_enabled">
              <input
                v-model="emailConfig.report.weekly_summary_schedule"
                type="text"
                class="input"
                placeholder="0 9 * * 1"
              />
            </div>
          </div>
        </div>
      </div>

      <!-- Metric threshold settings -->
      <div class="rounded-surface bg-gray-50 p-4 dark:bg-dark-950">
        <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{
          t('admin.ops.settings.metricThresholds')
        }}</h4>
        <p class="mb-4 text-xs text-gray-500 dark:text-gray-400">{{
          t('admin.ops.settings.metricThresholdsHint')
        }}</p>

        <div class="space-y-4">
          <div>
            <label class="input-label">{{ t('admin.ops.settings.slaMinPercent') }}</label>
            <input
              v-model.number="metricThresholds.sla_percent_min"
              type="number"
              min="0"
              max="100"
              step="0.1"
              class="input"
            />
            <p class="mt-1 text-xs text-gray-500">{{
              t('admin.ops.settings.slaMinPercentHint')
            }}</p>
          </div>

          <div>
            <label class="input-label">{{ t('admin.ops.settings.ttftP99MaxMs') }}</label>
            <input
              v-model.number="metricThresholds.ttft_p99_ms_max"
              type="number"
              min="0"
              step="50"
              class="input"
            />
            <p class="mt-1 text-xs text-gray-500">{{ t('admin.ops.settings.ttftP99MaxMsHint') }}</p>
          </div>

          <div>
            <label class="input-label">{{
              t('admin.ops.settings.requestErrorRateMaxPercent')
            }}</label>
            <input
              v-model.number="metricThresholds.request_error_rate_percent_max"
              type="number"
              min="0"
              max="100"
              step="0.1"
              class="input"
            />
            <p class="mt-1 text-xs text-gray-500">{{
              t('admin.ops.settings.requestErrorRateMaxPercentHint')
            }}</p>
          </div>

          <div>
            <label class="input-label">{{
              t('admin.ops.settings.upstreamErrorRateMaxPercent')
            }}</label>
            <input
              v-model.number="metricThresholds.upstream_error_rate_percent_max"
              type="number"
              min="0"
              max="100"
              step="0.1"
              class="input"
            />
            <p class="mt-1 text-xs text-gray-500">{{
              t('admin.ops.settings.upstreamErrorRateMaxPercentHint')
            }}</p>
          </div>
        </div>
      </div>

      <!-- Advanced dashboard settings -->
      <Disclosure
        summary-class="cursor-pointer p-4 text-sm font-semibold text-gray-900 dark:text-white"
        class="rounded-surface bg-gray-50 dark:bg-dark-950"
      >
        <template #summary>
          {{ t('admin.ops.settings.advancedSettings') }}
        </template>
        <div class="space-y-4 px-4 pb-4">
          <!-- Observation retention settings -->
          <div class="space-y-3">
            <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{
              t('admin.ops.settings.dataRetention')
            }}</h5>

            <div class="flex items-center justify-between">
              <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                t('admin.ops.settings.enableCleanup')
              }}</label>
              <Toggle v-model="advancedSettings.data_retention.cleanup_enabled" />
            </div>

            <div v-if="advancedSettings.data_retention.cleanup_enabled">
              <label class="input-label">{{ t('admin.ops.settings.cleanupSchedule') }}</label>
              <input
                v-model="advancedSettings.data_retention.cleanup_schedule"
                type="text"
                class="input"
                placeholder="0 3 * * *"
              />
              <p class="mt-1 text-xs text-gray-500">{{
                t('admin.ops.settings.cleanupScheduleHint')
              }}</p>
            </div>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
              <div>
                <label class="input-label">{{ t('admin.ops.settings.cleanupBatchSize') }}</label>
                <input
                  v-model.number="advancedSettings.data_retention.cleanup_batch_size"
                  type="number"
                  min="100"
                  max="5000"
                  step="100"
                  class="input"
                />
              </div>
              <div>
                <label class="input-label">{{ t('admin.ops.settings.cleanupPauseMs') }}</label>
                <input
                  v-model.number="advancedSettings.data_retention.cleanup_pause_ms"
                  type="number"
                  min="1"
                  max="2000"
                  step="50"
                  class="input"
                />
              </div>
            </div>
            <p class="text-xs text-gray-500">{{ t('admin.ops.settings.cleanupThrottleHint') }}</p>

            <div class="grid grid-cols-1 gap-4 md:grid-cols-3">
              <div>
                <label class="input-label">{{
                  t('admin.ops.settings.errorLogRetentionDays')
                }}</label>
                <input
                  v-model.number="advancedSettings.data_retention.error_log_retention_days"
                  type="number"
                  min="0"
                  max="365"
                  class="input"
                />
              </div>
              <div>
                <label class="input-label">{{
                  t('admin.ops.settings.minuteMetricsRetentionDays')
                }}</label>
                <input
                  v-model.number="advancedSettings.data_retention.minute_metrics_retention_days"
                  type="number"
                  min="0"
                  max="365"
                  class="input"
                />
              </div>
              <div>
                <label class="input-label">{{
                  t('admin.ops.settings.hourlyMetricsRetentionDays')
                }}</label>
                <input
                  v-model.number="advancedSettings.data_retention.hourly_metrics_retention_days"
                  type="number"
                  min="0"
                  max="365"
                  class="input"
                />
              </div>
            </div>
            <p class="text-xs text-gray-500">{{ t('admin.ops.settings.retentionDaysHint') }}</p>
          </div>

          <!-- Error Filtering -->
          <div class="space-y-3">
            <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{
              t('admin.ops.settings.errorFiltering')
            }}</h5>

            <div>
              <label class="input-label">{{ t('admin.ops.settings.ignoredStatusCodes') }}</label>
              <input
                v-model="ignoredStatusCodesInput"
                type="text"
                class="input"
                :placeholder="t('admin.ops.settings.ignoredStatusCodesPlaceholder')"
              />
              <p class="mt-1 text-xs text-gray-500">
                {{ t('admin.ops.settings.ignoredStatusCodesHint') }}
              </p>
            </div>

            <div class="flex items-center justify-between">
              <div>
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                  t('admin.ops.settings.ignoreCountTokensErrors')
                }}</label>
                <p class="mt-1 text-xs text-gray-500">
                  {{ t('admin.ops.settings.ignoreCountTokensErrorsHint') }}
                </p>
              </div>
              <Toggle v-model="advancedSettings.ignore_count_tokens_errors" />
            </div>

            <div class="flex items-center justify-between">
              <div>
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                  t('admin.ops.settings.ignoreContextCanceled')
                }}</label>
                <p class="mt-1 text-xs text-gray-500">
                  {{ t('admin.ops.settings.ignoreContextCanceledHint') }}
                </p>
              </div>
              <Toggle v-model="advancedSettings.ignore_context_canceled" />
            </div>

            <div class="flex items-center justify-between">
              <div>
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                  t('admin.ops.settings.ignoreNoAvailableProviders')
                }}</label>
                <p class="mt-1 text-xs text-gray-500">
                  {{ t('admin.ops.settings.ignoreNoAvailableProvidersHint') }}
                </p>
              </div>
              <Toggle v-model="advancedSettings.ignore_no_available_providers" />
            </div>

            <div class="flex items-center justify-between">
              <div>
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                  t('admin.ops.settings.ignoreInsufficientBalanceErrors')
                }}</label>
                <p class="mt-1 text-xs text-gray-500">
                  {{ t('admin.ops.settings.ignoreInsufficientBalanceErrorsHint') }}
                </p>
              </div>
              <Toggle v-model="advancedSettings.ignore_insufficient_balance_errors" />
            </div>
          </div>

          <!-- Auto Refresh -->
          <div class="space-y-3">
            <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{
              t('admin.ops.settings.autoRefresh')
            }}</h5>

            <div class="flex items-center justify-between">
              <div>
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                  t('admin.ops.settings.enableAutoRefresh')
                }}</label>
                <p class="mt-1 text-xs text-gray-500">
                  {{ t('admin.ops.settings.enableAutoRefreshHint') }}
                </p>
              </div>
              <Toggle v-model="advancedSettings.auto_refresh_enabled" />
            </div>

            <div v-if="advancedSettings.auto_refresh_enabled">
              <label class="input-label">{{ t('admin.ops.settings.refreshInterval') }}</label>
              <Select
                v-model="advancedSettings.auto_refresh_interval_seconds"
                :options="[
                  { value: 15, label: t('admin.ops.settings.refreshInterval15s') },
                  { value: 30, label: t('admin.ops.settings.refreshInterval30s') },
                  { value: 60, label: t('admin.ops.settings.refreshInterval60s') },
                ]"
              />
            </div>
          </div>

          <!-- Dashboard Cards -->
          <div class="space-y-3">
            <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{
              t('admin.ops.settings.dashboardCards')
            }}</h5>

            <div class="flex items-center justify-between">
              <div>
                <label class="text-sm font-medium text-gray-700 dark:text-gray-300">{{
                  t('admin.ops.settings.displayAlertEvents')
                }}</label>
                <p class="mt-1 text-xs text-gray-500">
                  {{ t('admin.ops.settings.displayAlertEventsHint') }}
                </p>
              </div>
              <Toggle v-model="advancedSettings.display_alert_events" />
            </div>
          </div>
        </div>
      </Disclosure>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button
          class="btn btn-primary"
          :disabled="saving || !validation.valid"
          @click="saveAllSettings"
        >
          {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>
