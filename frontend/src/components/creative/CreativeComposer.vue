<template>
  <div
    ref="rootRef"
    class="composer-shell canvas-island relative w-[min(600px,calc(100vw-2rem))] max-w-full"
  >
    <Collapse :open="statusLine !== null" unmount-on-hide>
      <div
        v-if="statusLine"
        class="flex h-8 items-center gap-2 border-b border-primary-900/8 px-4 text-xs dark:border-dark-600"
        :class="STATUS_TONE_CLASSES[statusLine.tone]"
        role="status"
        aria-live="polite"
        data-testid="creative-status-line"
      >
        <Icon
          :name="statusLine.icon"
          size="xs"
          class="flex-shrink-0"
          :class="statusLine.tone === 'active' && 'animate-spin'"
          :animate-on-hover="false"
        />
        <span class="whitespace-nowrap font-medium">{{ statusLine.text }}</span>
        <span
          v-if="statusLine.detail"
          class="min-w-0 truncate text-gray-500 dark:text-dark-400"
          :title="statusLine.detail"
          >{{ statusLine.detail }}</span
        >
        <span
          v-if="statusLine.elapsed"
          class="ml-auto flex-shrink-0 tabular-nums text-gray-400 dark:text-dark-400"
          >{{ statusLine.elapsed }}</span
        >
      </div>
    </Collapse>

    <textarea
      ref="textareaRef"
      v-model="prompt"
      rows="2"
      class="composer-textarea block w-full resize-none bg-transparent px-4 pb-1.5 pt-3.5 text-sm leading-relaxed text-gray-900 outline-none placeholder:text-gray-400 dark:text-dark-50 dark:placeholder:text-dark-400"
      :class="studio.busy.value && 'opacity-60'"
      :placeholder="t('creative.panel.promptPlaceholder')"
      @keydown="onKeydown"
    ></textarea>

    <div class="flex items-center gap-1 px-2 pb-2">
      <span class="relative min-w-0">
        <button
          type="button"
          class="composer-chip"
          :class="openPanel === 'model' && 'composer-chip-active'"
          :title="t('creative.composer.model')"
          :aria-expanded="openPanel === 'model'"
          @click="togglePanel('model', $event)"
        >
          <ModelIcon
            v-if="selectedModelName"
            :model="selectedModelName"
            size="14px"
            class="flex-shrink-0"
          />
          <Icon v-else name="sparkles" size="sm" class="flex-shrink-0" />
          <span class="max-w-32 truncate">{{ modelChipLabel }}</span>
          <Icon
            name="chevronDown"
            size="xs"
            class="flex-shrink-0 opacity-60 transition-transform"
            :class="openPanel === 'model' && 'rotate-180'"
            :animate-on-hover="false"
          />
        </button>
        <MotionTransition name="pop-float">
          <div v-if="openPanel === 'model'" class="chip-popover" :style="popoverStyle">
            <div class="max-h-menu overflow-y-auto p-1.5">
              <p
                v-if="showModelsEmptyHint"
                class="px-2.5 py-2 text-xs text-gray-500 dark:text-dark-400"
              >
                {{ modelsEmptyHintText }}
              </p>
              <button
                v-for="option in studio.models.value"
                :key="creativeOptionKey(option)"
                type="button"
                class="composer-option"
                :class="
                  studio.selectedOptionKey.value === creativeOptionKey(option) &&
                  'composer-option-active'
                "
                @click="selectModel(option)"
              >
                <ModelIcon :model="option.model" size="16px" class="flex-shrink-0" />
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm font-medium">{{ option.model }}</span>
                  <span
                    class="block truncate text-xs font-normal text-gray-400 dark:text-dark-400"
                    >{{ option.group_name }}</span
                  >
                </span>
                <Icon
                  v-if="studio.selectedOptionKey.value === creativeOptionKey(option)"
                  name="check"
                  size="sm"
                  class="flex-shrink-0"
                  :animate-on-hover="false"
                />
              </button>
            </div>
          </div>
        </MotionTransition>
      </span>

      <span class="relative min-w-0">
        <button
          type="button"
          class="composer-chip"
          :class="openPanel === 'params' && 'composer-chip-active'"
          :title="t('creative.composer.params')"
          :aria-expanded="openPanel === 'params'"
          @click="togglePanel('params', $event)"
        >
          <Icon name="sliders" size="sm" class="flex-shrink-0" />
          <span class="max-w-40 truncate tabular-nums">{{ paramsChipLabel }}</span>
          <Icon
            name="chevronDown"
            size="xs"
            class="flex-shrink-0 opacity-60 transition-transform"
            :class="openPanel === 'params' && 'rotate-180'"
            :animate-on-hover="false"
          />
        </button>
        <MotionTransition name="pop-float">
          <div v-if="openPanel === 'params'" class="chip-popover" :style="popoverStyle">
            <div class="max-h-[min(70vh,32rem)] space-y-4 overflow-y-auto p-3">
              <div
                v-if="!studio.selectedOption.value"
                class="text-xs text-gray-500 dark:text-dark-400"
              >
                {{ t('creative.composer.selectModelFirst') }}
              </div>
              <template v-for="group in leadingParamGroups" :key="group.key">
                <div>
                  <p class="param-label">{{ group.label }}</p>
                  <SettingsSegmented
                    v-if="group.options.length <= SEGMENTED_MAX_OPTIONS"
                    :model-value="group.value"
                    :options="group.options"
                    :ariaLabel="group.label"
                    block
                    @update:model-value="(value) => selectParam(group, value)"
                  />
                  <div v-else class="flex flex-wrap gap-1.5">
                    <button
                      v-for="option in group.options"
                      :key="option.value"
                      type="button"
                      class="param-chip"
                      :class="group.value === option.value && 'param-chip-active'"
                      @click="group.select(option.value)"
                    >
                      {{ option.label }}
                    </button>
                  </div>
                </div>
              </template>
              <div v-if="studio.outputFormatOptions.value.length > 1">
                <p class="param-label">{{ t('creative.panel.outputFormat') }}</p>
                <SettingsSegmented
                  v-model="studio.outputFormat.value"
                  :options="
                    studio.outputFormatOptions.value.map((value) => ({
                      value,
                      label: value.toUpperCase(),
                    }))
                  "
                  :ariaLabel="t('creative.panel.outputFormat')"
                  block
                />
              </div>
              <div v-if="studio.outputFormat.value === 'gif'" class="grid grid-cols-2 gap-3">
                <label class="param-label">
                  {{ t('creative.panel.frameCount') }}
                  <input
                    v-model.number="studio.frameCount.value"
                    type="number"
                    class="input mt-1"
                    :min="studio.selectedOption.value?.gif_frame_count?.min ?? 2"
                    :max="studio.selectedOption.value?.gif_frame_count?.max ?? 16"
                    step="1"
                  />
                </label>
                <label class="param-label">
                  {{ t('creative.panel.frameDuration') }}
                  <input
                    v-model.number="studio.frameDurationMS.value"
                    type="number"
                    class="input mt-1"
                    :min="studio.selectedOption.value?.gif_frame_duration_ms?.min ?? 20"
                    :max="studio.selectedOption.value?.gif_frame_duration_ms?.max ?? 2000"
                    step="10"
                  />
                </label>
              </div>
              <div v-if="studio.aspectRatioOptions.value.length">
                <p class="param-label">{{ t('creative.panel.aspectRatio') }}</p>
                <div class="flex flex-wrap gap-1.5">
                  <button
                    v-for="ratio in studio.aspectRatioOptions.value"
                    :key="ratio"
                    type="button"
                    class="param-chip"
                    :class="studio.aspectRatio.value === ratio && 'param-chip-active'"
                    @click="setAspectRatio(ratio)"
                  >
                    <Icon
                      v-if="ratio === 'auto'"
                      name="sparkles"
                      size="xs"
                      class="opacity-70"
                      aria-hidden="true"
                    />
                    <span v-else class="ratio-preview" :style="ratioPreviewStyle(ratio)"></span>
                    {{ ratio === 'auto' ? t('creative.composer.autoRatio') : ratio }}
                  </button>
                </div>
              </div>
              <template v-for="group in trailingParamGroups" :key="group.key">
                <div>
                  <p class="param-label">{{ group.label }}</p>
                  <SettingsSegmented
                    v-if="group.options.length <= SEGMENTED_MAX_OPTIONS"
                    :model-value="group.value"
                    :options="group.options"
                    :ariaLabel="group.label"
                    block
                    @update:model-value="(value) => selectParam(group, value)"
                  />
                  <div v-else class="flex flex-wrap gap-1.5">
                    <button
                      v-for="option in group.options"
                      :key="option.value"
                      type="button"
                      class="param-chip"
                      :class="group.value === option.value && 'param-chip-active'"
                      @click="group.select(option.value)"
                    >
                      {{ option.label }}
                    </button>
                  </div>
                </div>
              </template>
            </div>
          </div>
        </MotionTransition>
      </span>

      <span class="relative min-w-0">
        <button
          type="button"
          class="composer-chip"
          :class="openPanel === 'operation' && 'composer-chip-active'"
          :title="t('creative.composer.operation')"
          :aria-expanded="openPanel === 'operation'"
          @click="togglePanel('operation', $event)"
        >
          <Icon :name="OPERATION_ICONS[studio.operation.value]" size="sm" class="flex-shrink-0" />
          <span class="max-w-24 truncate">{{ operationChipLabel }}</span>
          <Icon
            name="chevronDown"
            size="xs"
            class="flex-shrink-0 opacity-60 transition-transform"
            :class="openPanel === 'operation' && 'rotate-180'"
            :animate-on-hover="false"
          />
        </button>
        <MotionTransition name="pop-float">
          <div v-if="openPanel === 'operation'" class="chip-popover" :style="popoverStyle">
            <div class="p-1.5">
              <p
                v-if="!studio.operationOptions.value.length"
                class="px-2.5 py-2 text-xs text-gray-500 dark:text-dark-400"
              >
                {{ t('creative.composer.selectModelFirst') }}
              </p>
              <button
                v-for="op in studio.operationOptions.value"
                :key="op"
                type="button"
                class="composer-option"
                :class="studio.operation.value === op && 'composer-option-active'"
                @click="selectOperation(op)"
              >
                <Icon :name="OPERATION_ICONS[op]" size="sm" class="flex-shrink-0" />
                <span class="min-w-0 flex-1">
                  <span class="block text-sm font-medium">{{
                    t(`creative.operations.${op}`, op)
                  }}</span>
                  <span class="block text-xs font-normal text-gray-400 dark:text-dark-400">{{
                    t(`creative.operationsDesc.${op}`)
                  }}</span>
                </span>
                <Icon
                  v-if="studio.operation.value === op"
                  name="check"
                  size="sm"
                  class="flex-shrink-0"
                  :animate-on-hover="false"
                />
              </button>
            </div>
          </div>
        </MotionTransition>
      </span>

      <div class="ml-auto flex items-center gap-3">
        <i18n-t
          v-if="estimatedCostText !== null"
          keypath="creative.panel.estimatedCost"
          tag="span"
          scope="global"
          class="composer-cost"
        >
          <template #cost>
            <span class="composer-cost-amount">
              <span class="composer-cost-unit">{{ balanceUnitSymbol }}</span>
              <span>{{ estimatedCostText }}</span>
            </span>
          </template>
        </i18n-t>
        <button
          ref="sendButtonRef"
          type="button"
          class="composer-send"
          :disabled="!studio.canGenerate.value"
          :title="t('creative.composer.send')"
          @click="emit('generate')"
        >
          <Icon
            v-if="studio.busy.value"
            name="loader"
            size="sm"
            class="animate-spin"
            :animate-on-hover="false"
          />
          <Icon v-else name="arrowUp" size="sm" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onClickOutside, useEventListener } from '@vueuse/core'
