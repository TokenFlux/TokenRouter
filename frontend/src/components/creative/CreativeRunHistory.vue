<template>
  <!-- 历史入口：画布区域右上角的手写 SVG 图标按钮 -->
  <button
    ref="historyButtonRef"
    type="button"
    class="absolute right-3 top-3 z-20 flex h-9 w-9 items-center justify-center rounded-xl border border-primary-900/10 bg-white/90 text-gray-600 shadow-md backdrop-blur transition-colors hover:text-gray-900 dark:border-dark-600 dark:bg-dark-900/90 dark:text-gray-300 dark:hover:text-gray-100"
    :class="open && 'text-primary-700 dark:text-primary-300'"
    :title="t('creative.history.toggle')"
    :aria-expanded="open"
    @click="open = !open"
  >
    <HistoryIcon />
    <!-- 活动任务数量：保持在图标右上角，不展开历史也能感知后台进度。 -->
    <span
      v-if="props.activeRunCount > 0"
      class="absolute -right-1.5 -top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-amber-500 px-1 text-xs font-semibold leading-none text-white shadow-sm ring-2 ring-white dark:ring-dark-900"
    >
      {{ props.activeRunCount > 99 ? '99+' : props.activeRunCount }}
    </span>
  </button>

  <!-- 悬浮历史列表：点击展开 / 收起，选择行后不自动收起 -->
  <Transition name="history-panel">
    <div
      v-if="open"
      class="absolute right-3 top-14 z-20 flex max-h-[70%] w-80 flex-col overflow-hidden rounded-xl border border-primary-900/10 bg-white/95 shadow-lg backdrop-blur dark:border-dark-600 dark:bg-dark-900/95"
    >
    <div class="flex items-center gap-2 border-b border-primary-900/10 px-3 py-2 dark:border-dark-600">
      <h3 class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-dark-400">
        {{ t('creative.history.title') }}
      </h3>
      <button
        type="button"
        class="ml-auto text-gray-400 transition-colors hover:text-gray-600 disabled:cursor-not-allowed disabled:opacity-60 dark:hover:text-gray-200"
        :disabled="refreshing || studio.loadingHistory.value"
        :aria-busy="refreshing || studio.loadingHistory.value"
        :title="t('common.refresh')"
        @click="refresh"
      >
        <Icon
          name="refresh"
          size="sm"
          :class="(refreshing || studio.loadingHistory.value) && 'animate-spin'"
        />
      </button>
      <button
        type="button"
        class="text-gray-400 transition-colors hover:text-gray-600 dark:hover:text-gray-200"
        :title="t('common.close')"
        @click="open = false"
      >
        <Icon name="x" size="sm" />
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-y-auto p-2">
      <div v-if="studio.runHistory.value.length" class="space-y-1.5">
        <div
          v-for="run in studio.runHistory.value"
          :key="run.id"
          class="rounded-lg border border-primary-900/10 transition-colors dark:border-dark-600"
          :class="studio.currentRun.value?.id === run.id && 'border-primary-500 dark:border-primary-500'"
        >
          <!-- 行头：点击原地展开 / 收起 -->
          <button type="button" class="w-full px-3 py-2 text-left" @click="toggleRun(run.id)">
            <div class="flex items-center gap-2">
              <span class="status-badge flex-shrink-0" :class="`status-${run.status}`">
                {{ t(`creative.status.${run.status}`, run.status) }}
              </span>
              <span class="min-w-0 flex-1 truncate text-xs text-gray-600 dark:text-gray-300">{{ run.model }}</span>
              <Icon
                name="chevronDown"
                size="sm"
                class="flex-shrink-0 text-gray-400 transition-transform dark:text-dark-400"
                :class="expandedRunId === run.id && 'rotate-180'"
              />
            </div>
            <div class="mt-1 flex items-center gap-2 text-xs text-gray-400 dark:text-dark-400">
              <span>{{ formatRunTime(run.created_at) }}</span>
              <span
                v-if="formatElapsed(run)"
                class="inline-flex shrink-0 items-center gap-1 tabular-nums"
                :aria-label="t('creative.history.elapsed', { time: formatElapsed(run) })"
                :title="t('creative.history.elapsed', { time: formatElapsed(run) })"
              >
                <Icon name="clock" size="xs" aria-hidden="true" />
                <span>{{ formatElapsed(run) }}</span>
              </span>
              <span v-if="run.actual_cost != null" class="ml-auto">{{ t('creative.result.actualCost', { cost: formatBalanceAmount(run.actual_cost, { fractionDigits: 3 }) }) }}</span>
            </div>
          </button>

          <!-- 进行中的任务只显示加载状态，终态任务才显示素材与操作按钮。 -->
          <Transition name="history-details">
            <div v-if="expandedRunId === run.id" class="history-details-grid">
              <div class="min-h-0 overflow-hidden">
                <div class="space-y-2 border-t border-primary-900/10 px-3 pb-3 pt-2 dark:border-dark-600">
                  <div v-if="isActive(run)" class="flex items-center gap-3 py-2 text-xs text-gray-500 dark:text-dark-300">
                    <div class="flex h-16 w-16 flex-shrink-0 items-center justify-center rounded-md border border-primary-900/10 bg-gray-50 dark:border-dark-600 dark:bg-dark-950">
                      <Icon name="refresh" size="md" class="animate-spin text-primary-500" />
                    </div>
                    <div class="min-w-0">
                      <span class="block">{{ t(`creative.status.${run.status}`, run.status) }}</span>
                      <span
                        v-if="formatElapsed(run)"
                        data-testid="creative-run-elapsed"
                        class="mt-1 inline-flex items-center gap-1 tabular-nums text-xs text-gray-400 dark:text-dark-400"
                        :aria-label="t('creative.history.elapsed', { time: formatElapsed(run) })"
                        :title="t('creative.history.elapsed', { time: formatElapsed(run) })"
                      >
                        <Icon name="clock" size="xs" aria-hidden="true" />
                        <span>{{ formatElapsed(run) }}</span>
                      </span>
                    </div>
                  </div>
                  <template v-else-if="run.outputs?.length">
                    <!-- 输出纵向排列：图片优先撑满弹窗宽度，操作按钮统一放在图片下方 -->
                    <div v-for="output in run.outputs" :key="output.output_index" class="flex flex-col gap-1.5">
                      <div
                        class="flex w-full items-center justify-center overflow-hidden rounded-md border border-primary-900/10 bg-gray-50 dark:border-dark-600 dark:bg-dark-950"
                      >
                        <img
                          v-if="assetFor(run.id, output.output_index)"
                          :src="urlForAsset(outputAssetKey(run.id, output.output_index), assetFor(run.id, output.output_index)!.blob)"
                          :alt="`output-${output.output_index}`"
                          draggable="true"
                          class="block h-auto w-full cursor-grab select-none active:cursor-grabbing"
                          @dragstart.stop="onOutputDragStart($event, run.id, output.output_index)"
                        />
                        <div v-else class="flex h-24 w-full flex-col items-center justify-center gap-0.5 text-gray-300 dark:text-dark-600">
                          <Icon name="modalityImage" size="sm" />
                          <span class="scale-90 text-xs">{{ t('creative.result.missing') }}</span>
                        </div>
                      </div>
                      <div class="flex gap-1.5">
                        <button
                          type="button"
                          class="flex flex-1 items-center justify-center gap-1 rounded-md border border-primary-900/10 px-2 py-1 text-xs text-gray-600 transition-colors hover:border-primary-500 hover:text-primary-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-dark-600 dark:text-gray-300 dark:hover:border-primary-500 dark:hover:text-primary-300"
                          :disabled="!assetFor(run.id, output.output_index)"
                          @click="importToCanvas(run.id, output.output_index)"
                        >
                          <Icon name="plus" size="sm" />
                          {{ t('creative.history.importToCanvas') }}
                        </button>
                        <button
                          type="button"
                          class="flex flex-1 items-center justify-center gap-1 rounded-md border border-primary-900/10 px-2 py-1 text-xs text-gray-600 transition-colors hover:border-primary-500 hover:text-primary-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-dark-600 dark:text-gray-300 dark:hover:border-primary-500 dark:hover:text-primary-300"
                          :disabled="!assetFor(run.id, output.output_index)"
                          @click="downloadOutput(run.id, output.output_index, output.mime_type)"
                        >
                          <Icon name="download" size="sm" />
                          {{ t('creative.history.download') }}
                        </button>
                      </div>
                    </div>
                  </template>
                  <p v-else class="py-1 text-xs text-gray-400 dark:text-dark-400">{{ t('creative.history.noOutputs') }}</p>
                </div>
              </div>
            </div>
          </Transition>
        </div>
      </div>
      <p v-else-if="!studio.loadingHistory.value" class="py-6 text-center text-xs text-gray-400 dark:text-dark-400">
        {{ t('creative.history.empty') }}
      </p>
    </div>
    </div>
  </Transition>
