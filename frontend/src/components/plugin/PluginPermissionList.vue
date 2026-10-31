<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { pluginPermissionInfo } from './permissions'

// PluginPermissionList.vue presents requested capabilities for administrator review.
// It uses distinct native icons, localized scope descriptions, and financial-risk
// emphasis, while updates can mark newly requested grants. Rendering this list
// never confirms permissions; the installation controller owns the explicit acceptance action.

// Props supplies declared capabilities and the optional subset introduced by an
// update; the parent determines that subset against the currently accepted installation.
withDefaults(
  defineProps<{
    permissions: string[] // Ordered requested capability names.
    newPermissions?: string[] // Newly requested update grants.
  }>(),
  { newPermissions: () => [] },
)
const { t } = useI18n()
</script>

<template>
  <ul class="space-y-3">
    <li v-for="permission in permissions" :key="permission" class="flex items-start gap-3">
      <Icon
        :name="pluginPermissionInfo(permission).icon"
        size="sm"
        class="mt-0.5 shrink-0"
        :class="
          pluginPermissionInfo(permission).highRisk
            ? 'text-red-600 dark:text-red-400'
            : 'text-gray-500 dark:text-dark-400'
        "
      />
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-2">
          <span
            class="text-sm font-medium"
            :class="pluginPermissionInfo(permission).highRisk && 'text-red-600 dark:text-red-400'"
            >{{ t(`admin.plugins.permissions.${pluginPermissionInfo(permission).key}.name`) }}</span
          >
          <span v-if="newPermissions.includes(permission)" class="badge badge-primary">{{
            t('admin.plugins.newPermission')
          }}</span>
        </div>
        <p class="text-xs text-gray-500 dark:text-dark-400">{{
          t(`admin.plugins.permissions.${pluginPermissionInfo(permission).key}.description`)
        }}</p>
        <code class="text-xs text-gray-500 dark:text-dark-400">{{ permission }}</code>
      </div>
    </li>
  </ul>
</template>
