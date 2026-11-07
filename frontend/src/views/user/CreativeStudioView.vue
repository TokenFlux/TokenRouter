<template>
  <AppLayout full-viewport>
    <div
      ref="stageRef"
      class="relative h-full min-h-0"
      :class="[
        '[--creative-history-w:20rem] xl:[--creative-history-w:24rem]',
        historyOpen && 'md:[--creative-history-reserve:calc(var(--creative-history-w)_+_0.75rem)]',
      ]"
    >
      <CreativeCanvas
        ref="canvasRef"
        class="absolute inset-0"
        :operation="studio.operation.value"
        :allowed-mimes="studio.capabilities.value.allowed_mime_types"
        @error="onCanvasError"
      />
      <CreativeRunHistory
        ref="historyRef"
        v-model:open="historyOpen"
        :studio="studio"
        :active-run-count="activeRunCount"
      />

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
        <MotionTransition name="pop-float">
          <div v-if="settingsOpen" class="settings-pop-float dropdown left-0 top-12 w-56 py-0">
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

      <MotionTransition name="fade">
        <div
          v-if="showEmptyGuide"
          class="creative-empty-guide pointer-events-none absolute left-4 top-1/3 z-10 flex flex-col items-center gap-4 text-center"
          data-testid="creative-empty-guide"
        >
          <div class="space-y-1">
            <p class="text-base font-semibold text-gray-800 dark:text-dark-50">
              {{ t('creative.empty.title') }}
            </p>
            <p class="text-sm text-gray-500 dark:text-dark-300">
              {{ t('creative.empty.description') }}
            </p>
          </div>
          <div class="pointer-events-auto flex max-w-xl flex-wrap justify-center gap-2">
            <button
              type="button"
              class="canvas-island empty-guide-chip"
              @click="canvasRef?.openFilePicker()"
            >
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

      <div class="creative-composer-dock absolute bottom-4 z-30 -translate-x-1/2">
        <CreativeComposer
          ref="composerRef"
          :studio="studio"
          :guide="canvasRef?.guideKey ?? null"
          @generate="onGenerate"
        />
      </div>

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

/**
 * CreativeStudioView.vue connects the canvas, generation form, and history
 * within the image workspace. Submitting collects the selected references or
 * anchored source and painted mask from the canvas, then passes those bytes to
 * the shared studio. The mounted view registers canvas placement callbacks used
 * by recovered outputs and history imports.
 *
 * The empty guide waits for scene restoration before interpreting zero images.
 * Desktop history reserves space so the toolbar, guide, and composer share a
 * center; narrow layouts overlay the canvas. Submission feedback measures the
 * current buttons and schedules an animation toward the visible history entry.
 * Unmount removes the bridge and cancels the pending animation frame, while the
 * child components own their storage and drawing resources.
 */

/**
 * t, appStore, and studio provide localization, feedback, and generation state
 * shared by the workspace components.
 */
const { t } = useI18n()
const appStore = useAppStore()
const studio = useCreativeStudio()

/**
 * canvasRef, composerRef, and historyRef expose mounted child actions and
 * measured elements used for submission feedback.
 */
const canvasRef = ref<InstanceType<typeof CreativeCanvas> | null>(null)
const composerRef = ref<InstanceType<typeof CreativeComposer> | null>(null)
const historyRef = ref<InstanceType<typeof CreativeRunHistory> | null>(null)
/**
 * stageRef and settingsRef locate animation coordinates and outside-click
 * handling within the full-viewport workspace.
 */
const stageRef = ref<HTMLDivElement | null>(null)
const settingsRef = ref<HTMLDivElement | null>(null)
/**
 * showClearConfirm separates the destructive local-data confirmation from the
 * canvas-only reset action in the settings menu.
 */
const showClearConfirm = ref(false)
/**
 * settingsOpen controls the upper-left menu and closes on outside interaction
 * or selection of a settings action.
 */
const settingsOpen = ref(false)
/**
 * historyOpen reserves desktop width for history and moves the toolbar, guide,
 * and composer together as visibility changes.
 */
const historyOpen = ref(false)
/**
 * Submission visibility, style, and frame state own one measured flight
 * animation from the composer toward the visible history entry.
 */
const submissionAnimationVisible = ref(false)
const submissionAnimationStyle = ref<Record<string, string>>({})
let submissionAnimationFrame: number | null = null

/**
 * activeRunCount counts every nonterminal history record for the history
 * entry's current-work badge.
 */
const activeRunCount = computed(
  () =>
    studio.runHistory.value.filter((run) => !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status))
      .length,
)

/**
 * EXAMPLE_PROMPT_KEYS identifies the localized example labels and prompts
 * offered by the empty-canvas guide.
 */
const EXAMPLE_PROMPT_KEYS = ['poster', 'product', 'illustration'] as const

/**
 * showEmptyGuide waits for canvas recovery to finish before treating zero
 * images as an empty workspace.
 */
const showEmptyGuide = computed(
  () => Boolean(canvasRef.value?.sceneReady) && canvasRef.value?.imageCount === 0,
)

onClickOutside(settingsRef, () => {
  settingsOpen.value = false
})

/**
 * useExamplePrompt fills the composer with one localized empty-canvas example
 * prompt.
 */
function useExamplePrompt(key: (typeof EXAMPLE_PROMPT_KEYS)[number]): void {
  composerRef.value?.fillPrompt(t(`creative.empty.examples.${key}.prompt`))
}

/**
 * Register placement callbacks after child mounting, then begin catalog and
 * history loading so recovered images can use the current canvas bridge.
 */
