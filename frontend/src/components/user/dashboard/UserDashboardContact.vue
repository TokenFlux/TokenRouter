<template>
  <div v-if="contactEntries.length" class="card flex flex-col" data-testid="dashboard-contact">
    <!-- Match the quick-action heading so the stacked sidebar cards align. -->
    <div
      class="flex min-h-[61px] items-center border-b border-gray-100 px-6 py-4 dark:border-dark-700"
    >
      <h2 class="truncate text-lg font-semibold text-gray-900 dark:text-dark-50">
        {{ t('common.contactSupport') }}
      </h2>
    </div>
    <div class="space-y-2 p-4">
      <template v-for="(entry, idx) in contactEntries" :key="idx">
        <a
          v-if="entry.url"
          :href="entry.url"
          target="_blank"
          rel="noopener noreferrer"
          class="group text-link-area flex w-full items-center gap-3 rounded-control bg-gray-50 p-3 text-left transition duration-normal hover:bg-gray-100 dark:bg-dark-800/50 dark:hover:bg-dark-800"
          :title="entry.value"
          data-testid="dashboard-contact-link"
        >
          <div
            class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-control bg-primary-100 transition-transform group-hover:scale-105 dark:bg-primary-900/30"
          >
            <Icon name="link" size="md" class="text-primary-600 dark:text-primary-400" />
          </div>
          <div class="min-w-0 flex-1">
            <p class="text-link truncate text-sm font-medium">
              {{ entry.label || formatContactUrl(entry.url) }}
            </p>
            <p v-if="entry.label" class="truncate text-xs text-gray-500 dark:text-dark-400">
              {{ formatContactUrl(entry.url) }}
            </p>
          </div>
          <Icon
            name="externalLink"
            size="sm"
            class="shrink-0 text-gray-400 transition duration-fast group-hover:text-primary-500 dark:text-dark-500"
          />
        </a>
        <button
          v-else
          type="button"
          class="group flex w-full items-center gap-3 rounded-control bg-gray-50 p-3 text-left transition duration-normal hover:bg-gray-100 dark:bg-dark-800/50 dark:hover:bg-dark-800"
          :title="t('common.copy')"
          data-testid="dashboard-contact-copy"
          @click="copyToClipboard(entry.value)"
        >
          <div
            class="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-control bg-primary-100 transition-transform group-hover:scale-105 dark:bg-primary-900/30"
          >
            <Icon name="chat" size="md" class="text-primary-600 dark:text-primary-400" />
          </div>
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-medium text-gray-900 dark:text-dark-50">
              {{ entry.label || entry.value }}
            </p>
            <p
              v-if="entry.label"
              class="truncate text-xs tabular-nums text-gray-500 dark:text-dark-400"
            >
              {{ entry.value }}
            </p>
          </div>
          <Icon
            name="copy"
            size="sm"
            class="shrink-0 text-gray-400 transition duration-fast group-hover:text-primary-500 dark:text-dark-500"
          />
        </button>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores/app'
import { formatContactUrl, parseContactInfo } from '@/utils/contactInfo'

// UserDashboardContact.vue lists the site's configured contact details on the user
// dashboard, next to the quick actions. It reads the same public contact setting as the
// header Contact Us menu through the shared contactInfo parser, so link entries open a
// new tab and other values copy to the clipboard. The card renders nothing when the
// setting is empty, leaving the dashboard column without an empty placeholder.

// Translation, public contact settings, and clipboard feedback supply the card labels
// and row actions.
const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

// contactEntries holds the parsed rows; an empty list removes the card from the page.
const contactEntries = computed(() => parseContactInfo(appStore.contactInfo))
</script>