import Collapse from '@/components/common/Collapse.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import ModelIcon from '@/components/common/ModelIcon.vue'
import SettingsSegmented from '@/components/common/settings/SettingsSegmented.vue'
import Icon from '@/components/icons/Icon.vue'
import type { IconName } from '@/components/icons/registry'
import { useAppStore } from '@/stores/app'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { CreativeCanvasGuide, useCreativeStudio } from '@/composables/useCreativeStudio'
import { creativeOptionKey } from '@/composables/useCreativeStudio'
import {
  CREATIVE_RUN_TERMINAL_STATUSES,
  type CreativeModelOption,
  type CreativeOperation,
} from '@/api/creative'
import { formatCreativeRunElapsed } from '@/utils/creativeRunTime'

/**
 * CreativeComposer.vue presents the generation prompt, model choices, and
 * parameter controls shared with useCreativeStudio. The composer writes
 * directly to the supplied reactive studio and emits generation requests for
 * the parent to collect canvas inputs. Parameter popovers are sized and
 * positioned within the composer, with narrow layouts clamped against the
 * viewport.
 *
 * One status line prioritizes explicit errors, briefly visible failed terminal
 * results, missing canvas input, and active task progress. Active tasks are
 * selected from history so another running request remains visible after the
 * most recently submitted task completes. Separate timers control elapsed-time
 * updates and terminal feedback, and both are cleared when the component
 * unmounts.
 */

