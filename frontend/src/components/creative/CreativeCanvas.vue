<template>
  <div
    ref="containerRef"
    class="dot-grid relative h-full w-full overflow-hidden"
    :class="dropTargetActive && 'drop-target-active'"
  >
    <canvas ref="canvasElRef"></canvas>
    <!-- Composite mask strokes on a separate layer so shared opacity is applied after overlaps, while pointer input continues to reach Fabric. -->
    <canvas ref="maskCanvasElRef" class="mask-overlay"></canvas>
    <!-- Keys separate the centered play control from the corner stop control so one fades out in place while the other appears. -->
    <MotionTransition name="canvas-gif-toggle">
      <button
        v-if="gifControlPosition"
        :key="gifControlState === 'stop' ? 'corner' : 'center'"
        type="button"
        class="canvas-gif-toggle"
        :class="gifControlState === 'stop' && 'canvas-gif-toggle-compact'"
        :style="gifControlPosition"
        :title="gifControlLabel"
        :aria-label="gifControlLabel"
        :aria-busy="gifControlState === 'loading'"
        @pointerdown.stop
        @click.stop="toggleSelectedGIF"
      >
        <span v-if="gifControlState === 'loading'" class="spinner" aria-hidden="true"></span>
        <Icon
          v-else-if="gifControlState === 'play'"
          name="play"
          size="lg"
          fill="currentColor"
          class="canvas-gif-play-icon"
          :animate-on-hover="false"
        />
        <Icon v-else name="square" size="xs" fill="currentColor" :animate-on-hover="false" />
      </button>
    </MotionTransition>
    <div
      v-if="dropTargetActive"
      class="pointer-events-none absolute inset-2 z-[2] rounded-surface border-2 border-dashed border-primary-500/70 bg-primary-500/5"
    >
      <!-- check-ui-allow: Canvas-local stacking. -->
    </div>

    <div
      class="canvas-toolbar canvas-island absolute top-3 z-10 flex max-sm:w-fit max-sm:max-w-[calc(100%-8rem)] max-sm:flex-wrap max-sm:justify-center -translate-x-1/2 items-center gap-1 rounded-full p-1"
      :class="isInpaint && 'max-sm:rounded-dialog'"
    >
      <button
        type="button"
        class="canvas-tool-btn"
        :title="t('creative.panel.uploadSource')"
        @click="fileInputRef?.click()"
      >
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
      <button
        type="button"
        class="canvas-tool-btn"
        :disabled="!selectedImage"
        :title="t('creative.canvas.downloadSelected')"
        @click="downloadSelected"
      >
        <Icon name="download" size="sm" />
      </button>

      <span class="canvas-tool-divider"></span>
      <button
        type="button"
        class="canvas-tool-btn"
        :class="boxSelectMode && 'canvas-tool-btn-active'"
        :title="t('creative.canvas.boxSelect')"
        @click="setBoxSelectMode(!boxSelectMode)"
      >
        <Icon name="selection" size="sm" class="h-4 w-4" stroke-dasharray="3 2" />
      </button>

      <MotionTransition name="canvas-toolbar-extension">
        <div v-if="isInpaint" class="canvas-toolbar-extension">
          <span class="canvas-tool-divider flex-none"></span>
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :class="painting && 'canvas-tool-btn-active'"
            :title="
              painting ? t('creative.canvas.paintToggleOff') : t('creative.canvas.paintToggleOn')
            "
            @click="togglePainting"
          >
            <Icon name="edit" size="sm" />
          </button>
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :disabled="!hasMaskStrokes"
            :title="t('creative.canvas.clearMask')"
            @click="clearMask"
          >
            <Icon name="eraser" size="sm" />
          </button>
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :disabled="!canUndoMask"
            :title="t('creative.canvas.undoMask')"
            @click="undoMask"
          >
            <Icon name="undo" size="sm" class="h-4 w-4" />
          </button>
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
            <span class="w-6 text-center text-xs tabular-nums text-gray-500 dark:text-dark-400">{{
              brushSize
            }}</span>
          </div>
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :class="brushShape === 'round' && 'canvas-tool-btn-active'"
            :title="t('creative.canvas.shapeRound')"
            @click="setBrushShape('round')"
          >
            <Icon name="circle" size="sm" class="h-4 w-4" />
          </button>
          <button
            type="button"
            class="canvas-tool-btn flex-none"
            :class="brushShape === 'square' && 'canvas-tool-btn-active'"
            :title="t('creative.canvas.shapeSquare')"
            @click="setBrushShape('square')"
          >
            <Icon name="square" size="sm" class="h-4 w-4" />
          </button>
        </div>
      </MotionTransition>

      <span class="canvas-tool-divider"></span>
      <button
        type="button"
        class="canvas-tool-btn"
        :disabled="selectedObjectCount === 0"
        :title="t('creative.canvas.removeSelected')"
        @click="removeSelected"
      >
        <Icon name="trash" size="sm" />
      </button>
    </div>

    <CropperModal
      :show="cropQueue.length > 0"
      :blob="cropQueue[0] ?? null"
      @confirm="onCropConfirm"
      @skip="onCropConfirm"
      @cancel="onCropCancel"
    />
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { saveAs } from 'file-saver'
import {
  Canvas,
  FabricImage,
  InteractiveFabricObject,
  PencilBrush,
  Point,
  Rect,
  StaticCanvas,
  Text as FabricText,
  type FabricObject,
  type TMat2D,
} from 'fabric'
import { SquarePencilBrush } from './SquarePencilBrush'
import type { GIFPlayback } from './gifPlayback'
import Icon from '@/components/icons/Icon.vue'
import CropperModal from './CropperModal.vue'
import type { CreativeOperation } from '@/api/creative'
import type { CreativeCanvasGuide } from '@/composables/useCreativeStudio'
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

/**
 * CreativeCanvas.vue owns the image workspace, Fabric interactions, mask
 * drawing, and browser scene recovery. Image bytes live in IndexedDB while
 * snapshots retain asset keys, so restoring a scene rebuilds temporary URLs and
 * skips unavailable images. Uploaded sources are saved before their scene
 * references, and output placement waits for initial restoration and earlier
 * queued outputs before choosing positions.
 *
 * Mask strokes use a separate canvas whose viewport follows the main scene.
 * Their uniform layer opacity keeps overlapping strokes consistent, while
 * export temporarily restores scene coordinates and converts painted regions
 * into transparent PNG pixels. Scene revisions serialize snapshot writes,
 * restoration generations reject stale recovery work, and unmount removes
 * listeners and disposes both canvases. These responsibilities share the same
 * image identity and lifetime state. Selected GIFs replace their poster with a
 * decoded frame canvas during playback, preserving Fabric transforms and scene
 * stacking. Playback ends on deselection, removal, page hiding, or disposal.
 */

/**
 * Props supplies the operation and upload formats accepted by the creative
 * canvas.
 */
interface Props {
  /**
   * The operation enables inpaint tools and determines whether selection supplies
   * edit references or an inpaint anchor.
   */
  operation: CreativeOperation
  /**
   * Allowed MIME types come from server capabilities; the file picker defaults to
   * PNG, JPEG, and WebP when omitted.
   */
  allowedMimes?: string[]
}

/**
 * Emits forwards canvas storage and image-processing failures to the studio
 * status line.
 */
interface Emits {
  /**
   * The error event forwards a localized storage or image-processing failure to
   * the studio. The parent displays that message in the composer status line for
   * the current canvas operation.
   */
  (e: 'error', message: string): void
}

/**
 * props, emit, and t connect server-supplied canvas options and localized
 * failure feedback to the parent studio.
 */
const props = withDefaults(defineProps<Props>(), {
  allowedMimes: () => ['image/png', 'image/jpeg', 'image/webp'],
})
const emit = defineEmits<Emits>()
const { t } = useI18n()

/**
 * SCENE_KEY and SCENE_SAVE_DEBOUNCE identify the stored canvas snapshot and
 * delay coalesced scene writes.
 */
const SCENE_KEY = 'creative:canvas'
const SCENE_SAVE_DEBOUNCE = 1000
/**
 * MIN_ZOOM, MAX_ZOOM, and ZOOM_WHEEL_FACTOR bound gesture scaling and determine
 * wheel pinch sensitivity.
 */
const MIN_ZOOM = 0.2
const MAX_ZOOM = 3
const ZOOM_WHEEL_FACTOR = 0.995
/**
 * WHEEL_LINE_HEIGHT and wheel mode constants convert browser line or page
 * scrolling into viewport pixels.
 */
const WHEEL_LINE_HEIGHT = 16
const WHEEL_DELTA_MODE_LINE = 1
const WHEEL_DELTA_MODE_PAGE = 2
/**
 * PLACE_GAP, PLACE_WRAP_X, and PLACE_SCALE determine displayed image spacing,
 * automatic row wrapping, and source-independent scaling.
 */
const PLACE_GAP = 40
const PLACE_WRAP_X = 2200
const PLACE_SCALE = 0.25
/**
 * ASSET_PROTOCOL marks snapshot image references whose original bytes must be
 * restored from the local asset store.
 */
const ASSET_PROTOCOL = 'asset://'
/**
 * SELECTION_COLOR and SELECTION_FILL give object borders and selection
 * rectangles the same canvas interaction color.
 */
const SELECTION_COLOR = '#12A7E8'
const SELECTION_FILL = 'rgba(18, 167, 232, 0.08)'
/**
 * Apply selection defaults before either new images or restored objects are
 * constructed so both use the same interaction appearance.
 */
Object.assign(InteractiveFabricObject.ownDefaults, {
  borderColor: SELECTION_COLOR,
  borderScaleFactor: 1.5,
  cornerColor: '#ffffff',
  cornerStrokeColor: SELECTION_COLOR,
  cornerStyle: 'circle',
  cornerSize: 10,
  transparentCorners: false,
  padding: 0,
})
/**
 * MASK_COLOR, MASK_TINT, MASK_OPACITY, and MASK_PREVIEW_TINT separate exported
 * stroke pixels from the translucent drawing preview.
 */
const MASK_COLOR = '#ffffff'
const MASK_TINT = '#a855f7'
const MASK_OPACITY = 0.55
const MASK_PREVIEW_TINT = `rgba(168, 85, 247, ${MASK_OPACITY})`
/**
 * ANCHOR_OUTLINE_KIND, REF_OUTLINE_KIND, and RESOLUTION_TAG_KIND identify
 * runtime helpers excluded from persistent scene snapshots.
 */
const ANCHOR_OUTLINE_KIND = 'anchor-outline'
const REF_OUTLINE_KIND = 'ref-outline'
const RESOLUTION_TAG_KIND = 'resolution-tag'
/**
 * ANCHOR_OUTLINE_STYLE supplies the shared dashed appearance used for inpaint
 * anchors and selected edit references.
 */
const ANCHOR_OUTLINE_STYLE = {
  fill: 'transparent',
  stroke: 'rgba(0, 210, 255, 0.7)',
  strokeWidth: 1.5,
  strokeDashArray: [6, 4],
}
/**
 * Resolution tag padding and offset constants position its background and text
 * inside the selected image bounds.
 */
const RESOLUTION_TAG_PADDING_X = 5
const RESOLUTION_TAG_PADDING_Y = 3
const RESOLUTION_TAG_OFFSET = 8
/**
 * RESOLUTION_TAG_TEXT_STYLE and RESOLUTION_TAG_BACKGROUND_STYLE create paired
 * noninteractive label objects with left-top scene origins.
 */
const RESOLUTION_TAG_TEXT_STYLE = {
  fill: '#ffffff',
  fontSize: 11,
  fontWeight: '500',
  originX: 'left' as const,
  originY: 'top' as const,
  selectable: false,
  evented: false,
}
const RESOLUTION_TAG_BACKGROUND_STYLE = {
  fill: 'rgba(8, 12, 20, 0.78)',
  rx: 6,
  ry: 6,
  originX: 'left' as const,
  originY: 'top' as const,
  selectable: false,
  evented: false,
}
/**
 * GRID_SPACING and GRID_MIN_PITCH control scene grid density while
 * zoom-dependent layers blend between spacing levels.
 */
const GRID_SPACING = 20
const GRID_MIN_PITCH = 12

