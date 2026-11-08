<template>
  <div
    class="canvas-island absolute right-3 top-3 z-20 rounded-surface p-1 transition-opacity duration-fast"
    :class="[bumping && 'history-bump', open && 'pointer-events-none opacity-0']"
    :inert="open"
    @animationend="onBumpEnd"
  >
    <button
      ref="triggerButtonRef"
      type="button"
      class="canvas-tool-btn relative"
      :class="open && 'canvas-tool-btn-active'"
      :title="t('creative.history.toggle')"
      :aria-expanded="open"
      @click="open = !open"
    >
      <Icon name="history" size="md" />
      <span
        v-if="props.activeRunCount > 0"
        class="absolute -right-1.5 -top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-primary-600 px-1 text-xs font-semibold leading-none text-white ring-2 ring-white tabular-nums dark:ring-dark-900"
      >
        {{ props.activeRunCount > 99 ? '99+' : props.activeRunCount }}
      </span>
    </button>
  </div>

  <MotionTransition name="fade">
    <div
      v-if="open"
      class="absolute inset-0 z-40 bg-[var(--overlay-bg)] md:hidden"
      aria-hidden="true"
      @click="open = false"
    ></div>
  </MotionTransition>

  <MotionTransition name="history-drawer">
    <aside
      v-if="open"
      class="canvas-island absolute bottom-3 right-3 top-3 z-40 flex w-[calc(100%-3.75rem)] max-w-md flex-col overflow-hidden rounded-surface max-md:bg-white max-md:dark:bg-dark-900 md:w-[var(--creative-history-w,20rem)] md:max-w-none"
      :aria-label="t('creative.history.title')"
    >
      <div class="flex items-center gap-1 border-b border-primary-900/8 p-1 dark:border-dark-600">
        <span
          ref="headerIconRef"
          class="inline-flex h-8 w-8 flex-shrink-0 items-center justify-center text-primary-600 dark:text-primary-500"
          :class="bumping && 'history-bump'"
          @animationend="onBumpEnd"
        >
          <Icon name="history" size="sm" :animate-on-hover="false" />
        </span>
        <h3 class="min-w-0 flex-1 truncate text-sm font-semibold text-gray-900 dark:text-white">
          {{ t('creative.history.title') }}
        </h3>
        <button
          type="button"
          class="canvas-tool-btn"
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
          ref="closeButtonRef"
          type="button"
          class="canvas-tool-btn"
          :title="t('common.close')"
          @click="open = false"
        >
          <Icon name="x" size="sm" />
        </button>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto p-1.5">
        <div v-if="studio.runHistory.value.length" class="space-y-0.5">
          <div
            v-for="run in studio.runHistory.value"
            :key="run.id"
            class="rounded-control transition-colors"
            :class="
              studio.currentRun.value?.id === run.id || expandedRunId === run.id
                ? 'bg-primary-500/8'
                : 'hover:bg-gray-50 dark:hover:bg-dark-800'
            "
          >
            <button type="button" class="w-full px-2.5 py-2 text-left" @click="toggleRun(run.id)">
              <div class="flex items-center gap-2">
                <span class="run-status flex-shrink-0" :class="`run-status-${statusTone(run)}`">
                  <Icon
                    v-if="isActive(run)"
                    name="loader"
                    size="xs"
                    class="animate-spin"
                    :animate-on-hover="false"
                  />
                  <span
                    v-else
                    class="h-1.5 w-1.5 rounded-full bg-current"
                    aria-hidden="true"
                  ></span>
                  {{ t(`creative.status.${run.status}`, run.status) }}
                </span>
                <span class="min-w-0 flex-1 truncate text-xs text-gray-500 dark:text-dark-300">{{
                  run.model
                }}</span>
                <Icon
                  name="chevronDown"
                  size="sm"
                  class="flex-shrink-0 text-gray-400 transition-transform dark:text-dark-400"
                  :class="expandedRunId === run.id && 'rotate-180'"
                  :animate-on-hover="false"
                />
              </div>
              <p
                v-if="paramsFor(run.id)?.prompt && expandedRunId !== run.id"
                class="mt-1 line-clamp-2 break-words text-xs text-gray-700 dark:text-dark-200"
              >
                {{ paramsFor(run.id)!.prompt }}
              </p>
              <div
                class="mt-1 flex items-center gap-2 text-xs tabular-nums text-gray-400 dark:text-dark-400"
              >
                <span :title="formatRunTime(run.created_at)">{{
                  formatRunRelative(run.created_at)
                }}</span>
                <span
                  v-if="formatElapsed(run)"
                  class="inline-flex shrink-0 items-center gap-1"
                  :aria-label="t('creative.history.elapsed', { time: formatElapsed(run) })"
                  :title="t('creative.history.elapsed', { time: formatElapsed(run) })"
                >
                  <Icon name="clock" size="xs" aria-hidden="true" />
                  <span>{{ formatElapsed(run) }}</span>
                </span>
                <span v-if="run.actual_cost != null" class="ml-auto">{{
                  t('creative.result.actualCost', {
                    cost: formatBalanceAmount(run.actual_cost, { fractionDigits: 3 }),
                  })
                }}</span>
              </div>
            </button>

            <MotionTransition name="history-details">
              <div v-if="expandedRunId === run.id" class="history-details-grid">
                <div class="min-h-0 overflow-hidden">
                  <div class="space-y-2 px-2.5 pb-2.5">
                    <div class="space-y-2 rounded-control bg-white p-2 dark:bg-dark-950">
                      <div v-if="paramsFor(run.id)" class="flex items-start gap-1">
                        <p
                          class="max-h-40 min-w-0 flex-1 overflow-y-auto whitespace-pre-wrap break-words text-xs leading-relaxed text-gray-800 dark:text-dark-100"
                        >
                          {{ paramsFor(run.id)!.prompt }}
                        </p>
                        <button
                          type="button"
                          class="history-param-btn"
                          :title="t('creative.history.copyPrompt')"
                          :aria-label="t('creative.history.copyPrompt')"
                          @click="
                            copyToClipboard(
                              paramsFor(run.id)!.prompt,
                              t('creative.history.promptCopied'),
                            )
                          "
                        >
                          <Icon name="copy" size="xs" />
                        </button>
                        <button
                          type="button"
                          class="history-param-btn"
                          :title="t('creative.history.reuseParams')"
                          :aria-label="t('creative.history.reuseParams')"
                          @click="reuseParams(run.id)"
                        >
                          <Icon name="edit" size="xs" />
                        </button>
                      </div>
                      <p v-else class="text-xs text-gray-400 dark:text-dark-400">
                        {{ t('creative.history.promptUnavailable') }}
                      </p>
                      <div class="flex flex-wrap gap-1">
                        <span
                          v-for="tag in paramTags(run)"
                          :key="tag.key"
                          class="history-param-tag"
                        >
                          <span v-if="tag.label" class="text-gray-400 dark:text-dark-400">{{
                            tag.label
                          }}</span>
                          {{ tag.value }}
                        </span>
                      </div>
                    </div>
                    <div
                      v-if="isActive(run)"
                      class="flex items-center gap-3 text-xs text-gray-500 dark:text-dark-300"
                    >
                      <div
                        class="flex h-16 w-16 flex-shrink-0 items-center justify-center rounded-control bg-white dark:bg-dark-950"
                      >
                        <Icon
                          name="loader"
                          size="md"
                          class="animate-spin text-primary-500"
                          :animate-on-hover="false"
                        />
                      </div>
                      <div class="min-w-0">
                        <span class="block">{{
                          t(`creative.status.${run.status}`, run.status)
                        }}</span>
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
                      <div
                        v-for="output in run.outputs"
                        :key="output.output_index"
                        class="history-output group relative flex w-full items-center justify-center overflow-hidden rounded-control bg-white dark:bg-dark-950"
                      >
                        <img
                          v-if="assetFor(run.id, output.output_index)"
                          :src="
                            urlForAsset(
                              outputAssetKey(run.id, output.output_index),
                              assetFor(run.id, output.output_index)!.blob,
                            )
                          "
                          :alt="`output-${output.output_index}`"
                          draggable="true"
                          class="block h-auto w-full cursor-grab select-none active:cursor-grabbing"
                          @dragstart.stop="onOutputDragStart($event, run.id, output.output_index)"
                        />
                        <div
                          v-else
                          class="flex h-24 w-full flex-col items-center justify-center gap-1 text-gray-400 dark:text-dark-500"
                        >
                          <Icon name="modalityImage" size="sm" />
                          <span class="text-xs">{{ t('creative.result.missing') }}</span>
                        </div>
                        <div
                          v-if="assetFor(run.id, output.output_index)"
                          class="history-output-actions"
                        >
                          <button
                            type="button"
                            class="history-output-btn"
                            :title="t('creative.history.importToCanvas')"
                            :aria-label="t('creative.history.importToCanvas')"
                            @click="importToCanvas(run.id, output.output_index)"
                          >
                            <Icon name="plus" size="sm" />
                          </button>
                          <button
                            type="button"
                            class="history-output-btn"
                            :title="t('creative.history.download')"
                            :aria-label="t('creative.history.download')"
                            @click="downloadOutput(run.id, output.output_index, output.mime_type)"
                          >
                            <Icon name="download" size="sm" />
                          </button>
                        </div>
                      </div>
                    </template>
                    <p v-else class="text-xs text-gray-400 dark:text-dark-400">
                      {{ t('creative.history.noOutputs') }}
                    </p>
                  </div>
                </div>
              </div>
            </MotionTransition>
          </div>
        </div>
        <p
          v-else-if="!studio.loadingHistory.value"
          class="py-8 text-center text-xs text-gray-400 dark:text-dark-400"
        >
          {{ t('creative.history.empty') }}
        </p>
      </div>
    </aside>
  </MotionTransition>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onKeyStroke, useMediaQuery } from '@vueuse/core'
