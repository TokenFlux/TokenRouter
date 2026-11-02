<template>
  <div class="flex justify-between gap-4" :class="layoutClass" :data-setting-row="setting">
    <div class="min-w-0">
      <div class="flex items-center gap-2">
        <label
          v-if="labelFor"
          :for="labelFor"
          class="text-sm font-medium text-primary-900 dark:text-dark-50"
          >{{ label }}</label
        >
        <span v-else class="text-sm font-medium text-primary-900 dark:text-dark-50">{{
          label
        }}</span>
        <HelpTooltip
          v-if="help"
          :content="help"
          :tooltip-id="`${id}-help`"
          trigger="both"
          :closable="false"
        >
          <template #trigger>
            <button
              type="button"
              class="inline-flex rounded-compact text-gray-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500"
              :aria-label="label"
              :aria-describedby="`${id}-help`"
            >
              <Icon name="questionCircle" size="sm" />
            </button>
          </template>
        </HelpTooltip>
      </div>
      <p v-if="hint" :id="`${id}-hint`" class="input-hint leading-relaxed">{{ hint }}</p>
      <slot name="hint" />
    </div>
    <div class="shrink-0" :class="field && 'w-full sm:w-56'">
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, useSlots } from 'vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Icon from '@/components/icons/Icon.vue'

/**
 * SettingRow.vue lays out a setting label, explanation, help tooltip, and slotted
 * control. Field rows stack on narrow screens, while desktop alignment depends on
 * whether a text or slot hint exists; input identifiers connect labels and help
 * descriptions.
 */

/**
 * props supplies label and explanation content, optional input association, and the
 * responsive layout choice for the slotted control.
 */
const props = defineProps<{
  id: string /* input association identifier */
  label: string /* visible field label */
  hint?: string /* inline explanation text */
  help?: string /* tooltip explanation text */
  setting?: string /* setting data identifier */

  /**
   * labelFor connects the visible label to an input or selector; omission renders an
   * ordinary text label without a focus target.
   */
  labelFor?: string
  field?: boolean /* responsive input layout */
}>()

/**
 * slots provides the named hint slot presence used alongside hint text to choose
 * control alignment.
 */
const slots = useSlots()

/**
 * layoutClass aligns controls with the top of explanatory hints or centers a lone
 * label; field rows stack before the small breakpoint.
 */
const layoutClass = computed(() => {
  const hasHint = Boolean(props.hint || slots.hint)
  if (!props.field) return hasHint ? 'items-start' : 'items-center'
  return hasHint
    ? 'flex-col gap-y-2 sm:flex-row sm:items-start'
    : 'flex-col gap-y-2 sm:flex-row sm:items-center'
})
</script>
