<template>
  <!-- 品牌信息与页面卡片随认证步骤更新，背景外壳由 AuthShell 常驻。 -->
  <div class="mb-8 text-center">
    <template v-if="settingsLoaded">
      <div
        class="mb-4 inline-flex h-16 w-16 items-center justify-center overflow-hidden rounded-2xl shadow-lg shadow-primary-500/30"
      >
        <img :src="siteLogo || '/logo.svg'" alt="Logo" class="h-full w-full object-contain" />
      </div>
      <h1 class="mb-2 text-3xl font-bold text-black dark:text-white">
        {{ siteName }}
      </h1>
      <p class="text-sm text-gray-500 dark:text-dark-400">
        {{ siteSubtitle }}
      </p>
    </template>
  </div>

  <div class="card-glass rounded-2xl p-8 shadow-glass">
    <slot />
  </div>

  <div class="mt-6 text-center text-sm">
    <slot name="footer" />
  </div>

  <div class="mt-8 text-center text-xs text-gray-400 dark:text-dark-500">
    &copy; {{ currentYear }} {{ siteName }}. All rights reserved.
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'

const appStore = useAppStore()
const { locale } = useI18n()

const siteName = computed(() => appStore.siteName || 'Sub2API')
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteSubtitle = computed(() => {
  const settings = appStore.cachedPublicSettings
  const isZh = String(locale.value).toLowerCase().startsWith('zh')
  const primary = isZh ? settings?.site_subtitle_zh : settings?.site_subtitle_en
  const secondary = isZh ? settings?.site_subtitle_en : settings?.site_subtitle_zh
  return firstConfiguredText(primary, secondary, settings?.site_subtitle, 'Subscription to API Conversion Platform')
})
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)

const currentYear = computed(() => new Date().getFullYear())

function firstConfiguredText(...values: Array<string | undefined>): string {
  for (const value of values) {
    const normalized = value?.trim()
    if (normalized) {
      return normalized
    }
  }
  return ''
}

onMounted(() => {
  appStore.fetchPublicSettings()
})
</script>
