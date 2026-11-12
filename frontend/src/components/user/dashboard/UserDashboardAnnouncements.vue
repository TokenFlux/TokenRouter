<template>
  <div class="card overflow-hidden">
    <div
      class="flex min-h-[61px] items-center border-b border-gray-100 px-6 py-4 dark:border-dark-700"
    >
      <h2 class="min-w-0 truncate text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('dashboard.recentAnnouncements') }}
      </h2>
    </div>

    <div class="p-6">
      <div
        v-if="loading"
        data-testid="announcement-timeline-loading"
        class="space-y-5 py-4"
        role="status"
        :aria-label="t('common.loading')"
        aria-busy="true"
      >
        <div v-for="row in 3" :key="row" class="flex items-start gap-4" aria-hidden="true">
          <Skeleton variant="circle" :width="12" :height="12" class="shrink-0" />
          <div class="min-w-0 flex-1 space-y-3">
            <Skeleton width="65%" :height="16" />
            <Skeleton width="90%" :height="12" />
            <Skeleton width="35%" :height="12" />
          </div>
        </div>
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
            :class="
              item.announcement.read_at
                ? 'border-gray-200 bg-gray-400 dark:border-dark-700 dark:bg-dark-500'
                : 'border-primary-100 bg-primary-500 dark:border-dark-900 dark:bg-primary-400'
            "
            class="absolute left-0 top-4 z-10 h-3 w-3 rounded-full border-[3px]"
          ></span>

          <button
            type="button"
            data-testid="announcement-timeline-item"
            :data-announcement-id="item.announcement.id"
            class="group flex min-h-[88px] w-full items-start gap-3 rounded-control px-3 py-3 text-left transition-colors hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:hover:bg-dark-800/60 dark:focus-visible:ring-primary-400/70"
            @click="openAnnouncement(item.announcement)"
          >
            <div class="min-w-0 flex-1">
              <h3
                class="line-clamp-2 break-words text-sm font-semibold leading-5 text-gray-900 group-hover:text-primary-700 dark:text-white dark:group-hover:text-primary-300"
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
              :animate-on-hover="false"
            />
          </button>
        </li>
      </ol>
    </div>

    <AnnouncementPopup
      :announcement="selectedAnnouncement"
      preview
      @close="selectedAnnouncement = null"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatDateTime } from '@/utils/format'
import type { UserAnnouncement } from '@/api/announcements.types'
import AnnouncementPopup from '@/components/announcements/AnnouncementPopup.vue'
import {
  formatAnnouncementDate,
  summarizeAnnouncement,
} from '@/components/announcements/announcementDisplay'
import EmptyState from '@/components/common/EmptyState.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import Icon from '@/components/icons/Icon.vue'

// UserDashboardAnnouncements.vue displays recent publications and opens full announcement
// content in a detail popup. The shared store can prioritize unread entries, so the timeline
// copies and sorts records by publication time, with IDs breaking ties, before selecting its
// visible entries.
//
// Preview text and dates come from the shared announcement display module, so the timeline
// summarizes content the same way as the header list while the detail popup retains complete
// content. Read marking is requested when a previously unread entry opens, and the store
// remains responsible for publishing that status to other announcement consumers.

// MAX_ANNOUNCEMENTS limits the dashboard timeline to the most recent publications after its
// independent chronological sort.
const MAX_ANNOUNCEMENTS = 5

// The announcement store owns fetched entries and read status; this component retains the
// selected detail record for its popup.
const { t } = useI18n()
const announcementStore = useAnnouncementStore()
const { announcements, loading } = storeToRefs(announcementStore)
const selectedAnnouncement = ref<UserAnnouncement | null>(null)

// getCreatedTimestamp returns a publication timestamp, using zero when parsing fails. The
// timeline therefore places invalid dates behind valid recent dates and breaks equal
// timestamps by announcement ID.
function getCreatedTimestamp(announcement: UserAnnouncement): number {
  const timestamp = Date.parse(announcement.created_at)
  return Number.isNaN(timestamp) ? 0 : timestamp
}

// timelineItems copies the store entries before chronological sorting because the store
// ordering prioritizes unread announcements for other consumers.
const timelineItems = computed(() =>
  [...announcements.value]
    .sort((left, right) => {
      const timestampDifference = getCreatedTimestamp(right) - getCreatedTimestamp(left)
      return timestampDifference || right.id - left.id
    })
    .slice(0, MAX_ANNOUNCEMENTS)
    .map((announcement) => ({
      announcement,
      summary: summarizeAnnouncement(announcement.content),
    })),
)

// openAnnouncement opens the selected record and requests read marking when needed. The
// popup appears immediately; the shared store owns the asynchronous update and error
// handling for read status.
function openAnnouncement(announcement: UserAnnouncement) {
  selectedAnnouncement.value = announcement
  if (!announcement.read_at) {
    void announcementStore.markAsRead(announcement.id)
  }
}
</script>