import { saveAs } from 'file-saver'
import Icon from '@/components/icons/Icon.vue'
import { CREATIVE_RUN_TERMINAL_STATUSES, type CreativeRun } from '@/api/creative'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import { creativeTimestampToMs, formatCreativeRunElapsed } from '@/utils/creativeRunTime'
import { outputAssetKey, type LocalAsset, type LocalRunParams } from '@/utils/creativeLocalStore'
import { CREATIVE_OUTPUT_DRAG_MIME, serializeCreativeOutputDrag } from '@/utils/creativeDrag'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import { useClipboard } from '@/composables/useClipboard'
import { MEDIA_MAX_MD } from '@/constants/layout'
import type { useCreativeStudio } from '@/composables/useCreativeStudio'

/**
 * CreativeRunHistory.vue displays task status, submitted parameters, and
 * browser-local output images in the studio sidebar. The shared studio owns
 * server refresh and recovery, while this component expands one task and
 * creates preview URLs for its stored Blobs. Download and drag actions use the
 * same output identity that the canvas resolves locally.
 *
 * Expanded-row changes and sidebar closure revoke the preview URLs created by
 * the previous display. Active-task timing runs only while history contains
 * nonterminal work, and unmount clears the timer and remaining URLs. On narrow
 * screens successful import dispatch or parameter reuse closes the sidebar so
 * the canvas and composer become visible again. The parent receives open state
 * to reserve desktop workspace width.
 */