/**
 * Studio represents the shared reactive generation controls returned by
 * useCreativeStudio.
 */
type Studio = ReturnType<typeof useCreativeStudio>

/**
 * Props supplies shared studio state and optional missing-input guidance from
 * the canvas.
 */
interface Props {
  /**
   * The parent owns this reactive studio object; the component reads its state
   * and invokes its actions while sharing the same refs.
   */
  studio: Studio
  /**
   * Canvas guidance is displayed after errors and terminal feedback, before
   * ordinary active-task progress in the status line.
   */
  guide?: CreativeCanvasGuide | null
}

/**
 * Emits requests generation after the parent collects current canvas image
 * inputs.
 */
interface Emits {
  /**
   * The generate event asks the parent to collect current canvas inputs and
   * submit a task. This component owns prompt controls while the parent owns
   * source and mask extraction.
   */
  (e: 'generate'): void
}

/**
 * props, studio, emit, and t bind shared generation state and translated
 * controls to parent submission events.
 */
const props = withDefaults(defineProps<Props>(), {
  guide: null,
})
const studio = props.studio
const emit = defineEmits<Emits>()
const { t } = useI18n()
/**
 * appStore and balance display helpers provide feature availability and the
 * current account currency presentation.
 */
const appStore = useAppStore()
const { formatBalanceAmount, balanceUnitSymbol } = useBalanceDisplay()

