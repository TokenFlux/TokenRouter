<template>
  <div class="space-y-4" data-testid="decision-results">
    <template v-if="!result">
      <div class="flex items-start gap-3" data-testid="decision-results-guide">
        <Icon v-if="running" name="loader" size="lg" class="shrink-0 animate-spin text-primary-500" :animate-on-hover="false" />
        <div class="space-y-1">
          <h4 class="text-sm font-semibold text-gray-900 dark:text-dark-50">{{ t(running ? 'admin.providers.decisionTest.waitingTitle' : 'admin.providers.decisionTest.guideTitle') }}</h4>
          <p class="text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t(running ? 'admin.providers.decisionTest.running' : 'admin.providers.decisionTest.guideHint') }}</p>
        </div>
      </div>
      <div class="grid gap-4 sm:grid-cols-3">
        <div v-for="type in questionTypes" :key="type" class="space-y-2 rounded-control border border-gray-200 bg-gray-50/70 p-3 dark:border-dark-600 dark:bg-dark-950">
          <div class="text-sm font-semibold text-gray-900 dark:text-dark-50">{{ typeNames[type] }}</div>
          <p class="text-xs leading-relaxed text-gray-500 dark:text-dark-400">{{ t(`admin.providers.decisionTest.guide.${type}`) }}</p>
        </div>
      </div>
    </template>
    <template v-else>
      <SettingsNotice v-if="!usageValid" tone="warning">
        {{ t('admin.providers.decisionTest.usageUnknown') }}
      </SettingsNotice>
      <div v-if="rows.length > 1" class="text-xs text-gray-500 dark:text-dark-400">{{ t('admin.providers.decisionTest.answerCount', { count: rows.length }) }}</div>
      <article
        v-for="(row, index) in rows"
        :key="row.id"
        class="space-y-3 rounded-surface border border-gray-200 p-3 dark:border-dark-600"
        :aria-labelledby="`${labelId}-${index}`"
        :data-testid="`decision-answer-${row.id}`"
      >
        <header class="space-y-2">
          <div class="flex items-center justify-between gap-2">
            <span class="break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{ row.id }}</span>
            <span class="shrink-0 rounded-compact bg-gray-100 px-2 py-0.5 text-xs font-medium text-gray-600 dark:bg-dark-800 dark:text-dark-300">{{ typeNames[row.answer.type] }}</span>
          </div>
          <h4 :id="`${labelId}-${index}`" class="whitespace-pre-wrap break-words text-sm font-semibold text-gray-900 dark:text-dark-50">{{ row.title }}</h4>
        </header>
        <dl class="grid grid-cols-2 gap-4">
          <div class="min-w-0">
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{ row.primaryLabel }}</dt>
            <dd class="mt-1">
              <div class="break-all font-mono text-2xl font-semibold tabular-nums text-primary-600 dark:text-primary-400">{{ row.primaryValue }}</div>
              <p v-if="row.primaryDescription" class="mt-1 break-words text-xs text-gray-500 dark:text-dark-400">{{ row.primaryDescription }}</p>
            </dd>
          </div>
          <div class="min-w-0">
            <dt class="text-xs text-gray-500 dark:text-dark-400">{{ row.secondaryLabel }}</dt>
            <dd class="mt-1">
              <div class="font-mono text-lg font-medium tabular-nums text-gray-700 dark:text-dark-200">{{ row.secondaryValue }}</div>
              <p v-if="row.secondaryDescription" class="mt-1 break-words text-xs text-gray-500 dark:text-dark-400">{{ row.secondaryDescription }}</p>
            </dd>
          </div>
        </dl>
        <div v-if="row.answer.type === 'noul'" class="space-y-2">
          <div
            class="h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-700"
            role="meter"
            :aria-label="`${row.id}: ${t('admin.providers.decisionTest.probabilityTrue')}`"
            aria-valuemin="0"
            aria-valuemax="1"
            :aria-valuenow="bounded(row.answer.noul)"
          >
            <div class="h-full rounded-full bg-primary-500" :style="{ width: `${bounded(row.answer.noul) * 100}%` }" />
          </div>
        </div>
        <div v-else class="space-y-3">
          <div class="text-xs font-medium text-gray-500 dark:text-dark-400">{{ t(row.answer.type === 'score' ? 'admin.providers.decisionTest.levelProbabilities' : 'admin.providers.decisionTest.optionProbabilities') }}</div>
          <div v-for="option in row.distribution" :key="option.id" class="space-y-1.5" :data-option="option.id">
            <div class="flex items-center justify-between gap-2 text-xs">
              <div class="flex min-w-0 items-center gap-2">
                <span class="min-w-0 break-words text-gray-700 dark:text-dark-200">
                  <span class="mr-1.5 break-all font-mono text-gray-500 dark:text-dark-400">{{ option.id }}</span>{{ option.description }}
                </span>
                <span v-if="option.selected" class="shrink-0 rounded-compact bg-primary-50 px-1.5 py-0.5 text-primary-600 dark:bg-primary-500/10 dark:text-primary-400">{{ t('admin.providers.decisionTest.selectedOption') }}</span>
              </div>
              <span class="shrink-0 font-mono tabular-nums text-gray-700 dark:text-dark-200">{{ percent(option.probability) }}</span>
            </div>
            <div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-800">
              <div
                class="h-full rounded-full"
                :class="option.selected || row.answer.type === 'score' ? 'bg-primary-500' : 'bg-gray-300 dark:bg-dark-500'"
                :style="{ width: `${bounded(option.probability) * 100}%` }"
              />
            </div>
          </div>
        </div>
        <p v-if="typeof row.answer.legend === 'string'" class="whitespace-pre-wrap break-words text-xs text-gray-500 dark:text-dark-400">{{ row.answer.legend }}</p>
      </article>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import { Icon } from '@/components/icons'