/**
 * Studio represents task state and actions shared with the generation page.
 */
type Studio = ReturnType<typeof useCreativeStudio>

/**
 * Props supplies studio history state and the active-task badge count.
 */
interface Props {
  /**
   * The parent owns this reactive studio object; the component reads its state
   * and invokes its actions while sharing the same refs.
   */
  studio: Studio
  activeRunCount?: number // active task badge count
}

/**
 * props, studio, and t connect shared task state to localized history content
 * and actions.
 */
const props = withDefaults(defineProps<Props>(), {
  activeRunCount: 0,
})
const studio = props.studio
const { t } = useI18n()
/**
 * Balance formatting and clipboard helpers apply account display preferences
 * and existing copy feedback to expanded task details.
 */
const { formatBalanceAmount } = useBalanceDisplay()
const { copyToClipboard } = useClipboard()

/**
 * open synchronizes sidebar visibility with the parent, or retains local
 * visibility when no model binding is supplied.
 */
const open = defineModel<boolean>('open', { default: false })
/**
 * isNarrow identifies overlay layouts where a successful import or parameter
 * reuse should reveal the underlying workspace.
 */
const isNarrow = useMediaQuery(MEDIA_MAX_MD)
/**
 * expandedRunId limits details to one task and triggers revocation of preview
 * URLs when the displayed task changes.
 */