/**
 * syncDotGrid positions the CSS dot grid and mask viewport from the main canvas
 * transform. Grid pitch doubles or halves at zoom thresholds, with a smoothstep
 * fade between the fine and coarse layers. The coarse layer supplies stable
 * dots while the fine layer approaches the next density. Copying the same
 * viewport to the separate mask canvas keeps painted paths aligned with images,
 * and an explicit render refreshes its manually managed display.
 */
function syncDotGrid(): void {
  const el = containerRef.value
  if (!el || !canvas) return
  const vpt = canvas.viewportTransform
  const rawPitch = GRID_SPACING * canvas.getZoom()
  /**
   * Choose a power-of-two pitch so visible spacing stays within the configured
   * interval while coarse and fine grid layers exchange density.
   */
  const pitch = rawPitch * 2 ** Math.ceil(Math.log2(GRID_MIN_PITCH / rawPitch))
  const progress = Math.min(1, Math.max(0, (pitch - GRID_MIN_PITCH) / GRID_MIN_PITCH))
  /**
   * Smoothstep softens the fine-dot transition at both density limits.
   */
  const fineOpacity = progress * progress * (3 - 2 * progress)
  const x = vpt[4]
  const y = vpt[5]
  el.style.setProperty('--grid-fine-opacity', fineOpacity.toFixed(3))
  el.style.backgroundSize = `${pitch * 2}px ${pitch * 2}px`
  /**
   * Gradient dots sit at tile centers; offset the coarse layer to the scene
   * origin and use three fine layers to fill adjacent positions.
   */
  el.style.backgroundPosition = [
    `${x - pitch}px ${y - pitch}px`,
    `${x}px ${y - pitch}px`,
    `${x - pitch}px ${y}px`,
    `${x}px ${y}px`,
  ].join(', ')
  if (maskCanvas) {
    /**
     * Keep the overlay on the same scene transform, then request rendering because
     * its automatic add/remove rendering is disabled.
     */
    maskCanvas.setViewportTransform([...vpt] as TMat2D)
    maskCanvas.requestRenderAll()
  }
}

/**
 * ObjectWithData exposes the custom metadata attached to Fabric scene objects.
 */
type ObjectWithData = {
  data?: Record<string, unknown> // custom Fabric object metadata
}

/**
 * objectData returns the metadata attached to a Fabric object. An absent data
 * property produces a new empty object; present metadata remains shared with
 * its owning scene object.
 */
function objectData(object: FabricObject): Record<string, unknown> {
  return (object as unknown as ObjectWithData).data ?? {}
}

/**
 * setObjectData attaches shared custom metadata to the supplied Fabric scene
 * object.
 */
function setObjectData(object: FabricObject, data: Record<string, unknown>): void {
  const target = object as unknown as ObjectWithData
  target.data = data
}

/**
 * isImageSnapshotObject recognizes serialized Fabric image types irrespective
 * of letter case.
 */
function isImageSnapshotObject(object: Record<string, unknown>): boolean {
  return typeof object.type === 'string' && object.type.toLowerCase() === 'image'
}

/**
 * imageResolution returns positive original pixel dimensions for a Fabric
 * image. Stored metadata takes precedence; older images use their element
 * dimensions, and unavailable or nonpositive sizes produce null.
 */
function imageResolution(image: FabricImage): ImageResolution | null {
  /**
   * Round stored dimensions before accepting positive image pixel sizes.
   */
  const stored = objectData(image).resolution
  if (stored && typeof stored === 'object') {
    const value = stored as {
      width?: unknown // image width pixels
      height?: unknown // image height pixels
    }
    const width = typeof value.width === 'number' ? Math.round(value.width) : 0
    const height = typeof value.height === 'number' ? Math.round(value.height) : 0
    if (width > 0 && height > 0) return { width, height }
  }
  const element = image.getElement() as Partial<HTMLImageElement>
  const width = Math.round(Number(element.naturalWidth || element.width || 0))
  const height = Math.round(Number(element.naturalHeight || element.height || 0))
  return width > 0 && height > 0 ? { width, height } : null
}

/**
 * removeResolutionTag removes both runtime label objects and clears their
 * shared reference.
 */
function removeResolutionTag(): void {
  if (canvas && resolutionTag) {
    canvas.remove(resolutionTag.background, resolutionTag.text)
  }
  resolutionTag = null
}

/**
 * refreshResolutionTag displays original pixel dimensions beside a selected
 * image. Missing images or dimensions remove the tag; changed labels replace
 * its objects, and refreshed scene coordinates position it even inside a
 * grouped selection.
 */
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
  const label = `${objectData(image).animatedGIF ? 'GIF · ' : ''}${resolution.width} × ${resolution.height}`
  if (!resolutionTag || resolutionTag.image !== image || resolutionTag.text.text !== label) {
    removeResolutionTag()
    /**
     * Canvas text does not inherit page CSS, so resolve the body font before
     * constructing the image resolution label.
     */
    const fontFamily = getComputedStyle(document.body).fontFamily || 'sans-serif'
    const text = new FabricText(label, { ...RESOLUTION_TAG_TEXT_STYLE, fontFamily })
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
  resolutionTag.text.set({
    left: left + RESOLUTION_TAG_PADDING_X,
    top: top + RESOLUTION_TAG_PADDING_Y,
  })
  resolutionTag.background.setCoords()
  resolutionTag.text.setCoords()
  canvas.requestRenderAll()
}

/**
 * getMaskPaths collects marked mask objects from the main and overlay canvases.
 * The result is a new array of shared Fabric objects; an unavailable main
 * canvas yields an empty list.
 */
function getMaskPaths(): FabricObject[] {
  if (!canvas) return []
  const paths = canvas.getObjects().filter((object) => objectData(object).kind === 'mask')
  if (maskCanvas) {
    paths.push(...maskCanvas.getObjects().filter((object) => objectData(object).kind === 'mask'))
  }
  return paths
}

/**
 * addMaskPath adds a stroke to the mask overlay and applies its shared clip.
 * When the overlay is unavailable it uses the main canvas, allowing callers to
 * restore strokes through one entry point.
 */
function addMaskPath(path: FabricObject): void {
  if (maskCanvas) {
    maskCanvas.add(path)
    if (maskClipRect) path.clipPath = maskClipRect
    maskCanvas.requestRenderAll()
  } else {
    canvas?.add(path)
  }
}

/**
 * removeMaskPath removes a stroke from its owning canvas and refreshes the
 * overlay. Paths absent from the overlay are passed to the main canvas,
 * including legacy strokes restored there.
 */
function removeMaskPath(path: FabricObject): void {
  if (maskCanvas?.getObjects().includes(path)) {
    maskCanvas.remove(path)
    maskCanvas.requestRenderAll()
  } else {
    canvas?.remove(path)
  }
}

/**
 * BrushShape selects the round or square mask stroke implementation.
 */
type BrushShape = 'round' | 'square'
/**
 * ImageResolution stores an image's original positive dimensions in pixels.
 */
type ImageResolution = {
  width: number // image width pixels
  height: number // image height pixels
}
/**
 * ResolutionTag groups image, background, and text for one runtime resolution
 * label. Removing or replacing the selected image removes both label objects
 * together from the scene.
 */
type ResolutionTag = {
  image: FabricImage // associated Fabric image
  background: Rect // runtime tag background
  text: FabricText // runtime tag text
}
/**
 * TouchPoint carries client-space coordinates used by the two-finger gesture
 * calculation.
 */
type TouchPoint = {
  clientX: number // client horizontal pixels
  clientY: number // client vertical pixels
}
/**
 * PinchGesture captures startDistance and startZoom with startScenePoint and
 * startViewportTransform at touch start. Later updates scale that transform
 * while keeping the captured scene point beneath the current midpoint.
 */
type PinchGesture = {
  startDistance: number // initial touch separation
  startZoom: number // initial canvas zoom
  startScenePoint: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  } // initial midpoint scene coordinates
  startViewportTransform: TMat2D // copied initial viewport transform
}

/**
 * containerRef, canvasElRef, maskCanvasElRef, and fileInputRef connect mounted
 * elements to Fabric setup and image selection.
 */
const containerRef = ref<HTMLDivElement | null>(null)
const canvasElRef = ref<HTMLCanvasElement | null>(null)
const maskCanvasElRef = ref<HTMLCanvasElement | null>(null)
const fileInputRef = ref<HTMLInputElement | null>(null)
/**
 * cropQueue retains upload Blobs until each image is confirmed, skipped, or the
 * remaining crop session is cancelled.
 */
const cropQueue = ref<Blob[]>([])
/**
 * painting and hasMaskStrokes expose the drawing session and current stroke
 * availability to toolbar controls.
 */
const painting = ref(false)
const hasMaskStrokes = ref(false)
/**
 * brushSize and brushShape configure the next mask stroke and replace the
 * active brush when needed.
 */
const brushSize = ref(28)
const brushShape = ref<BrushShape>('round')
/**
 * selectedImage tracks Fabric selection; drawing can clear that selection while
 * inpaintAnchor retains the target image.
 */
const selectedImage = shallowRef<FabricObject | null>(null)
/**
 * GIF playback belongs to the selected image. The generation invalidates pending
 * asset reads, while the poster restores the persistent image before releasing
 * its temporary frame canvas. Control position follows Fabric's rendered scene.
 */
const gifPlaybackTarget = shallowRef<FabricImage | null>(null)
const gifPlaybackLoading = ref(false)
const gifControlPosition = ref<{
  left: string // horizontal viewport CSS pixels
  top: string // vertical viewport CSS pixels
} | null>(null)
let gifPlayback: GIFPlayback | null = null
let gifPoster: ReturnType<FabricImage['getElement']> | null = null
let gifPlaybackGeneration = 0
/**
 * gifControlState selects the playback control's icon and placement. Loading
 * keeps the spinner at the clicked center; only a published player moves the
 * compact stop control to the image's upper-left corner.
 */
const gifControlState = computed<'play' | 'loading' | 'stop'>(() => {
  if (!gifPlaybackTarget.value) return 'play'
  return gifPlaybackLoading.value ? 'loading' : 'stop'
})
/** gifControlLabel names the action a click performs: start, or cancel and stop. */
const gifControlLabel = computed(() =>
  t(gifControlState.value === 'play' ? 'creative.canvas.playGIF' : 'creative.canvas.stopGIF'),
)
/**
 * selectedObjectCount enables removal for grouped selections as well as a
 * single selected image object.
 */
const selectedObjectCount = ref(0)
/**
 * dropTargetActive controls the canvas drop feedback while nested drag events
 * update their shared depth count.
 */
const dropTargetActive = ref(false)
/**
 * inpaintAnchor retains the paint target across Fabric selection clearing and
 * changes when another image is selected.
 */
const inpaintAnchor = shallowRef<FabricObject | null>(null)
/**
 * paintSuspended records manual drawing suspension so the user can pan or
 * choose another image before resuming.
 */
const paintSuspended = ref(false)
/**
 * editRefs retains shared selected image objects for reference export even
 * after ordinary Fabric selection is cleared.
 */
const editRefs = shallowRef<FabricObject[]>([])
/**
 * boxSelectMode assigns blank-space dragging to Fabric selection while the
 * default interaction pans the viewport.
 */
const boxSelectMode = ref(false)
/**
 * imageCount and sceneReady let the parent distinguish an empty restored scene
 * from one still loading.
 */
const imageCount = ref(0)
const sceneReady = ref(false)
/**
 * anchorOutline, refOutlines, and resolutionTag own temporary scene helpers
 * that are removed when their selected images change.
 */
let anchorOutline: Rect | null = null
const refOutlines = new Map<FabricObject, Rect>()
let resolutionTag: ResolutionTag | null = null

/**
 * canvas and maskCanvas share dimensions and viewport transforms, but the
 * overlay applies opacity once to all mask strokes.
 */
let canvas: Canvas | null = null
let maskCanvas: StaticCanvas | null = null
/**
 * resizeObserver owns container sizing notifications and is disconnected before
 * the Fabric instances are disposed.
 */
let resizeObserver: ResizeObserver | null = null
/**
 * sceneSaveTimer coalesces scene writes and is cleared before explicit flushes
 * or component disposal.
 */