/**
 * estimatedCostText rounds displayed estimates to three decimals and trims
 * insignificant zeros before applying account currency formatting.
 */
const estimatedCostText = computed(() => {
  const cost = studio.estimatedCost.value
  if (cost === null) return null
  /**
   * Round estimates before choosing display precision so trailing zeros can be
   * removed without showing more than three fractional digits.
   */
  const normalized = Number(cost.toFixed(3))
  let fractionDigits = 0
  while (fractionDigits < 3 && Number(normalized.toFixed(fractionDigits)) !== normalized) {
    fractionDigits += 1
  }
  return formatBalanceAmount(normalized, { fractionDigits, withSymbol: false })
})

/**
 * TEXTAREA_MAX_HEIGHT, SEGMENTED_MAX_OPTIONS, and TERMINAL_STATUS_VISIBLE_MS
 * bound prompt expansion, choice layout, and temporary failure feedback.
 */
const TEXTAREA_MAX_HEIGHT = 160
const SEGMENTED_MAX_OPTIONS = 5
const TERMINAL_STATUS_VISIBLE_MS = 5000

/**
 * OPERATION_ICONS maps each generation operation to the icon displayed on its
 * selector chip.
 */
const OPERATION_ICONS: Record<CreativeOperation, IconName> = {
  generate: 'sparkles',
  edit: 'modalityImage',
  inpaint: 'brush',
}

/**
 * rootRef, textareaRef, and sendButtonRef connect popover positioning, prompt
 * focus, and parent submission animation to mounted elements.
 */
const rootRef = ref<HTMLDivElement | null>(null)
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const sendButtonRef = ref<HTMLButtonElement | null>(null)
/**
 * openPanel allows one model, parameter, or operation popover at a time and
 * closes on outside interaction.
 */
const openPanel = ref<'model' | 'params' | 'operation' | null>(null)
/**
 * popoverStyle and panelAnchor keep the active popover inside the composer as
 * window dimensions change.
 */
const popoverStyle = ref<{
  left: string // chip-relative CSS pixel offset
  width: string // horizontal dimension pixels
}>({ left: '0px', width: '' })
let panelAnchor: HTMLElement | null = null

