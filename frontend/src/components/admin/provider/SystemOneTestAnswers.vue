<template>
  <div class="space-y-4">
    <SettingsNotice v-if="!usageValid" tone="warning">
      {{ t('admin.providers.decisionTest.usageUnknown') }}
    </SettingsNotice>
    <article
      v-for="(answer, id) in result.answers"
      :key="id"
      class="space-y-3 border-b border-gray-100 pb-4 last:border-b-0 last:pb-0 dark:border-dark-700"
      :data-testid="`decision-answer-${id}`"
    >
      <div class="flex items-center justify-between gap-2">
        <h4 class="break-all text-sm font-semibold text-gray-900 dark:text-dark-50">{{ id }}</h4>
        <span class="text-xs text-gray-500 dark:text-dark-400">{{ t(`admin.providers.decisionTest.${answer.type}`) }}</span>
      </div>
      <div class="text-base font-semibold tabular-nums text-primary-600 dark:text-primary-400">
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
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import type { SystemOneTestResult } from './systemOneTest'

// 决策答案放在共用结果面板内，按问题类型展示概率、选项或评分。
defineProps<{ result: SystemOneTestResult; usageValid?: boolean }>()
const { t } = useI18n()
const percent = (value: number | undefined) => value == null ? '-' : `${(value * 100).toFixed(2)}%`
</script>
