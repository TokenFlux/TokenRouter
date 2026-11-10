<template>
  <div
    class="relative flex min-h-screen items-center justify-center overflow-hidden bg-[var(--page-bg)] px-4"
  >
    <div v-content-reveal="motionRoute?.path" class="relative z-10 w-full max-w-md text-center">
      <div class="mb-8">
        <div class="relative inline-block">
          <span class="text-[12rem] font-bold leading-none text-gray-100 dark:text-dark-800"
            >404</span
          >
          <div class="absolute inset-0 flex items-center justify-center">
            <div
              class="flex h-24 w-24 items-center justify-center rounded-surface bg-gradient-to-br from-primary-500 to-primary-600 shadow-lg shadow-primary-500/30"
            >
              <Icon name="exclamationTriangle" size="md" class="h-12 w-12 text-white" />
            </div>
          </div>
        </div>
      </div>

      <div class="mb-8">
        <h1 class="mb-3 text-2xl font-bold text-gray-900 dark:text-white">
          {{ t('errors.pageNotFound') }}
        </h1>
        <p class="text-gray-500 dark:text-dark-400">
          {{ t('notFoundPage.description') }}
        </p>
      </div>

      <div class="flex flex-col justify-center gap-3 sm:flex-row">
        <button @click="goBack" class="btn btn-secondary">
          <Icon name="arrowLeft" size="sm" class="mr-2" />
          {{ t('notFoundPage.back') }}
        </button>
        <router-link to="/dashboard" class="btn btn-primary">
          <Icon name="home" size="sm" class="mr-2" />
          {{ t('notFoundPage.dashboard') }}
        </router-link>
      </div>

      <p class="mt-8 text-sm text-gray-400 dark:text-dark-500">
        {{ t('notFoundPage.help') }}
        <a href="#" class="text-link">
          {{ t('notFoundPage.support') }}
        </a>
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { vContentReveal } from '@/directives/contentReveal'
import { useRoute as useMotionRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'

// NotFoundView.vue renders the missing-page message on a plain, full-height
// background. Unmatched routes offer browser-history navigation and a dashboard
// link, while the central content uses the shared route-driven reveal animation.

// Route state supplies the content-reveal trigger, while translation and router
// services provide the missing-page labels and browser-history action for this view.
const motionRoute = useMotionRoute()
const { t } = useI18n()
const router = useRouter()

// goBack requests the previous browser-history entry through the active router.
function goBack(): void {
  router.back()
}
</script>
