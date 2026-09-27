<template>
  <!-- 聊天式输入框：顶部状态行，中间提示词，左下模型 / 参数 / 操作三个调参入口，右下费用 + 发送 -->
  <div
    ref="rootRef"
    class="composer-shell canvas-island relative w-[min(600px,calc(100vw-2rem))] max-w-full"
  >
    <!-- 状态行：生成进度、错误、失败原因或当前操作的画布引导，同一时间只显示一条 -->
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
        <span v-if="statusLine.detail" class="min-w-0 truncate text-gray-500 dark:text-dark-400" :title="statusLine.detail">{{ statusLine.detail }}</span>
        <span v-if="statusLine.elapsed" class="ml-auto flex-shrink-0 tabular-nums text-gray-400 dark:text-dark-400">{{ statusLine.elapsed }}</span>
      </div>
    </Collapse>

    <!-- 提示词输入区（高度随内容自适应，上限约 6 行） -->
    <textarea
      ref="textareaRef"
      v-model="prompt"
      rows="2"
      class="composer-textarea block w-full resize-none bg-transparent px-4 pb-1.5 pt-3.5 text-sm leading-relaxed text-gray-900 outline-none placeholder:text-gray-400 dark:text-dark-50 dark:placeholder:text-dark-400"
      :class="studio.busy.value && 'opacity-60'"
      :placeholder="t('creative.panel.promptPlaceholder')"
      @input="autosize"
      @keydown="onKeydown"
    ></textarea>

    <!-- 底栏左侧放模型、参数和操作入口，右侧放预估费用和发送按钮，窄屏隐藏预估费用。 -->
    <div class="flex items-center gap-1 px-2 pb-2">
      <!-- 模型：弹层锚定在该按钮上方 -->
      <span class="relative min-w-0">
        <button
          type="button"
          class="composer-chip"
          :class="openPanel === 'model' && 'composer-chip-active'"
          :title="t('creative.composer.model')"
          :aria-expanded="openPanel === 'model'"
          @click="togglePanel('model', $event)"
        >
          <!-- ModelIcon 根据模型 ID 选择图形和配色。 -->
          <ModelIcon v-if="selectedModelName" :model="selectedModelName" size="14px" class="flex-shrink-0" />
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
          <div
            v-if="openPanel === 'model'"
            class="chip-popover"
            :style="popoverStyle"
          >
            <div class="max-h-menu overflow-y-auto p-1.5">
              <p v-if="showModelsEmptyHint" class="px-2.5 py-2 text-xs text-gray-500 dark:text-dark-400">
                {{ modelsEmptyHintText }}
              </p>
              <button
                v-for="option in studio.models.value"
                :key="creativeOptionKey(option)"
                type="button"
                class="composer-option"
                :class="studio.selectedOptionKey.value === creativeOptionKey(option) && 'composer-option-active'"
                @click="selectModel(option)"
              >
                <ModelIcon :model="option.model" size="16px" class="flex-shrink-0" />
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-sm font-medium">{{ option.model }}</span>
                  <span class="block truncate text-xs font-normal text-gray-400 dark:text-dark-400">{{ option.group_name }}</span>
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

      <!-- 参数：chip 直接显示当前尺寸、比例和画质；弹层锚定在该按钮上方 -->
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
          <div
            v-if="openPanel === 'params'"
            class="chip-popover"
            :style="popoverStyle"
          >
            <div class="max-h-[min(70vh,32rem)] space-y-4 overflow-y-auto p-3">
              <div v-if="!studio.selectedOption.value" class="text-xs text-gray-500 dark:text-dark-400">
                {{ t('creative.composer.selectModelFirst') }}
              </div>
              <template v-for="group in leadingParamGroups" :key="group.key">
                <div>
                  <p class="param-label">{{ group.label }}</p>
                  <SettingsSegmented
                    v-if="group.options.length <= SEGMENTED_MAX_OPTIONS"
                    :model-value="group.value"
                    :options="group.options"
                    :aria-label="group.label"
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
                    <!-- 比例预览小方框：直观展示宽高比 -->
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
                    :aria-label="group.label"
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

      <!-- 操作：图标随当前操作变化；弹层锚定在该按钮上方 -->
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
          <div
            v-if="openPanel === 'operation'"
            class="chip-popover"
            :style="popoverStyle"
          >
            <div class="p-1.5">
              <p v-if="!studio.operationOptions.value.length" class="px-2.5 py-2 text-xs text-gray-500 dark:text-dark-400">
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
                  <span class="block text-sm font-medium">{{ t(`creative.operations.${op}`, op) }}</span>
                  <span class="block text-xs font-normal text-gray-400 dark:text-dark-400">{{ t(`creative.operationsDesc.${op}`) }}</span>
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

      <!-- 历史提示词：一键回填之前用过的提示词；快照保存在浏览器本地，服务端只保存哈希 -->
      <span class="relative min-w-0">
        <button
          type="button"
          class="composer-chip"
          :class="openPanel === 'prompts' && 'composer-chip-active'"
          :title="t('creative.composer.promptHistory')"
          :aria-expanded="openPanel === 'prompts'"
          data-testid="creative-prompt-history"
          @click="togglePanel('prompts', $event)"
        >
          <Icon name="clock" size="sm" class="flex-shrink-0" />
          <span class="max-w-24 truncate">{{ t('creative.composer.promptHistory') }}</span>
          <Icon
            name="chevronDown"
            size="xs"
            class="flex-shrink-0 opacity-60 transition-transform"
            :class="openPanel === 'prompts' && 'rotate-180'"
            :animate-on-hover="false"
          />
        </button>
        <MotionTransition name="pop-float">
          <div v-if="openPanel === 'prompts'" class="chip-popover" :style="popoverStyle">
            <p v-if="!recentPrompts.length" class="px-2.5 py-2 text-xs text-gray-500 dark:text-dark-400">
              {{ t('creative.composer.promptHistoryEmpty') }}
            </p>
            <button
              v-for="item in recentPrompts"
              :key="item.runId"
              type="button"
              class="composer-option"
              @click="applyPrompt(item.prompt)"
            >
              <span class="line-clamp-3 min-w-0 flex-1 text-xs text-gray-700 dark:text-gray-200">{{ item.prompt }}</span>
            </button>
          </div>
        </MotionTransition>
      </span>

      <div class="ml-auto flex items-center gap-3">
        <!-- 单张预估价：金额加深加粗，余额符号和“/ 张”弱化，emoji 符号和数字之间留出间距。 -->
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
/**
 * 创作台聊天式输入框：
 * - 顶部状态行按优先级显示一条：错误、最近失败（5 秒后收起）、画布引导、进行中任务的阶段和计时
 * - 主体为提示词输入区 + 右下圆形发送按钮；左下三个调参 chip 展开模型 / 参数 / 操作面板
 * - 参数 chip 直接显示当前尺寸、比例和画质；操作 chip 的图标随当前操作变化
 * - 弹层面板锚定在对应 chip 上方；窄屏右侧空间不足时自动向左回退，钳制在输入框内防止超出屏幕
 * - 位置由父级控制，本组件负责内容与发送；状态全部经由 props 传入的 studio（useCreativeStudio 返回值）读写
 */
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
import { listRecentPrompts, type CreativeRunInputSnapshot } from '@/utils/creativeRunInputs'

