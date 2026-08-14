<template>
  <Teleport to="body">
    <Transition name="modal">
      <div
        v-if="show"
        class="modal-overlay h-[100dvh] w-[100dvw] min-w-0 overflow-hidden"
        :style="zIndexStyle"
        :aria-labelledby="dialogId"
        role="dialog"
        aria-modal="true"
        @click.self="handleClose"
      >
        <!-- 动态视口单位避开移动端浏览器工具栏，vh/vw 规则由公共样式作为旧浏览器兜底。 -->
        <div
          ref="dialogRef"
          :class="['modal-content min-h-0 min-w-0 max-h-[95dvh] sm:max-h-[90dvh]', widthClasses]"
          @click.stop
        >
          <!-- 头部 -->
          <div class="modal-header min-w-0 max-w-full">
            <h3 :id="dialogId" class="modal-title min-w-0 break-words">
              {{ title }}
            </h3>
            <button
              @click="emit('close')"
              class="-mr-2 rounded-xl p-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-600 focus:outline-none focus-visible:ring-2 focus-visible:ring-black/10 focus-visible:ring-offset-2 dark:text-dark-500 dark:hover:bg-dark-700 dark:hover:text-dark-300 dark:focus-visible:ring-primary-500/30 dark:focus-visible:ring-offset-dark-900"
              aria-label="Close modal"
            >
              <Icon name="x" size="md" />
            </button>
          </div>

          <!-- 内容区 -->
          <div class="modal-body min-h-0 min-w-0 max-w-full">
            <slot></slot>
          </div>

          <!-- 底部 -->
          <div v-if="$slots.footer" class="modal-footer min-w-0 max-w-full">
            <slot name="footer"></slot>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script lang="ts">
let dialogIdCounter = 0
let openDialogCount = 0
</script>

<script setup lang="ts">
import { computed, watch, onMounted, onUnmounted, ref, nextTick } from 'vue'
import Icon from '@/components/icons/Icon.vue'

// 生成唯一ID以避免多个对话框时ID冲突
const dialogId = `modal-title-${++dialogIdCounter}`

// 焦点管理
const dialogRef = ref<HTMLElement | null>(null)
const modalBodyRef = ref<HTMLElement | null>(null)
let previousActiveElement: HTMLElement | null = null
const bodyScrollLocked = ref(false)

type DialogWidth = 'narrow' | 'normal' | 'wide' | 'extra-wide' | 'full'

interface Props {
  show: boolean
  title: string
  width?: DialogWidth
  closeOnEscape?: boolean
  closeOnClickOutside?: boolean
  zIndex?: number
}

interface Emits {
  (e: 'close'): void
}

const props = withDefaults(defineProps<Props>(), {
  width: 'normal',
  closeOnEscape: true,
  closeOnClickOutside: false,
  zIndex: 50
})

const emit = defineEmits<Emits>()

// 自定义层级会覆盖 CSS 中默认的 z-50。
const zIndexStyle = computed(() => {
  return props.zIndex !== 50 ? { zIndex: props.zIndex } : undefined
})

const widthClasses = computed(() => {
  // 移动端统一保留遮罩边距，避免弹窗内容的最小宽度撑开页面。
  const widths: Record<DialogWidth, string> = {
    narrow: 'max-w-[calc(100vw-1rem)] sm:max-w-md',
    normal: 'max-w-[calc(100vw-1rem)] sm:max-w-lg',
    wide: 'max-w-[calc(100vw-1rem)] sm:max-w-2xl md:max-w-3xl lg:max-w-4xl',
    'extra-wide': 'max-w-[calc(100vw-1rem)] sm:max-w-3xl md:max-w-4xl lg:max-w-5xl xl:max-w-6xl',
    full: 'max-w-[calc(100vw-1rem)] sm:max-w-4xl md:max-w-5xl lg:max-w-6xl xl:max-w-7xl'
  }
  return widths[props.width]
})

const handleClose = () => {
  if (props.closeOnClickOutside) {
    emit('close')
  }
}

const handleEscape = (event: KeyboardEvent) => {
  if (props.show && props.closeOnEscape && event.key === 'Escape') {
    emit('close')
  }
}

const lockBodyScroll = () => {
  openDialogCount++
  document.body.classList.add('modal-open')
  bodyScrollLocked.value = true
}

const unlockBodyScroll = () => {
  if (!bodyScrollLocked.value) {
    return
  }
  openDialogCount = Math.max(0, openDialogCount - 1)
  if (openDialogCount === 0) {
    document.body.classList.remove('modal-open')
  }
  bodyScrollLocked.value = false
}

// 弹窗打开时锁定页面滚动并管理焦点。
watch(
  () => props.show,
  async (isOpen) => {
    if (isOpen) {
      // 保存当前焦点元素
      previousActiveElement = document.activeElement as HTMLElement
      lockBodyScroll()

      // 等待DOM更新后设置焦点到对话框
      await nextTick()
      if (modalBodyRef.value) {
        modalBodyRef.value.scrollTop = 0
      }
      if (dialogRef.value) {
        const firstFocusable = dialogRef.value.querySelector<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
        )
        firstFocusable?.focus()
      }
    } else {
      unlockBodyScroll()
      // 恢复之前的焦点
      if (previousActiveElement && typeof previousActiveElement.focus === 'function') {
        previousActiveElement.focus()
      }
      previousActiveElement = null
    }
  },
  { immediate: true }
)

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleEscape)
  // 确保组件卸载时移除滚动锁定
  unlockBodyScroll()
})
</script>