const expandedRunId = ref<string | null>(null)
/**
 * refreshing tracks manual requests separately so a spinner render can precede
 * the shared history refresh.
 */
const refreshing = ref(false)
/**
 * triggerButtonRef, headerIconRef, and closeButtonRef provide animation
 * destinations and focus targets as sidebar visibility changes.
 */
const triggerButtonRef = ref<HTMLButtonElement | null>(null)
const headerIconRef = ref<HTMLSpanElement | null>(null)
const closeButtonRef = ref<HTMLButtonElement | null>(null)
/**
 * historyButtonRef chooses the visible history icon used as the parent
 * submission animation's destination.
 */
const historyButtonRef = computed<HTMLElement | null>(() =>
  open.value ? headerIconRef.value : triggerButtonRef.value,
)
/**
 * bumping controls the destination feedback class and resets when the named
 * history animation ends.
 */
const bumping = ref(false)

/**
 * bump restarts the history-entry feedback animation on the next browser frame.
 * Clearing the class first lets consecutive accepted submissions produce a new
 * animation, whose end event resets the flag.
 */
function bump(): void {
  bumping.value = false
  /**
   * Reapply the bump class in a later frame so the browser observes its removal
   * and restarts feedback for consecutive submissions.
   */
  requestAnimationFrame(() => {
    bumping.value = true
  })
}

/**
 * onBumpEnd clears feedback when the history bump animation completes. Events
 * from any other animation name are ignored, so unrelated motion cannot reset
 * the current destination feedback state.
 */
function onBumpEnd(event: AnimationEvent): void {
  if (event.animationName === 'history-bump') bumping.value = false
}

defineExpose({ historyButtonRef, bump })
/**
 * elapsedNow and elapsedTimer advance task duration labels only while the
 * shared history includes active work.
 */
const elapsedNow = ref(Date.now())
let elapsedTimer: ReturnType<typeof setInterval> | null = null

/**
 * expandedUrls owns preview URLs keyed by local asset identity and releases
 * them when expanded content is replaced.
 */
const expandedUrls = new Map<string, string>()

/**
 * toggleRun expands one task or collapses its already expanded details.
 */
function toggleRun(runId: string): void {
  expandedRunId.value = expandedRunId.value === runId ? null : runId
}

/**
 * assetFor resolves an output identity against the studio's local asset index.
 * Missing records return null so the history row can render unavailable output
 * feedback instead of an image.
 */
function assetFor(runId: string, outputIndex: number): LocalAsset | null {
  return studio.outputAssetMap.value.get(outputAssetKey(runId, outputIndex)) ?? null
}

/**
 * paramsFor returns locally remembered submission parameters for a server run.
 * Missing records return null, leaving the history renderer to use server
 * metadata for fields that remain available.
 */