type Studio = ReturnType<typeof useCreativeStudio>

interface Props {
  studio: Studio
  // 画布当前缺少的输入，状态行据此显示引导
  guide?: CreativeCanvasGuide | null
}

interface Emits {
  (e: 'generate'): void
}

const props = withDefaults(defineProps<Props>(), {
  guide: null,
})
// 本地别名：studio 为 props 传入的共享状态机，子组件经它读写
const studio = props.studio
const emit = defineEmits<Emits>()
const { t } = useI18n()
const appStore = useAppStore()
const { formatBalanceAmount, balanceUnitSymbol } = useBalanceDisplay()

// 单张预估价最多保留 3 位小数并去掉末尾的 0，价格 1 显示成“1”。“1.000”容易被看成一千。
const estimatedCostText = computed(() => {
  const cost = studio.estimatedCost.value
  if (cost === null) return null
  const normalized = Number(cost.toFixed(3))
  let fractionDigits = 0
  while (fractionDigits < 3 && Number(normalized.toFixed(fractionDigits)) !== normalized) {
    fractionDigits += 1
  }
  return formatBalanceAmount(normalized, { fractionDigits, withSymbol: false })
})

// 输入框自适应高度上限（约 6 行）
const TEXTAREA_MAX_HEIGHT = 160
// 选项不超过这个数量时用分段控件，更多时换行排列 chip
const SEGMENTED_MAX_OPTIONS = 5
// 失败、取消、结果丢失的状态行停留时长
const TERMINAL_STATUS_VISIBLE_MS = 5000

