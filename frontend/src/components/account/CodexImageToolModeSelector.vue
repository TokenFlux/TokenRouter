<template>
  <div
    class="overflow-hidden rounded-lg border border-sky-100 bg-sky-50/60 shadow-sm dark:border-sky-900/50 dark:bg-sky-950/20"
    role="radiogroup"
    :aria-label="t('admin.accounts.openai.codexImageTool')"
    :data-testid="`${testIdPrefix}-selector`"
  >
    <div class="flex items-start gap-3 px-4 py-3">
      <div class="mt-0.5 flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-white text-sky-600 shadow-sm ring-1 ring-sky-100 dark:bg-dark-800 dark:text-sky-300 dark:ring-sky-900/60">
        <Icon name="sparkles" size="sm" />
      </div>
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2">
          <span class="input-label mb-0">{{ t('admin.accounts.openai.codexImageTool') }}</span>
          <span class="rounded-full px-2 py-0.5 text-xs font-medium" :class="badgeClass">
            {{ badgeLabel }}
          </span>
        </div>
        <p class="mt-1 text-xs leading-5 text-slate-600 dark:text-slate-300">
          {{ t('admin.accounts.openai.codexImageToolDesc') }}
        </p>
      </div>
    </div>
    <div class="border-t border-sky-100 bg-white/70 p-2 dark:border-sky-900/50 dark:bg-dark-800/70">
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <button
          v-for="option in options"
          :key="option.value"
          type="button"
          role="radio"
          :aria-checked="modelValue === option.value"
          :data-testid="`${testIdPrefix}-${option.value}`"
          :class="[
            'group flex min-h-[62px] items-start gap-2 rounded-md border px-3 py-2 text-left transition-all',
            modelValue === option.value
              ? option.selectedCardClass
              : 'border-transparent bg-transparent text-slate-600 hover:border-gray-200 hover:bg-gray-50 dark:text-slate-300 dark:hover:border-dark-500 dark:hover:bg-dark-700'
          ]"
          @click="emit('update:modelValue', option.value)"
        >
          <span
            :class="[
              'mt-0.5 flex h-5 w-5 flex-shrink-0 items-center justify-center rounded-full border transition-colors',
              modelValue === option.value
                ? option.selectedDotClass
                : 'border-gray-300 text-transparent group-hover:border-gray-400 dark:border-dark-500'
            ]"
          >
            <Icon name="check" size="xs" :stroke-width="2" />
          </span>
          <span class="min-w-0">
            <span class="block text-sm font-medium">{{ option.label }}</span>
            <span class="mt-0.5 block text-xs leading-4 text-slate-500 dark:text-slate-400">
              {{ option.description }}
            </span>
          </span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/icons/Icon.vue'
import type { CodexImageToolMode } from '@/utils/codexImageToolMode'

const props = withDefaults(defineProps<{
  modelValue: CodexImageToolMode
  testIdPrefix?: string
}>(), {
  testIdPrefix: 'codex-image-tool',
})

const emit = defineEmits<{
  (event: 'update:modelValue', value: CodexImageToolMode): void
}>()

const { t } = useI18n()

// 四态共用同一套视觉语义，避免三个入口对同一策略给出不同提示。
const options = computed(() => [
  {
    value: 'inherit' as const,
    label: t('admin.accounts.openai.codexImageToolInherit'),
    description: t('admin.accounts.openai.codexImageToolInheritDesc'),
    selectedCardClass: 'border-sky-300 bg-sky-50 text-sky-900 shadow-sm ring-1 ring-sky-200 dark:border-sky-700 dark:bg-sky-900/25 dark:text-sky-100 dark:ring-sky-800',
    selectedDotClass: 'border-sky-500 bg-sky-500 text-white',
  },
  {
    value: 'enabled' as const,
    label: t('admin.accounts.openai.codexImageToolEnabled'),
    description: t('admin.accounts.openai.codexImageToolEnabledDesc'),
    selectedCardClass: 'border-emerald-300 bg-emerald-50 text-emerald-900 shadow-sm ring-1 ring-emerald-200 dark:border-emerald-700 dark:bg-emerald-900/25 dark:text-emerald-100 dark:ring-emerald-800',
    selectedDotClass: 'border-emerald-500 bg-emerald-500 text-white',
  },
  {
    value: 'disabled' as const,
    label: t('admin.accounts.openai.codexImageToolDisabled'),
    description: t('admin.accounts.openai.codexImageToolDisabledDesc'),
    selectedCardClass: 'border-amber-300 bg-amber-50 text-amber-900 shadow-sm ring-1 ring-amber-200 dark:border-amber-700 dark:bg-amber-900/25 dark:text-amber-100 dark:ring-amber-800',
    selectedDotClass: 'border-amber-500 bg-amber-500 text-white',
  },
  {
    value: 'block' as const,
    label: t('admin.accounts.openai.codexImageToolBlock'),
    description: t('admin.accounts.openai.codexImageToolBlockDesc'),
    selectedCardClass: 'border-rose-300 bg-rose-50 text-rose-900 shadow-sm ring-1 ring-rose-200 dark:border-rose-700 dark:bg-rose-900/25 dark:text-rose-100 dark:ring-rose-800',
    selectedDotClass: 'border-rose-500 bg-rose-500 text-white',
  },
])

const badgeLabel = computed(() => {
  switch (props.modelValue) {
    case 'enabled':
      return t('admin.accounts.openai.codexImageToolBadgeEnabled')
    case 'disabled':
      return t('admin.accounts.openai.codexImageToolBadgeDisabled')
    case 'block':
      return t('admin.accounts.openai.codexImageToolBadgeBlock')
    default:
      return t('admin.accounts.openai.codexImageToolBadgeInherit')
  }
})

const badgeClass = computed(() => {
  switch (props.modelValue) {
    case 'enabled':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    case 'disabled':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
    case 'block':
      return 'bg-rose-100 text-rose-700 dark:bg-rose-900/40 dark:text-rose-300'
    default:
      return 'bg-slate-100 text-slate-600 dark:bg-dark-600 dark:text-slate-300'
  }
})
</script>
