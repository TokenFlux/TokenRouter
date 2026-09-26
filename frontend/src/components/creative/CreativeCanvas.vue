<template>
  <!-- 画布即整个背景：透明无边框，圆点网格铺满 -->
  <div
    ref="containerRef"
    class="dot-grid relative h-full w-full overflow-hidden"
    :class="dropTargetActive && 'drop-target-active'"
  >
    <canvas ref="canvasElRef"></canvas>
    <!-- 独立 mask 画布：先以不透明颜色合成，再整体设置透明度，避免笔迹重叠变深 -->
    <canvas ref="maskCanvasElRef" class="mask-overlay"></canvas>
    <!-- 拖放目标反馈不接收指针事件，避免覆盖 Fabric 画布交互。 -->
    <div v-if="dropTargetActive" class="pointer-events-none absolute inset-2 z-[2] rounded-surface border-2 border-dashed border-primary-500/70 bg-primary-500/5"><!-- check-ui-allow: 画布内局部堆叠 --></div>

    <!-- 浮动工具栏（顶部居中，含移动端；窄屏限宽并换行，圆角保持与桌面端一致，避免与左上角设置、右上角历史按钮重叠）：上传 | 局部重绘画笔组 | 删除选中 / 清空 -->
    <div
      class="absolute left-1/2 top-3 z-10 flex max-sm:w-fit max-sm:max-w-[calc(100%-7.5rem)] max-sm:flex-wrap max-sm:justify-center -translate-x-1/2 items-center gap-1.5 rounded-full border border-primary-900/10 bg-white/90 px-2 py-1.5 shadow-md backdrop-blur dark:border-dark-600 dark:bg-dark-900/90"
    >
      <!-- 上传图片：裁剪确认后直接放上画布当前视角中心 -->
      <button type="button" class="canvas-tool-btn" :title="t('creative.panel.uploadSource')" @click="fileInputRef?.click()">
        <Icon name="upload" size="sm" />
      </button>
      <input
        ref="fileInputRef"
        type="file"
        :accept="props.allowedMimes.join(',')"
        multiple
        class="hidden"
        @change="onFilesPicked"
      />
      <!-- 下载选中图片到本地 -->
      <button
        type="button"
        class="canvas-tool-btn"
        :disabled="!selectedImage"
        :title="t('creative.canvas.downloadSelected')"
        @click="downloadSelected"
      >
        <Icon name="download" size="sm" />
      </button>

      <!-- 框选参考图 / 画布对象工具：三种模式都可用，开启后空白拖拽画选框 -->
      <span class="mx-0.5 h-5 w-px bg-primary-900/10 dark:bg-dark-600"></span>
      <button
        type="button"
        class="canvas-tool-btn"
        :class="boxSelectMode && 'canvas-tool-btn-active'"
        :title="t('creative.canvas.boxSelect')"
        @click="setBoxSelectMode(!boxSelectMode)"
      >
        <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-dasharray="3 2">
          <rect x="4" y="4" width="16" height="16" rx="2" />
        </svg>
      </button>

      <!-- 画笔组：仅局部重绘模式可用（选中图片后自动进入涂抹，可用开关暂停去移动视角） -->
      <Transition name="canvas-toolbar-extension">
        <div v-if="isInpaint" class="canvas-toolbar-extension">
          <span class="mx-0.5 h-5 w-px flex-none bg-primary-900/10 dark:bg-dark-600"></span>
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :class="painting && 'canvas-tool-btn-active'"
            :title="painting ? t('creative.canvas.paintToggleOff') : t('creative.canvas.paintToggleOn')"
            @click="togglePainting"
          >
            <Icon name="edit" size="sm" />
          </button>
          <!-- 清除当前所有涂抹笔迹 -->
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :disabled="!hasMaskStrokes"
            :title="t('creative.canvas.clearMask')"
            @click="clearMask"
          >
            <Icon name="trash" size="sm" />
          </button>
          <!-- 撤销上一笔涂抹（同 Ctrl/Cmd+Z） -->
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :disabled="!canUndoMask"
            :title="t('creative.canvas.undoMask')"
            @click="undoMask"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
              <path d="M9 15L3 9m0 0l6-6M3 9h12a6 6 0 010 12h-3" />
            </svg>
          </button>
          <!-- 画笔粗细滑块：8–96（固定高度与工具栏按钮同高；轨道/滑块配色见 .brush-size） -->
          <div class="flex flex-none items-center gap-1.5 px-1">
            <input
              v-model.number="brushSize"
              type="range"
              min="8"
              max="96"
              step="1"
              class="brush-size h-8 w-16 cursor-pointer sm:w-24"
              :title="t('creative.canvas.brushSize')"
            />
            <span class="w-6 text-center text-xs tabular-nums text-gray-500 dark:text-dark-400">{{ brushSize }}</span>
          </div>
          <!-- 笔迹形状：圆头 / 方头 -->
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :class="brushShape === 'round' && 'canvas-tool-btn-active'"
            :title="t('creative.canvas.shapeRound')"
            @click="setBrushShape('round')"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="12" cy="12" r="5" />
            </svg>
          </button>
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :class="brushShape === 'square' && 'canvas-tool-btn-active'"
            :title="t('creative.canvas.shapeSquare')"
            @click="setBrushShape('square')"
          >
            <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
              <rect x="7" y="7" width="10" height="10" />
            </svg>
          </button>
        </div>
      </Transition>

      <span class="mx-0.5 h-5 w-px bg-primary-900/10 dark:bg-dark-600"></span>
      <!-- 删除选中图片 -->
      <button
        type="button"
        class="canvas-tool-btn"
        :disabled="selectedObjectCount === 0"
        :title="t('creative.canvas.removeSelected')"
        @click="removeSelected"
      >
        <Icon name="x" size="sm" />
      </button>
    </div>

    <!-- 局部重绘未选中图片：引导点击选择目标图片（位于顶部工具栏下方；移动端工具栏可能换行，留白更大） -->
    <div
      v-if="isInpaint && !inpaintAnchor"
      class="pointer-events-none absolute left-1/2 top-28 z-10 -translate-x-1/2 whitespace-nowrap rounded-full bg-black/60 px-3 py-1 text-xs text-white dark:bg-white/15 lg:top-16"
    >
      {{ t('creative.canvas.inpaintPickHint') }}
    </div>
    <!-- 图生图未选择参考图：同款胶囊引导（点击单选，Shift+点击加选） -->
    <div
      v-else-if="isEdit && !editRefs.length"
      class="pointer-events-none absolute left-1/2 top-28 z-10 -translate-x-1/2 whitespace-nowrap rounded-full bg-black/60 px-3 py-1 text-xs text-white dark:bg-white/15 lg:top-16"
    >
      {{ t('creative.canvas.editPickHint') }}
    </div>
    <!-- 涂抹引导：首次落笔前提示紫色笔迹即重绘区域 -->
    <div
      v-else-if="painting && !hasMaskStrokes"
      class="pointer-events-none absolute left-1/2 top-28 z-10 -translate-x-1/2 whitespace-nowrap rounded-full bg-black/60 px-3 py-1 text-xs text-white dark:bg-white/15 lg:top-16"
    >
      {{ t('creative.canvas.maskPaintHint') }}
    </div>

    <!-- 裁剪弹窗队列：每张图片依次进入，确认/跳过后直接放上画布 -->
    <CropperModal :show="cropQueue.length > 0" :blob="cropQueue[0] ?? null" @confirm="onCropConfirm" @skip="onCropConfirm" @cancel="onCropCancel" />
  </div>
</template>

<script setup lang="ts">
/**
 * 创作台无限画布（fabric 7）
 * - 逻辑尺寸跟随容器；空白处拖拽或普通 wheel 平移视角，触控板捏合（ctrlKey wheel）与移动端双指手势缩放（0.2–3）
 * - 图片对象可点选 / 拖动 / Delete 删除；生成输出自动按"上一个放置位置右侧 40px、约 2200px 换行"上板并平滑平移视角
 * - 局部重绘：选中图片自动进入涂抹模式（紫色笔迹 = 重绘区域，导出时自动转白底 mask）；
 *   涂抹中可用中键 / 右键拖拽平移，工具栏开关可暂停涂抹去移动 / 换选图片
 * - 工具栏：上传、下载选中、框选、画笔组（仅局部重绘）、删除选中；清空画布收在左上角设置里
 * - 拖放：外部 PNG/JPEG/WebP 直接保存并按落点上板，历史 output 通过本地 key 拖放并按落点上板
 * - 场景快照（含 data 自定义属性，图片 src 以 asset:// 占位）防抖存入 IndexedDB，刷新后恢复并重建输出注册表
 */
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { saveAs } from 'file-saver'
import { Canvas, FabricImage, PencilBrush, Point, Rect, StaticCanvas, Text as FabricText, type FabricObject, type TMat2D } from 'fabric'
import { SquarePencilBrush } from './SquarePencilBrush'
import Icon from '@/components/icons/Icon.vue'
import CropperModal from './CropperModal.vue'
import type { CreativeOperation } from '@/api/creative'
import {
  LocalStoreQuotaError,
  loadAsset,
  loadSceneJson,
  localAssetKey,
  outputAssetKey,
  saveAsset,
  saveSceneJson,
} from '@/utils/creativeLocalStore'
import {
  CREATIVE_OUTPUT_DRAG_MIME,
  isSupportedCreativeImageFile,
  parseCreativeOutputDrag,
} from '@/utils/creativeDrag'

interface Props {
  // 当前操作：局部重绘时启用画笔组并在选中图片后自动进入涂抹模式
  operation: CreativeOperation
  // 从服务端能力接口注入的文件 MIME 白名单
  allowedMimes?: string[]
}

interface Emits {
  (e: 'error', message: string): void
}

const props = withDefaults(defineProps<Props>(), {
  allowedMimes: () => ['image/png', 'image/jpeg', 'image/webp'],
})
const emit = defineEmits<Emits>()
const { t } = useI18n()

