<template>
  <div>
    <!--
      Announcement list trigger. The header text variant shows its label from md upward
      and the bell below md; unread state uses only the red dot there because the header
      label color is owned by the shared navigation style.
    -->
    <button
      @click="openModal"
      :class="[
        triggerClass,
        variant !== 'text' && unreadCount > 0 && 'text-primary-600 dark:text-primary-500',
      ]"
      :aria-label="t('announcements.title')"
      :title="variant === 'text' ? undefined : t('announcements.title')"
    >
      <Icon name="bell" size="md" :class="variant === 'text' && 'md:hidden'" />
      <span v-if="variant === 'text'" class="hidden md:inline">{{ t('announcements.title') }}</span>
      <!-- Unread indicator. -->
      <span
        v-if="unreadCount > 0"
        data-testid="announcement-unread-dot"
        class="absolute right-1 top-1 h-2 w-2 rounded-full bg-red-500"
        aria-hidden="true"
      ></span>
    </button>

    <!-- The list and detail dialogs share the announcement card styling. -->
    <Teleport to="body">
      <MotionTransition name="pop-fade">
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
                  <Icon
                    name="checkCircle"
                    size="sm"
                    :stroke-width="1.75"
                    :animate-on-hover="false"
                  />
                  <span>{{ t('announcements.markAllRead') }}</span>
                </button>
                <button
                  type="button"
                  data-testid="announcement-list-close"
                  class="flex rounded-control text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-100 dark:focus-visible:ring-primary-500/50 btn-icon-sm"
                  :aria-label="t('common.close')"
                  @click="closeModal"
                >
                  <Icon name="x" size="sm" :stroke-width="1.75" />
                </button>
              </div>
            </header>

            <!-- The announcement list scrolls within the viewport. -->
            <div
              class="announcement-list-scrollbar min-h-0 flex-1 overflow-y-auto border-t border-gray-100 dark:border-dark-700/70"
            >
              <ContentSkeleton
                v-if="loading"
                data-testid="announcement-list-loading"
                variant="list"
                :rows="4"
                class="p-6"
              />

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
                    <!-- A static dot identifies unread announcements beside the row icon. -->
                    <span
                      v-if="!item.read_at"
                      data-testid="announcement-list-status-unread"
                      class="relative flex h-9 w-9 items-center justify-center rounded-control bg-primary-50 text-primary-600 dark:bg-primary-500/10 dark:text-primary-400"
                    >
                      <Icon name="bell" size="sm" :stroke-width="1.75" />
                      <span
                        data-testid="announcement-list-unread-marker"
                        class="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-primary-500"
                        aria-hidden="true"
                      ></span>
                    </span>
                    <span
                      v-else
                      data-testid="announcement-list-status-read"
                      class="flex h-9 w-9 items-center justify-center rounded-control bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-dark-400"
                    >
                      <Icon
                        name="checkCircle"
                        size="sm"
                        :stroke-width="1.75"
                        :animate-on-hover="false"
                      />
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
                      :animate-on-hover="false"
                    />
                  </button>
                </li>
              </ul>

              <div v-else class="flex flex-col items-center justify-center px-6 py-12 text-center">
                <div
                  class="flex h-10 w-10 items-center justify-center rounded-control bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-dark-400"
                >
                  <Icon name="inbox" size="md" :stroke-width="1.75" />
                </div>
                <p class="mt-3 text-sm font-medium text-gray-900 dark:text-white">
                  {{ t('announcements.empty') }}
                </p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('announcements.emptyDescription') }}
                </p>
              </div>
            </div>
          </section>
        </div>
      </MotionTransition>
    </Teleport>

    <!-- The bell and dashboard reuse the announcement detail popup. -->
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
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useAppStore } from '@/stores/app'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeTime } from '@/utils/format'
import type { UserAnnouncement } from '@/api/announcements.types'
import AnnouncementPopup from '@/components/announcements/AnnouncementPopup.vue'
import Icon from '@/components/icons/Icon.vue'

