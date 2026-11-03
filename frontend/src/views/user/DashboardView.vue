<template>
  <AppLayout>
    <template #page-heading-meta>
      <UserDashboardLiveStats ref="liveStatsRef" />
    </template>
    <template #page-heading-actions>
      <UserDashboardUsageToolbar :refreshing="refreshing" @refresh="refreshAll" />
    </template>

    <!-- Stagger initial sections with --rise-i, then remove the entrance class after the final section finishes. -->
    <div
      class="space-y-4"
      :class="{ 'dash-rise': entering }"
      :style="dashboardMotionVars"
      data-testid="dashboard-root"
      @animationend="onRiseEnd"
    >
      <UserDashboardUsageChart ref="usageChartRef" />
      <div class="dash-rise-item" :style="{ '--rise-i': 2 }">
        <UserDashboardHeatmap ref="heatmapRef" @select-day="revealChart" />
      </div>
      <div
        class="dash-rise-item grid grid-cols-1 gap-4 lg:grid-cols-3"
        :style="{ '--rise-i': 3 }"
        data-rise-last="true"
      >
        <!-- The contact card fills the remaining height so both desktop columns end at the same bottom edge. -->
        <div class="flex flex-col gap-4 lg:order-2 lg:col-span-1">
          <UserDashboardQuickActions />
          <UserDashboardContact class="lg:flex-1" />
        </div>
        <div class="lg:order-1 lg:col-span-2"><UserDashboardAnnouncements /></div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { useLocaleRefresh } from '@/composables/useLocaleRefresh'
import { onMounted, ref } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useAnnouncementStore } from '@/stores/announcements'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserDashboardUsageChart from '@/components/user/dashboard/UserDashboardUsageChart.vue'
import UserDashboardUsageToolbar from '@/components/user/dashboard/UserDashboardUsageToolbar.vue'
import UserDashboardLiveStats from '@/components/user/dashboard/UserDashboardLiveStats.vue'
import { provideUsageChartState } from '@/components/user/dashboard/usageChartState'
import { dashboardMotionVars } from '@/components/user/dashboard/dashboardMotion'
import UserDashboardHeatmap from '@/components/user/dashboard/UserDashboardHeatmap.vue'
import UserDashboardAnnouncements from '@/components/user/dashboard/UserDashboardAnnouncements.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import UserDashboardContact from '@/components/user/dashboard/UserDashboardContact.vue'

// DashboardView.vue arranges usage analysis, activity, and account shortcuts in
// the user dashboard. The page provides the shared usage selection consumed by
// its heading toolbar and trend chart, while live statistics and the heatmap
// retain their own refresh lifecycles. Manual refresh waits for those independent
// sections before restoring the refresh control, and each section handles its
// own request failures.
//
// Quick actions and the configured contact card precede announcements in
// document order so narrow screens and keyboard navigation reach account tasks
// first. The desktop grid places the announcements beside that column, and
// initial section animations share the timing variables supplied by the page
// root.

// Account and announcement stores support the page-wide refresh while each dashboard visual
// section owns its displayed result state.
const authStore = useAuthStore()
const announcementStore = useAnnouncementStore()

// usageState is provided once by this page so the heading toolbar and body chart share
// filter and interval selections.
const usageState = provideUsageChartState()
// Section refs expose refresh or scrolling operations; refreshing tracks the full refresh
// batch rather than any one section request.
const heatmapRef = ref<InstanceType<typeof UserDashboardHeatmap> | null>(null)
const liveStatsRef = ref<InstanceType<typeof UserDashboardLiveStats> | null>(null)
const usageChartRef = ref<InstanceType<typeof UserDashboardUsageChart> | null>(null)
const refreshing = ref(false)
// entering keeps the section entrance class active until the final section animation reports
// completion after initial mount.
const entering = ref(true)

// refreshUser reloads account information so the shared heading balance updates. Errors are
// logged here and resolve normally, allowing the remaining dashboard refresh operations to
// finish independently.
const refreshUser = async () => {
  try {
    await authStore.refreshUser()
  } catch (error) {
    // Keep the dashboard refresh promise resolved after account refresh failure;
    // other sections can still complete and retain their own error reporting.
    console.error('Failed to refresh user:', error)
  }
}

// refreshAll starts account, usage, heatmap, live-statistics, and forced announcement
// refreshes together. It waits for every operation to settle before clearing the page
// refresh indicator, with each section retaining its own request-error behavior.
const refreshAll = async () => {
  refreshing.value = true
  try {
    // Each section owns its failure display. Wait for every request to settle so
    // the toolbar does not stop indicating refresh while a slower section remains busy.
    await Promise.allSettled([
      refreshUser(),
      usageState.load(),
      heatmapRef.value?.reload(),
      liveStatsRef.value?.reload(),
      announcementStore.fetchAnnouncements(true),
    ])
  } finally {
    refreshing.value = false
  }
}

useLocaleRefresh(() => Promise.all([usageState.loadFilterOptions(), usageState.load()]))

// revealChart scrolls the trend card into view after a heatmap date selection. Missing chart
// refs are ignored, and reduced-motion preference chooses immediate scrolling instead of a
// smooth transition.
const revealChart = () => {
  const card = usageChartRef.value?.chartCardRef
  if (!card) return
  const reduced =
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  card.scrollIntoView({ behavior: reduced ? 'auto' : 'smooth', block: 'center' })
}

// onRiseEnd removes the entrance class after an element marked as the final section
// completes animation. Subsequent rerenders then retain their settled layout without
// replaying the page entrance.
const onRiseEnd = (event: AnimationEvent) => {
  if ((event.target as HTMLElement).dataset?.riseLast) entering.value = false
}

onMounted(() => {
  void refreshUser()
  void usageState.load()
  void usageState.loadFilterOptions()
})
</script>

<style scoped>
/* Sections rise eight pixels with staggered starts. Backwards fill retains the
   initial state during delay, then releases transform so fixed descendants use
   their normal positioning after the entrance ends. */
.dash-rise :deep(.dash-rise-item) {
  animation: dash-rise var(--dash-rise-ms) var(--motion-ease) backwards;
  animation-delay: calc(var(--rise-i, 0) * var(--dash-rise-step-ms));
}

@keyframes dash-rise {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@media (prefers-reduced-motion: reduce) {
  .dash-rise :deep(.dash-rise-item) {
    animation: none;
  }
}
</style>