const OPERATION_ICONS: Record<CreativeOperation, IconName> = {
  generate: 'sparkles',
  edit: 'modalityImage',
  inpaint: 'brush',
}

const rootRef = ref<HTMLDivElement | null>(null)
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const sendButtonRef = ref<HTMLButtonElement | null>(null)
// 当前展开的调参面板（同时只开一个）
const openPanel = ref<'model' | 'params' | 'operation' | 'prompts' | null>(null)
// 调参弹层的水平定位（内联样式）：宽度取 320px、输入框宽、视口宽 - 3.5rem 三者最小值，
// 左缘限制在输入框范围内。窄屏下 chip 右侧空间不足时向左移动弹层，使其留在屏幕内。
const popoverStyle = ref<{ left: string; width: string }>({ left: '0px', width: '' })
// 当前展开面板对应的 chip 按钮（窗口缩放时据此重算弹层定位）
let panelAnchor: HTMLElement | null = null

// 点击输入框外部时收起调参面板
onClickOutside(rootRef, () => {
  openPanel.value = null
  panelAnchor = null
})

const prompt = computed({
  get: () => studio.prompt.value,
  set: (value: string) => {
    studio.prompt.value = value
  },
})

// 历史提示词快照保存在浏览器本地（服务端只保存 prompt 的 sha256），一键回填之前用过的提示词
const recentPrompts = ref<CreativeRunInputSnapshot[]>([])

async function loadRecentPrompts(): Promise<void> {
  recentPrompts.value = await listRecentPrompts(10)
}

function applyPrompt(value: string): void {
  fillPrompt(value)
  openPanel.value = null
}

// ==================== 状态行 ====================

type StatusTone = 'active' | 'danger' | 'muted' | 'guide'

interface StatusLine {
  tone: StatusTone
  icon: IconName
  text: string
  detail?: string
  elapsed?: string
}

const STATUS_TONE_CLASSES: Record<StatusTone, string> = {
  active: 'text-primary-700 dark:text-primary-500',
  danger: 'text-red-600 dark:text-red-400',
  muted: 'text-gray-600 dark:text-dark-300',
  guide: 'text-gray-700 dark:text-dark-100',
}

// 失败、取消、结果丢失显示一段时间后收起；成功的结果直接放上画布
const terminalStatusVisible = ref(false)
let terminalStatusTimer: ReturnType<typeof setTimeout> | null = null
// 进行中任务的计时时钟，只在有活动任务时每秒更新
const now = ref(Date.now())
let clockTimer: ReturnType<typeof setInterval> | null = null

// 并发生成时当前任务可能已完成，优先显示历史中仍在执行的任务。
const activeRun = computed(() =>
  studio.runHistory.value.find((run) => !CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)) ?? null,
)

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
      // 失败 / 结果丢失附带服务端原因
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

// 当前任务进入失败类终态时显示状态行，到期自动收起
watch(
  () => studio.currentRun.value?.status,
  (status, previous) => {
    if (!status || status === previous) return
    if (terminalStatusTimer) clearTimeout(terminalStatusTimer)
    terminalStatusTimer = null
    terminalStatusVisible.value = status === 'failed' || status === 'cancelled' || status === 'result_lost'
    if (terminalStatusVisible.value) {
      terminalStatusTimer = setTimeout(() => {
        terminalStatusVisible.value = false
      }, TERMINAL_STATUS_VISIBLE_MS)
    }
  },
)

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

onBeforeUnmount(() => {
  if (terminalStatusTimer) clearTimeout(terminalStatusTimer)
  if (clockTimer) clearInterval(clockTimer)
})

// ==================== 调参入口 ====================

// 模型目录为空时的空态提示（加载失败时 models 同样为空，错误显示在状态行）
const showModelsEmptyHint = computed(
  () => !studio.loadingModels.value && studio.models.value.length === 0,
)

// 功能被管理员关闭时提示联系管理员开启，否则提示分组未配置图片生成
const modelsEmptyHintText = computed(() =>
  appStore.cachedPublicSettings?.creative_enabled === false
    ? t('creative.panel.studioDisabled')
    : t('creative.panel.noModelsAvailable'),
)

// 未选择模型时，按钮显示 sparkles 图标。
const selectedModelName = computed(() => studio.selectedOption.value?.model ?? null)

const modelChipLabel = computed(() => {
  const option = studio.selectedOption.value
  return option ? option.model : t('creative.composer.selectModel')
})

