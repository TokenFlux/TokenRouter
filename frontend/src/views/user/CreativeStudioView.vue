<template>
  <AppLayout full-viewport>
    <!-- 整个内容区即无限画布背景:fullViewport 模式下 app-main 无内边距,画布经 flex 链铺满全幅(含顶部,点阵直达 header 边界) -->
    <div ref="stageRef" class="relative h-full min-h-0">
      <CreativeCanvas ref="canvasRef" class="absolute inset-0" :operation="studio.operation.value" :allowed-mimes="studio.capabilities.value.allowed_mime_types" @error="onCanvasError" />
      <CreativeRunHistory ref="historyRef" :studio="studio" :active-run-count="activeRunCount" />

      <!-- 设置：左上角齿轮按钮，点击向下展开设置项 -->
      <div class="absolute left-3 top-3 z-20">
        <button
          type="button"
          class="flex rounded-control border border-primary-900/10 bg-white/90 text-gray-600 shadow-md backdrop-blur transition-colors hover:text-gray-900 dark:border-dark-600 dark:bg-dark-900/90 dark:text-gray-300 dark:hover:text-gray-100 btn-icon"
          :class="settingsOpen && 'text-primary-700 dark:text-primary-300'"
          :title="t('creative.canvas.settings')"
          :aria-expanded="settingsOpen"
          @click="settingsOpen = !settingsOpen"
        >
          <Icon name="cog" size="md" />
        </button>
        <!-- 向下展开的设置面板：清空画布 / 清空本机创作数据 -->
        <Transition name="pop-float">
          <div
            v-if="settingsOpen"
            class="settings-pop-float absolute left-0 top-12 w-64 rounded-surface border border-primary-900/10 bg-white/95 p-3 shadow-lg backdrop-blur dark:border-dark-600 dark:bg-dark-900/95"
          >
            <button
              type="button"
              class="flex h-9 w-full items-center justify-center gap-1.5 rounded-control border border-primary-900/10 text-xs text-gray-600 transition-colors hover:bg-gray-50 hover:text-gray-900 dark:border-dark-600 dark:text-gray-300 dark:hover:bg-dark-700 dark:hover:text-gray-100"
              @click="onResetCanvas"
            >
              <Icon name="trash" size="sm" />
              {{ t('creative.canvas.reset') }}
            </button>
            <button
              type="button"
              class="mt-2 flex h-9 w-full items-center justify-center gap-1.5 rounded-control border border-red-200 text-xs text-red-600 transition-colors hover:bg-red-50 dark:border-red-500/30 dark:text-red-400 dark:hover:bg-red-500/10"
              @click="onClearRequested"
            >
              <Icon name="trash" size="sm" />
              {{ t('creative.history.clearData') }}
            </button>
          </div>
        </Transition>
      </div>

      <!-- 聊天式输入框：固定底部居中，不随选中图片移动 -->
      <div class="absolute bottom-4 left-1/2 z-30 -translate-x-1/2">
        <CreativeComposer
          ref="composerRef"
          :studio="studio"
          @generate="onGenerate"
        />
      </div>

      <!-- 提交反馈：从真实发送按钮飞向历史入口，坐标由两个按钮的运行时位置决定。 -->
      <div
        v-if="submissionAnimationVisible"
        class="creative-submit-flight"
        :style="submissionAnimationStyle"
        @animationend="onSubmitFlightEnd"
        aria-hidden="true"
      >
        <Icon name="mail" size="sm" />
      </div>

      <!-- 生成状态胶囊：仅桌面端左下角显示，移动端隐藏以避免占用画布空间 -->
      <div
        v-if="pillState && !pillHidden"
        class="absolute left-1/2 top-28 z-10 hidden max-w-[calc(100%-6rem)] -translate-x-1/2 items-center gap-2 rounded-full border border-primary-900/10 bg-white/90 px-3 py-1.5 text-xs shadow-md backdrop-blur dark:border-dark-600 dark:bg-dark-900/90 lg:bottom-3 lg:left-3 lg:top-auto lg:flex lg:max-w-[calc(100%-24rem)] lg:translate-x-0"
        :class="pillState.toneClass"
      >
        <Icon v-if="pillState.spinning" name="refresh" size="sm" class="animate-spin" />
        <span class="whitespace-nowrap font-medium">{{ pillState.text }}</span>
        <span v-if="pillState.detail" class="truncate text-gray-500 dark:text-dark-400">{{ pillState.detail }}</span>
      </div>
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
 * - 输入框固定底部居中（早期试过跟随选中图片，缩放场景下位置不稳定，按用户要求回退为固定）
 * - 生成时从画布收集输入：edit/inpaint 取当前选中图片的原始 blob，inpaint 另取画笔 mask 导出
 * - 注册画布桥接：收割成功的输出自动上板；历史里的输出可一键导入画布
 * - 顶栏返回控制台，左上角设置（清空画布 / 清空本机创作数据）、右上角历史、顶部工具栏（上传 / 下载 / 局部重绘画笔组 / 删除）
 * 图片本体只存当前浏览器（IndexedDB），生成时才把所选素材发给模型供应商。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
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
const showClearConfirm = ref(false)
// 设置弹层开关（齿轮在画布左上角，弹层向下展开）
const settingsOpen = ref(false)
// 终态（非成功）状态胶囊几秒后自动消隐
let pillHideTimer: ReturnType<typeof setTimeout> | null = null
const pillHidden = ref(false)
const submissionAnimationVisible = ref(false)
const submissionAnimationStyle = ref<Record<string, string>>({})
let submissionAnimationFrame: number | null = null

