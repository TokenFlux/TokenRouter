<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'

const props = withDefaults(defineProps<{
  content?: string
  trigger?: 'hover' | 'click' | 'both' | 'manual'
  // 手动提示由调用方控制，外部关闭时同步状态，避免下次提交无法重新打开。
  open?: boolean
  tooltipId?: string
  placement?: 'top' | 'bottom'
  widthClass?: string
  // 是否显示右上角关闭按钮；纯说明性的短提示可以关闭，靠点击外部/Esc 关闭。
  closable?: boolean
}>(), {
  trigger: 'hover',
  placement: 'top',
  widthClass: 'w-64',
  closable: true,
  open: undefined,
})

const emit = defineEmits<{ 'update:open': [value: boolean] }>()
const internalShow = ref(false)
const show = computed(() => props.open ?? internalShow.value)
const clickPinned = ref(false)
const resolvedPlacement = ref<'top' | 'bottom'>(props.placement)
const triggerRef = useTemplateRef<HTMLElement>('trigger')
const tooltipRef = useTemplateRef<HTMLElement>('tooltip')
const tooltipStyle = ref({ top: '0px', left: '0px' })
const caretLeft = ref('50%')

watch(show, (visible) => {
  if (visible) nextTick(updatePosition)
})

function hoverEnabled() {
  return props.trigger === 'hover' || props.trigger === 'both'
}

function clickEnabled() {
  return props.trigger === 'click' || props.trigger === 'both'
}

function openTooltip() {
  internalShow.value = true
  emit('update:open', true)
  nextTick(updatePosition)
}

function closeTooltip() {
  internalShow.value = false
  emit('update:open', false)
  clickPinned.value = false
}

function onEnter() {
  if (!hoverEnabled() || clickPinned.value) return
  openTooltip()
}

function isInside(container: HTMLElement | null, target: EventTarget | null): boolean {
  return target instanceof Node && !!container?.contains(target)
}

