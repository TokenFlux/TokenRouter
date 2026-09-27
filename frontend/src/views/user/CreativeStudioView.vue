<template>
  <AppLayout full-viewport>
    <!-- 整个内容区即无限画布背景:fullViewport 模式下 app-main 无内边距,画布经 flex 链铺满全幅(含顶部,点阵直达 header 边界) -->
    <!-- --creative-history-w 是历史侧栏在 md 起的宽度；侧栏展开时 --creative-history-reserve 记下侧栏占用的右侧宽度，
         顶部工具条和输入框据此在左侧剩余区域居中。窄屏侧栏盖在画布上，这个变量保持未设置。 -->
    <div
      ref="stageRef"
      class="relative h-full min-h-0"
      :class="[
        '[--creative-history-w:20rem] xl:[--creative-history-w:24rem]',
        historyOpen && 'md:[--creative-history-reserve:calc(var(--creative-history-w)_+_0.75rem)]',
      ]"
    >
      <CreativeCanvas ref="canvasRef" class="absolute inset-0" :operation="studio.operation.value" :allowed-mimes="studio.capabilities.value.allowed_mime_types" @error="onCanvasError" />
      <CreativeRunHistory
        ref="historyRef"
        v-model:open="historyOpen"
        :studio="studio"
        :active-run-count="activeRunCount"
        @retry="onRetryRun"
      />

      <!-- 设置：左上角齿轮按钮，点击向下展开菜单 -->
      <div ref="settingsRef" class="absolute left-3 top-3 z-20">
        <div class="canvas-island rounded-surface p-1">
          <button
            type="button"
            class="canvas-tool-btn"
            :class="settingsOpen && 'canvas-tool-btn-active'"
            :title="t('creative.canvas.settings')"
            :aria-expanded="settingsOpen"
            @click="settingsOpen = !settingsOpen"
          >
            <Icon name="cog" size="md" />
          </button>
        </div>
        <!-- 向下展开的设置菜单：清空画布 / 清空本机创作数据 -->
        <MotionTransition name="pop-float">
          <div
            v-if="settingsOpen"
            class="settings-pop-float dropdown left-0 top-12 w-56 py-0"
          >
            <div class="menu-section">
              <button type="button" class="menu-item" @click="onResetCanvas">
                <Icon name="trash" size="sm" />
                {{ t('creative.canvas.reset') }}
              </button>
              <button type="button" class="menu-item menu-item-danger" @click="onClearRequested">
                <Icon name="database" size="sm" />
                {{ t('creative.history.clearData') }}
              </button>
            </div>
          </div>
        </MotionTransition>
      </div>

      <!-- 空画布引导：场景恢复完成且画布上没有图片时显示，放上第一张图后淡出 -->
      <MotionTransition name="fade">
        <div
          v-if="showEmptyGuide"
          class="pointer-events-none absolute inset-x-4 top-1/3 z-10 flex flex-col items-center gap-4 text-center"
          data-testid="creative-empty-guide"
        >
          <div class="space-y-1">
            <p class="text-base font-semibold text-gray-800 dark:text-dark-50">{{ t('creative.empty.title') }}</p>
            <p class="text-sm text-gray-500 dark:text-dark-300">{{ t('creative.empty.description') }}</p>
          </div>
          <div class="pointer-events-auto flex max-w-xl flex-wrap justify-center gap-2">
            <button type="button" class="canvas-island empty-guide-chip" @click="canvasRef?.openFilePicker()">
              <Icon name="upload" size="sm" />
              {{ t('creative.empty.upload') }}
            </button>
            <button
              v-for="example in EXAMPLE_PROMPT_KEYS"
              :key="example"
              type="button"
              class="canvas-island empty-guide-chip"
              @click="useExamplePrompt(example)"
            >
              <Icon name="sparkles" size="sm" class="text-primary-600 dark:text-primary-500" />
              {{ t(`creative.empty.examples.${example}.label`) }}
            </button>
          </div>
        </div>
      </MotionTransition>

      <!-- 聊天式输入框：固定底部居中，不随选中图片移动；历史侧栏展开时在侧栏左侧居中 -->
      <div class="creative-composer-dock absolute bottom-4 z-30 -translate-x-1/2">
        <CreativeComposer
          ref="composerRef"
          :studio="studio"
          :guide="canvasRef?.guideKey ?? null"
          @generate="onGenerate"
        />
      </div>

      <!-- 提交反馈：品牌青圆点从发送按钮飞向历史入口，坐标由两个按钮的运行时位置决定。 -->
      <div
        v-if="submissionAnimationVisible"
        class="creative-submit-flight"
        :style="submissionAnimationStyle"
        @animationend="onSubmitFlightEnd"
        aria-hidden="true"
      ></div>
    </div>

    <ConfirmDialog
      :show="showClearConfirm"
      :title="t('creative.history.confirmClearTitle')"
      :message="t('creative.history.confirmClearMessage')"
      :confirm-text="t('creative.history.clearData')"
      danger
      @confirm="onClearLocalData"
      @cancel="showClearConfirm = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
