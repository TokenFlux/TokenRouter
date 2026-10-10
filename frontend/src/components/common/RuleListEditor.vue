<template>
  <div :class="variant === 'card' ? 'space-y-4' : 'space-y-2'" :data-testid="testId || undefined">
    <div v-if="hasHeader" class="space-y-2">
      <div class="flex items-center justify-between gap-2">
        <div class="flex min-w-0 items-center">
          <h4
            v-if="title && titleStyle === 'section'"
            class="text-sm font-semibold text-primary-900 dark:text-dark-50"
          >
            {{ title }}
          </h4>
          <p v-else-if="title" class="input-label mb-0">{{ title }}</p>
          <slot name="title-suffix" />
        </div>
        <div class="flex shrink-0 flex-wrap items-center justify-end gap-2">
          <slot name="header-actions" />
          <button
            v-if="addPlacement === 'header'"
            type="button"
            class="btn btn-secondary shrink-0"
            :disabled="addBlocked"
            :data-testid="testIdFor('add')"
            @click="requestAdd"
          >
            <Icon name="plus" size="sm" class="mr-1.5" />
            {{ resolvedAddLabel }}
          </button>
        </div>
      </div>
      <p v-if="hint" class="input-hint">{{ hint }}</p>
    </div>

    <slot name="header-extra" />

    <p
      v-if="items.length === 0 && leavingCount === 0 && emptyText"
      class="rounded-control border border-dashed border-gray-200 px-3 py-4 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-dark-400"
    >
      {{ emptyText }}
    </p>

    <TransitionGroup
      ref="listRef"
      :name="animated ? 'motion-list' : 'rule-list-static'"
      :css="animated"
      tag="div"
      class="relative"
      :class="variant === 'card' ? 'space-y-4' : 'space-y-2'"
      @before-leave="onBeforeLeave"
      @after-leave="onLeaveDone"
      @leave-cancelled="onLeaveDone"
      @before-enter="restoreEnteringElement"
    >
      <div
        v-for="(item, index) in items"
        :key="resolveKey(item, index)"
        :data-rule-list-row="listId"
        :data-testid="testIdFor('row')"
        class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-2"
        :class="rowClass"
      >
        <p
          v-if="itemLabel"
          class="self-center text-xs font-medium text-gray-500 dark:text-dark-400"
        >
          {{ itemLabel(index) }}
        </p>
        <div class="min-w-0" :class="{ 'col-span-2 row-start-2': itemLabel }">
          <slot name="row" :item="item" :index="index" />
        </div>
        <div
          v-if="reorderable || removable"
          class="flex items-center gap-1"
          :class="{ 'col-start-2 row-start-1 justify-self-end': itemLabel }"
        >
          <template v-if="reorderable">
            <button
              type="button"
              :class="[actionSizeClass, moveButtonClass]"
              :disabled="disabled || index === 0"
              :title="t('common.moveUp')"
              :aria-label="t('common.moveUp')"
              :data-testid="testIdFor(`move-up-${index}`)"
              @click="emit('move', index, index - 1)"
            >
              <Icon name="arrowUp" size="sm" />
            </button>
            <button
              type="button"
              :class="[actionSizeClass, moveButtonClass]"
              :disabled="disabled || index === items.length - 1"
              :title="t('common.moveDown')"
              :aria-label="t('common.moveDown')"
              :data-testid="testIdFor(`move-down-${index}`)"
              @click="emit('move', index, index + 1)"
            >
              <Icon name="arrowDown" size="sm" />
            </button>
          </template>
          <button
            v-if="removable"
            type="button"
            :class="[actionSizeClass, removeButtonClass]"
            :disabled="disabled || items.length <= min"
            :title="resolvedRemoveLabel"
            :aria-label="resolvedRemoveLabel"
            :data-testid="testIdFor(`remove-${index}`)"
            @click="emit('remove', index)"
          >
            <Icon name="trash" size="sm" />
          </button>
        </div>
      </div>
    </TransitionGroup>

    <p v-if="error" class="text-xs text-red-500" role="alert">{{ error }}</p>

    <div v-if="addPlacement === 'footer'">
      <button
        type="button"
        class="btn btn-secondary"
        :disabled="addBlocked"
        :data-testid="testIdFor('add')"
        @click="requestAdd"
      >
        <Icon name="plus" size="sm" class="mr-1.5" />
        {{ resolvedAddLabel }}
      </button>
    </div>

    <slot name="footer" />
  </div>
</template>

<script setup lang="ts" generic="T">
import {
  computed,
  nextTick,
  ref,
  useId,
  useSlots,
  watch,
  type ComponentPublicInstance,
} from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { prepareListLeave, restoreEnteringElement } from '@/utils/leavingElement'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'

