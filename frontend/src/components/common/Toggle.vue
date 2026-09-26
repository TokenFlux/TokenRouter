<template>
  <button
    type="button"
    @click="toggle"
    class="toggle-control relative inline-flex flex-shrink-0 cursor-pointer rounded-full border-0 p-0 transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 dark:focus:ring-offset-dark-800"
    :class="[
      props.modelValue ? props.onClass : offTrackClass,
      props.disabled && 'cursor-not-allowed opacity-50'
    ]"
    :data-size="props.size"
    :data-variant="props.variant"
    role="switch"
    :aria-checked="props.modelValue"
    :aria-disabled="props.disabled"
    :disabled="props.disabled"
  >
    <!-- 滑块尺寸、边距与开态位移全部由下方 CSS 变量推导,改档位只调变量不改位移。 -->
    <span
      class="toggle-thumb pointer-events-none absolute block transform rounded-full bg-white shadow ring-0 transition-transform duration-200 ease-in-out"
    />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  modelValue: boolean
  size?: 'sm' | 'md'
  /** inset:滑块内嵌留白(默认);flush:大滑块贴边(原 Headless 手写风)。 */
  variant?: 'inset' | 'flush'
  disabled?: boolean
  /** 开/关态轨道配色透传,默认沿用全站统一的品牌蓝与中性灰。 */
  onClass?: string
  offClass?: string
  /** 关态配色档:default=gray-300(既有消费方);soft=gray-200(手写开关迁移站点的原色)。 */
  offTone?: 'default' | 'soft'
}>(), {
  size: 'md',
  variant: 'inset',
  disabled: false,
  onClass: 'toggle-active',
  offClass: undefined,
  offTone: 'default'
})

const OFF_TONE_CLASSES = {
  default: 'bg-gray-300 dark:bg-dark-600',
  soft: 'bg-gray-200 dark:bg-dark-600'
} as const

const offTrackClass = computed(() => props.offClass ?? OFF_TONE_CLASSES[props.offTone])

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

// 开关保持受控，异步保存或确认期间由调用方决定何时更新值。
function toggle() {
  if (props.disabled) return
  emit('update:modelValue', !props.modelValue)
}
</script>

<style scoped>
/* 几何唯一来源:开态位移 = 轨道宽 − 滑块 − 2×边距,由 calc 推导。
   md(默认):轨道 44×24、滑块 16、边距 4 → 位移 20px;
   sm:轨道 36×20、滑块 16、边距 2 → 位移 16px;
   flush:md 轨道上滑块 20、边距 2 → 位移 20px(视觉等同旧手写 border-2 风格)。 */
.toggle-control {
  --toggle-track-w: 2.75rem;
  --toggle-track-h: 1.5rem;
  --toggle-thumb: 1rem;
  --toggle-inset: 0.25rem;
  width: var(--toggle-track-w);
  height: var(--toggle-track-h);
}

.toggle-control[data-size='sm'] {
  --toggle-track-w: 2.25rem;
  --toggle-track-h: 1.25rem;
  --toggle-inset: 0.125rem;
}

.toggle-control[data-variant='flush'] {
  --toggle-thumb: 1.25rem;
  --toggle-inset: 0.125rem;
}

.toggle-thumb {
  width: var(--toggle-thumb);
  height: var(--toggle-thumb);
  left: var(--toggle-inset);
  top: var(--toggle-inset);
}

.toggle-control[aria-checked='true'] .toggle-thumb {
  transform: translateX(calc(var(--toggle-track-w) - var(--toggle-thumb) - 2 * var(--toggle-inset)));
}
</style>
