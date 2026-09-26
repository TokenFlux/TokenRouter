<template>
  <div>
    <!-- 铃铛按钮 -->
    <button
      @click="openModal"
      :class="[
        triggerClass,
        { 'text-blue-600 dark:text-blue-400': unreadCount > 0 }
      ]"
      :aria-label="t('announcements.title')"
      :title="t('announcements.title')"
    >
      <Icon name="bell" size="md" />
      <!-- 未读红点 -->
      <span
        v-if="unreadCount > 0"
        class="absolute right-1 top-1 flex h-2 w-2"
      >
        <span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-red-500 opacity-75"></span>
        <span class="relative inline-flex h-2 w-2 rounded-full bg-red-500"></span>
      </span>
    </button>

    <!-- 公告列表弹窗与公告详情共用同一套轻量卡片风格。 -->
    <Teleport to="body">
      <Transition name="pop-fade">
        <div
          v-if="isModalOpen"
          class="fixed inset-0 z-announcement flex items-center justify-center overflow-y-auto bg-[var(--overlay-bg)] p-3 backdrop-blur-sm sm:p-6"
          @click.self="closeModal"
        >
          <section
            role="dialog"
            aria-modal="true"
            :aria-label="t('announcements.title')"
            data-testid="announcement-list-dialog"
            class="flex max-h-[calc(100dvh-1.5rem)] w-full max-w-[640px] flex-col overflow-hidden rounded-surface border border-gray-200 bg-white shadow-2xl shadow-black/20 dark:border-dark-600 dark:bg-dark-900 dark:shadow-black/50 sm:max-h-[calc(100dvh-3rem)] sm:rounded-dialog"
            @click.stop
          >
            <header class="flex shrink-0 items-center justify-between gap-3 px-4 py-4 sm:px-6">
              <div class="flex min-w-0 items-center gap-2.5">
                <Icon
                  name="bell"
                  size="md"
                  class="shrink-0 text-primary-600 dark:text-primary-400"
                  :stroke-width="1.75"
                />
                <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">
                  {{ t('announcements.title') }}
                </h2>
                <span
                  v-if="unreadCount > 0"
                  data-testid="announcement-list-unread-count"
                  class="shrink-0 rounded-compact bg-primary-50 px-2 py-1 text-xs font-medium leading-none text-primary-700 dark:bg-primary-500/10 dark:text-primary-300"
                >
                  {{ unreadCount }} {{ t('announcements.unread') }}
                </span>
              </div>

              <div class="flex shrink-0 items-center gap-1">
                <button
                  v-if="unreadCount > 0"
                  type="button"
                  data-testid="announcement-list-mark-all-read"
                  :disabled="loading"
                  class="inline-flex h-8 items-center gap-1.5 rounded-compact px-2.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 disabled:cursor-not-allowed disabled:opacity-50 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-white dark:focus-visible:ring-primary-500/50"
                  @click="markAllAsRead"
                >
                  <Icon name="checkCircle" size="sm" :stroke-width="1.75" />
                  <span>{{ t('announcements.markAllRead') }}</span>
                </button>
                <button
                  type="button"
                  data-testid="announcement-list-close"
                  class="flex rounded-control text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-100 dark:focus-visible:ring-primary-500/50 btn-icon-sm"
                  :aria-label="t('common.close')"
                  @click="closeModal"
                >
                  <Icon name="x" size="md" :stroke-width="1.75" />
                </button>
              </div>
            </header>

            <!-- 列表区域独立滚动，避免较多公告撑出视口。 -->
            <div class="announcement-list-scrollbar min-h-0 flex-1 overflow-y-auto border-t border-gray-100 dark:border-dark-700/70">
              <div
                v-if="loading"
                data-testid="announcement-list-loading"
                class="flex items-center justify-center py-14"
              >
                <LoadingSpinner size="md" color="secondary" />
              </div>

              <ul
                v-else-if="displayedAnnouncements.length > 0"
                class="divide-y divide-gray-100 dark:divide-dark-700/70"
              >
                <li v-for="item in displayedAnnouncements" :key="item.id">
                  <button
                    type="button"
                    data-testid="announcement-list-item"
                    :data-announcement-id="item.id"
                    :data-unread="!item.read_at"
                    class="group grid min-h-[72px] w-full grid-cols-[2.25rem_minmax(0,1fr)_1.25rem] items-center gap-3 px-4 py-3.5 text-left transition-colors hover:bg-gray-50 focus-visible:z-10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-black/10 dark:hover:bg-dark-800 dark:focus-visible:ring-primary-500/50 sm:px-6"
                    @click="openDetail(item)"
                  >
                    <!-- 未读状态仅使用小圆点动效，降低列表的视觉重量。 -->
                    <span
                      v-if="!item.read_at"
                      data-testid="announcement-list-status-unread"
                      class="relative flex h-9 w-9 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-400"
                    >
                      <Icon name="bell" size="sm" :stroke-width="1.75" />
                      <span class="absolute -right-0.5 -top-0.5 flex h-2 w-2" aria-hidden="true">
                        <span
                          data-testid="announcement-list-unread-pulse"
                          class="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary-500 opacity-60 motion-reduce:animate-none"
                        ></span>
                        <span class="relative inline-flex h-2 w-2 rounded-full bg-primary-500"></span>
                      </span>
                    </span>
                    <span
                      v-else
                      data-testid="announcement-list-status-read"
                      class="flex h-9 w-9 items-center justify-center rounded-control bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-dark-400"
                    >
                      <Icon name="checkCircle" size="sm" :stroke-width="1.75" />
                    </span>

                    <span class="min-w-0">
                      <span
                        class="block truncate text-sm leading-5 text-gray-900 dark:text-dark-100"
                        :class="item.read_at ? 'font-medium' : 'font-semibold dark:text-white'"
                      >
                        {{ item.title }}
                      </span>
                      <time
                        :datetime="item.created_at"
                        class="mt-1 block text-xs text-gray-500 dark:text-dark-400"
                      >
                        {{ formatRelativeTime(item.created_at) }}
                      </time>
                    </span>

                    <Icon
                      name="chevronRight"
                      size="sm"
                      class="justify-self-end text-gray-400 transition-transform group-hover:translate-x-0.5 dark:text-dark-500"
                      :stroke-width="1.75"
                    />
                  </button>
                </li>
              </ul>

              <div v-else class="flex flex-col items-center justify-center px-6 py-12 text-center">
                <div class="flex h-10 w-10 items-center justify-center rounded-control bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-dark-400">
                  <Icon name="inbox" size="md" :stroke-width="1.75" />
                </div>
                <p class="mt-3 text-sm font-medium text-gray-900 dark:text-white">{{ t('announcements.empty') }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('announcements.emptyDescription') }}</p>
              </div>
            </div>
          </section>
        </div>
      </Transition>
    </Teleport>

    <!-- 铃铛详情与仪表盘共用同一个轻量公告浮层。 -->
    <AnnouncementPopup
      :announcement="selectedAnnouncement"
      preview
      show-read-status
      :lock-body-scroll="false"
      @close="closeDetail"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useAppStore } from '@/stores/app'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeTime } from '@/utils/format'