onMounted(() => {
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

/**
 * Detach the canvas bridge before child resources disappear and cancel any
 * pending submission animation start owned by this view.
 */
onBeforeUnmount(() => {
  studio.registerCanvasBridge(null)
  if (submissionAnimationFrame !== null && typeof cancelAnimationFrame === 'function') {
    cancelAnimationFrame(submissionAnimationFrame)
    submissionAnimationFrame = null
  }
})

/**
 * playSubmissionAnimation measures visible submission and history buttons and
 * starts feedback between them. It cancels a pending animation frame first;
 * missing targets skip animation, and animationend owns final visibility
 * cleanup.
 */
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
    /**
     * Environments without animation-frame scheduling reveal the feedback
     * immediately; the named CSS animation end still owns normal completion
     * cleanup.
     */
    submissionAnimationVisible.value = true
  }
}

/**
 * onSubmitFlightEnd hides submission feedback and triggers the history
 * destination bump. It ignores unrelated animation names so another animation
 * on the same element cannot prematurely complete this feedback sequence.
 */
function onSubmitFlightEnd(event: AnimationEvent): void {
  if (event.animationName !== 'creative-submit-flight') return
  submissionAnimationVisible.value = false
  historyRef.value?.bump()
}

/**
 * cancelSubmissionAnimation hides feedback and cancels any scheduled animation
 * start frame. It clears the retained handle after cancellation so future
 * submissions can begin their own measured feedback.
 */
function cancelSubmissionAnimation(): void {
  submissionAnimationVisible.value = false
  if (submissionAnimationFrame !== null && typeof cancelAnimationFrame === 'function') {
    cancelAnimationFrame(submissionAnimationFrame)
    submissionAnimationFrame = null
  }
}

/**
 * onGenerate collects operation-specific canvas inputs and submits them through
 * the studio. Edit uses selected references, inpaint requires an anchored image
 * and exported mask, and missing inputs cancel feedback with a localized
 * message before submission.
 */
async function onGenerate(): Promise<void> {
  playSubmissionAnimation()
  const operation = studio.operation.value
  let sourceBlobs: Blob[] = []
  let maskBlob: Blob | null = null
  if (operation === 'edit') {
    sourceBlobs = (await canvasRef.value?.getEditRefBlobs()) ?? []
    if (sourceBlobs.some((blob) => blob.type === 'image/gif')) {
      cancelSubmissionAnimation()
      studio.error.value = t('creative.canvas.gifReadOnly')
      return
    }
    if (!sourceBlobs.length) {
      cancelSubmissionAnimation()
      studio.error.value = t('creative.panel.selectImageHint')
      return
    }
  } else if (operation === 'inpaint') {
    const blob = await canvasRef.value?.getSelectedImageBlob()
    if (blob?.type === 'image/gif') {
      cancelSubmissionAnimation()
      studio.error.value = t('creative.canvas.gifReadOnly')
      return
    }
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
      /**
       * Mask extraction failure leaves maskBlob empty so the required-mask check
       * cancels feedback and reports guidance before submitting an inpaint request.
       */
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

/**
 * onCanvasError publishes canvas failure text through the shared studio status
 * line.
 */
function onCanvasError(message: string): void {
  studio.error.value = message
}

/**
 * onResetCanvas closes the settings menu and clears the current canvas scene.
 */
function onResetCanvas(): void {
  settingsOpen.value = false
  canvasRef.value?.resetCanvas()
}

/**
 * onClearRequested closes settings and opens confirmation for deleting local
 * creative data.
 */
function onClearRequested(): void {
  settingsOpen.value = false
  showClearConfirm.value = true
}

/**
 * onClearLocalData clears studio storage before resetting the displayed canvas.
 * Success shows confirmation feedback; failures leave the studio error
 * available and show an error toast after the confirmation closes.
 */
async function onClearLocalData(): Promise<void> {
  showClearConfirm.value = false
  try {
    await studio.clearLocalData()
    canvasRef.value?.resetCanvas()
    appStore.showSuccess(t('creative.history.clearSuccess'))
  } catch {
    /**
     * The studio has already recorded clearing details; show the view-level failure
     * toast and leave the canvas reset contingent on successful storage clearing.
     */
    appStore.showError(t('creative.error.clearFailed'))
  }
}
</script>

<style scoped>
.settings-pop-float {
  --pop-shift: calc(-1 * var(--motion-shift));
}

/* Subtract the desktop history reserve from both centering and maximum width so the composer stays inside the visible canvas region. */
.creative-composer-dock {
  left: calc((100% - var(--creative-history-reserve, 0px)) / 2);
  max-width: calc(100% - var(--creative-history-reserve, 0px) - 1.5rem);
  transition:
    left var(--motion-layout) var(--motion-ease),
    max-width var(--motion-layout) var(--motion-ease);
}

/* Use the same history reserve as the toolbar and composer so empty-state actions follow their shared available workspace width. */
.creative-empty-guide {
  right: calc(var(--creative-history-reserve, 0px) + 1rem);
  transition: right var(--motion-layout) var(--motion-ease);
}

.empty-guide-chip {
  @apply inline-flex h-9 items-center gap-2 rounded-full px-4 text-sm text-gray-700 transition-colors hover:text-gray-900;
  @apply dark:text-dark-100 dark:hover:text-white;
}

/* Runtime coordinates supply the flight vector from the send button to history. The animationend handler owns hiding this feedback and triggering the destination bump. */
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
    /* Keep a short animation even with reduced motion so animationend still drives submission feedback cleanup and the history destination response. */
    animation-duration: 1ms;
  }
}
</style>