// 场景快照在本地库中的 key（沿用既有约定）
const SCENE_KEY = 'creative:canvas'
// 场景自动保存防抖
const SCENE_SAVE_DEBOUNCE = 1000
// 缩放范围
const MIN_ZOOM = 0.2
const MAX_ZOOM = 3
// 触控板捏合缩放灵敏度：相对最初的 0.999 系数约提升五倍
const ZOOM_WHEEL_FACTOR = 0.995
// 非像素 wheel 的常用换算；触控板通常使用像素模式，鼠标滚轮常使用行模式
const WHEEL_LINE_HEIGHT = 16
const WHEEL_DELTA_MODE_LINE = 1
const WHEEL_DELTA_MODE_PAGE = 2
// 输出自动上板的排布参数
const PLACE_GAP = 40
const PLACE_WRAP_X = 2200
// 图片上板缩放（上传与生成输出统一）：原始图过大，按 1/4 线性尺寸（1/16 面积）放置，blob 本身不变
const PLACE_SCALE = 0.25
// 图片 src 在场景快照中的占位协议，恢复时回 IndexedDB 取 blob
const ASSET_PROTOCOL = 'asset://'
// mask 导出色；画布内使用不透明紫色笔迹，再通过统一图层透明度显示
const MASK_COLOR = '#ffffff'
const MASK_TINT = '#a855f7'
const MASK_OPACITY = 0.55
const MASK_PREVIEW_TINT = `rgba(168, 85, 247, ${MASK_OPACITY})`
// 涂抹锚点描边 / 编辑参考图描边 / 分辨率标记（运行时辅助对象，不进快照、不进 mask）
const ANCHOR_OUTLINE_KIND = 'anchor-outline'
const REF_OUTLINE_KIND = 'ref-outline'
const RESOLUTION_TAG_KIND = 'resolution-tag'
const ANCHOR_OUTLINE_STYLE = {
  fill: 'transparent',
  stroke: 'rgba(0, 210, 255, 0.7)',
  strokeWidth: 1.5,
  strokeDashArray: [6, 4],
}
// 选中图片右下角的分辨率标记（场景辅助对象，不参与交互与持久化）
const RESOLUTION_TAG_PADDING_X = 5
const RESOLUTION_TAG_PADDING_Y = 3
const RESOLUTION_TAG_OFFSET = 8
const RESOLUTION_TAG_TEXT_STYLE = {
  fill: '#ffffff',
  fontSize: 11,
  fontFamily: 'sans-serif',
  fontWeight: '500',
  originX: 'left' as const,
  originY: 'top' as const,
  selectable: false,
  evented: false,
}
const RESOLUTION_TAG_BACKGROUND_STYLE = {
  fill: 'rgba(8, 12, 20, 0.78)',
  rx: 4,
  ry: 4,
  originX: 'left' as const,
  originY: 'top' as const,
  selectable: false,
  evented: false,
}
// 圆点网格的场景间距（px，缩放 1 时）
const GRID_SPACING = 20

// 圆点网格锚定场景坐标：背景位置 = 视口平移分量，间距 = 场景间距 × 缩放系数；
// 因此平移时网格与图片同步滑动，缩小变密、放大变稀疏，与画布缩放观感一致。
function syncDotGrid(): void {
  const el = containerRef.value
  if (!el || !canvas) return
  const vpt = canvas.viewportTransform
  const spacing = GRID_SPACING * canvas.getZoom()
  el.style.backgroundPosition = `${vpt[4]}px ${vpt[5]}px`
  el.style.backgroundSize = `${spacing}px ${spacing}px`
  // 独立 mask 画布必须与主画布共享视口变换，保证平移和缩放时笔迹跟随图片
  if (maskCanvas) {
    maskCanvas.setViewportTransform([...vpt] as TMat2D)
    // mask 画布关闭了自动增删重绘，视口变化后需主动刷新，否则下一笔才会显示新位置
    maskCanvas.requestRenderAll()
  }
}

// fabric 7 类型未声明自定义 data 属性，运行时允许挂任意键，这里做最小封装
type ObjectWithData = { data?: Record<string, unknown> }

function objectData(object: FabricObject): Record<string, unknown> {
  return (object as unknown as ObjectWithData).data ?? {}
}

function setObjectData(object: FabricObject, data: Record<string, unknown>): void {
  const target = object as unknown as ObjectWithData
  target.data = data
}

// Fabric 7 序列化图片类型为大写 Image，兼容旧快照中的小写 image。
function isImageSnapshotObject(object: Record<string, unknown>): boolean {
  return typeof object.type === 'string' && object.type.toLowerCase() === 'image'
}

// 读取图片原始像素尺寸：优先使用快照中的数据，兼容旧场景则回退到图片元素本身
function imageResolution(image: FabricImage): ImageResolution | null {
  const stored = objectData(image).resolution
  if (stored && typeof stored === 'object') {
    const value = stored as { width?: unknown; height?: unknown }
    const width = typeof value.width === 'number' ? Math.round(value.width) : 0
    const height = typeof value.height === 'number' ? Math.round(value.height) : 0
    if (width > 0 && height > 0) return { width, height }
  }
  const element = image.getElement() as Partial<HTMLImageElement>
  const width = Math.round(Number(element.naturalWidth || element.width || 0))
  const height = Math.round(Number(element.naturalHeight || element.height || 0))
  return width > 0 && height > 0 ? { width, height } : null
}

function removeResolutionTag(): void {
  if (canvas && resolutionTag) {
    canvas.remove(resolutionTag.background, resolutionTag.text)
  }
  resolutionTag = null
}

// 按图片场景包围盒右下角定位标记；对象处于 ActiveSelection 时先刷新绝对坐标
function refreshResolutionTag(image: FabricImage | null): void {
  if (!canvas || !image) {
    removeResolutionTag()
    return
  }
  const resolution = imageResolution(image)
  if (!resolution) {
    removeResolutionTag()
    return
  }
  const label = `${resolution.width}x${resolution.height}`
  if (!resolutionTag || resolutionTag.image !== image || resolutionTag.text.text !== label) {
    removeResolutionTag()
    const text = new FabricText(label, RESOLUTION_TAG_TEXT_STYLE)
    const textWidth = text.width ?? 0
    const textHeight = text.height ?? 0
    const background = new Rect({
      ...RESOLUTION_TAG_BACKGROUND_STYLE,
      width: textWidth + RESOLUTION_TAG_PADDING_X * 2,
      height: textHeight + RESOLUTION_TAG_PADDING_Y * 2,
    })
    setObjectData(background, { kind: RESOLUTION_TAG_KIND })
    setObjectData(text, { kind: RESOLUTION_TAG_KIND })
    canvas.add(background, text)
    resolutionTag = { image, background, text }
  }
  image.setCoords()
  const bounds = image.getBoundingRect()
  const backgroundWidth = resolutionTag.background.width ?? 0
  const backgroundHeight = resolutionTag.background.height ?? 0
  const left = bounds.left + bounds.width - backgroundWidth - RESOLUTION_TAG_OFFSET
  const top = bounds.top + bounds.height - backgroundHeight - RESOLUTION_TAG_OFFSET
  resolutionTag.background.set({ left, top })
  resolutionTag.text.set({ left: left + RESOLUTION_TAG_PADDING_X, top: top + RESOLUTION_TAG_PADDING_Y })
  resolutionTag.background.setCoords()
  resolutionTag.text.setCoords()
  canvas.requestRenderAll()
}

// 返回当前会话中的全部 mask 路径；画笔路径位于独立画布，普通对象仍位于主画布
function getMaskPaths(): FabricObject[] {
  if (!canvas) return []
  const paths = canvas.getObjects().filter((object) => objectData(object).kind === 'mask')
  if (maskCanvas) {
    paths.push(...maskCanvas.getObjects().filter((object) => objectData(object).kind === 'mask'))
  }
  return paths
}

// 把 mask 路径加入独立画布，并重新套用当前目标图片的裁剪框
function addMaskPath(path: FabricObject): void {
  if (maskCanvas) {
    maskCanvas.add(path)
    if (maskClipRect) path.clipPath = maskClipRect
    maskCanvas.requestRenderAll()
  } else {
    canvas?.add(path)
  }
}

// 从 mask 所在画布移除路径，供撤销、清除和退出画笔模式复用
function removeMaskPath(path: FabricObject): void {
  if (maskCanvas?.getObjects().includes(path)) {
    maskCanvas.remove(path)
    maskCanvas.requestRenderAll()
  } else {
    canvas?.remove(path)
  }
}

type BrushShape = 'round' | 'square'
type ImageResolution = { width: number; height: number }
type ResolutionTag = { image: FabricImage; background: Rect; text: FabricText }
type TouchPoint = { clientX: number; clientY: number }
type PinchGesture = {
  startDistance: number
  startZoom: number
  startScenePoint: { x: number; y: number }
  startViewportTransform: TMat2D
}

const containerRef = ref<HTMLDivElement | null>(null)
const canvasElRef = ref<HTMLCanvasElement | null>(null)
const maskCanvasElRef = ref<HTMLCanvasElement | null>(null)
const fileInputRef = ref<HTMLInputElement | null>(null)
// 待裁剪队列：确认/跳过一张后自动出队下一张
const cropQueue = ref<Blob[]>([])
// 涂抹模式开关：局部重绘 + 锚定图片时自动开启（工具栏可暂停）
const painting = ref(false)
// 是否已有 mask 笔迹（控制"清除涂抹"/橡皮按钮可用态）
const hasMaskStrokes = ref(false)
// 画笔粗细（8–96）/ 形状
const brushSize = ref(28)
const brushShape = ref<BrushShape>('round')
// 当前选中的图片对象（fabric 活动对象；画笔落笔时 fabric 会丢弃选中，不能作为 mask 锚点依据）
const selectedImage = shallowRef<FabricObject | null>(null)
// 当前 Fabric 活动选区内的对象数量；框选多张图片时也要允许使用删除工具
const selectedObjectCount = ref(0)
// 当前是否有外部图片或历史输出悬停在画布上，用于显示拖放目标反馈
const dropTargetActive = ref(false)
// mask 锚定的图片对象：选中图片时设置，与 fabric 选中态解耦，避免画笔落笔自动丢弃选中后涂抹被中断
const inpaintAnchor = shallowRef<FabricObject | null>(null)
// 手动暂停涂抹（移动视角 / 换选图片后再恢复）
const paintSuspended = ref(false)
// 编辑模式（edit）的参考图集合：点击图片 = 单选替换，框选工具 = 批量替换为框内图片
const editRefs = shallowRef<FabricObject[]>([])
// 框选工具开关：三种模式均可开启，空白拖拽绘制选框
const boxSelectMode = ref(false)
// 锚点描边对象（运行时辅助，涂抹期间标示目标图片）
let anchorOutline: Rect | null = null
// 编辑参考图描边对象表：图片对象 → 描边矩形（运行时辅助）
const refOutlines = new Map<FabricObject, Rect>()
// 当前单选图片的分辨率标记
let resolutionTag: ResolutionTag | null = null