// 参数 chip 显示「尺寸 · 比例 · 画质」中当前模型支持的几项，都没有时显示「参数」
const paramsChipLabel = computed(() => {
  const parts: string[] = []
  if (studio.imageSizeOptions.value.length && studio.imageSize.value) parts.push(studio.imageSize.value)
  if (studio.aspectRatioOptions.value.length && studio.aspectRatio.value) {
    parts.push(studio.aspectRatio.value === 'auto' ? t('creative.composer.autoRatio') : studio.aspectRatio.value)
  }
  if (studio.qualityOptions.value.length && studio.quality.value) {
    parts.push(t(`creative.qualities.${studio.quality.value}`, studio.quality.value))
  }
  return parts.length ? parts.join(' · ') : t('creative.composer.params')
})

const operationChipLabel = computed(() => t(`creative.operations.${studio.operation.value}`, studio.operation.value))

interface ParamGroup {
  key: string
  label: string
  value: string
  options: Array<{ value: string; label: string }>
  select: (value: string) => void
}

// 比例之前的参数组：图片尺寸
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

// 比例之后的参数组：画质、背景、思考强度，模型不支持的组不显示
const trailingParamGroups = computed<ParamGroup[]>(() => {
  const groups: ParamGroup[] = [
    {
      key: 'quality',
      label: t('creative.panel.quality'),
      value: studio.quality.value,
      options: studio.qualityOptions.value.map((option) => ({ value: option, label: t(`creative.qualities.${option}`, option) })),
      select: (value) => {
        studio.quality.value = value
      },
    },
    {
      key: 'background',
      label: t('creative.panel.background'),
      value: studio.background.value,
      options: studio.backgroundOptions.value.map((option) => ({ value: option, label: t(`creative.backgrounds.${option}`, option) })),
      select: (value) => {
        studio.background.value = value
      },
    },
    {
      key: 'thinkingLevel',
      label: t('creative.panel.thinkingLevel'),
      value: studio.thinkingLevel.value,
      options: studio.thinkingLevelOptions.value.map((option) => ({ value: option, label: t(`creative.thinkingLevels.${option}`, option) })),
      select: (value) => {
        studio.thinkingLevel.value = value
      },
    },
  ]
  return groups.filter((group) => group.options.length > 0)
})

// 分段控件回传的取值写入对应参数；SettingsSegmented 是泛型组件，这里收窄为字符串
function selectParam(group: ParamGroup, value: unknown): void {
  if (typeof value === 'string') group.select(value)
}

// 展开 / 收起调参面板；展开时同步计算弹层定位（宽度 + 水平钳制）
function togglePanel(panel: 'model' | 'params' | 'operation' | 'prompts', event: MouseEvent): void {
  openPanel.value = openPanel.value === panel ? null : panel
  panelAnchor = openPanel.value ? (event.currentTarget as HTMLElement) : null
  if (panelAnchor) layoutPopover(panelAnchor)
  // 历史提示词快照存在 IndexedDB，展开面板时读取一次
  if (openPanel.value === 'prompts') void loadRecentPrompts()
}

// 弹层水平定位：chip 左侧偏移钳制到 [0, 输入框宽 - 弹层宽]，保证弹层整体落在输入框内
function layoutPopover(anchor: HTMLElement | null): void {
  const root = rootRef.value
  // chip 按钮外层即定位用的 span（position: relative）
  const chipSpan = anchor?.parentElement
  if (!root || !chipSpan) return
  const rootWidth = root.clientWidth
  const width = Math.min(320, rootWidth, window.innerWidth - 56)
  const chipOffset = chipSpan.offsetLeft
  const left = Math.min(Math.max(chipOffset, 0), Math.max(rootWidth - width, 0))
  // 弹层绝对定位于 chip 的 span 内，这里换算成相对 span 的偏移（负值即向左回退）
  popoverStyle.value = { left: `${left - chipOffset}px`, width: `${width}px` }
}

// 窗口尺寸变化时重新定位已展开的弹层。
useEventListener(window, 'resize', () => layoutPopover(panelAnchor))

// 选择模型后收起面板；参数面板支持连续调整，不自动收起
function selectModel(option: CreativeModelOption): void {
  studio.selectOption(creativeOptionKey(option))
  openPanel.value = null
}

function selectOperation(op: CreativeOperation): void {
  studio.operation.value = op
  openPanel.value = null
}

// 比例 chip 选择：经 studio 别名写入，模板里直接赋值会触发 prop mutation 校验
function setAspectRatio(ratio: string): void {
  studio.aspectRatio.value = ratio
}

