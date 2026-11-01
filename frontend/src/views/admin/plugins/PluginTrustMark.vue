<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import type { PluginTrust } from './pluginPresentation'

// PluginTrustMark.vue marks where a plugin's code came from. Index signatures and
// local delivery paths carry different review expectations, so each provenance has
// its own symbol: official signatures are green, unsigned or invalid sources use
// warning colors, and uploads or development directories stay neutral. The label is
// optional because list rows show only the symbol while headers spell it out.

// Props names the provenance and whether its label is rendered beside the symbol.
const props = defineProps<{
  trust: PluginTrust // Signature status or local source.
  label?: boolean // Renders the text label.
  detail?: string // Extra tooltip text such as the index name.
}>()
const { t } = useI18n()

// MARKS pair each provenance with a distinct symbol, color, and translation key.
const MARKS: Record<PluginTrust, { icon: IconName; tone: string; key: string }> = {
  official: {
    icon: 'shieldCheck',
    tone: 'text-emerald-600 dark:text-emerald-400',
    key: 'signatures.official',
  },
  custom: { icon: 'key', tone: 'text-gray-500 dark:text-dark-300', key: 'signatures.custom' },
  unsigned: {
    icon: 'exclamationTriangle',
    tone: 'text-amber-600 dark:text-amber-400',
    key: 'signatures.unsigned',
  },
  invalid: { icon: 'shieldX', tone: 'text-red-600 dark:text-red-400', key: 'signatures.invalid' },
  unknown: { icon: 'clock', tone: 'text-gray-500 dark:text-dark-300', key: 'signatures.unknown' },
  upload: { icon: 'upload', tone: 'text-gray-500 dark:text-dark-300', key: 'sources.upload' },
  dev: { icon: 'terminal', tone: 'text-gray-500 dark:text-dark-300', key: 'sources.dev' },
}

// mark, text, and title resolve the symbol, label, and tooltip for the provenance.
const mark = computed(() => MARKS[props.trust])
const text = computed(() => t(`admin.plugins.${mark.value.key}`))
const title = computed(() => (props.detail ? `${text.value} · ${props.detail}` : text.value))
</script>

<template>
  <span
    class="inline-flex shrink-0 items-center gap-1"
    :class="mark.tone"
    :title="title"
    :aria-label="label ? undefined : title"
    :role="label ? undefined : 'img'"
  >
    <Icon :name="mark.icon" size="sm" :animate-on-hover="false" />
    <span v-if="label">{{ text }}</span>
  </span>
</template>