/**
 * Close the active panel and clear its positioning anchor together so
 * subsequent resize events do not reposition a dismissed popover.
 */
onClickOutside(rootRef, () => {
  openPanel.value = null
  panelAnchor = null
})

/**
 * prompt proxies the studio prompt so text entry and parent example selection
 * share the same value.
 */
const prompt = computed({
  get: () => studio.prompt.value,
  set: (value: string) => {
    studio.prompt.value = value
  },
})

/**
 * StatusTone selects the visual treatment of the composer status message.
 */
type StatusTone = 'active' | 'danger' | 'muted' | 'guide'

/**
 * StatusLine describes the prioritized composer message. tone, icon, and text
 * define its main display; detail and elapsed supply additional context only
 * for status paths that provide them.
 */
interface StatusLine {
  tone: StatusTone // status text treatment
  icon: IconName // status indicator icon
  text: string // primary status message
  detail?: string // optional supporting status text
  elapsed?: string // optional formatted elapsed time
}

/**
 * STATUS_TONE_CLASSES assigns text colors to active work, failures, muted
 * results, and canvas guidance messages.
 */
const STATUS_TONE_CLASSES: Record<StatusTone, string> = {
  active: 'text-primary-700 dark:text-primary-500',
  danger: 'text-red-600 dark:text-red-400',
  muted: 'text-gray-600 dark:text-dark-300',
  guide: 'text-gray-700 dark:text-dark-100',
}

/**
 * terminalStatusVisible and terminalStatusTimer briefly retain failed,
 * cancelled, or lost-result feedback after the current task changes status.
 */
const terminalStatusVisible = ref(false)
let terminalStatusTimer: ReturnType<typeof setTimeout> | null = null
/**
 * now and clockTimer update elapsed text once per second while history still
 * contains an active task.
 */
const now = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | null = null

/**
 * activeRun finds the first nonterminal history task so concurrent work remains
 * visible after the current task completes.
 */
const activeRun = computed(
  () =>
    studio.runHistory.value.find((run) => !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)) ??
    null,
)

/**
 * statusLine gives errors precedence over temporary terminal feedback, canvas
 * guidance, and current generation progress in that order.
 */
const statusLine = computed<StatusLine | null>(() => {
  if (studio.error.value) {
    return { tone: 'danger', icon: 'exclamationCircle', text: studio.error.value }
  }
  const run = studio.currentRun.value
  if (run && terminalStatusVisible.value) {
    const cancelled = run.status === 'cancelled'
    return {
      tone: cancelled ? 'muted' : 'danger',
      icon: cancelled ? 'ban' : 'exclamationCircle',
      text: t(`creative.status.${run.status}`, run.status),
      detail: run.error_message,
    }
  }
  if (props.guide) {
    return { tone: 'guide', icon: 'infoCircle', text: t(`creative.canvas.${props.guide}Hint`) }
  }
  if (studio.polling.value || studio.busy.value) {
    const status = activeRun.value?.status
    const phase = status ?? 'submitting'
    return {
      tone: 'active',
      icon: 'loader',
      text: t(`creative.status.${phase}`),
      detail: activeRun.value?.model,
      elapsed: activeRun.value ? formatCreativeRunElapsed(activeRun.value, now.value) : '',
    }
  }
  return null
})

/**
 * Replace the terminal feedback timer when the current status changes so an
 * older timeout cannot hide a later failure message early.
 */
watch(
  () => studio.currentRun.value?.status,
  (status, previous) => {
    if (!status || status === previous) return
    if (terminalStatusTimer) clearTimeout(terminalStatusTimer)
    terminalStatusTimer = null
    terminalStatusVisible.value =
      status === 'failed' || status === 'cancelled' || status === 'result_lost'
    if (terminalStatusVisible.value) {
      terminalStatusTimer = setTimeout(() => {
        terminalStatusVisible.value = false
      }, TERMINAL_STATUS_VISIBLE_MS)
    }
  },
)

/**
 * Start one elapsed clock while an active history task exists and stop it after
 * the last active task leaves the list.
 */
watch(
  activeRun,
  (run) => {
    if (run && !clockTimer) {
      now.value = Date.now()
      clockTimer = setInterval(() => {
        now.value = Date.now()
      }, 1000)
    } else if (!run && clockTimer) {
      clearInterval(clockTimer)
      clockTimer = null
    }
  },
  { immediate: true },
)