let canvas: Canvas | null = null
// mask 使用独立静态画布，整层透明度只合成一次
let maskCanvas: StaticCanvas | null = null
let resizeObserver: ResizeObserver | null = null
let sceneSaveTimer: ReturnType<typeof setTimeout> | null = null
// 恢复期间禁止保存 Fabric 的 clear/add 事件，避免异步恢复把快照写成空场景
let sceneRestoreInProgress = false
// 恢复代际用于使卸载或用户清空后的迟到恢复回调失效
let sceneRestoreGeneration = 0
// 组件销毁后忽略所有异步恢复与放置回调
let sceneDisposed = false
// 只有用户变更过画布才需要在卸载时刷新快照
let sceneDirty = false
// 快照版本递增，旧写入完成后不能把较新的变更标记为已保存
let sceneRevision = 0
// IndexedDB 快照写入串行化，保持提交顺序稳定
let sceneWriteChain: Promise<void> = Promise.resolve()
// 初始恢复 Promise 作为所有外部上板/导入操作的屏障
let sceneRestorePromise: Promise<void> = Promise.resolve()
// dragenter/dragleave 在子元素间移动时会成对触发，使用深度计数避免反馈闪烁
let dragDepth = 0
// 平移拖拽状态：pointerId 统一鼠标 / 触摸
let isPanning = false
let lastClientX = 0
let lastClientY = 0
// 移动端双指手势状态；以开始时中点下的场景点为锚，避免缩放过程中画面漂移
let pinchGesture: PinchGesture | null = null
// 平滑平移动画的 rAF 句柄
let panAnimFrame: number | null = null
// 图片原始 blob 的运行时缓存：assetKey → blob（生成时取源图，避免反复读 IndexedDB）
const runtimeBlobs = new Map<string, Blob>()
// 上一个放置位置（场景坐标，right/bottom 为右缘 / 下缘）
let lastPlaced: { right: number; top: number; bottom: number } | null = null
// 多个异步输出共享同一画布时串行排布，避免同时计算到相同的上板位置
let outputPlacementQueue: Promise<void> = Promise.resolve()
// mask 笔迹撤销栈（LIFO，上限见 MASK_UNDO_LIMIT）：记录笔迹新增与整体清除
type MaskUndoEntry =
  | { type: 'add'; path: FabricObject }
  | { type: 'clear'; paths: FabricObject[] }
const maskUndoStack: MaskUndoEntry[] = []
// mask 笔迹共享裁剪框（限制笔迹不画出目标图片；fabric 允许跨对象共享 clipPath 实例）
let maskClipRect: Rect | null = null
// 画笔模式下被锁定对象的原始交互状态
let lockedStates: Map<FabricObject, { selectable: boolean; evented: boolean }> | null = null

// ==================== 初始化 ====================

onMounted(() => {
  const element = canvasElRef.value
  const container = containerRef.value
  if (!element || !container) return
  canvas = new Canvas(element, {
    width: Math.max(container.clientWidth, 320),
    height: Math.max(container.clientHeight, 320),
    preserveObjectStacking: true,
    // 背景透明，圆点网格由容器 CSS 透出；导出的 mask 带 alpha
    backgroundColor: '',
    // 默认关闭组选；点击框选工具后由交互同步逻辑开启
    selection: false,
    defaultCursor: 'grab',
  })
  const maskElement = maskCanvasElRef.value
  if (maskElement) {
    maskElement.style.opacity = String(MASK_OPACITY)
    maskCanvas = new StaticCanvas(maskElement, {
      width: canvas.getWidth(),
      height: canvas.getHeight(),
      backgroundColor: '',
      renderOnAddRemove: false,
    })
    maskCanvas.setViewportTransform([...canvas.viewportTransform] as TMat2D)
  }
  bindCanvasEvents()
  bindTouchGestures(container)
  syncCanvasSelection()
  // 涂抹模式下右键 / 中键拖拽平移，需屏蔽画布上的右键菜单
  container.addEventListener('contextmenu', suppressContextMenu)
  // 原生文件与历史缩略图拖放都在画布根节点接收，落点转换在 drop 时完成。
  container.addEventListener('dragenter', onDragEnter)
  container.addEventListener('dragover', onDragOver)
  container.addEventListener('dragleave', onDragLeave)
  container.addEventListener('drop', onDrop)
  resizeObserver = new ResizeObserver(fitToContainer)
  resizeObserver.observe(container)
  window.addEventListener('keydown', onKeyDown)
  window.addEventListener('pagehide', onPageHide)
  document.addEventListener('visibilitychange', onVisibilityChange)
  syncDotGrid()
  sceneDisposed = false
  sceneRestoreInProgress = true
  const generation = ++sceneRestoreGeneration
  sceneRestorePromise = restoreScene(generation)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  window.removeEventListener('pagehide', onPageHide)
  document.removeEventListener('visibilitychange', onVisibilityChange)
  const container = containerRef.value
  container?.removeEventListener('contextmenu', suppressContextMenu)
  container?.removeEventListener('dragenter', onDragEnter)
  container?.removeEventListener('dragover', onDragOver)
  container?.removeEventListener('dragleave', onDragLeave)
  container?.removeEventListener('drop', onDrop)
  unbindTouchGestures(container)
  dragDepth = 0
  dropTargetActive.value = false
  pinchGesture = null
  resizeObserver?.disconnect()
  resizeObserver = null
  stopPanAnim()
  if (sceneSaveTimer) clearTimeout(sceneSaveTimer)
  sceneSaveTimer = null
  // 恢复尚未完成时不要用当前空画布覆盖已有快照；已完成且有变更时尽力刷新最后一次写入。
  if (!sceneRestoreInProgress && sceneDirty) flushSceneSave()
  sceneDisposed = true
  sceneRestoreGeneration++
  if (canvas) {
    void canvas.dispose()
    canvas = null
  }
  if (maskCanvas) {
    void maskCanvas.dispose()
    maskCanvas = null
  }
})

// 画布逻辑尺寸跟随容器（无上界，配合平移形成无限画布）
function fitToContainer(): void {
  if (!canvas || !containerRef.value) return
  const { clientWidth, clientHeight } = containerRef.value
  if (clientWidth <= 0 || clientHeight <= 0) return
  canvas.setDimensions({ width: Math.floor(clientWidth), height: Math.floor(clientHeight) })
  maskCanvas?.setDimensions({ width: Math.floor(clientWidth), height: Math.floor(clientHeight) })
}

// ==================== 事件绑定 ====================

function suppressContextMenu(event: Event): void {
  event.preventDefault()
}

// 拖放过程中浏览器可能隐藏文件列表，只能同时检查 Files 类型与实际文件。
function isCreativeDropCandidate(dataTransfer: DataTransfer | null): boolean {
  if (!dataTransfer) return false
  const types = Array.from(dataTransfer.types ?? [])
  return types.includes(CREATIVE_OUTPUT_DRAG_MIME) || types.includes('Files') || dataTransfer.files.length > 0
}

function onDragEnter(event: DragEvent): void {
  if (!isCreativeDropCandidate(event.dataTransfer)) return
  dragDepth += 1
  dropTargetActive.value = true
}

function onDragOver(event: DragEvent): void {
  if (!isCreativeDropCandidate(event.dataTransfer)) return
  event.preventDefault()
  event.stopPropagation()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
  dropTargetActive.value = true
}

function onDragLeave(event: DragEvent): void {
  if (!dropTargetActive.value && !isCreativeDropCandidate(event.dataTransfer)) return
  dragDepth = Math.max(0, dragDepth - 1)
  if (dragDepth === 0) dropTargetActive.value = false
}

// 将浏览器拖放事件转换为场景坐标；Fabric 会自动扣除当前缩放与视口平移。
function scenePointForDrop(event: DragEvent): { x: number; y: number } | null {
  if (!canvas) return null
  return canvas.getScenePoint(event)
}

function onDrop(event: DragEvent): void {
  const dataTransfer = event.dataTransfer
  if (!isCreativeDropCandidate(dataTransfer)) return
  event.preventDefault()
  event.stopPropagation()
  dragDepth = 0
  dropTargetActive.value = false
  const center = scenePointForDrop(event)
  if (!center || !dataTransfer) return

  const outputValue = dataTransfer.getData(CREATIVE_OUTPUT_DRAG_MIME)
  if (outputValue) {
    const payload = parseCreativeOutputDrag(outputValue)
    if (!payload) {
      emit('error', t('creative.error.dropInvalid'))
      return
    }
    void importDroppedOutput(payload, center)
    return
  }

  const files = Array.from(dataTransfer.files ?? [])
  const imageFiles = files.filter(isSupportedCreativeImageFile)
  if (!imageFiles.length) {
    emit('error', t('creative.error.dropUnsupported'))
    return
  }
  void addDroppedFiles(imageFiles, center)
}

