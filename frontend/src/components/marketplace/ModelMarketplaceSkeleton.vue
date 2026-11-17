<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'

// ModelMarketplaceSkeleton.vue supplies loading placeholders for marketplace
// groups and model cards. Its grid follows the loaded page's responsive
// breakpoints, while one translated status label exposes loading to assistive
// technology and decorative placeholder content remains hidden.

// The localized status label names the whole loading region; its repeated
// visual skeleton cards are decorative rather than independently announced content.
const { t } = useI18n()
</script>

<template>
  <!-- Match the loaded group and model grid breakpoints to limit layout movement when data arrives. -->
  <div
    class="space-y-4"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-testid="marketplace-skeleton"
  >
    <section v-for="group in 2" :key="group" class="card overflow-hidden" aria-hidden="true">
      <div
        class="card-header flex flex-col gap-4 px-4 py-4 md:px-5 xl:flex-row xl:items-center xl:justify-between"
      >
        <div class="min-w-0 flex-1 space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <Skeleton :width="86" :height="26" class="rounded-full" />
            <Skeleton :width="64" :height="26" class="rounded-full" />
          </div>
          <div class="flex items-start gap-3">
            <Skeleton :width="48" :height="48" class="mt-0.5 shrink-0 rounded-surface" />
            <div class="min-w-0 flex-1 space-y-2 py-1">
              <Skeleton :width="144" :height="20" class="max-w-full" />
              <Skeleton :width="240" :height="14" class="max-w-full" />
            </div>
          </div>
        </div>
        <div class="flex w-full min-w-0 items-center gap-2 xl:ml-6 xl:w-[560px] xl:shrink-0">
          <div class="grid h-8 min-w-0 flex-1 grid-cols-7 items-center gap-0.5">
            <Skeleton
              v-for="bucket in 7"
              :key="bucket"
              :width="8"
              :height="24"
              class="max-w-full justify-self-center"
            />
          </div>
          <div class="w-24 shrink-0">
            <Skeleton :width="64" :height="16" />
          </div>
        </div>
      </div>
      <div
        class="grid min-w-0 grid-cols-1 items-start gap-4 p-4 md:grid-cols-2 lg:grid-cols-3 md:p-5"
      >
        <div
          v-for="model in 3"
          :key="model"
          class="min-w-0 rounded-surface border border-gray-100 bg-gray-50/80 p-4 dark:border-dark-700 dark:bg-dark-950/80"
        >
          <div class="flex items-center justify-between gap-2">
            <Skeleton width="45%" :height="20" />
            <div class="flex gap-1">
              <Skeleton v-for="tag in 3" :key="tag" :width="20" :height="20" />
            </div>
          </div>
          <div class="mt-1 flex items-center gap-1.5">
            <Skeleton :width="24" :height="18" />
            <Skeleton :width="62" :height="12" />
            <Skeleton :width="12" :height="12" />
          </div>
          <div class="mt-4 space-y-2">
            <div
              v-for="quote in 2"
              :key="quote"
              class="flex min-h-5 items-center justify-between gap-3"
            >
              <Skeleton :width="68" :height="14" />
              <Skeleton :width="86" :height="14" />
            </div>
          </div>
          <div
            class="mt-3 flex min-h-8 items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-700"
          >
            <Skeleton :width="144" :height="16" />
            <Skeleton :width="12" :height="12" />
          </div>
        </div>
      </div>
    </section>
  </div>
</template>
