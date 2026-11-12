<template>
  <article class="flex min-h-0 flex-1 flex-col">
    <header class="flex shrink-0 items-start gap-3 px-6 pt-6">
      <div class="min-w-0 flex-1">
        <h2
          class="break-words text-lg font-semibold leading-7 text-gray-900 dark:text-white sm:text-xl"
        >
          {{ announcement.title }}
        </h2>
        <time
          :datetime="announcement.created_at"
          class="mt-1 block text-xs tabular-nums text-gray-500 dark:text-dark-400"
        >
          {{ formatDateTimeToMinute(announcement.created_at) }}
        </time>
      </div>
      <!-- Controls that belong beside the title, such as a dialog close button. -->
      <slot name="actions"></slot>
    </header>

    <!-- The body scrolls on its own so long content leaves the heading in view. -->
    <div class="announcement-article-body min-h-0 flex-1 overflow-y-auto px-6 pb-6 pt-4">
      <div class="markdown-body text-link-content" v-html="renderedContent"></div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { formatDateTimeToMinute } from '@/utils/format'
import {
  renderAnnouncementHtml,
  type DisplayedAnnouncement,
} from '@/components/announcements/announcementDisplay'
import '@/styles/announcement-markdown.css'

// AnnouncementArticle.vue renders one announcement as a heading, its publication time,
// and a scrolling sanitized Markdown body. The header list dialog and the standalone
// popup both show announcement details, so they share this layout and differ only in
// the surrounding shell and the controls passed to the actions slot. The article fills
// its flex column parent, which must bound the height for the body to scroll.

// props carries the announcement shown by the article.
const props = defineProps<{
  announcement: DisplayedAnnouncement // Title, Markdown content, and publication time.
}>()

// renderedContent holds the sanitized HTML for the current announcement content.
const renderedContent = computed(() => renderAnnouncementHtml(props.announcement.content))
</script>

<style scoped>
/*
 * The body fades at its top and bottom edges, so scrolled content dissolves next to
 * the fixed heading and a footer below it. Each fade spans the body padding on that
 * side, which leaves content at either end of the scroll range fully opaque.
 */
.announcement-article-body {
  mask-image: linear-gradient(
    to bottom,
    transparent,
    black 1rem,
    black calc(100% - 1.5rem),
    transparent
  );
}
</style>
