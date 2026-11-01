<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginState } from '@/api/admin/plugins.types'
import Icon from '@/components/icons/Icon.vue'

// PluginStatusPill.vue shows one visible plugin lifecycle state as a colored dot and
// label. Callers pass the state already resolved by visibleState, so list rows and
// the detail header agree on precedence between global policy, enablement, and the
// runtime observation. Transitional states pulse; frontend-only plugins show a
// layers symbol because they have no process to be running or stopped.

// Props selects the resolved state to display.
const props = defineProps<{
  state: PluginState // Visible lifecycle state.
}>()
const { t } = useI18n()

// TONES assign semantic text colors; the dot inherits them through bg-current.
const TONES: Record<PluginState, string> = {
  running: 'text-emerald-600 dark:text-emerald-400',
  starting: 'text-amber-600 dark:text-amber-400',
  backoff: 'text-amber-600 dark:text-amber-400',
  draining: 'text-amber-600 dark:text-amber-400',
  crashloop: 'text-red-600 dark:text-red-400',
  failed: 'text-red-600 dark:text-red-400',
  stopped: 'text-gray-500 dark:text-dark-400',
  static: 'text-gray-500 dark:text-dark-300',
}

// pulsing marks transitional states whose dot animates.
const pulsing = computed(() => ['starting', 'backoff', 'draining'].includes(props.state))
</script>

<template>
  <span
    class="inline-flex shrink-0 items-center gap-1.5 whitespace-nowrap text-xs font-medium"
    :class="TONES[state]"
  >
    <Icon v-if="state === 'static'" name="layers" size="xs" :animate-on-hover="false" />
    <span
      v-else
      class="h-1.5 w-1.5 rounded-full bg-current"
      :class="pulsing && 'animate-pulse'"
      aria-hidden="true"
    />
    {{ t(`admin.plugins.states.${state}`) }}
  </span>
</template>