function paramsFor(runId: string): LocalRunParams | null {
  return studio.runParamsMap.value.get(runId) ?? null
}

/**
 * ParamTag describes one nonempty submitted parameter shown under an expanded
 * task.
 */
interface ParamTag {
  key: string // parameter tag identifier
  /**
   * Operation tags omit the label because their translated value already
   * identifies the image operation to the user.
   */
  label?: string
  value: string // displayed parameter value
}

/**
 * paramTags combines server run metadata with locally remembered submission
 * parameters for display. Local size and ratio values take precedence when
 * present, while operation and requested output count remain available from the
 * run itself. Browser-only fields such as group, quality, and reference count
 * appear only when their local values exist. Empty tags are omitted, and
 * translated option values retain their raw values as the translation fallback.
 */
function paramTags(run: CreativeRun): ParamTag[] {
  const params = paramsFor(run.id)
  const tags: ParamTag[] = []
  /**
   * push appends a parameter tag only when its display value is nonempty.
   */
  const push = (key: string, value: string | undefined, label?: string) => {
    if (value) tags.push({ key, label, value })
  }
  push('operation', t(`creative.operations.${run.operation}`, run.operation))
  push('group', params?.groupName, t('creative.history.params.group'))
  push('size', params?.imageSize || run.image_size, t('creative.history.params.size'))
  if (run.output_format === 'gif' || params?.outputFormat === 'gif') {
    push('format', 'GIF', t('creative.panel.outputFormat'))
    push(
      'frames',
      String(run.frame_count ?? params?.frameCount ?? 9),
      t('creative.panel.frameCount'),
    )
    push(
      'duration',
      String(run.frame_duration_ms ?? params?.frameDurationMS ?? 200),
      t('creative.panel.frameDuration'),
    )
  }
  const ratio = params?.aspectRatio || run.aspect_ratio
  push(
    'ratio',
    ratio === 'auto' ? t('creative.composer.autoRatio') : ratio,
    t('creative.history.params.ratio'),
  )
  if (params?.quality)
    push(
      'quality',
      t(`creative.qualities.${params.quality}`, params.quality),
      t('creative.panel.quality'),
    )
  if (params?.background)
    push(
      'background',
      t(`creative.backgrounds.${params.background}`, params.background),
      t('creative.panel.background'),
    )
  if (params?.thinkingLevel)
    push(
      'thinking',
      t(`creative.thinkingLevels.${params.thinkingLevel}`, params.thinkingLevel),
      t('creative.history.params.thinking'),
    )
  if (params?.referenceCount)
    push('references', String(params.referenceCount), t('creative.history.params.references'))
  if (run.requested_output_count > 1)
    push('count', String(run.requested_output_count), t('creative.history.params.count'))
  return tags
}

/**
 * reuseParams asks the studio to restore one task's saved form values. A
 * successful application closes a narrow sidebar, while missing local
 * parameters leave the panel and form unchanged.
 */
function reuseParams(runId: string): void {
  const applied = studio.applyRunParams(runId)
  if (applied && isNarrow.value) open.value = false
}

/**
 * urlForAsset reuses or creates a preview URL for an output asset key. This
 * component owns the cached URL and revokes it when expanded content changes,
 * the sidebar closes, or the component unmounts.
 */
function urlForAsset(key: string, blob: Blob): string {
  const cached = expandedUrls.get(key)
  if (cached) return cached
  const url = URL.createObjectURL(blob)
  expandedUrls.set(key, url)
  return url
}

/**
 * revokeExpandedUrls revokes every cached preview URL and empties the key map.
 * Call it when the displayed task changes or its sidebar lifetime ends so Blob
 * resources can be released.
 */
function revokeExpandedUrls(): void {
  expandedUrls.forEach((url) => URL.revokeObjectURL(url))
  expandedUrls.clear()
}

/**
 * Changing expanded rows releases URLs from the previous task; closing the
 * panel and unmounting also release any remaining preview resources.
 */
