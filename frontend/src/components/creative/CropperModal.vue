<template>
  <BaseDialog :show="show" :title="t('creative.cropper.title')" width="wide" @close="handleCancel">
    <div class="space-y-4">
      <!-- cropperjs v2 会把 cropper-canvas 注入该容器 -->
      <div ref="containerRef" class="creative-cropper-container overflow-hidden rounded-control bg-black/5 dark:bg-dark-950"></div>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('creative.cropper.hint') }}</p>
    </div>

    <template #footer>
      <div class="flex justify-end space-x-3">
        <button
          type="button"
          class="rounded-control border border-primary-900/10 bg-white px-4 py-2 text-sm font-medium text-gray-700 hover:border-black/20 hover:bg-gray-50 focus:outline-none focus:ring-2 focus:ring-black/10 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-200 dark:hover:border-dark-600 dark:hover:bg-dark-600 dark:focus:ring-primary-500"
          @click="handleSkip"
        >
          {{ t('creative.cropper.skip') }}
        </button>
        <button
          type="button"
          :disabled="processing"
          class="rounded-control bg-primary-600 px-4 py-2 text-sm font-medium text-white hover:bg-primary-700 focus:outline-none focus:ring-2 focus:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-60"
          @click="handleConfirm"
        >
          {{ processing ? t('common.processing') : t('creative.cropper.confirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
/**
 * cropperjs v2 裁剪弹窗
 * v2 为 Web Component 实现（Shadow DOM 内联样式），无需引入额外 CSS；
 * 通过 selection.$toCanvas() 直接拿到选区内的裁剪结果。
 */
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Cropper from 'cropperjs'
import BaseDialog from '@/components/common/BaseDialog.vue'

interface Props {
  show: boolean
  blob: Blob | null
}

interface Emits {
  (e: 'confirm', blob: Blob): void
  (e: 'skip', blob: Blob): void
  (e: 'cancel'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()
const { t } = useI18n()

const containerRef = ref<HTMLDivElement | null>(null)
const processing = ref(false)
let cropper: Cropper | null = null
let objectUrl = ''

// blob 变化且弹窗打开时（重新）初始化裁剪器
watch(
  () => [props.show, props.blob] as const,
  ([show, blob]) => {
    if (show && blob) {
      void initCropper(blob)
    } else {
      teardownCropper()
    }
  },
  { immediate: true },
)

async function initCropper(blob: Blob): Promise<void> {
  teardownCropper()
  await new Promise((resolve) => setTimeout(resolve, 0))
  const container = containerRef.value
  if (!container) return

  objectUrl = URL.createObjectURL(blob)
  const image = document.createElement('img')
  image.src = objectUrl
  image.alt = 'cropper-source'
  container.appendChild(image)
  // container 缺省时取图片父元素，这里显式传入保证挂载位置可控
  cropper = new Cropper(image, { container })
}

function teardownCropper(): void {
  if (cropper) {
    cropper.destroy()
    cropper = null
  }
  if (containerRef.value) {
    containerRef.value.innerHTML = ''
  }
  if (objectUrl) {
    URL.revokeObjectURL(objectUrl)
    objectUrl = ''
  }
}

// 确认裁剪：取选区 canvas 转 PNG blob
async function handleConfirm(): Promise<void> {
  const selection = cropper?.getCropperSelection()
  if (!selection) {
    handleSkip()
    return
  }
  processing.value = true
  try {
    const canvas = await selection.$toCanvas()
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, 'image/png'))
    if (blob) {
      emit('confirm', blob)
    }
  } finally {
    processing.value = false
  }
}

// 跳过裁剪：原图直接作为源图
function handleSkip(): void {
  if (props.blob) {
    emit('skip', props.blob)
  }
}

function handleCancel(): void {
  emit('cancel')
}

onBeforeUnmount(() => {
  teardownCropper()
})
</script>

<style scoped>
/* 限制裁剪画布高度，宽度由 cropper-canvas 自适应填满容器 */
.creative-cropper-container :deep(cropper-canvas) {
  display: block;
  width: 100%;
  height: min(60vh, 480px);
}
</style>