const props = withDefaults(
  defineProps<{
    items: T[]
    itemKey?: (item: T, index: number) => string | number
    title?: string
    hint?: string
    titleStyle?: 'label' | 'section'
    addLabel?: string
    addPlacement?: 'header' | 'footer'
    addDisabled?: boolean
    removeLabel?: string
    emptyText?: string
    min?: number
    max?: number
    disabled?: boolean
    removable?: boolean
    reorderable?: boolean
    variant?: 'line' | 'card'
    itemLabel?: (index: number) => string
    animated?: boolean
    error?: string
    testId?: string
  }>(),
  {
    titleStyle: 'label',
    addPlacement: 'header',
    min: 0,
    removable: true,
    variant: 'line',
    animated: true,
  },
)

const emit = defineEmits<{
  add: []
  remove: [index: number]
  move: [from: number, to: number]
}>()

defineSlots<{
  row(props: { item: T; index: number }): unknown
  'title-suffix'?(): unknown
  'header-actions'?(): unknown
  'header-extra'?(): unknown
  footer?(): unknown
}>()

const { t } = useI18n()
const slots = useSlots()
// 行带上实例编号，嵌套列表查询时不会拿到内层的行。
const listId = useId()
const listRef = ref<ComponentPublicInstance | null>(null)
const resolveObjectKey = createStableObjectKeyResolver<object>('rule-list-row')

const hasHeader = computed(
  () =>
    Boolean(props.title || props.hint || slots['title-suffix'] || slots['header-actions']) ||
    props.addPlacement === 'header',
)
const resolvedAddLabel = computed(() => props.addLabel || t('common.add'))
const resolvedRemoveLabel = computed(() => props.removeLabel || t('common.delete'))
const addBlocked = computed(
  () =>
    props.disabled ||
    props.addDisabled ||
    (props.max !== undefined && props.items.length >= props.max),
)

// 线形行之间只画分隔线；卡片形态各自带边框和浅底。
const rowClass = computed(() =>
  props.variant === 'card'
    ? 'rounded-surface border border-gray-200 bg-gray-50/50 p-4 dark:border-dark-600 dark:bg-dark-800/40'
    : 'border-b border-gray-200 pb-3 last:border-b-0 last:pb-0 dark:border-dark-600',
)
// 带序号头的卡片使用紧凑按钮，其余与 36px 输入框对齐。
const actionSizeClass = computed(() =>
  props.variant === 'card' && props.itemLabel ? 'btn-icon-sm' : 'btn-icon',
)
const moveButtonClass =
  'text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-gray-400 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100'
const removeButtonClass =
  'text-gray-400 transition-colors hover:bg-red-50 hover:text-red-500 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent disabled:hover:text-gray-400 dark:text-dark-400 dark:hover:bg-red-900/20 dark:hover:text-red-400'

const resolveKey = (item: T, index: number): string | number => {
  if (props.itemKey) return props.itemKey(item, index)
  return typeof item === 'object' && item !== null ? resolveObjectKey(item) : index
}

const testIdFor = (suffix: string) =>
  props.testId ? `${props.testId}-${suffix}` : undefined

// 退出中的行继续占位，全部退出后显示空态。
const leavingCount = ref(0)

// 末尾行留在文档流中淡出，动画结束后下方内容上移。
const onBeforeLeave = (element: Element) => {
  leavingCount.value += 1
  prepareListLeave(element)
  let sibling = element.nextElementSibling
  while (sibling?.hasAttribute('inert')) sibling = sibling.nextElementSibling
  if (!sibling) element.classList.add('rule-list-leave-in-flow')
}

const onLeaveDone = (element: Element) => {
  leavingCount.value = Math.max(0, leavingCount.value - 1)
  element.classList.remove('rule-list-leave-in-flow')
}

// 点击添加按钮后聚焦新行，预设和导入追加行时保持当前焦点。
let pendingFocus = false

const requestAdd = async () => {
  pendingFocus = true
  emit('add')
  await nextTick()
  pendingFocus = false
}

const focusableSelector = [
  'input:not([type="hidden"]):not(:disabled):not(.sr-only)',
  'textarea:not(:disabled)',
  '.input-trigger',
].join(', ')

/** focusRow 聚焦指定行里的第一个可编辑控件。 */
const focusRow = (index: number) => {
  const root = listRef.value?.$el as HTMLElement | undefined
  const rows = Array.from(
    root?.querySelectorAll<HTMLElement>(`[data-rule-list-row="${listId}"]:not([inert])`) ?? [],
  )
  rows[index]?.querySelector<HTMLElement>(focusableSelector)?.focus()
}

watch(
  () => props.items.length,
  (length, previous) => {
    if (pendingFocus && length > previous) focusRow(length - 1)
  },
  { flush: 'post' },
)

defineExpose({ focusRow })
</script>

<style scoped>
/* 覆盖 motion-list 退出时的绝对定位，只作用于列表末尾的行。 */
.rule-list-leave-in-flow.motion-list-leave-active {
  position: static;
  width: auto;
}
</style>
