<template>
  <Teleport to="body">
    <MotionTransition name="pop-fade" @after-leave="handleAfterLeave">
      <div
        v-if="displayedAnnouncement"
        class="modal-overlay z-announcement-raised"
        @click.self="handleDismiss"
      >
        <section
          role="dialog"
          aria-modal="true"
          :aria-label="displayedAnnouncement.title"
          class="modal-content max-h-[95dvh] max-w-[640px] overflow-hidden sm:max-h-[90dvh]"
          @click.stop
        >
          <AnnouncementArticle :announcement="displayedAnnouncement">
            <!--
              The negative margins place the close glyph on the panel's 24px inset and
              center the 32px button on the first title line.
            -->
            <template #actions>
              <button
                type="button"
                data-testid="announcement-popup-close"
                class="btn-icon-sm -mr-2 -mt-0.5 shrink-0 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 dark:text-dark-400 dark:hover:bg-dark-700 dark:hover:text-dark-100 dark:focus-visible:ring-primary-500/50"
                :aria-label="t('common.close')"
                @click="handleDismiss"
              >
                <Icon name="x" size="sm" />
              </button>
            </template>
          </AnnouncementArticle>

          <!--
            Queued announcements interrupt the user, so they end with an explicit
            acknowledgement. Previews opened by the user close from the header.
          -->
          <footer v-if="!preview" class="flex shrink-0 justify-end px-6 pb-6">
            <button
              type="button"
              data-testid="announcement-popup-dismiss"
              class="btn btn-primary"
              @click="handleDismiss"
            >
              {{ t('announcements.acknowledge') }}
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
import { useAnnouncementStore } from '@/stores/announcements'
import AnnouncementArticle from '@/components/announcements/AnnouncementArticle.vue'
import type { DisplayedAnnouncement } from '@/components/announcements/announcementDisplay'
import Icon from '@/components/icons/Icon.vue'

// AnnouncementPopup.vue shows one announcement in a standalone dialog, either a queued
// announcement from the store or a preview supplied by a parent. In preview mode, the
// parent supplies content and receives a close notification. In queued mode, the
// announcement store owns the displayed item and its dismissal, while the component
// delays queue advancement until the exit transition completes.
//
// Unmount can bypass the transition callback after an item has already been
// dismissed. The unmount callback advances an empty queued popup in that case,
// but leaves an active item waiting for dismissal. The popup locks body scrolling
// while content is visible. Previews restore scrolling when their content clears,
// queued announcements leave that to the header announcement list, and every
// instance releases the lock during unmount.

// props chooses controlled preview content or the store queue.
const props = withDefaults(
  defineProps<{
    // announcement supplies parent-owned content when preview is enabled; null
    // closes the preview and leaves the store queue outside that instance.
    announcement?: DisplayedAnnouncement | null // Controlled preview announcement content.
    // preview selects parent-owned visibility and dismissal and hides the
    // acknowledgement footer; queued mode reads currentPopup and advances the
    // announcement store after the exit transition.
    preview?: boolean // Parent controls displayed content.
  }>(),
  {
    announcement: null,
    preview: false,
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
// displayedAnnouncement selects the parent's preview content or the active store
// popup according to the preview flag.
const displayedAnnouncement = computed(() =>
  props.preview ? props.announcement : announcementStore.currentPopup,
)

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
  // release the scroll lock held by this popup.
  document.body.style.overflow = ''
  // Unmount during an exit transition can skip after-leave. Advance an already
  // dismissed queue entry here, while an active popup still waits for dismissal.
  if (!props.preview && !announcementStore.currentPopup) {
    announcementStore.onPopupClosed()
  }
})
</script>