/**
 * 创作台主视图：全幅无限画布 + 聊天式输入框。
 * - 输入框固定底部居中（跟随选中图片在缩放场景下位置不稳定）；生成状态和画布引导显示在输入框顶部的状态行
 * - 生成时从画布收集输入：edit/inpaint 取当前选中图片的原始 blob，inpaint 另取画笔 mask 导出
 * - 注册画布桥接：收割成功的输出自动上板；历史里的输出可一键导入画布
 * - 左上角设置（清空画布 / 清空本机创作数据）、右上角历史、顶部工具栏（上传 / 下载 / 局部重绘画笔组 / 删除），三块浮层同高
 * - 历史从右侧拉开侧栏：md 起停靠右侧，工具条和输入框移到侧栏左侧区域居中；窄屏侧栏带遮罩盖住画布
 * - 画布没有图片时，中央显示上传入口和示例提示词
 * 图片本体只存当前浏览器（IndexedDB），生成时才把所选素材发给模型供应商。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onClickOutside } from '@vueuse/core'
import MotionTransition from '@/components/common/MotionTransition.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import CreativeComposer from '@/components/creative/CreativeComposer.vue'
import CreativeCanvas from '@/components/creative/CreativeCanvas.vue'
import CreativeRunHistory from '@/components/creative/CreativeRunHistory.vue'
import { CREATIVE_RUN_TERMINAL_STATUSES } from '@/api/creative'
import { useCreativeStudio } from '@/composables/useCreativeStudio'
import { useAppStore } from '@/stores'

const { t } = useI18n()
const appStore = useAppStore()
const studio = useCreativeStudio()

const canvasRef = ref<InstanceType<typeof CreativeCanvas> | null>(null)
const composerRef = ref<InstanceType<typeof CreativeComposer> | null>(null)
const historyRef = ref<InstanceType<typeof CreativeRunHistory> | null>(null)
const stageRef = ref<HTMLDivElement | null>(null)
const settingsRef = ref<HTMLDivElement | null>(null)
const showClearConfirm = ref(false)
// 设置菜单开关（齿轮在画布左上角，菜单向下展开）
const settingsOpen = ref(false)
// 历史侧栏开关，决定工具条和输入框是否给侧栏让位
const historyOpen = ref(false)
const submissionAnimationVisible = ref(false)
const submissionAnimationStyle = ref<Record<string, string>>({})
let submissionAnimationFrame: number | null = null

const activeRunCount = computed(
  () => studio.runHistory.value.filter((run) => !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)).length,
)

// 空画布引导里的示例提示词，文案在 creative.empty.examples 下
const EXAMPLE_PROMPT_KEYS = ['poster', 'product', 'illustration'] as const

// 场景恢复期间画布暂时为空，等恢复结束再判断是否显示引导
const showEmptyGuide = computed(() => Boolean(canvasRef.value?.sceneReady) && canvasRef.value?.imageCount === 0)

onClickOutside(settingsRef, () => {
  settingsOpen.value = false
})

function useExamplePrompt(key: (typeof EXAMPLE_PROMPT_KEYS)[number]): void {
  composerRef.value?.fillPrompt(t(`creative.empty.examples.${key}.prompt`))
}

// ==================== 生命周期 ====================

onMounted(() => {
  // 画布桥接：收割自动上板 + 历史输出导入画布；桥接方法自身保证异常不外溢
  studio.registerCanvasBridge({
    placeOutput: (asset) => {
      return canvasRef.value?.placeOutput(asset)
    },
    importToCanvas: (blob, runId, outputIndex) => {
      return canvasRef.value?.placeOutput({ blob, runId, outputIndex })
    },
  })
  void studio.loadModels()
  void studio.refreshHistory()
})

onBeforeUnmount(() => {
  studio.registerCanvasBridge(null)
  if (submissionAnimationFrame !== null && typeof cancelAnimationFrame === 'function') {
    cancelAnimationFrame(submissionAnimationFrame)
    submissionAnimationFrame = null
  }
})

// 计算提交反馈的起点与终点，保证桌面端和移动端都从实际按钮飞向历史入口。
function playSubmissionAnimation(): void {
  cancelSubmissionAnimation()
  const stage = stageRef.value
  const source = composerRef.value?.sendButtonRef
  const target = historyRef.value?.historyButtonRef
  if (!stage || !source || !target) return
  const stageRect = stage.getBoundingClientRect()
  const sourceRect = source.getBoundingClientRect()
  const targetRect = target.getBoundingClientRect()
  const startX = sourceRect.left + sourceRect.width / 2 - stageRect.left
  const startY = sourceRect.top + sourceRect.height / 2 - stageRect.top
  const endX = targetRect.left + targetRect.width / 2 - stageRect.left
  const endY = targetRect.top + targetRect.height / 2 - stageRect.top
  submissionAnimationStyle.value = {
    left: `${startX}px`,
    top: `${startY}px`,
    '--flight-x': `${endX - startX}px`,
    '--flight-y': `${endY - startY}px`,
  }
  submissionAnimationVisible.value = false
  if (typeof requestAnimationFrame === 'function') {
    submissionAnimationFrame = requestAnimationFrame(() => {
      submissionAnimationFrame = null
      submissionAnimationVisible.value = true
    })
  } else {
    submissionAnimationVisible.value = true
  }
  // 节点移除由 CSS 动画的 animationend 事件驱动（见 onSubmitFlightEnd），
  // reduced-motion 下动画缩到 1ms，仍通过动画事件通知完成。
}

function onSubmitFlightEnd(event: AnimationEvent): void {
  // 只响应飞行动画本身，防御未来在元素上叠加其他动画时误收。
  if (event.animationName !== 'creative-submit-flight') return
  submissionAnimationVisible.value = false
  historyRef.value?.bump()
}

function cancelSubmissionAnimation(): void {
  submissionAnimationVisible.value = false
  if (submissionAnimationFrame !== null && typeof cancelAnimationFrame === 'function') {
    cancelAnimationFrame(submissionAnimationFrame)
    submissionAnimationFrame = null
  }
}

// ==================== 生成与画布输入采集 ====================

// 提交生成：edit 取画布上全部图片作多参考图；inpaint 取选中图片 + 画笔 mask
async function onGenerate(): Promise<void> {
  playSubmissionAnimation()
  const operation = studio.operation.value
  let sourceBlobs: Blob[] = []
  let maskBlob: Blob | null = null
  if (operation === 'edit') {
    // 编辑模式以用户选择的参考图集合为准（点击单选、Shift 加选）
    sourceBlobs = (await canvasRef.value?.getEditRefBlobs()) ?? []
    if (!sourceBlobs.length) {
      cancelSubmissionAnimation()
      studio.error.value = t('creative.panel.selectImageHint')
      return
    }
  } else if (operation === 'inpaint') {
    const blob = await canvasRef.value?.getSelectedImageBlob()
    if (!blob) {
      cancelSubmissionAnimation()
      studio.error.value = t('creative.panel.selectImageHint')
      return
    }
    sourceBlobs = [blob]
  }
  if (operation === 'inpaint') {
    try {
      maskBlob = (await canvasRef.value?.getMaskBlob()) ?? null
    } catch (error) {
      console.error('Failed to export mask:', error)
    }
    if (!maskBlob) {
      cancelSubmissionAnimation()
      studio.error.value = t('creative.error.maskRequired')
      return
    }
  }
  const submitted = await studio.createRun({ sourceBlobs, maskBlob })
  if (!submitted) cancelSubmissionAnimation()
}

// 历史里失败任务的重试：先用本地快照还原输入区（提示词与参数），
// 再走与点击生成完全相同的画布采集与提交路径，避免维护第二条提交分支。
async function onRetryRun(runId: string): Promise<void> {
  const restored = await studio.restoreRunInput(runId)
  if (!restored) return
  await onGenerate()
}

function onCanvasError(message: string): void {
  studio.error.value = message
}

// 设置弹层里的清空画布入口：收起弹层并清空画布全部对象
function onResetCanvas(): void {
  settingsOpen.value = false
  canvasRef.value?.resetCanvas()
}

// 设置弹层里的清空入口：收起弹层并弹出确认
function onClearRequested(): void {
  settingsOpen.value = false
  showClearConfirm.value = true
}

async function onClearLocalData(): Promise<void> {
  showClearConfirm.value = false
  try {
    await studio.clearLocalData()
    canvasRef.value?.resetCanvas()
    appStore.showSuccess(t('creative.history.clearSuccess'))
  } catch {
    // 清空失败时给出明确提示，错误详情已写入 studio.error
    appStore.showError(t('creative.error.clearFailed'))
  }
}
</script>

<style scoped>
/* 设置菜单动效用全局 pop-float,锚点方向(左上锚、向上收起)用局部变量表达。 */
.settings-pop-float {
  --pop-shift: calc(-1 * var(--motion-shift));
}

