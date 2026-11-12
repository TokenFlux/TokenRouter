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

    <!--
      One dialog holds the list and the selected announcement. The fixed panel height
      keeps the frame still while the detail layer covers and uncovers the list.
    -->
    <Teleport to="body">
      <MotionTransition name="pop-fade" @after-leave="resetDetail">
        <div v-if="isModalOpen" class="modal-overlay z-announcement" @click.self="closeModal">
          <section
            role="dialog"
            aria-modal="true"
            :aria-label="t('announcements.title')"
            data-testid="announcement-list-dialog"
            class="modal-content relative h-[36rem] max-h-[95dvh] max-w-[640px] overflow-hidden sm:max-h-[90dvh]"
            @click.stop
          >
            <!-- The list stays mounted under the detail layer so its scroll position survives. -->
            <div
              ref="listViewRef"
              data-testid="announcement-list-view"
              class="flex min-h-0 flex-1 flex-col"
              :inert="selectedAnnouncement ? true : undefined"
            >
              <header
                class="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-primary-900/10 px-6 dark:border-dark-600"
              >
                <div class="flex min-w-0 items-center gap-2.5">
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
                    class="inline-flex h-8 items-center gap-1.5 rounded-control px-2.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 disabled:cursor-not-allowed disabled:opacity-50 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-white dark:focus-visible:ring-primary-500/50"
                    @click="markAllAsRead"
                  >
                    <Icon name="checkCircle" size="sm" :animate-on-hover="false" />
                    <span>{{ t('announcements.markAllRead') }}</span>
                  </button>
                  <!-- The negative margin puts the close glyph on the 24px inset shared with row dates. -->
                  <button
                    type="button"
                    data-testid="announcement-list-close"
                    class="btn-icon-sm -mr-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-100 dark:focus-visible:ring-primary-500/50"
                    :aria-label="t('common.close')"
                    @click="closeModal"
                  >
                    <Icon name="x" size="sm" />
                  </button>
                </div>
              </header>

              <div class="min-h-0 flex-1 overflow-y-auto">
                <!-- Placeholder rows repeat the title, date, and summary geometry of loaded rows. -->
                <div
                  v-if="loading && listRows.length === 0"
                  data-testid="announcement-list-loading"
                  class="p-2"
                  role="status"
                  :aria-label="t('common.loading')"
                  aria-busy="true"
                >
                  <div
                    v-for="row in LOADING_ROWS"
                    :key="row"
                    class="space-y-1 px-4 py-3"
                    aria-hidden="true"
                  >
                    <div class="flex h-5 items-center justify-between gap-3">
                      <Skeleton width="45%" :height="14" />
                      <Skeleton :width="48" :height="12" />
                    </div>
                    <div class="flex h-5 items-center">
                      <Skeleton width="75%" :height="12" />
                    </div>
                  </div>
                </div>

                <!--
                  Rows are inset by the list padding so their rounded hover fill follows
                  the panel corner, and their text starts on the header's 24px inset.
                -->
                <ul v-else-if="listRows.length > 0" class="p-2">
                  <li v-for="row in listRows" :key="row.announcement.id">
                    <button
                      type="button"
                      data-testid="announcement-list-item"
                      :data-announcement-id="row.announcement.id"
                      :data-unread="!row.announcement.read_at"
                      class="group block w-full rounded-control px-4 py-3 text-left transition-colors duration-fast hover:bg-primary-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-black/10 dark:hover:bg-dark-800 dark:focus-visible:ring-primary-500/50"
                      @click="openDetail(row.announcement)"
                    >
                      <span class="relative flex items-baseline gap-3">
                        <!--
                          The unread dot hangs in the row's left padding. It is anchored
                          to this line wrapper because the truncated title clips overflow.
                        -->
                        <span
                          v-if="!row.announcement.read_at"
                          data-testid="announcement-list-unread-marker"
                          class="absolute -left-3 top-1/2 h-2 w-2 -translate-y-1/2 rounded-full bg-primary-500"
                          aria-hidden="true"
                        ></span>
                        <span
                          class="min-w-0 flex-1 truncate text-sm leading-5 group-hover:text-primary-700 dark:group-hover:text-primary-500"
                          :class="
                            row.announcement.read_at
                              ? 'font-medium text-gray-900 dark:text-dark-100'
                              : 'font-semibold text-gray-900 dark:text-white'
                          "
                        >
                          {{ row.announcement.title }}
                        </span>
                        <time
                          :datetime="row.announcement.created_at"
                          class="shrink-0 text-xs tabular-nums text-gray-500 dark:text-dark-400"
                        >
                          {{ formatRowDate(row.announcement.created_at) }}
                        </time>
                      </span>
                      <span
                        v-if="row.summary"
                        data-testid="announcement-list-summary"
                        class="mt-1 block truncate text-sm leading-5 text-gray-500 dark:text-dark-400"
                      >
                        {{ row.summary }}
                      </span>
                    </button>
                  </li>
                </ul>

                <div
                  v-else
                  class="flex h-full flex-col items-center justify-center px-6 text-center"
                >
                  <div
                    class="flex h-10 w-10 items-center justify-center rounded-control bg-gray-100 text-gray-400 dark:bg-dark-800 dark:text-dark-400"
                  >
                    <Icon name="inbox" size="md" />
                  </div>
                  <p class="mt-3 text-sm font-medium text-gray-900 dark:text-white">
                    {{ t('announcements.empty') }}
                  </p>
                  <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
                    {{ t('announcements.emptyDescription') }}
                  </p>
                </div>
              </div>
            </div>

            <!-- The opaque detail layer covers the list inside the same panel. -->
            <MotionTransition name="announcement-detail">
              <div
                v-if="selectedAnnouncement"
                data-testid="announcement-detail-view"
                class="absolute inset-0 flex flex-col bg-white dark:bg-dark-900"
              >
                <header
                  class="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-primary-900/10 px-6 dark:border-dark-600"
                >
                  <!-- The negative margin puts the arrow on the 24px inset used by the article. -->
                  <button
                    ref="backButtonRef"
                    type="button"
                    data-testid="announcement-detail-back"
                    class="-ml-2 inline-flex h-8 items-center gap-1.5 rounded-control px-2 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:text-dark-300 dark:hover:bg-dark-700 dark:hover:text-white dark:focus-visible:ring-primary-500/50"
                    :aria-label="t('common.back')"
                    @click="showList"
                  >
                    <Icon name="arrowLeft" size="sm" />
                    <span>{{ t('announcements.title') }}</span>
                  </button>
                  <button
                    type="button"
                    data-testid="announcement-detail-close"
                    class="btn-icon-sm -mr-2 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-100 dark:focus-visible:ring-primary-500/50"
                    :aria-label="t('common.close')"
                    @click="closeModal"
                  >
                    <Icon name="x" size="sm" />
                  </button>
                </header>
                <AnnouncementArticle :announcement="selectedAnnouncement" />
              </div>
            </MotionTransition>
          </section>
        </div>
      </MotionTransition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import { ref, computed, nextTick, onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useAppStore } from '@/stores/app'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeTime } from '@/utils/format'
