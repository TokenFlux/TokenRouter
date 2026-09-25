<template>
  <!-- 聊天式输入框：底部居中或跟随选中图片；左下调参入口，右下费用 + 发送 -->
  <div
    ref="rootRef"
    class="relative w-[min(600px,calc(100vw-2rem))] rounded-[24px] border border-primary-900/10 bg-white/95 shadow-xl backdrop-blur dark:border-dark-600 dark:bg-dark-900/95"
  >
    <!-- 提示词输入区（高度随内容自适应，上限约 6 行） -->
    <div class="relative">
      <textarea
        ref="textareaRef"
        v-model="prompt"
        rows="2"
        class="composer-textarea w-full resize-none bg-transparent px-4 pb-1.5 pt-3.5 text-sm leading-relaxed text-gray-900 outline-none placeholder:text-gray-400 dark:text-gray-100 dark:placeholder:text-dark-400"
        :class="studio.busy.value && 'opacity-60'"
        :placeholder="t('creative.panel.promptPlaceholder')"
        @input="autosize"
        @keydown="onKeydown"
      ></textarea>
    </div>

    <!-- 错误提示（前置引导已收进画布胶囊） -->
    <p v-if="studio.error.value" class="px-4 pb-1.5 text-xs text-red-600 dark:text-red-400">{{ studio.error.value }}</p>

    <!-- 底栏：左下 = 模型 / 参数 / 操作 三个调参入口；右下 = 预估费用 + 发送（预估费用窄屏隐藏，避免把发送按钮挤出屏幕） -->
    <div class="flex items-center gap-1.5 px-3 pb-3">
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
          <!-- 行首图标：已选模型显示厂家品牌 logo（ProviderIcon 解析，未知品牌回落首字母），未选模型用 sparkles -->
          <ProviderIcon v-if="modelBrandName" :brand="modelBrandName" size="13px" class="flex-shrink-0" />
          <Icon v-else name="sparkles" size="xs" class="flex-shrink-0" />
          <span class="max-w-28 truncate">{{ modelChipLabel }}</span>
          <Icon name="chevronUp" size="xs" class="flex-shrink-0 transition-transform" :class="openPanel !== 'model' && 'rotate-180'" />
        </button>
        <Transition name="composer-popover">
          <div
            v-if="openPanel === 'model'"
            class="chip-popover"
            :style="popoverStyle"
          >
            <p v-if="showModelsEmptyHint" class="rounded-md bg-primary-900/5 px-3 py-2 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
              {{ modelsEmptyHintText }}
            </p>
            <button
              v-for="option in studio.models.value"
              :key="creativeOptionKey(option)"
              type="button"
              class="composer-option flex w-full items-center gap-2 px-2.5 py-2 text-left transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
              :class="studio.selectedOptionKey.value === creativeOptionKey(option) && 'bg-primary-600/5 dark:bg-primary-900/20'"
              @click="selectModel(option)"
            >
              <ProviderIcon :brand="option.model" size="16px" class="flex-shrink-0" />
              <span class="min-w-0 flex-1">
                <span class="block truncate text-xs font-medium text-gray-800 dark:text-gray-100">{{ option.model }}</span>
                <span class="block truncate text-xs text-gray-400 dark:text-dark-400">{{ option.group_name }}</span>
              </span>
              <Icon v-if="studio.selectedOptionKey.value === creativeOptionKey(option)" name="check" size="sm" class="flex-shrink-0 text-primary-600 dark:text-primary-300" />
            </button>
          </div>
        </Transition>
      </span>

      <!-- 参数：弹层锚定在该按钮上方 -->
      <span class="relative min-w-0">
        <button
          type="button"
          class="composer-chip"
          :class="openPanel === 'params' && 'composer-chip-active'"
          :title="t('creative.composer.params')"
          :aria-expanded="openPanel === 'params'"
          @click="togglePanel('params', $event)"
        >
          <Icon name="filter" size="xs" class="flex-shrink-0" />
          <span class="max-w-24 truncate">{{ paramsChipLabel }}</span>
          <Icon name="chevronUp" size="xs" class="flex-shrink-0 transition-transform" :class="openPanel !== 'params' && 'rotate-180'" />
        </button>
        <Transition name="composer-popover">
          <div
            v-if="openPanel === 'params'"
            class="chip-popover"
            :style="popoverStyle"
          >
            <div class="max-h-[min(70vh,32rem)] space-y-3 overflow-y-auto p-3">
              <div>
                <p class="param-label">{{ t('creative.panel.imageSize') }}</p>
                <div class="flex flex-wrap gap-1.5">
                  <button
                    v-for="size in studio.imageSizeOptions.value"
                    :key="size"
                    type="button"
                    class="param-chip"
                    :class="studio.imageSize.value === size && 'param-chip-active'"
                    @click="setImageSize(size)"
                  >
                    {{ size }}
                  </button>
                  <span v-if="!studio.imageSizeOptions.value.length" class="text-xs text-gray-400 dark:text-dark-400">—</span>
                </div>
              </div>
              <div>
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
                    <Icon v-if="ratio === 'auto'" name="sparkles" size="xs" class="opacity-70" aria-hidden="true" />
                    <!-- 比例预览小方框：直观展示宽高比 -->
                    <span v-else class="ratio-preview" :style="ratioPreviewStyle(ratio)"></span>
                    {{ ratio }}
                  </button>
                </div>
              </div>
              <div v-if="studio.qualityOptions.value.length">
                <p class="param-label">{{ t('creative.panel.quality') }}</p>
                <div class="flex flex-wrap gap-1.5">
                  <button
                    v-for="option in studio.qualityOptions.value"
                    :key="option"
                    type="button"
                    class="param-chip"
                    :class="studio.quality.value === option && 'param-chip-active'"
                    @click="setQuality(option)"
                  >
                    {{ t(`creative.qualities.${option}`, option) }}
                  </button>
                </div>
              </div>
              <div v-if="studio.backgroundOptions.value.length">
                <p class="param-label">{{ t('creative.panel.background') }}</p>
                <div class="flex flex-wrap gap-1.5">
                  <button
                    v-for="option in studio.backgroundOptions.value"
                    :key="option"
                    type="button"
                    class="param-chip"
                    :class="studio.background.value === option && 'param-chip-active'"
                    @click="setBackground(option)"
                  >
                    {{ t(`creative.backgrounds.${option}`, option) }}
                  </button>
                </div>
              </div>
              <div v-if="studio.thinkingLevelOptions.value.length">
                <p class="param-label">{{ t('creative.panel.thinkingLevel') }}</p>
                <div class="flex flex-wrap gap-1.5">
                  <button
                    v-for="option in studio.thinkingLevelOptions.value"
                    :key="option"
                    type="button"
                    class="param-chip"
                    :class="studio.thinkingLevel.value === option && 'param-chip-active'"
                    @click="setThinkingLevel(option)"
                  >
                    {{ t(`creative.thinkingLevels.${option}`, option) }}
                  </button>
                </div>
              </div>
            </div>
          </div>
        </Transition>
      </span>

      <!-- 操作：弹层锚定在该按钮上方 -->
      <span class="relative min-w-0">
        <button
          type="button"
          class="composer-chip"
          :class="openPanel === 'operation' && 'composer-chip-active'"
          :title="t('creative.composer.operation')"
          :aria-expanded="openPanel === 'operation'"
          @click="togglePanel('operation', $event)"
        >
          <Icon name="swap" size="xs" class="flex-shrink-0" />
          <span class="max-w-24 truncate">{{ operationChipLabel }}</span>
          <Icon name="chevronUp" size="xs" class="flex-shrink-0 transition-transform" :class="openPanel !== 'operation' && 'rotate-180'" />
        </button>
        <Transition name="composer-popover">
          <div
            v-if="openPanel === 'operation'"
            class="chip-popover"
            :style="popoverStyle"
          >
            <p v-if="!studio.operationOptions.value.length" class="px-2.5 py-2 text-xs text-gray-400 dark:text-dark-400">
              {{ t('creative.composer.selectModelFirst') }}
            </p>
            <button
              v-for="op in studio.operationOptions.value"
              :key="op"
              type="button"
              class="composer-option flex w-full items-center gap-2 px-2.5 py-2 text-left transition-colors hover:bg-gray-100 dark:hover:bg-dark-700"
              :class="studio.operation.value === op && 'bg-primary-600/5 dark:bg-primary-900/20'"
              @click="selectOperation(op)"
            >
              <span class="min-w-0 flex-1">
                <span class="block text-xs font-medium text-gray-800 dark:text-gray-100">{{ t(`creative.operations.${op}`, op) }}</span>
                <span class="block text-xs text-gray-400 dark:text-dark-400">{{ t(`creative.operationsDesc.${op}`) }}</span>
              </span>
              <Icon v-if="studio.operation.value === op" name="check" size="sm" class="flex-shrink-0 text-primary-600 dark:text-primary-300" />
            </button>
          </div>
        </Transition>
      </span>

      <div class="ml-auto flex items-center gap-2">
        <span v-if="studio.estimatedCost.value !== null" class="max-sm:hidden whitespace-nowrap text-sm text-black dark:text-white">
          {{ t('creative.panel.estimatedCost', { cost: formatBalanceAmount(studio.estimatedCost.value, { fractionDigits: 3 }) }) }}
        </span>
        <button
          ref="sendButtonRef"
          type="button"
          class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-full bg-primary-600 text-white transition-colors hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="!studio.canGenerate.value"
          :title="t('creative.composer.send')"
          @click="emit('generate')"
        >
          <Icon v-if="studio.busy.value" name="refresh" size="sm" class="animate-spin" />
          <Icon v-else name="arrowUp" size="sm" />
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 创作台聊天式输入框（替代旧左侧面板）：
 * - 主体为提示词输入区 + 右下圆形发送按钮；左下三个调参 chip 展开模型 / 参数 / 操作面板
 * - 弹层面板锚定在对应 chip 上方（而非整个输入框上方）；窄屏右侧空间不足时自动向左回退，钳制在输入框内防止超出屏幕
 * - 模型 chip 行首展示厂家品牌 logo（ProviderIcon 按模型名解析，未知品牌回落首字母），弹层列表每行同理，chip 文字保持中性色
 * - 位置由父级控制（底部居中或跟随选中图片），本组件只负责内容与发送
 * - 状态全部经由 props 传入的 studio（useCreativeStudio 返回值）读写
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onClickOutside, useEventListener } from '@vueuse/core'
import Icon from '@/components/icons/Icon.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import { useAppStore } from '@/stores/app'
import { useBalanceDisplay } from '@/composables/useBalanceDisplay'
import type { useCreativeStudio } from '@/composables/useCreativeStudio'
import { creativeOptionKey } from '@/composables/useCreativeStudio'
import type { CreativeModelOption, CreativeOperation } from '@/api/creative'