/* 输入框在侧栏左侧的区域居中，最大宽度同样扣掉侧栏；侧栏开合时平移过去 */
.creative-composer-dock {
  left: calc((100% - var(--creative-history-reserve, 0px)) / 2);
  max-width: calc(100% - var(--creative-history-reserve, 0px) - 1.5rem);
  transition:
    left var(--motion-layout) var(--motion-ease),
    max-width var(--motion-layout) var(--motion-ease);
}

/* 空画布引导里的上传和示例提示词按钮 */
.empty-guide-chip {
  @apply inline-flex h-9 items-center gap-2 rounded-full px-4 text-sm text-gray-700 transition-colors hover:text-gray-900;
  @apply dark:text-dark-100 dark:hover:text-white;
}

/* 发送反馈：8px 品牌青圆点带一圈淡光晕，沿运行时计算的向量缓动飞行，结尾缩小淡出。 */
.creative-submit-flight {
  @apply pointer-events-none absolute z-40 h-2 w-2 rounded-full bg-primary-500 ring-4 ring-primary-500/30;
  margin-left: -0.25rem;
  margin-top: -0.25rem;
  animation: creative-submit-flight 450ms cubic-bezier(0.2, 0.8, 0.2, 1) forwards;
}

@keyframes creative-submit-flight {
  0% {
    opacity: 0;
    transform: translate3d(0, 0, 0) scale(0.5);
  }

  15% {
    opacity: 1;
    transform: translate3d(calc(var(--flight-x) * 0.15), calc(var(--flight-y) * 0.15), 0) scale(1);
  }

  100% {
    opacity: 0;
    transform: translate3d(var(--flight-x), var(--flight-y), 0) scale(0.6);
  }
}

@media (prefers-reduced-motion: reduce) {
  .creative-submit-flight {
    animation-duration: 1ms;
  }

  /* 设置菜单使用全局 pop-float，减少动态效果由全局样式处理。 */
}
</style>