function bindCanvasEvents(): void {
  if (!canvas) return
  // 空白处按下左键 / 单指 = 平移视角；框选工具开启时交给 Fabric 绘制选框；中键 / 右键始终可平移
  canvas.on('mouse:down', (event) => {
    if (!canvas) return
    if (painting.value) {
      if (isPanButton(event.e)) startPan(event.e)
      return
    }
    if (isPanButton(event.e)) {
      startPan(event.e)
      return
    }
    if (event.target) return
    if (!isPrimaryPointer(event.e)) return
    // 框选工具开启时空白拖拽交给 Fabric 绘制选框，不做平移
    if (boxSelectMode.value) return
    startPan(event.e)
    canvas.discardActiveObject()
  })
  canvas.on('mouse:move', (event) => {
    if (!canvas || !isPanning) return
    const client = clientPoint(event.e)
    const dx = client.x - lastClientX
    const dy = client.y - lastClientY
    lastClientX = client.x
    lastClientY = client.y
    // 视口平移与缩放无关：直接改 vpt 平移分量
    const vpt = [...canvas.viewportTransform] as TMat2D
    vpt[4] += dx
    vpt[5] += dy
    canvas.setViewportTransform(vpt)
    syncDotGrid()
    scheduleSceneSave()
  })
  canvas.on('mouse:up', () => stopPanning())
  canvas.on('mouse:wheel', (event) => {
    if (!canvas) return
    const wheel = event.e as WheelEvent
    wheel.preventDefault()
    wheel.stopPropagation()
    if (wheel.ctrlKey) {
      // 浏览器会给触控板捏合 wheel 标记 ctrlKey；以光标位置为缩放中心。
      const next = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, canvas.getZoom() * ZOOM_WHEEL_FACTOR ** wheel.deltaY))
      canvas.zoomToPoint(new Point(wheel.offsetX, wheel.offsetY), next)
    } else {
      // 普通双指滚动沿手指方向平移画布，取反 wheel delta 抵消浏览器滚动语义。
      const dx = normalizeWheelDelta(wheel.deltaX, wheel.deltaMode, canvas.getWidth())
      const dy = normalizeWheelDelta(wheel.deltaY, wheel.deltaMode, canvas.getHeight())
      const vpt = [...canvas.viewportTransform] as TMat2D
      vpt[4] -= dx
      vpt[5] -= dy
      canvas.setViewportTransform(vpt)
    }
    syncDotGrid()
    scheduleSceneSave()
  })
  // 画笔落笔完成：标记为 mask 轨迹，画在图片上层、不参与选中；入撤销栈并套用图片范围裁剪
  canvas.on('path:created', (event) => {
    const path = event.path as FabricObject | undefined
    if (path && painting.value) {
      setObjectData(path, { kind: 'mask' })
      path.set({ selectable: false, evented: false })
      // 预览画笔带透明度，落笔后改为不透明颜色交给独立画布统一合成
      path.set({
        fill: path.fill ? MASK_TINT : path.fill,
        stroke: path.stroke ? MASK_TINT : path.stroke,
      })
      if (maskClipRect) {
        path.clipPath = maskClipRect
      }
      // 将每笔路径移入独立画布，先合成不透明笔迹，再由画布整体设置透明度
      if (maskCanvas) {
        canvas?.remove(path)
        addMaskPath(path)
      }
      hasMaskStrokes.value = true
      pushMaskUndo({ type: 'add', path })
    }
    scheduleSceneSave()
  })
  canvas.on('object:added', () => {
    scheduleSceneSave()
  })
  canvas.on('object:modified', () => {
    refreshRefOutlines()
    refreshResolutionTag(selectedImage.value instanceof FabricImage ? selectedImage.value : null)
    scheduleSceneSave()
  })
  // 拖图 / 缩放 / 旋转过程持续同步参考图描边（这些操作只画 upper canvas）
  canvas.on('object:moving', () => {
    refreshRefOutlines()
    refreshResolutionTag(selectedImage.value instanceof FabricImage ? selectedImage.value : null)
  })
  canvas.on('object:scaling', () => {
    refreshRefOutlines()
    refreshResolutionTag(selectedImage.value instanceof FabricImage ? selectedImage.value : null)
  })
  canvas.on('object:rotating', () => {
    refreshRefOutlines()
    refreshResolutionTag(selectedImage.value instanceof FabricImage ? selectedImage.value : null)
  })
  canvas.on('object:removed', (event) => {
    // 锚点图片被删除时同步清空，涂抹模式随之退出；参考图集合同步剔除
    if (event.target && inpaintAnchor.value === event.target) {
      inpaintAnchor.value = null
    }
    if (event.target && editRefs.value.includes(event.target)) {
      editRefs.value = editRefs.value.filter((item) => item !== event.target)
    }
    if (event.target === resolutionTag?.image) {
      removeResolutionTag()
    }
    if (!getMaskPaths().length) {
      hasMaskStrokes.value = false
    }
    scheduleSceneSave()
  })
  canvas.on('selection:created', (event) => {
    selectedObjectCount.value = canvas?.getActiveObjects().length || event.selected?.length || 0
    syncEditRefFromSelection(event.selected)
    onImageSelected(event.selected?.length === 1 ? pickImage(event.selected[0]) : null)
  })
  canvas.on('selection:updated', (event) => {
    selectedObjectCount.value = canvas?.getActiveObjects().length || event.selected?.length || 0
    syncEditRefFromSelection(event.selected)
    onImageSelected(event.selected?.length === 1 ? pickImage(event.selected[0]) : null)
  })
  canvas.on('selection:cleared', () => {
    selectedObjectCount.value = 0
    selectedImage.value = null
    // fabric 画笔落笔时会自动丢弃选中对象：涂抹期间保留锚点，仅手动取消选中才清空；
    // 编辑模式的参考图集合与选中态解耦，取消选中不影响已选参考
    if (!painting.value) {
      removeResolutionTag()
      inpaintAnchor.value = null
    } else {
      // 涂抹期间活动选中态会被 Fabric 清掉，标签仍跟随当前涂抹锚点
      refreshResolutionTag(inpaintAnchor.value instanceof FabricImage ? inpaintAnchor.value : null)
    }
  })
}

// ==================== 移动端双指缩放 ====================

// 触控事件使用捕获阶段，确保 Fabric 的主触点拖动不会抢走双指移动事件。
function bindTouchGestures(container: HTMLDivElement): void {
  container.addEventListener('touchstart', onCanvasTouchStart, { capture: true, passive: false })
  container.addEventListener('touchmove', onCanvasTouchMove, { capture: true, passive: false })
  container.addEventListener('touchend', onCanvasTouchEnd, { capture: true, passive: false })
  container.addEventListener('touchcancel', onCanvasTouchCancel, { capture: true, passive: false })
}

function unbindTouchGestures(container: HTMLDivElement | null): void {
  if (!container) return
  container.removeEventListener('touchstart', onCanvasTouchStart, true)
  container.removeEventListener('touchmove', onCanvasTouchMove, true)
  container.removeEventListener('touchend', onCanvasTouchEnd, true)
  container.removeEventListener('touchcancel', onCanvasTouchCancel, true)
}

function touchPoints(event: TouchEvent): [TouchPoint, TouchPoint] | null {
  if (!event.touches || event.touches.length < 2) return null
  const first = event.touches[0]
  const second = event.touches[1]
  if (!first || !second) return null
  return [
    { clientX: first.clientX, clientY: first.clientY },
    { clientX: second.clientX, clientY: second.clientY },
  ]
}

function midpointAndDistance(points: [TouchPoint, TouchPoint]): { midpoint: TouchPoint; distance: number } {
  const [first, second] = points
  return {
    midpoint: {
      clientX: (first.clientX + second.clientX) / 2,
      clientY: (first.clientY + second.clientY) / 2,
    },
    distance: Math.hypot(second.clientX - first.clientX, second.clientY - first.clientY),
  }
}

// Fabric 的指针换算同时处理画布偏移、滚动和 Retina 缩放；没有该 API 时回退到 DOM 边界换算，便于测试和降级环境继续工作。
function viewportPointForClient(point: TouchPoint): { x: number; y: number } {
  if (!canvas) return { x: point.clientX, y: point.clientY }
  const target = canvas.upperCanvasEl ?? canvasElRef.value ?? containerRef.value
  const pointerEvent = {
    type: 'touchmove',
    target,
    touches: [point],
    changedTouches: [point],
  } as unknown as TouchEvent
  const fabricCanvas = canvas as unknown as {
    getViewportPoint?: (event: TouchEvent) => { x: number; y: number }
  }
  if (typeof fabricCanvas.getViewportPoint === 'function') {
    return fabricCanvas.getViewportPoint(pointerEvent)
  }
  const bounds = target?.getBoundingClientRect?.()
  return {
    x: point.clientX - (bounds?.left ?? 0),
    y: point.clientY - (bounds?.top ?? 0),
  }
}

function scenePointForClient(point: TouchPoint): { x: number; y: number } {
  if (!canvas) return { x: point.clientX, y: point.clientY }
  const target = canvas.upperCanvasEl ?? canvasElRef.value ?? containerRef.value
  const pointerEvent = {
    type: 'touchmove',
    target,
    touches: [point],
    changedTouches: [point],
  } as unknown as TouchEvent
  const fabricCanvas = canvas as unknown as {
    getScenePoint?: (event: TouchEvent) => { x: number; y: number }
  }
  if (typeof fabricCanvas.getScenePoint === 'function') {
    return fabricCanvas.getScenePoint(pointerEvent)
  }
  const viewport = viewportPointForClient(point)
  const [a, b, c, d, tx, ty] = canvas.viewportTransform
  const determinant = a * d - b * c
  if (!determinant) return viewport
  return {
    x: (d * (viewport.x - tx) - c * (viewport.y - ty)) / determinant,
    y: (-b * (viewport.x - tx) + a * (viewport.y - ty)) / determinant,
  }
}

function onCanvasTouchStart(event: TouchEvent): void {
  const points = touchPoints(event)
  if (!canvas || !points) return
  const { midpoint, distance } = midpointAndDistance(points)
  if (!Number.isFinite(distance) || distance <= 0) return
  const startViewportTransform = [...canvas.viewportTransform] as TMat2D
  pinchGesture = {
    startDistance: distance,
    startZoom: canvas.getZoom(),
    startScenePoint: scenePointForClient(midpoint),
    startViewportTransform,
  }
  stopPanning()
  event.preventDefault()
  event.stopPropagation()
}

function onCanvasTouchMove(event: TouchEvent): void {
  if (!canvas || !pinchGesture) return
  const points = touchPoints(event)
  if (!points) return
  const { midpoint, distance } = midpointAndDistance(points)
  if (!Number.isFinite(distance) || distance <= 0) return

  const ratio = distance / pinchGesture.startDistance
  const nextZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, pinchGesture.startZoom * ratio))
  const linearRatio = nextZoom / pinchGesture.startZoom
  const start = pinchGesture.startViewportTransform
  const vpt = [...start] as TMat2D
  vpt[0] = start[0] * linearRatio
  vpt[1] = start[1] * linearRatio
  vpt[2] = start[2] * linearRatio
  vpt[3] = start[3] * linearRatio
  const viewport = viewportPointForClient(midpoint)
  vpt[4] = viewport.x - (vpt[0] * pinchGesture.startScenePoint.x + vpt[2] * pinchGesture.startScenePoint.y)
  vpt[5] = viewport.y - (vpt[1] * pinchGesture.startScenePoint.x + vpt[3] * pinchGesture.startScenePoint.y)
  canvas.setViewportTransform(vpt)
  syncDotGrid()
  scheduleSceneSave()
  event.preventDefault()
  event.stopPropagation()
}

function onCanvasTouchEnd(event: TouchEvent): void {
  if (!pinchGesture || (event.touches?.length ?? 0) >= 2) return
  event.preventDefault()
  pinchGesture = null
  isPanning = false
}

function onCanvasTouchCancel(event: TouchEvent): void {
  if (!pinchGesture) return
  event.preventDefault()
  pinchGesture = null
  isPanning = false
}