watch(expandedRunId, revokeExpandedUrls)
watch(open, async (value) => {
  if (!value) {
    expandedRunId.value = null
    revokeExpandedUrls()
  }
  /**
   * Wait for visibility updates before moving focus because the open-state
   * template makes the hidden trigger inert while the close button becomes
   * available.
   */
  await nextTick()
  if (value) {
    closeButtonRef.value?.focus({ preventScroll: true })
  } else if (!document.activeElement || document.activeElement === document.body) {
    triggerButtonRef.value?.focus({ preventScroll: true })
  }
})
onKeyStroke('Escape', (event) => {
  if (!open.value || event.defaultPrevented) return
  open.value = false
})
/**
 * Run one elapsed timer while any task remains active, then stop it when the
 * final task reaches a terminal status.
 */
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
/**
 * Release the interval and all remaining Blob URLs when the panel unmounts,
 * ending both periodic updates and preview resource ownership.
 */
onBeforeUnmount(() => {
  if (elapsedTimer) {
    clearInterval(elapsedTimer)
    elapsedTimer = null
  }
  revokeExpandedUrls()
})

/**
 * importToCanvas dispatches a local output through the studio canvas bridge. A
 * successful dispatch closes the narrow sidebar; the studio's boolean describes
 * immediate dispatch and does not await asynchronous placement completion.
 */
function importToCanvas(runId: string, outputIndex: number): void {
  const imported = studio.importOutputToCanvas(runId, outputIndex)
  if (imported && isNarrow.value) open.value = false
}

/**
 * onOutputDragStart writes a validated local output identity into the private
 * drag type. Missing local bytes or DataTransfer makes the operation a no-op,
 * and the receiver loads image bytes from IndexedDB.
 */
function onOutputDragStart(event: DragEvent, runId: string, outputIndex: number): void {
  const asset = assetFor(runId, outputIndex)
  if (!asset || !event.dataTransfer) return
  event.dataTransfer.effectAllowed = 'copy'
  event.dataTransfer.setData(
    CREATIVE_OUTPUT_DRAG_MIME,
    serializeCreativeOutputDrag({ runId, outputIndex }),
  )
}

/**
 * downloadOutput downloads stored output bytes using the run and index in the
 * filename. The requested MIME type takes precedence over Blob metadata, with
 * PNG used when neither supplies an extension.
 */
function downloadOutput(runId: string, outputIndex: number, mimeType?: string): void {
  const asset = assetFor(runId, outputIndex)
  if (!asset) return
  const extension = (mimeType || asset.blob.type || 'image/png').split('/')[1] || 'png'
  /**
   * Keep the filename compact with a twelve-character run prefix.
   */
  saveAs(asset.blob, `creative-${runId.slice(0, 12)}-${outputIndex}.${extension}`)
}

/**
 * isActive reports whether the run remains outside the terminal status set.
 */
function isActive(run: CreativeRun): boolean {
  return !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)
}

/**
 * statusTone maps task progress and outcomes to history row color categories.
 */
function statusTone(run: CreativeRun): 'active' | 'success' | 'danger' | 'muted' {
  if (isActive(run)) return 'active'
  if (run.status === 'succeeded') return 'success'
  if (run.status === 'failed' || run.status === 'result_lost') return 'danger'
  return 'muted'
}

/**
 * formatRunTime formats a valid server timestamp using the shared date-time
 * formatter. Missing or nonfinite timestamps produce empty text after
 * normalization so history omits an unavailable time label.
 */
function formatRunTime(timestamp: number | undefined): string {
  const ms = creativeTimestampToMs(timestamp)
  if (ms == null) return ''
  return formatDateTime(new Date(ms))
}

/**
 * formatRunRelative formats a valid server timestamp relative to the current
 * clock. Missing or nonfinite timestamps produce empty text, sharing the same
 * timestamp normalization as the absolute time label.
 */