const activeRunCount = computed(
  () => studio.runHistory.value.filter((run) => !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)).length,
)

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
  if (pillHideTimer) clearTimeout(pillHideTimer)
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
  // 不再用定时器猜时长；reduced-motion 下动画缩到 1ms，事件同样触发。
}

function onSubmitFlightEnd(event: AnimationEvent): void {
  // 只响应飞行动画本身，防御未来在元素上叠加其他动画时误收。
  if (event.animationName !== 'creative-submit-flight') return
  submissionAnimationVisible.value = false
}

function cancelSubmissionAnimation(): void {
  submissionAnimationVisible.value = false
  if (submissionAnimationFrame !== null && typeof cancelAnimationFrame === 'function') {
    cancelAnimationFrame(submissionAnimationFrame)
    submissionAnimationFrame = null
  }
}

// ==================== 生成状态胶囊 ====================

interface StatusPill {
  text: string
  detail?: string
  spinning: boolean
  toneClass: string
}

const PILL_TONES: Record<string, string> = {
  queued: 'text-blue-700 dark:text-blue-300',
  running: 'text-amber-700 dark:text-amber-300',
  provider_succeeded: 'text-amber-700 dark:text-amber-300',
  settlement_pending: 'text-amber-700 dark:text-amber-300',
  release_pending: 'text-orange-700 dark:text-orange-300',
  succeeded: 'text-green-700 dark:text-green-300',
  failed: 'text-red-700 dark:text-red-300',
  cancelled: 'text-gray-600 dark:text-dark-300',
  result_lost: 'text-red-700 dark:text-red-300',
  submitting: 'text-blue-700 dark:text-blue-300',
}

const pillState = computed<StatusPill | null>(() => {
  // 并发生成时当前任务可能已完成，优先显示历史中仍在执行的任务状态。
  const activeRun = studio.runHistory.value.find((run) => !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status))
  if (studio.polling.value || studio.busy.value) {
    const status = activeRun?.status ?? studio.currentRun.value?.status
    const phase = status && PILL_TONES[status] ? status : 'submitting'
    return {
      text: t(`creative.status.${phase}`),
      spinning: true,
      toneClass: PILL_TONES[phase],
    }
  }
  const run = studio.currentRun.value
  if (!run || !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)) return null
  return {
    text: t(`creative.status.${run.status}`, run.status),
    // 失败 / 结果丢失附带服务端原因
    detail: run.error_message,
    spinning: false,
    toneClass: PILL_TONES[run.status] ?? '',
  }
})

// 状态变化时重新显示；失败 / 取消 / 结果丢失几秒后自动消隐，成功保持到下次生成
watch(
  () => studio.currentRun.value?.status,
  (status, previous) => {
    if (!status || status === previous) return
    pillHidden.value = false
    if (pillHideTimer) clearTimeout(pillHideTimer)
    pillHideTimer = null
    if (status === 'failed' || status === 'cancelled' || status === 'result_lost') {
      pillHideTimer = setTimeout(() => {
        pillHidden.value = true
      }, 5000)
    }
  },
)

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
/* 设置面板动效用全局 pop-float,锚点方向(左上锚、向上收起)用局部变量表达。 */
.settings-pop-float {
  --pop-origin: top left;
  --pop-shift: -6px;
}

/* 信封沿运行时计算的向量匀速飞行，透明度收尾避免落到历史按钮上时产生遮挡。 */
.creative-submit-flight {
  @apply pointer-events-none absolute z-40 flex h-7 w-7 items-center justify-center rounded-control border border-primary-500/40 bg-white/95 text-primary-600 shadow-lg dark:border-primary-400/40 dark:bg-dark-800/95 dark:text-primary-300;
  margin-left: -0.875rem;
  margin-top: -0.875rem;
  animation: creative-submit-flight 1000ms linear forwards;
}

@keyframes creative-submit-flight {
  0% {
    opacity: 0;
    transform: translate3d(0, 0, 0) scale(0.65) rotate(-12deg);
  }

  14% {
    opacity: 1;
    transform: translate3d(calc(var(--flight-x) * 0.14), calc(var(--flight-y) * 0.14 - 8px), 0) scale(1) rotate(-4deg);
  }

  92% {
    opacity: 1;
    transform: translate3d(calc(var(--flight-x) * 0.92), calc(var(--flight-y) * 0.92), 0) scale(0.78) rotate(10deg);
  }

  100% {
    opacity: 0;
    transform: translate3d(var(--flight-x), var(--flight-y), 0) scale(0.72) rotate(12deg);
  }
}

@media (prefers-reduced-motion: reduce) {
  .creative-submit-flight {
    animation-duration: 1ms;
  }

  /* 设置面板走全局 pop-float,reduced-motion 由全局配方收敛。 */
}
</style>