</template>

<script setup lang="ts">
/**
 * 创作 run 历史（悬浮层）：
 * - 画布右上角图标按钮展开 / 收起；列表每行 = 状态徽章 + 模型名 + 时间（+ 实际费用）
 * - 点击行原地向下展开：终态任务显示本地保存的输出图片，图片按原始比例撑满弹窗宽度，
 *   图片支持拖到画布，且「导入到画布」和「下载」按钮统一放在图片下方并排展示；本地素材缺失时按钮禁用并展示缺失占位
 * - 进行中的任务只展示加载状态，不提供素材操作或取消入口
 */
import { h, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { saveAs } from 'file-saver'
import Icon from '@/components/icons/Icon.vue'
import { CREATIVE_RUN_TERMINAL_STATUSES, type CreativeRun } from '@/api/creative'
import { formatDateTime } from '@/utils/format'
import { outputAssetKey, type LocalAsset } from '@/utils/creativeLocalStore'
import { CREATIVE_OUTPUT_DRAG_MIME, serializeCreativeOutputDrag } from '@/utils/creativeDrag'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { useCreativeStudio } from '@/composables/useCreativeStudio'

type Studio = ReturnType<typeof useCreativeStudio>

interface Props {
  studio: Studio
  activeRunCount?: number
}

const props = withDefaults(defineProps<Props>(), {
  activeRunCount: 0,
})
// 本地别名：studio 为 props 传入的共享状态机，子组件经它读写
const studio = props.studio
const { t } = useI18n()
const { formatBalanceAmount } = useBalanceDisplay()

// 历史面板展开状态：默认折叠
const open = ref(false)
// 原地展开的历史任务 id（同时只展开一条）
const expandedRunId = ref<string | null>(null)
// 手动刷新状态独立维护，确保快速响应也能先渲染出旋转反馈
const refreshing = ref(false)
const historyButtonRef = ref<HTMLButtonElement | null>(null)
defineExpose({ historyButtonRef })
// 只有存在活动任务时才运行时钟，终态任务直接使用服务端完成时间。
const elapsedNow = ref(Date.now())
let elapsedTimer: ReturnType<typeof setInterval> | null = null

// 展开区的 objectURL 缓存：切换收起或卸载时统一回收
const expandedUrls = new Map<string, string>()

function toggleRun(runId: string): void {
  expandedRunId.value = expandedRunId.value === runId ? null : runId
}

function assetFor(runId: string, outputIndex: number): LocalAsset | null {
  return studio.outputAssetMap.value.get(outputAssetKey(runId, outputIndex)) ?? null
}

function urlForAsset(key: string, blob: Blob): string {
  const cached = expandedUrls.get(key)
  if (cached) return cached
  const url = URL.createObjectURL(blob)
  expandedUrls.set(key, url)
  return url
}

function revokeExpandedUrls(): void {
  expandedUrls.forEach((url) => URL.revokeObjectURL(url))
  expandedUrls.clear()
}

// 切换展开行 / 收起面板时回收上一批 objectURL；组件卸载兜底回收
watch(expandedRunId, revokeExpandedUrls)
watch(open, (value) => {
  if (!value) {
    expandedRunId.value = null
    revokeExpandedUrls()
  }
})
watch(
  () => studio.runHistory.value,
  (runs) => {
    const hasActiveRun = runs.some((run) => isActive(run))
    if (hasActiveRun && !elapsedTimer) {
      elapsedNow.value = Date.now()
      elapsedTimer = setInterval(() => {
        elapsedNow.value = Date.now()
      }, 1000)
    } else if (!hasActiveRun && elapsedTimer) {
      clearInterval(elapsedTimer)
      elapsedTimer = null
    }
  },
  { deep: true, immediate: true },
)
onBeforeUnmount(() => {
  if (elapsedTimer) {
    clearInterval(elapsedTimer)
    elapsedTimer = null
  }
  revokeExpandedUrls()
})

// 导入画布：把本地保存的输出素材放上画布（走画布桥接）
function importToCanvas(runId: string, outputIndex: number): void {
  studio.importOutputToCanvas(runId, outputIndex)
}

// 历史缩略图拖放只传运行记录索引，画布接收后从 IndexedDB 取回图片本体。
function onOutputDragStart(event: DragEvent, runId: string, outputIndex: number): void {
  const asset = assetFor(runId, outputIndex)
  if (!asset || !event.dataTransfer) return
  event.dataTransfer.effectAllowed = 'copy'
  event.dataTransfer.setData(
    CREATIVE_OUTPUT_DRAG_MIME,
    serializeCreativeOutputDrag({ runId, outputIndex }),
  )
}

// 下载本地保存的输出素材
function downloadOutput(runId: string, outputIndex: number, mimeType?: string): void {
  const asset = assetFor(runId, outputIndex)
  if (!asset) return
  const extension = (mimeType || asset.blob.type || 'image/png').split('/')[1] || 'png'
  saveAs(asset.blob, `creative-${runId.slice(0, 12)}-${outputIndex}.${extension}`)
}

// 历史（回旋时钟）图标：手写 SVG，仿 🕘 样式，风格对齐 AppSidebar 内手写图标
const HistoryIcon = {
  render: () =>
    h(
      'svg',
      { fill: 'none', viewBox: '0 0 24 24', stroke: 'currentColor', 'stroke-width': '1.5', class: 'h-5 w-5' },
      [
        h('path', {
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          d: 'M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8',
        }),
        h('path', {
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          d: 'M3 3v5h5',
        }),
        h('path', {
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          d: 'M12 7v5l4 2',
        }),
      ],
    ),
}

function isActive(run: CreativeRun): boolean {
  return !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)
}