// AnnouncementBell.vue renders the header announcement list and opens selected
// announcements in a controlled detail popup. The announcement store supplies
// unread counts and acknowledgement operations, while the header truncates its
// visible list to twenty entries. Opening an unread entry requests
// acknowledgement and keeps the selected detail visible even when that request
// reports an error.
//
// The list, selected detail, and globally queued popup can overlap. Their
// combined presence determines whether body scrolling is locked, and Escape
// closes the selected detail before the list. Component unmount removes the
// keyboard listener and releases body overflow so navigation can remove the
// header dialogs together.

// props chooses the bell button or the header navigation text entry while
// retaining the same announcement list behavior in both variants.
const props = withDefaults(
  defineProps<{
    variant?: 'default' | 'text' // Header trigger appearance variant.
  }>(),
  {
    variant: 'default',
  },
)

// Translation, toast, and announcement stores provide shared display text,
// feedback, and read-state updates for this header instance.
const { t } = useI18n()
const appStore = useAppStore()
const announcementStore = useAnnouncementStore()

// Store refs keep the list and loading state reactive when the announcement store replaces them.
const { announcements, loading } = storeToRefs(announcementStore)
const unreadCount = computed(() => announcementStore.unreadCount)
// Limit the header list to twenty announcements; unread counts continue to use
// the complete store list.
const displayedAnnouncements = computed(() => announcements.value.slice(0, 20))
// triggerClass selects the trigger recipe from the requested header variant and
// keeps unread indicator placement shared.
const triggerClass = computed(() => {
  if (props.variant === 'text') {
    return 'header-nav-link relative'
  }
  return 'relative flex h-9 w-9 items-center justify-center rounded-control text-gray-600 transition hover:bg-gray-100 hover:scale-105 dark:text-gray-400 dark:hover:bg-dark-800'
})

// List visibility and selected detail are independent, allowing the detail
// dialog to open above the list.
const isModalOpen = ref(false)
const selectedAnnouncement = ref<UserAnnouncement | null>(null)

// openModal makes the announcement list dialog visible to the user.
function openModal() {
  isModalOpen.value = true
}

// closeModal hides the list dialog while retaining the selected detail.
function closeModal() {
  isModalOpen.value = false
}

// openDetail selects an announcement and starts acknowledgment for an unread
// item through the store. The detail remains selected while acknowledgment runs;
// the store logs and suppresses ordinary API failures.
function openDetail(announcement: UserAnnouncement) {
  selectedAnnouncement.value = announcement
  if (!announcement.read_at) {
    markAsRead(announcement.id)
  }
}

// closeDetail clears the selected announcement and closes its detail dialog.
function closeDetail() {
  selectedAnnouncement.value = null
}

// markAsRead asks the announcement store to acknowledge a single announcement.
// The store logs and suppresses API failures. This wrapper shows an error toast
// only if the store invocation itself rejects, leaving detail visibility with
// the caller.
async function markAsRead(id: number) {
  try {
    await announcementStore.markAsRead(id)
  } catch (err: any) {
    appStore.showError(err?.message || t('common.unknownError'))
  }
}

// markAllAsRead acknowledges all announcements through the store and reports
// completion with a toast. A rejected request reports its message, or a
// translated generic error when absent.
async function markAllAsRead() {
  try {
    await announcementStore.markAllAsRead()
    appStore.showSuccess(t('announcements.allMarkedAsRead'))
  } catch (err: any) {
    appStore.showError(err?.message || t('common.unknownError'))
  }
}

// handleEscape closes the selected detail before closing the announcement list.
// It responds to Escape only, allowing the list to remain open when its child
// detail is dismissed.
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
  // Release this component's page scroll lock when its dialogs are removed
  // together with the header during navigation.
  document.body.style.overflow = ''
})

watch(
  [isModalOpen, selectedAnnouncement, () => announcementStore.currentPopup],
  ([modal, announcement, popup]) => {
    document.body.style.overflow = modal || announcement || popup ? 'hidden' : ''
  },
)
</script>

<style scoped>
/* The global pop-fade transition also handles reduced motion preferences. */
/* The list scrollbar uses neutral colors. */
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

.dark .announcement-list-scrollbar::-webkit-scrollbar-thumb {
  background: rgb(82 82 91 / 0.7);
  border: 2px solid transparent;
  background-clip: padding-box;
}
</style>