type Studio = ReturnType<typeof useCreativeStudio>

interface Props {
  studio: Studio
}

interface Emits {
  (e: 'generate'): void
}

const props = defineProps<Props>()
// 本地别名：studio 为 props 传入的共享状态机，子组件经它读写
const studio = props.studio
const emit = defineEmits<Emits>()
const { t } = useI18n()
const appStore = useAppStore()
const { formatBalanceAmount } = useBalanceDisplay()

// 输入框自适应高度上限（约 6 行）
const TEXTAREA_MAX_HEIGHT = 160

const rootRef = ref<HTMLDivElement | null>(null)
const textareaRef = ref<HTMLTextAreaElement | null>(null)
const sendButtonRef = ref<HTMLButtonElement | null>(null)
defineExpose({ sendButtonRef })
// 当前展开的调参面板（同时只开一个）
const openPanel = ref<'model' | 'params' | 'operation' | null>(null)
// 调参弹层的水平定位（内联样式）：宽度取 320px、输入框宽、视口宽 - 3.5rem 三者最小值，
// 左侧位置钳制在输入框范围内——窄屏下 chip 右侧空间不足时自动向左回退，避免弹层超出屏幕
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

// 模型目录为空时的空态提示（加载失败时 models 同样为空，伴随 error 红条展示）
const showModelsEmptyHint = computed(
  () => !studio.loadingModels.value && studio.models.value.length === 0,
)