// 后端时间戳兼容秒 / 毫秒。
function timestampToMilliseconds(timestamp: number | null | undefined): number | null {
  if (timestamp == null || !Number.isFinite(timestamp)) return null
  return timestamp < 1e12 ? timestamp * 1000 : timestamp
}

function formatRunTime(timestamp: number | undefined): string {
  const ms = timestampToMilliseconds(timestamp)
  if (ms == null) return ''
  return formatDateTime(new Date(ms))
}

// 活动任务实时计时，终态任务使用服务端完成时间固定显示最终耗时。
function formatElapsed(run: CreativeRun): string {
  const startedAt = timestampToMilliseconds(run.started_at ?? run.created_at)
  if (startedAt == null) return ''
  const endedAt = isActive(run)
    ? elapsedNow.value
    : timestampToMilliseconds(run.completed_at ?? run.cancelled_at)
  if (endedAt == null) return ''
  const totalSeconds = Math.max(0, Math.floor((endedAt - startedAt) / 1000))
  const seconds = totalSeconds % 60
  const minutes = Math.floor(totalSeconds / 60) % 60
  const hours = Math.floor(totalSeconds / 3600)
  const pad = (value: number): string => String(value).padStart(2, '0')
  return hours > 0 ? `${pad(hours)}:${pad(minutes)}:${pad(seconds)}` : `${pad(minutes)}:${pad(seconds)}`
}

