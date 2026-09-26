<template>
  <Teleport to="body">
    <Transition name="pop-fade" @after-leave="handleAfterLeave">
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
          <!-- 头部仅保留公告类型、标题和元信息，维持站内通知的轻量层级。 -->
          <header class="relative shrink-0 px-5 pb-3 pt-5 sm:px-6 sm:pt-6">
            <div class="flex items-center gap-1.5 text-xs font-medium text-primary-600 dark:text-primary-400">
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
              <Icon name="x" size="md" :stroke-width="1.75" />
            </button>

            <h2 class="mt-4 break-words pr-10 text-lg font-semibold leading-7 text-gray-900 dark:text-white sm:text-xl">
              {{ displayedAnnouncement.title }}
            </h2>

            <div class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 dark:text-dark-300">
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

          <!-- 正文独立滚动，长公告不会挤压标题和底部操作。 -->
          <div class="announcement-popup-scrollbar min-h-0 flex-1 overflow-y-auto px-5 py-3 sm:px-6 sm:py-3.5">
            <div
              class="announcement-popup-content markdown-body max-w-none"
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
              <span class="truncate">{{ t(isRead ? 'announcements.read' : 'announcements.unread') }}</span>
            </div>

            <button
              type="button"
              data-testid="announcement-popup-dismiss"
              class="shrink-0 rounded-control bg-primary-600 px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-primary-700 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/50 focus-visible:ring-offset-2 dark:bg-primary-500 dark:hover:bg-primary-600 dark:focus-visible:ring-offset-dark-900"
              @click="handleDismiss"
            >
              {{ t('common.close') }}
            </button>
          </footer>
        </section>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import type { Announcement, UserAnnouncement } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import '@/styles/announcement-markdown.css'

type PopupAnnouncement = Pick<Announcement | UserAnnouncement, 'title' | 'content' | 'created_at'> & {
  read_at?: string | null
}

const props = withDefaults(defineProps<{
  announcement?: PopupAnnouncement | null
  preview?: boolean
  showReadStatus?: boolean
  lockBodyScroll?: boolean
}>(), {
  announcement: null,
  preview: false,
  showReadStatus: false,
  lockBodyScroll: true,
})

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const announcementStore = useAnnouncementStore()
const displayedAnnouncement = computed(() => (
  props.preview ? props.announcement : announcementStore.currentPopup
))
const showStatus = computed(() => !props.preview || props.showReadStatus)
const isRead = computed(() => Boolean(displayedAnnouncement.value?.read_at))

marked.setOptions({
  breaks: true,
  gfm: true,
})

// 公告支持 Markdown 和有限 HTML，渲染前统一清理不安全节点和属性。
const renderedContent = computed(() => {
  const content = displayedAnnouncement.value?.content
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

function handleDismiss() {
  if (props.preview) {
    emit('close')
    return
  }
  announcementStore.dismissPopup()
}

// 离场动画结束后推进公告队列（取代 store 里 300ms 的时长猜测）。
// 预览实例的显隐由父组件控制，与全局队列无关。
function handleAfterLeave() {
  if (props.preview) return
  announcementStore.onPopupClosed()
}

function handleEscape(event: KeyboardEvent) {
  if (event.key === 'Escape' && displayedAnnouncement.value) {
    handleDismiss()
  }
}

// 普通公告由铃铛组件恢复页面滚动；受控弹窗默认由当前组件负责恢复。
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
  // 路由切换会直接卸载弹窗，组件需要自行解除页面滚动锁定。
  if (props.lockBodyScroll) {
    document.body.style.overflow = ''
  }
  // 若卸载发生在离场动画途中（如切回首页门面），after-leave 不会触发，
  // 队列会停在已关闭状态；仅在弹窗已关闭时补推一次，展示中的弹窗不抢下一条。
  if (!props.preview && !announcementStore.currentPopup) {
    announcementStore.onPopupClosed()
  }
})
</script>

<style scoped>
/* 过渡配方用全局 pop-fade(数值与原 scoped 拷贝逐字一致),reduced-motion 也由全局收敛。 */
/* 滚动条沿用中性色，避免正文区域出现额外强调色。 */
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

:global(.dark) .announcement-popup-scrollbar::-webkit-scrollbar-thumb {
  background: rgb(82 82 91 / 0.7);
  border: 2px solid transparent;
  background-clip: padding-box;
}
</style>
