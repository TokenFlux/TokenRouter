<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'

// EmailTemplateSkeleton.vue reserves the template source and preview columns.
// When the template index is pending, it also reserves the two selectors that
// determine the event and locale displayed by the editor.

// The selector flag indicates that the event and locale index is still pending.
defineProps<{ showSelectors?: boolean /* Include event and locale selectors. */ }>()

// The translator supplies editor labels and the accessibility status for loading.
const { t } = useI18n()
</script>

<template>
  <div
    class="space-y-6"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-loading-skeleton
  >
    <div v-if="showSelectors" class="grid gap-4 md:grid-cols-2" aria-hidden="true">
      <div v-for="key in ['event', 'locale']" :key="key">
        <p class="input-label">{{ t(`admin.settings.emailTemplates.${key}`) }}</p>
        <Skeleton :height="36" class="rounded-control" />
      </div>
    </div>
    <div class="grid grid-cols-1 gap-6 xl:grid-cols-2" aria-hidden="true">
      <div class="space-y-4">
        <div>
          <p class="input-label">{{ t('admin.settings.emailTemplates.subject') }}</p>
          <Skeleton :height="36" class="rounded-control" />
        </div>
        <div>
          <p class="input-label">{{ t('admin.settings.emailTemplates.html') }}</p>
          <Skeleton height="28rem" class="rounded-control" />
        </div>
      </div>
      <div class="self-start rounded-control border border-gray-200 dark:border-dark-700">
        <p class="border-b border-gray-100 px-4 py-3 text-sm font-medium dark:border-dark-700">
          {{ t('admin.settings.emailTemplates.livePreview') }}
        </p>
        <div class="p-3"><Skeleton height="36rem" class="rounded-control" /></div>
      </div>
    </div>
  </div>
</template>