import type { UserAnnouncement } from '@/api/announcements.types'
import AnnouncementArticle from '@/components/announcements/AnnouncementArticle.vue'
import {
  formatAnnouncementDate,
  summarizeAnnouncement,
} from '@/components/announcements/announcementDisplay'
import Icon from '@/components/icons/Icon.vue'

// AnnouncementBell.vue renders the header announcement entry, its list dialog, and the
// selected announcement inside that same dialog. The announcement store supplies
// unread counts and acknowledgement operations, while the header truncates its
// visible list to twenty entries. Opening an unread entry requests acknowledgement
// and keeps the selected detail visible even when that request reports an error.
//
// The detail is a layer over the list instead of a second dialog. The list stays
// mounted and inert underneath, so returning restores its scroll position, and
// keyboard focus moves to the back button and then back to the opened row. Escape
// returns from the detail before it closes the dialog. The dialog and the globally
// queued popup can overlap, and either one keeps body scrolling locked. Component
// unmount removes the keyboard listener and releases body overflow so navigation
// can remove the header dialog.

// MAX_LISTED_ANNOUNCEMENTS caps the rows in the header list, RELATIVE_DATE_WINDOW_MS is
// the publication age below which a row shows elapsed time, and LOADING_ROWS is the
// number of placeholder rows drawn before the first announcements arrive.
const MAX_LISTED_ANNOUNCEMENTS = 20
const RELATIVE_DATE_WINDOW_MS = 7 * 24 * 60 * 60 * 1000
const LOADING_ROWS = 4

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
// listRows pairs the first twenty announcements with their one-line summaries. Parsing
// content is cached here so read-state changes and rerenders reuse the summaries;
// unread counts continue to use the complete store list.
const listRows = computed(() =>
  announcements.value.slice(0, MAX_LISTED_ANNOUNCEMENTS).map((announcement) => ({
    announcement,
    summary: summarizeAnnouncement(announcement.content),
  })),
)
// triggerClass selects the trigger recipe from the requested header variant and
// keeps unread indicator placement shared.
const triggerClass = computed(() => {
  if (props.variant === 'text') {
    return 'header-nav-link relative'
  }
  return 'relative flex h-9 w-9 items-center justify-center rounded-control text-gray-600 transition hover:bg-gray-100 hover:scale-105 dark:text-gray-400 dark:hover:bg-dark-800'
})