// 页面进入后台时提前刷新待写快照，降低直接关闭浏览器造成的最后一次变更丢失概率。
function onPageHide(): void {
  flushSceneSave()
}

function onVisibilityChange(): void {
  if (document.visibilityState === 'hidden') flushSceneSave()
}

// 将 wheel 的像素、行、页三种单位统一成画布像素，避免鼠标滚轮平移过慢。
function normalizeWheelDelta(delta: number, deltaMode: number, pageSize: number): number {
  if (!Number.isFinite(delta) || delta === 0) return 0
  if (deltaMode === WHEEL_DELTA_MODE_LINE) return delta * WHEEL_LINE_HEIGHT
  if (deltaMode === WHEEL_DELTA_MODE_PAGE) return delta * pageSize
  return delta
}

// 平移拖拽开始（坐标记录 + 抓手光标）
function startPan(event: Event): void {
  if (!canvas) return
  isPanning = true
  const client = clientPoint(event)
  lastClientX = client.x
  lastClientY = client.y
  canvas.defaultCursor = 'grabbing'
}

// 画布平移的备用按键：鼠标中键 / 右键
function isPanButton(event: Event): boolean {
  const mouse = event as MouseEvent
  return typeof mouse.button === 'number' && (mouse.button === 1 || mouse.button === 2)
}

function stopPanning(): void {
  isPanning = false
  if (canvas) canvas.defaultCursor = boxSelectMode.value || painting.value ? 'crosshair' : 'grab'
}

// 鼠标左键或单指触摸才可平移
function isPrimaryPointer(event: Event): boolean {
  const mouse = event as MouseEvent
  if (typeof mouse.button === 'number' && mouse.button !== 0) return false
  const touch = event as TouchEvent
  if (touch.touches && touch.touches.length > 1) return false
  return true
}

// 统一鼠标 / 触摸的屏幕坐标
function clientPoint(event: Event): { x: number; y: number } {
  const touch = event as TouchEvent
  const point = touch.touches?.[0] ?? touch.changedTouches?.[0]
  if (point) return { x: point.clientX, y: point.clientY }
  const mouse = event as MouseEvent
  return { x: mouse.clientX, y: mouse.clientY }
}

function pickImage(target: FabricObject | undefined): FabricObject | null {
  return target instanceof FabricImage ? target : null
}

// Delete / Backspace 删除选中对象；Ctrl / Cmd + Z 撤销上一笔涂抹；Esc 退出框选（输入控件聚焦时不拦截）
function onKeyDown(event: KeyboardEvent): void {
  if (!canvas) return
  const target = event.target as HTMLElement | null
  if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return
  if (event.key === 'Escape' && boxSelectMode.value) {
    event.preventDefault()
    setBoxSelectMode(false)
    return
  }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'z' && !event.shiftKey) {
    event.preventDefault()
    undoMask()
    return
  }
  if (painting.value || event.key !== 'Delete' && event.key !== 'Backspace') return
  const selected = canvas.getActiveObjects()
  if (!selected.length) return
  event.preventDefault()
  // ActiveSelection 只是选区包装对象，必须移除其中的实际图片对象，否则框选删除不会生效。
  canvas.discardActiveObject()
  canvas.remove(...selected)
  canvas.requestRenderAll()
}

// ==================== 涂抹（mask）模式 ====================

// 仅局部重绘开放画笔组与涂抹模式
const isInpaint = computed(() => props.operation === 'inpaint')
// 图生图模式（未选中源图时展示引导胶囊）
const isEdit = computed(() => props.operation === 'edit')

// 选中图片时登记为涂抹锚点（换选自动切换）；仅局部重绘模式下登记
function onImageSelected(image: FabricObject | null): void {
  selectedImage.value = image
  refreshResolutionTag(image instanceof FabricImage ? image : null)
  if (image && isInpaint.value) {
    inpaintAnchor.value = image
    paintSuspended.value = false
  }
}

// 切到局部重绘时若已有选中图片（此前在其它操作下选中），直接登记为锚点并自动进入涂抹；
// 必须先于 syncPainting 的 watch 注册，保证同一 tick 内先登记锚点再判断涂抹条件
watch(isInpaint, (inpaint) => {
  if (inpaint && !inpaintAnchor.value && selectedImage.value) {
    inpaintAnchor.value = selectedImage.value
    paintSuspended.value = false
  }
})

// 涂抹条件 = 局部重绘 + 有锚点图片 + 未手动暂停；任一条件变化统一经 syncPainting 进出
watch([isInpaint, inpaintAnchor, paintSuspended], syncPainting)

function syncPainting(): void {
  const shouldPaint = isInpaint.value && inpaintAnchor.value !== null && !paintSuspended.value
  if (shouldPaint) enterPainting()
  else exitPainting()
  // 锚点切换（生成结果/上传自动上板改选新图）时 painting 可能未中断，
  // enterPainting 直接跳过，必须在此同步刷新笔迹裁剪框，否则会裁在旧锚点位置
  if (painting.value) refreshMaskClip()
  updateAnchorOutline()
}

function makeBrush(): PencilBrush {
  const brush =
    brushShape.value === 'square' ? new SquarePencilBrush(canvas!) : new PencilBrush(canvas!)
  // 展示用紫色叠加层；导出 mask 时才统一改回白色
  // 预览直接绘制在主画布上，保留与独立 mask 画布一致的视觉透明度
  brush.color = MASK_PREVIEW_TINT
  brush.width = brushSize.value
  return brush
}

// 进入涂抹：锁定全部对象，仅落笔作画（锚点用独立描边标示，不依赖 fabric 选中态）
function enterPainting(): void {
  if (!canvas || painting.value || !isInpaint.value || !inpaintAnchor.value) return
  painting.value = true
  lockAllObjects()
  refreshMaskClip()
  canvas.freeDrawingBrush = makeBrush()
  canvas.isDrawingMode = true
  canvas.defaultCursor = 'crosshair'
  syncCanvasSelection()
  canvas.requestRenderAll()
}

function exitPainting(): void {
  if (!canvas || !painting.value) return
  painting.value = false
  canvas.isDrawingMode = false
  canvas.freeDrawingBrush = undefined
  unlockAllObjects()
  canvas.defaultCursor = 'grab'
  syncCanvasSelection()
  // 退出画笔模式即丢弃全部笔迹：mask 是会话级草稿，不保留到下次涂抹
  discardMaskStrokes()
  canvas.requestRenderAll()
}

// 锚点描边：涂抹期间在目标图片外围画一圈青色虚线，避免"在涂哪张图"失焦
function updateAnchorOutline(): void {
  const anchor = inpaintAnchor.value
  if (!canvas || !anchor || !painting.value) {
    removeAnchorOutline()
    return
  }
  if (!anchorOutline) {
    anchorOutline = new Rect({
      ...ANCHOR_OUTLINE_STYLE,
      // 图片使用左上角定位，描边也必须使用相同原点，避免按默认中心原点产生半尺寸偏移。
      originX: 'left',
      originY: 'top',
      selectable: false,
      evented: false,
    })
    setObjectData(anchorOutline, { kind: ANCHOR_OUTLINE_KIND })
    canvas.add(anchorOutline)
  }
  const width = ((anchor as unknown as { width?: number }).width ?? 0) * (anchor.scaleX ?? 1)
  const height = ((anchor as unknown as { height?: number }).height ?? 0) * (anchor.scaleY ?? 1)
  anchorOutline.set({ left: anchor.left, top: anchor.top, width, height, angle: anchor.angle ?? 0 })
  anchorOutline.setCoords()
  canvas.requestRenderAll()
}

function removeAnchorOutline(): void {
  if (anchorOutline && canvas) {
    canvas.remove(anchorOutline)
  }
  anchorOutline = null
}

// 刷新 mask 笔迹的共享裁剪框：限制笔迹只能落在锚点图片的轴对齐包围盒内（场景坐标）。
// 进入涂抹 / 换锚点时调用；锚点涂抹期间被锁定不会移动，无需持续刷新。
function refreshMaskClip(): void {
  maskClipRect = null
  if (!canvas || !inpaintAnchor.value) return
  // getBoundingRect 返回场景坐标（fabric 7 getCoords 为 scene plane），无需视口换算
  const vp = inpaintAnchor.value.getBoundingRect()
  maskClipRect = new Rect({
    left: vp.left,
    top: vp.top,
    width: vp.width,
    height: vp.height,
    // getBoundingRect 给出包围盒左上角；显式使用左上原点，避免 Fabric 默认中心原点造成半尺寸偏移
    originX: 'left',
    originY: 'top',
  })
  maskClipRect.absolutePositioned = true
  // 局部变量收窄类型（forEach 闭包内无法收窄模块级 let）
  const clip = maskClipRect
  getMaskPaths().forEach((object) => {
    object.clipPath = clip
  })
}

// 工具栏涂抹开关：暂停涂抹去移动视角 / 换选图片，再次点击恢复
function togglePainting(): void {
  paintSuspended.value = painting.value
}

// ==================== 编辑模式参考图选择 ====================

// 点击或框选变化时同步参考集：单点选中为 1 个元素，框选为框内全部图片；统一替换整个集合
function syncEditRefFromSelection(selected: FabricObject[] | undefined): void {
  if (!isEdit.value || !selected?.length) return
  const images = selected.filter((object): object is FabricObject => object instanceof FabricImage)
  if (images.length) {
    editRefs.value = images
  }
}

// 同步框选工具状态：开启时 Fabric 接管空白拖拽，关闭时恢复空白拖拽平移
function syncCanvasSelection(): void {
  if (!canvas) return
  canvas.selection = boxSelectMode.value && !painting.value
  canvas.defaultCursor = boxSelectMode.value || painting.value ? 'crosshair' : 'grab'
  canvas.requestRenderAll()
}

// 框选工具开关：三种模式都可用；关闭后空白拖拽恢复平移
function setBoxSelectMode(on: boolean): void {
  boxSelectMode.value = on
  syncCanvasSelection()
}

// 参考图集合变化 → 同步描边；离开编辑模式 → 清空集合
watch(editRefs, refreshRefOutlines)
watch(isEdit, (edit) => {
  if (!edit) {
    editRefs.value = []
  } else if (canvas) {
    // 切入 edit 时沿用其它模式下已经框选的对象，避免必须重新拖一次选框
    syncEditRefFromSelection(canvas.getActiveObjects())
  }
  syncCanvasSelection()
})