import type { UserAnnouncement } from '@/types'
import AnnouncementPopup from '@/components/common/AnnouncementPopup.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(defineProps<{
  variant?: 'default' | 'status'
}>(), {
  variant: 'default'
})

const { t } = useI18n()
const appStore = useAppStore()
const announcementStore = useAnnouncementStore()

// 通过 storeToRefs 保持公告列表和加载状态的响应性。
const { announcements, loading } = storeToRefs(announcementStore)
const unreadCount = computed(() => announcementStore.unreadCount)
// Header 维持原有的 20 条展示上限，完整列表留给未读统计和仪表盘时间排序。
const displayedAnnouncements = computed(() => announcements.value.slice(0, 20))
const triggerClass = computed(() => {
  if (props.variant === 'status') {
    return 'relative flex h-9 w-9 items-center justify-center rounded-control text-primary-900/70 transition-colors hover:bg-primary-100 hover:text-primary-900 dark:text-dark-100/80 dark:hover:bg-dark-800 dark:hover:text-white'
  }
  return 'relative flex h-9 w-9 items-center justify-center rounded-control text-gray-600 transition-all hover:bg-gray-100 hover:scale-105 dark:text-gray-400 dark:hover:bg-dark-800'
})

// 列表弹窗和详情弹窗分别维护显示状态。
const isModalOpen = ref(false)
const selectedAnnouncement = ref<UserAnnouncement | null>(null)

function openModal() {
  isModalOpen.value = true
}

function closeModal() {
  isModalOpen.value = false
}

function openDetail(announcement: UserAnnouncement) {
  selectedAnnouncement.value = announcement
  if (!announcement.read_at) {
    markAsRead(announcement.id)
  }
}

function closeDetail() {
  selectedAnnouncement.value = null
}

async function markAsRead(id: number) {
  try {
    await announcementStore.markAsRead(id)
  } catch (err: any) {
    appStore.showError(err?.message || t('common.unknownError'))
  }
}

async function markAllAsRead() {
  try {
    await announcementStore.markAllAsRead()
    appStore.showSuccess(t('announcements.allMarkedAsRead'))
  } catch (err: any) {
    appStore.showError(err?.message || t('common.unknownError'))
  }
}

function handleEscape(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    if (selectedAnnouncement.value) {
      closeDetail()
    } else if (isModalOpen.value) {
      closeModal()
    }
  }
}

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleEscape)
  document.body.style.overflow = ''
})

watch(
  [isModalOpen, selectedAnnouncement, () => announcementStore.currentPopup],
  ([modal, announcement, popup]) => {
    document.body.style.overflow = (modal || announcement || popup) ? 'hidden' : ''
  }
)
</script>

<style scoped>
/* 过渡配方用全局 pop-fade(数值与原 scoped 拷贝逐字一致),reduced-motion 也由全局收敛。 */
/* 滚动条使用中性色，避免列表区域产生额外强调。 */
.announcement-list-scrollbar::-webkit-scrollbar {
  width: 8px;
}

.announcement-list-scrollbar::-webkit-scrollbar-track {
  background: transparent;
}

.announcement-list-scrollbar::-webkit-scrollbar-thumb {
  background: rgb(156 163 175 / 0.45);
  border: 2px solid transparent;
  border-radius: 9999px;
  background-clip: padding-box;
}

:global(.dark) .announcement-list-scrollbar::-webkit-scrollbar-thumb {
  background: rgb(82 82 91 / 0.7);
  border: 2px solid transparent;
  background-clip: padding-box;
}
</style>
