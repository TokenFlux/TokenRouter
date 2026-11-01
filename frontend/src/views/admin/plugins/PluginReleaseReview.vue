<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PluginManifest } from '@/api/plugins.types'
import Disclosure from '@/components/common/Disclosure.vue'
import Icon from '@/components/icons/Icon.vue'
import PluginPermissionList from '@/components/plugin/PluginPermissionList.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import PluginIcon from './PluginIcon.vue'
import PluginTrustMark from './PluginTrustMark.vue'
import type { PluginTrust } from './pluginPresentation'

// PluginReleaseReview.vue summarizes a release before an administrator installs or
// updates it from a catalog source or an uploaded package. It shows who publishes the
// code, where it came from, whether this host can run it, and what it may access.
// For updates the newly requested permissions come first and the complete list is
// folded away, because the new grants are what the administrator has not yet
// accepted. Declared network hosts are shown as declarations; the host does not
// enforce them. Acceptance controls and trust warnings belong to the owning dialog.

// Props describes the candidate release and the context it is reviewed in.
const props = defineProps<{
  manifest: PluginManifest // Candidate release manifest.
  iconSrc: string // Resolved icon URL or empty string.
  trust: PluginTrust // Catalog signature or local delivery path.
  sourceName?: string // Catalog source name for the trust tooltip.
  platform?: string // Host platform the release is checked against.
  platformAvailable?: boolean // Whether an archive exists for that platform.
  digest?: string // Manifest or package SHA-256 shown for verification.

  /**
   * newPermissions lists grants an update adds to the installed release; it is
   * omitted for new installations, which review the complete permission list.
   */
  newPermissions?: string[]
}>()
const { t, locale } = useI18n()

// permissions lists every requested grant; isUpdate reports whether an installed
// release is being replaced, which switches the list to new grants first.
const permissions = computed(() => props.manifest.permissions ?? [])
const isUpdate = computed(() => props.newPermissions !== undefined)
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start gap-3">
      <PluginIcon :src="iconSrc" :size="48" />
      <div class="min-w-0 flex-1">
        <p class="flex flex-wrap items-baseline gap-x-2">
          <span class="text-base font-semibold text-gray-900 dark:text-dark-50">{{
            pluginText(manifest.name, locale, manifest.id)
          }}</span>
          <span class="font-mono text-sm text-gray-500 dark:text-dark-400"
            >v{{ manifest.version }}</span
          >
        </p>
        <p
          class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-gray-500 dark:text-dark-400"
        >
          <span>{{ manifest.publisher.name }}</span>
          <span aria-hidden="true" class="text-gray-300 dark:text-dark-600">·</span>
          <PluginTrustMark :trust="trust" label :detail="sourceName" />
          <span aria-hidden="true" class="text-gray-300 dark:text-dark-600">·</span>
          <span class="font-mono">{{ manifest.id }}</span>
        </p>
        <p class="mt-2 text-sm text-gray-600 dark:text-dark-300">{{
          pluginText(manifest.description, locale)
        }}</p>
      </div>
    </div>

    <dl
      v-if="platform || digest"
      class="grid gap-3 rounded-surface border border-gray-100 bg-gray-50/70 p-4 text-sm dark:border-dark-700 dark:bg-dark-950 sm:grid-cols-2"
    >
      <div v-if="platform">
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.platform')
        }}</dt>
        <dd
          class="mt-0.5 flex items-center gap-1.5 font-mono"
          :class="
            platformAvailable === false
              ? 'text-red-600 dark:text-red-400'
              : 'text-gray-900 dark:text-dark-50'
          "
        >
          <Icon
            :name="platformAvailable === false ? 'x' : 'check'"
            size="sm"
            :class="platformAvailable !== false && 'text-emerald-600 dark:text-emerald-400'"
            :animate-on-hover="false"
          />{{ platform }}
        </dd>
      </div>
      <div v-if="digest" class="min-w-0">
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{
          t('admin.plugins.review.manifestDigest')
        }}</dt>
        <dd class="mt-0.5 truncate font-mono text-gray-900 dark:text-dark-50" :title="digest">{{
          digest
        }}</dd>
      </div>
    </dl>

    <section>
      <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-dark-50">{{
        isUpdate && newPermissions?.length
          ? t('admin.plugins.review.newPermissions', { count: newPermissions.length })
          : t('admin.plugins.review.permissions')
      }}</h4>
      <p
        v-if="!permissions.length"
        class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-400"
      >
        <Icon name="checkCircle" size="sm" class="text-emerald-600 dark:text-emerald-400" />{{
          t('admin.plugins.review.noPermissions')
        }}
      </p>
      <template v-else-if="isUpdate">
        <PluginPermissionList
          v-if="newPermissions?.length"
          :permissions="newPermissions"
          :new-permissions="newPermissions"
        />
        <p v-else class="flex items-center gap-2 text-sm text-gray-500 dark:text-dark-400">
          <Icon name="checkCircle" size="sm" class="text-emerald-600 dark:text-emerald-400" />{{
            t('admin.plugins.review.noNewPermissions')
          }}
        </p>
        <Disclosure
          class="mt-3"
          summary-class="inline-flex items-center gap-1 text-xs font-medium text-gray-500 hover:text-gray-900 dark:text-dark-400 dark:hover:text-dark-50"
        >
          <template #summary>{{
            t('admin.plugins.review.allPermissions', { count: permissions.length })
          }}</template>
          <PluginPermissionList
            class="mt-3"
            :permissions="permissions"
            :new-permissions="newPermissions"
          />
        </Disclosure>
      </template>
      <PluginPermissionList v-else :permissions="permissions" />
    </section>

    <section v-if="manifest.network?.length">
      <h4 class="text-sm font-semibold text-gray-900 dark:text-dark-50">{{
        t('admin.plugins.review.network')
      }}</h4>
      <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{
        t('admin.plugins.review.networkHint')
      }}</p>
      <ul class="mt-2 flex flex-wrap gap-2">
        <li
          v-for="host in manifest.network"
          :key="host"
          class="rounded-compact bg-gray-100 px-2 py-1 font-mono text-xs text-gray-700 dark:bg-dark-800 dark:text-dark-200"
          >{{ host }}</li
        >
      </ul>
    </section>
  </div>
</template>