// 参考图描边：为集合内每张图片维护一个青色虚线框，拖动/缩放时跟随
function refreshRefOutlines(): void {
  if (!canvas) return
  const current = editRefs.value
  refOutlines.forEach((rect, object) => {
    if (!current.includes(object)) {
      canvas!.remove(rect)
      refOutlines.delete(object)
    }
  })
  current.forEach((object) => {
    let rect = refOutlines.get(object)
    if (!rect) {
      rect = new Rect({
        ...ANCHOR_OUTLINE_STYLE,
        // 参考图可能处于 Fabric ActiveSelection 中，描边位置统一按绝对包围盒的左上角解释
        originX: 'left',
        originY: 'top',
        selectable: false,
        evented: false,
      })
      setObjectData(rect, { kind: REF_OUTLINE_KIND })
      refOutlines.set(object, rect)
      canvas!.add(rect)
    }
    syncOutlineToObject(rect, object)
  })
  canvas.requestRenderAll()
}

// 描边矩形对齐对象的绝对包围盒；ActiveSelection 中不能直接使用对象的 left/top 组内坐标
function syncOutlineToObject(rect: Rect, object: FabricObject): void {
  // ActiveSelection 会改变对象的父级，先刷新角点缓存，避免沿用入组前的场景坐标
  object.setCoords()
  const bounds = object.getBoundingRect()
  rect.set({ left: bounds.left, top: bounds.top, width: bounds.width, height: bounds.height, angle: 0 })
  rect.setCoords()
}

// mask 撤销栈上限，防止长会话无限增长
const MASK_UNDO_LIMIT = 50

// 撤销是否可用（工具栏按钮置灰依据）
const canUndoMask = ref(false)

function pushMaskUndo(entry: MaskUndoEntry): void {
  maskUndoStack.push(entry)
  if (maskUndoStack.length > MASK_UNDO_LIMIT) {
    maskUndoStack.shift()
  }
  canUndoMask.value = true
}

// 撤销上一笔：新增 → 移除该笔迹；清除 → 整体恢复被清除的笔迹
function undoMask(): void {
  if (!canvas) return
  const entry = maskUndoStack.pop()
  if (!entry) {
    canUndoMask.value = false
    return
  }
  if (entry.type === 'add') {
    removeMaskPath(entry.path)
    hasMaskStrokes.value = getMaskPaths().length > 0
  } else {
    entry.paths.forEach((path) => {
      addMaskPath(path)
    })
    hasMaskStrokes.value = true
  }
  canUndoMask.value = maskUndoStack.length > 0
  canvas.requestRenderAll()
  scheduleSceneSave()
}

function setBrushShape(shape: BrushShape): void {
  brushShape.value = shape
  // 涂抹开启时立即换笔，保证下一次落笔即新形状
  if (painting.value && canvas) canvas.freeDrawingBrush = makeBrush()
}

watch(brushSize, (size) => {
  if (canvas?.freeDrawingBrush) canvas.freeDrawingBrush.width = size
})

// 清除全部 mask 笔迹（整体入撤销栈，可一步恢复）
function clearMask(): void {
  if (!canvas) return
  const paths = getMaskPaths()
  if (!paths.length) return
  paths.forEach(removeMaskPath)
  hasMaskStrokes.value = false
  pushMaskUndo({ type: 'clear', paths })
}

// 静默丢弃全部笔迹（退出画笔模式用）：不入撤销栈并清空撤销栈，
// 避免丢弃的草稿被 Ctrl+Z / 撤销按钮复活
function discardMaskStrokes(): void {
  if (!canvas) return
  getMaskPaths().forEach(removeMaskPath)
  hasMaskStrokes.value = false
  maskUndoStack.length = 0
  canUndoMask.value = false
}

function lockAllObjects(): void {
  if (!canvas) return
  lockedStates = new Map()
  canvas.getObjects().forEach((object) => {
    lockedStates?.set(object, { selectable: object.selectable, evented: object.evented })
    object.set({ selectable: false, evented: false })
  })
}

function unlockAllObjects(): void {
  lockedStates?.forEach((state, object) => {
    object.set({ selectable: state.selectable, evented: state.evented })
  })
  lockedStates = null
}

// ==================== 视角与平滑平移 ====================

// 当前视口中心的场景坐标
function viewCenterScene(): { x: number; y: number } {
  const vpt = canvas!.viewportTransform
  const zoom = canvas!.getZoom()
  return {
    x: (canvas!.getWidth() / 2 - vpt[4]) / zoom,
    y: (canvas!.getHeight() / 2 - vpt[5]) / zoom,
  }
}

function stopPanAnim(): void {
  if (panAnimFrame !== null) cancelAnimationFrame(panAnimFrame)
  panAnimFrame = null
}

// 视角平滑移动到指定场景点（保持当前缩放）
function panToScenePoint(point: { x: number; y: number }): void {
  if (!canvas) return
  stopPanAnim()
  const zoom = canvas.getZoom()
  const targetX = canvas.getWidth() / 2 - zoom * point.x
  const targetY = canvas.getHeight() / 2 - zoom * point.y
  const startX = canvas.viewportTransform[4]
  const startY = canvas.viewportTransform[5]
  const duration = 240
  const startTime = performance.now()
  const step = (now: number) => {
    if (!canvas) return
    const progress = Math.min(1, (now - startTime) / duration)
    const eased = 1 - (1 - progress) ** 3
    const vpt = [...canvas.viewportTransform] as TMat2D
    vpt[4] = startX + (targetX - startX) * eased
    vpt[5] = startY + (targetY - startY) * eased
    canvas.setViewportTransform(vpt)
    syncDotGrid()
    if (progress < 1) {
      panAnimFrame = requestAnimationFrame(step)
    } else {
      panAnimFrame = null
      scheduleSceneSave()
    }
  }
  panAnimFrame = requestAnimationFrame(step)
}

// ==================== 图片上板 ====================

// 读取 blob 的像素尺寸（优先 createImageBitmap，失败回退 Image 元素）
async function probeImageSize(blob: Blob): Promise<{ width: number; height: number }> {
  try {
    const bitmap = await createImageBitmap(blob)
    const size = { width: bitmap.width, height: bitmap.height }
    bitmap.close()
    return size
  } catch {
    return await new Promise((resolve, reject) => {
      const url = URL.createObjectURL(blob)
      const image = new Image()
      image.onload = () => {
        URL.revokeObjectURL(url)
        resolve({ width: image.naturalWidth, height: image.naturalHeight })
      }
      image.onerror = () => {
        URL.revokeObjectURL(url)
        reject(new Error('image load failed'))
      }
      image.src = url
    })
  }
}

// 下一个放置位置（场景坐标，返回左上角）：上一个右侧 40px，超出约 2200px 换行
function nextPlacementPoint(width: number, height: number): { x: number; y: number } {
  if (!lastPlaced) {
    const center = viewCenterScene()
    return { x: center.x - width / 2, y: center.y - height / 2 }
  }
  let x = lastPlaced.right + PLACE_GAP
  let y = lastPlaced.top
  if (x + width > PLACE_WRAP_X) {
    x = 0
    y = lastPlaced.bottom + PLACE_GAP
  }
  return { x, y }
}

// 根据图片中心与显示比例计算左上角场景坐标，保证拖放落点对应图片中心。
function centeredPlacementPoint(
  size: { width: number; height: number },
  center: { x: number; y: number },
  scale = PLACE_SCALE,
): { x: number; y: number } {
  return {
    x: center.x - (size.width * scale) / 2,
    y: center.y - (size.height * scale) / 2,
  }
}

function recordLastPlaced(position: { x: number; y: number }, size: { width: number; height: number }): void {
  lastPlaced = {
    right: position.x + size.width,
    top: position.y,
    bottom: position.y + size.height,
  }
}

// 把图片 blob 放上画布：左上角定位于 position，按 scale 缩放显示（原始 blob 不受影响），登记 data 与运行时 blob
async function addImageToScene(
  blob: Blob,
  meta: { assetKey: string; runId?: string; outputIndex?: number },
  position: { x: number; y: number },
  scale = 1,
): Promise<FabricImage | null> {
  if (!canvas) return null
  const url = URL.createObjectURL(blob)
  try {
    const image = await FabricImage.fromURL(url)
    image.set({
      // position 是按左上角计算的，覆盖 FabricImage 默认的中心原点，保证拖放中心定位准确。
      originX: 'left',
      originY: 'top',
      left: position.x,
      top: position.y,
      scaleX: scale,
      scaleY: scale,
    })
    const resolution = imageResolution(image)
    setObjectData(image, {
      kind: 'image',
      assetKey: meta.assetKey,
      ...(resolution ? { resolution } : {}),
      ...(meta.runId !== undefined ? { runId: meta.runId, outputIndex: meta.outputIndex } : {}),
    })
    canvas.add(image)
    canvas.setActiveObject(image)
    selectedObjectCount.value = 1
    selectedImage.value = image
    refreshResolutionTag(image)
    canvas.requestRenderAll()
    return image
  } catch (error) {
    console.error('Failed to load image into canvas:', error)
    emit('error', t('creative.error.loadImageFailed'))
    return null
  } finally {
    URL.revokeObjectURL(url)
  }
}

// 收割成功的输出自动上板：通过队列串行执行，记录 blob → 放置 → 视角移动到新图中心
async function placeOutput(asset: { blob: Blob; runId: string; outputIndex: number }): Promise<void> {
  const placement = outputPlacementQueue.then(async () => {
    await sceneRestorePromise
    if (sceneDisposed) return
    await placeOutputNow(asset)
  })
  // 队列本身不能被单次失败阻塞；具体错误由 placeOutputNow 自行处理并转为已完成。
  outputPlacementQueue = placement.catch(() => undefined)
  await placement
}

// 历史输出拖到画布时使用指定中心点，但仍串入输出队列，避免与自动收割同时修改 Fabric 场景。
async function placeOutputAt(
  asset: { blob: Blob; runId: string; outputIndex: number },
  center: { x: number; y: number },
): Promise<void> {
  const placement = outputPlacementQueue.then(async () => {
    await sceneRestorePromise
    if (sceneDisposed) return
    await placeOutputNow(asset, center)
  })
  outputPlacementQueue = placement.catch(() => undefined)
  await placement
}