import type { SystemOneTestPayload, SystemOneTestResult } from './systemOneTest'

// 问题快照解释本次答案，结果按 Noul、Choice 和 Score 分别呈现。
const props = defineProps<{
  result?: SystemOneTestResult | null
  questions?: SystemOneTestPayload['questions'] | null
  usageValid?: boolean
  running?: boolean
}>()
const { t } = useI18n()
const labelId = useId()
const questionTypes = ['noul', 'choice', 'score'] as const
const typeNames = { noul: 'Noul', choice: 'Choice', score: 'Score' }
type Answer = SystemOneTestResult['answers'][string]
type Question = SystemOneTestPayload['questions'][string]
const percent = (value: number | undefined) => value == null ? '-' : `${(value * 100).toFixed(2)}%`
const bounded = (value: number | undefined) => Math.max(0, Math.min(1, value ?? 0))

// 评分等级优先读取响应 legend，选项和真假条件读取提交时的定义。
function description(answer: Answer, question: Question | undefined, key: string): string {
  const sources = answer.type === 'score' ? [answer.legend, question?.criteria] : [question?.criteria]
  for (const source of sources) {
    if (source && typeof source === 'object' && Object.prototype.hasOwnProperty.call(source, key)) {
      const value = (source as Record<string, unknown>)[key]
      return typeof value === 'string' ? value : value == null ? '' : JSON.stringify(value)
    }
  }
  return ''
}

// Choice 按概率排列；Score 按等级排列，便于阅读连续评分。
function distribution(answer: Answer, question?: Question) {
  const options = Object.entries(answer.probabilities ?? {}).map(([id, probability]) => ({
    id, probability, selected: answer.type === 'choice' && id === answer.choice,
    description: description(answer, question, id)
  }))
  if (answer.type === 'choice') options.sort((a, b) => b.probability - a.probability)
  else options.sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true }))
  return options
}

// 按提交顺序呈现问题，响应新增的 ID 放在最后。
const rows = computed(() => {
  const answers = props.result?.answers ?? {}
  const ordered = [...Object.keys(props.questions ?? {}).filter(id => Object.prototype.hasOwnProperty.call(answers, id))]
  const seen = new Set(ordered)
  ordered.push(...Object.keys(answers).filter(id => !seen.has(id)))
  return ordered.map(id => {
    const answer = answers[id]
    const question = props.questions && Object.prototype.hasOwnProperty.call(props.questions, id) ? props.questions[id] : undefined
    const noul = answer.type === 'noul'
    return {
      id, answer,
      title: question?.instructions || id,
      primaryLabel: t(noul ? 'admin.providers.decisionTest.trueProbability' : answer.type === 'choice' ? 'admin.providers.decisionTest.choiceResult' : 'admin.providers.decisionTest.scoreResult'),
      primaryValue: noul ? percent(answer.noul) : answer.type === 'choice' ? answer.choice : String(answer.score ?? '-'),
      primaryDescription: description(answer, question, noul ? 'true' : answer.choice ?? ''),
      secondaryLabel: t(noul ? 'admin.providers.decisionTest.falseProbability' : 'admin.providers.decisionTest.confidence'),
      secondaryValue: noul ? percent(answer.noul == null ? undefined : 1 - answer.noul) : percent(answer.confidence),
      secondaryDescription: noul ? description(answer, question, 'false') : '',
      distribution: distribution(answer, question)
    }
  })
})
</script>