function onLeave(event?: MouseEvent) {
  if (!hoverEnabled() || clickPinned.value) return
  if (event && isInside(tooltipRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onTooltipLeave(event: MouseEvent) {
  if (props.trigger !== 'hover') return
  if (isInside(triggerRef.value, event.relatedTarget)) return
  closeTooltip()
}

function onClick(event: MouseEvent) {
  if (!clickEnabled()) return
  event.stopPropagation()
  if (clickPinned.value) {
    closeTooltip()
    return
  }
  clickPinned.value = true
  openTooltip()
}

function onDocumentInteraction(event: Event) {
  if ((!clickEnabled() && props.trigger !== 'manual') || !show.value) return
  const target = event.target as Node | null
  if (!target) return
  if (triggerRef.value?.contains(target) || tooltipRef.value?.contains(target)) return
  closeTooltip()
}

function onDocumentKeydown(event: KeyboardEvent) {
  if (!clickEnabled() && props.trigger !== 'manual') return
  if (event.key === 'Escape') {
    closeTooltip()
  }
}

function onViewportChange() {
  if (!show.value) return
  updatePosition()
}

function updatePosition() {
  const el = triggerRef.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const tooltipRect = tooltipRef.value?.getBoundingClientRect()
  const tooltipWidth = tooltipRect?.width ?? 0
  const tooltipHeight = tooltipRect?.height ?? 0
  const centeredLeft = rect.left + rect.width / 2
  const viewportPadding = 12
  const halfTooltipWidth = tooltipWidth / 2
  const minLeft = halfTooltipWidth + viewportPadding
  const maxLeft = Math.max(
    minLeft,
    window.innerWidth - halfTooltipWidth - viewportPadding,
  )
  const left = tooltipWidth > 0
    ? Math.min(maxLeft, Math.max(minLeft, centeredLeft))
    : centeredLeft
  let placement = props.placement
  let tooltipTop = rect.bottom + 8
  if (tooltipHeight > 0) {
    const topPosition = rect.top - 8 - tooltipHeight
    const bottomPosition = rect.bottom + 8
    const fitsAbove = topPosition >= viewportPadding
    const fitsBelow = bottomPosition + tooltipHeight <= window.innerHeight - viewportPadding

    if (placement === 'bottom' && !fitsBelow && fitsAbove) {
      placement = 'top'
      tooltipTop = topPosition
    } else if (placement === 'top' && !fitsAbove && fitsBelow) {
      placement = 'bottom'
      tooltipTop = bottomPosition
    } else if (!fitsAbove && !fitsBelow) {
      tooltipTop = Math.max(
        viewportPadding,
        window.innerHeight - tooltipHeight - viewportPadding,
      )
    } else if (placement === 'top') {
      tooltipTop = topPosition
    }
  } else if (placement === 'top') {
    tooltipTop = rect.top - 8
  }
  resolvedPlacement.value = placement
  // 提示框为了避开屏幕边缘发生横移时，箭头仍对准真实触发点。
  caretLeft.value = `${Math.max(8, Math.min(tooltipWidth - 8, centeredLeft - left + halfTooltipWidth))}px`
  tooltipStyle.value = {
    top: `${placement === 'top' ? tooltipTop + tooltipHeight + 8 : tooltipTop}px`,
    left: `${left}px`,
  }
}

onMounted(() => {
  document.addEventListener('click', onDocumentInteraction, true)
  document.addEventListener('pointerdown', onDocumentInteraction, true)
  document.addEventListener('touchstart', onDocumentInteraction, { capture: true, passive: true })
  document.addEventListener('keydown', onDocumentKeydown)
  window.addEventListener('resize', onViewportChange)
  window.addEventListener('scroll', onViewportChange, true)
  if (show.value) nextTick(updatePosition)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', onDocumentInteraction, true)
  document.removeEventListener('pointerdown', onDocumentInteraction, true)
  document.removeEventListener('touchstart', onDocumentInteraction, true)
  document.removeEventListener('keydown', onDocumentKeydown)
  window.removeEventListener('resize', onViewportChange)
  window.removeEventListener('scroll', onViewportChange, true)
})
</script>

<template>
  <div
    ref="trigger"
    class="group relative inline-flex items-center align-middle"
    :class="{ 'ml-1': trigger !== 'manual' }"
    @mouseenter="onEnter"
    @mouseleave="onLeave"
    @click="onClick"
  >
    <!-- 触发图标 -->
    <slot name="trigger">
      <svg
        class="h-4 w-4 cursor-help text-gray-400 transition-colors hover:text-primary-600 dark:text-gray-500 dark:hover:text-primary-400"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        stroke-width="2"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
        />
      </svg>
    </slot>

    <!-- 挂载到 body，避免被弹窗的 overflow 裁剪 -->
    <Teleport to="body">
      <!-- before: 伪元素向下延伸一段透明区域，盖住提示框与触发图标之间的空隙，让指针能连续移入提示框。 -->
      <div
        ref="tooltip"
        :id="tooltipId"
        v-show="show"
        role="tooltip"
        :class="[
          'fixed z-help-tooltip max-w-[calc(100vw-1.5rem)] -translate-x-1/2 rounded-control bg-gray-900 text-white shadow-xl ring-1 ring-white/10 dark:bg-gray-800',
          resolvedPlacement === 'top' ? '-translate-y-full' : 'translate-y-0',
          props.widthClass,
        ]"
        :style="{
          top: resolvedPlacement === 'top'
            ? `calc(${tooltipStyle.top} - 8px)`
            : tooltipStyle.top,
          left: tooltipStyle.left,
        }"
        @mouseleave="onTooltipLeave"
      >
        <!-- 滚动只发生在内容层，避免小箭头伸出边框被 overflow 裁剪或挤出滚动条。 -->
        <div class="relative max-h-[calc(100vh-1.5rem)] overflow-y-auto p-3 text-xs leading-relaxed">
          <button
            v-if="clickEnabled() && closable"
            type="button"
            class="absolute right-1.5 top-1.5 rounded-compact p-1 text-gray-300 transition-colors hover:bg-white/10 hover:text-white"
            aria-label="Close"
            @click.stop="closeTooltip"
          >
            <svg class="h-3.5 w-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
          <slot>{{ content }}</slot>
        </div>
        <div
          class="absolute h-2 w-2 -translate-x-1/2 rotate-45 bg-gray-900 dark:bg-gray-800"
          :style="{ left: caretLeft }"
          :class="resolvedPlacement === 'top' ? '-bottom-1' : '-top-1'"
        ></div>
      </div>
    </Teleport>
  </div>
</template>
