<template>
  <div
    class="ba-theme-shell"
    :class="fullViewport ? 'fixed inset-0 h-[100dvh] overflow-hidden' : 'min-h-screen'"
  >
    <!-- Background Decoration -->
    <div class="ba-theme-backdrop pointer-events-none fixed inset-0"></div>

    <!-- 全局顶栏横跨侧栏和内容区，页面标题由内容区承载。 -->
    <AppHeader />

    <!-- Sidebar and Main Content Area -->
    <AppSidebar v-if="!hideSidebar" />

    <div
      class="relative z-10 min-w-0 pt-14 transition-all duration-300"
      :class="[
        fullViewport ? 'h-full min-h-0' : 'min-h-screen',
        hideSidebar ? '' : sidebarCollapsed ? 'lg:ml-[72px]' : 'lg:ml-56',
      ]"
    >
      <!-- Main Content -->
      <main
        class="app-main min-w-0 px-4 pb-4 pt-4 md:px-6 md:pb-6 lg:px-8 lg:pb-8"
        :class="{ 'has-page-heading': pageTitle }"
      >
        <div v-if="pageTitle" class="page-heading mb-4 flex flex-wrap items-start justify-between gap-3">
          <div>
            <h1 class="page-title">{{ pageTitle }}</h1>
            <p v-if="pageDescription" class="page-description">{{ pageDescription }}</p>
          </div>
          <div v-if="$slots['page-heading-actions']" class="shrink-0">
            <slot name="page-heading-actions" />
          </div>
        </div>
        <slot />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/onboarding.css'
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useAppStore } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingTour } from '@/composables/useOnboardingTour'
import { usePageMeta } from '@/composables/usePageMeta'
import { useOnboardingStore } from '@/stores/onboarding'
import AppSidebar from './AppSidebar.vue'
import AppHeader from './AppHeader.vue'

interface Props {
  // 全屏工作区使用动态视口锁定布局，并在组件存续期间禁止页面滚动。
  fullViewport?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  fullViewport: false,
})

const appStore = useAppStore()
const authStore = useAuthStore()
const route = useRoute()
const sidebarCollapsed = computed(() => appStore.sidebarCollapsed)
// 全屏工作区页面（如创作台）通过路由 meta 隐藏侧栏并取消内容区缩进。
const hideSidebar = computed(() => route.meta.hideSidebar === true)
const fullViewport = computed(() => props.fullViewport)
const isAdmin = computed(() => authStore.user?.role === 'admin')

let previousHtmlOverflowY: string | null = null
let previousBodyOverflowY: string | null = null

// 动态视口变化可能触发浏览器恢复根页面滚动；全屏工作区必须只让内部控件滚动。
function lockDocumentScroll(): void {
  if (!fullViewport.value || typeof document === 'undefined') return
  previousHtmlOverflowY = document.documentElement.style.overflowY
  previousBodyOverflowY = document.body.style.overflowY
  document.documentElement.style.overflowY = 'hidden'
  document.body.style.overflowY = 'hidden'
}

// 路由离开时恢复进入创作台前的页面滚动策略，避免影响普通页面。
function restoreDocumentScroll(): void {
  if (typeof document === 'undefined') return
  if (previousHtmlOverflowY !== null) {
    document.documentElement.style.overflowY = previousHtmlOverflowY
    previousHtmlOverflowY = null
  }
  if (previousBodyOverflowY !== null) {
    document.body.style.overflowY = previousBodyOverflowY
    previousBodyOverflowY = null
  }
}

const { replayTour, startTeamTour } = useOnboardingTour({
  storageKey: isAdmin.value ? 'admin_guide' : 'user_guide',
  autoStart: true
})

const onboardingStore = useOnboardingStore()
const { pageTitle, pageDescription } = usePageMeta()

onMounted(() => {
  lockDocumentScroll()
  onboardingStore.setReplayCallback(replayTour)
  onboardingStore.setTeamGuideCallback(startTeamTour)
})

onBeforeUnmount(() => {
  restoreDocumentScroll()
})

defineExpose({ replayTour })
</script>

<style scoped>
/* 表格页需要知道内容区标题占用的固定空间，避免滚动区域向视口底部溢出。 */
.app-main {
  --page-heading-space: 0px;
  /* 主区上下内边距的镜像变量，供表格页计算可视高度；必须与 main 的 padding 类一致。 */
  --main-pad-top: 1rem;
  --main-pad-bottom: 1rem;
}

/* 5rem 由 text-2xl 标题、描述行和 mb-4 间距相加得出。 */
.app-main.has-page-heading {
  --page-heading-space: 5rem;
}

@media (min-width: 768px) {
  .app-main {
    --main-pad-bottom: 1.5rem;
  }
}

@media (min-width: 1024px) {
  .app-main {
    --main-pad-bottom: 2rem;
  }
}
</style>
