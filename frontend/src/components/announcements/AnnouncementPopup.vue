<template>
  <Teleport to="body">
    <MotionTransition name="pop-fade" @after-leave="handleAfterLeave">
      <div
        v-if="displayedAnnouncement"
        class="fixed inset-0 z-announcement-raised flex items-center justify-center overflow-y-auto bg-[var(--overlay-bg)] p-3 backdrop-blur-sm sm:p-6"
        @click.self="handleDismiss"
      >
        <section
          role="dialog"
          aria-modal="true"
          :aria-label="displayedAnnouncement.title"
          class="flex max-h-[calc(100dvh-1.5rem)] w-full max-w-[640px] flex-col overflow-hidden rounded-surface border border-gray-200 bg-white shadow-2xl shadow-black/20 dark:border-dark-600 dark:bg-dark-900 dark:shadow-black/50 sm:max-h-[calc(100dvh-3rem)] sm:rounded-dialog"
          @click.stop
        >
          <!-- The header displays announcement type, title, and publication metadata. -->
          <header class="relative shrink-0 px-5 pb-3 pt-5 sm:px-6 sm:pt-6">
            <div
              class="flex items-center gap-1.5 text-xs font-medium text-primary-600 dark:text-primary-400"
            >
              <Icon name="bell" size="sm" :stroke-width="2" />
              <span>{{ t('announcements.title') }}</span>
            </div>

            <button
              type="button"
              data-testid="announcement-popup-close"
              class="absolute right-3 top-3 flex rounded-control text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-100 dark:focus-visible:ring-primary-500/50 sm:right-4 sm:top-4 btn-icon-sm"
              :aria-label="t('common.close')"
              @click="handleDismiss"
            >
              <Icon name="x" size="sm" :stroke-width="1.75" />
            </button>

            <h2
              class="mt-4 break-words pr-10 text-lg font-semibold leading-7 text-gray-900 dark:text-white sm:text-xl"
            >
              {{ displayedAnnouncement.title }}
            </h2>

            <div
              class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 dark:text-dark-300"
            >
              <span class="inline-flex items-center gap-1.5">
                <Icon name="clock" size="sm" :stroke-width="1.75" />
                <time :datetime="displayedAnnouncement.created_at">
                  {{ formatRelativeTime(displayedAnnouncement.created_at) }}
                </time>
              </span>
              <span aria-hidden="true" class="text-gray-300 dark:text-dark-500">·</span>
              <time :datetime="displayedAnnouncement.created_at">
                {{ formatDateTime(displayedAnnouncement.created_at) }}
              </time>
            </div>
          </header>

          <!-- The body scrolls independently so long content leaves the header and footer visible. -->
          <div
            class="announcement-popup-scrollbar min-h-0 flex-1 overflow-y-auto px-5 py-3 sm:px-6 sm:py-3.5"
          >
            <div
              class="announcement-popup-content markdown-body text-link-content max-w-none"
              v-html="renderedContent"
            ></div>
          </div>

          <footer
            class="flex shrink-0 items-center gap-4 px-5 py-3 sm:px-6 sm:py-3.5"
            :class="showStatus ? 'justify-between' : 'justify-end'"
          >
            <div
              v-if="showStatus"
              data-testid="announcement-popup-status"
              class="flex min-w-0 items-center gap-1.5 text-xs text-gray-500 dark:text-dark-300"
            >
              <Icon
                :name="isRead ? 'checkCircle' : 'eye'"
                size="sm"
                class="shrink-0"
                :class="isRead ? 'text-primary-500 dark:text-primary-400' : ''"
                :stroke-width="1.75"
              />
              <span class="truncate">{{
                t(isRead ? 'announcements.read' : 'announcements.unread')
              }}</span>
            </div>

            <button
              type="button"
              data-testid="announcement-popup-dismiss"
              class="btn btn-primary shrink-0"
              @click="handleDismiss"
            >
              {{ t('common.close') }}
            </button>
          </footer>
        </section>
      </div>
    </MotionTransition>
  </Teleport>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import type { Announcement, UserAnnouncement } from '@/api/announcements.types'
import Icon from '@/components/icons/Icon.vue'
import '@/styles/announcement-markdown.css'

// AnnouncementPopup.vue renders queued announcements and parent-controlled
// previews using the same sanitized Markdown dialog. In preview mode, the parent
// supplies content and receives a close notification. In queued mode, the
// announcement store owns the displayed item and its dismissal, while the
// component delays queue advancement until the exit transition completes.
//
// Unmount can bypass the transition callback after an item has already been
// dismissed. The unmount callback advances an empty queued popup in that case,
// but leaves an active item waiting for dismissal. Body scrolling follows the
// configured lock ownership: previews restore scrolling when content clears, and
// every owning instance releases its lock during unmount.