/**
 * Clear both display timers when the composer unmounts so hidden component
 * state stops receiving terminal-feedback and elapsed-clock updates.
 */
onBeforeUnmount(() => {
  if (terminalStatusTimer) clearTimeout(terminalStatusTimer)
  if (clockTimer) clearInterval(clockTimer)
})

/**
 * showModelsEmptyHint and modelsEmptyHintText distinguish a completed empty
 * catalog from an administrator-disabled creative feature.
 */
const showModelsEmptyHint = computed(
  () => !studio.loadingModels.value && studio.models.value.length === 0,
)

const modelsEmptyHintText = computed(() =>
  appStore.cachedPublicSettings?.creative_enabled === false
    ? t('creative.panel.studioDisabled')
    : t('creative.panel.noModelsAvailable'),
)

/**
 * selectedModelName and modelChipLabel supply the current model identity or the
 * prompt to select a model.
 */
const selectedModelName = computed(() => studio.selectedOption.value?.model ?? null)

const modelChipLabel = computed(() => {
  const option = studio.selectedOption.value
  return option ? option.model : t('creative.composer.selectModel')
})

/**
 * paramsChipLabel combines supported current size, ratio, and quality values
 * into the compact parameter selector label.
 */
const paramsChipLabel = computed(() => {
  const parts: string[] = []
  if (studio.imageSizeOptions.value.length && studio.imageSize.value)
    parts.push(studio.imageSize.value)
  if (studio.aspectRatioOptions.value.length && studio.aspectRatio.value) {
    parts.push(
      studio.aspectRatio.value === 'auto'
        ? t('creative.composer.autoRatio')
        : studio.aspectRatio.value,
    )
  }
  if (studio.qualityOptions.value.length && studio.quality.value) {
    parts.push(t(`creative.qualities.${studio.quality.value}`, studio.quality.value))
  }
  return parts.length ? parts.join(' · ') : t('creative.composer.params')
})

/**
 * operationChipLabel translates the current operation with its raw value
 * available when a translation is absent.
 */
const operationChipLabel = computed(() =>
  t(`creative.operations.${studio.operation.value}`, studio.operation.value),
)

/**
 * ParamGroup connects one model parameter to its value, available options, and
 * select callback. The composer filters groups with empty options and uses
 * select to update the matching studio field.
 */
interface ParamGroup {
  key: string // parameter group identifier
  label: string // displayed parameter label
  value: string // current parameter value
  options: Array<{
    value: string // current parameter value
    label: string // displayed parameter label
  }> // available parameter choices
  /**
   * select writes a chosen string value to the corresponding shared studio
   * parameter.
   */
  select: (value: string) => void
}

/**
 * leadingParamGroups supplies image size before aspect ratio, hiding the group
 * when the selected model has no size choices.
 */
const leadingParamGroups = computed<ParamGroup[]>(() => {
  if (!studio.imageSizeOptions.value.length) return []
  return [
    {
      key: 'size',
      label: t('creative.panel.imageSize'),
      value: studio.imageSize.value,
      options: studio.imageSizeOptions.value.map((size) => ({ value: size, label: size })),
      select: (value) => {
        studio.imageSize.value = value
      },
    },
  ]
})

/**
 * trailingParamGroups supplies supported quality, background, and thinking
 * choices after aspect ratio and omits empty choice lists.
 */
const trailingParamGroups = computed<ParamGroup[]>(() => {
  const groups: ParamGroup[] = [
    {
      key: 'quality',
      label: t('creative.panel.quality'),
      value: studio.quality.value,
      options: studio.qualityOptions.value.map((option) => ({
        value: option,
        label: t(`creative.qualities.${option}`, option),
      })),
      select: (value) => {
        studio.quality.value = value
      },
    },
    {
      key: 'background',
      label: t('creative.panel.background'),
      value: studio.background.value,
      options: studio.backgroundOptions.value.map((option) => ({
        value: option,
        label: t(`creative.backgrounds.${option}`, option),
      })),
      select: (value) => {
        studio.background.value = value
      },
    },
    {
      key: 'thinkingLevel',
      label: t('creative.panel.thinkingLevel'),
      value: studio.thinkingLevel.value,
      options: studio.thinkingLevelOptions.value.map((option) => ({
        value: option,
        label: t(`creative.thinkingLevels.${option}`, option),
      })),
      select: (value) => {
        studio.thinkingLevel.value = value
      },
    },
  ]
  return groups.filter((group) => group.options.length > 0)
})