async function refresh(): Promise<void> {
  if (refreshing.value || studio.loadingHistory.value) return
  refreshing.value = true
  // 先提交刷新状态，让图标在请求开始前完成一次绘制。
  await nextTick()
  try {
    await studio.refreshHistory()
  } finally {
    refreshing.value = false
  }
}
</script>

<style scoped>
/* 历史面板从右上入口展开；条目详情使用网格轨道实现真实高度折叠。 */
.history-panel-enter-active,
.history-panel-leave-active {
  transform-origin: top right;
  transition:
    opacity 200ms ease,
    transform 200ms cubic-bezier(0.22, 1, 0.36, 1);
  will-change: opacity, transform;
}

.history-panel-enter-from,
.history-panel-leave-to {
  opacity: 0;
  transform: translateY(-6px) scale(0.97);
}

.history-details-grid {
  display: grid;
  grid-template-rows: 1fr;
}

.history-details-enter-active,
.history-details-leave-active {
  overflow: hidden;
  transition:
    grid-template-rows 220ms cubic-bezier(0.22, 1, 0.36, 1),
    opacity 220ms cubic-bezier(0.22, 1, 0.36, 1);
}

.history-details-enter-from,
.history-details-leave-to {
  grid-template-rows: 0fr;
  opacity: 0;
}

.status-badge {
  @apply inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium;
  @apply bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300;
}

.status-queued {
  @apply bg-blue-50 text-blue-600 dark:bg-blue-500/10 dark:text-blue-400;
}

.status-running {
  @apply bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-400;
}

.status-provider_succeeded,
.status-settlement_pending {
  @apply bg-amber-50 text-amber-600 dark:bg-amber-500/10 dark:text-amber-400;
}

.status-release_pending {
  @apply bg-orange-50 text-orange-600 dark:bg-orange-500/10 dark:text-orange-400;
}

.status-succeeded {
  @apply bg-green-50 text-green-600 dark:bg-green-500/10 dark:text-green-400;
}

.status-failed,
.status-result_lost {
  @apply bg-red-50 text-red-600 dark:bg-red-500/10 dark:text-red-400;
}

.status-cancelled {
  @apply bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300;
}

@media (prefers-reduced-motion: reduce) {
  .history-panel-enter-active,
  .history-panel-leave-active,
  .history-details-enter-active,
  .history-details-leave-active {
    transition-duration: 1ms;
  }
}
</style>
