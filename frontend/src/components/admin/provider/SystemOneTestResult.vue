<template>
  <div
    class="flex min-h-0 flex-1 flex-col gap-4"
    data-testid="systemone-test-result"
  >
    <div
      v-if="$slots.leading"
      class="flex items-center gap-2"
    >
      <slot name="leading" />
    </div>
    <div class="grid shrink-0 grid-cols-2 gap-4 rounded-surface border border-gray-200 p-4 dark:border-dark-600">
      <div
        v-for="metric in metrics"
        :key="metric.label"
        class="min-w-0"
      >
        <div class="text-xs text-gray-500 dark:text-dark-400">{{ metric.label }}</div>
        <div class="mt-1 break-words font-mono text-sm text-gray-900 dark:text-dark-50">{{ metric.value }}</div>
      </div>
    </div>
    <div
      class="min-h-64 space-y-4 md:min-h-0 md:flex-1 md:overflow-auto"
      aria-live="polite"
    >
      <SettingsNotice
        v-if="run.status === 'error'"
        tone="error"
      >
        {{ run.errorMessage }}
      </SettingsNotice>
      <SettingsNotice
        v-if="run.status === 'success' && !run.decisionUsageValid"
        tone="warning"
      >
        {{ t('admin.providers.decisionTest.usageUnknown') }}
      </SettingsNotice>
      <div
        v-if="!run.decisionResult && run.status !== 'error'"
        class="flex min-h-40 flex-col items-center justify-center gap-2 text-center"
      >
        <Icon
          :name="run.status === 'connecting' ? 'loader' : 'beaker'"
          size="xl"
          :class="run.status === 'connecting' ? 'animate-spin text-primary-500' : 'text-gray-400'"
        />
        <p class="text-sm text-gray-600 dark:text-dark-300">{{ t(run.status === 'connecting' ? 'admin.providers.decisionTest.running' : 'admin.providers.decisionTest.empty') }}</p>
      </div>
      <article
        v-for="(answer, id) in run.decisionResult?.answers"
        :key="id"
        class="space-y-3 rounded-surface border border-gray-200 p-4 dark:border-dark-600"
        :data-testid="`decision-answer-${id}`"
      >
        <div class="flex items-center justify-between gap-2">
          <h4 class="break-all text-sm font-semibold text-gray-900 dark:text-dark-50">{{ id }}</h4>
          <span class="text-xs text-gray-500 dark:text-dark-400">{{ t(`admin.providers.decisionTest.${answer.type}`) }}</span>
        </div>
        <div class="text-lg font-semibold tabular-nums text-primary-600 dark:text-primary-400">
          {{ answer.type === 'noul' ? percent(answer.noul) : answer.type === 'choice' ? answer.choice : answer.score }}
        </div>
        <p
          v-if="answer.type === 'noul'"
          class="input-hint"
        >
          {{ t('admin.providers.decisionTest.probabilityTrue') }}
        </p>
        <p
          v-if="answer.confidence != null"
          class="input-hint"
        >
          {{ t('admin.providers.decisionTest.confidence') }} {{ percent(answer.confidence) }}
        </p>
        <div
          v-if="answer.probabilities"
          class="space-y-2"
        >
          <div
            v-for="(probability, option) in answer.probabilities"
            :key="option"
          >
            <div class="mb-1 flex justify-between gap-2 text-xs text-gray-600 dark:text-dark-300"><span class="break-all">{{ option }}</span><span>{{ percent(probability) }}</span></div>
            <div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
              <div
                class="h-full rounded-full bg-primary-500"
                :style="{ width: `${Math.max(0, Math.min(1, probability)) * 100}%` }"
              />
            </div>
          </div>
        </div>
        <p
          v-if="answer.legend != null"
          class="whitespace-pre-wrap break-words text-xs text-gray-500 dark:text-dark-400"
        >
          {{ typeof answer.legend === 'string' ? answer.legend : JSON.stringify(answer.legend, null, 2) }}
        </p>
      </article>
      <details
        v-if="run.decisionResult"
        class="text-xs text-gray-500 dark:text-dark-400"
      >
        <summary class="cursor-pointer">{{ t('admin.providers.decisionTest.rawResponse') }}</summary>
        <pre class="mt-2 whitespace-pre-wrap break-all">{{ JSON.stringify(run.decisionResult, null, 2) }}</pre>
      </details>
      <details
        v-if="run.logLines.length"
        class="text-xs text-gray-500 dark:text-dark-400"
      >
        <summary class="cursor-pointer">{{ t('admin.providers.testDialog.viewLog') }}</summary>
        <p
          v-for="(line, index) in run.logLines"
          :key="index"
          class="mt-2 break-words"
        >
          {{ line.text }}
        </p>
      </details>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import { Icon } from '@/components/icons'
import type { ProviderTestRun } from './providerTestRun'

const props = defineProps<{ run: ProviderTestRun }>()
const { t } = useI18n()
const percent = (value: number | undefined) => value == null ? '-' : `${(value * 100).toFixed(2)}%`
// 有效零用量显示 0；缺失或非法用量显示未知。
const metrics = computed(() => [
  { label: t('admin.providers.testDialog.model'), value: props.run.decisionResult?.model || props.run.resolvedModel || '-' },
  { label: t('admin.providers.testDialog.metricTotal'), value: props.run.totalMs == null ? '-' : `${(props.run.totalMs / 1000).toFixed(2)} s` },
  { label: t('admin.providers.decisionTest.inputTokens'), value: props.run.decisionUsageValid ? props.run.decisionResult?.usage?.input_tokens : '-' },
  { label: t('admin.providers.decisionTest.outputTokens'), value: props.run.decisionUsageValid ? props.run.decisionResult?.usage?.output_tokens : '-' }
])
</script>