let sceneSaveTimer: ReturnType<typeof setTimeout> | null = null
/**
 * sceneRestoreInProgress suppresses mutation-triggered saves while Fabric loads
 * a snapshot, protecting stored images from temporary emptiness.
 */
let sceneRestoreInProgress = false
/**
 * sceneRestoreGeneration invalidates delayed recovery checks after reset or
 * unmount, tying results to their captured scene lifetime.
 */
let sceneRestoreGeneration = 0
/**
 * sceneDisposed blocks new persistence and queued placement after unmount;
 * individual asynchronous functions retain their own checks.
 */
let sceneDisposed = false
/**
 * sceneDirty and sceneRevision track user mutations so an older successful
 * write cannot mark a newer change saved.
 */
let sceneDirty = false
let sceneRevision = 0
/**
 * sceneWriteChain serializes snapshot requests; rejected writes are logged
 * while later queued snapshots can still run.
 */
let sceneWriteChain: Promise<void> = Promise.resolve()
/**
 * sceneRestorePromise delays external placements until initial recovery
 * finishes, preventing early additions from being replaced by loading.
 */
let sceneRestorePromise: Promise<void> = Promise.resolve()
/**
 * dragDepth counts nested drag entries so crossing child elements does not hide
 * the active drop target.
 */
let dragDepth = 0
/**
 * isPanning, lastClientX, and lastClientY retain the pointer baseline consumed
 * by each subsequent canvas drag event.
 */
let isPanning = false
let lastClientX = 0
let lastClientY = 0
/**
 * pinchGesture captures the transform and scene anchor once, then current touch
 * distances determine later viewport updates.
 */
let pinchGesture: PinchGesture | null = null
/**
 * panAnimFrame identifies the scheduled viewport animation callback, cancelled
 * on replacement animations and component teardown.
 */
let panAnimFrame: number | null = null
/**
 * runtimeBlobs caches original image bytes by asset key so exports can reuse
 * full-resolution sources after display scaling.
 */
const runtimeBlobs = new Map<string, Blob>()
/**
 * lastPlaced stores the displayed edges of the latest insertion for sequential
 * automatic placement and row wrapping.
 */
let lastPlaced: {
  right: number // displayed right edge
  top: number // displayed top edge
  bottom: number // displayed bottom edge
} | null = null
/**
 * outputPlacementQueue orders decoded output insertions so each automatic
 * position observes the preceding image placement.
 */
let outputPlacementQueue: Promise<void> = Promise.resolve()
/**
 * MaskUndoEntry retains shared stroke identities for one reversible operation.
 * An add entry removes its path on undo; a clear entry restores every path in
 * its retained array.
 */
type MaskUndoEntry =
  | {
      type: 'add' // undo operation kind
      path: FabricObject // added shared stroke
    }
  | {
      type: 'clear' // undo operation kind
      paths: FabricObject[] // cleared shared strokes
    }
/**
 * maskUndoStack retains shared stroke objects for undo and clears when the
 * painting draft is discarded.
 */
const maskUndoStack: MaskUndoEntry[] = []
/**
 * maskClipRect is shared by active strokes and replaced when the inpaint target
 * changes scene bounds.
 */
let maskClipRect: Rect | null = null
/**
 * lockedStates records each object's original interaction flags before painting
 * disables them, then unlockAllObjects restores those values.
 */
let lockedStates: Map<
  FabricObject,
  {
    selectable: boolean // saved selection eligibility
    evented: boolean // saved event eligibility
  }
> | null = null

/**
 * Create the main and mask canvases before registering interactions and
 * starting recovery, so handlers and restored strokes share initialized
 * resources.
 */
