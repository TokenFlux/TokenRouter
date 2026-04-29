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
      </div>
    </div>

    <!-- 固定区域：分页器 -->
    <div v-if="$slots.pagination" class="layout-section-fixed">
      <slot name="pagination" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

const isMobile = ref(false)

const checkMobile = () => {
  isMobile.value = window.innerWidth < 1024
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
  @apply flex flex-col gap-6;
  height: calc(100vh - 64px - 4rem); /* 减去 header + lg:p-8 的上下padding */
}

.layout-section-fixed {
  @apply flex-shrink-0;
}

.layout-section-scrollable {
  @apply flex-1 min-h-0 flex flex-col;
}

/* 表格滚动容器 - 增强版表体滚动方案 */
.table-scroll-container {
  @apply flex flex-col overflow-hidden h-full border;
  border-radius: var(--apple-radius-2xl);
  border-color: var(--ba-glass-edge);
  background:
    linear-gradient(145deg, rgba(255, 255, 255, 0.68), rgba(236, 250, 255, 0.42)),
    linear-gradient(82deg, rgba(255, 255, 255, 0.36), transparent 48%);
  box-shadow: var(--ba-glass-shadow), var(--ba-glass-inset);
  backdrop-filter: blur(22px) saturate(150%);
  padding: 0.75rem;
}

.dark .table-scroll-container {
  border-color: rgba(139, 221, 248, 0.18);
  background:
    linear-gradient(145deg, rgba(25, 43, 76, 0.78), rgba(9, 18, 36, 0.58)),
    linear-gradient(82deg, rgba(139, 221, 248, 0.08), transparent 48%);
}

.table-scroll-container :deep(.table-wrapper) {
  @apply flex-1 overflow-x-auto overflow-y-auto;
  /* 确保横向滚动条显示在最底部 */
  scrollbar-gutter: stable;
  border: 1px solid rgba(72, 190, 235, 0.12);
  border-radius: calc(var(--apple-radius-2xl) - 0.45rem);
  background: var(--ba-solid-surface);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.72);
  clip-path: inset(0 round calc(var(--apple-radius-2xl) - 0.45rem));
}

.dark .table-scroll-container :deep(.table-wrapper) {
  border-color: rgba(139, 221, 248, 0.13);
  background: var(--ba-solid-surface);
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.08);
}

.table-scroll-container :deep(table) {
  @apply w-full;
  min-width: max-content; /* 关键：确保表格宽度根据内容撑开，从而触发横向滚动 */
  display: table; /* 使用标准 table 布局以支持 sticky 列 */
  border-collapse: separate;
  border-spacing: 0;
}

.table-scroll-container :deep(thead) {
  background: var(--ba-solid-surface-muted);
  backdrop-filter: blur(10px);
}

.table-scroll-container :deep(thead tr:first-child th:first-child) {
  border-top-left-radius: calc(var(--apple-radius-2xl) - 0.45rem);
}

.table-scroll-container :deep(thead tr:first-child th:last-child) {
  border-top-right-radius: calc(var(--apple-radius-2xl) - 0.45rem);
}

.table-scroll-container :deep(tbody) {
  /* 保持默认 table-row-group 显示，不使用 block */
}

.table-scroll-container :deep(th) {
  @apply px-5 py-4 text-left text-sm font-medium text-gray-600 dark:text-dark-300;
  border-bottom: 1px solid var(--ba-glass-hairline);
}

.table-scroll-container :deep(td) {
  @apply px-5 py-4 text-sm text-gray-700 dark:text-gray-300;
  border-bottom: 1px solid rgba(72, 190, 235, 0.1);
}

.dark .table-scroll-container :deep(td) {
  border-bottom-color: rgba(139, 221, 248, 0.1);
}

/* 移动端：恢复正常滚动 */
.table-page-layout.mobile-mode .table-scroll-container {
  @apply h-auto overflow-visible;
  padding: 0.5rem;
}

.table-page-layout.mobile-mode .layout-section-scrollable {
  @apply flex-none min-h-fit;
}

.table-page-layout.mobile-mode .table-scroll-container :deep(.table-wrapper) {
  @apply overflow-visible;
}

.table-page-layout.mobile-mode .table-scroll-container :deep(table) {
  @apply flex-none;
  display: table;
  min-width: 100%;
}
</style>
