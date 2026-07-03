<template>
  <span
    :class="[
      'inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium transition-colors',
      badgeClass
    ]"
  >
    <!-- 分组 platform 是接入格式；有展示品牌时优先按品牌渲染图标和配色。 -->
    <ProviderIcon v-if="brandName" :brand="brandName" size="14px" />
    <PlatformIcon v-else-if="platform" :platform="platform" size="sm" />
    <!-- Group name -->
    <span class="truncate">{{ name }}</span>
    <!-- Right side label -->
    <span v-if="showLabel" :class="labelClass">
      <template v-if="hasCustomRate">
        <!-- 原倍率删除线 + 专属倍率高亮 -->
        <span class="line-through opacity-50 mr-0.5">{{ rateMultiplier }}x</span>
        <span class="font-bold">{{ userRateMultiplier }}x</span>
      </template>
      <template v-else>
        {{ labelText }}
      </template>
    </span>
    <span v-if="hasPeakRate" :class="peakRateClass" :title="peakRateTitle">
      {{ peakRateText }}
    </span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { GroupPlatform } from '@/types'
import { currentServerTimezoneLabel, formatPeakRateWindow } from '@/utils/peak-rate'
import PlatformIcon from './PlatformIcon.vue'
import ProviderIcon from './ProviderIcon.vue'
import { resolveProviderBrand } from '@/utils/providerBrand'

interface Props {
  name: string
  platform?: GroupPlatform
  displayBrand?: string | null
  rateMultiplier?: number
  userRateMultiplier?: number | null // 用户专属倍率
  peakRateEnabled?: boolean
  peakStart?: string
  peakEnd?: string
  peakRateMultiplier?: number
  showRate?: boolean
  daysRemaining?: number | null
}

const props = withDefaults(defineProps<Props>(), {
  showRate: true,
  daysRemaining: null,
  userRateMultiplier: null,
  peakRateEnabled: false
})

const { t } = useI18n()

const brandName = computed(() => props.displayBrand?.trim() || '')

// 是否有专属倍率（且与默认倍率不同）
const hasCustomRate = computed(() => {
  return (
    props.userRateMultiplier !== null &&
    props.userRateMultiplier !== undefined &&
    props.rateMultiplier !== undefined &&
    props.userRateMultiplier !== props.rateMultiplier
  )
})

const hasPeakRate = computed(() => {
  return Boolean(props.showRate && props.peakRateEnabled && props.peakStart && props.peakEnd)
})

const peakRateText = computed(() => {
  return formatPeakRateWindow(
    {
      peak_rate_enabled: props.peakRateEnabled,
      peak_start: props.peakStart,
      peak_end: props.peakEnd,
      peak_rate_multiplier: props.peakRateMultiplier
    },
    currentServerTimezoneLabel()
  )
})

const peakRateTitle = computed(() => {
  return t('common.peakRateTooltip', { window: peakRateText.value })
})

// 是否显示右侧标签
const showLabel = computed(() => {
  if (!props.showRate) return false
  if (props.daysRemaining !== null && props.daysRemaining !== undefined) return true
  return props.rateMultiplier !== undefined || hasCustomRate.value
})

// Label text
const labelText = computed(() => {
  if (props.daysRemaining !== null && props.daysRemaining !== undefined) {
    if (props.daysRemaining <= 0) {
      return t('admin.users.expired')
    }
    return t('admin.users.daysRemaining', { days: props.daysRemaining })
  }
  return props.rateMultiplier !== undefined ? `${props.rateMultiplier}x` : ''
})

// Label style based on type and days remaining
const labelClass = computed(() => {
  const base = 'px-1.5 py-0.5 rounded text-[10px] font-semibold'

  if (props.daysRemaining === null || props.daysRemaining === undefined) {
    return `${base} bg-black/10 dark:bg-white/10`
  }

  if (props.daysRemaining <= 0 || props.daysRemaining <= 3) {
    return `${base} bg-red-200/80 text-red-800 dark:bg-red-800/50 dark:text-red-300`
  }
  if (props.daysRemaining <= 7) {
    return `${base} bg-amber-200/80 text-amber-800 dark:bg-amber-800/50 dark:text-amber-300`
  }

  if (props.platform === 'anthropic') {
    return `${base} bg-orange-200/60 text-orange-800 dark:bg-orange-800/40 dark:text-orange-300`
  }
  if (props.platform === 'openai') {
    return `${base} bg-emerald-200/60 text-emerald-800 dark:bg-emerald-800/40 dark:text-emerald-300`
  }
  if (props.platform === 'gemini') {
    return `${base} bg-blue-200/60 text-blue-800 dark:bg-blue-800/40 dark:text-blue-300`
  }
  if (props.platform === 'antigravity') {
    return `${base} bg-purple-200/60 text-purple-800 dark:bg-purple-800/40 dark:text-purple-300`
  }
  if (props.platform === 'grok') {
    return `${base} bg-zinc-300/70 text-zinc-800 dark:bg-zinc-700/60 dark:text-zinc-200`
  }
  return `${base} bg-violet-200/60 text-violet-800 dark:bg-violet-800/40 dark:text-violet-300`
})

const peakRateClass = computed(() => {
  return 'px-1.5 py-0.5 rounded text-[10px] font-semibold bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
})

const badgeClass = computed(() => {
  if (brandName.value) {
    return `ring-1 ring-inset ${resolveProviderBrand(brandName.value).badgeClass}`
  }
  if (props.platform === 'anthropic') {
    return 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-400'
  }
  if (props.platform === 'openai') {
    return 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'
  }
  if (props.platform === 'gemini') {
    return 'bg-sky-50 text-sky-700 dark:bg-sky-900/20 dark:text-sky-400'
  }
  if (props.platform === 'antigravity') {
    return 'bg-fuchsia-50 text-fuchsia-700 dark:bg-fuchsia-900/20 dark:text-fuchsia-400'
  }
  if (props.platform === 'grok') {
    return 'bg-zinc-100 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-200'
  }
  return 'bg-violet-50 text-violet-700 dark:bg-violet-900/20 dark:text-violet-400'
})
</script>
