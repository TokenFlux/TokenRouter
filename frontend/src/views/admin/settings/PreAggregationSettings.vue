<template>
  <section class="card">
    <div class="flex items-start justify-between gap-4 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex min-w-0 items-start gap-3">
        <span class="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-control bg-emerald-50 text-emerald-600 dark:bg-emerald-900/20 dark:text-emerald-400">
          <Icon name="database" size="md" />
        </span>
        <div class="min-w-0">
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
            {{ t("admin.settings.preAggregation.title") }}
          </h2>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.preAggregation.description") }}
          </p>
        </div>
      </div>
      <button
        type="button"
        class="btn btn-secondary h-9 w-9 shrink-0 p-0"
        :disabled="loading"
        :title="t('common.refresh')"
        @click="loadSettings"
      >
        <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
      </button>
    </div>

    <div v-if="loading && !state" class="flex min-h-40 items-center justify-center text-gray-400">
      <Icon name="refresh" size="lg" class="animate-spin" />
    </div>

    <div v-else-if="state" class="divide-y divide-gray-100 dark:divide-dark-700">
      <div class="grid gap-6 p-6 lg:grid-cols-[minmax(0,240px)_minmax(0,1fr)]">
        <div class="space-y-5">
          <div class="flex items-center justify-between gap-4">
            <div>
              <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
                {{ t("admin.settings.preAggregation.usage") }}
              </h3>
              <p v-if="!state.availability.usage_available" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
                {{ t("admin.settings.preAggregation.unavailable") }}
              </p>
            </div>
            <Toggle
              v-model="form.usage.enabled"
              :disabled="!state.availability.usage_available || saving"
            />
          </div>
          <div>
            <label class="input-label">{{ t("admin.settings.preAggregation.interval") }}</label>
            <input
              v-model.number="form.usage.interval_seconds"
              type="number"
              min="30"
              max="3600"
              step="30"
              class="input"
              :disabled="saving"
            />
          </div>
        </div>

        <dl class="grid grid-cols-2 gap-x-6 gap-y-4 text-sm xl:grid-cols-4">
          <StatusItem :label="t('admin.settings.preAggregation.phase')">
            <span :class="phaseClass(state.usage_status.phase)">{{ phaseLabel(state.usage_status.phase) }}</span>
          </StatusItem>
          <StatusItem :label="t('admin.settings.preAggregation.coverage')" class="col-span-2 xl:col-span-1">
            {{ usageCoverage }}
          </StatusItem>
          <StatusItem :label="t('admin.settings.preAggregation.lastSuccess')">
            {{ formatDate(state.usage_status.last_success_at) }}
          </StatusItem>
          <StatusItem :label="t('admin.settings.preAggregation.lag')">
            {{ formatDuration(state.usage_status.lag_seconds * 1000) }}
          </StatusItem>
          <StatusItem :label="t('admin.settings.preAggregation.lastDuration')">
            {{ formatDuration(state.usage_status.last_duration_ms) }}
          </StatusItem>
          <StatusItem
            v-if="state.usage_status.last_error"
            :label="t('admin.settings.preAggregation.lastError')"
            class="col-span-2 text-red-600 dark:text-red-400 xl:col-span-3"
          >
            {{ state.usage_status.last_error }}
          </StatusItem>
        </dl>
      </div>

      <div class="grid gap-6 p-6 lg:grid-cols-[minmax(0,240px)_minmax(0,1fr)]">
        <div class="flex items-start justify-between gap-4">
          <div>
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">
              {{ t("admin.settings.preAggregation.ops") }}
            </h3>
            <p v-if="!state.availability.ops_available" class="mt-1 text-xs text-amber-600 dark:text-amber-400">
              {{ t("admin.settings.preAggregation.unavailable") }}
            </p>
          </div>
          <Toggle
            v-model="form.ops.enabled"
            :disabled="!state.availability.ops_available || saving"
          />
        </div>

        <dl class="grid grid-cols-2 gap-x-6 gap-y-4 text-sm xl:grid-cols-4">
          <StatusItem :label="t('admin.settings.preAggregation.phase')">
            <span :class="phaseClass(state.ops_status.phase)">{{ phaseLabel(state.ops_status.phase) }}</span>
          </StatusItem>
          <StatusItem :label="t('admin.settings.preAggregation.lastSuccess')">
            {{ formatDate(state.ops_status.last_success_at) }}
          </StatusItem>
          <StatusItem :label="t('admin.settings.preAggregation.lastDuration')">
            {{ formatDuration(state.ops_status.last_duration_ms) }}
          </StatusItem>
          <StatusItem
            v-if="state.ops_status.last_error"
            :label="t('admin.settings.preAggregation.lastError')"
            class="col-span-2 text-red-600 dark:text-red-400 xl:col-span-1"
          >
            {{ state.ops_status.last_error }}
          </StatusItem>
        </dl>
      </div>

      <div class="flex flex-col gap-4 p-6 sm:flex-row sm:items-end sm:justify-between">
        <div class="flex flex-wrap items-end gap-3">
          <div>
            <label class="input-label">{{ t("admin.settings.preAggregation.backfillDays") }}</label>
            <input
              v-model.number="backfillDays"
              type="number"
              min="1"
              :max="state.availability.manual_backfill_max_days"
              class="input w-28"
              :disabled="backfilling || !canBackfill"
            />
          </div>
          <button
            type="button"
            class="btn btn-secondary"
            :disabled="backfilling || !canBackfill"
            @click="startBackfill"
          >
            <Icon :name="backfilling ? 'refresh' : 'play'" size="sm" :class="backfilling ? 'animate-spin' : ''" />
            {{ t("admin.settings.preAggregation.startBackfill") }}
          </button>
        </div>

        <button type="button" class="btn btn-primary" :disabled="saving" @click="saveSettings">
          <Icon :name="saving ? 'refresh' : 'check'" size="sm" :class="saving ? 'animate-spin' : ''" />
          {{ t("admin.settings.preAggregation.save") }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onMounted, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import { adminAPI } from "@/api";
import type { PreAggregationSettingsResponse } from "@/api/admin/settings";
import Toggle from "@/components/common/Toggle.vue";
import Icon from "@/components/icons/Icon.vue";
import { useAppStore } from "@/stores";
import { extractApiErrorMessage } from "@/utils/apiError";

// 状态项使用无边框网格，避免在设置卡片内再次嵌套卡片。
const StatusItem = defineComponent({
  props: { label: { type: String, required: true } },
  setup(props, { slots, attrs }) {
    return () => h("div", attrs, [
      h("dt", { class: "text-xs text-gray-500 dark:text-gray-400" }, props.label),
      h("dd", { class: "mt-1 break-words font-medium text-gray-800 dark:text-gray-200" }, slots.default?.()),
    ]);
  },
});

const { t, locale } = useI18n();
const appStore = useAppStore();
const loading = ref(false);
const saving = ref(false);
const backfilling = ref(false);
const state = ref<PreAggregationSettingsResponse | null>(null);
const backfillDays = ref(7);
const form = reactive({
  usage: { enabled: false, interval_seconds: 60 },
  ops: { enabled: false },
});

const canBackfill = computed(() => Boolean(
  state.value?.availability.manual_backfill_available && form.usage.enabled,
));

const usageCoverage = computed(() => {
  const status = state.value?.usage_status;
  if (!status?.coverage_start || !status.live_watermark) {
    return t("admin.settings.preAggregation.noData");
  }
  return `${formatDate(status.coverage_start)} - ${formatDate(status.live_watermark)}`;
});

function applyResponse(response: PreAggregationSettingsResponse) {
  state.value = response;
  form.usage.enabled = response.settings.usage.enabled;
  form.usage.interval_seconds = response.settings.usage.interval_seconds;
  form.ops.enabled = response.settings.ops.enabled;
  backfillDays.value = Math.min(Math.max(backfillDays.value, 1), response.availability.manual_backfill_max_days);
}

async function loadSettings() {
  loading.value = true;
  try {
    applyResponse(await adminAPI.settings.getPreAggregationSettings());
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.preAggregation.loadFailed")));
  } finally {
    loading.value = false;
  }
}

async function saveSettings() {
  saving.value = true;
  try {
    const response = await adminAPI.settings.updatePreAggregationSettings({
      usage: {
        enabled: form.usage.enabled,
        interval_seconds: Number(form.usage.interval_seconds),
      },
      ops: { enabled: form.ops.enabled },
    });
    applyResponse(response);
    appStore.showSuccess(t("admin.settings.preAggregation.saved"));
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.preAggregation.saveFailed")));
  } finally {
    saving.value = false;
  }
}

async function startBackfill() {
  if (!state.value) return;
  backfilling.value = true;
  try {
    await adminAPI.settings.backfillPreAggregation(Number(backfillDays.value));
    appStore.showSuccess(t("admin.settings.preAggregation.backfillAccepted"));
    await loadSettings();
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.preAggregation.backfillFailed")));
  } finally {
    backfilling.value = false;
  }
}

function phaseLabel(phase: string): string {
  const key = `admin.settings.preAggregation.phases.${phase || "unavailable"}`;
  const translated = t(key);
  return translated === key ? phase : translated;
}

function phaseClass(phase: string): string {
  if (phase === "error") return "text-red-600 dark:text-red-400";
  if (phase === "live" || phase === "backfill") return "text-emerald-600 dark:text-emerald-400";
  if (phase === "disabled" || phase === "unavailable") return "text-gray-500 dark:text-gray-400";
  return "text-gray-800 dark:text-gray-200";
}

function formatDate(value?: string): string {
  if (!value) return t("admin.settings.preAggregation.noData");
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(locale.value, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(date);
}

function formatDuration(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds <= 0) return "0s";
  if (milliseconds < 1000) return `${Math.round(milliseconds)}ms`;
  if (milliseconds < 60_000) return `${(milliseconds / 1000).toFixed(1)}s`;
  const minutes = Math.floor(milliseconds / 60_000);
  const seconds = Math.floor((milliseconds % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

onMounted(loadSettings);
</script>
