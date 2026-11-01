<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { PERMISSION_TONE_TILE, pluginPermissionInfo } from './permissions'

// PluginPermissionList.vue presents requested capabilities for administrator review.
// Capabilities are grouped by risk: financial and entitlement writes, together with
// unrecognized names, come first under their own heading so a reviewer reads them
// before routine read and notification access. Each item pairs an icon tile, tinted
// by what the capability does, with localized copy and the raw permission name, and
// updates can mark newly requested grants. Rendering this list never confirms
// permissions; the installation controller owns the explicit acceptance action.

// Props supplies declared capabilities and the optional subset introduced by an
// update; the parent determines that subset against the currently accepted installation.
const props = withDefaults(
  defineProps<{
    permissions: string[] // Ordered requested capability names.
    newPermissions?: string[] // Newly requested update grants.
  }>(),
  { newPermissions: () => [] },
)
const { t } = useI18n()

// groups splits the declared order into high-risk and routine sections, dropping
// an empty section so a read-only plugin shows a single unlabeled list.
const groups = computed(() =>
  [
    {
      key: 'highRisk',
      items: props.permissions.filter((item) => pluginPermissionInfo(item).highRisk),
    },
    {
      key: 'standard',
      items: props.permissions.filter((item) => !pluginPermissionInfo(item).highRisk),
    },
  ].filter((group) => group.items.length),
)
</script>

<template>
  <div class="space-y-5">
    <section v-for="group in groups" :key="group.key">
      <h4
        v-if="groups.length > 1"
        class="mb-3 text-xs font-medium text-gray-500 dark:text-dark-400"
        >{{ t(`admin.plugins.permissionGroups.${group.key}`) }}</h4
      >
      <ul class="space-y-3">
        <li v-for="permission in group.items" :key="permission" class="flex items-start gap-3">
          <span
            class="flex h-8 w-8 shrink-0 items-center justify-center rounded-control"
            :class="PERMISSION_TONE_TILE[pluginPermissionInfo(permission).tone]"
          >
            <Icon
              :name="pluginPermissionInfo(permission).icon"
              size="sm"
              :animate-on-hover="false"
            />
          </span>
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-gray-900 dark:text-dark-50">{{
                t(`admin.plugins.permissions.${pluginPermissionInfo(permission).key}.name`)
              }}</span>
              <span v-if="newPermissions.includes(permission)" class="badge badge-primary">{{
                t('admin.plugins.newPermission')
              }}</span>
            </div>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{
              t(`admin.plugins.permissions.${pluginPermissionInfo(permission).key}.description`)
            }}</p>
          </div>
          <code
            class="hidden shrink-0 rounded-compact bg-gray-100 px-1.5 py-0.5 font-mono text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400 sm:inline"
            >{{ permission }}</code
          >
        </li>
      </ul>
    </section>
  </div>
</template>
