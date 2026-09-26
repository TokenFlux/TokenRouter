<template>
  <div ref="rootRef" class="group-form-tabs" @onboarding-reveal="onTourReveal">
    <div class="group-tab-list" role="tablist" :aria-label="t('admin.groups.tabs.label')">
      <button
        v-for="tab in visibleTabs"
        :id="`${idPrefix}-tab-${tab}`"
        :key="tab"
        type="button"
        role="tab"
        :aria-selected="activeTab === tab"
        :aria-controls="`${idPrefix}-panel-${tab}`"
        :tabindex="activeTab === tab ? 0 : -1"
        :data-group-tab-button="tab"
        class="group-tab"
        :class="{ 'group-tab-active': activeTab === tab }"
        @click="selectTab(tab)"
        @keydown="onTabKeydown($event, tab)"
      >
        {{ t(`admin.groups.tabs.${tab}`) }}
      </button>
    </div>
    <div ref="contentRef" class="group-tab-content">
      <!-- 所有页签持续挂载，避免定价条目和推理规则的内部草稿在切页时丢失。 -->
      <section
        v-for="tab in allTabs"
        v-show="activeTab === tab && visibleTabs.includes(tab)"
        :id="`${idPrefix}-panel-${tab}`"
        :key="tab"
        role="tabpanel"
        :aria-labelledby="`${idPrefix}-tab-${tab}`"
        :data-group-tab="tab"
        tabindex="0"
        class="group-tab-panel space-y-5"
      >
        <slot :name="tab" />
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ platform: string; idPrefix: string }>()
const { t } = useI18n()
const allTabs = ['general', 'platform', 'routing', 'pricing', 'protocol'] as const
type GroupFormTab = typeof allTabs[number]
const activeTab = ref<GroupFormTab>('general')
const rootRef = ref<HTMLElement | null>(null)
const contentRef = ref<HTMLElement | null>(null)
const visibleTabs = computed(() => allTabs.filter(tab =>
  tab !== 'platform' || ['anthropic', 'openai', 'gemini', 'antigravity'].includes(props.platform),
))

watch(visibleTabs, tabs => {
  if (!tabs.includes(activeTab.value)) activeTab.value = 'general'
})

// 回顶在 DOM 更新后的同一刷新周期完成，先于 revealElement 的 nextTick 定位。
watch(activeTab, () => {
  if (contentRef.value) contentRef.value.scrollTop = 0
}, { flush: 'post' })

async function selectTab(tab: GroupFormTab) {
  activeTab.value = tab
  await nextTick()
  rootRef.value?.querySelector<HTMLElement>(`[data-group-tab-button="${tab}"]`)
    ?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
}

function onTabKeydown(event: KeyboardEvent, tab: GroupFormTab) {
  const tabs = visibleTabs.value
  const index = tabs.indexOf(tab)
  let next: GroupFormTab | undefined
  if (event.key === 'ArrowRight') next = tabs[(index + 1) % tabs.length]
  if (event.key === 'ArrowLeft') next = tabs[(index + tabs.length - 1) % tabs.length]
  if (event.key === 'Home') next = tabs[0]
  if (event.key === 'End') next = tabs[tabs.length - 1]
  if (!next) return
  event.preventDefault()
  void selectTab(next)
  rootRef.value?.querySelector<HTMLElement>(`[data-group-tab-button="${next}"]`)?.focus()
}

// 原生校验和创建引导共用定位流程，先展示页签，再滚动及聚焦目标。
async function revealElement(element: HTMLElement, focus = true) {
  const tab = element.closest<HTMLElement>('[data-group-tab]')?.dataset.groupTab as GroupFormTab | undefined
  if (!tab || !visibleTabs.value.includes(tab)) return
  activeTab.value = tab
  element.dispatchEvent(new Event('form-field-reveal', { bubbles: true }))
  await nextTick()
  element.scrollIntoView?.({ block: 'center', inline: 'nearest' })
  if (focus) {
    const selector = 'input, textarea, button, select, [tabindex]'
    const target = element.matches(selector) ? element
      : element.querySelector<HTMLElement>(selector) ?? element.parentElement?.querySelector<HTMLElement>(selector)
    target?.focus({ preventScroll: true })
  }
}

async function revealField(selector: string) {
  const element = rootRef.value?.querySelector<HTMLElement>(selector)
  if (element) await revealElement(element)
}

async function validate(): Promise<boolean> {
  const fields = rootRef.value?.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>(
    'input, textarea, select',
  )
  // 不依赖浏览器提交时自动聚焦，隐藏页签中的无效输入也能正常报告。
  const invalid = fields && Array.from(fields).find(field => field.willValidate && !field.validity.valid)
  if (!invalid) return true
  await revealElement(invalid)
  invalid.reportValidity()
  return false
}

function onTourReveal(event: Event) {
  if (event.target instanceof HTMLElement) void revealElement(event.target, false)
}

defineExpose({ validate, revealField })
</script>

<style scoped>
.group-form-tabs {
  display: flex;
  height: min(68dvh, 760px);
  max-height: calc(90dvh - 180px);
  min-height: 0;
  min-width: 0;
  flex-direction: column;
}

.group-tab-list {
  @apply flex shrink-0 overflow-x-auto border-b border-gray-200 dark:border-dark-700;
}

.group-tab {
  @apply shrink-0 whitespace-nowrap border-b-2 border-transparent px-4 py-3 text-sm font-medium text-gray-500 transition-colors hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 dark:text-gray-400 dark:hover:text-white;
}

.group-tab-active {
  @apply border-primary-500 text-primary-700 dark:text-primary-300;
}

.group-tab-content {
  @apply min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain px-1 pb-2 pt-5;
}

/* 分区本身不使用卡片，首块内容省略多余的顶部边线。 */
.group-tab-panel :deep(> :first-child) {
  border-top: 0;
  margin-top: 0;
  padding-top: 0;
}

@media (max-width: 639px) {
  .group-form-tabs { max-height: calc(95dvh - 180px); }
  .group-tab { @apply px-3; }
}
</style>