async function placeOutputNow(
  asset: { blob: Blob; runId: string; outputIndex: number },
  center?: { x: number; y: number },
): Promise<void> {
  if (!canvas) return
  const assetKey = outputAssetKey(asset.runId, asset.outputIndex)
  runtimeBlobs.set(assetKey, asset.blob)
  try {
    // 生成输出与上传图统一按 1/4 尺寸上板；排布、换行与视角定位均按缩放后尺寸计算
    const size = await probeImageSize(asset.blob)
    const placedWidth = size.width * PLACE_SCALE
    const placedHeight = size.height * PLACE_SCALE
    const position = center
      ? centeredPlacementPoint(size, center)
      : nextPlacementPoint(placedWidth, placedHeight)
    const image = await addImageToScene(
      asset.blob,
      { assetKey, runId: asset.runId, outputIndex: asset.outputIndex },
      position,
      PLACE_SCALE,
    )
    if (!image) return
    recordLastPlaced(position, { width: placedWidth, height: placedHeight })
    if (!center) {
      panToScenePoint({ x: position.x + placedWidth / 2, y: position.y + placedHeight / 2 })
    }
    scheduleSceneSave()
    // 图片完成上板后立即落一份快照，避免用户在防抖窗口内刷新导致场景丢失。
    flushSceneSave()
  } catch (error) {
    console.error('Failed to place creative output:', error)
    emit('error', t('creative.error.loadImageFailed'))
  }
}

// 上传或外部拖入的源图先保存本地库，再按指定中心点放置，保证场景刷新后仍可恢复。
async function addUploadedImageAt(blob: Blob, center: { x: number; y: number }): Promise<void> {
  await sceneRestorePromise
  if (sceneDisposed) return
  if (!canvas) return
  const assetKey = localAssetKey('source', `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`)
  try {
    await saveAsset({ key: assetKey, kind: 'source', blob, createdAt: Date.now() })
  } catch (error) {
    // 本地保存失败（多为配额不足）：不上板，避免画出无法恢复的场景
    if (error instanceof LocalStoreQuotaError) {
      emit('error', t('creative.error.quotaExceeded'))
    } else {
      emit('error', t('creative.error.loadImageFailed'))
    }
    return
  }
  runtimeBlobs.set(assetKey, blob)
  try {
    // 上传图按 1/4 尺寸上板（与生成输出统一；原始 blob 不变，源图仍发全分辨率给模型）
    const size = await probeImageSize(blob)
    const scale = PLACE_SCALE
    const position = centeredPlacementPoint(size, center, scale)
    const image = await addImageToScene(
      blob,
      { assetKey },
      position,
      scale,
    )
    if (image) {
      recordLastPlaced(position, { width: size.width * scale, height: size.height * scale })
    }
    scheduleSceneSave()
    // 上传图片已经写入素材库后立即保存场景，确保刚放置完成就刷新也能恢复。
    flushSceneSave()
  } catch (error) {
    console.error('Failed to place uploaded image:', error)
    emit('error', t('creative.error.loadImageFailed'))
  }
}

// 工具栏上传仍沿用“当前视角中心”语义；外部拖放通过 addUploadedImageAt 传入落点。
async function addUploadedImage(blob: Blob): Promise<void> {
  if (!canvas) return
  await addUploadedImageAt(blob, viewCenterScene())
}

// 多张外部图片按落点中心开始斜向错开，单张失败不会阻塞后续文件。
async function addDroppedFiles(files: File[], center: { x: number; y: number }): Promise<void> {
  for (const [index, file] of files.entries()) {
    const offset = index * PLACE_GAP
    await addUploadedImageAt(file, { x: center.x + offset, y: center.y + offset })
  }
}

// 历史缩略图只传本地 key；实际图片从当前浏览器 IndexedDB 读取，不触发网络请求。
async function importDroppedOutput(
  payload: { runId: string; outputIndex: number },
  center: { x: number; y: number },
): Promise<void> {
  const asset = await loadAsset(outputAssetKey(payload.runId, payload.outputIndex)).catch(() => null)
  if (!asset || asset.kind !== 'output') {
    emit('error', t('creative.error.dropHistoryUnavailable'))
    return
  }
  await placeOutputAt(
    { blob: asset.blob, runId: payload.runId, outputIndex: payload.outputIndex },
    center,
  )
}

// ==================== 生成输入采集 ====================

// 当前关联图片对象：优先 fabric 活动对象，缺失时回退到涂抹锚点
// （画笔落笔时 fabric 会自动丢弃选中，涂抹期间锚点才是可靠的图片引用）
function selectedImageObject(): FabricImage | null {
  if (!canvas) return null
  const active = canvas.getActiveObject()
  if (active instanceof FabricImage) return active
  return inpaintAnchor.value instanceof FabricImage ? inpaintAnchor.value : null
}

// 选中图片的原始 blob：运行时缓存优先，缺失时回 IndexedDB 取
async function getSelectedImageBlob(): Promise<Blob | null> {
  const image = selectedImageObject()
  if (!image) return null
  const assetKey = objectData(image).assetKey
  if (typeof assetKey !== 'string') return null
  const cached = runtimeBlobs.get(assetKey)
  if (cached) return cached
  const asset = await loadAsset(assetKey).catch(() => null)
  if (asset) {
    runtimeBlobs.set(assetKey, asset.blob)
    return asset.blob
  }
  return null
}

// 编辑模式已选参考图的原始 blob（按画布对象顺序收集，解析失败的跳过）
async function getEditRefBlobs(): Promise<Blob[]> {
  const blobs: Blob[] = []
  for (const image of editRefs.value) {
    if (!(image instanceof FabricImage)) continue
    const assetKey = objectData(image).assetKey
    if (typeof assetKey !== 'string') continue
    const cached = runtimeBlobs.get(assetKey)
    if (cached) {
      blobs.push(cached)
      continue
    }
    const asset = await loadAsset(assetKey).catch(() => null)
    if (asset) {
      runtimeBlobs.set(assetKey, asset.blob)
      blobs.push(asset.blob)
    }
  }
  return blobs
}

// 导出 mask：选中图片 → 独立 mask 画布切到单位阵视口
// → 展示色笔迹临时改回纯白 → toCanvasElement 裁剪 → 离屏铺不透明底并清除笔迹 → PNG
async function getMaskBlob(): Promise<Blob | null> {
  if (!canvas || !maskCanvas) return null
  const image = selectedImageObject()
  if (!image) return null
  const maskObjects = getMaskPaths()
  if (!maskObjects.length) return null

  const rect = image.getBoundingRect()
  // 展示用紫色叠加层，导出先统一改为纯白轨迹，结束后恢复原样。
  const styleBackup = maskObjects.map((object) => ({ object, fill: object.fill, stroke: object.stroke }))
  styleBackup.forEach(({ object }) => {
    object.set({
      fill: object.fill ? MASK_COLOR : object.fill,
      stroke: object.stroke ? MASK_COLOR : object.stroke,
    })
  })
  const viewport = [...maskCanvas.viewportTransform] as TMat2D
  // 单位阵视口：裁剪框即场景坐标系下的包围盒
  maskCanvas.setViewportTransform([1, 0, 0, 1, 0, 0])
  let element: HTMLCanvasElement
  try {
    element = maskCanvas.toCanvasElement(1, {
      left: rect.left,
      top: rect.top,
      width: rect.width,
      height: rect.height,
    })
  } finally {
    maskCanvas.setViewportTransform(viewport)
    styleBackup.forEach(({ object, fill, stroke }) => {
      object.set({ fill: fill ?? null, stroke: stroke ?? null })
    })
    canvas.requestRenderAll()
    maskCanvas.requestRenderAll()
    syncDotGrid()
  }

  // 拉伸回原图自然尺寸，与服务端"mask 尺寸必须与源图一致"的校验对齐
  const source = image.getElement() as HTMLImageElement
  const naturalWidth = source.naturalWidth || Math.max(1, Math.round(rect.width))
  const naturalHeight = source.naturalHeight || Math.max(1, Math.round(rect.height))
  const offscreen = document.createElement('canvas')
  offscreen.width = naturalWidth
  offscreen.height = naturalHeight
  const context = offscreen.getContext('2d')
  if (!context) return null
  // OpenAI mask 约定 alpha=0 的透明区域为重绘区域；因此先铺不透明底，
  // 再用用户笔迹做 destination-out 清除，保证用户涂抹的区域透明。
  context.fillStyle = '#ffffff'
  context.fillRect(0, 0, naturalWidth, naturalHeight)
  context.globalCompositeOperation = 'destination-out'
  context.drawImage(element, 0, 0, naturalWidth, naturalHeight)
  context.globalCompositeOperation = 'source-over'
  return await new Promise<Blob | null>((resolve) => offscreen.toBlob(resolve, 'image/png'))
}

// ==================== 移除与重置 ====================

function removeSelected(): void {
  if (!canvas) return
  const selected = canvas.getActiveObjects()
  if (!selected.length) return
  // ActiveSelection 不是画布中的实际图片，逐个移除其成员才能删除框选的全部图片。
  canvas.discardActiveObject()
  canvas.remove(...selected)
  canvas.requestRenderAll()
}

// 下载选中图片的原始 blob 到本地（与历史下载一致走 file-saver）
async function downloadSelected(): Promise<void> {
  const blob = await getSelectedImageBlob()
  if (!blob) return
  const extension = (blob.type || 'image/png').split('/')[1] || 'png'
  saveAs(blob, `creative-image-${Date.now()}.${extension}`)
}

function resetCanvas(): void {
  if (!canvas) return
  // 用户明确清空画布时取消尚未完成的旧快照恢复，避免清空后旧对象迟到复活。
  sceneRestoreGeneration++
  sceneRestoreInProgress = false
  stopPanAnim()
  inpaintAnchor.value = null
  paintSuspended.value = false
  editRefs.value = []
  if (boxSelectMode.value) setBoxSelectMode(false)
  removeResolutionTag()
  refOutlines.clear()
  maskUndoStack.length = 0
  canUndoMask.value = false
  maskClipRect = null
  exitPainting()
  removeAnchorOutline()
  canvas.discardActiveObject()
  canvas.clear()
  canvas.backgroundColor = ''
  canvas.setViewportTransform([1, 0, 0, 1, 0, 0])
  syncDotGrid()
  hasMaskStrokes.value = false
  lastPlaced = null
  runtimeBlobs.clear()
  selectedImage.value = null
  selectedObjectCount.value = 0
  canvas.requestRenderAll()
  scheduleSceneSave()
  // 清空是用户明确操作，立即写入空场景，避免旧快照在短暂防抖期间复活。
  flushSceneSave()
}

// ==================== 场景持久化 ====================