/**
 * selectParam forwards string values from a generic segmented control to its
 * parameter group. Other values are ignored so this adapter cannot write a
 * nonstring into generation form state.
 */
function selectParam(group: ParamGroup, value: unknown): void {
  if (typeof value === 'string') group.select(value)
}

/**
 * togglePanel opens or closes one parameter panel and records its chip anchor.
 * Opening computes its current layout immediately; closing clears the anchor
 * used by later window resize events.
 */
function togglePanel(panel: 'model' | 'params' | 'operation', event: MouseEvent): void {
  openPanel.value = openPanel.value === panel ? null : panel
  panelAnchor = openPanel.value ? (event.currentTarget as HTMLElement) : null
  if (panelAnchor) layoutPopover(panelAnchor)
}

/**
 * layoutPopover sizes and positions the active popover within the composer. Its
 * width respects the composer and viewport limits; the computed left offset is
 * relative to the chip wrapper that owns absolute positioning.
 */
function layoutPopover(anchor: HTMLElement | null): void {
  const root = rootRef.value
  const chipSpan = anchor?.parentElement
  if (!root || !chipSpan) return
  const rootWidth = root.clientWidth
  /**
   * Keep popover width within the composer and viewport allowance.
   */
  const width = Math.min(320, rootWidth, window.innerWidth - 56)
  const chipOffset = chipSpan.offsetLeft
  const left = Math.min(Math.max(chipOffset, 0), Math.max(rootWidth - width, 0))
  /**
   * Convert the clamped composer-space position to the chip wrapper's local
   * offset, including negative offsets when right-edge chips need leftward
   * placement.
   */
  popoverStyle.value = { left: `${left - chipOffset}px`, width: `${width}px` }
}

useEventListener(window, 'resize', () => layoutPopover(panelAnchor))

/**
 * selectModel applies the selected group-model option and closes the model
 * panel.
 */
function selectModel(option: CreativeModelOption): void {
  studio.selectOption(creativeOptionKey(option))
  openPanel.value = null
}

/**
 * selectOperation stores the requested generation operation and closes the
 * operation panel.
 */
function selectOperation(op: CreativeOperation): void {
  studio.operation.value = op
  openPanel.value = null
}

/**
 * setAspectRatio writes the selected ratio into the shared studio form.
 */
function setAspectRatio(ratio: string): void {
  studio.aspectRatio.value = ratio
}

/**
 * ratioPreviewStyle converts ratio text into bounded pixel dimensions for its
 * icon. Nonnumeric and zero parts use one, the smaller side targets fourteen
 * pixels, and each resulting side is capped at twenty-two pixels.
 */
function ratioPreviewStyle(ratio: string): {
  width: string // horizontal dimension pixels
  height: string // vertical dimension pixels
} {
  const [w, h] = ratio.split(':').map((part) => Number(part) || 1)
  const base = 14
  /**
   * Scale the shorter ratio side to the preview baseline.
   */
  const scale = base / Math.min(w, h)
  /**
   * Cap each preview dimension to fit the ratio chip.
   */
  const width = Math.min(22, Math.round(w * scale))
  const height = Math.min(22, Math.round(h * scale))
  return { width: `${width}px`, height: `${height}px` }
}

/**
 * onKeydown emits generation for Ctrl or Command plus Enter when submission is
 * available. The shortcut prevents its default action; ordinary Enter remains
 * available for prompt line breaks.
 */
function onKeydown(event: KeyboardEvent): void {
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    event.preventDefault()
    if (studio.canGenerate.value) emit('generate')
  }
}

/**
 * autosize measures the prompt textarea after clearing its previous explicit
 * height. It caps the resulting height at the configured maximum, leaving
 * overflow handling to the existing textarea styling.
 */
