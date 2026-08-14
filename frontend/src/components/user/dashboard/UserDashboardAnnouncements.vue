<template>
  <div class="dashboard-panel overflow-hidden">
    <div class="flex min-h-[61px] items-center justify-between gap-3 border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <div class="flex min-w-0 items-center gap-2.5">
        <Icon name="bell" size="md" class="shrink-0 text-primary-600 dark:text-primary-400" />
        <h2 class="truncate text-lg font-semibold text-gray-900 dark:text-white">
          {{ t('dashboard.recentAnnouncements') }}
        </h2>
      </div>
      <span
        data-testid="announcement-unread-count"
        :class="unreadCount > 0
          ? 'bg-primary-100 text-primary-800 dark:bg-primary-900/30 dark:text-primary-300'
          : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-300'"
        class="inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium"
      >
        <span
          :class="unreadCount > 0
            ? 'bg-primary-500 dark:bg-primary-400'
            : 'bg-gray-400 dark:bg-dark-400'"
          class="h-1.5 w-1.5 rounded-full"
        ></span>
        {{ t('dashboard.unreadAnnouncements', { count: unreadCount }) }}
      </span>
    </div>

    <div class="p-6">
      <div
        v-if="loading"
        data-testid="announcement-timeline-loading"
        class="flex items-center justify-center py-12"
      >
        <LoadingSpinner size="lg" />
      </div>

      <div v-else-if="timelineItems.length === 0" class="py-8">
        <EmptyState
          :title="t('announcements.empty')"
          :description="t('announcements.emptyDescription')"
        />
      </div>

      <ol v-else data-testid="announcement-timeline" class="space-y-1">
        <li
          v-for="(item, index) in timelineItems"
          :key="item.announcement.id"
          class="relative pl-7"
        >
          <span
            v-if="index < timelineItems.length - 1"
            data-testid="announcement-timeline-connector"
            aria-hidden="true"
            class="absolute bottom-[-1.625rem] left-[5px] top-[1.375rem] w-px bg-gray-200 dark:bg-dark-600"
          ></span>
          <span
            v-if="!item.announcement.read_at"
            data-testid="announcement-timeline-pulse"
            aria-hidden="true"
            class="absolute left-0 top-4 h-3 w-3 animate-ping rounded-full bg-primary-400 opacity-75 motion-reduce:animate-none"
          ></span>
          <span
            data-testid="announcement-timeline-node"
            :data-unread="!item.announcement.read_at"
            aria-hidden="true"
            :class="item.announcement.read_at
              ? 'border-gray-200 bg-gray-400 dark:border-dark-700 dark:bg-dark-500'
              : 'border-primary-100 bg-primary-500 dark:border-dark-900 dark:bg-primary-400'"
            class="absolute left-0 top-4 z-10 h-3 w-3 rounded-full border-[3px]"
          ></span>

          <button
            type="button"
            data-testid="announcement-timeline-item"
            :data-announcement-id="item.announcement.id"
            class="group flex min-h-[88px] w-full items-start gap-3 rounded-lg px-3 py-3 text-left transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:hover:bg-dark-800/60 dark:focus-visible:ring-primary-400/70"
            @click="openAnnouncement(item.announcement)"
          >
            <div class="min-w-0 flex-1">
              <h3
                class="line-clamp-1 break-words text-sm font-semibold text-gray-900 group-hover:text-primary-700 dark:text-white dark:group-hover:text-primary-300"
              >
                {{ item.announcement.title }}
              </h3>
              <p
                class="mt-1.5 line-clamp-2 break-words text-sm leading-5 text-gray-500 dark:text-dark-300"
              >
                {{ item.summary || t('dashboard.announcementNoSummary') }}
              </p>
              <time
                :datetime="item.announcement.created_at"
                :title="formatDateTime(item.announcement.created_at)"
                class="mt-2 block text-xs text-gray-400 dark:text-dark-400"
              >
                {{ formatAnnouncementDate(item.announcement.created_at) }}
              </time>
            </div>
            <Icon
              name="chevronRight"
              size="sm"
              class="mt-1 shrink-0 text-gray-400 transition-transform group-hover:translate-x-0.5 group-hover:text-primary-500 dark:text-dark-500 dark:group-hover:text-primary-400"
            />
          </button>
        </li>
      </ol>
    </div>

    <AnnouncementPopup
      :announcement="selectedAnnouncement"
      preview
      show-read-status
      @close="selectedAnnouncement = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatDate, formatDateTime } from '@/utils/format'
import type { UserAnnouncement } from '@/types'
import AnnouncementPopup from '@/components/common/AnnouncementPopup.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'

const MAX_ANNOUNCEMENTS = 5

const { t } = useI18n()
const announcementStore = useAnnouncementStore()
const { announcements, loading } = storeToRefs(announcementStore)
const selectedAnnouncement = ref<UserAnnouncement | null>(null)
const unreadCount = computed(() => announcementStore.unreadCount)

function getCreatedTimestamp(announcement: UserAnnouncement): number {
  const timestamp = Date.parse(announcement.created_at)
  return Number.isNaN(timestamp) ? 0 : timestamp
}

// 同年省略年份，跨年补充年份，并沿用当前界面语言的日期格式。
function formatAnnouncementDate(createdAt: string): string {
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return ''

  const options: Intl.DateTimeFormatOptions = {
    month: 'long',
    day: 'numeric',
  }
  if (date.getFullYear() !== new Date().getFullYear()) {
    options.year = 'numeric'
  }
  return formatDate(date, options)
}

// 公告正文支持 Markdown 和 HTML，先走项目现有的安全解析链路，再提取纯文本摘要。
function createSummary(content: string): string {
  if (!content) return ''

  const html = DOMPurify.sanitize(marked.parse(content, { breaks: true, gfm: true }) as string)
  const parsed = new DOMParser().parseFromString(html, 'text/html')
  return (parsed.body.textContent || '').replace(/\s+/g, ' ').trim()
}

// 服务端列表优先展示未读公告，时间线需要独立恢复为严格的发布时间倒序。
const timelineItems = computed(() => (
  [...announcements.value]
    .sort((left, right) => {
      const timestampDifference = getCreatedTimestamp(right) - getCreatedTimestamp(left)
      return timestampDifference || right.id - left.id
    })
    .slice(0, MAX_ANNOUNCEMENTS)
    .map((announcement) => ({
      announcement,
      summary: createSummary(announcement.content),
    }))
))

function openAnnouncement(announcement: UserAnnouncement) {
  selectedAnnouncement.value = announcement
  if (!announcement.read_at) {
    void announcementStore.markAsRead(announcement.id)
  }
}
</script>