function scheduleSceneSave(): void {
  if (sceneDisposed || sceneRestoreInProgress) return
  sceneDirty = true
  sceneRevision++
  if (sceneSaveTimer) clearTimeout(sceneSaveTimer)
  sceneSaveTimer = setTimeout(() => {
    sceneSaveTimer = null
    void persistSceneNow()
  }, SCENE_SAVE_DEBOUNCE)
}

function flushSceneSave(): void {
  if (sceneSaveTimer) clearTimeout(sceneSaveTimer)
  sceneSaveTimer = null
  void persistSceneNow()
}

function persistSceneNow(): Promise<void> {
  if (!canvas || sceneDisposed || sceneRestoreInProgress || !sceneDirty) return sceneWriteChain
  const json = snapshotScene()
  const revision = sceneRevision
  sceneWriteChain = sceneWriteChain
    .catch(() => undefined)
    .then(() => saveSceneJson(SCENE_KEY, json))
    .then(() => {
      // 只有期间没有更新过画布时才清除脏标记，否则保留给下一次写入。
      if (sceneRevision === revision) sceneDirty = false
    })
    .catch((error) => {
      console.error('Failed to persist creative scene:', error)
    })
  return sceneWriteChain
}

// 序列化（含自定义 data）：图片像素不进快照，src 用 asset://<assetKey> 占位；
// 锚点/参考图描边和分辨率标记是运行时辅助对象，不持久化
function snapshotScene(): string {
  if (!canvas) return '{}'
  const json = canvas.toObject(['data']) as { objects?: Array<Record<string, unknown> & ObjectWithData> }
  for (const object of json.objects ?? []) {
    const assetKey = object.data?.assetKey
    if (isImageSnapshotObject(object) && typeof assetKey === 'string') {
      object.src = `${ASSET_PROTOCOL}${assetKey}`
    }
  }
  json.objects = (json.objects ?? []).filter(
    (object) =>
      object.data?.kind !== ANCHOR_OUTLINE_KIND &&
      object.data?.kind !== REF_OUTLINE_KIND &&
      object.data?.kind !== RESOLUTION_TAG_KIND &&
      object.data?.kind !== 'mask',
  )
  return JSON.stringify(json)
}

// 挂载时恢复：asset:// 图片回 IndexedDB 取 blob，缺失的图跳过不阻塞
async function restoreScene(generation: number): Promise<void> {
  const target = canvas
  if (!target) {
    sceneRestoreInProgress = false
    return
  }
  const pendingUrls: string[] = []
  const isCurrent = () => !sceneDisposed && canvas === target && sceneRestoreGeneration === generation
  try {
    const stored = await loadSceneJson(SCENE_KEY)
    if (!stored || !isCurrent()) return
    const parsed = JSON.parse(stored) as { objects?: Array<Record<string, unknown> & ObjectWithData> }
    const objects = Array.isArray(parsed.objects) ? parsed.objects : []
    const restored: typeof objects = []
    for (const object of objects) {
      // 旧快照里可能残留锚点/参考图描边等运行时辅助对象，直接跳过
      if (
        object.data?.kind === ANCHOR_OUTLINE_KIND ||
        object.data?.kind === REF_OUTLINE_KIND ||
        object.data?.kind === RESOLUTION_TAG_KIND
      ) {
        continue
      }
      // mask 笔迹是会话级草稿（退出画笔即丢弃），不应跨刷新复活：
      // 历史快照里残留的笔迹位置可能与拖动后的图片错位，直接丢弃
      if (object.data?.kind === 'mask') {
        continue
      }
      const assetKey = object.data?.assetKey
      if (isImageSnapshotObject(object) && typeof assetKey === 'string') {
        const blob = await loadAsset(assetKey)
          .then((asset) => asset?.blob ?? null)
          .catch(() => null)
        if (!isCurrent()) return
        if (!blob) {
          // 本地素材缺失：跳过该图，继续恢复其它对象
          continue
        }
        runtimeBlobs.set(assetKey, blob)
        const objectUrl = URL.createObjectURL(blob)
        object.src = objectUrl
        pendingUrls.push(objectUrl)
      } else if (
        isImageSnapshotObject(object) &&
        typeof object.src === 'string' &&
        // 旧版本快照里的 blob: 对象 URL 已失效，无法恢复，跳过
        object.src.startsWith('blob:')
      ) {
        continue
      }
      restored.push(object)
    }
    parsed.objects = restored
    if (!isCurrent()) return
    await target.loadFromJSON(parsed as never)
    if (!isCurrent()) return
    target.requestRenderAll()
    // 恢复的视口/缩放同步到圆点网格
    syncDotGrid()
    hasMaskStrokes.value = getMaskPaths().length > 0
  } catch (error) {
    console.error('Failed to restore creative scene:', error)
  } finally {
    pendingUrls.forEach((url) => URL.revokeObjectURL(url))
    if (sceneRestoreGeneration === generation) {
      sceneRestoreInProgress = false
      // 恢复过程本身触发的 Fabric 事件不属于用户变更。
      sceneDirty = false
    }
  }
}

// ==================== 上传（裁剪流程） ====================

// 选择文件后全部进入裁剪队列
function onFilesPicked(event: Event): void {
  const input = event.target as HTMLInputElement
  const allowedMIMEs = new Set(props.allowedMimes.map((mime) => mime.toLowerCase()))
  const files = Array.from(input.files ?? []).filter((file) => allowedMIMEs.has(file.type.toLowerCase()))
  input.value = ''
  if (!files.length) return
  cropQueue.value = [...cropQueue.value, ...files]
}

// 裁剪确认（跳过也走这里，直接保留原图）：放上画布当前视角中心
function onCropConfirm(blob: Blob): void {
  cropQueue.value = cropQueue.value.slice(1)
  void addUploadedImage(blob)
}

// 取消裁剪：丢弃剩余队列
function onCropCancel(): void {
  cropQueue.value = []
}

defineExpose({
  placeOutput,
  placeOutputAt,
  addUploadedImage,
  getSelectedImageBlob,
  getEditRefBlobs,
  getMaskBlob,
  resetCanvas,
  clearMask,
  // 是否已有 mask 笔迹
  hasMaskStrokes,
})
</script>

<style scoped>
/* 深色圆点网格：浅色主题用暗点，dark 类下用亮点，画布背景透明透出 */
.dot-grid {
  /* 禁止浏览器接管双指手势，交由画布实现缩放与平移。 */
  touch-action: none;
  background-image: radial-gradient(circle, rgb(15 23 42 / 0.12) 1px, transparent 1px);
  background-size: 20px 20px;
}

/* 拖放期间给画布边缘提供稳定反馈，不改变图片与 Fabric 对象尺寸。 */
.drop-target-active {
  box-shadow: inset 0 0 0 2px rgb(124 58 237 / 0.45);
}

.dark .dot-grid {
  background-image: radial-gradient(circle, rgb(255 255 255 / 0.14) 1px, transparent 1px);
}

/* mask 独立画布只展示，不拦截主画布的指针事件；整层透明度避免笔迹重叠变深 */
.mask-overlay {
  @apply pointer-events-none absolute inset-0 z-[1]; /* check-ui-allow: 画布内局部堆叠 */
}

.canvas-tool-btn {
  @apply inline-flex h-8 w-8 items-center justify-center rounded-control text-gray-600 transition-colors;
  @apply hover:bg-gray-100 hover:text-gray-900;
  @apply disabled:cursor-not-allowed disabled:opacity-40;
  @apply dark:text-gray-300 dark:hover:bg-dark-700 dark:hover:text-gray-100;
}

.canvas-tool-btn-active {
  @apply bg-primary-600/10 text-primary-700 dark:text-primary-300;
}

/* 桌面端让新增画笔组带动工具条平滑扩展；窄屏保留原有逐项换行，并淡入新增控件。 */
.canvas-toolbar-extension {
  display: flex;
  min-width: 0;
  max-width: 22rem;
  max-height: 2rem;
  flex: 0 1 auto;
  align-items: center;
  gap: 0.375rem;
  overflow: hidden;
}

.canvas-toolbar-extension-enter-active,
.canvas-toolbar-extension-leave-active {
  transition:
    max-width 280ms cubic-bezier(0.22, 1, 0.36, 1),
    max-height 280ms cubic-bezier(0.22, 1, 0.36, 1),
    opacity 280ms ease,
    transform 280ms cubic-bezier(0.22, 1, 0.36, 1);
  will-change: max-width, max-height, opacity, transform;
}

.canvas-toolbar-extension-enter-from,
.canvas-toolbar-extension-leave-to {
  max-width: 0;
  max-height: 0;
  opacity: 0;
  transform: translateX(-6px) scale(0.96);
}

@media (max-width: 639px) {
  .canvas-toolbar-extension {
    display: contents;
  }

  .canvas-toolbar-extension-enter-active > *,
  .canvas-toolbar-extension-leave-active > * {
    transition:
      opacity 160ms ease,
      transform 220ms cubic-bezier(0.22, 1, 0.36, 1);
  }

  .canvas-toolbar-extension-enter-from > *,
  .canvas-toolbar-extension-leave-to > * {
    opacity: 0;
    transform: translateY(-4px) scale(0.92);
  }
}

/* 画笔粗细滑块自定义配色：accent-color 对未填充轨道的着色在浅色模式下过深；
   浅色模式用浅灰轨道 + 品牌青滑块，深色模式用暗色轨道（配色对齐项目 dark-700） */
.brush-size {
  -webkit-appearance: none;
  appearance: none;
  background: transparent;
}

.brush-size::-webkit-slider-runnable-track {
  height: 6px;
  border-radius: 9999px;
  background: rgb(209 213 219);
}

.dark .brush-size::-webkit-slider-runnable-track {
  background: rgb(41 41 46);
}

.brush-size::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  margin-top: -5px;
  height: 16px;
  width: 16px;
  border: none;
  border-radius: 9999px;
  background: rgb(0 210 255);
}

.brush-size::-moz-range-track {
  height: 6px;
  border-radius: 9999px;
  background: rgb(209 213 219);
}

.dark .brush-size::-moz-range-track {
  background: rgb(41 41 46);
}

.brush-size::-moz-range-thumb {
  height: 16px;
  width: 16px;
  border: none;
  border-radius: 9999px;
  background: rgb(0 210 255);
}

@media (prefers-reduced-motion: reduce) {
  .canvas-toolbar-extension-enter-active,
  .canvas-toolbar-extension-leave-active,
  .canvas-toolbar-extension-enter-active > *,
  .canvas-toolbar-extension-leave-active > * {
    transition-duration: 1ms;
  }
}
</style>
