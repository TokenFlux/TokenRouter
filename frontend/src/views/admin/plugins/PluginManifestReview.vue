<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginManifest } from '@/api/plugins.types'
import PluginPermissionList from '@/components/plugin/PluginPermissionList.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'

// PluginManifestReview.vue displays accepted package metadata before installation.
// Publisher text, requested capabilities, and declared network hosts are presented
// together so the administrator can review the exact release being selected.
// Links use the native URL sanitizer, and this component does not grant permissions
// or execute any code while rendering the preview.

// Props identifies the reviewed release and optional archive metadata; newly
// requested grants are supplied by the parent after comparison with installed permissions.
const props = withDefaults(
  defineProps<{
    manifest: PluginManifest // Accepted release manifest declaration.
    iconUrl?: string // Optional remote catalog presentation icon.
    digest?: string // Original manifest SHA-256 digest.
    platform?: string // Validated package target platform.
    newPermissions?: string[] // Newly requested update capabilities.
  }>(),
  { newPermissions: () => [] },
)
const { t, locale } = useI18n()
const iconFailed = ref(false)
const imageURL = computed(() =>
  !iconFailed.value ? sanitizeUrl(props.iconUrl ?? '', { allowRelative: false }) : '',
)
watch(
  () => props.iconUrl,
  () => {
    iconFailed.value = false
  },
)
const publisherURL = computed(() =>
  sanitizeUrl(props.manifest.publisher.url || '', { allowRelative: false }),
)
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-start gap-3">
      <img
        v-if="imageURL"
        :src="imageURL"
        alt=""
        class="h-12 w-12 shrink-0 rounded-control bg-white object-contain p-1"
        referrerpolicy="no-referrer"
        @error="iconFailed = true"
      />
      <Icon v-else name="puzzle" size="lg" class="mt-1 shrink-0 text-gray-500 dark:text-dark-400" />
      <div class="min-w-0">
        <h3 class="text-base font-semibold"
          >{{ pluginText(manifest.name, locale, manifest.id) }}
          <span class="ml-2 text-sm font-normal text-gray-500 dark:text-dark-400"
            >v{{ manifest.version }}</span
          ></h3
        >
        <p class="break-all font-mono text-xs text-gray-500 dark:text-dark-400">{{
          manifest.id
        }}</p>
        <p class="mt-2 text-sm text-gray-600 dark:text-dark-300">{{
          pluginText(manifest.description, locale)
        }}</p>
      </div>
    </div>
    <dl class="grid gap-3 text-sm sm:grid-cols-2">
      <div
        ><dt class="text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.publisher')
        }}</dt
        ><dd
          ><a
            v-if="publisherURL"
            :href="publisherURL"
            target="_blank"
            rel="noopener noreferrer"
            class="text-primary-600 hover:underline"
            >{{ manifest.publisher.name }}</a
          ><span v-else>{{ manifest.publisher.name }}</span></dd
        ></div
      >
      <div
        ><dt class="text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.license')
        }}</dt
        ><dd>{{ manifest.license }}</dd></div
      >
      <div v-if="platform"
        ><dt class="text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.platform')
        }}</dt
        ><dd class="font-mono">{{ platform }}</dd></div
      >
      <div v-if="digest" class="sm:col-span-2"
        ><dt class="text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.manifestDigest')
        }}</dt
        ><dd class="break-all font-mono text-xs">{{ digest }}</dd></div
      >
    </dl>
    <SettingsSection :title="t('admin.plugins.review.permissions')">
      <PluginPermissionList
        v-if="manifest.permissions?.length"
        :permissions="manifest.permissions"
        :new-permissions="newPermissions"
      />
      <p v-else class="text-sm text-gray-500 dark:text-dark-400">{{
        t('admin.plugins.review.noPermissions')
      }}</p>
    </SettingsSection>
    <SettingsSection
      v-if="manifest.network?.length"
      :title="t('admin.plugins.review.network')"
      :hint="t('admin.plugins.review.networkHint')"
    >
      <ul class="space-y-1 text-sm"
        ><li v-for="host in manifest.network" :key="host" class="break-all font-mono">{{
          host
        }}</li></ul
      >
    </SettingsSection>
  </div>
</template>