function autosize(): void {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, TEXTAREA_MAX_HEIGHT)}px`
}

/**
 * Measure after Vue has updated textarea content so reused history prompts
 * resize from their newly rendered value, just like manual input.
 */
watch([prompt, textareaRef], autosize, { flush: 'post' })

/**
 * fillPrompt replaces the shared prompt and schedules focus at its end. A
 * missing textarea still receives the prompt value, while a mounted textarea is
 * focused on the next animation frame.
 */
function fillPrompt(text: string): void {
  prompt.value = text
  const el = textareaRef.value
  if (!el) return
  requestAnimationFrame(() => {
    el.focus()
    el.setSelectionRange(text.length, text.length)
  })
}

defineExpose({ sendButtonRef, fillPrompt })
</script>

<style scoped>
/* The composer combines dialog and control radius tokens for its outer shell, matching the larger floating input shape documented by the UI conventions. */
.composer-shell {
  --composer-radius: calc(var(--radius-dialog) + var(--radius-control));
  border-radius: var(--composer-radius);
}

.composer-textarea {
  max-height: 160px;
  overflow-y: auto;
}

.composer-chip {
  @apply inline-flex h-8 min-w-0 max-w-full items-center gap-1.5 rounded-control px-2.5 text-xs font-medium text-gray-600 transition-colors;
  @apply hover:bg-gray-100 hover:text-gray-900;
  @apply dark:text-dark-100 dark:hover:bg-dark-800 dark:hover:text-white;
}

.composer-chip-active {
  @apply bg-primary-500/8 text-primary-700 hover:bg-primary-500/15 hover:text-primary-700;
  @apply dark:text-primary-500 dark:hover:bg-primary-500/15 dark:hover:text-primary-500;
}

/* These initial dimensions are replaced by layoutPopover when opened. Position remains relative to the chip wrapper while the computed bounds keep content inside the composer. */
.chip-popover {
  @apply absolute bottom-full left-0 z-30 mb-2 w-[min(320px,calc(100vw-3.5rem))] overflow-hidden rounded-surface border border-primary-900/10 bg-white shadow-lg;
  @apply dark:border-dark-600 dark:bg-dark-900;
}

.composer-option {
  @apply flex w-full items-center gap-2.5 rounded-control px-2.5 py-2 text-left text-gray-800 transition-colors duration-fast;
  @apply hover:bg-primary-100 hover:text-primary-700;
  @apply dark:text-dark-100 dark:hover:bg-dark-800 dark:hover:text-primary-500;
}

.composer-option-active {
  @apply bg-primary-500/8 text-primary-700 dark:text-primary-500;
}

.param-label {
  @apply mb-2 text-xs font-medium text-gray-500 dark:text-dark-400;
}

.param-chip {
  @apply inline-flex items-center gap-1.5 rounded-control border border-gray-200 px-2.5 py-1.5 text-xs text-gray-600 transition-colors;
  @apply hover:border-gray-300 hover:text-gray-900;
  @apply dark:border-dark-600 dark:text-dark-100 dark:hover:border-dark-500 dark:hover:text-white;
}

.param-chip-active {
  @apply border-primary-500 bg-primary-500/8 text-primary-700 hover:border-primary-500 hover:text-primary-700;
  @apply dark:border-primary-500 dark:bg-primary-500/15 dark:text-primary-500 dark:hover:border-primary-500 dark:hover:text-primary-500;
}

/* ratioPreviewStyle supplies bounded inline dimensions so each chip can show its aspect ratio while retaining this shared border treatment. */
.ratio-preview {
  @apply inline-block flex-shrink-0 rounded-compact border-[1.5px] border-current opacity-70;
}

.composer-cost {
  @apply inline-flex items-baseline gap-1 whitespace-nowrap text-xs text-gray-400 max-sm:hidden;
  @apply dark:text-dark-400;
}

.composer-cost-amount {
  @apply inline-flex items-baseline gap-0.5 text-sm font-medium tabular-nums text-gray-700;
  @apply dark:text-dark-100;
}

/* Account currency symbols can be emoji; reduced size and opacity keep their visual weight close to the adjacent amount. */
.composer-cost-unit {
  @apply text-xs opacity-70;
}

.composer-send {
  @apply inline-flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-primary-600 text-white transition-colors hover:bg-primary-700;
  @apply disabled:cursor-not-allowed disabled:bg-gray-200 disabled:text-gray-400;
  @apply dark:disabled:bg-dark-700 dark:disabled:text-dark-500;
}
</style>
