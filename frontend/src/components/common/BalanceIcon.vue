<template>
  <span :class="['inline-flex shrink-0 items-center justify-center', sizeClass]">
    <span
      v-if="sanitizedSvg"
      class="balance-icon-svg inline-flex h-full w-full items-center justify-center"
      v-html="sanitizedSvg"
    ></span>
    <Icon
      v-else
      :name="fallbackName"
      :size="size"
      :stroke-width="strokeWidth"
    />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { sanitizeSvg } from '@/utils/sanitize'

type IconName = InstanceType<typeof Icon>['$props']['name']

const props = withDefaults(defineProps<{
  svg?: string
  fallbackName?: IconName
  size?: 'xs' | 'sm' | 'md' | 'lg' | 'xl'
  strokeWidth?: number
  useGlobalFallback?: boolean
}>(), {
  fallbackName: 'dollar',
  size: 'sm',
  strokeWidth: 1.5,
  useGlobalFallback: true
})

const { balanceIconSvg: globalBalanceIconSvg } = useBalanceDisplay()

const resolvedSvg = computed(() => {
  const localSvg = props.svg?.trim() ?? ''
  if (localSvg) {
    return localSvg
  }
  if (!props.useGlobalFallback) {
    return ''
  }
  return globalBalanceIconSvg.value
})

const sanitizedSvg = computed(() => sanitizeSvg(resolvedSvg.value))
const sizeClass = computed(
  () =>
    ({
      xs: 'h-3 w-3',
      sm: 'h-4 w-4',
      md: 'h-5 w-5',
      lg: 'h-6 w-6',
      xl: 'h-8 w-8'
    })[props.size]
)
</script>

<style scoped>
.balance-icon-svg :deep(svg) {
  display: block;
  height: 100%;
  width: 100%;
}
</style>