onMounted(() => {
  const element = canvasElRef.value
  const container = containerRef.value
  if (!element || !container) return
  canvas = new Canvas(element, {
    width: Math.max(container.clientWidth, 320),
    height: Math.max(container.clientHeight, 320),
    preserveObjectStacking: true,
    backgroundColor: '',
    selection: false,
    selectionColor: SELECTION_FILL,
    selectionBorderColor: SELECTION_COLOR,
    selectionLineWidth: 1,
    defaultCursor: 'grab',
  })
  /**
   * Fabric enables native dragging on its interaction canvas for text transfers.
   * Disable that drag source so browser drag startup cannot interrupt viewport
   * panning; image imports still arrive through the container's drop handlers.
   */
  canvas.upperCanvasEl.draggable = false
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
  container.addEventListener('contextmenu', suppressContextMenu)
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

/**
 * Remove browser listeners and stop scheduled work before disposing drawing
 * resources; flush only a restored, dirty scene to protect the stored snapshot.
 */
onBeforeUnmount(() => {
  stopGIFPlayback()
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
  /**
   * An incomplete restore leaves the live scene temporarily empty, so teardown
   * must not persist that partial state over saved image references.
   */
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

/**
 * fitToContainer updates both canvas dimensions from the visible container
 * size. It ignores missing or zero-sized containers, preserving the current
 * drawing surface while layout is unavailable or hidden.
 */
function fitToContainer(): void {
  if (!canvas || !containerRef.value) return
  const { clientWidth, clientHeight } = containerRef.value
  if (clientWidth <= 0 || clientHeight <= 0) return
  canvas.setDimensions({ width: Math.floor(clientWidth), height: Math.floor(clientHeight) })
  maskCanvas?.setDimensions({ width: Math.floor(clientWidth), height: Math.floor(clientHeight) })
}

/**
 * suppressContextMenu prevents the browser menu during alternate-button canvas
 * panning.
 */
function suppressContextMenu(event: Event): void {
  event.preventDefault()
}

/**
 * isCreativeDropCandidate reports whether a drag may carry files or a history
 * output. Browsers can hide file entries before dropping, so the Files type
 * also enables feedback before content validation.
 */
function isCreativeDropCandidate(dataTransfer: DataTransfer | null): boolean {
  if (!dataTransfer) return false
  const types = Array.from(dataTransfer.types ?? [])
  return (
    types.includes(CREATIVE_OUTPUT_DRAG_MIME) ||
    types.includes('Files') ||
    dataTransfer.files.length > 0
  )
}

/**
 * onDragEnter counts an entry from a recognized canvas drop candidate. It keeps
 * feedback visible across nested child entries; later dragleave events
 * decrement the same count before hiding the target.
 */
function onDragEnter(event: DragEvent): void {
  if (!isCreativeDropCandidate(event.dataTransfer)) return
  dragDepth += 1
  dropTargetActive.value = true
}

/**
 * onDragOver accepts candidate drops and selects copy feedback for the browser.
 * It prevents surrounding drop handlers from consuming the event; file and
 * payload validation happens when the drop arrives.
 */
function onDragOver(event: DragEvent): void {
  if (!isCreativeDropCandidate(event.dataTransfer)) return
  event.preventDefault()
  event.stopPropagation()
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy'
  dropTargetActive.value = true
}

/**
 * onDragLeave decreases nested drag depth and hides feedback after the outer
 * leave. The count is clamped to zero because browser child transitions can
 * produce leave events after feedback has already cleared.
 */
function onDragLeave(event: DragEvent): void {
  if (!dropTargetActive.value && !isCreativeDropCandidate(event.dataTransfer)) return
  dragDepth = Math.max(0, dragDepth - 1)
  if (dragDepth === 0) dropTargetActive.value = false
}

/**
 * scenePointForDrop converts a browser drop event through Fabric scene
 * coordinates. A missing canvas returns null, allowing the drop handler to stop
 * before launching image loading or asset lookup.
 */
function scenePointForDrop(event: DragEvent): {
  x: number // horizontal scene coordinate
  y: number // vertical scene coordinate
} | null {
  if (!canvas) return null
  return canvas.getScenePoint(event)
}

/**
 * onDrop routes history references or supported external images to canvas
 * placement. A nonempty private payload takes precedence over files and is
 * validated before local lookup; unsupported files produce an error message
 * after drop feedback is cleared.
 */
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

/**
 * bindCanvasEvents connects Fabric pointer, selection, object, and stroke
 * events to component state. Blank primary drags pan unless box selection owns
 * them, while alternate mouse buttons remain available during painting.
 * Finished strokes move to the separate overlay and retain their undo identity.
 * Selection clearing during a brush stroke keeps the inpaint anchor, whereas
 * ordinary deselection clears it. Object mutations update guidance and schedule
 * snapshots, with restoration suppression handled by scheduleSceneSave.
 */
function bindCanvasEvents(): void {
  if (!canvas) return
  canvas.on('after:render', syncGIFControlPosition)
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
      const next = Math.min(
        MAX_ZOOM,
        Math.max(MIN_ZOOM, canvas.getZoom() * ZOOM_WHEEL_FACTOR ** wheel.deltaY),
      )
      canvas.zoomToPoint(new Point(wheel.offsetX, wheel.offsetY), next)
    } else {
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
  /**
   * Completed brush paths move to the independent overlay with opaque colors,
   * where applying opacity once prevents overlaps from becoming darker.
   */
  canvas.on('path:created', (event) => {
    const path = event.path as FabricObject | undefined
    if (path && painting.value) {
      setObjectData(path, { kind: 'mask' })
      path.set({ selectable: false, evented: false })
      path.set({
        fill: path.fill ? MASK_TINT : path.fill,
        stroke: path.stroke ? MASK_TINT : path.stroke,
      })
      if (maskClipRect) {
        path.clipPath = maskClipRect
      }
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
    syncImageCount()
    scheduleSceneSave()
  })
  canvas.on('object:modified', () => {
    refreshRefOutlines()
    refreshResolutionTag(selectedImage.value instanceof FabricImage ? selectedImage.value : null)
    scheduleSceneSave()
  })
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
    if (event.target === gifPlaybackTarget.value) stopGIFPlayback()
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
    syncImageCount()
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
  /**
   * Fabric clears selection when brush drawing begins; keep the inpaint anchor
   * during painting so the active mask target survives that event.
   */
  canvas.on('selection:cleared', () => {
    selectedObjectCount.value = 0
    selectedImage.value = null
    if (!painting.value) {
      removeResolutionTag()
      inpaintAnchor.value = null
    } else {
      refreshResolutionTag(inpaintAnchor.value instanceof FabricImage ? inpaintAnchor.value : null)
    }
  })
}

/**
 * bindTouchGestures registers captured, nonpassive touch handlers for the
 * canvas container. Capture allows a two-finger gesture to stop Fabric's
 * single-pointer processing; unbindTouchGestures removes these registrations
 * during component cleanup.
 */
function bindTouchGestures(container: HTMLDivElement): void {
  container.addEventListener('touchstart', onCanvasTouchStart, { capture: true, passive: false })
  container.addEventListener('touchmove', onCanvasTouchMove, { capture: true, passive: false })
  container.addEventListener('touchend', onCanvasTouchEnd, { capture: true, passive: false })
  container.addEventListener('touchcancel', onCanvasTouchCancel, { capture: true, passive: false })
}

/**
 * unbindTouchGestures removes all captured gesture handlers from the supplied
 * container. A missing container is accepted during teardown, and removal uses
 * the same capture flag as registration.
 */
function unbindTouchGestures(container: HTMLDivElement | null): void {
  if (!container) return
  container.removeEventListener('touchstart', onCanvasTouchStart, true)
  container.removeEventListener('touchmove', onCanvasTouchMove, true)
  container.removeEventListener('touchend', onCanvasTouchEnd, true)
  container.removeEventListener('touchcancel', onCanvasTouchCancel, true)
}

/**
 * touchPoints returns the first two active touch positions as client
 * coordinates. Fewer than two usable entries return null so pinch handlers can
 * leave single-pointer processing to the canvas.
 */
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

/**
 * midpointAndDistance calculates the client-space center and separation of two
 * touches.
 */
function midpointAndDistance(points: [TouchPoint, TouchPoint]): {
  midpoint: TouchPoint // two-touch client midpoint
  distance: number // two-touch separation pixels
} {
  const [first, second] = points
  return {
    midpoint: {
      clientX: (first.clientX + second.clientX) / 2,
      clientY: (first.clientY + second.clientY) / 2,
    },
    distance: Math.hypot(second.clientX - first.clientX, second.clientY - first.clientY),
  }
}

/**
 * viewportPointForClient converts client coordinates into the drawing viewport.
 * It delegates to Fabric when supported, otherwise subtracts the target element
 * bounds; with no canvas it returns the supplied client coordinates.
 */
function viewportPointForClient(point: TouchPoint): {
  x: number // horizontal viewport pixels
  y: number // vertical viewport pixels
} {
  if (!canvas) return { x: point.clientX, y: point.clientY }
  const target = canvas.upperCanvasEl ?? canvasElRef.value ?? containerRef.value
  const pointerEvent = {
    type: 'touchmove',
    target,
    touches: [point],
    changedTouches: [point],
  } as unknown as TouchEvent
  const fabricCanvas = canvas as unknown as {
    getViewportPoint?: (event: TouchEvent) => {
      x: number // horizontal viewport pixels
      y: number // vertical viewport pixels
    } // optional Fabric viewport converter
  }
  if (typeof fabricCanvas.getViewportPoint === 'function') {
    return fabricCanvas.getViewportPoint(pointerEvent)
  }
  /**
   * Reduced Fabric substitutes may omit viewport conversion, so DOM bounds supply
   * the client-to-viewport offset for those environments.
   */
  const bounds = target?.getBoundingClientRect?.()
  return {
    x: point.clientX - (bounds?.left ?? 0),
    y: point.clientY - (bounds?.top ?? 0),
  }
}

/**
 * scenePointForClient converts client coordinates into the canvas scene plane.
 * It prefers Fabric's conversion and otherwise inverts the viewport transform;
 * a singular transform returns viewport coordinates because inversion is
 * unavailable.
 */
function scenePointForClient(point: TouchPoint): {
  x: number // horizontal scene coordinate
  y: number // vertical scene coordinate
} {
  if (!canvas) return { x: point.clientX, y: point.clientY }
  const target = canvas.upperCanvasEl ?? canvasElRef.value ?? containerRef.value
  const pointerEvent = {
    type: 'touchmove',
    target,
    touches: [point],
    changedTouches: [point],
  } as unknown as TouchEvent
  const fabricCanvas = canvas as unknown as {
    getScenePoint?: (event: TouchEvent) => {
      x: number // horizontal scene coordinate
      y: number // vertical scene coordinate
    } // optional Fabric scene converter
  }
  if (typeof fabricCanvas.getScenePoint === 'function') {
    return fabricCanvas.getScenePoint(pointerEvent)
  }
  /**
   * When Fabric scene conversion is unavailable, invert its affine viewport
   * transform; singular transforms retain viewport coordinates because no inverse
   * exists.
   */
  const viewport = viewportPointForClient(point)
  const [a, b, c, d, tx, ty] = canvas.viewportTransform
  const determinant = a * d - b * c
  if (!determinant) return viewport
  return {
    x: (d * (viewport.x - tx) - c * (viewport.y - ty)) / determinant,
    y: (-b * (viewport.x - tx) + a * (viewport.y - ty)) / determinant,
  }
}

/**
 * onCanvasTouchStart captures a valid two-finger gesture's transform and scene
 * anchor. It stops panning and consumes the event so subsequent pinch updates
 * use one initial zoom and midpoint reference.
 */
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
  /**
   * A captured two-finger gesture takes over movement, so clear single-pointer
   * drag state before consuming the event from Fabric.
   */
  stopPanning()
  event.preventDefault()
  event.stopPropagation()
}

/**
 * onCanvasTouchMove updates the viewport from the captured pinch state and
 * current touches. The distance ratio determines a bounded zoom, and the
 * starting linear transform is scaled by that ratio. Translation is then solved
 * so the original scene anchor follows the current midpoint. This combines
 * pinch zoom with two-finger movement without accumulating per-event rounding
 * drift, and the resulting transform updates the grid and pending scene save.
 */
function onCanvasTouchMove(event: TouchEvent): void {
  if (!canvas || !pinchGesture) return
  const points = touchPoints(event)
  if (!points) return
  const { midpoint, distance } = midpointAndDistance(points)
  if (!Number.isFinite(distance) || distance <= 0) return

  const ratio = distance / pinchGesture.startDistance
  /**
   * Clamp the gesture zoom before computing translation so the same captured
   * scene anchor stays beneath the current midpoint at either limit.
   */
  const nextZoom = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, pinchGesture.startZoom * ratio))
  const linearRatio = nextZoom / pinchGesture.startZoom
  const start = pinchGesture.startViewportTransform
  const vpt = [...start] as TMat2D
  vpt[0] = start[0] * linearRatio
  vpt[1] = start[1] * linearRatio
  vpt[2] = start[2] * linearRatio
  vpt[3] = start[3] * linearRatio
  const viewport = viewportPointForClient(midpoint)
  vpt[4] =
    viewport.x - (vpt[0] * pinchGesture.startScenePoint.x + vpt[2] * pinchGesture.startScenePoint.y)
  vpt[5] =
    viewport.y - (vpt[1] * pinchGesture.startScenePoint.x + vpt[3] * pinchGesture.startScenePoint.y)
  canvas.setViewportTransform(vpt)
  syncDotGrid()
  scheduleSceneSave()
  event.preventDefault()
  event.stopPropagation()
}

/**
 * onCanvasTouchEnd releases pinch ownership when fewer than two touches remain.
 * It also clears panning so the remaining finger cannot continue a gesture with
 * stale single-pointer coordinates.
 */
function onCanvasTouchEnd(event: TouchEvent): void {
  if (!pinchGesture || (event.touches?.length ?? 0) >= 2) return
  event.preventDefault()
  pinchGesture = null
  isPanning = false
}

/**
 * onCanvasTouchCancel clears an interrupted pinch and its panning state. It
 * consumes cancellation only while a pinch is active, leaving unrelated
 * single-pointer cancellation to the other handlers.
 */
function onCanvasTouchCancel(event: TouchEvent): void {
  if (!pinchGesture) return
  event.preventDefault()
  pinchGesture = null
  isPanning = false
}

/**
 * onPageHide stops playback and flushes the latest dirty scene snapshot.
 * Browser shutdown may interrupt asynchronous IndexedDB work, so this callback
 * starts the write before normal debounce delay.
 */
function onPageHide(): void {
  stopGIFPlayback()
  flushSceneSave()
}

/**
 * onVisibilityChange flushes scene changes when the document becomes hidden.
 * Visible transitions keep the debounce schedule, while hidden transitions
 * stop GIF playback and reuse the same best-effort persistence path as pagehide.
 */
function onVisibilityChange(): void {
  if (document.visibilityState === 'hidden') onPageHide()
}

/**
 * normalizeWheelDelta converts line and page wheel deltas to canvas pixels.
 * Invalid or zero deltas return zero; line mode uses the configured line
 * estimate and page mode uses the supplied viewport dimension.
 */
function normalizeWheelDelta(delta: number, deltaMode: number, pageSize: number): number {
  if (!Number.isFinite(delta) || delta === 0) return 0
  /**
   * Line and page deltas need viewport pixel conversion.
   */
  if (deltaMode === WHEEL_DELTA_MODE_LINE) return delta * WHEEL_LINE_HEIGHT
  if (deltaMode === WHEEL_DELTA_MODE_PAGE) return delta * pageSize
  return delta
}

/**
 * startPan records the current client position and enables the grabbing cursor.
 * It requires a live canvas and establishes the baseline consumed by later
 * Fabric mouse-move events.
 */
function startPan(event: Event): void {
  if (!canvas) return
  isPanning = true
  const client = clientPoint(event)
  lastClientX = client.x
  lastClientY = client.y
  canvas.defaultCursor = 'grabbing'
}

/**
 * isPanButton recognizes middle and secondary mouse buttons for canvas panning.
 */
function isPanButton(event: Event): boolean {
  const mouse = event as MouseEvent
  return typeof mouse.button === 'number' && (mouse.button === 1 || mouse.button === 2)
}

/**
 * stopPanning clears drag state and restores the cursor for the active tool.
 */
function stopPanning(): void {
  isPanning = false
  if (canvas) canvas.defaultCursor = boxSelectMode.value || painting.value ? 'crosshair' : 'grab'
}

/**
 * isPrimaryPointer accepts primary mouse input or at most one active touch.
 */
function isPrimaryPointer(event: Event): boolean {
  const mouse = event as MouseEvent
  if (typeof mouse.button === 'number' && mouse.button !== 0) return false
  const touch = event as TouchEvent
  if (touch.touches && touch.touches.length > 1) return false
  return true
}

/**
 * clientPoint reads client coordinates from current touches before changed
 * touches. Mouse coordinates supply the result when neither touch collection
 * provides a point, allowing one pan path to handle both input types.
 */
function clientPoint(event: Event): {
  x: number // horizontal browser client pixels
  y: number // vertical browser client pixels
} {
  const touch = event as TouchEvent
  const point = touch.touches?.[0] ?? touch.changedTouches?.[0]
  if (point) return { x: point.clientX, y: point.clientY }
  const mouse = event as MouseEvent
  return { x: mouse.clientX, y: mouse.clientY }
}

/**
 * pickImage returns the supplied target when it is a Fabric image. Missing
 * targets and other object types return null so selection handlers can clear
 * image-specific controls safely.
 */
function pickImage(target: FabricObject | undefined): FabricObject | null {
  return target instanceof FabricImage ? target : null
}

/**
 * onKeyDown handles canvas deletion, mask undo, and box-selection escape
 * shortcuts. Focused text editors retain their keyboard input, painting blocks
 * deletion, and grouped selections are unpacked before their members are
 * removed.
 */
function onKeyDown(event: KeyboardEvent): void {
  if (!canvas) return
  const target = event.target as HTMLElement | null
  if (
    target &&
    (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
  )
    return
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
  if (painting.value || (event.key !== 'Delete' && event.key !== 'Backspace')) return
  const selected = canvas.getActiveObjects()
  if (!selected.length) return
  event.preventDefault()
  /**
   * Remove the ActiveSelection wrapper before deleting its member objects,
   * because the wrapper itself is not the underlying set of scene images.
   */
  canvas.discardActiveObject()
  canvas.remove(...selected)
  canvas.requestRenderAll()
}

/**
 * isInpaint and isEdit derive the canvas interaction mode from the studio
 * operation selection.
 */
const isInpaint = computed(() => props.operation === 'inpaint')
const isEdit = computed(() => props.operation === 'edit')

/**
 * guideKey chooses missing-anchor, missing-reference, or empty-mask guidance in
 * the order shown by the composer status line.
 */
const guideKey = computed<CreativeCanvasGuide | null>(() => {
  if (isInpaint.value && !inpaintAnchor.value) return 'inpaintPick'
  if (isEdit.value && !editRefs.value.length) return 'editPick'
  if (painting.value && !hasMaskStrokes.value) return 'maskPaint'
  return null
})

/**
 * syncImageCount publishes the number of image-tagged objects in the current
 * canvas.
 */
function syncImageCount(): void {
  imageCount.value = canvas
    ? canvas.getObjects().filter((object) => objectData(object).kind === 'image').length
    : 0
}

/**
 * onImageSelected updates the selected image and its resolution tag. In inpaint
 * mode a selected image also replaces the painting anchor and clears manual
 * suspension so painting can resume.
 */
function onImageSelected(image: FabricObject | null): void {
  selectedImage.value = image
  refreshResolutionTag(image instanceof FabricImage ? image : null)
  if (image && objectData(image).animatedGIF) {
    inpaintAnchor.value = null
    paintSuspended.value = true
    return
  }
  if (image && isInpaint.value) {
    inpaintAnchor.value = image
    paintSuspended.value = false
  }
}

/**
 * Register anchor adoption before the painting watcher so entering inpaint with
 * an existing selection can enable drawing in the same update.
 */
watch(isInpaint, (inpaint) => {
  if (
    inpaint &&
    !inpaintAnchor.value &&
    selectedImage.value &&
    !objectData(selectedImage.value).animatedGIF
  ) {
    inpaintAnchor.value = selectedImage.value
    paintSuspended.value = false
  }
})

watch([isInpaint, inpaintAnchor, paintSuspended], syncPainting)

/**
 * syncPainting derives brush ownership from the operation, selected anchor, and
 * manual suspension state. It enters drawing only for an unsuspended inpaint
 * anchor and otherwise exits through the path that restores object
 * interactions. An anchor can change while drawing remains active, causing
 * enterPainting to return early. Clipping is therefore refreshed independently
 * before the outline update, keeping existing strokes constrained to the
 * current target rather than the previous image.
 */
function syncPainting(): void {
  const shouldPaint = isInpaint.value && inpaintAnchor.value !== null && !paintSuspended.value
  if (shouldPaint) enterPainting()
  else exitPainting()
  /**
   * Changing the anchor can leave painting enabled; refresh clipping
   * independently because enterPainting returns early for an already active brush
   * session.
   */
  if (painting.value) refreshMaskClip()
  updateAnchorOutline()
}

/**
 * makeBrush creates the selected brush shape against the current live canvas.
 * Callers provide that canvas first; brush size and translucent preview color
 * are taken from the current controls.
 */
function makeBrush(): PencilBrush {
  const brush =
    brushShape.value === 'square' ? new SquarePencilBrush(canvas!) : new PencilBrush(canvas!)
  brush.color = MASK_PREVIEW_TINT
  brush.width = brushSize.value
  return brush
}

/**
 * enterPainting enables drawing for an inpaint anchor and locks existing scene
 * objects. Repeated entry is ignored; interaction restoration belongs to
 * exitPainting, which consumes the saved per-object flags.
 */
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

/**
 * exitPainting disables drawing, restores saved interaction flags, and discards
 * mask drafts. Calling it outside active painting is harmless; exiting
 * deliberately clears undo history so discarded strokes cannot return.
 */
function exitPainting(): void {
  if (!canvas || !painting.value) return
  painting.value = false
  canvas.isDrawingMode = false
  canvas.freeDrawingBrush = undefined
  unlockAllObjects()
  canvas.defaultCursor = 'grab'
  syncCanvasSelection()
  /**
   * Leaving drawing mode discards session mask drafts and their undo entries,
   * preventing later undo from restoring strokes for an abandoned target.
   */
  discardMaskStrokes()
  canvas.requestRenderAll()
}

/**
 * updateAnchorOutline positions the runtime outline around the active inpaint
 * image. Missing canvas, anchor, or painting state removes the outline; newly
 * created outlines use the image's left-top coordinate origin.
 */
function updateAnchorOutline(): void {
  const anchor = inpaintAnchor.value
  if (!canvas || !anchor || !painting.value) {
    removeAnchorOutline()
    return
  }
  if (!anchorOutline) {
    anchorOutline = new Rect({
      ...ANCHOR_OUTLINE_STYLE,
      originX: 'left',
      originY: 'top',
      selectable: false,
      evented: false,
    })
    setObjectData(anchorOutline, { kind: ANCHOR_OUTLINE_KIND })
    canvas.add(anchorOutline)
  }
  const width =
    ((
      anchor as unknown as {
        width?: number // image width pixels
      }
    ).width ?? 0) * (anchor.scaleX ?? 1)
  const height =
    ((
      anchor as unknown as {
        height?: number // image height pixels
      }
    ).height ?? 0) * (anchor.scaleY ?? 1)
  anchorOutline.set({ left: anchor.left, top: anchor.top, width, height, angle: anchor.angle ?? 0 })
  anchorOutline.setCoords()
  canvas.requestRenderAll()
}

/**
 * removeAnchorOutline removes the runtime anchor rectangle and clears its
 * stored reference.
 */
function removeAnchorOutline(): void {
  if (anchorOutline && canvas) {
    canvas.remove(anchorOutline)
  }
  anchorOutline = null
}

/**
 * refreshMaskClip replaces the shared stroke clip with the anchor's scene
 * bounding box. It runs when painting starts or changes anchors; every existing
 * stroke receives the same absolute-positioned clip rectangle.
 */
function refreshMaskClip(): void {
  maskClipRect = null
  if (!canvas || !inpaintAnchor.value) return
  const vp = inpaintAnchor.value.getBoundingRect()
  maskClipRect = new Rect({
    left: vp.left,
    top: vp.top,
    width: vp.width,
    height: vp.height,
    originX: 'left',
    originY: 'top',
  })
  maskClipRect.absolutePositioned = true
  /**
   * Share the replacement clip across existing mask paths so changing the anchor
   * updates every retained stroke to the same scene bounds.
   */
  const clip = maskClipRect
  getMaskPaths().forEach((object) => {
    object.clipPath = clip
  })
}

/**
 * togglePainting toggles manual suspension using the current active painting
 * state.
 */
function togglePainting(): void {
  paintSuspended.value = painting.value
}

/**
 * syncEditRefFromSelection replaces edit references with images from a nonempty
 * selection. It acts only in edit mode and keeps existing references when
 * selection contains no images or is cleared. A GIF-only selection clears old
 * references so generating cannot accidentally reuse a previously selected still.
 */
function syncEditRefFromSelection(selected: FabricObject[] | undefined): void {
  if (!isEdit.value || !selected?.length) return
  const images = selected.filter(
    (object): object is FabricObject =>
      object instanceof FabricImage && !objectData(object).animatedGIF,
  )
  if (images.length || selected.some((object) => objectData(object).animatedGIF)) {
    editRefs.value = images
  }
}

/**
 * syncCanvasSelection enables Fabric box selection only when painting is
 * inactive. Cursor feedback also reflects the chosen selection tool, and a
 * render request applies the interaction change.
 */
function syncCanvasSelection(): void {
  if (!canvas) return
  canvas.selection = boxSelectMode.value && !painting.value
  canvas.defaultCursor = boxSelectMode.value || painting.value ? 'crosshair' : 'grab'
  canvas.requestRenderAll()
}

/**
 * setBoxSelectMode updates the selection tool and synchronizes Fabric
 * interaction settings.
 */
function setBoxSelectMode(on: boolean): void {
  boxSelectMode.value = on
  syncCanvasSelection()
}

watch(editRefs, refreshRefOutlines)
/**
 * Leaving edit mode drops its reference set; entering it adopts current
 * selected images before synchronizing the canvas selection tool.
 */
watch(isEdit, (edit) => {
  if (!edit) {
    editRefs.value = []
  } else if (canvas) {
    syncEditRefFromSelection(canvas.getActiveObjects())
  }
  syncCanvasSelection()
})

/**
 * refreshRefOutlines reconciles runtime reference rectangles with the current
 * edit-image set. It first removes rectangles whose associated image left the
 * set, then creates any missing outlines and updates every retained one from
 * absolute scene bounds. The map preserves each outline's association with its
 * Fabric image across selection changes. Refreshing coordinates before reading
 * bounds keeps outlines aligned when images are temporarily reparented into an
 * ActiveSelection group.
 */
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

/**
 * syncOutlineToObject positions a reference rectangle using refreshed absolute
 * scene bounds. Refreshing coordinates first matters after ActiveSelection
 * reparents an image, because the image's own left and top may then be
 * group-relative.
 */
function syncOutlineToObject(rect: Rect, object: FabricObject): void {
  /**
   * Refresh absolute corner coordinates after selection grouping so reference
   * outlines follow scene positions rather than image coordinates relative to the
   * group.
   */
  object.setCoords()
  const bounds = object.getBoundingRect()
  rect.set({
    left: bounds.left,
    top: bounds.top,
    width: bounds.width,
    height: bounds.height,
    angle: 0,
  })
  rect.setCoords()
}

/**
 * MASK_UNDO_LIMIT bounds retained stroke history so repeated painting cannot
 * grow the undo stack indefinitely.
 */
const MASK_UNDO_LIMIT = 50

/**
 * canUndoMask mirrors whether the stroke history contains an operation
 * available to the undo toolbar button.
 */
const canUndoMask = ref(false)

/**
 * pushMaskUndo records one reversible stroke operation and bounds retained
 * history. When the limit is exceeded the oldest entry is discarded, while the
 * newest operation remains available to the toolbar.
 */
function pushMaskUndo(entry: MaskUndoEntry): void {
  maskUndoStack.push(entry)
  if (maskUndoStack.length > MASK_UNDO_LIMIT) {
    /**
     * Drop the oldest operation when history reaches its limit.
     */
    maskUndoStack.shift()
  }
  canUndoMask.value = true
}

/**
 * undoMask reverses the newest retained mask operation using the original
 * Fabric path objects. An add entry removes one path, while a clear entry
 * restores the full retained group through addMaskPath so current clipping
 * applies. Empty history disables the control and changes no canvas content.
 * After reversal the stroke and undo flags reflect the remaining objects and
 * entries, then rendering and snapshot scheduling follow the updated state.
 */
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

/**
 * setBrushShape stores the desired shape and replaces an active drawing brush.
 * The replacement uses current size and preview color, so the next stroke
 * immediately reflects the toolbar selection.
 */
function setBrushShape(shape: BrushShape): void {
  brushShape.value = shape
  /**
   * Replacing an active brush applies the new shape to the next stroke while
   * reusing current size and preview settings.
   */
  if (painting.value && canvas) canvas.freeDrawingBrush = makeBrush()
}

watch(brushSize, (size) => {
  if (canvas?.freeDrawingBrush) canvas.freeDrawingBrush.width = size
})

/**
 * clearMask removes all current strokes as one reversible operation. Empty
 * canvases and empty stroke sets do nothing, and the retained path objects
 * allow undo to restore the complete group.
 */
function clearMask(): void {
  if (!canvas) return
  const paths = getMaskPaths()
  if (!paths.length) return
  paths.forEach(removeMaskPath)
  hasMaskStrokes.value = false
  pushMaskUndo({ type: 'clear', paths })
}

/**
 * discardMaskStrokes removes current strokes and empties their undo history. It
 * is used when painting ends, so later undo cannot restore paths belonging to
 * an abandoned mask draft.
 */
function discardMaskStrokes(): void {
  if (!canvas) return
  getMaskPaths().forEach(removeMaskPath)
  hasMaskStrokes.value = false
  maskUndoStack.length = 0
  canUndoMask.value = false
}

/**
 * lockAllObjects records each current scene object's interaction flags before
 * disabling selection and events for painting. The map retains object
 * identities so later restoration targets the same instances, including objects
 * whose flags were already disabled before painting began. Callers enter
 * through enterPainting, which skips repeated entry while a session is active.
 * unlockAllObjects consumes the retained flags on exit, restoring individual
 * behavior rather than enabling all objects indiscriminately.
 */
function lockAllObjects(): void {
  if (!canvas) return
  /**
   * Capture each object's interaction flags before disabling them so exiting
   * painting restores its previous selection and event behavior.
   */
  lockedStates = new Map()
  canvas.getObjects().forEach((object) => {
    lockedStates?.set(object, { selectable: object.selectable, evented: object.evented })
    object.set({ selectable: false, evented: false })
  })
}

/**
 * unlockAllObjects restores interaction flags on each object retained by
 * lockAllObjects. It uses the saved values even when an object originally
 * rejected interaction, then drops the map so the next painting session records
 * fresh state.
 */
function unlockAllObjects(): void {
  lockedStates?.forEach((state, object) => {
    object.set({ selectable: state.selectable, evented: state.evented })
  })
  /**
   * Release the saved interaction map after restoration so a later painting
   * session cannot reuse flags captured before intervening object changes.
   */
  lockedStates = null
}

/**
 * viewCenterScene converts the viewport center to scene coordinates using
 * current zoom. Callers require a live canvas; the calculation assumes the
 * translation-and-uniform-scale viewport used by this component.
 */
function viewCenterScene(): {
  x: number // horizontal scene coordinate
  y: number // vertical scene coordinate
} {
  const vpt = canvas!.viewportTransform
  const zoom = canvas!.getZoom()
  return {
    x: (canvas!.getWidth() / 2 - vpt[4]) / zoom,
    y: (canvas!.getHeight() / 2 - vpt[5]) / zoom,
  }
}

/**
 * stopPanAnim cancels the scheduled viewport animation frame and clears its
 * handle. Calls with no pending frame are harmless, allowing replacement
 * animation and unmount cleanup to share the same entry point.
 */
function stopPanAnim(): void {
  if (panAnimFrame !== null) cancelAnimationFrame(panAnimFrame)
  panAnimFrame = null
}

/**
 * panToScenePoint animates the viewport center toward a scene point at the
 * current zoom. It cancels the prior scheduled frame before capturing a
 * starting translation, so a replacement request begins from the currently
 * displayed viewport. Each frame applies cubic easing and synchronizes the grid
 * and mask transform. Completion clears the animation handle and schedules
 * scene saving, while teardown can cancel the pending callback through
 * stopPanAnim.
 */
function panToScenePoint(point: {
  x: number // horizontal scene coordinate
  y: number // vertical scene coordinate
}): void {
  if (!canvas) return
  stopPanAnim()
  const zoom = canvas.getZoom()
  const targetX = canvas.getWidth() / 2 - zoom * point.x
  const targetY = canvas.getHeight() / 2 - zoom * point.y
  const startX = canvas.viewportTransform[4]
  const startY = canvas.viewportTransform[5]
  /**
   * The viewport transition duration is measured in milliseconds.
   */
  const duration = 240
  const startTime = performance.now()
  /**
   * step advances the active pan interpolation and updates the synchronized grid.
   * It requests another frame until the duration elapses, then clears the handle
   * and schedules the final scene snapshot.
   */
  const step = (now: number) => {
    if (!canvas) return
    const progress = Math.min(1, (now - startTime) / duration)
    /**
     * Cubic easing slows movement near the destination center.
     */
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

/**
 * probeImageSize decodes original pixel dimensions and releases its temporary
 * resources. It closes a successful ImageBitmap; when bitmap decoding fails, an
 * Image element owns a Blob URL revoked on either load or error.
 */
async function probeImageSize(blob: Blob): Promise<{
  width: number // image width pixels
  height: number // image height pixels
}> {
  try {
    const bitmap = await createImageBitmap(blob)
    const size = { width: bitmap.width, height: bitmap.height }
    bitmap.close()
    return size
  } catch {
    /**
     * Bitmap decoding can be unavailable or reject this image; use an Image element
     * whose temporary URL is released on both completion paths.
     */
    return await new Promise((resolve, reject) => {
      const url = URL.createObjectURL(blob)
      const image = new Image()
      image.onload = () => {
        /**
         * Release the image element's temporary URL after decoding has supplied
         * dimensions, before resolving the waiting placement operation.
         */
        URL.revokeObjectURL(url)
        resolve({ width: image.naturalWidth, height: image.naturalHeight })
      }
      image.onerror = () => {
        /**
         * Release the temporary URL on decoding failure as well, since no successful
         * placement will take ownership of this resource.
         */
        URL.revokeObjectURL(url)
        reject(new Error('image load failed'))
      }
      image.src = url
    })
  }
}

/**
 * nextPlacementPoint chooses the next output's left-top scene position. The
 * first image centers in the current viewport; later images follow the previous
 * right edge and wrap when their displayed width crosses the configured limit.
 */
function nextPlacementPoint(
  width: number,
  height: number,
): {
  x: number // horizontal scene coordinate
  y: number // vertical scene coordinate
} {
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

/**
 * centeredPlacementPoint calculates a displayed image's left-top position
 * around a scene center. The default scale matches output placement; callers
 * pass original pixel dimensions so scaling is applied exactly once.
 */
function centeredPlacementPoint(
  size: {
    width: number // image width pixels
    height: number // image height pixels
  },
  center: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
  scale = PLACE_SCALE,
): {
  x: number // horizontal scene coordinate
  y: number // vertical scene coordinate
} {
  return {
    x: center.x - (size.width * scale) / 2,
    y: center.y - (size.height * scale) / 2,
  }
}

/**
 * recordLastPlaced records displayed image edges for the next automatic output
 * position.
 */
function recordLastPlaced(
  position: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
  size: {
    width: number // image width pixels
    height: number // image height pixels
  },
): void {
  lastPlaced = {
    right: position.x + size.width,
    top: position.y,
    bottom: position.y + size.height,
  }
}

/**
 * addImageToScene decodes a Blob and adds a selected image with asset metadata.
 * Position uses a left-top origin and scale changes display dimensions. The
 * temporary URL is revoked in finally; decoding or canvas errors emit a message
 * and return null.
 */
async function addImageToScene(
  blob: Blob,
  meta: {
    assetKey: string // local image record key
    runId?: string // server output owner
    outputIndex?: number // zero-based output index
  },
  position: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
  scale = 1,
): Promise<FabricImage | null> {
  if (!canvas) return null
  const url = URL.createObjectURL(await canvasImagePoster(blob))
  try {
    const image = await FabricImage.fromURL(url)
    image.set({
      /**
       * Placement positions describe the image's top-left corner, so override
       * Fabric's origin defaults before applying drop-centered coordinates and
       * display scaling.
       */
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
      animatedGIF: blob.type === 'image/gif',
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

/**
 * placeOutput queues an automatic output placement behind prior outputs and
 * initial scene restoration. Each queued callback checks disposal before
 * calling placeOutputNow, so decoded dimensions can determine the next position
 * after the previous image updates layout state. The queue retains a caught
 * continuation to allow later work after rejection. The returned promise
 * follows this placement, and callers may await it before proceeding to another
 * recovered output.
 */
async function placeOutput(asset: {
  blob: Blob // original image bytes
  runId: string // server output owner
  outputIndex: number // zero-based output index
}): Promise<void> {
  const placement = outputPlacementQueue.then(async () => {
    await sceneRestorePromise
    if (sceneDisposed) return
    await placeOutputNow(asset)
  })
  outputPlacementQueue = placement.catch(() => undefined)
  await placement
}

/**
 * placeOutputAt queues a history output at a supplied scene-space center. It
 * shares the automatic output queue so image decoding and insertion complete in
 * the same order as accepted placements. The queued callback waits for initial
 * restoration and skips work if disposal has occurred before its turn. A caught
 * queue continuation allows later placements after rejection, while the
 * returned promise still follows this requested insertion.
 */
async function placeOutputAt(
  asset: {
    blob: Blob // original image bytes
    runId: string // server output owner
    outputIndex: number // zero-based output index
  },
  center: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
): Promise<void> {
  const placement = outputPlacementQueue.then(async () => {
    await sceneRestorePromise
    if (sceneDisposed) return
    await placeOutputNow(asset, center)
  })
  outputPlacementQueue = placement.catch(() => undefined)
  await placement
}

/**
 * placeOutputNow loads and positions one output using its browser-local asset
 * key. It records the Blob in memory, chooses automatic layout unless a center
 * was supplied, and flushes the resulting scene after successful insertion;
 * loading errors emit feedback.
 */
async function placeOutputNow(
  asset: {
    blob: Blob // original image bytes
    runId: string // server output owner
    outputIndex: number // zero-based output index
  },
  center?: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
): Promise<void> {
  if (!canvas) return
  const assetKey = outputAssetKey(asset.runId, asset.outputIndex)
  runtimeBlobs.set(assetKey, asset.blob)
  try {
    /**
     * Use displayed dimensions for both automatic layout and centering; original
     * Blob bytes remain cached for full-resolution submission and download.
     */
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
    flushSceneSave()
  } catch (error) {
    console.error('Failed to place creative output:', error)
    emit('error', t('creative.error.loadImageFailed'))
  }
}

/**
 * addUploadedImageAt persists a source image before creating its scene
 * reference. It waits for initial restoration and checks the component lifetime
 * before storage begins, preventing an early upload from racing the initial
 * scene replacement. A storage failure stops placement and selects quota or
 * general error feedback. Successful storage supplies the runtime Blob cache,
 * then decoded dimensions center the displayed image and the completed
 * insertion triggers an immediate snapshot write.
 */
async function addUploadedImageAt(
  blob: Blob,
  center: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
): Promise<void> {
  await sceneRestorePromise
  if (sceneDisposed) return
  if (!canvas) return
  /**
   * A short random suffix distinguishes upload keys created within the same
   * millisecond.
   */
  const assetKey = localAssetKey(
    'source',
    `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
  )
  try {
    /**
     * Persist source bytes before creating their scene reference so a successful
     * scene snapshot can resolve that asset after a reload.
     */
    await saveAsset({ key: assetKey, kind: 'source', blob, createdAt: Date.now() })
  } catch (error) {
    /**
     * A failed source write prevents scene insertion; quota errors select backup
     * guidance while other storage errors use image-load feedback.
     */
    if (error instanceof LocalStoreQuotaError) {
      emit('error', t('creative.error.quotaExceeded'))
    } else {
      emit('error', t('creative.error.loadImageFailed'))
    }
    return
  }
  runtimeBlobs.set(assetKey, blob)
  try {
    const size = await probeImageSize(blob)
    const scale = PLACE_SCALE
    const position = centeredPlacementPoint(size, center, scale)
    const image = await addImageToScene(blob, { assetKey }, position, scale)
    if (image) {
      recordLastPlaced(position, { width: size.width * scale, height: size.height * scale })
    }
    scheduleSceneSave()
    flushSceneSave()
  } catch (error) {
    console.error('Failed to place uploaded image:', error)
    emit('error', t('creative.error.loadImageFailed'))
  }
}

/**
 * addUploadedImage places a source Blob around the current viewport center. A
 * missing canvas makes it a no-op; storage, restoration ordering, and
 * image-load errors are handled by addUploadedImageAt.
 */
async function addUploadedImage(blob: Blob): Promise<void> {
  if (!canvas) return
  await addUploadedImageAt(blob, viewCenterScene())
}

/**
 * addDroppedFiles places supported files sequentially with diagonal offsets
 * from the drop center. Each upload handles its own storage or decode failure,
 * allowing the next file to continue through the placement loop.
 */
async function addDroppedFiles(
  files: File[],
  center: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
): Promise<void> {
  for (const [index, file] of files.entries()) {
    const offset = index * PLACE_GAP
    await addUploadedImageAt(file, { x: center.x + offset, y: center.y + offset })
  }
}

/**
 * importDroppedOutput resolves a history reference from the local asset store.
 * Missing, unreadable, or non-output records emit unavailable-history feedback;
 * usable assets enter the shared placement queue at the drop center.
 */
async function importDroppedOutput(
  payload: {
    runId: string // server output owner
    outputIndex: number // zero-based output index
  },
  center: {
    x: number // horizontal scene coordinate
    y: number // vertical scene coordinate
  },
): Promise<void> {
  /**
   * A failed history asset read is treated as unavailable local content, allowing
   * the drop handler to show its dedicated recovery message.
   */
  const asset = await loadAsset(outputAssetKey(payload.runId, payload.outputIndex)).catch(
    () => null,
  )
  if (!asset || asset.kind !== 'output') {
    emit('error', t('creative.error.dropHistoryUnavailable'))
    return
  }
  await placeOutputAt(
    { blob: asset.blob, runId: payload.runId, outputIndex: payload.outputIndex },
    center,
  )
}

/**
 * selectedImageObject returns the active image or retained inpaint anchor. The
 * anchor remains usable while Fabric clears selection during brush drawing, and
 * an unavailable canvas returns null.
 */
function selectedImageObject(): FabricImage | null {
  if (!canvas) return null
  const active = canvas.getActiveObject()
  if (active instanceof FabricImage) return active
  return inpaintAnchor.value instanceof FabricImage ? inpaintAnchor.value : null
}

/**
 * getSelectedImageBlob reads the original bytes for the selected image or
 * anchor. Runtime cache entries take precedence; missing keys, unreadable
 * records, and absent images return null for submission validation.
 */
async function getSelectedImageBlob(): Promise<Blob | null> {
  const image = selectedImageObject()
  if (!image) return null
  const assetKey = objectData(image).assetKey
  if (typeof assetKey !== 'string') return null
  const cached = runtimeBlobs.get(assetKey)
  if (cached) return cached
  /**
   * An unreadable local record supplies no source bytes, so submission validation
   * can report a missing image instead of propagating a storage exception.
   */
  const asset = await loadAsset(assetKey).catch(() => null)
  if (asset) {
    runtimeBlobs.set(assetKey, asset.blob)
    return asset.blob
  }
  return null
}

/**
 * getEditRefBlobs collects original image bytes in the stored edit-reference
 * order. It reuses runtime cache entries and reads remaining assets locally,
 * skipping references whose image type, key, or stored record is unavailable.
 */
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
    /**
     * Skip unreadable reference assets while retaining other selected images, so
     * one missing local record does not prevent exporting the remaining references.
     */
    const asset = await loadAsset(assetKey).catch(() => null)
    if (asset) {
      runtimeBlobs.set(assetKey, asset.blob)
      blobs.push(asset.blob)
    }
  }
  return blobs
}

/**
 * getMaskBlob exports painted regions as a PNG matching the selected image's
 * natural dimensions. It temporarily makes strokes white and removes the mask
 * viewport transform so extraction uses scene coordinates. A finally block
 * restores colors and the viewport even when extraction fails. The extracted
 * strokes erase an opaque offscreen image through destination-out, producing
 * transparent repaint regions. Missing canvas, image, strokes, or drawing
 * context returns null for the caller's required-mask validation.
 */
async function getMaskBlob(): Promise<Blob | null> {
  if (!canvas || !maskCanvas) return null
  const image = selectedImageObject()
  if (!image || objectData(image).animatedGIF) return null
  const maskObjects = getMaskPaths()
  if (!maskObjects.length) return null

  const rect = image.getBoundingRect()
  /**
   * Keep each stroke's display colors before white-mask extraction so finally can
   * restore the same path objects even if export fails.
   */
  const styleBackup = maskObjects.map((object) => ({
    object,
    fill: object.fill,
    stroke: object.stroke,
  }))
  styleBackup.forEach(({ object }) => {
    object.set({
      fill: object.fill ? MASK_COLOR : object.fill,
      stroke: object.stroke ? MASK_COLOR : object.stroke,
    })
  })
  const viewport = [...maskCanvas.viewportTransform] as TMat2D
  /**
   * Export bounds are expressed in scene coordinates, so temporarily remove
   * viewport pan and zoom before extracting the painted rectangle.
   */
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
    /**
     * Restore the viewport and stroke colors before requesting either render,
     * including when canvas extraction throws and no PNG is produced.
     */
    maskCanvas.setViewportTransform(viewport)
    styleBackup.forEach(({ object, fill, stroke }) => {
      object.set({ fill: fill ?? null, stroke: stroke ?? null })
    })
    canvas.requestRenderAll()
    maskCanvas.requestRenderAll()
    syncDotGrid()
  }

  const source = image.getElement() as HTMLImageElement
  /**
   * Resize the extracted region to source pixel dimensions because mask upload
   * validation expects the mask and source image sizes to match.
   */
  const naturalWidth = source.naturalWidth || Math.max(1, Math.round(rect.width))
  const naturalHeight = source.naturalHeight || Math.max(1, Math.round(rect.height))
  const offscreen = document.createElement('canvas')
  offscreen.width = naturalWidth
  offscreen.height = naturalHeight
  const context = offscreen.getContext('2d')
  if (!context) return null
  context.fillStyle = '#ffffff'
  context.fillRect(0, 0, naturalWidth, naturalHeight)
  /**
   * Painted strokes erase the opaque offscreen base, converting the visible mask
   * draft into transparent regions used for image repainting.
   */
  context.globalCompositeOperation = 'destination-out'
  context.drawImage(element, 0, 0, naturalWidth, naturalHeight)
  context.globalCompositeOperation = 'source-over'
  return await new Promise<Blob | null>((resolve) => offscreen.toBlob(resolve, 'image/png'))
}

/**
 * removeSelected removes members of the current Fabric selection from the
 * scene. It discards the ActiveSelection wrapper first so deleting a group
 * removes its images, then requests a render.
 */
function removeSelected(): void {
  if (!canvas) return
  const selected = canvas.getActiveObjects()
  if (!selected.length) return
  canvas.discardActiveObject()
  canvas.remove(...selected)
  canvas.requestRenderAll()
}

/**
 * downloadSelected downloads the selected image's original Blob through
 * file-saver. Missing image bytes are ignored, and the filename extension comes
 * from its MIME type with PNG as the default.
 */
async function downloadSelected(): Promise<void> {
  const blob = await getSelectedImageBlob()
  if (!blob) return
  const extension = (blob.type || 'image/png').split('/')[1] || 'png'
  saveAs(blob, `creative-image-${Date.now()}.${extension}`)
}

/**
 * resetCanvas clears image state and invalidates any pending scene restoration.
 * The generation changes before objects are removed, so later restoration
 * checks reject their captured generation. Runtime references, mask undo
 * history, and cached Blobs are cleared alongside the canvas, and the viewport
 * returns to its default transform. The empty scene is then marked dirty and
 * flushed immediately, making a successful clear the snapshot used on the next
 * page visit.
 */
function resetCanvas(): void {
  if (!canvas) return
  stopGIFPlayback()
  /**
   * Invalidate recovery before clearing scene state, so pending reads fail their
   * generation check when they resume after the user reset.
   */
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
  syncImageCount()
  sceneReady.value = true
  canvas.requestRenderAll()
  scheduleSceneSave()
  flushSceneSave()
}

/**
 * scheduleSceneSave records a user mutation and restarts the scene-save
 * debounce. Calls made during restoration or after disposal are ignored because
 * those scene events must not create a user snapshot. Each accepted call
 * increments the revision before replacing the pending timer. When the callback
 * runs it captures the current scene through persistSceneNow, whose
 * matching-revision check prevents an older write from clearing later dirty
 * state.
 */
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

/**
 * flushSceneSave cancels the debounce and starts persistence of the current
 * dirty scene. It does not await the write, allowing pagehide and unmount to
 * initiate best-effort storage synchronously.
 */
function flushSceneSave(): void {
  if (sceneSaveTimer) clearTimeout(sceneSaveTimer)
  sceneSaveTimer = null
  void persistSceneNow()
}

/**
 * persistSceneNow serializes the current dirty scene and appends its write to
 * the ordered persistence chain. Disposed or restoring canvases leave the
 * existing chain unchanged. Each write captures its revision and clears dirty
 * state only if no newer mutation has occurred before success. A rejected
 * predecessor is consumed so later saves can run, while a failed current write
 * is logged and leaves the dirty flag available for a later flush.
 */
function persistSceneNow(): Promise<void> {
  if (!canvas || sceneDisposed || sceneRestoreInProgress || !sceneDirty) return sceneWriteChain
  const json = snapshotScene()
  const revision = sceneRevision
  /**
   * Queue snapshot writes in capture order and clear dirty state only for the
   * captured revision, keeping newer mutations eligible for another save.
   */
  sceneWriteChain = sceneWriteChain
    .catch(() => undefined)
    .then(() => saveSceneJson(SCENE_KEY, json))
    .then(() => {
      if (sceneRevision === revision) sceneDirty = false
    })
    .catch((error) => {
      console.error('Failed to persist creative scene:', error)
    })
  return sceneWriteChain
}

/**
 * snapshotScene serializes persistent Fabric objects with their custom asset
 * metadata. Image sources are rewritten to asset-key placeholders so ephemeral
 * Blob URLs and image bytes are separated from scene JSON. Runtime outlines,
 * resolution labels, and session mask strokes are excluded from the object
 * list. Restoration resolves the retained asset keys against IndexedDB, keeping
 * image identity across reloads while allowing missing local images to be
 * skipped independently.
 */
function snapshotScene(): string {
  if (!canvas) return '{}'
  const json = canvas.toObject(['data']) as {
    objects?: Array<Record<string, unknown> & ObjectWithData> // serialized Fabric scene objects
  }
  for (const object of json.objects ?? []) {
    const assetKey = object.data?.assetKey
    if (isImageSnapshotObject(object) && typeof assetKey === 'string') {
      /**
       * Replace ephemeral image URLs with durable asset references while keeping
       * custom metadata, allowing reload to reconstruct bytes from the local store.
       */
      object.src = `${ASSET_PROTOCOL}${assetKey}`
    }
  }
  /**
   * Exclude selection helpers and mask drafts from persistence because their
   * lifecycle belongs to the current interaction rather than the saved image
   * workspace.
   */
  json.objects = (json.objects ?? []).filter(
    (object) =>
      object.data?.kind !== ANCHOR_OUTLINE_KIND &&
      object.data?.kind !== REF_OUTLINE_KIND &&
      object.data?.kind !== RESOLUTION_TAG_KIND &&
      object.data?.kind !== 'mask',
  )
  return JSON.stringify(json)
}

/**
 * restoreScene loads a saved scene and rebuilds its image sources from
 * browser-local assets. The captured canvas and generation are checked around
 * asynchronous reads and after Fabric loading to stop obsolete recovery before
 * further publication. Missing assets and expired legacy Blob URLs are skipped,
 * while runtime helpers and mask drafts are discarded. Every temporary URL is
 * revoked in finally, including interrupted recovery; readiness is published
 * after cleanup, and only the matching generation clears restoration
 * suppression.
 */
async function restoreScene(generation: number): Promise<void> {
  const target = canvas
  if (!target) {
    sceneRestoreInProgress = false
    return
  }
  const pendingUrls: string[] = []
  /**
   * isCurrent reports whether restoration still owns the same live canvas
   * generation.
   */
  const isCurrent = () =>
    !sceneDisposed && canvas === target && sceneRestoreGeneration === generation
  try {
    const stored = await loadSceneJson(SCENE_KEY)
    if (!stored || !isCurrent()) return
    const parsed = JSON.parse(stored) as {
      objects?: Array<Record<string, unknown> & ObjectWithData> // serialized Fabric scene objects
    }
    const objects = Array.isArray(parsed.objects) ? parsed.objects : []
    const restored: typeof objects = []
    for (const object of objects) {
      if (
        object.data?.kind === ANCHOR_OUTLINE_KIND ||
        object.data?.kind === REF_OUTLINE_KIND ||
        object.data?.kind === RESOLUTION_TAG_KIND
      ) {
        continue
      }
      if (object.data?.kind === 'mask') {
        continue
      }
      const assetKey = object.data?.assetKey
      if (isImageSnapshotObject(object) && typeof assetKey === 'string') {
        /**
         * A missing or failed asset read skips this image while allowing other scene
         * objects to recover from their own available records.
         */
        const blob = await loadAsset(assetKey)
          .then((asset) => asset?.blob ?? null)
          .catch(() => null)
        if (!isCurrent()) return
        if (!blob) {
          continue
        }
        runtimeBlobs.set(assetKey, blob)
        const objectUrl = URL.createObjectURL(await canvasImagePoster(blob))
        object.data = { ...object.data, animatedGIF: blob.type === 'image/gif' }
        object.src = objectUrl
        pendingUrls.push(objectUrl)
      } else if (
        isImageSnapshotObject(object) &&
        typeof object.src === 'string' &&
        /**
         * Legacy Blob URLs expire with their creating document; skip those image
         * entries when no durable asset key can rebuild their source.
         */
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
    syncDotGrid()
    hasMaskStrokes.value = getMaskPaths().length > 0
  } catch (error) {
    /**
     * Recovery errors are logged while finally releases temporary URLs and marks
     * loading complete, leaving the currently available scene accessible to the
     * user.
     */
    console.error('Failed to restore creative scene:', error)
  } finally {
    /**
     * Revoke every temporary source after Fabric loading or interrupted recovery so
     * success, failure, and stale generations all release allocated Blob URLs.
     */
    pendingUrls.forEach((url) => URL.revokeObjectURL(url))
    /**
     * Matching recovery completes with a clean scene because Fabric add/remove
     * events during loading do not represent user edits to persist.
     */
    if (sceneRestoreGeneration === generation) {
      sceneRestoreInProgress = false
      sceneDirty = false
    }
    syncImageCount()
    sceneReady.value = true
  }
}

/**
 * onFilesPicked appends MIME-approved files to the crop queue and resets the
 * input. Clearing the input allows choosing the same file again, while each
 * queue entry remains available for crop confirmation.
 */
function onFilesPicked(event: Event): void {
  const input = event.target as HTMLInputElement
  const allowedMIMEs = new Set(props.allowedMimes.map((mime) => mime.toLowerCase()))
  const files = Array.from(input.files ?? []).filter((file) =>
    allowedMIMEs.has(file.type.toLowerCase()),
  )
  /**
   * Reset the file input after collecting selected files so choosing the same
   * image again can trigger another upload event.
   */
  input.value = ''
  if (!files.length) return
  cropQueue.value = [...cropQueue.value, ...files]
}

/**
 * onCropConfirm removes the current crop entry and starts centered source
 * placement. Skip events use the same path with original bytes, and remaining
 * queued images advance independently of placement completion.
 */
function onCropConfirm(blob: Blob): void {
  cropQueue.value = cropQueue.value.slice(1)
  void addUploadedImage(blob)
}

/**
 * onCropCancel discards every remaining image in the current crop queue.
 */
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
  openFilePicker: () => fileInputRef.value?.click(),
  hasMaskStrokes,
  guideKey,
  imageCount,
  sceneReady,
})

/**
 * canvasImagePoster decodes the first GIF frame into an independent PNG Blob.
 * Static inputs retain their original bytes. The bitmap closes after rendering,
 * while callers keep the original animation separately for playback and download.
 */
async function canvasImagePoster(blob: Blob): Promise<Blob> {
  if (blob.type !== 'image/gif') return blob
  const bitmap = await createImageBitmap(blob)
  try {
    const surface = document.createElement('canvas')
    surface.width = bitmap.width
    surface.height = bitmap.height
    const context = surface.getContext('2d')
    if (!context) throw new Error('GIF poster context unavailable')
    context.drawImage(bitmap, 0, 0)
    return await new Promise<Blob>((resolve, reject) =>
      surface.toBlob(
        (value) => (value ? resolve(value) : reject(new Error('GIF poster encoding failed'))),
        'image/png',
      ),
    )
  } finally {
    // The poster owns copied pixels; release the decoder without discarding the
    // original animation Blob retained by the canvas asset store.
    bitmap.close()
  }
}

/**
 * syncGIFControlPosition anchors playback controls inside the selected GIF.
 * The play button and loading spinner sit at its center; once frames play, the
 * compact stop button moves inward from its upper-left corner, where it covers
 * less of the animation. Scene and viewport transforms keep the
 * control attached while objects rotate, scale, or move and the canvas pans.
 * Its hit area stays fixed in screen pixels and its icon remains upright.
 * Smaller images clamp the inset at their center so the control stays anchored
 * to the image instead of an unrelated point in the workspace.
 */
function syncGIFControlPosition(): void {
  const selected = selectedImage.value
  if (!canvas || !(selected instanceof FabricImage) || !objectData(selected).animatedGIF) {
    gifControlPosition.value = null
    return
  }
  const matrix = selected.calcTransformMatrix()
  let position = new Point(0, 0)
  if (gifPlaybackTarget.value === selected && gifControlState.value === 'stop') {
    // Keep the 32px stop button's center 28 screen pixels inside each image edge,
    // leaving a 12px gap; smaller images clamp those insets at their center.
    const insetX = Math.min(
      selected.width / 2,
      28 / (Math.hypot(matrix[0], matrix[1]) * canvas.getZoom()),
    )
    const insetY = Math.min(
      selected.height / 2,
      28 / (Math.hypot(matrix[2], matrix[3]) * canvas.getZoom()),
    )
    position = new Point(-selected.width / 2 + insetX, -selected.height / 2 + insetY)
  }
  position = position.transform(matrix).transform(canvas.viewportTransform)
  gifControlPosition.value = { left: `${position.x}px`, top: `${position.y}px` }
}

/**
 * stopGIFPlayback invalidates pending loads and restores the selected poster.
 * Restoring the element precedes buffer disposal so Fabric never references a
 * released canvas. Repeated calls also cancel a load that has not published a
 * player yet, and leave original asset bytes untouched.
 *
 * Pending decoders compare their captured generation before installing a frame
 * surface. Invalidating that generation first prevents a late load from
 * restarting playback after cleanup or replacing the next selected image.
 */
function stopGIFPlayback(): void {
  gifPlaybackGeneration++
  const target = gifPlaybackTarget.value
  if (target && gifPoster) {
    target.setElement(gifPoster)
    target.dirty = true
  }
  gifPlayback?.dispose()
  gifPlayback = null
  gifPoster = null
  gifPlaybackTarget.value = null
  gifPlaybackLoading.value = false
  syncGIFControlPosition()
  canvas?.requestRenderAll()
}

/**
 * toggleSelectedGIF plays original GIF frames on the existing Fabric image.
 * A second click stops either loading or playback. Selection changes, reset,
 * hiding, and unmount invalidate the captured generation before publishing a
 * decoded player, so obsolete work cannot replace another image's poster.
 *
 * Only the image element changes: its geometry, stacking, and asset metadata
 * remain owned by the scene. Each frame marks the image dirty and requests a
 * render; failures restore the poster and report a localized playback error.
 */
async function toggleSelectedGIF(): Promise<void> {
  if (gifPlaybackTarget.value) {
    stopGIFPlayback()
    return
  }
  const selected = selectedImage.value
  if (!(selected instanceof FabricImage) || !objectData(selected).animatedGIF) return
  const generation = ++gifPlaybackGeneration
  gifPlaybackTarget.value = selected
  gifPlaybackLoading.value = true
  syncGIFControlPosition()
  try {
    const blob = await getSelectedImageBlob()
    if (!blob || blob.type !== 'image/gif') throw new Error('GIF asset unavailable')
    const { createGIFPlayback } = await import('./gifPlayback')
    const player = await createGIFPlayback(
      blob,
      () => {
        selected.dirty = true
        canvas?.requestRenderAll()
      },
      () => {
        stopGIFPlayback()
        emit('error', t('creative.error.playGIFFailed'))
      },
    )
    if (sceneDisposed || generation !== gifPlaybackGeneration || selectedImage.value !== selected) {
      // A newer selection owns the scene now. Release the unpublished player
      // without changing that selection or installing the stale animation surface.
      player.dispose()
      return
    }
    gifPoster = selected.getElement()
    gifPlayback = player
    selected.setElement(player.surface)
    gifPlaybackLoading.value = false
    // Fabric renders the first frame on a later animation frame. Syncing now keeps
    // the compact stop button from entering at the old center position meanwhile.
    syncGIFControlPosition()
    player.start()
  } catch {
    if (generation !== gifPlaybackGeneration) return
    stopGIFPlayback()
    emit('error', t('creative.error.playGIFFailed'))
  }
}

/**
 * A selected animation owns playback resources; changing selection restores its
 * poster before the new image can start loading or receive playback controls.
 */
watch(selectedImage, () => {
  if (gifPlaybackTarget.value && gifPlaybackTarget.value !== selectedImage.value) stopGIFPlayback()
  syncGIFControlPosition()
})
</script>

<style scoped>
/* syncDotGrid supplies tile pitch and positions for one coarse and three fine dot layers. Initial values cover the first frame before viewport synchronization. */
.dot-grid {
  /* Keep browser gestures from consuming two-finger input before the captured canvas handlers calculate scene pan and zoom. */
  touch-action: none;
  --grid-fine-opacity: 0.74;
  --grid-dot-alpha: 0.1;
  --grid-dot: rgb(15 23 42 / var(--grid-dot-alpha));
  --grid-dot-fine: rgb(15 23 42 / calc(var(--grid-dot-alpha) * var(--grid-fine-opacity)));
  background-image:
    radial-gradient(circle, var(--grid-dot) 0.55px, transparent 1.25px),
    radial-gradient(circle, var(--grid-dot-fine) 0.55px, transparent 1.25px),
    radial-gradient(circle, var(--grid-dot-fine) 0.55px, transparent 1.25px),
    radial-gradient(circle, var(--grid-dot-fine) 0.55px, transparent 1.25px);
  background-size: 40px 40px;
  background-position:
    -20px -20px,
    0 -20px,
    -20px 0,
    0 0;
}

.drop-target-active {
  box-shadow: inset 0 0 0 2px rgb(0 210 255 / 0.45);
}

.dark .dot-grid {
  --grid-dot-alpha: 0.11;
  --grid-dot: rgb(255 255 255 / var(--grid-dot-alpha));
  --grid-dot-fine: rgb(255 255 255 / calc(var(--grid-dot-alpha) * var(--grid-fine-opacity)));
}

/* The page supplies reserved history width so toolbar positioning shares the remaining desktop workspace center with the composer and empty guide. */
.canvas-toolbar {
  left: calc((100% - var(--creative-history-reserve, 0px)) / 2);
  transition: left var(--motion-layout) var(--motion-ease);
}

.canvas-toolbar .canvas-tool-btn {
  border-radius: 9999px;
}

/*
 * GIF playback controls sit on image pixels rather than workspace chrome, so both
 * themes use a dark media scrim with a white glyph and a faint light edge. The
 * standalone translate property centers the button on synchronized coordinates,
 * leaving scale free for hover, press, and enter or leave transitions.
 */
.canvas-gif-toggle {
  @apply absolute inline-flex h-14 w-14 items-center justify-center rounded-full;
  @apply bg-black/55 text-white shadow-md ring-1 ring-white/15 backdrop-blur-md;
  @apply focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500;
  z-index: 2; /* check-ui-allow: Canvas-local playback control stacking. */
  translate: -50% -50%;
  transition:
    background-color var(--motion-fast) var(--motion-ease),
    scale var(--motion-fast) var(--motion-ease);
}

.canvas-gif-toggle:hover {
  @apply bg-black/70;
  scale: 1.06;
}

.canvas-gif-toggle:active {
  scale: 0.96;
}

/* During playback the stop control shrinks so it covers less of the animation. */
.canvas-gif-toggle-compact {
  @apply h-8 w-8;
}

/* The play triangle's visual mass sits left of its box, so it shifts right by 2px. */
.canvas-gif-play-icon {
  @apply translate-x-0.5;
}

/* Switching between play and stop cross-fades the two buttons while each scales from 80%. */
.canvas-gif-toggle-enter-active,
.canvas-gif-toggle-leave-active {
  transition:
    opacity var(--motion-fast) var(--motion-ease),
    scale var(--motion-fast) var(--motion-ease);
}

.canvas-gif-toggle-enter-from,
.canvas-gif-toggle-leave-to {
  opacity: 0;
  scale: 0.8;
}

/* Pointer events pass through to Fabric while whole-layer opacity keeps overlapping mask strokes at the same visible intensity. */
.mask-overlay {
  @apply pointer-events-none absolute inset-0 z-[1]; /* check-ui-allow: Canvas-local stacking. */
}

/* Desktop expansion animates the brush group as one bounded row. The narrow-screen rule switches to contents so individual controls can wrap. */
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
    max-width var(--motion-layout) var(--motion-ease),
    max-height var(--motion-layout) var(--motion-ease),
    opacity var(--motion-layout) var(--motion-ease),
    transform var(--motion-layout) var(--motion-ease);
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
      opacity var(--motion-fast) var(--motion-ease),
      transform var(--motion-layout) var(--motion-ease);
  }

  .canvas-toolbar-extension-enter-from > *,
  .canvas-toolbar-extension-leave-to > * {
    opacity: 0;
    transform: translateY(-4px) scale(0.92);
  }
}

/* Explicit track and thumb colors avoid relying on accent-color for the unfilled range track, keeping the same controls readable in both themes. */
.brush-size {
  -webkit-appearance: none;
  appearance: none;
  background: transparent;
}

.brush-size::-webkit-slider-runnable-track {
  height: 4px;
  border-radius: 9999px;
  background: rgb(229 231 235);
}

.dark .brush-size::-webkit-slider-runnable-track {
  background: rgb(39 39 42);
}

.brush-size::-webkit-slider-thumb {
  -webkit-appearance: none;
  appearance: none;
  margin-top: -6px;
  height: 16px;
  width: 16px;
  border: none;
  border-radius: 9999px;
  background: rgb(18 167 232);
}

.brush-size::-moz-range-track {
  height: 4px;
  border-radius: 9999px;
  background: rgb(229 231 235);
}

.dark .brush-size::-moz-range-track {
  background: rgb(39 39 42);
}

.brush-size::-moz-range-thumb {
  height: 16px;
  width: 16px;
  border: none;
  border-radius: 9999px;
  background: rgb(18 167 232);
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