function formatRunRelative(timestamp: number | undefined): string {
  const ms = creativeTimestampToMs(timestamp)
  if (ms == null) return ''
  return formatRelativeTime(new Date(ms))
}

/**
 * formatElapsed formats task duration using the history panel's active-task
 * clock.
 */
function formatElapsed(run: CreativeRun): string {
  return formatCreativeRunElapsed(run, elapsedNow.value)
}

/**
 * refresh performs a manual history refresh while guarding against concurrent
 * refreshes. It publishes spinner state before awaiting the studio request and
 * resets that state in finally even if refresh rejects.
 */
async function refresh(): Promise<void> {
  if (refreshing.value || studio.loadingHistory.value) return
  refreshing.value = true
  /**
   * Publish manual-refresh state before calling the studio so even a fast
   * response can follow a rendered spinner update.
   */
  await nextTick()
  try {
    await studio.refreshHistory()
  } finally {
    refreshing.value = false
  }
}
</script>

<style scoped>
.history-drawer-enter-active {
  transition:
    transform var(--motion-layout) var(--motion-ease),
    opacity var(--motion-layout) var(--motion-ease);
}

.history-drawer-leave-active {
  transition:
    transform var(--motion-exit) var(--motion-ease-exit),
    opacity var(--motion-exit) var(--motion-ease-exit);
}

.history-drawer-enter-from,
.history-drawer-leave-to {
  opacity: 0;
  transform: translateX(calc(100% + 0.75rem));
}

/* A grid track follows expanded content height while the collapsed state uses zero fractions, allowing task details to animate without a measured fixed height. */
.history-details-grid {
  display: grid;
  grid-template-rows: 1fr;
}

.history-details-enter-active,
.history-details-leave-active {
  overflow: hidden;
  transition:
    grid-template-rows var(--motion-layout) var(--motion-ease),
    opacity var(--motion-layout) var(--motion-ease);
}

.history-details-enter-from,
.history-details-leave-to {
  grid-template-rows: 0fr;
  opacity: 0;
}

.run-status {
  @apply inline-flex items-center gap-1.5 text-xs font-medium;
}

.run-status-active {
  @apply text-primary-700 dark:text-primary-500;
}

.run-status-success {
  @apply text-green-600 dark:text-green-400;
}

.run-status-danger {
  @apply text-red-600 dark:text-red-400;
}

.run-status-muted {
  @apply text-gray-500 dark:text-dark-300;
}

.history-param-btn {
  @apply inline-flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-control text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100;
}

.history-param-tag {
  @apply inline-flex items-center gap-1 rounded-control bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700 dark:bg-dark-800 dark:text-dark-200;
}

/* Hover-capable devices reveal actions on hover or keyboard focus. The hover-none override keeps those controls visible for touch interaction. */
.history-output-actions {
  @apply absolute right-1.5 top-1.5 flex gap-1 opacity-0 transition-opacity duration-fast;
}

.history-output:hover .history-output-actions,
.history-output:focus-within .history-output-actions {
  @apply opacity-100;
}

@media (hover: none) {
  .history-output-actions {
    @apply opacity-100;
  }
}

.history-output-btn {
  @apply inline-flex h-8 w-8 items-center justify-center rounded-control bg-gray-900/60 text-white backdrop-blur transition-colors hover:bg-gray-900/80;
}

.history-bump {
  animation: history-bump var(--motion-fast) var(--motion-ease);
}

@keyframes history-bump {
  50% {
    transform: scale(1.12);
  }
}

/* Reduce drawer exit transitions as well as entry and detail motion, since the exit-duration token has its own timing value. */
@media (prefers-reduced-motion: reduce) {
  .history-drawer-enter-active,
  .history-drawer-leave-active,
  .history-details-enter-active,
  .history-details-leave-active {
    transition-duration: 1ms;
  }

  .history-bump {
    animation-duration: 1ms;
  }
}
</style>