// PopupAnnouncement contains the shared display fields accepted from full
// announcements and user-specific announcement records.
type PopupAnnouncement = Pick<
  Announcement | UserAnnouncement,
  'title' | 'content' | 'created_at'
> & {
  read_at?: string | null // Optional acknowledgement timestamp value.
}

// props chooses controlled preview content or the store queue and assigns read-
// status and body-scroll responsibilities.
const props = withDefaults(
  defineProps<{
    // announcement supplies parent-owned content when preview is enabled; null
    // closes the preview and leaves the store queue outside that instance.
    announcement?: PopupAnnouncement | null // Controlled preview announcement content.
    // preview selects parent-owned visibility and dismissal; queued mode reads
    // currentPopup and advances the announcement store after the exit transition.
    preview?: boolean // Parent controls displayed content.
    // showReadStatus enables acknowledgement metadata for previews; queued
    // announcements display their read status regardless of this preview option.
    showReadStatus?: boolean // Display preview acknowledgement status.
    // lockBodyScroll lets this instance mutate body overflow while visible and
    // release it on unmount; callers disable it when another dialog owns scrolling.
    lockBodyScroll?: boolean // Component owns body overflow.
  }>(),
  {
    announcement: null,
    preview: false,
    showReadStatus: false,
    lockBodyScroll: true,
  },
)

// emit notifies the preview owner when dismissal should remove the currently
// supplied announcement content.
const emit = defineEmits<{
  close: [] /* Controlled preview dismissal notification. */
}>()

// The translation and announcement stores supply popup labels and queued content
// for the current signed-in user.
const { t } = useI18n()
const announcementStore = useAnnouncementStore()
// Displayed content and read indicators derive from the preview flag, optional
// content, and the active store popup.
const displayedAnnouncement = computed(() =>
  props.preview ? props.announcement : announcementStore.currentPopup,
)
const showStatus = computed(() => !props.preview || props.showReadStatus)
const isRead = computed(() => Boolean(displayedAnnouncement.value?.read_at))

marked.setOptions({
  breaks: true,
  gfm: true,
})

// renderedContent parses announcement Markdown and sanitizes HTML before the
// template inserts it into the document.
const renderedContent = computed(() => {
  const content = displayedAnnouncement.value?.content
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

// handleDismiss notifies the preview owner or dismisses the current store popup.
// Queued announcements advance later through the exit callback, allowing the
// closing content to finish its transition.
function handleDismiss() {
  if (props.preview) {
    emit('close')
    return
  }
  announcementStore.dismissPopup()
}

// handleAfterLeave advances the queued popup after the exit transition completes.
// Controlled previews return immediately because their parent owns visibility
// and dismissal rather than the global announcement queue.
function handleAfterLeave() {
  if (props.preview) return
  announcementStore.onPopupClosed()
}

// handleEscape dismisses visible announcement content when Escape is pressed.
// Empty popups and other keys leave state unchanged, and preview dismissal is
// delegated to the owning parent.
function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape' && displayedAnnouncement.value) {
    handleDismiss()
  }
}

// The bell restores scrolling for normal queued announcements; controlled
// previews restore it here when their content closes.
watch(
  displayedAnnouncement,
  (popup) => {
    if (!props.lockBodyScroll) return
    if (popup) {
      document.body.style.overflow = 'hidden'
    } else if (props.preview) {
      document.body.style.overflow = ''
    }
  },
  { immediate: true },
)

onMounted(() => {
  document.addEventListener('keydown', handleEscape)
})

onBeforeUnmount(() => {
  document.removeEventListener('keydown', handleEscape)
  // Unmount can occur during navigation before an exit transition finishes, so
  // release the scroll lock owned by this popup.
  if (props.lockBodyScroll) {
    document.body.style.overflow = ''
  }
  // Unmount during an exit transition can skip after-leave. Advance an already
  // dismissed queue entry here, while an active popup still waits for dismissal.
  if (!props.preview && !announcementStore.currentPopup) {
    announcementStore.onPopupClosed()
  }
})
</script>

<style scoped>
/* The global pop-fade transition also handles reduced motion preferences. */
/* The content scrollbar uses neutral colors. */
.announcement-popup-scrollbar::-webkit-scrollbar {
  width: 8px;
}

.announcement-popup-scrollbar::-webkit-scrollbar-track {
  background: transparent;
}

.announcement-popup-scrollbar::-webkit-scrollbar-thumb {
  background: rgb(156 163 175 / 0.45);
  border: 2px solid transparent;
  border-radius: 9999px;
  background-clip: padding-box;
}

/* Keep the thumb scoped; a standalone :global(.dark) targets the page root. */
.dark .announcement-popup-scrollbar::-webkit-scrollbar-thumb {
  background: rgb(82 82 91 / 0.7);
  border: 2px solid transparent;
  background-clip: padding-box;
}
</style>