// 功能被管理员关闭时提示联系管理员开启，否则提示分组未配置图片生成
const modelsEmptyHintText = computed(() =>
  appStore.cachedPublicSettings?.creative_enabled === false
    ? t('creative.panel.studioDisabled')
    : t('creative.panel.noModelsAvailable'),
)

// 模型 chip 行首图标：已选模型名（供 ProviderIcon 解析厂家 logo，未知品牌回落首字母），未选中为 null 显示 sparkles
const modelBrandName = computed(() => studio.selectedOption.value?.model ?? null)

// 三个 chip 的当前值标签
const modelChipLabel = computed(() => {
  const option = studio.selectedOption.value
  return option ? option.model : t('creative.composer.selectModel')
})
const paramsChipLabel = computed(() => t('creative.composer.params'))
const operationChipLabel = computed(() => t(`creative.operations.${studio.operation.value}`, studio.operation.value))

// 展开 / 收起调参面板；展开时同步计算弹层定位（宽度 + 水平钳制）
function togglePanel(panel: 'model' | 'params' | 'operation', event: MouseEvent): void {
  openPanel.value = openPanel.value === panel ? null : panel
  panelAnchor = openPanel.value ? (event.currentTarget as HTMLElement) : null
  if (panelAnchor) layoutPopover(panelAnchor)
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

// 窗口尺寸变化（如旋转屏幕）时重算已展开弹层的定位，避免错位溢出
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

// 参数 chips 选择（经别名写入，避免模板内联赋值触发 prop mutation 校验）
function setImageSize(size: string): void {
  studio.imageSize.value = size
}

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

// 画质选择（经别名写入，避免模板内联赋值触发 prop mutation 校验）
function setQuality(option: string): void {
  studio.quality.value = option
}

function setBackground(value: string): void {
  studio.background.value = value
}

function setThinkingLevel(value: string): void {
  studio.thinkingLevel.value = value
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
</script>

<style scoped>
.composer-textarea {
  max-height: 160px;
  overflow-y: auto;
}

.composer-chip {
  @apply inline-flex h-8 min-w-0 max-w-full items-center gap-1 rounded-full border border-primary-900/10 bg-white px-2.5 text-xs text-gray-600 transition-colors;
  @apply hover:border-black/20 hover:text-gray-900;
  @apply dark:border-dark-600 dark:bg-dark-800 dark:text-gray-300 dark:hover:border-dark-400 dark:hover:text-gray-100;
}

.composer-chip-active {
  @apply border-primary-500/50 text-primary-700 dark:border-primary-500/50 dark:text-primary-300;
}

/* 调参弹层：锚定在所点击 chip 的正上方，内容超过视口时由内层滚动。
   此处宽度仅为初始值，展开时由 layoutPopover 写入内联样式（宽度三路取小、位置钳制在输入框内，防止窄屏溢出屏幕） */
.chip-popover {
  @apply absolute bottom-full left-0 z-30 mb-2 w-[min(320px,calc(100vw-3.5rem))] overflow-hidden rounded-[16px] border border-primary-900/10 bg-white/95 shadow-xl backdrop-blur;
  @apply p-1.5;
  @apply dark:border-dark-600 dark:bg-dark-900/95;
}

/* 三类调参弹层共用同一套向上展开动效，离场也只用一条节奏，避免视觉顿点。 */
.composer-popover-enter-active,
.composer-popover-leave-active {
  transform-origin: bottom center;
  transition:
    opacity 200ms ease,
    transform 200ms cubic-bezier(0.22, 1, 0.36, 1);
  will-change: opacity, transform;
}

.composer-popover-enter-from,
.composer-popover-leave-to {
  opacity: 0;
  transform: translateY(8px) scale(0.97);
}

.composer-option {
  @apply rounded-[12px];
}

.param-label {
  @apply mb-1.5 text-xs font-medium text-gray-900 dark:text-white;
}

.param-chip {
  @apply rounded-[12px] border border-primary-900/10 px-2.5 py-1 text-xs text-gray-600 transition-colors;
  @apply hover:border-black/20 hover:text-gray-900;
  @apply dark:border-dark-600 dark:text-gray-300 dark:hover:border-dark-400 dark:hover:text-gray-100;
}

.param-chip-active {
  @apply border-primary-500 bg-primary-600/10 text-primary-700 dark:border-primary-500 dark:text-primary-300;
}

/* 比例预览小方框：内联尺寸由 ratioPreviewStyle 计算 */
.ratio-preview {
  @apply inline-block flex-shrink-0 rounded-[3px] border-[1.5px] border-current opacity-70;
}

@media (prefers-reduced-motion: reduce) {
  .composer-popover-enter-active,
  .composer-popover-leave-active {
    transition-duration: 1ms;
  }
}
</style>
