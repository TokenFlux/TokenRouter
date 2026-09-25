<template>
  <div class="table-page-layout" :class="{ 'mobile-mode': isMobile }">
    <!-- 固定区域：操作按钮 -->
    <div v-if="$slots.actions" class="layout-section-fixed">
      <slot name="actions" />
    </div>

    <!-- 固定区域：搜索和过滤器 -->
    <div v-if="$slots.filters" class="layout-section-fixed">
      <slot name="filters" />
    </div>

    <!-- 滚动区域：表格 -->
    <div class="layout-section-scrollable">
      <div class="card table-scroll-container">
        <slot name="table" />
        <!-- 桌面端分页器固定在表格外框内，表体独立滚动。 -->
        <div v-if="$slots.pagination" class="table-pagination-footer">
          <slot name="pagination" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { TABLE_DESKTOP_MIN_WIDTH } from '@/constants/layout'

const isMobile = ref(false)

const checkMobile = () => {
  isMobile.value = window.innerWidth < TABLE_DESKTOP_MIN_WIDTH
}

onMounted(() => {
  checkMobile()
  window.addEventListener('resize', checkMobile)
})

onUnmounted(() => {
  window.removeEventListener('resize', checkMobile)
})
</script>

<style scoped>
/* 桌面端：Flexbox 布局 */
.table-page-layout {
  @apply flex flex-col gap-4;
  /* 高度扣除顶栏、主区上下内边距和标题预留，分页条与视口底部保持主区底距。 */
  height: calc(100vh - var(--header-h) - var(--main-pad-top, 1rem) - var(--main-pad-bottom, 1rem) - var(--page-heading-space, 0px));
  height: calc(100dvh - var(--header-h) - var(--main-pad-top, 1rem) - var(--main-pad-bottom, 1rem) - var(--page-heading-space, 0px));
}

.layout-section-fixed {
  @apply flex-shrink-0;
}

.layout-section-scrollable {
  @apply flex-1 min-h-0 flex flex-col;
}

/* 表格滚动容器 - 增强版表体滚动方案 */
.table-scroll-container {
  @apply flex flex-col overflow-hidden h-full bg-white dark:bg-dark-900 rounded-surface border border-gray-200 dark:border-dark-600 shadow-none;
}

.table-scroll-container :deep(.table-wrapper) {
  @apply flex-1 overflow-x-auto overflow-y-auto;
  /* 确保横向滚动条显示在最底部 */
  scrollbar-gutter: stable;
}

.table-scroll-container :deep(table) {
  @apply w-full;
  min-width: max-content; /* 关键：确保表格宽度根据内容撑开，从而触发横向滚动 */
  display: table; /* 使用标准 table 布局以支持 sticky 列 */
}

.table-scroll-container :deep(thead) {
  /* sticky 表头使用不透明底色，避免合成层模糊表头文字边缘。 */
  @apply bg-gray-50 dark:bg-dark-950;
}

.table-scroll-container :deep(tbody) {
  /* 保持默认 table-row-group 显示，不使用 block */
}

.table-scroll-container :deep(th) {
  /* 表头与 DataTable、.table 保持同一密度:py-2 + text-xs,给数据行留出可视空间。 */
  @apply px-4 py-2 text-left text-xs font-medium tracking-wider text-gray-500 dark:text-dark-400 border-b border-gray-200 dark:border-dark-700;
}

.table-scroll-container :deep(td) {
  @apply px-4 py-3 text-sm text-gray-700 dark:text-gray-300 border-b border-gray-100 dark:border-dark-800;
}

/* 桌面分页器与表头共用同一外框，表体滚动时保持固定。 */
.table-pagination-footer {
  @apply flex-shrink-0;
}

.table-pagination-footer:empty {
  display: none;
}

.table-page-layout:not(.mobile-mode) .table-pagination-footer {
  @apply border-t border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-950;
}

/* 页脚内的分页器去掉自带边框与底色,与表格外框融为一体;控件保持全站 36px 基线。 */
.table-page-layout:not(.mobile-mode) .table-pagination-footer :deep(.pagination-root),
.table-page-layout:not(.mobile-mode) .table-pagination-footer :deep(.batch-pagination-root) {
  border-top: 0;
  background: transparent;
  padding-top: 0.5rem;
  padding-bottom: 0.5rem;
}

/* 移动端：恢复正常滚动 */
.table-page-layout.mobile-mode {
  /* 移动端表格卡片高度由内容决定，避免固定视口高度导致后续区域被溢出内容覆盖。 */
  height: auto;
}

.table-page-layout.mobile-mode .table-scroll-container {
  @apply h-auto overflow-visible border-none shadow-none bg-transparent;
}

.table-page-layout.mobile-mode .layout-section-scrollable {
  @apply flex-none min-h-fit;
}

.table-page-layout.mobile-mode .table-pagination-footer {
  @apply mt-4;
}

.table-page-layout.mobile-mode .table-scroll-container :deep(table) {
  @apply flex-none;
  display: table;
  min-width: 100%;
}
</style>