// 比例预览框尺寸（宽×高，px）：以 1:1 为 14px 基准按比例缩放，上限 22px
function ratioPreviewStyle(ratio: string): { width: string; height: string } {
  const [w, h] = ratio.split(':').map((part) => Number(part) || 1)
  const base = 14
  const scale = base / Math.min(w, h)
  const width = Math.min(22, Math.round(w * scale))
  const height = Math.min(22, Math.round(h * scale))
  return { width: `${width}px`, height: `${height}px` }
}

// Ctrl / Cmd + Enter 发送；普通 Enter 换行
function onKeydown(event: KeyboardEvent): void {
  if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
    event.preventDefault()
    if (studio.canGenerate.value) emit('generate')
  }
}

// 输入框高度随内容自适应（不超过上限）
function autosize(): void {
  const el = textareaRef.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, TEXTAREA_MAX_HEIGHT)}px`
}

// 空画布引导的示例提示词：写入提示词、调整高度并把光标放到末尾
function fillPrompt(text: string): void {
  prompt.value = text
  const el = textareaRef.value
  if (!el) return
  requestAnimationFrame(() => {
    autosize()
    el.focus()
    el.setSelectionRange(text.length, text.length)
  })
}

defineExpose({ sendButtonRef, fillPrompt })
</script>

<style scoped>
/* 输入框外壳圆角取 dialog 加 control 两档之和（24px），比弹窗更圆润；
   底栏按钮距边缘 8px，按钮自身 8px 圆角，外壳圆角接近两者之和，弧线大致同心。 */
.composer-shell {
  --composer-radius: calc(var(--radius-dialog) + var(--radius-control));
  border-radius: var(--composer-radius);
}

.composer-textarea {
  max-height: 160px;
  overflow-y: auto;
}

/* 调参入口：无描边的幽灵按钮，悬停弱填充，展开时淡品牌青底 */
.composer-chip {
  @apply inline-flex h-8 min-w-0 max-w-full items-center gap-1.5 rounded-control px-2.5 text-xs font-medium text-gray-600 transition-colors;
  @apply hover:bg-gray-100 hover:text-gray-900;
  @apply dark:text-dark-100 dark:hover:bg-dark-800 dark:hover:text-white;
}

.composer-chip-active {
  @apply bg-primary-500/8 text-primary-700 hover:bg-primary-500/15 hover:text-primary-700;
  @apply dark:text-primary-500 dark:hover:bg-primary-500/15 dark:hover:text-primary-500;
}

/* 调参弹层：锚定在所点击 chip 的正上方，实底便于阅读，内容超过视口时由内层滚动。
   此处宽度仅为初始值，展开时由 layoutPopover 写入内联样式（宽度三路取小、位置钳制在输入框内，防止窄屏溢出屏幕） */
.chip-popover {
  @apply absolute bottom-full left-0 z-30 mb-2 w-[min(320px,calc(100vw-3.5rem))] overflow-hidden rounded-surface border border-primary-900/10 bg-white shadow-lg;
  @apply dark:border-dark-600 dark:bg-dark-900;
}

/* 弹层使用全局 pop-float，减少动态效果由全局样式处理。 */

/* 模型和操作的选项行：悬停配色与 .menu-item 一致，选中项淡品牌青底 */
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

/* 比例预览小方框：内联尺寸由 ratioPreviewStyle 计算 */
.ratio-preview {
  @apply inline-block flex-shrink-0 rounded-compact border-[1.5px] border-current opacity-70;
}

/* 发送按钮：可用时品牌青实底；禁用时灰色实底加灰色箭头 */
/* 单张预估价：整行按基线对齐，“/ 张”用浅色小字，金额用 14px 中粗深色数字。 */
.composer-cost {
  @apply inline-flex items-baseline gap-1 whitespace-nowrap text-xs text-gray-400 max-sm:hidden;
  @apply dark:text-dark-400;
}

.composer-cost-amount {
  @apply inline-flex items-baseline gap-0.5 text-sm font-medium tabular-nums text-gray-700;
  @apply dark:text-dark-100;
}

/* 余额符号可能是 emoji，缩到 12px 并降低不透明度，和金额数字的视觉重量接近。 */
.composer-cost-unit {
  @apply text-xs opacity-70;
}

.composer-send {
  @apply inline-flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-primary-600 text-white transition-colors hover:bg-primary-700;
  @apply disabled:cursor-not-allowed disabled:bg-gray-200 disabled:text-gray-400;
  @apply dark:disabled:bg-dark-700 dark:disabled:text-dark-500;
}
</style>