// isModalOpen controls the dialog, and selectedAnnouncement decides whether the
// dialog shows its list or the detail layer. The selection outlives a closing
// dialog until the exit transition ends, so the closing frame keeps its content.
const isModalOpen = ref(false)
const selectedAnnouncement = ref<UserAnnouncement | null>(null)

// listViewRef locates the row that receives focus when the detail closes, and
// backButtonRef receives focus when the detail opens.
const listViewRef = ref<HTMLElement | null>(null)
const backButtonRef = ref<HTMLButtonElement | null>(null)

// formatRowDate returns elapsed time for announcements published within the last
// seven days and a calendar date for older ones. Future and invalid timestamps take
// the calendar branch because formatRelativeTime labels them as never.
function formatRowDate(createdAt: string): string {
  const age = Date.now() - Date.parse(createdAt)
  return age >= 0 && age < RELATIVE_DATE_WINDOW_MS
    ? formatRelativeTime(createdAt)
    : formatAnnouncementDate(createdAt)
}

// openModal makes the announcement list dialog visible to the user.
function openModal() {
  isModalOpen.value = true
}

// closeModal hides the dialog from either view. The selected announcement is
// cleared by resetDetail once the exit transition finishes.
function closeModal() {
  isModalOpen.value = false
}

// resetDetail clears the selection after the dialog has left, so the next opening
// starts at the list. A dialog reopened during its exit keeps the selection.
function resetDetail() {
  selectedAnnouncement.value = null
}

// openDetail shows an announcement in the detail layer and starts acknowledgment
// for an unread item through the store. The detail remains selected while
// acknowledgment runs; the store logs and suppresses ordinary API failures.
function openDetail(announcement: UserAnnouncement) {
  selectedAnnouncement.value = announcement
  if (!announcement.read_at) {
    markAsRead(announcement.id)
  }
  // The clicked row becomes inert with the list and drops focus, so place focus on
  // the back button once the detail layer has rendered.
  void nextTick(() => backButtonRef.value?.focus({ preventScroll: true }))
}

// showList returns from the detail layer to the list and restores focus to the row
// that opened it. The row is looked up by announcement identifier after the list
// leaves its inert state, because a refresh may have replaced the row element.
function showList() {
  const announcementId = selectedAnnouncement.value?.id
  selectedAnnouncement.value = null
  void nextTick(() => {
    listViewRef.value
      ?.querySelector<HTMLElement>(`[data-announcement-id="${announcementId}"]`)
      ?.focus({ preventScroll: true })
  })
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

// handleEscape steps back through the open dialog: the detail layer returns to the
// list, and the list closes the dialog. Other keys and a closed dialog are ignored,
// including the short interval while a closing dialog still holds its selection.
function handleEscape(e: KeyboardEvent) {
  if (e.key !== 'Escape' || !isModalOpen.value) return
  if (selectedAnnouncement.value) {
    showList()
  } else {
    closeModal()
  }
}

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleEscape)
  // Release this component's page scroll lock when its dialog is removed
  // together with the header during navigation.
  document.body.style.overflow = ''
})

// Body scrolling stays locked while the dialog or a queued popup is visible. The
// queued popup only sets the lock, so this watcher also releases it for that popup.
watch([isModalOpen, () => announcementStore.currentPopup], ([modal, popup]) => {
  document.body.style.overflow = modal || popup ? 'hidden' : ''
})
</script>

<style scoped>
/*
 * The detail layer fades and slides a short distance from the right when it covers
 * the list, and reverses on return. The shared motion variables shorten the
 * durations and remove the offset when reduced motion is requested.
 */
.announcement-detail-enter-active {
  transition:
    opacity var(--motion-layout) var(--motion-ease),
    transform var(--motion-layout) var(--motion-ease);
}

.announcement-detail-leave-active {
  transition:
    opacity var(--motion-exit) var(--motion-ease-exit),
    transform var(--motion-exit) var(--motion-ease-exit);
}

.announcement-detail-enter-from,
.announcement-detail-leave-to {
  opacity: 0;
  transform: translateX(calc(var(--motion-shift) * 4));
}
</style>
