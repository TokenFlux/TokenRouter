<template>
  <svg
    v-if="iconInfo"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    xmlns="http://www.w3.org/2000/svg"
    class="provider-icon"
    fill="currentColor"
    fill-rule="evenodd"
    aria-hidden="true"
  >
    <path v-for="(p, idx) in iconInfo.paths" :key="idx" :d="p" :fill="iconColor" />
  </svg>
  <span
    v-else
    class="provider-icon-fallback"
    :style="{ width: size, height: size, fontSize: `calc(${size} * 0.58)`, color: iconColor }"
    aria-hidden="true"
  >
    {{ fallbackText }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { modelIconData } from '@/utils/modelIconData'
import { providerBrandDisplayName, resolveProviderBrand } from '@/utils/providerBrand'

const props = withDefaults(defineProps<{
  brand: string
  size?: string
  color?: string
}>(), {
  size: '18px',
  color: '',
})

const brandInfo = computed(() => resolveProviderBrand(props.brand))
const iconInfo = computed(() => {
  const iconKey = brandInfo.value.iconKey
  return iconKey ? modelIconData[iconKey] : null
})
const iconColor = computed(() => props.color || brandInfo.value.iconColor || iconInfo.value?.color || 'currentColor')
const displayName = computed(() => providerBrandDisplayName(props.brand))
const fallbackText = computed(() => (displayName.value === '未知供应商' ? '?' : displayName.value.charAt(0).toUpperCase()))
</script>

<style scoped>
.provider-icon,
.provider-icon-fallback {
  flex-shrink: 0;
}

.provider-icon-fallback {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-weight: 700;
  line-height: 1;
}
</style>
